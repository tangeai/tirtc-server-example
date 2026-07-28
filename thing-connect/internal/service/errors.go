package service

import (
	"errors"
	"fmt"
)

type detailedError struct {
	msg    string
	parent error
}

func (e *detailedError) Error() string { return e.msg }
func (e *detailedError) Unwrap() error { return e.parent }

var (
	ErrCodeExpired   = errors.New("验证码已过期或不存在")
	ErrAlreadyBound  = errors.New("设备已绑定至当前账号")
	ErrBoundByOther  = errors.New("设备已被其他用户绑定")
	ErrQuotaEmpty    = errors.New("可用额度已耗尽")
	ErrDeviceOffline = errors.New("设备离线")
	ErrMQTTTimeout   = errors.New("MQTT消息下发超时")
	ErrCloneAttack   = errors.New("硬件物理标识不匹配，疑似克隆设备攻击")
	ErrSigFail       = errors.New("签名校验失败")
	// Timestamp errors still wrap ErrSigFail so existing clients continue to
	// receive HTTP 401 / code 6008 while msg provides an actionable reason.
	ErrSigTimestampInvalid = fmt.Errorf(
		"%w：时间戳格式错误，应使用 Unix 秒级时间戳", ErrSigFail)
	ErrSigTimestampSkew = fmt.Errorf(
		"%w：设备与服务器时间偏差超过 300 秒，请校准设备时钟", ErrSigFail)
	ErrSigTimestampTooOld = &detailedError{
		msg:    "签名校验失败：设备时间戳早于服务器时间超过 300 秒，请校准设备时钟",
		parent: ErrSigTimestampSkew,
	}
	ErrSigTimestampTooNew = &detailedError{
		msg:    "签名校验失败：设备时间戳晚于服务器时间超过 300 秒，请校准设备时钟",
		parent: ErrSigTimestampSkew,
	}
	ErrSigFieldsMissing = fmt.Errorf(
		"%w：签名请求头不完整，请检查 X-Device-Id、X-Timestamp、X-Nonce 和 X-Signature", ErrSigFail)
	ErrSigNonceReplay = fmt.Errorf(
		"%w：Nonce 已使用，请重新生成随机 Nonce 后重试", ErrSigFail)
	ErrDeviceReset    = errors.New("设备已恢复出厂重置")
	ErrUserExists     = errors.New("该邮箱已注册")
	ErrInvalidCreds   = errors.New("账号或密码凭证无效")
	ErrDeviceNotFound = errors.New("未查询到该设备")
	ErrRateLimit      = errors.New("请求过于频繁，请稍后再试")
	ErrVerifyPending  = errors.New("已有验证流程正在进行中")
	ErrCaptchaFailed  = errors.New("图形验证码校验失败")
	ErrInvalidCode    = errors.New("验证码错误或已过期")

	ErrEmptyFingerprint = errors.New("设备指纹三字段全空，至少填一个")
	ErrCloneConflict    = errors.New("设备指纹不匹配，疑似克隆")
	ErrPoolExhausted    = errors.New("设备池已耗尽，请联系管理员补充")

	ErrIPFingerprintLimit = errors.New("单 IP 新设备请求过多，请稍后再试")
	ErrGlobalBusy         = errors.New("系统繁忙，请稍后再试")

	ErrFingerprintMismatch = errors.New("设备指纹不匹配，请确认设备身份")
	ErrDeviceIDUntrusted   = errors.New("设备ID不可信，请升级固件支持签名验证")
	ErrMACDuplicateBinding = errors.New("该MAC已绑定至你名下其它设备")

	ErrDeviceReportProofMissing = fmt.Errorf(
		"%w：设备签名上报记录不存在或已过期，请重新上报", ErrDeviceOffline)
	ErrDevicePendingBindMissing = fmt.Errorf(
		"%w：设备待绑定状态不存在或已过期，请重新上报", ErrDeviceOffline)
	ErrDeviceMQTTOffline = fmt.Errorf(
		"%w：设备临时 MQTT 连接已断开，请重新连接后重试", ErrDeviceOffline)

	ErrMQTTPublishFailed = fmt.Errorf(
		"%w：MQTT 消息发布失败，请稍后重试", ErrMQTTTimeout)
	ErrMQTTAckTimeout = fmt.Errorf(
		"%w：设备未在规定时间内确认授权消息，请保持连接后重试", ErrMQTTTimeout)
)
