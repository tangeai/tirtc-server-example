package logging

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func restoreDefault(t *testing.T) {
	t.Helper()
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
}

func TestNewLoggerWith_Level(t *testing.T) {
	ctx := context.Background()

	lg := newLoggerWith(&bytes.Buffer{}, "debug", "text")
	if !lg.Enabled(ctx, slog.LevelDebug) {
		t.Error("debug level should enable Debug")
	}

	lg = newLoggerWith(&bytes.Buffer{}, "info", "text")
	if lg.Enabled(ctx, slog.LevelDebug) {
		t.Error("info level should not enable Debug")
	}
	if !lg.Enabled(ctx, slog.LevelInfo) {
		t.Error("info level should enable Info")
	}

	lg = newLoggerWith(&bytes.Buffer{}, "warn", "text")
	if lg.Enabled(ctx, slog.LevelInfo) {
		t.Error("warn level should not enable Info")
	}
}

func TestContextHandler_RequestID(t *testing.T) {
	var buf bytes.Buffer
	lg := newLoggerWith(&buf, "debug", "text")
	ctx := WithRequestID(context.Background(), "abc123")
	lg.InfoContext(ctx, "hello")

	out := buf.String()
	if !strings.Contains(out, "request_id=abc123") {
		t.Errorf("want request_id=abc123 in output, got: %s", out)
	}
	if !strings.Contains(out, "msg=hello") {
		t.Errorf("want msg=hello, got: %s", out)
	}
}

func TestContextHandler_Service(t *testing.T) {
	var buf bytes.Buffer
	lg := newLoggerWith(&buf, "debug", "text")
	ctx := context.WithValue(context.Background(), serviceKey, "my-svc")
	lg.InfoContext(ctx, "hello")

	out := buf.String()
	if !strings.Contains(out, "service=my-svc") {
		t.Errorf("want service=my-svc in output, got: %s", out)
	}
}

func TestContextHandler_NoRequestID(t *testing.T) {
	var buf bytes.Buffer
	lg := newLoggerWith(&buf, "debug", "text")
	lg.Info("hello") // no ctx → must not add an empty request_id attr

	out := buf.String()
	if strings.Contains(out, "request_id=") {
		t.Errorf("should not contain request_id attr when none set, got: %s", out)
	}
}

func TestInit_SetsDefault(t *testing.T) {
	restoreDefault(t)
	Init("debug", "text")
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Init debug should set default logger to debug")
	}
}

