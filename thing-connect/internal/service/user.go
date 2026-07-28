package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
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
	user      store.UserStore
	cache     store.CacheStore
	captcha   captcha.Verifier
	mailer    mailer.Mailer
	jwtSecret string
	cfg       ServiceConfig
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
		user:      user,
		cache:     cache,
		captcha:   captcha,
		mailer:    mailer,
		jwtSecret: jwtSecret,
		cfg:       cfg,
	}
}

// SendCode verifies the captcha token, then sends a 6-digit email code.
// The code is stored in Redis under key "email_code:{email}" with TTL = cfg.CodeTTL.
func (s *UserService) SendCode(ctx context.Context, email string, tok captcha.CaptchaToken) error {
	if err := s.captcha.Verify(ctx, tok); err != nil {
		return ErrCaptchaFailed
	}

	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return fmt.Errorf("service.SendCode rand: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())
	if err := s.cache.SetEmailCode(ctx, email, code, s.cfg.CodeTTL); err != nil {
		return fmt.Errorf("service.SendCode SetEmailCode: %w", err)
	}

	body := fmt.Sprintf("Your verification code is: %s\n\nValid for %s.", code, s.cfg.CodeTTL)
	if err := s.mailer.Send(ctx, email, "Your verification code", body); err != nil {
		_ = s.cache.DelEmailCode(ctx, email)
		return fmt.Errorf("service.SendCode Send: %w", err)
	}
	return nil
}

// Register validates the email code, then creates the user.
func (s *UserService) Register(ctx context.Context, email, password, code string) (string, int64, error) {
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

	userID, err := s.user.CreateUser(ctx, email, string(hash))
	if err != nil {
		return "", 0, fmt.Errorf("service.Register CreateUser: %w", err)
	}

	// Consume the code (best-effort; ignore error).
	_ = s.cache.DelEmailCode(ctx, email)

	tok, err := s.issueJWT(userID)
	if err != nil {
		return "", 0, fmt.Errorf("service.Register issueJWT: %w", err)
	}
	return tok, userID, nil
}

// Login verifies the captcha token, then authenticates the user.
func (s *UserService) Login(ctx context.Context, email, password string, tok captcha.CaptchaToken) (string, int64, error) {
	if err := s.captcha.Verify(ctx, tok); err != nil {
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

	jwtTok, err := s.issueJWT(user.ID)
	if err != nil {
		return "", 0, fmt.Errorf("service.Login issueJWT: %w", err)
	}
	return jwtTok, user.ID, nil
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

func (s *UserService) issueJWT(userID int64) (string, error) {
	claims := jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(s.cfg.TokenExpiry).Unix(),
		"iat":     time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(s.jwtSecret))
	if err != nil {
		return "", fmt.Errorf("issueJWT: %w", err)
	}
	return signed, nil
}
