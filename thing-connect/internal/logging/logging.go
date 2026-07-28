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
// level. Request bodies are recorded for all methods except GET/HEAD/DELETE;
// multipart uploads are skipped. Response bodies are only recorded for
// application/json content types (files/binaries get a summary). Bodies are
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
			reqAttrs = append(reqAttrs, slog.String("body", truncate(reqBody)))
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
			respAttrs = append(respAttrs, slog.String("body", truncate(rb.buf.String())))
		}
		logger.LogAttrs(ctx, slog.LevelDebug, "http response", respAttrs...)
	}
}

func queryForLog(c *gin.Context) string {
	query := c.Request.URL.Query()
	if c.Request.URL.Path == "/v1/device/tts" && query.Has("code") {
		query.Set("code", "[REDACTED]")
	}
	return query.Encode()
}

func readAndRestoreBody(c *gin.Context) string {
	if shouldSkipBody(c) || c.Request.Body == nil {
		return ""
	}
	b, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(b))
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
	return strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data")
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
		return "Bearer ***" + val[len(val)-8:]
	case name == "X-Signature" || name == "X-Internal-Key":
		if len(val) > 8 {
			return "***" + val[len(val)-8:]
		}
		return "***"
	default:
		return val
	}
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
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}
