package mailer

import (
	"context"
	"errors"
)

// Mailer sends email. Implement this interface to swap in a different
// email provider (SendGrid, SES, etc.).
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// RichMailer is implemented by transports that can deliver multipart email.
// Callers retain the Mailer fallback for providers that only accept text.
type RichMailer interface {
	Mailer
	SendMessage(ctx context.Context, to, subject, textBody, htmlBody string) error
}

// DisabledMailer represents an explicitly disabled production mail channel.
// It never logs message bodies or verification codes.
type DisabledMailer struct{}

func (DisabledMailer) Send(context.Context, string, string, string) error {
	return errors.New("email delivery is disabled")
}
