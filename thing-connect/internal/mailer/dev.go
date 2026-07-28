package mailer

import (
	"context"
	"log/slog"
)

// DevMailer logs the email body instead of sending. Use when smtp.host is not configured.
type DevMailer struct{}

func (DevMailer) Send(_ context.Context, to, subject, body string) error {
	slog.Info("[mailer] dev mode", "to", to, "subject", subject, "body", body)
	return nil
}
