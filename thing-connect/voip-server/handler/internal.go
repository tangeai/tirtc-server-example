package handler

import (
	"github.com/gin-gonic/gin"

	"thing-connect/voip-server/apiresp"
)

type internalUnbindBody struct {
	DeviceID string `json:"device_id"`
}

// POST /v1/voip/internal/unbind — service-to-service only, guarded by X-Internal-Key.
func (s *Server) postInternalUnbind(c *gin.Context) {
	if s.Config().Internal.Key == "" || c.GetHeader("X-Internal-Key") != s.Config().Internal.Key {
		apiresp.Fail(c, apiresp.ErrInternalCredential, "内部服务凭证无效")
		return
	}
	var body internalUnbindBody
	if err := c.ShouldBindJSON(&body); err != nil || body.DeviceID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id")
		return
	}
	if s.cleanup == nil {
		apiresp.Fail(c, apiresp.ErrInternal, "VoIP cleanup is not configured")
		return
	}
	if err := s.cleanup.Unbind(c.Request.Context(), body.DeviceID); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	apiresp.OK(c, nil)
}
