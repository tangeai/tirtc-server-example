package handler

import (
	"database/sql"
	"errors"

	"github.com/gin-gonic/gin"

	"thing-connect/call-server/apiresp"
)

// GET /v1/call/device/contacts
// Returns both device contacts (from call_contact) and VoIP contacts
// (from voip_device_auth — WeChat mini-program authorized users).
// Each contact has a "type" field: "device" or "voip".
func (s *Server) getDeviceContacts(c *gin.Context) {
	self := currentDeviceID(c)
	ctx := c.Request.Context()

	rows, err := s.listContacts(ctx, self)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		peer := row.peer(self)
		out = append(out, gin.H{
			"device_id": peer,
			"type":      "device",
			"remark":    row.remarkFor(self),
			"source":    row.Source,
			"online":    s.isOnline(ctx, peer),
		})
	}

	// VoIP contacts: WeChat mini-program authorized callers.
	// No request/accept flow — they appear automatically once authorized.
	voipRows, err := s.listVoipContacts(ctx, self)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	for _, v := range voipRows {
		out = append(out, gin.H{
			"id":          v.ID,
			"device_id":   v.WxOpenID,
			"type":        "voip",
			"source":      "voip",
			"remark":      v.Remark,
			"wx_open_id":  v.WxOpenID,
			"wx_app_id":   v.WxAppID,
			"wx_model_id": v.WxModelID,
		})
	}

	apiresp.OK(c, gin.H{"contacts": out})
}

// GET /v1/call/device/contacts/pending — requests this device may approve.
func (s *Server) getDeviceContactsPending(c *gin.Context) {
	self := currentDeviceID(c)
	rows, err := s.listPendingContacts(c.Request.Context(), []string{self})
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"type":           "device",
			"peer_device_id": row.peer(self),
			"created_at":     row.CreatedAt,
		})
	}
	apiresp.OK(c, gin.H{"pending": out})
}

type contactRequestBody struct {
	TargetDeviceID string `json:"target_device_id"`
}

// POST /v1/call/device/contacts/request
func (s *Server) postDeviceContactRequest(c *gin.Context) {
	self := currentDeviceID(c)
	var body contactRequestBody
	if err := c.ShouldBindJSON(&body); err != nil || body.TargetDeviceID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 target_device_id")
		return
	}
	if body.TargetDeviceID == self {
		apiresp.Fail(c, apiresp.ErrBadParam, "不能将自己添加为联系人")
		return
	}
	ctx := c.Request.Context()

	selfBind, err := s.dev.GetBindByDeviceID(ctx, self)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	targetBind, err := s.dev.GetBindByDeviceID(ctx, body.TargetDeviceID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if selfBind == nil || targetBind == nil {
		apiresp.Fail(c, apiresp.ErrNotFound, "目标设备不存在")
		return
	}

	// Same-account pairs auto-link instead of going through the request flow.
	if selfBind.UserID != 0 && selfBind.UserID == targetBind.UserID {
		if err := s.upsertAutoContact(ctx, self, body.TargetDeviceID, selfBind.UserID, targetBind.UserID); err != nil {
			apiresp.Fail(c, apiresp.ErrInternal, err.Error())
			return
		}
		s.publishCallersUpdate(body.TargetDeviceID, "accept", "device", self)
		apiresp.OK(c, gin.H{"status": "accepted", "source": "auto"})
		return
	}

	n, err := s.countAcceptedContacts(ctx, self)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if n >= s.Config().Service.MaxContactsPerDevice {
		apiresp.Fail(c, apiresp.ErrContactMax, "联系人数量已达上限")
		return
	}

	row, err := s.createRequest(ctx, self, body.TargetDeviceID, selfBind.UserID, targetBind.UserID)
	if err != nil {
		apiresp.Fail(c, contactErrCode(err), err.Error())
		return
	}
	s.publishCallersUpdate(body.TargetDeviceID, "request", "device", self)
	apiresp.OK(c, gin.H{"status": statusName(row.Status), "source": row.Source})
}

type contactRespondBody struct {
	PeerDeviceID string `json:"peer_device_id"`
	Action       string `json:"action"` // "accept" | "reject"
}

