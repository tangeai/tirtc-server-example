package mailer

import "context"

// Mailer sends email. Implement this interface to swap in a different
// email provider (SendGrid, SES, etc.).
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}
