package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"thing-connect/internal/captcha"
	"thing-connect/internal/model"
)

// ---- fakes ----

type fakeUserStore struct {
	user         *model.User
	lookupEmail  string
	createdEmail string
	createID     int64
	createErr    error
	allocateErr  error
	quota        int
	deviceRows   []model.UserDeviceRow
	updateName   bool
	updateErr    error
	password     string
	updatePwdErr error
}

func (f *fakeUserStore) GetUserByEmail(_ context.Context, email string) (*model.User, error) {
	f.lookupEmail = email
	return f.user, nil
}
func (f *fakeUserStore) CreateUser(_ context.Context, email, _ string) (int64, error) {
	f.createdEmail = email
	return f.createID, f.createErr
}
func (f *fakeUserStore) UpdatePassword(_ context.Context, _ int64, passwordHash string) error {
	f.password = passwordHash
	return f.updatePwdErr
}
func (f *fakeUserStore) AllocateDevices(_ context.Context, _ int64, _ int) error {
	return f.allocateErr
}
func (f *fakeUserStore) GetQuota(_ context.Context, _ int64) (int, error) {
	return f.quota, nil
}
func (f *fakeUserStore) GetDeviceList(_ context.Context, _ int64) ([]model.UserDeviceRow, error) {
	return f.deviceRows, nil
}
func (f *fakeUserStore) UpdateDeviceName(_ context.Context, _ int64, _, _ string) (bool, error) {
	return f.updateName, f.updateErr
}

type fakeCacheStore struct {
	deviceCodes           map[string]string // code → physical hash
	emailCodes            map[string]string // email → code
	passwordResetAttempts map[string]int64
}

func newFakeCache() *fakeCacheStore {
	return &fakeCacheStore{
		deviceCodes: map[string]string{}, emailCodes: map[string]string{}, passwordResetAttempts: map[string]int64{},
	}
}