func TestRequestIDMiddleware_GeneratesAndEchoes(t *testing.T) {
	r := gin.New()
	r.Use(RequestID("test"))
	r.GET("/", func(c *gin.Context) { c.Status(200) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Header().Get("X-Request-ID") == "" {
		t.Fatal("want non-empty X-Request-ID in response header")
	}
}

func TestRequestIDMiddleware_PassesThrough(t *testing.T) {
	r := gin.New()
	r.Use(RequestID("test"))
	r.GET("/", func(c *gin.Context) {
		if RequestIDFrom(c.Request.Context()) != "fixed-id" {
			t.Error("handler should see passed-through request id")
		}
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "fixed-id")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got != "fixed-id" {
		t.Errorf("want fixed-id echoed, got %s", got)
	}
}

// JSON POST: both request body and JSON response body are logged (Debug).
func TestBodyLog_JSONRequestResponse(t *testing.T) {
	restoreDefault(t)
	var buf bytes.Buffer
	slog.SetDefault(newLoggerWith(&buf, "debug", "text"))

	r := gin.New()
	r.Use(RequestID("test"), BodyLog())
	r.POST("/x", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x?foo=bar", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	out := buf.String()
	flat := strings.ReplaceAll(out, "\\", "")
	if !strings.Contains(flat, `{"a":1}`) {
		t.Errorf("want request body logged, got: %s", out)
	}
	if !strings.Contains(flat, `{"ok":true}`) {
		t.Errorf("want response body logged, got: %s", out)
	}
}

// Info level: bodies must not be logged.
func TestBodyLog_InfoLevelSkipsBody(t *testing.T) {
	restoreDefault(t)
	var buf bytes.Buffer
	slog.SetDefault(newLoggerWith(&buf, "info", "text"))

	r := gin.New()
	r.Use(RequestID("test"), BodyLog())
	r.POST("/x", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	out := buf.String()
	if strings.Contains(out, `"a":1`) || strings.Contains(out, `"ok":true`) {
		t.Errorf("info level should not log body, got: %s", out)
	}
}

// GET with JSON response: path/query logged, response body logged, no request body.
func TestBodyLog_GET_JSONResponse(t *testing.T) {
	restoreDefault(t)
	var buf bytes.Buffer
	slog.SetDefault(newLoggerWith(&buf, "debug", "text"))

	r := gin.New()
	r.Use(RequestID("test"), BodyLog())
	r.GET("/callers", func(c *gin.Context) { c.JSON(200, gin.H{"items": []int{1, 2}}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/callers?device_id=d1", nil)
	r.ServeHTTP(w, req)

	out := buf.String()
	if !strings.Contains(out, "/callers") {
		t.Errorf("want path logged, got: %s", out)
	}
	if !strings.Contains(out, "device_id=d1") {
		t.Errorf("want query logged, got: %s", out)
	}
	if !strings.Contains(out, "items") {
		t.Errorf("want response body logged for GET, got: %s", out)
	}
}

// File/binary response: summary only, body must not be logged.
func TestBodyLog_NonJSONResponseSkipsBody(t *testing.T) {
	restoreDefault(t)
	var buf bytes.Buffer
	slog.SetDefault(newLoggerWith(&buf, "debug", "text"))

	r := gin.New()
	r.Use(RequestID("test"), BodyLog())
	r.GET("/file", func(c *gin.Context) {
		c.Data(200, "application/octet-stream", []byte("BINARY\x00\x01CONTENT"))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/file", nil)
	r.ServeHTTP(w, req)

	out := buf.String()
	if strings.Contains(out, "BINARY") {
		t.Errorf("non-JSON response body should not be logged, got: %s", out)
	}
}

// POST with any Content-Type (e.g. text/plain): request body is still logged.
func TestBodyLog_AnyContentTypeLogsBody(t *testing.T) {
	restoreDefault(t)
	var buf bytes.Buffer
	slog.SetDefault(newLoggerWith(&buf, "debug", "text"))

	r := gin.New()
	r.Use(RequestID("test"), BodyLog())
	r.POST("/upload", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("raw-text-body"))
	req.Header.Set("Content-Type", "text/plain")
	r.ServeHTTP(w, req)

	out := buf.String()
	if !strings.Contains(out, "raw-text-body") {
		t.Errorf("POST body should be logged regardless of content type, got: %s", out)
	}
}

// Multipart file upload: request body is skipped (too large / not repeatable).
func TestBodyLog_MultipartSkipsBody(t *testing.T) {
	restoreDefault(t)
	var buf bytes.Buffer
	slog.SetDefault(newLoggerWith(&buf, "debug", "text"))

	r := gin.New()
	r.Use(RequestID("test"), BodyLog())
	r.POST("/upload", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.txt\"\r\n\r\nhello\r\n--boundary--"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	r.ServeHTTP(w, req)

	out := buf.String()
	if strings.Contains(out, "hello") {
		t.Errorf("multipart body should be skipped, got: %s", out)
	}
}

func TestBodyLog_TTSSkipsVerificationCode(t *testing.T) {
	restoreDefault(t)
	var buf bytes.Buffer
	slog.SetDefault(newLoggerWith(&buf, "debug", "text"))

	r := gin.New()
	r.Use(RequestID("device"), BodyLog())
	r.GET("/v1/device/tts", func(c *gin.Context) { c.Data(200, "audio/pcm", []byte{0, 0}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/device/tts?code=386236", nil)
	r.ServeHTTP(w, req)

	if strings.Contains(buf.String(), "386236") {
		t.Fatalf("TTS verification code leaked into debug log: %s", buf.String())
	}
}

// Large body is truncated to 4KB with a marker.
func TestBodyLog_TruncatesLargeBody(t *testing.T) {
	restoreDefault(t)
	var buf bytes.Buffer
	slog.SetDefault(newLoggerWith(&buf, "debug", "text"))

	r := gin.New()
	r.Use(RequestID("test"), BodyLog())
	r.POST("/x", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	big := strings.Repeat("a", 8192)
	body := `{"d":"` + big + `"}`

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	out := buf.String()
	if strings.Contains(out, strings.Repeat("a", 5000)) {
		t.Errorf("large body should be truncated, got output len=%d", len(out))
	}
	if !strings.Contains(out, "truncat") {
		t.Errorf("want truncation marker, got: %s", out[:min(200, len(out))])
	}
}
