package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"thing-connect/internal/captcha"
	"thing-connect/internal/mailer"
	"thing-connect/internal/store"
)

// DeviceInfo is the service-layer device list item (online status added by service).
type DeviceInfo struct {
	DeviceID       string   `json:"device_id"`
	DeviceName     string   `json:"device_name"`
	Status         int8     `json:"status"`
	MAC            string   `json:"mac"`
	BindTime       *string  `json:"bind_time"`
	Online         bool     `json:"online"`
	UpVideoMT      string   `json:"up_video_mt"`
	DownVideoMT    string   `json:"down_video_mt"`
	DownAudioMT    string   `json:"down_audio_mt"`
	AudioRate      int      `json:"audio_rate"`
	CameraRotation *int     `json:"camera_rotation,omitempty"`
	AspectRatio    *float64 `json:"aspect_ratio,omitempty"`
	HorMirror      *bool    `json:"hor_mirror,omitempty"`
	VertMirror     *bool    `json:"vert_mirror,omitempty"`
	ObjectFit      *string  `json:"object_fit,omitempty"`
	HasCamera      bool     `json:"has_camera"`
	HasScreen      bool     `json:"has_screen"`
	VoipRoomType   string   `json:"voip_room_type"`
}

// OnlineChecker allows service to check device online status without importing mqttc.
type OnlineChecker interface {
	IsOnline(ctx context.Context, clientID string) bool
}

// UserService handles user registration, login, and quota.
type UserService struct {
	user                    store.UserStore
	cache                   store.CacheStore
	captcha                 captcha.Verifier
	mailer                  mailer.Mailer
	passwordResetEmailQueue PasswordResetEmailQueue
	jwtSecret               string
	cfg                     ServiceConfig
	runtimeMu               sync.RWMutex
	templates               map[string]EmailTemplate
	emailSendWindow         time.Duration
	emailSendMaxPerEmail    int
	emailSendMaxPerIP       int
	defaultBindQuota        int
}

type EmailTemplate struct {
	Enabled  bool
	Subject  string
	HTMLBody string
	TextBody string
}

// PasswordResetEmailQueue accepts a public reset request for asynchronous
// delivery. It deliberately receives every valid request before account
// lookup, keeping the HTTP path independent of account existence.
type PasswordResetEmailQueue interface {
	Enqueue(ctx context.Context, email string) error
}

const (
	passwordResetMailQueueSize = 256
	passwordResetMailWorkers   = 2

	defaultEmailSendWindow      = 15 * time.Minute
	defaultEmailSendMaxPerEmail = 5
	defaultEmailSendMaxPerIP    = 20
)

// InMemoryPasswordResetEmailQueue keeps the public request path independent
// of account lookup and SMTP without adding persistent application state. A
// user can safely request a new code if a process restart drops a queued job.
type InMemoryPasswordResetEmailQueue struct {
	jobs    chan string
	deliver func(context.Context, string) error

	mu      sync.Mutex
	pending map[string]struct{}
}

func NewInMemoryPasswordResetEmailQueue(
	deliver func(context.Context, string) error,
) *InMemoryPasswordResetEmailQueue {
	return &InMemoryPasswordResetEmailQueue{
		jobs:    make(chan string, passwordResetMailQueueSize),
		deliver: deliver,
		pending: make(map[string]struct{}),
	}
}

func (q *InMemoryPasswordResetEmailQueue) Enqueue(ctx context.Context, email string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.pending[email]; ok {
		return nil
	}
	q.pending[email] = struct{}{}
	select {
	case q.jobs <- email:
		return nil
	case <-ctx.Done():
		delete(q.pending, email)
		return ctx.Err()
	default:
		delete(q.pending, email)
		return ErrPasswordResetMailQueueFull
	}
}

// Run processes password-reset email jobs until ctx is cancelled. Delivery
// errors are logged; users can request another verification code to retry.
func (q *InMemoryPasswordResetEmailQueue) Run(ctx context.Context) {
	for i := 0; i < passwordResetMailWorkers; i++ {
		go q.runWorker(ctx)
	}
	<-ctx.Done()
}

