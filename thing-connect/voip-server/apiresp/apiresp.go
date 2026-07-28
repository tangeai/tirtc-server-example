package apiresp

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// JSON 统一业务响应体。
type JSON struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data,omitempty"`
}

// OK 成功。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, JSON{Code: 0, Msg: "ok", Data: data})
}

// Fail 失败（仍 HTTP 200 与 code 业务码）。
func Fail(c *gin.Context, code int, msg string) {
	switch code {
	case ErrInternal:
		slog.ErrorContext(c.Request.Context(), "voip request internal failure", "err", msg)
		msg = "服务器内部错误，请稍后重试"
	case ErrWechatAPI, ErrTirtcServerAPI:
		slog.WarnContext(c.Request.Context(), "voip upstream request failure", "code", code, "err", msg)
		if code == ErrWechatAPI {
			msg = "微信接口调用失败，请稍后重试"
		} else {
			msg = "实时音视频服务调用失败，请稍后重试"
		}
	}
	c.AbortWithStatusJSON(http.StatusOK, JSON{Code: code, Msg: msg})
}

const (
	ErrAuth               = 401
	ErrBadParam           = 40000
	ErrWechatLoginInvalid = 40203
	ErrVoipAuthInvalid    = 40205
	ErrForbidden          = 40300
	ErrInternalCredential = 40301
	ErrNotFound           = 40400
	ErrConflict           = 40900
	ErrInternal           = 50000
	ErrWechatCfg          = 50001
	ErrWechatAPI          = 50002
	ErrTirtcServerAPI     = 50003
	ErrDeviceUnbound      = 6006
)
