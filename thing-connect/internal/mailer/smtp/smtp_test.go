package smtp

import (
	"context"
	"errors"
	"mime"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBuildMessageEncodesUTF8Subject(t *testing.T) {
	const subject = "【TiRTC 体验平台】找回密码验证码"
	messageBytes, err := buildMessage("noreply@example.com", "user@example.com", subject, "邮件正文")
	if err != nil {
		t.Fatalf("buildMessage returned error: %v", err)
	}
	message := string(messageBytes)
	var subjectHeader string
	for _, line := range strings.Split(message, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			subjectHeader = strings.TrimPrefix(line, "Subject: ")
			break
		}
	}
	if subjectHeader == "" {
		t.Fatal("message has no Subject header")
	}
	if !strings.HasPrefix(subjectHeader, "=?UTF-8?") {
		t.Fatalf("subject is not MIME encoded: %q", subjectHeader)
	}
	decoded, err := (&mime.WordDecoder{}).DecodeHeader(subjectHeader)
	if err != nil {
		t.Fatalf("DecodeHeader returned error: %v", err)
	}
	if decoded != subject {
		t.Fatalf("decoded subject=%q, want %q", decoded, subject)
	}
	if !strings.Contains(message, "Content-Transfer-Encoding: quoted-printable\r\n") {
		t.Fatal("message body is not declared as quoted-printable")
	}
	if strings.Contains(message, "\r\n\r\n邮件正文") {
		t.Fatal("UTF-8 body was written without transfer encoding")
	}
}

func TestSendHonorsContextDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort returned error: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("Atoi returned error: %v", err)
	}
	mailer := New(Config{Host: host, Port: port, Username: "user", Password: "pass", From: "noreply@example.com"})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = mailer.Send(ctx, "user@example.com", "验证码", "正文")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send error=%v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Send ignored context deadline; elapsed=%v", elapsed)
	}
	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(time.Second):
		t.Fatal("SMTP test server did not accept the connection")
	}
}
