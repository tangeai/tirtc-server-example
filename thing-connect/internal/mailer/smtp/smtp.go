package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"thing-connect/internal/mailer"
)

// Config holds SMTP connection parameters.
type Config struct {
	Host     string
	Port     int
	TLSMode  string
	Username string
	Password string
	From     string
}

type smtpMailer struct{ cfg Config }

// New returns a mailer.Mailer backed by SMTP.
// Port 465 uses implicit TLS; port 587 uses STARTTLS.
func New(cfg Config) mailer.Mailer {
	return &smtpMailer{cfg: cfg}
}

func (m *smtpMailer) Send(ctx context.Context, to, subject, body string) error {
	return m.send(ctx, to, subject, body, "")
}

// SendMessage sends a multipart/alternative message when HTML is provided.
func (m *smtpMailer) SendMessage(ctx context.Context, to, subject, textBody, htmlBody string) error {
	return m.send(ctx, to, subject, textBody, htmlBody)
}

func (m *smtpMailer) send(ctx context.Context, to, subject, textBody, htmlBody string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	var msg []byte
	var err error
	if strings.TrimSpace(htmlBody) == "" {
		msg, err = buildMessage(m.cfg.From, to, subject, textBody)
	} else {
		msg, err = buildAlternativeMessage(m.cfg.From, to, subject, textBody, htmlBody)
	}
	if err != nil {
		return fmt.Errorf("smtp build message: %w", err)
	}

	mode := strings.ToLower(strings.TrimSpace(m.cfg.TLSMode))
	if mode == "" || mode == "auto" {
		if m.cfg.Port == 465 {
			mode = "implicit_tls"
		} else {
			mode = "starttls"
		}
	}
	if mode == "implicit_tls" {
		return m.sendTLS(ctx, addr, to, msg)
	}
	if mode == "starttls" {
		return m.sendSTARTTLS(ctx, addr, to, msg)
	}
	return fmt.Errorf("smtp: unsupported TLS mode %q", m.cfg.TLSMode)
}

func buildAlternativeMessage(from, to, subject, textBody, htmlBody string) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writePart := func(contentType, content string) error {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", contentType+`; charset="UTF-8"`)
		header.Set("Content-Transfer-Encoding", "quoted-printable")
		part, err := writer.CreatePart(header)
		if err != nil {
			return err
		}
		encoded := quotedprintable.NewWriter(part)
		if _, err := encoded.Write([]byte(content)); err != nil {
			return err
		}
		return encoded.Close()
	}
	if strings.TrimSpace(textBody) != "" {
		if err := writePart("text/plain", textBody); err != nil {
			return nil, fmt.Errorf("build text MIME part: %w", err)
		}
	}
	if err := writePart("text/html", htmlBody); err != nil {
		return nil, fmt.Errorf("build HTML MIME part: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close MIME writer: %w", err)
	}
	headers := "From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + mime.BEncoding.Encode("UTF-8", subject) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=\"" + writer.Boundary() + "\"\r\n\r\n"
	return append([]byte(headers), body.Bytes()...), nil
}

func buildMessage(from, to, subject, body string) ([]byte, error) {
	var encodedBody bytes.Buffer
	writer := quotedprintable.NewWriter(&encodedBody)
	if _, err := writer.Write([]byte(body)); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close body encoder: %w", err)
	}
	return []byte(
		"From: " + from + "\r\n" +
			"To: " + to + "\r\n" +
			"Subject: " + mime.BEncoding.Encode("UTF-8", subject) + "\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/plain; charset=UTF-8\r\n" +
			"Content-Transfer-Encoding: quoted-printable\r\n" +
			"\r\n" +
			encodedBody.String() + "\r\n",
	), nil
}

func (m *smtpMailer) sendTLS(ctx context.Context, addr, to string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: m.cfg.Host}
	rawConn, stopContextWatch, err := dialContext(ctx, addr)
	if err != nil {
		return smtpOperationError(ctx, "smtp.sendTLS dial", err)
	}
	defer stopContextWatch()
	conn := tls.Client(rawConn, tlsCfg)
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return smtpOperationError(ctx, "smtp.sendTLS handshake", err)
	}
	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return smtpOperationError(ctx, "smtp.sendTLS new client", err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
		return smtpOperationError(ctx, "smtp.sendTLS auth", err)
	}
	if err := m.deliver(client, to, msg); err != nil {
		return smtpOperationError(ctx, "smtp.sendTLS", err)
	}
	return nil
}

func (m *smtpMailer) sendSTARTTLS(ctx context.Context, addr, to string, msg []byte) error {
	host, _, _ := net.SplitHostPort(addr)
	conn, stopContextWatch, err := dialContext(ctx, addr)
	if err != nil {
		return smtpOperationError(ctx, "smtp.sendSTARTTLS dial", err)
	}
	defer stopContextWatch()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return smtpOperationError(ctx, "smtp.sendSTARTTLS new client", err)
	}
	defer func() { _ = client.Close() }()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return fmt.Errorf("smtp.sendSTARTTLS: server does not support STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
		return smtpOperationError(ctx, "smtp.sendSTARTTLS start TLS", err)
	}
	auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, host)
	if err := client.Auth(auth); err != nil {
		return smtpOperationError(ctx, "smtp.sendSTARTTLS auth", err)
	}
	if err := m.deliver(client, to, msg); err != nil {
		return smtpOperationError(ctx, "smtp.sendSTARTTLS", err)
	}
	return nil
}

func dialContext(ctx context.Context, addr string) (net.Conn, func(), error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, func() {}, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			return nil, func() {}, err
		}
	}
	stop := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	return conn, func() { stop() }, nil
}

func smtpOperationError(ctx context.Context, operation string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", operation, ctxErr)
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return fmt.Errorf("%s: %w", operation, context.DeadlineExceeded)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func (m *smtpMailer) deliver(client *smtp.Client, to string, msg []byte) error {
	if err := client.Mail(m.cfg.From); err != nil {
		return fmt.Errorf("smtp.deliver MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp.deliver RCPT TO: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp.deliver DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp.deliver write: %w", err)
	}
	return w.Close()
}
