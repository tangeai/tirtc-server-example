// Package registry constructs the configured captcha provider. Provider
// implementations are isolated here so business services only depend on the
// captcha.Verifier interface.
package registry

import (
	"context"
	"fmt"
	"strings"

	"thing-connect/internal/captcha"
	"thing-connect/internal/captcha/aliyun"
	"thing-connect/internal/captcha/geetest"
	"thing-connect/internal/captcha/tencent"
	"thing-connect/internal/captcha/yidun"
)

type Config struct {
	CaptchaID            string
	SecretID             string
	SecretKey            string
	AppSecretKey         string
	MiniProgramSecretKey string
	PublicConfig         map[string]string
}

func New(provider string, cfg Config) (captcha.Verifier, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	var verifier captcha.Verifier
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "yidun":
		if cfg.CaptchaID == "" || cfg.SecretID == "" || cfg.SecretKey == "" {
			return nil, fmt.Errorf("captcha provider yidun requires captcha_id, secret_id, and secret_key")
		}
		verifier = yidun.New(cfg.SecretID, cfg.SecretKey)
	case "geetest":
		if cfg.CaptchaID == "" || cfg.SecretKey == "" {
			return nil, fmt.Errorf("captcha provider geetest requires captcha_id and secret_key")
		}
		miniID := cfg.PublicConfig["mini_program_captcha_id"]
		if miniID != "" && cfg.MiniProgramSecretKey == "" {
			return nil, fmt.Errorf("geetest mini_program_captcha_id requires mini_program_secret_key")
		}
		verifier = geetest.New(cfg.CaptchaID, cfg.SecretKey, miniID, cfg.MiniProgramSecretKey)
	case "aliyun":
		if cfg.CaptchaID == "" || cfg.SecretID == "" || cfg.SecretKey == "" {
			return nil, fmt.Errorf("captcha provider aliyun requires scene_id, access_key_id, and access_key_secret")
		}
		var err error
		verifier, err = aliyun.New(cfg.SecretID, cfg.SecretKey, cfg.CaptchaID, cfg.PublicConfig["region"])
		if err != nil {
			return nil, err
		}
	case "tencent":
		if cfg.CaptchaID == "" || cfg.SecretID == "" || cfg.SecretKey == "" || cfg.AppSecretKey == "" {
			return nil, fmt.Errorf("captcha provider tencent requires captcha_app_id, cloud secret_id, cloud secret_key, and app_secret_key")
		}
		var err error
		verifier, err = tencent.New(cfg.SecretID, cfg.SecretKey, cfg.CaptchaID, cfg.AppSecretKey, cfg.PublicConfig["mini_program_captcha_id"], cfg.MiniProgramSecretKey)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported captcha provider %q", provider)
	}
	return boundVerifier{provider: provider, captchaID: cfg.CaptchaID, miniProgramCaptchaID: cfg.PublicConfig["mini_program_captcha_id"], delegate: verifier}, nil
}

type boundVerifier struct {
	provider             string
	captchaID            string
	miniProgramCaptchaID string
	delegate             captcha.Verifier
}

func (v boundVerifier) Verify(ctx context.Context, token captcha.CaptchaToken) error {
	if token.Provider != "" && !strings.EqualFold(token.Provider, v.provider) {
		return captcha.ErrVerifyFailed
	}
	id := token.CaptchaID
	if token.Metadata != nil && token.Metadata["captcha_id"] != "" {
		id = token.Metadata["captcha_id"]
	}
	if id != v.captchaID && (v.miniProgramCaptchaID == "" || id != v.miniProgramCaptchaID) {
		return captcha.ErrVerifyFailed
	}
	return v.delegate.Verify(ctx, token)
}