func (f *fakeCacheStore) IncrReportAttempt(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 1, nil
}
func (f *fakeCacheStore) SetReportReplay(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}
func (f *fakeCacheStore) GetReportReplay(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}
func (f *fakeCacheStore) SetVerifyRecord(_ context.Context, _ string, _ []byte, _ time.Duration) (bool, error) {
	return true, nil
}
func (f *fakeCacheStore) GetVerifyRecord(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}
func (f *fakeCacheStore) ReserveDeviceCode(_ context.Context, code, physHash string, _ time.Duration) (bool, error) {
	if _, exists := f.deviceCodes[code]; exists {
		return false, nil
	}
	f.deviceCodes[code] = physHash
	return true, nil
}
func (f *fakeCacheStore) GetDeviceCodeLookup(_ context.Context, code string) (string, error) {
	return f.deviceCodes[code], nil
}
func (f *fakeCacheStore) DelDeviceCodeLookup(_ context.Context, code string) error {
	delete(f.deviceCodes, code)
	return nil
}
func (f *fakeCacheStore) SetEmailCode(_ context.Context, email, code string, _ time.Duration) error {
	f.emailCodes[email] = code
	return nil
}
func (f *fakeCacheStore) GetEmailCode(_ context.Context, email string) (string, error) {
	return f.emailCodes[email], nil
}
func (f *fakeCacheStore) ConsumeEmailCode(_ context.Context, email, code string) (bool, error) {
	if f.emailCodes[email] != code {
		return false, nil
	}
	delete(f.emailCodes, email)
	return true, nil
}
func (f *fakeCacheStore) IsDeviceOnline(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (f *fakeCacheStore) IsInCall(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (f *fakeCacheStore) DelVerifyAndCode(_ context.Context, physHash, code string) error {
	delete(f.deviceCodes, code)
	delete(f.deviceCodes, physHash)
	return nil
}
func (f *fakeCacheStore) DelEmailCode(_ context.Context, email string) error {
	delete(f.emailCodes, email)
	return nil
}
func (f *fakeCacheStore) IncrPasswordResetAttempt(_ context.Context, scope string, _ time.Duration) (int64, error) {
	f.passwordResetAttempts[scope]++
	return f.passwordResetAttempts[scope], nil
}
func (f *fakeCacheStore) SetNonce(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

type fakeCaptcha struct{ fail bool }

func (f *fakeCaptcha) Verify(_ context.Context, _ captcha.CaptchaToken) error {
	if f.fail {
		return captcha.ErrVerifyFailed
	}
	return nil
}

type fakeMailer struct {
	sent  []string
	mails []fakeMail
}

type fakeMail struct {
	to      string
	subject string
	body    string
}

type fakePasswordResetEmailQueue struct {
	emails []string
	err    error
}

func (f *fakePasswordResetEmailQueue) Enqueue(_ context.Context, email string) error {
	f.emails = append(f.emails, email)
	return f.err
}

func (f *fakeMailer) Send(_ context.Context, to, subject, body string) error {
	f.sent = append(f.sent, to)
	f.mails = append(f.mails, fakeMail{to: to, subject: subject, body: body})
	return nil
}

// ---- helpers ----

func newSvc(us *fakeUserStore, cs *fakeCacheStore, capFail bool) *UserService {
	return NewUserService(us, cs, &fakeCaptcha{fail: capFail}, &fakeMailer{}, "secret", DefaultServiceConfig())
}

// ---- tests ----

func TestUserService_Register_DuplicateEmail(t *testing.T) {
	cache := newFakeCache()
	// pre-seed a valid code for a@b.com so the code check passes
	_ = cache.SetEmailCode(context.Background(), "a@b.com", "123456", time.Minute)
	svc := newSvc(&fakeUserStore{user: &model.User{ID: 1, Email: "a@b.com"}}, cache, false)
	_, _, err := svc.Register(context.Background(), "a@b.com", "pass1234", "123456")
	if !errors.Is(err, ErrUserExists) {
		t.Errorf("want ErrUserExists, got %v", err)
	}
}

func TestUserService_Register_InvalidCode(t *testing.T) {
	cache := newFakeCache() // empty — no code stored
	svc := newSvc(&fakeUserStore{user: nil, createID: 42}, cache, false)
	_, _, err := svc.Register(context.Background(), "new@b.com", "pass1234", "000000")
	if !errors.Is(err, ErrInvalidCode) {
		t.Errorf("want ErrInvalidCode, got %v", err)
	}
}

func TestUserService_Register_Success(t *testing.T) {
	cache := newFakeCache()
	_ = cache.SetEmailCode(context.Background(), "new@b.com", "123456", time.Minute)
	users := &fakeUserStore{user: nil, createID: 42}
	svc := newSvc(users, cache, false)
	tok, userID, err := svc.Register(context.Background(), " New@B.COM ", "pass1234", "123456")
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if userID != 42 {
		t.Errorf("want userID=42, got %d", userID)
	}
	if tok == "" {
		t.Error("want non-empty token")
	}
	if users.lookupEmail != "new@b.com" || users.createdEmail != "new@b.com" {
		t.Fatalf("register did not normalize email: lookup=%q create=%q", users.lookupEmail, users.createdEmail)
	}
}

func TestUserService_EmailCodeIsolatedFromDeviceCode(t *testing.T) {
	cache := newFakeCache()
	reserved, err := cache.ReserveDeviceCode(context.Background(), "123456", "physical-hash", time.Minute)
	if err != nil || !reserved {
		t.Fatalf("reserve device code: reserved=%v err=%v", reserved, err)
	}
	if err := cache.SetEmailCode(context.Background(), "new@b.com", "123456", time.Minute); err != nil {
		t.Fatal(err)
	}

	svc := newSvc(&fakeUserStore{createID: 42}, cache, false)
	if _, _, err := svc.Register(context.Background(), "new@b.com", "pass1234", "123456"); err != nil {
		t.Fatalf("register with same numeric code as a device should pass: %v", err)
	}
	if got, _ := cache.GetDeviceCodeLookup(context.Background(), "123456"); got != "physical-hash" {
		t.Fatalf("email code consumption changed device lookup: got %q", got)
	}
}

func TestUserService_Login_CaptchaFail(t *testing.T) {
	cache := newFakeCache()
	svc := newSvc(&fakeUserStore{user: &model.User{ID: 1, Password: "x"}}, cache, true)
	_, _, err := svc.Login(context.Background(), "a@b.com", "pass", captcha.CaptchaToken{})
	if !errors.Is(err, ErrCaptchaFailed) {
		t.Errorf("want ErrCaptchaFailed, got %v", err)
	}
}

func TestUserService_Login_WrongPassword(t *testing.T) {
	cache := newFakeCache()
	users := &fakeUserStore{user: &model.User{ID: 1, Password: "$2a$10$invalid-hash-placeholder"}}
	svc := newSvc(users, cache, false)
	_, _, err := svc.Login(context.Background(), " A@B.COM ", "wrong", captcha.CaptchaToken{})
	if !errors.Is(err, ErrInvalidCreds) {
		t.Errorf("want ErrInvalidCreds, got %v", err)
	}
	if users.lookupEmail != "a@b.com" {
		t.Errorf("login lookup email=%q, want a@b.com", users.lookupEmail)
	}
}

func TestUserService_SendCode_CaptchaFail(t *testing.T) {
	cache := newFakeCache()
	svc := newSvc(&fakeUserStore{}, cache, true)
	err := svc.SendCode(context.Background(), " A@B.COM ", captcha.CaptchaToken{})
	if !errors.Is(err, ErrCaptchaFailed) {
		t.Errorf("want ErrCaptchaFailed, got %v", err)
	}
}

func TestUserService_SendCode_Success(t *testing.T) {
	cache := newFakeCache()
	ml := &fakeMailer{}
	svc := NewUserService(&fakeUserStore{}, cache, &fakeCaptcha{}, ml, "secret", DefaultServiceConfig())
	err := svc.SendCode(context.Background(), " A@B.COM ", captcha.CaptchaToken{})
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if len(ml.sent) != 1 || ml.sent[0] != "a@b.com" {
		t.Errorf("want email sent to a@b.com, got %v", ml.sent)
	}
	mail := ml.mails[0]
	if mail.subject != "【TiRTC 体验平台】注册验证码" {
		t.Errorf("registration email subject=%q", mail.subject)
	}
	for _, want := range []string{"注册 TiRTC 体验平台账号", "来源：TiRTC 体验平台", "3 分 10 秒"} {
		if !strings.Contains(mail.body, want) {
			t.Errorf("registration email body missing %q: %q", want, mail.body)
		}
	}
}

func TestUserService_SendPasswordResetCode(t *testing.T) {
	cache := newFakeCache()
	mailer := &fakeMailer{}
	queue := &fakePasswordResetEmailQueue{}
	svc := NewUserService(
		&fakeUserStore{user: &model.User{ID: 7, Email: "a@b.com"}},
		cache, &fakeCaptcha{}, mailer, "secret", DefaultServiceConfig(),
	)
	svc.SetPasswordResetEmailQueue(queue)
	if err := svc.SendPasswordResetCode(context.Background(), " A@B.COM ", "127.0.0.1", captcha.CaptchaToken{}); err != nil {
		t.Fatalf("SendPasswordResetCode returned error: %v", err)
	}
	if len(queue.emails) != 1 || queue.emails[0] != "a@b.com" {
		t.Fatalf("want a queued reset email for a@b.com, got %v", queue.emails)
	}
	if cache.passwordResetAttempts["send:email:a@b.com"] != 1 {
		t.Fatalf("reset email rate-limit key was not normalized: %v", cache.passwordResetAttempts)
	}
	if len(mailer.sent) != 0 {
		t.Fatalf("reset request must not send email synchronously: %v", mailer.sent)
	}
	if err := svc.DeliverPasswordResetCode(context.Background(), " A@B.COM "); err != nil {
		t.Fatalf("DeliverPasswordResetCode returned error: %v", err)
	}
	mail := mailer.mails[0]
	if mail.subject != "【TiRTC 体验平台】找回密码验证码" {
		t.Errorf("password reset email subject=%q", mail.subject)
	}
	for _, want := range []string{"重置 TiRTC 体验平台账号密码", "来源：TiRTC 体验平台", "3 分 10 秒"} {
		if !strings.Contains(mail.body, want) {
			t.Errorf("password reset email body missing %q: %q", want, mail.body)
		}
	}
	if got, _ := cache.GetEmailCode(context.Background(), passwordResetCodeKey("a@b.com")); got == "" {
		t.Fatal("password-reset code was not stored")
	}
	if got, _ := cache.GetEmailCode(context.Background(), "a@b.com"); got != "" {
		t.Fatalf("password-reset code reused registration key: %q", got)
	}

	unknownMailer := &fakeMailer{}
	unknownSvc := NewUserService(&fakeUserStore{}, newFakeCache(), &fakeCaptcha{}, unknownMailer, "secret", DefaultServiceConfig())
	unknownQueue := &fakePasswordResetEmailQueue{}
	unknownSvc.SetPasswordResetEmailQueue(unknownQueue)
	if err := unknownSvc.SendPasswordResetCode(context.Background(), "missing@b.com", "127.0.0.1", captcha.CaptchaToken{}); err != nil {
		t.Fatalf("unknown email should still be queued: %v", err)
	}
	if len(unknownQueue.emails) != 1 {
		t.Fatalf("unknown email was not queued: %v", unknownQueue.emails)
	}
	if err := unknownSvc.DeliverPasswordResetCode(context.Background(), "missing@b.com"); err != nil {
		t.Fatalf("unknown email delivery returned error: %v", err)
	}
	if len(unknownMailer.sent) != 0 {
		t.Fatalf("unknown email sent a reset message: %v", unknownMailer.sent)
	}
}

func TestInMemoryPasswordResetEmailQueue(t *testing.T) {
	delivered := make(chan string, 1)
	queue := NewInMemoryPasswordResetEmailQueue(func(_ context.Context, email string) error {
		delivered <- email
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go queue.Run(ctx)
	if err := queue.Enqueue(context.Background(), "a@b.com"); err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	select {
	case got := <-delivered:
		if got != "a@b.com" {
			t.Errorf("delivered email=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("queued email was not delivered")
	}
}

func TestInMemoryPasswordResetEmailQueue_CoalescesSameEmail(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	delivered := make(chan string, 2)
	queue := NewInMemoryPasswordResetEmailQueue(func(_ context.Context, email string) error {
		started <- struct{}{}
		<-release
		delivered <- email
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go queue.Run(ctx)

	if err := queue.Enqueue(context.Background(), "a@b.com"); err != nil {
		t.Fatalf("first Enqueue returned error: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first job did not start")
	}
	if err := queue.Enqueue(context.Background(), "a@b.com"); err != nil {
		t.Fatalf("duplicate Enqueue returned error: %v", err)
	}
	close(release)
	select {
	case got := <-delivered:
		if got != "a@b.com" {
			t.Errorf("delivered email=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first job was not delivered")
	}
	select {
	case got := <-delivered:
		t.Fatalf("duplicate job was delivered: %q", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestUserService_SendPasswordResetCode_RateLimited(t *testing.T) {
	cache := newFakeCache()
	queue := &fakePasswordResetEmailQueue{}
	svc := NewUserService(&fakeUserStore{}, cache, &fakeCaptcha{}, &fakeMailer{}, "secret", DefaultServiceConfig())
	svc.SetPasswordResetEmailQueue(queue)

	if err := svc.SendPasswordResetCode(context.Background(), "a@b.com", "127.0.0.1", captcha.CaptchaToken{}); err != nil {
		t.Fatalf("first request returned error: %v", err)
	}
	if err := svc.SendPasswordResetCode(context.Background(), "a@b.com", "127.0.0.1", captcha.CaptchaToken{}); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("second request should be rate limited, got %v", err)
	}
	if len(queue.emails) != 1 {
		t.Fatalf("rate-limited request was queued: %v", queue.emails)
	}
}

func TestUserService_SendPasswordResetCode_IPRateLimited(t *testing.T) {
	cache := newFakeCache()
	cache.passwordResetAttempts["send:ip:127.0.0.1"] = passwordResetSendIPMaxAttempts
	queue := &fakePasswordResetEmailQueue{}
	svc := NewUserService(&fakeUserStore{}, cache, &fakeCaptcha{}, &fakeMailer{}, "secret", DefaultServiceConfig())
	svc.SetPasswordResetEmailQueue(queue)

	err := svc.SendPasswordResetCode(context.Background(), "a@b.com", "127.0.0.1", captcha.CaptchaToken{})
	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("request should be IP rate limited, got %v", err)
	}
	if len(queue.emails) != 0 {
		t.Fatalf("IP-rate-limited request was queued: %v", queue.emails)
	}
}

func TestUserService_ResetPassword_Success(t *testing.T) {
	cache := newFakeCache()
	user := &fakeUserStore{user: &model.User{ID: 7, Email: "a@b.com", Password: "old"}}
	_ = cache.SetEmailCode(context.Background(), passwordResetCodeKey("a@b.com"), "123456", time.Minute)
	svc := newSvc(user, cache, false)
	if err := svc.ResetPassword(context.Background(), " A@B.COM ", "new-pass", "123456", "127.0.0.1"); err != nil {
		t.Fatalf("ResetPassword returned error: %v", err)
	}
	if user.lookupEmail != "a@b.com" {
		t.Fatalf("reset lookup email=%q, want a@b.com", user.lookupEmail)
	}
	if user.password == "" || user.password == "new-pass" {
		t.Fatalf("password was not stored as a bcrypt hash: %q", user.password)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.password), []byte("new-pass")); err != nil {
		t.Fatalf("stored password does not match: %v", err)
	}
	if got, _ := cache.GetEmailCode(context.Background(), passwordResetCodeKey("a@b.com")); got != "" {
		t.Errorf("reset code was not consumed: %q", got)
	}
	if err := svc.ResetPassword(context.Background(), "a@b.com", "another-pass", "123456", "127.0.0.1"); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("consumed code should not be reusable, got %v", err)
	}
}

func TestUserService_ResetPassword_DoesNotAcceptRegistrationCode(t *testing.T) {
	cache := newFakeCache()
	_ = cache.SetEmailCode(context.Background(), "a@b.com", "123456", time.Minute)
	svc := newSvc(&fakeUserStore{user: &model.User{ID: 7}}, cache, false)
	if err := svc.ResetPassword(context.Background(), "a@b.com", "new-pass", "123456", "127.0.0.1"); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("want ErrInvalidCode, got %v", err)
	}
}

func TestUserService_ResetPassword_RateLimited(t *testing.T) {
	cache := newFakeCache()
	cache.passwordResetAttempts["email:a@b.com"] = passwordResetEmailMaxAttempts
	svc := newSvc(&fakeUserStore{user: &model.User{ID: 7}}, cache, false)
	if err := svc.ResetPassword(context.Background(), "a@b.com", "new-pass", "123456", "127.0.0.1"); !errors.Is(err, ErrRateLimit) {
		t.Errorf("want ErrRateLimit, got %v", err)
	}
}

func TestUserService_Quota(t *testing.T) {
	svc := newSvc(&fakeUserStore{quota: 7}, newFakeCache(), false)
	q, err := svc.Quota(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q != 7 {
		t.Errorf("want quota=7, got %d", q)
	}
}

func TestUserService_DeviceList_NormalizesNoVideoCapabilities(t *testing.T) {
	profiles := []string{
		`{"up_video_mt":" NoNe ","down_video_mt":"NONE","audio_rate":8000}`,
		`{"up_video_mt":"h264","down_video_mt":"mjpeg","no_video":true,"audio_rate":16000}`,
		`{"video_mt":"none","audio_rate":8000}`,
	}
	rows := make([]model.UserDeviceRow, 0, len(profiles))
	for i := range profiles {
		profile := profiles[i]
		rows = append(rows, model.UserDeviceRow{
			DeviceID:    "voice-device",
			DeviceName:  "书房学习机",
			Status:      1,
			VoipProfile: &profile,
		})
	}
	svc := newSvc(&fakeUserStore{deviceRows: rows}, newFakeCache(), false)

	devices, err := svc.DeviceList(context.Background(), 1, nil)
	if err != nil {
		t.Fatalf("DeviceList returned error: %v", err)
	}
	if len(devices) != len(profiles) {
		t.Fatalf("want %d devices, got %d", len(profiles), len(devices))
	}
	for i, device := range devices {
		if device.DeviceName != "书房学习机" {
			t.Errorf("device %d: device_name=%q", i, device.DeviceName)
		}
		if device.UpVideoMT != "" || device.DownVideoMT != "" {
			t.Errorf("device %d: want empty video formats, got up=%q down=%q", i, device.UpVideoMT, device.DownVideoMT)
		}
		if device.HasCamera || device.HasScreen {
			t.Errorf("device %d: want no video capabilities, got camera=%v screen=%v", i, device.HasCamera, device.HasScreen)
		}
		if device.VoipRoomType != "voice" {
			t.Errorf("device %d: want voice room, got %q", i, device.VoipRoomType)
		}
	}
}

func TestUserService_UpdateDeviceName(t *testing.T) {
	svc := newSvc(&fakeUserStore{updateName: true}, newFakeCache(), false)
	if err := svc.UpdateDeviceName(context.Background(), 7, "dev-1", "书房学习机"); err != nil {
		t.Fatalf("UpdateDeviceName returned error: %v", err)
	}

	svc = newSvc(&fakeUserStore{updateName: false}, newFakeCache(), false)
	if err := svc.UpdateDeviceName(context.Background(), 7, "foreign", "名称"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("UpdateDeviceName not-owned error=%v, want ErrDeviceNotFound", err)
	}
}

func TestParseVoipProfile_PreservesVideoCapabilities(t *testing.T) {
	profile := `{"up_video_mt":" h264 ","down_video_mt":"mjpeg","camera_rotation":270,` +
		`"aspect_ratio":1.7777777778,"hor_mirror":true,"vert_mirror":false,"object_fit":"contain"}`
	media := parseVoipProfile(&profile)
	if media.UpVideoMT != "h264" || media.DownVideoMT != "mjpeg" {
		t.Fatalf("want h264/mjpeg, got %q/%q", media.UpVideoMT, media.DownVideoMT)
	}
	if media.CameraRotation == nil || *media.CameraRotation != 270 {
		t.Fatalf("want camera rotation 270, got %#v", media.CameraRotation)
	}
	if media.AspectRatio == nil || *media.AspectRatio != 1.7777777778 ||
		media.HorMirror == nil || !*media.HorMirror ||
		media.VertMirror == nil || *media.VertMirror ||
		media.ObjectFit == nil || *media.ObjectFit != "contain" {
		t.Fatalf("unexpected video UI fields: %+v", media)
	}
	if got := voipRoomType(media.UpVideoMT, media.DownVideoMT); got != "video" {
		t.Fatalf("want video room, got %q", got)
	}
}

func TestParseVoipProfile_InvalidVideoUIFieldsAreOmitted(t *testing.T) {
	profile := `{"up_video_mt":"h264","camera_rotation":45,"aspect_ratio":0,"object_fit":"cover"}`
	media := parseVoipProfile(&profile)
	if media.CameraRotation != nil || media.AspectRatio != nil || media.ObjectFit != nil {
		t.Fatalf("invalid video UI fields were preserved: %+v", media)
	}
}

func (f *fakeCacheStore) SetPendingBind(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}
func (f *fakeCacheStore) GetPendingBind(_ context.Context, _ string) (string, error) { return "", nil }
func (f *fakeCacheStore) DelPendingBind(_ context.Context, _ string) error           { return nil }

func (f *fakeCacheStore) AddIPFingerprint(_ context.Context, _, _ string, _ time.Duration) (bool, int64, error) {
	return true, 1, nil
}
func (f *fakeCacheStore) IncrGlobalPending(_ context.Context) (int64, error) { return 1, nil }
func (f *fakeCacheStore) DecrGlobalPending(_ context.Context) error          { return nil }
func (f *fakeCacheStore) ReconcileGlobalPending(_ context.Context) error     { return nil }

func (f *fakeCacheStore) SetReportFingerprint(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}
func (f *fakeCacheStore) GetReportFingerprint(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (f *fakeCacheStore) DelReportFingerprint(_ context.Context, _ string) error { return nil }