// POST /v1/call/device/contacts/respond
func (s *Server) postDeviceContactRespond(c *gin.Context) {
	self := currentDeviceID(c)
	var body contactRespondBody
	if err := c.ShouldBindJSON(&body); err != nil || body.PeerDeviceID == "" || (body.Action != "accept" && body.Action != "reject") {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 peer_device_id，或 action 不是 accept/reject")
		return
	}
	ctx := c.Request.Context()

	row, err := s.respondRequest(ctx, self, body.PeerDeviceID, body.Action == "accept")
	if err != nil {
		apiresp.Fail(c, contactErrCode(err), err.Error())
		return
	}
	s.publishCallersUpdate(body.PeerDeviceID, body.Action, "device", self)
	apiresp.OK(c, gin.H{"status": statusName(row.Status)})
}

type deviceRemarkBody struct {
	PeerID string `json:"peer_id"`
	Remark string `json:"remark"`
}

// DELETE /v1/call/device/contacts?peer_id=xxx — remove a device contact.
// Soft-deletes the call_contact row, so both devices lose the contact.
// VoIP (WeChat) contacts are out of scope: their lifecycle is driven by
// authorization, not by device-side delete.
func (s *Server) deleteDeviceContact(c *gin.Context) {
	self := currentDeviceID(c)
	peerID := c.Query("peer_id")
	if peerID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 peer_id")
		return
	}
	if err := s.deleteContact(c.Request.Context(), self, peerID); err != nil {
		apiresp.Fail(c, contactErrCode(err), err.Error())
		return
	}
	s.publishCallersUpdate(peerID, "delete", "device", self)
	apiresp.OK(c, nil)
}

// PUT /v1/call/device/contacts/remark — 统一修改设备联系人或 VoIP 联系人备注
func (s *Server) putDeviceContactRemark(c *gin.Context) {
	self := currentDeviceID(c)
	var body deviceRemarkBody
	if err := c.ShouldBindJSON(&body); err != nil || body.PeerID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 peer_id")
		return
	}
	if !validContactRemark(body.Remark) {
		apiresp.Fail(c, apiresp.ErrBadParam, "remark 不能超过 64 个字符")
		return
	}
	ctx := c.Request.Context()

	// 先尝试设备联系人
	row, err := s.getContactRow(ctx, self, body.PeerID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if row != nil && row.Status == contactStatusAccepted {
		if err := s.setRemark(ctx, self, body.PeerID, body.Remark); err != nil {
			apiresp.Fail(c, contactErrCode(err), err.Error())
			return
		}
		apiresp.OK(c, nil)
		return
	}

	// 再尝试 VoIP 联系人（peer_id = wx_open_id）
	var voipID int64
	if err := s.db.GetContext(ctx, &voipID,
		`SELECT id FROM voip_device_auth
			  WHERE device_id=? AND wx_open_id=? AND auth_status='active'
		  ORDER BY created_at DESC, id DESC LIMIT 1`,
		self, body.PeerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			apiresp.Fail(c, apiresp.ErrContactNotExist, "联系人不存在")
		} else {
			apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		}
		return
	}
	deviceIDs, err := s.setVoipRemark(ctx, voipID, body.Remark)
	if err != nil {
		apiresp.Fail(c, contactErrCode(err), err.Error())
		return
	}
	for _, deviceID := range deviceIDs {
		s.publishCallersUpdate(deviceID, "remark", "voip", body.PeerID)
	}
	apiresp.OK(c, nil)
}

func statusName(status int8) string {
	switch status {
	case contactStatusPending:
		return "pending"
	case contactStatusAccepted:
		return "accepted"
	case contactStatusRejected:
		return "rejected"
	case contactStatusDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

func contactErrCode(err error) int {
	switch {
	case errors.Is(err, errContactNotExist):
		return apiresp.ErrContactNotExist
	case errors.Is(err, errContactDuplicate):
		return apiresp.ErrContactDuplicate
	case errors.Is(err, errContactPending):
		return apiresp.ErrContactPending
	case errors.Is(err, errContactProtected):
		return apiresp.ErrContactProtected
	case errors.Is(err, errContactMax):
		return apiresp.ErrContactMax
	default:
		return apiresp.ErrInternal
	}
}