func (q *InMemoryPasswordResetEmailQueue) runWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case email := <-q.jobs:
			deliveryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := q.deliver(deliveryCtx, email)
			cancel()
			q.mu.Lock()
			delete(q.pending, email)
			q.mu.Unlock()
			if err != nil {
				slog.Warn("password reset email delivery failed", "err", err)
			}
		}
	}
}

func NewUserService(
	user store.UserStore,
	cache store.CacheStore,
	captcha captcha.Verifier,
	mailer mailer.Mailer,
	jwtSecret string,
	cfg ServiceConfig,
) *UserService {
	return &UserService{
		user:                 user,
		cache:                cache,
		captcha:              captcha,
		mailer:               mailer,
		jwtSecret:            jwtSecret,
		cfg:                  cfg,
		templates:            map[string]EmailTemplate{},
		emailSendWindow:      defaultEmailSendWindow,
		emailSendMaxPerEmail: defaultEmailSendMaxPerEmail,
		emailSendMaxPerIP:    defaultEmailSendMaxPerIP,
		defaultBindQuota:     cfg.QuotaPerUser,
	}
}

func (s *UserService) UpdateCaptcha(value captcha.Verifier) {
	s.runtimeMu.Lock()
	s.captcha = value
	s.runtimeMu.Unlock()
}

func (s *UserService) UpdateMailer(value mailer.Mailer) {
	s.runtimeMu.Lock()
	s.mailer = value
	s.runtimeMu.Unlock()
}

func (s *UserService) UpdateEmailCodeTTL(value time.Duration) {
	s.runtimeMu.Lock()
	s.cfg.CodeTTL = value
	s.runtimeMu.Unlock()
}

func (s *UserService) UpdateTokenTTL(value time.Duration) {
	s.runtimeMu.Lock()
	s.cfg.TokenExpiry = value
	s.runtimeMu.Unlock()
}

func (s *UserService) UpdateDefaultBindQuota(value int) {
	s.runtimeMu.Lock()
	s.defaultBindQuota = value
	s.runtimeMu.Unlock()
}

func (s *UserService) UpdateEmailRateLimit(window time.Duration, maxPerEmail, maxPerIP int) {
	s.runtimeMu.Lock()
	s.emailSendWindow, s.emailSendMaxPerEmail, s.emailSendMaxPerIP = window, maxPerEmail, maxPerIP
	s.runtimeMu.Unlock()
}

func (s *UserService) UpdateEmailTemplate(key string, value EmailTemplate) {
	s.runtimeMu.Lock()
	s.templates[key] = value
	s.runtimeMu.Unlock()
}

func (s *UserService) runtimeSnapshot() (captcha.Verifier, mailer.Mailer, ServiceConfig, map[string]EmailTemplate, time.Duration, int, int) {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	templates := make(map[string]EmailTemplate, len(s.templates))
	for key, value := range s.templates {
		templates[key] = value
	}
	return s.captcha, s.mailer, s.cfg, templates, s.emailSendWindow, s.emailSendMaxPerEmail, s.emailSendMaxPerIP
}

// SetPasswordResetEmailQueue enables asynchronous reset-email delivery. It
// must be called during application startup before requests are served.
func (s *UserService) SetPasswordResetEmailQueue(queue PasswordResetEmailQueue) {
	s.passwordResetEmailQueue = queue
}

// SendCode verifies the captcha token, applies the shared email/IP send limit,
// then sends a 6-digit registration code.
// The code is stored in Redis under key "email_code:{email}" with TTL = cfg.CodeTTL.
func (s *UserService) SendCode(ctx context.Context, email, clientIP string, tok captcha.CaptchaToken) error {
	email = normalizeEmail(email)
	verifier, _, _, _, _, _, _ := s.runtimeSnapshot()
	if err := verifier.Verify(ctx, tok); err != nil {
		return ErrCaptchaFailed
	}
	if err := s.limitEmailCodeSend(ctx, email, clientIP); err != nil {
		return err
	}
	return s.sendEmailCode(ctx, email, email, "registration", registrationCodeEmail)
}

