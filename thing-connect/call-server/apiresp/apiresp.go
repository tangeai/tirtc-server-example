// Package apiresp is call-server's response envelope and business error codes.
// Success uses code:200, matching device-server/user-server/ai-server (the
// majority convention) rather than voip-server's code:0 — H5 talks to both
// user-server and call-server directly, so one success-code convention avoids
// per-service special-casing in the frontend.
package apiresp

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// JSON is the uniform business response body.
type JSON struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data,omitempty"`
}

// CodeOK is the success code (matches internal/apiresp.CodeOK).
const CodeOK = 200

// OK responds with a success envelope.
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, JSON{Code: CodeOK, Msg: "ok", Data: data})
}

// Fail responds with a business error (HTTP 200, business code in body).
func Fail(c *gin.Context, code int, msg string) {
	if code == ErrInternal {
		slog.ErrorContext(c.Request.Context(), "call request internal failure", "err", msg)
		msg = "服务器内部错误，请稍后重试"
	}
	c.AbortWithStatusJSON(http.StatusOK, JSON{Code: code, Msg: msg})
}

// FailWithData is Fail plus a data payload (e.g. an existing room_id).
func FailWithData(c *gin.Context, code int, msg string, data any) {
	if code == ErrInternal {
		slog.ErrorContext(c.Request.Context(), "call request internal failure", "err", msg)
		msg = "服务器内部错误，请稍后重试"
	}
	c.AbortWithStatusJSON(http.StatusOK, JSON{Code: code, Msg: msg, Data: data})
}

// Error codes — design doc §9.
const (
	ErrAuth               = 401
	ErrBadParam           = 40000
	ErrPeerOffline        = 40201
	ErrPeerBusy           = 40202
	ErrContactNotExist    = 40205
	ErrContactDuplicate   = 40206
	ErrContactPending     = 40207
	ErrContactDeleted     = 40208
	ErrContactMax         = 40209
	ErrAlreadyAnswered    = 40210
	ErrContactProtected   = 40211
	ErrForbidden          = 40300
	ErrInternalCredential = 40301
	ErrNotFound           = 40400
	ErrInternal           = 50000
)
