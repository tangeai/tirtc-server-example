package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"

	"thing-connect/internal/mailer"
)

// Config holds SMTP connection parameters.
type Config struct {
	Host     string
	Port     int
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

func (m *smtpMailer) Send(_ context.Context, to, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	msg := []byte(
		"From: " + m.cfg.From + "\r\n" +
			"To: " + to + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/plain; charset=UTF-8\r\n" +
			"\r\n" +
			body + "\r\n",
	)

	if m.cfg.Port == 465 {
		return m.sendTLS(addr, to, msg)
	}
	return m.sendSTARTTLS(addr, to, msg)
}

func (m *smtpMailer) sendTLS(addr, to string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: m.cfg.Host}
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("smtp.sendTLS dial: %w", err)
	}
	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp.sendTLS new client: %w", err)
	}
	defer client.Quit()
	if err := client.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
		return fmt.Errorf("smtp.sendTLS auth: %w", err)
	}
	return m.deliver(client, to, msg)
}

func (m *smtpMailer) sendSTARTTLS(addr, to string, msg []byte) error {
	host, _, _ := net.SplitHostPort(addr)
	auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, host)
	if err := smtp.SendMail(addr, auth, m.cfg.From, []string{to}, msg); err != nil {
		return fmt.Errorf("smtp.sendSTARTTLS: %w", err)
	}
	return nil
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