// SendPasswordResetCode validates and rate-limits the public request, then queues it for
// asynchronous delivery. It does not look up the account or contact SMTP on
// the request path, so the response cannot reveal whether an email is
// registered.
func (s *UserService) SendPasswordResetCode(ctx context.Context, email, clientIP string, tok captcha.CaptchaToken) error {
	email = normalizeEmail(email)
	verifier, _, _, _, _, _, _ := s.runtimeSnapshot()
	if err := verifier.Verify(ctx, tok); err != nil {
		return ErrCaptchaFailed
	}
	if err := s.limitEmailCodeSend(ctx, email, clientIP); err != nil {
		return err
	}
	if s.passwordResetEmailQueue == nil {
		return fmt.Errorf("service.SendPasswordResetCode: email queue is not configured")
	}
	if err := s.passwordResetEmailQueue.Enqueue(ctx, email); err != nil {
		if errors.Is(err, ErrPasswordResetMailQueueFull) {
			return ErrRateLimit
		}
		return fmt.Errorf("service.SendPasswordResetCode Enqueue: %w", err)
	}
	return nil
}

// DeliverPasswordResetCode runs only in the in-memory mail worker. Unknown
// emails are acknowledged without sending any mail. Delivery errors are
// returned for logging; users may submit a new request to retry.
func (s *UserService) DeliverPasswordResetCode(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	user, err := s.user.GetUserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("service.DeliverPasswordResetCode GetUserByEmail: %w", err)
	}
	if user == nil {
		return nil
	}
	return s.sendEmailCode(ctx, email, passwordResetCodeKey(email), "password_reset", passwordResetCodeEmail)
}

type emailCodeMessage struct {
	subject string
	body    string
	html    string
}

