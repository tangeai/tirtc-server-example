package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"thing-connect/call-server/apiresp"
)

// userDeviceIDs returns every device_id bound to userID.
func (s *Server) userDeviceIDs(ctx context.Context, userID int64) ([]string, error) {
	var ids []string
	if err := s.db.SelectContext(ctx, &ids, `SELECT device_id FROM device_bind WHERE user_id=?`, userID); err != nil {
		return nil, fmt.Errorf("userDeviceIDs: %w", err)
	}
	return ids, nil
}

func (s *Server) deviceBelongsToUser(ctx context.Context, deviceID string, userID int64) (bool, error) {
	bind, err := s.dev.GetBindByDeviceID(ctx, deviceID)
	if err != nil {
		return false, err
	}
	return bind != nil && bind.UserID == userID, nil
}

func (s *Server) getContactByID(ctx context.Context, id int64) (*deviceContact, error) {
	var row deviceContact
	if err := s.db.GetContext(ctx, &row, `SELECT * FROM call_contact WHERE id=?`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil //nolint:nilnil // No row means the optional contact does not exist.
		}
		return nil, fmt.Errorf("getContactByID: %w", err)
	}
	return &row, nil
}

// contactSideForUser resolves the side currently owned by userID. For a
// same-account pair both sides are owned; choosing A preserves the legacy ID
// endpoint semantics. Auto contacts cannot be deleted.
func (s *Server) contactSideForUser(ctx context.Context, row *deviceContact, userID int64) (self, peer string, ok bool, err error) {
	ownedA, err := s.deviceBelongsToUser(ctx, row.DeviceIDA, userID)
	if err != nil {
		return "", "", false, err
	}
	if ownedA {
		return row.DeviceIDA, row.DeviceIDB, true, nil
	}
	ownedB, err := s.deviceBelongsToUser(ctx, row.DeviceIDB, userID)
	if err != nil {
		return "", "", false, err
	}
	if ownedB {
		return row.DeviceIDB, row.DeviceIDA, true, nil
	}
	return "", "", false, nil
}

