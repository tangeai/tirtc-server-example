package captcha

import (
	"context"
	"log/slog"
)

// DevVerifier always passes and logs a warning. Use when secret_id is not configured.
type DevVerifier struct{}

func (DevVerifier) Verify(_ context.Context, _ CaptchaToken) error {
	slog.Info("[captcha] dev mode: skipping captcha verification")
	return nil
}