func (s *UserService) sendEmailCode(
	ctx context.Context,
	email, cacheKey string,
	templateKey string,
	buildMessage func(code string, ttl time.Duration) emailCodeMessage,
) error {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return fmt.Errorf("service.SendCode rand: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())
	_, activeMailer, cfg, templates, _, _, _ := s.runtimeSnapshot()
	if err := s.cache.SetEmailCode(ctx, cacheKey, code, cfg.CodeTTL); err != nil {
		return fmt.Errorf("service.SendCode SetEmailCode: %w", err)
	}

	message := buildMessage(code, cfg.CodeTTL)
	if template, ok := templates[templateKey]; ok {
		if !template.Enabled {
			_ = s.cache.DelEmailCode(ctx, cacheKey)
			return errors.New("email template is disabled")
		}
		message = renderEmailTemplate(template, code, cfg.CodeTTL)
	}
	var sendErr error
	if richMailer, ok := activeMailer.(mailer.RichMailer); ok && strings.TrimSpace(message.html) != "" {
		sendErr = richMailer.SendMessage(ctx, email, message.subject, message.body, message.html)
	} else {
		sendErr = activeMailer.Send(ctx, email, message.subject, message.body)
	}
	if sendErr != nil {
		_ = s.cache.DelEmailCode(ctx, cacheKey)
		return fmt.Errorf("service.SendCode Send: %w", sendErr)
	}
	return nil
}

const emailSource = "TiRTC 体验平台"

func registrationCodeEmail(code string, ttl time.Duration) emailCodeMessage {
	return emailCodeMessage{
		subject: "【TiRTC 体验平台】注册验证码",
		body: fmt.Sprintf(`您好：

您正在注册 TiRTC 体验平台账号，本次注册验证码为：%s。
验证码将在 %s 后失效，请勿向任何人透露。

若非本人操作，请忽略此邮件。

来源：%s`, code, formatCodeTTL(ttl), emailSource),
	}
}

func passwordResetCodeEmail(code string, ttl time.Duration) emailCodeMessage {
	return emailCodeMessage{
		subject: "【TiRTC 体验平台】找回密码验证码",
		body: fmt.Sprintf(`您好：

您正在重置 TiRTC 体验平台账号密码，本次找回密码验证码为：%s。
验证码将在 %s 后失效，请勿向任何人透露。

若非本人操作，请忽略此邮件；如有疑问，请联系平台管理员。

来源：%s`, code, formatCodeTTL(ttl), emailSource),
	}
}

func formatCodeTTL(ttl time.Duration) string {
	seconds := int(ttl.Round(time.Second).Seconds())
	if seconds <= 0 {
		return "短时间"
	}
	minutes, seconds := seconds/60, seconds%60
	if minutes == 0 {
		return fmt.Sprintf("%d 秒", seconds)
	}
	if seconds == 0 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	return fmt.Sprintf("%d 分 %d 秒", minutes, seconds)
}

func renderEmailTemplate(template EmailTemplate, code string, ttl time.Duration) emailCodeMessage {
	minutes := int((ttl + time.Minute - 1) / time.Minute)
	replacements := map[string]string{"code": code, "expires_in_minutes": fmt.Sprint(minutes), "product_name": emailSource, "support_email": ""}
	render := func(value string) string {
		return emailTemplateToken.ReplaceAllStringFunc(value, func(token string) string {
			parts := emailTemplateToken.FindStringSubmatch(token)
			return replacements[parts[1]]
		})
	}
	textBody := render(template.TextBody)
	htmlBody := render(template.HTMLBody)
	if textBody == "" {
		textBody = htmlBody
	}
	return emailCodeMessage{subject: render(template.Subject), body: textBody, html: htmlBody}
}

var emailTemplateToken = regexp.MustCompile(`\{\{\s*(code|expires_in_minutes|product_name|support_email)\s*\}\}`)

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func passwordResetCodeKey(email string) string { return "password_reset:" + normalizeEmail(email) }

const (
	passwordResetEmailMaxAttempts = 5
	passwordResetIPMaxAttempts    = 20
)

func (s *UserService) limitEmailCodeSend(ctx context.Context, email, clientIP string) error {
	_, _, _, _, window, maxEmail, maxIP := s.runtimeSnapshot()
	emailAttempts, err := s.cache.IncrRateLimitAttempt(ctx, "email-code:send:email:"+email, window)
	if err != nil {
		return fmt.Errorf("service email-code send email rate limit: %w", err)
	}
	if emailAttempts > int64(maxEmail) {
		return ErrRateLimit
	}
	ipAttempts, err := s.cache.IncrRateLimitAttempt(ctx, "email-code:send:ip:"+strings.TrimSpace(clientIP), window)
	if err != nil {
		return fmt.Errorf("service email-code send IP rate limit: %w", err)
	}
	if ipAttempts > int64(maxIP) {
		return ErrRateLimit
	}
	return nil
}

// Register validates the email code, then creates the user.
func (s *UserService) Register(ctx context.Context, email, password, code string) (string, int64, error) {
	email = normalizeEmail(email)
	// Validate email code first.
	storedCode, err := s.cache.GetEmailCode(ctx, email)
	if err != nil {
		return "", 0, fmt.Errorf("service.Register GetEmailCode: %w", err)
	}
	if storedCode != code {
		return "", 0, ErrInvalidCode
	}

	existing, err := s.user.GetUserByEmail(ctx, email)
	if err != nil {
		return "", 0, fmt.Errorf("service.Register GetUserByEmail: %w", err)
	}
	if existing != nil {
		return "", 0, ErrUserExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", 0, fmt.Errorf("service.Register bcrypt: %w", err)
	}

	s.runtimeMu.RLock()
	defaultQuota := s.defaultBindQuota
	s.runtimeMu.RUnlock()
	userID, err := s.user.CreateUser(ctx, email, string(hash), defaultQuota)
	if err != nil {
		return "", 0, fmt.Errorf("service.Register CreateUser: %w", err)
	}

	// Consume the code (best-effort; ignore error).
	_ = s.cache.DelEmailCode(ctx, email)

	tok, err := s.issueJWT(userID, 1)
	if err != nil {
		return "", 0, fmt.Errorf("service.Register issueJWT: %w", err)
	}
	return tok, userID, nil
}

// Login verifies the captcha token, then authenticates the user.
func (s *UserService) Login(ctx context.Context, email, password string, tok captcha.CaptchaToken) (string, int64, error) {
	email = normalizeEmail(email)
	verifier, _, _, _, _, _, _ := s.runtimeSnapshot()
	if err := verifier.Verify(ctx, tok); err != nil {
		return "", 0, ErrCaptchaFailed
	}

	user, err := s.user.GetUserByEmail(ctx, email)
	if err != nil {
		return "", 0, fmt.Errorf("service.Login GetUserByEmail: %w", err)
	}
	if user == nil {
		return "", 0, ErrInvalidCreds
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return "", 0, ErrInvalidCreds
	}

	if user.Status != 1 {
		return "", 0, ErrInvalidCreds
	}
	jwtTok, err := s.issueJWT(user.ID, user.AuthRevision)
	if err != nil {
		return "", 0, fmt.Errorf("service.Login issueJWT: %w", err)
	}
	return jwtTok, user.ID, nil
}

// ResetPassword verifies a password-reset code and replaces the stored bcrypt
// password hash. Reset codes use a separate cache key from registration codes.
func (s *UserService) ResetPassword(ctx context.Context, email, password, code, clientIP string) error {
	email = normalizeEmail(email)
	if err := s.limitPasswordResetAttempts(ctx, email, clientIP); err != nil {
		return err
	}
	user, err := s.user.GetUserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("service.ResetPassword GetUserByEmail: %w", err)
	}
	if user == nil {
		return ErrInvalidCode
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("service.ResetPassword bcrypt: %w", err)
	}
	consumed, err := s.cache.ConsumeEmailCode(ctx, passwordResetCodeKey(email), code)
	if err != nil {
		return fmt.Errorf("service.ResetPassword ConsumeEmailCode: %w", err)
	}
	if !consumed {
		return ErrInvalidCode
	}
	if err := s.user.UpdatePassword(ctx, user.ID, string(hash)); err != nil {
		return fmt.Errorf("service.ResetPassword UpdatePassword: %w", err)
	}
	return nil
}

