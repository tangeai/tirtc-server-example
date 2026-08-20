// Package logging configures the global slog logger and provides Gin
// middlewares for request-id propagation and request/response body logging
// (Debug level). Bodies are only recorded for application/json content types;
// file/binary requests and responses get a summary line without the body.
package logging

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type ctxKey string

const (
	requestIDKey    ctxKey = "request_id"
	serviceKey      ctxKey = "service"
	headerRequestID        = "X-Request-ID"
	maxBodyBytes           = 4 * 1024
)

// Init configures the global slog logger.
// level: debug|info|warn|error (default info). format: text|json (default text).
func Init(level, format string) {
	slog.SetDefault(newLoggerWith(os.Stdout, level, format))
}

// newLoggerWith builds a slog.Logger writing to w, wrapping the handler so a
// request_id stored in the context is attached to each record.
func newLoggerWith(w io.Writer, level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(&contextHandler{Handler: h})
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// contextHandler wraps a slog.Handler, attaching request_id from the context
// to every record so any *Context call carries it automatically.
type contextHandler struct{ slog.Handler }

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := ctx.Value(requestIDKey).(string); ok && id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	if svc, ok := ctx.Value(serviceKey).(string); ok && svc != "" {
		r.AddAttrs(slog.String("service", svc))
	}
	return h.Handler.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithGroup(name)}
}

// WithRequestID stores a request id in the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom extracts the request id from the context, if any.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// RequestID returns a Gin middleware that propagates or generates an
// X-Request-ID and stores it (together with service) in the request context
// for downstream slog logging. service is a short identifier for this server
// (e.g. "voip", "device") — every log line from this middleware and all
// downstream *Context calls will carry it automatically.
func RequestID(service string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(headerRequestID)
		if id == "" {
			id = newRequestID()
		}
		ctx := c.Request.Context()
		ctx = WithRequestID(ctx, id)
		ctx = context.WithValue(ctx, serviceKey, service)
		c.Request = c.Request.WithContext(ctx)
		c.Header(headerRequestID, id)
		c.Next()
	}
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405")))[:16]
	}
	return hex.EncodeToString(b[:])
}

// BodyLog is a Gin middleware that logs every request and response at Debug
// level. Only JSON bodies are eligible and credential-bearing fields are
// redacted recursively; authentication and configuration bodies are suppressed
// entirely. Files, forms and binaries get request metadata only. Bodies are
// truncated to 4KB. Below Debug level it is a no-op.
func BodyLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		logger := slog.Default()
		ctx := c.Request.Context()
		if !logger.Enabled(ctx, slog.LevelDebug) {
			c.Next()
			return
		}

		reqBody := readAndRestoreBody(c)
		reqAttrs := []slog.Attr{
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.String("query", queryForLog(c)),
			slog.String("headers", formatHeaders(c)),
		}
		if reqBody != "" {
			reqAttrs = append(reqAttrs, slog.String("body", bodyForLog(c.Request.URL.Path, reqBody)))
		}
		logger.LogAttrs(ctx, slog.LevelDebug, "http request", reqAttrs...)

		rb := &responseBodyWriter{ResponseWriter: c.Writer, buf: &bytes.Buffer{}}
		c.Writer = rb
		start := time.Now()
		c.Next()

		respAttrs := []slog.Attr{
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Int("dur_ms", int(time.Since(start).Milliseconds())),
		}
		if isJSON(c.Writer.Header().Get("Content-Type")) {
			respAttrs = append(respAttrs, slog.String("body", bodyForLog(c.Request.URL.Path, rb.buf.String())))
		}
		logger.LogAttrs(ctx, slog.LevelDebug, "http response", respAttrs...)
	}
}

func queryForLog(c *gin.Context) string {
	query := c.Request.URL.Query()
	for key := range query {
		if isSensitiveKey(key) || c.Request.URL.Path == "/v1/device/tts" && strings.EqualFold(key, "code") {
			query.Set(key, "[REDACTED]")
		}
	}
	return query.Encode()
}

func readAndRestoreBody(c *gin.Context) string {
	if shouldSkipBody(c) || c.Request.Body == nil {
		return ""
	}
	original := c.Request.Body
	b, err := io.ReadAll(io.LimitReader(original, maxBodyBytes+1))
	c.Request.Body = &replayBody{Reader: io.MultiReader(bytes.NewReader(b), original), Closer: original}
	if err != nil {
		return ""
	}
	return string(b)
}

