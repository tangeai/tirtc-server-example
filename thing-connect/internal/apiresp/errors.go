package apiresp

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// errMapping maps service sentinel errors to (httpStatus, bizCode).
// Populated by RegisterError; service package calls this at init.
var errMapping []errEntry

type errEntry struct {
	target     error
	httpStatus int
	bizCode    int
}

// RegisterError registers a sentinel error → HTTP status + biz code mapping.
// Call this from service package's init() or from main before serving.
// Calling RegisterError with an already-registered target is a no-op.
func RegisterError(target error, httpStatus, bizCode int) {
	for _, e := range errMapping {
		if errors.Is(e.target, target) {
			return // already registered
		}
	}
	errMapping = append(errMapping, errEntry{target, httpStatus, bizCode})
}

// FromError maps a service error to the appropriate HTTP response.
// Falls back to 500 if no mapping is found.
func FromError(c *gin.Context, err error) {
	for _, e := range errMapping {
		if errors.Is(err, e.target) {
			c.JSON(e.httpStatus, JSON{Code: e.bizCode, Msg: err.Error()})
			return
		}
	}
	slog.ErrorContext(c.Request.Context(), "unmapped service error", "err", err)
	c.JSON(http.StatusInternalServerError, JSON{Code: 50000, Msg: "服务器内部错误，请稍后重试"})
}