func (s *UserService) limitPasswordResetAttempts(ctx context.Context, email, clientIP string) error {
	_, _, cfg, _, _, _, _ := s.runtimeSnapshot()
	emailAttempts, err := s.cache.IncrRateLimitAttempt(ctx, "password-reset:verify:email:"+email, cfg.CodeTTL)
	if err != nil {
		return fmt.Errorf("service.ResetPassword email rate limit: %w", err)
	}
	if emailAttempts > passwordResetEmailMaxAttempts {
		return ErrRateLimit
	}
	ipAttempts, err := s.cache.IncrRateLimitAttempt(ctx, "password-reset:verify:ip:"+clientIP, cfg.CodeTTL)
	if err != nil {
		return fmt.Errorf("service.ResetPassword IP rate limit: %w", err)
	}
	if ipAttempts > passwordResetIPMaxAttempts {
		return ErrRateLimit
	}
	return nil
}

func (s *UserService) Quota(ctx context.Context, userID int64) (int, error) {
	q, err := s.user.GetQuota(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("service.Quota: %w", err)
	}
	return q, nil
}

func (s *UserService) DeviceList(ctx context.Context, userID int64, checker OnlineChecker) ([]DeviceInfo, error) {
	rows, err := s.user.GetDeviceList(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("service.DeviceList: %w", err)
	}
	result := make([]DeviceInfo, 0, len(rows))
	for _, r := range rows {
		media := parseVoipProfile(r.VoipProfile)
		di := DeviceInfo{
			DeviceID:       r.DeviceID,
			DeviceName:     r.DeviceName,
			Status:         r.Status,
			MAC:            r.MAC,
			BindTime:       r.BindTime,
			UpVideoMT:      media.UpVideoMT,
			DownVideoMT:    media.DownVideoMT,
			DownAudioMT:    media.DownAudioMT,
			AudioRate:      media.AudioRate,
			CameraRotation: media.CameraRotation,
			AspectRatio:    media.AspectRatio,
			HorMirror:      media.HorMirror,
			VertMirror:     media.VertMirror,
			ObjectFit:      media.ObjectFit,
			HasCamera:      media.UpVideoMT != "",
			HasScreen:      media.DownVideoMT != "",
			VoipRoomType:   voipRoomType(media.UpVideoMT, media.DownVideoMT),
		}
		if checker != nil {
			if r.DeviceID != "" {
				di.Online = checker.IsOnline(ctx, "sn_"+r.DeviceID)
			}
		}
		result = append(result, di)
	}
	return result, nil
}