// GET /v1/call/user/contacts?device_id=xxx — full contact list for one of the
// current user's devices (same shape as the device-side GET /v1/call/device/contacts).
func (s *Server) getUserContacts(c *gin.Context) {
	userID := currentUserID(c)
	deviceID := c.Query("device_id")
	if deviceID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id")
		return
	}
	ctx := c.Request.Context()

	ok, err := s.deviceBelongsToUser(ctx, deviceID, userID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if !ok {
		apiresp.Fail(c, apiresp.ErrForbidden, "设备不属于当前用户")
		return
	}
	rows, err := s.listContacts(ctx, deviceID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		peer := row.peer(deviceID)
		out = append(out, gin.H{
			"id":        row.ID,
			"device_id": peer,
			"type":      "device",
			"remark":    row.remarkFor(deviceID),
			"source":    row.Source,
			"online":    s.isOnline(ctx, peer),
		})
	}

	// VoIP contacts
	voipRows, err := s.listVoipContacts(ctx, deviceID)
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

// GET /v1/call/user/contacts/pending — requests awaiting approval on any of
// this user's devices (i.e. this user's device is the non-initiating side).
func (s *Server) getUserContactsPending(c *gin.Context) {
	userID := currentUserID(c)
	ctx := c.Request.Context()

	ids, err := s.userDeviceIDs(ctx, userID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if len(ids) == 0 {
		apiresp.OK(c, gin.H{"pending": []gin.H{}})
		return
	}

	rows, err := s.listPendingContacts(ctx, ids)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		initiatorDevice := row.DeviceIDA
		if row.Initiator == "b" {
			initiatorDevice = row.DeviceIDB
		}
		responderDevice := row.peer(initiatorDevice)
		out = append(out, gin.H{
			"id":               row.ID,
			"type":             "device",
			"initiator_device": initiatorDevice,
			"target_device":    responderDevice,
			"created_at":       row.CreatedAt,
		})
	}
	apiresp.OK(c, gin.H{"pending": out})
}

type userContactRequestBody struct {
	DeviceID       string `json:"device_id"`
	TargetDeviceID string `json:"target_device_id"`
}

// POST /v1/call/user/contacts/request
func (s *Server) postUserContactRequest(c *gin.Context) {
	userID := currentUserID(c)
	var body userContactRequestBody
	if err := c.ShouldBindJSON(&body); err != nil || body.DeviceID == "" || body.TargetDeviceID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id 或 target_device_id")
		return
	}
	ctx := c.Request.Context()

	ok, err := s.deviceBelongsToUser(ctx, body.DeviceID, userID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if !ok {
		apiresp.Fail(c, apiresp.ErrForbidden, "设备不属于当前用户")
		return
	}
	if body.TargetDeviceID == body.DeviceID {
		apiresp.Fail(c, apiresp.ErrBadParam, "不能将自己添加为联系人")
		return
	}

	targetBind, err := s.dev.GetBindByDeviceID(ctx, body.TargetDeviceID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if targetBind == nil {
		apiresp.Fail(c, apiresp.ErrNotFound, "目标设备不存在")
		return
	}

	if targetBind.UserID != 0 && targetBind.UserID == userID {
		if err := s.upsertAutoContact(ctx, body.DeviceID, body.TargetDeviceID, userID, targetBind.UserID); err != nil {
			apiresp.Fail(c, apiresp.ErrInternal, err.Error())
			return
		}
		s.publishCallersUpdate(body.DeviceID, "accept", "device", body.TargetDeviceID)
		s.publishCallersUpdate(body.TargetDeviceID, "accept", "device", body.DeviceID)
		apiresp.OK(c, gin.H{"status": "accepted", "source": "auto"})
		return
	}

	n, err := s.countAcceptedContacts(ctx, body.DeviceID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if n >= s.Config().Service.MaxContactsPerDevice {
		apiresp.Fail(c, apiresp.ErrContactMax, "联系人数量已达上限")
		return
	}

	row, err := s.createRequest(ctx, body.DeviceID, body.TargetDeviceID, userID, targetBind.UserID)
	if err != nil {
		apiresp.Fail(c, contactErrCode(err), err.Error())
		return
	}
	s.publishCallersUpdate(body.TargetDeviceID, "request", "device", body.DeviceID)
	apiresp.OK(c, gin.H{"status": statusName(row.Status), "source": row.Source})
}

type userContactRespondBody struct {
	ID     int64  `json:"id"`
	Action string `json:"action"` // "accept" | "reject"
}

// POST /v1/call/user/contacts/respond
func (s *Server) postUserContactRespond(c *gin.Context) {
	userID := currentUserID(c)
	var body userContactRespondBody
	if err := c.ShouldBindJSON(&body); err != nil || body.ID == 0 || (body.Action != "accept" && body.Action != "reject") {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 id，或 action 不是 accept/reject")
		return
	}
	ctx := c.Request.Context()

	row, err := s.getContactByID(ctx, body.ID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if row == nil {
		apiresp.Fail(c, apiresp.ErrContactNotExist, "联系人请求不存在")
		return
	}
	initiatorDevice := row.DeviceIDA
	if row.Initiator == "b" {
		initiatorDevice = row.DeviceIDB
	}
	responderDevice := row.peer(initiatorDevice)
	owned, err := s.deviceBelongsToUser(ctx, responderDevice, userID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if !owned {
		apiresp.Fail(c, apiresp.ErrForbidden, "联系人请求的接收设备不属于当前用户")
		return
	}

	updated, err := s.respondRequest(ctx, responderDevice, initiatorDevice, body.Action == "accept")
	if err != nil {
		apiresp.Fail(c, contactErrCode(err), err.Error())
		return
	}
	s.publishCallersUpdate(responderDevice, body.Action, "device", initiatorDevice)
	s.publishCallersUpdate(initiatorDevice, body.Action, "device", responderDevice)
	apiresp.OK(c, gin.H{"status": statusName(updated.Status)})
}

type userRemarkBody struct {
	DeviceID string `json:"device_id"`
	PeerID   string `json:"peer_id"`
	Remark   string `json:"remark"`
}

// PUT /v1/call/user/contacts/remark
func (s *Server) putUserContactRemark(c *gin.Context) {
	userID := currentUserID(c)
	var body userRemarkBody
	if err := c.ShouldBindJSON(&body); err != nil || body.DeviceID == "" || body.PeerID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id 或 peer_id")
		return
	}
	if !validContactRemark(body.Remark) {
		apiresp.Fail(c, apiresp.ErrBadParam, "remark 不能超过 64 个字符")
		return
	}
	ctx := c.Request.Context()

	owned, err := s.deviceBelongsToUser(ctx, body.DeviceID, userID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if !owned {
		apiresp.Fail(c, apiresp.ErrForbidden, "设备不属于当前用户")
		return
	}

	row, err := s.getContactRow(ctx, body.DeviceID, body.PeerID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if row != nil && row.Status == contactStatusAccepted {
		if err := s.setRemark(ctx, body.DeviceID, body.PeerID, body.Remark); err != nil {
			apiresp.Fail(c, contactErrCode(err), err.Error())
			return
		}
		s.publishCallersUpdate(body.DeviceID, "remark", "device", body.PeerID)
		apiresp.OK(c, nil)
		return
	}

	var voipID int64
	if err := s.db.GetContext(ctx, &voipID,
		`SELECT id FROM voip_device_auth
			  WHERE device_id=? AND wx_open_id=? AND auth_status='active'
		  ORDER BY created_at DESC, id DESC LIMIT 1`,
		body.DeviceID, body.PeerID); err != nil {
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

// DELETE /v1/call/user/contacts/:id
func (s *Server) deleteUserContact(c *gin.Context) {
	userID := currentUserID(c)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		apiresp.Fail(c, apiresp.ErrBadParam, "id 格式错误")
		return
	}
	ctx := c.Request.Context()

	row, err := s.getContactByID(ctx, id)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if row == nil {
		apiresp.Fail(c, apiresp.ErrContactNotExist, "联系人不存在")
		return
	}
	self, peer, owned, err := s.contactSideForUser(ctx, row, userID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if !owned {
		apiresp.Fail(c, apiresp.ErrForbidden, "联系人不属于当前用户")
		return
	}
	if err := s.deleteContact(ctx, self, peer); err != nil {
		apiresp.Fail(c, contactErrCode(err), err.Error())
		return
	}
	// The H5 initiated the mutation, so both device caches need invalidation.
	s.publishCallersUpdate(self, "delete", "device", peer)
	s.publishCallersUpdate(peer, "delete", "device", self)
	apiresp.OK(c, nil)
}
