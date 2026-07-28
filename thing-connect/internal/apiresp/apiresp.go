package apiresp

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Business error codes (PRD §10.2)
const (
	CodeOK                  = 200
	CodeBadCode             = 4001  // 验证码错误
	CodeCodeExpired         = 4002  // 验证码已过期
	CodeQuotaEmpty          = 4003  // 配额已用完
	CodeRateLimit           = 429   // 请求过频繁
	CodeMQTTTimeout         = 5001  // MQTT 下发超时
	CodeDevOffline          = 6002  // 设备 MQTT 离线
	CodeDevBound            = 6003  // 设备已绑定（本账号）
	CodeDevOtherBound       = 6004  // 设备已被其他用户绑定
	CodeCloneAttack         = 6005  // 物理标识不匹配
	CodeDevReset            = 6006  // 设备已被重置
	CodeSigFail             = 6008  // 签名校验失败
	CodeIDOccupied          = 6009  // 设备 ID 被其他 MAC 占用
	CodeEmptyFingerprint    = 6010  // 指纹三字段全空
	CodeCloneConflict       = 6011  // 绑定中设备指纹不符（克隆）
	CodePoolExhausted       = 6012  // 设备池耗尽
	CodeFingerprintMismatch = 6013  // 设备指纹不匹配（情况1）
	CodeDeviceIDUntrusted   = 6014  // 设备ID不可信（情况3）
	CodeMACDuplicateBinding = 6015  // 同MAC已绑定至本账号其它设备
	CodeUserExists          = 4090  // 邮箱已注册
	CodeInvalidCreds        = 4091  // 用户名或密码错误
	CodeDeviceNotFound      = 4040  // 设备不存在
	CodeVerifyPending       = 40901 // 验证码已在进行中
	CodeCaptchaFailed       = 40012 // captcha verification failed
	CodeInvalidCode         = 40013 // invalid or expired verification code
)

type JSON struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data,omitempty"`
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, JSON{Code: CodeOK, Msg: "ok", Data: data})
}

func Fail(c *gin.Context, httpStatus, bizCode int, msg string) {
	c.JSON(httpStatus, JSON{Code: bizCode, Msg: msg})
}

func BadParam(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, JSON{Code: 40000, Msg: msg})
}

// BadParamError logs the parser/validator detail and returns a stable,
// user-facing Chinese message. Validation library errors are implementation
// details and are commonly English, so they must not be copied to the API.
func BadParamError(c *gin.Context, err error) {
	slog.WarnContext(c.Request.Context(), "request parameter validation failed", "err", err)
	BadParam(c, "请求参数格式错误，请检查必填字段和字段类型")
}

func Internal(c *gin.Context, msg string) {
	slog.ErrorContext(c.Request.Context(), "internal request failure", "err", msg)
	c.JSON(http.StatusInternalServerError, JSON{Code: 50000, Msg: "服务器内部错误，请稍后重试"})
}

func Unauthorized(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, JSON{Code: 401, Msg: "未授权或登录凭证无效"})
}