func (s *UserService) UpdateDeviceName(
	ctx context.Context, userID int64, deviceID, deviceName string,
) error {
	updated, err := s.user.UpdateDeviceName(ctx, userID, deviceID, deviceName)
	if err != nil {
		return fmt.Errorf("service.UpdateDeviceName: %w", err)
	}
	if !updated {
		return ErrDeviceNotFound
	}
	return nil
}

type voipProfileMedia struct {
	UpVideoMT      string
	DownVideoMT    string
	DownAudioMT    string
	AudioRate      int
	CameraRotation *int
	AspectRatio    *float64
	HorMirror      *bool
	VertMirror     *bool
	ObjectFit      *string
	NoVideo        bool
}

func parseVoipProfile(profile *string) voipProfileMedia {
	if profile == nil || *profile == "" {
		return voipProfileMedia{}
	}
	var media struct {
		UpVideoMT      string   `json:"up_video_mt"`
		DownVideoMT    string   `json:"down_video_mt"`
		DownAudioMT    string   `json:"down_audio_mt"`
		AudioRate      int      `json:"audio_rate"`
		CameraRotation *int     `json:"camera_rotation"`
		AspectRatio    *float64 `json:"aspect_ratio"`
		HorMirror      *bool    `json:"hor_mirror"`
		VertMirror     *bool    `json:"vert_mirror"`
		ObjectFit      *string  `json:"object_fit"`
		VideoMT        string   `json:"video_mt"`
		NoVideo        bool     `json:"no_video"`
	}
	if err := json.Unmarshal([]byte(*profile), &media); err != nil {
		return voipProfileMedia{}
	}
	parsed := voipProfileMedia{
		UpVideoMT:      media.UpVideoMT,
		DownVideoMT:    media.DownVideoMT,
		DownAudioMT:    media.DownAudioMT,
		AudioRate:      media.AudioRate,
		CameraRotation: normalizeCameraRotation(media.CameraRotation),
		AspectRatio:    normalizeAspectRatio(media.AspectRatio),
		HorMirror:      media.HorMirror,
		VertMirror:     media.VertMirror,
		ObjectFit:      normalizeObjectFit(media.ObjectFit),
		NoVideo:        media.NoVideo,
	}
	if parsed.NoVideo {
		parsed.UpVideoMT = ""
		parsed.DownVideoMT = ""
		return parsed
	}
	parsed.UpVideoMT = normalizeVideoMT(parsed.UpVideoMT)
	parsed.DownVideoMT = normalizeVideoMT(parsed.DownVideoMT)
	if parsed.UpVideoMT == "" && parsed.DownVideoMT == "" {
		legacyVideoMT := normalizeVideoMT(media.VideoMT)
		parsed.UpVideoMT = legacyVideoMT
		parsed.DownVideoMT = legacyVideoMT
	}
	return parsed
}

func normalizeCameraRotation(rotation *int) *int {
	if rotation == nil {
		return nil
	}
	switch *rotation {
	case 0, 90, 180, 270:
		return rotation
	default:
		return nil
	}
}

func normalizeAspectRatio(ratio *float64) *float64 {
	if ratio == nil || *ratio <= 0 {
		return nil
	}
	return ratio
}

func normalizeObjectFit(objectFit *string) *string {
	if objectFit == nil || (*objectFit != "fill" && *objectFit != "contain") {
		return nil
	}
	return objectFit
}

func normalizeVideoMT(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "none") {
		return ""
	}
	return value
}

func voipRoomType(upVideoMT, downVideoMT string) string {
	if upVideoMT != "" || downVideoMT != "" {
		return "video"
	}
	return "voice"
}

func (s *UserService) issueJWT(userID, authRevision int64) (string, error) {
	_, _, cfg, _, _, _, _ := s.runtimeSnapshot()
	claims := jwt.MapClaims{
		"user_id":       userID,
		"auth_revision": authRevision,
		"exp":           time.Now().Add(cfg.TokenExpiry).Unix(),
		"iat":           time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(s.jwtSecret))
	if err != nil {
		return "", fmt.Errorf("issueJWT: %w", err)
	}
	return signed, nil
}
