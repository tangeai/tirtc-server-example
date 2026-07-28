package service

import (
	"net/http"

	"thing-connect/internal/apiresp"
)

func RegisterErrors() {
	apiresp.RegisterError(ErrCodeExpired, http.StatusBadRequest, apiresp.CodeCodeExpired)
	apiresp.RegisterError(ErrAlreadyBound, http.StatusConflict, apiresp.CodeDevBound)
	apiresp.RegisterError(ErrBoundByOther, http.StatusConflict, apiresp.CodeDevOtherBound)
	apiresp.RegisterError(ErrQuotaEmpty, http.StatusUnprocessableEntity, apiresp.CodeQuotaEmpty)
	apiresp.RegisterError(ErrDeviceOffline, http.StatusServiceUnavailable, apiresp.CodeDevOffline)
	apiresp.RegisterError(ErrMQTTTimeout, http.StatusGatewayTimeout, apiresp.CodeMQTTTimeout)
	apiresp.RegisterError(ErrCloneAttack, http.StatusForbidden, apiresp.CodeCloneAttack)
	apiresp.RegisterError(ErrSigFail, http.StatusUnauthorized, apiresp.CodeSigFail)
	apiresp.RegisterError(ErrDeviceReset, http.StatusGone, apiresp.CodeDevReset)
	apiresp.RegisterError(ErrUserExists, http.StatusConflict, apiresp.CodeUserExists)
	apiresp.RegisterError(ErrInvalidCreds, http.StatusUnauthorized, apiresp.CodeInvalidCreds)
	apiresp.RegisterError(ErrDeviceNotFound, http.StatusNotFound, apiresp.CodeDeviceNotFound)
	apiresp.RegisterError(ErrRateLimit, http.StatusTooManyRequests, apiresp.CodeRateLimit)
	apiresp.RegisterError(ErrVerifyPending, http.StatusConflict, apiresp.CodeVerifyPending)
	apiresp.RegisterError(ErrCaptchaFailed, http.StatusBadRequest, apiresp.CodeCaptchaFailed)
	apiresp.RegisterError(ErrInvalidCode, http.StatusBadRequest, apiresp.CodeInvalidCode)
	apiresp.RegisterError(ErrEmptyFingerprint, http.StatusBadRequest, apiresp.CodeEmptyFingerprint)
	apiresp.RegisterError(ErrCloneConflict, http.StatusForbidden, apiresp.CodeCloneConflict)
	apiresp.RegisterError(ErrPoolExhausted, http.StatusServiceUnavailable, apiresp.CodePoolExhausted)
	apiresp.RegisterError(ErrIPFingerprintLimit, http.StatusTooManyRequests, apiresp.CodeRateLimit)
	apiresp.RegisterError(ErrGlobalBusy, http.StatusTooManyRequests, apiresp.CodeRateLimit)
	apiresp.RegisterError(ErrFingerprintMismatch, http.StatusForbidden, apiresp.CodeFingerprintMismatch)
	apiresp.RegisterError(ErrDeviceIDUntrusted, http.StatusBadRequest, apiresp.CodeDeviceIDUntrusted)
	apiresp.RegisterError(ErrMACDuplicateBinding, http.StatusConflict, apiresp.CodeMACDuplicateBinding)
}
