package captcha

import (
	"context"
	"errors"
)

// ErrVerifyFailed is returned when the captcha check fails.
var ErrVerifyFailed = errors.New("captcha verification failed")

// CaptchaToken holds the three values the frontend sends after the user
// completes the captcha widget.
type CaptchaToken struct {
	Provider string            // provider that issued the token
	Token    string            // provider-neutral verification token
	Metadata map[string]string // provider-specific, non-secret fields
	UserIP   string            // request IP supplied by the trusted HTTP handler

	// Deprecated compatibility fields for the original 易盾 API. Providers
	// should prefer Provider, Token, and Metadata for new integrations.
	CaptchaID string // widget's captcha_id
	Validate  string // token returned by widget
	User      string // optional user identifier (may be empty)
}

// Verifier validates a human-verification token. Implement this interface
// to swap in a different captcha provider.
type Verifier interface {
	Verify(ctx context.Context, token CaptchaToken) error
}