func shouldSkipBody(c *gin.Context) bool {
	// Keep TTS excluded if its request shape changes in the future. Its current
	// GET query is handled separately by queryForLog.
	if c.Request.URL.Path == "/v1/device/tts" {
		return true
	}
	switch c.Request.Method {
	case "GET", "HEAD", "DELETE":
		return true
	}
	// Only structured JSON can be redacted safely. Multipart, form and binary
	// payloads are summarized by the normal request metadata instead.
	return !isJSON(c.GetHeader("Content-Type"))
}

func isJSON(contentType string) bool {
	return strings.Contains(contentType, "application/json")
}

// headersToLog lists the HTTP headers that appear in debug request logs.
var headersToLog = []string{
	"Authorization",
	"Content-Type",
	"X-Device-Id",
	"X-Timestamp",
	"X-Nonce",
	"X-Signature",
	"X-MAC",
	"X-Internal-Key",
	"X-Request-ID",
}

// formatHeaders builds a compact string of interesting headers with sensitive
// values masked (Bearer tokens, signatures, internal keys).
func formatHeaders(c *gin.Context) string {
	var parts []string
	for _, name := range headersToLog {
		v := c.GetHeader(name)
		if v == "" {
			continue
		}
		parts = append(parts, name+"="+maskHeader(name, v))
	}
	return strings.Join(parts, " ")
}

func maskHeader(name, val string) string {
	switch {
	case name == "Authorization" && strings.HasPrefix(val, "Bearer "):
		token := strings.TrimPrefix(val, "Bearer ")
		if len(token) > 8 {
			return "Bearer ***" + token[len(token)-8:]
		}
		return "Bearer ***"
	case name == "X-Signature" || name == "X-Internal-Key" || name == "X-MAC":
		if len(val) > 8 {
			return "***" + val[len(val)-8:]
		}
		return "***"
	default:
		return val
	}
}

// RedactJSON removes credential-bearing fields recursively before structured
// data is written to logs or durable audit records. Invalid JSON is never
// returned verbatim because it cannot be redacted reliably.
func RedactJSON(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "[UNAVAILABLE]"
	}
	redactValue(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return "[UNAVAILABLE]"
	}
	return truncate(string(encoded))
}

func bodyForLog(path, raw string) string {
	if sensitiveBodyPath(path) {
		return "[REDACTED]"
	}
	return RedactJSON(raw)
}

func sensitiveBodyPath(path string) bool {
	if strings.HasPrefix(path, "/v1/internal/configs/") || strings.HasPrefix(path, "/v1/admin/configs/") {
		return true
	}
	switch path {
	case "/v1/admin/auth/login",
		"/v1/admin/auth/mfa/verify",
		"/v1/admin/auth/refresh",
		"/v1/admin/auth/logout",
		"/v1/admin/me/password",
		"/v1/admin/me/mfa/totp/enroll",
		"/v1/admin/me/mfa/totp/confirm",
		"/v1/admin/me/mfa/recovery-codes/regenerate":
		return true
	default:
		return false
	}
}

func redactValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isSensitiveKey(key) {
				typed[key] = "[REDACTED]"
				continue
			}
			redactValue(child)
		}
	case []any:
		for _, child := range typed {
			redactValue(child)
		}
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	for _, fragment := range []string{"password", "secret", "token", "recovery_code", "mfa_code", "device_key", "internal_key", "authorization", "otpauth_uri"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return normalized == "recovery_codes" || normalized == "access_key_id" || normalized == "secret_key_id"
}

func truncate(s string) string {
	if len(s) <= maxBodyBytes {
		return s
	}
	return s[:maxBodyBytes] + "...(truncated)"
}

type responseBodyWriter struct {
	gin.ResponseWriter
	buf *bytes.Buffer
}

func (w *responseBodyWriter) Write(b []byte) (int, error) {
	if remaining := maxBodyBytes + 1 - w.buf.Len(); remaining > 0 {
		if remaining > len(b) {
			remaining = len(b)
		}
		_, _ = w.buf.Write(b[:remaining])
	}
	return w.ResponseWriter.Write(b)
}

type replayBody struct {
	io.Reader
	io.Closer
}
