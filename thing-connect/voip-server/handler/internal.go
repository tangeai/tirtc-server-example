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
	ctx := c.Request.Context()

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	defer tx.Rollback()
	var contacts []struct {
		WxAppID  string `db:"wx_app_id"`
		WxOpenID string `db:"wx_open_id"`
	}
	if err := tx.SelectContext(ctx, &contacts,
		`SELECT wx_app_id, wx_open_id FROM voip_device_auth WHERE device_id=?`,
		body.DeviceID); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE device_bind SET device_name='' WHERE device_id=?`,
		body.DeviceID); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM voip_device_profile WHERE device_id=?`,
		body.DeviceID); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM voip_device_auth WHERE device_id=?`,
		body.DeviceID); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	deviceGuardKey := deviceCallGuardKey(body.DeviceID)
	callID, callIDErr := s.rdb.Get(ctx, deviceGuardKey).Result()
	if callIDErr == nil && callID != "" {
		for _, contact := range contacts {
			s.ReleaseOutgoingCallGuards(
				ctx,
				contact.WxAppID,
				body.DeviceID,
				contact.WxOpenID,
				callID,
			)
		}
	}
	_ = s.rdb.Del(ctx, deviceGuardKey).Err()
	s.publishAuthUpdate(ctx, body.DeviceID)
	apiresp.OK(c, nil)
}
