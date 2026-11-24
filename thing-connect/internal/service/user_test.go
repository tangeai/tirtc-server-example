package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"thing-connect/internal/captcha"
	"thing-connect/internal/model"
)

// ---- fakes ----

type fakeUserStore struct {
	user           *model.User
	lookupEmail    string
	createdEmail   string
	registrationIP string
	loginIP        string
	loginCount     int
	loginErr       error
	createID       int64
	createErr      error
	allocateErr    error
	quota          int
	deviceRows     []model.UserDeviceRow
	updateName     bool
	updateErr      error
	password       string
	updatePwdErr   error
}

func (f *fakeUserStore) GetUserByEmail(_ context.Context, email string) (*model.User, error) {
	f.lookupEmail = email
	return f.user, nil
}
func (f *fakeUserStore) CreateUser(_ context.Context, email, _ string, _ int, ip string) (int64, error) {
	f.createdEmail = email
	f.registrationIP = ip
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
	emailDeliveries       map[string]string
	emailCodes            map[string]string // email → code
	passwordResetAttempts map[string]int64
}

func newFakeCache() *fakeCacheStore {
	return &fakeCacheStore{
		emailDeliveries: map[string]string{}, deviceCodes: map[string]string{}, emailCodes: map[string]string{}, passwordResetAttempts: map[string]int64{},
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
func (f *fakeCacheStore) IncrRateLimitAttempt(_ context.Context, scope string, _ time.Duration) (int64, error) {
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
	_, _, err := svc.Register(context.Background(), "a@b.com", "pass1234", "123456", "192.0.2.1")
	if !errors.Is(err, ErrUserExists) {
		t.Errorf("want ErrUserExists, got %v", err)
	}
}

func TestUserService_Register_InvalidCode(t *testing.T) {
	cache := newFakeCache() // empty — no code stored
	svc := newSvc(&fakeUserStore{user: nil, createID: 42}, cache, false)
	_, _, err := svc.Register(context.Background(), "new@b.com", "pass1234", "000000", "192.0.2.1")
	if !errors.Is(err, ErrInvalidCode) {
		t.Errorf("want ErrInvalidCode, got %v", err)
	}
}

func TestUserService_Register_Success(t *testing.T) {
	cache := newFakeCache()
	_ = cache.SetEmailCode(context.Background(), "new@b.com", "123456", time.Minute)
	users := &fakeUserStore{user: nil, createID: 42}
	svc := newSvc(users, cache, false)
	tok, userID, err := svc.Register(context.Background(), " New@B.COM ", "pass1234", "123456", "192.0.2.1")
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if userID != 42 {
		t.Errorf("want userID=42, got %d", userID)
	}
	if tok == "" {
		t.Error("want non-empty token")
	}
	if users.registrationIP != "192.0.2.1" {
		t.Fatal("registration IP not recorded")
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
	if _, _, err := svc.Register(context.Background(), "new@b.com", "pass1234", "123456", "192.0.2.1"); err != nil {
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
	err := svc.SendCode(context.Background(), " A@B.COM ", "127.0.0.1", captcha.CaptchaToken{})
	if !errors.Is(err, ErrCaptchaFailed) {
		t.Errorf("want ErrCaptchaFailed, got %v", err)
	}
}

func TestUserService_SendCode_Success(t *testing.T) {
	cache := newFakeCache()
	ml := &fakeMailer{}
	svc := NewUserService(&fakeUserStore{}, cache, &fakeCaptcha{}, ml, "secret", DefaultServiceConfig())
	err := svc.SendCode(context.Background(), " A@B.COM ", "127.0.0.1", captcha.CaptchaToken{})
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

func TestUserService_EmailSendLimitIsSharedByRegistrationAndPasswordReset(t *testing.T) {
	cache := newFakeCache()
	queue := &fakePasswordResetEmailQueue{}
	svc := NewUserService(&fakeUserStore{}, cache, &fakeCaptcha{}, &fakeMailer{}, "secret", DefaultServiceConfig())
	svc.SetPasswordResetEmailQueue(queue)
	svc.UpdateEmailRateLimit(time.Minute, 1, 20)

	if err := svc.SendCode(context.Background(), " A@B.COM ", "127.0.0.1", captcha.CaptchaToken{}); err != nil {
		t.Fatalf("registration SendCode returned error: %v", err)
	}
	if got := cache.passwordResetAttempts["email-code:send:email:a@b.com"]; got != 1 {
		t.Fatalf("registration did not increment normalized shared scope: %v", cache.passwordResetAttempts)
	}
	if err := svc.SendPasswordResetCode(context.Background(), "a@b.com", "127.0.0.2", captcha.CaptchaToken{}); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("password-reset request should share registration limit, got %v", err)
	}
	if len(queue.emails) != 0 {
		t.Fatalf("shared-limit request was queued: %v", queue.emails)
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
	if cache.passwordResetAttempts["email-code:send:email:a@b.com"] != 1 {
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

type memoryMailGate struct {
	mu       sync.Mutex
	pending  map[string]string
	next     int
	err      error
	released int
}

func newMemoryMailGate() *memoryMailGate { return &memoryMailGate{pending: map[string]string{}} }

func (g *memoryMailGate) Acquire(_ context.Context, email string, _ time.Duration) (string, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return "", false, g.err
	}
	if _, ok := g.pending[email]; ok {
		return "", false, nil
	}
	g.next++
	token := fmt.Sprint(g.next)
	g.pending[email] = token
	return token, true, nil
}

func (g *memoryMailGate) Release(_ context.Context, email, token string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending[email] == token {
		delete(g.pending, email)
		g.released++
	}
	return nil
}

func newTestMailQueue(t *testing.T, gate PasswordResetMailGate, deliver func(context.Context, string) error) *InMemoryPasswordResetEmailQueue {
	t.Helper()
	queue, err := NewInMemoryPasswordResetEmailQueue(deliver, gate)
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func TestInMemoryPasswordResetEmailQueue(t *testing.T) {
	delivered := make(chan string, 1)
	gate := newMemoryMailGate()
	queue := newTestMailQueue(t, gate, func(_ context.Context, email string) error {
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
	deadline := time.Now().Add(time.Second)
	for {
		gate.mu.Lock()
		released := gate.released
		gate.mu.Unlock()
		if released == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivery did not release the pending reservation")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNewInMemoryPasswordResetEmailQueueRejectsMissingGate(t *testing.T) {
	if _, err := NewInMemoryPasswordResetEmailQueue(func(context.Context, string) error { return nil }, nil); err == nil {
		t.Fatal("queue without a gate accepted")
	}
}

// Two queues sharing one gate stand for two user-server instances.
func TestInMemoryPasswordResetEmailQueue_CoalescesSameEmailAcrossInstances(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	delivered := make(chan string, 2)
	deliver := func(_ context.Context, email string) error {
		started <- struct{}{}
		<-release
		delivered <- email
		return nil
	}
	gate := newMemoryMailGate()
	first, second := newTestMailQueue(t, gate, deliver), newTestMailQueue(t, gate, deliver)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go first.Run(ctx)
	go second.Run(ctx)

	if err := first.Enqueue(context.Background(), "a@b.com"); err != nil {
		t.Fatalf("first Enqueue returned error: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first job did not start")
	}
	for _, queue := range []*InMemoryPasswordResetEmailQueue{first, second} {
		if err := queue.Enqueue(context.Background(), "a@b.com"); err != nil {
			t.Fatalf("duplicate Enqueue returned error: %v", err)
		}
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

func TestInMemoryPasswordResetEmailQueue_GateFailureAndFullQueue(t *testing.T) {
	gate := newMemoryMailGate()
	queue := newTestMailQueue(t, gate, func(context.Context, string) error { return nil })
	gate.err = errors.New("redis down")
	if err := queue.Enqueue(context.Background(), "a@b.com"); err == nil || errors.Is(err, ErrPasswordResetMailQueueFull) {
		t.Fatalf("gate failure Enqueue = %v, want a dependency error", err)
	}
	gate.err = nil
	for i := 0; i < passwordResetMailQueueSize; i++ {
		if err := queue.Enqueue(context.Background(), fmt.Sprintf("u%d@b.com", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := queue.Enqueue(context.Background(), "overflow@b.com"); !errors.Is(err, ErrPasswordResetMailQueueFull) {
		t.Fatalf("full queue Enqueue = %v", err)
	}
	if _, pending := gate.pending["overflow@b.com"]; pending {
		t.Fatal("rejected request kept its reservation and would block a retry")
	}
}
func TestUserService_SendPasswordResetCode_RateLimited(t *testing.T) {
	cache := newFakeCache()
	queue := &fakePasswordResetEmailQueue{}
	svc := NewUserService(&fakeUserStore{}, cache, &fakeCaptcha{}, &fakeMailer{}, "secret", DefaultServiceConfig())
	svc.SetPasswordResetEmailQueue(queue)
	svc.UpdateEmailRateLimit(time.Minute, 1, 10)

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
	cache.passwordResetAttempts["email-code:send:ip:127.0.0.1"] = defaultEmailSendMaxPerIP
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
	cache.passwordResetAttempts["password-reset:verify:email:a@b.com"] = passwordResetEmailMaxAttempts
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

func TestUserServiceDeviceListNestsVoIPProfile(t *testing.T) {
	profile := `{"hardware":{"chip_model":"任意芯片值","board_model":"任意开发板值"},"firmware_version":"任意版本值","voip":{"up_video_mt":"h264","down_video_mt":"mjpeg","down_audio_mt":"amr","audio_rate":8000,"no_video":false}}`
	svc := newSvc(&fakeUserStore{deviceRows: []model.UserDeviceRow{{
		DeviceID: "device-1", DeviceName: "书房设备", Status: 1, Profiles: &profile,
	}}}, newFakeCache(), false)
	devices, err := svc.DeviceList(context.Background(), 1, nil)
	if err != nil || len(devices) != 1 {
		t.Fatalf("DeviceList: devices=%v err=%v", devices, err)
	}
	voip := devices[0].Profiles["voip"]
	if string(voip["up_video_mt"]) != `"h264"` || string(voip["has_camera"]) != "true" ||
		string(voip["has_screen"]) != "true" || string(voip["voip_room_type"]) != `"video"` {
		t.Fatalf("voip profile=%v", voip)
	}
	if devices[0].Hardware == nil || devices[0].Hardware.ChipModel == nil || devices[0].Hardware.BoardModel == nil ||
		*devices[0].Hardware.ChipModel != "任意芯片值" || *devices[0].Hardware.BoardModel != "任意开发板值" ||
		devices[0].FirmwareVersion == nil || *devices[0].FirmwareVersion != "任意版本值" {
		t.Fatalf("metadata=%+v", devices[0])
	}
}

func (f *fakeUserStore) RecordLogin(_ context.Context, _ int64, ip string, _ time.Time) error {
	f.loginIP = ip
	f.loginCount++
	return f.loginErr
}

func TestUserService_LoginRecordsOnlySuccessfulAuthentication(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("pass1234"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	users := &fakeUserStore{user: &model.User{ID: 7, Email: "a@b.com", Password: string(hash), Status: 1, AuthRevision: 1}}
	svc := newSvc(users, newFakeCache(), false)
	token, _, err := svc.Login(context.Background(), "a@b.com", "pass1234", captcha.CaptchaToken{UserIP: "::ffff:192.0.2.7"})
	if err != nil || token == "" || users.loginIP != "192.0.2.7" || users.loginCount != 1 {
		t.Fatalf("success not recorded: err=%v ip=%s", err, users.loginIP)
	}
	if _, _, err := svc.Login(context.Background(), "a@b.com", "wrong", captcha.CaptchaToken{UserIP: "192.0.2.8"}); !errors.Is(err, ErrInvalidCreds) {
		t.Fatal(err)
	}
	if users.loginCount != 1 {
		t.Fatal("failed login changed last successful IP")
	}
	users.loginErr = errors.New("database unavailable")
	token, _, err = svc.Login(context.Background(), "a@b.com", "pass1234", captcha.CaptchaToken{UserIP: "192.0.2.8"})
	if err == nil || token != "" {
		t.Fatal("login returned success without recording its IP")
	}
}

type auditMailFunc func(context.Context, string, string, string) error

func (f auditMailFunc) Send(ctx context.Context, to, subject, body string) error {
	return f(ctx, to, subject, body)
}
func TestOlderRegistrationMailFailurePreservesNewCode(t *testing.T) {
	cache := newFakeCache()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	sender := auditMailFunc(func(context.Context, string, string, string) error {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return errors.New("old SMTP attempt failed")
		}
		return nil
	})
	svc := NewUserService(&fakeUserStore{}, cache, &fakeCaptcha{}, sender, "test-secret", DefaultServiceConfig())
	done := make(chan error, 1)
	go func() {
		done <- svc.SendCode(context.Background(), "person@example.test", "192.0.2.1", captcha.CaptchaToken{})
	}()
	<-started
	err := svc.SendCode(context.Background(), "person@example.test", "192.0.2.1", captcha.CaptchaToken{})
	if err != nil {
		close(release)
		<-done
		t.Fatal(err)
	}
	second := cache.emailCodes["person@example.test"]
	close(release)
	if err := <-done; err == nil {
		t.Fatal("missing injected failure")
	}
	if got := cache.emailCodes["person@example.test"]; got != second || got == "" {
		t.Fatal("late failure deleted the successfully sent newer registration code")
	}
}

func (f *fakeCacheStore) SetEmailCodeDelivery(ctx context.Context, email, code, delivery string, ttl time.Duration) error {
	f.emailDeliveries[email] = delivery
	return f.SetEmailCode(ctx, email, code, ttl)
}
func (f *fakeCacheStore) DiscardEmailCodeDelivery(ctx context.Context, email, delivery string) error {
	if f.emailDeliveries[email] == delivery {
		delete(f.emailDeliveries, email)
		return f.DelEmailCode(ctx, email)
	}
	return nil
}

func TestPasswordResetQueueWaitsForDeliveryAndGateReleaseOnStop(t *testing.T) {
	entered := make(chan struct{})
	finish := make(chan struct{})
	defer func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
	}()
	gate := newMemoryMailGate()
	q := newTestMailQueue(t, gate, func(ctx context.Context, _ string) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded SMTP delivery")
		}
		close(entered)
		select {
		case <-finish:
			return nil
		case <-ctx.Done():
			t.Error("interrupted active delivery")
			return ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := q.Enqueue(ctx, "a@example.test"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); q.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("queue exited during delivery")
	case <-time.After(20 * time.Millisecond):
	}
	close(finish)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.released != 1 {
		t.Fatalf("queue exited with %d gate releases", gate.released)
	}
}
