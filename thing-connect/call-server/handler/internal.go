package handler

import (
	"github.com/gin-gonic/gin"

	"thing-connect/call-server/apiresp"
)

type internalUnbindBody struct {
	DeviceID string `json:"device_id"`
}

// POST /v1/call/internal/unbind — service-to-service only, guarded by a shared
// X-Internal-Key header (design doc §6.8 doesn't specify an auth mechanism for
// this one; the other server-to-server calls in this codebase don't have a
// precedent either, so this is a deliberate addition).
func (s *Server) postInternalUnbind(c *gin.Context) {
	if s.cfg.Internal.Key == "" || c.GetHeader("X-Internal-Key") != s.cfg.Internal.Key {
		apiresp.Fail(c, apiresp.ErrInternalCredential, "内部服务凭证无效")
		return
	}
	var body internalUnbindBody
	if err := c.ShouldBindJSON(&body); err != nil || body.DeviceID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id")
		return
	}
	ctx := c.Request.Context()

	if roomID, err := s.rdb.Get(ctx, lockKey(body.DeviceID)).Result(); err == nil && roomID != "" {
		if rm, err := s.getRoom(ctx, roomID); err == nil && rm != nil {
			var notify []string
			switch {
			case rm.Caller == body.DeviceID:
				notify = rm.Targets
			case rm.AnsweredBy == body.DeviceID:
				notify = []string{rm.Caller}
			}
			notify = removeString(notify, body.DeviceID)
			_ = s.releaseRoom(ctx, rm, "unbound", notify)
		}
	}

	peers, err := s.contactPeers(ctx, body.DeviceID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if err := s.deleteAllContacts(ctx, body.DeviceID); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	for _, peer := range peers {
		s.publishCallersUpdate(peer, "delete", "device", body.DeviceID)
	}

	apiresp.OK(c, nil)
}
