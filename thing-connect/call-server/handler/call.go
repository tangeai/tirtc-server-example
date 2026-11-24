package handler

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/call"

	"thing-connect/call-server/apiresp"
)

type callRequestBody struct {
	Targets  []string `json:"targets"`
	CallType string   `json:"call_type"`
}

// POST /v1/call/request
func (s *Server) postCallRequest(c *gin.Context) {
	caller := currentDeviceID(c)
	var body callRequestBody
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Targets) == 0 {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 targets")
		return
	}
	if body.CallType != "audio" && body.CallType != "video" {
		apiresp.Fail(c, apiresp.ErrBadParam, "call_type 必须为 audio 或 video")
		return
	}
	ctx := c.Request.Context()
	// Normalize once so permission checks, notifications and rejection counts
	// all describe the same set of devices, preserving first-seen order.
	targets := make([]string, 0, len(body.Targets))
	seen := make(map[string]bool, len(body.Targets))
	for _, target := range body.Targets {
		if !seen[target] {
			targets = append(targets, target)
			seen[target] = true
		}
	}
	body.Targets = targets

	for _, target := range body.Targets {
		if target == caller {
			apiresp.Fail(c, apiresp.ErrBadParam, "不能呼叫自己")
			return
		}
		ok, err := s.isAcceptedContact(ctx, caller, target)
		if err != nil {
			apiresp.Fail(c, apiresp.ErrInternal, err.Error())
			return
		}
		if !ok {
			apiresp.Fail(c, apiresp.ErrContactNotExist, "目标设备不是已接受的联系人："+target)
			return
		}
	}

	online := make(map[string]bool, len(body.Targets))
	var offline []string
	anyOnline := false
	for _, target := range body.Targets {
		isOn := s.isOnline(ctx, target)
		online[target] = isOn
		if isOn {
			anyOnline = true
		} else {
			offline = append(offline, target)
		}
	}
	if !anyOnline {
		apiresp.Fail(c, apiresp.ErrPeerOffline, "所有目标设备均不在线")
		return
	}

	callerNames := make(map[string]string)
	for _, target := range body.Targets {
		callerName := caller
		if !online[target] {
			continue
		}
		if row, err := s.getContactRow(ctx, caller, target); err == nil && row != nil {
			if remark := row.remarkFor(target); remark != "" {
				callerName = remark
			}
		}
		callerNames[target] = callerName
	}

	roomID := "d_roomid_" + strings.ReplaceAll(uuidv4(), "-", "")
	existingRoomID, err := s.rooms.Create(ctx, call.Room{
		RoomID: roomID, Caller: caller, Targets: body.Targets, CallType: body.CallType, CallerNames: callerNames,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, offline, time.Duration(s.Config().Service.RoomTTLHours)*time.Hour)
	if err != nil {
		s.callError(c, err)
		return
	}
	if existingRoomID != "" {
		apiresp.FailWithData(c, apiresp.ErrPeerBusy, "设备已在通话中", gin.H{"room_id": existingRoomID})
		return
	}

	apiresp.OK(c, gin.H{
		"room_id": roomID,
		"online":  online,
		"offline": offline,
	})
}

type deviceInfoBody struct {
	DeviceID string `json:"device_id"`
	RoomID   string `json:"room_id"`
	Purpose  string `json:"purpose"`
}

// POST /v1/call/device/info — this server only implements purpose=call.
func (s *Server) postDeviceInfo(c *gin.Context) {
	self := currentDeviceID(c)
	var body deviceInfoBody
	if err := c.ShouldBindJSON(&body); err != nil || body.DeviceID == "" || body.RoomID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id 或 room_id")
		return
	}
	if body.Purpose != "call" {
		apiresp.Fail(c, apiresp.ErrBadParam, "不支持该 purpose，当前仅支持 purpose=call")
		return
	}
	ctx := c.Request.Context()

	rm, err := s.rooms.Get(ctx, body.RoomID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if rm == nil {
		apiresp.Fail(c, apiresp.ErrNotFound, "通话房间不存在")
		return
	}
	if rm.Caller != body.DeviceID || !containsString(rm.Targets, self) {
		apiresp.Fail(c, apiresp.ErrForbidden, "当前设备不属于该通话房间")
		return
	}

	// Obtain credentials before committing any state or notifying peers. A
	// failed lookup must leave both the pending call and any old call intact.
	token, err := s.buildConnectToken(ctx, rm.Caller)
	if err != nil {
		s.callError(c, err)
		return
	}
	_, err = s.rooms.Answer(ctx, body.RoomID, body.DeviceID, self)
	if err != nil {
		s.callError(c, err)
		return
	}

	apiresp.OK(c, gin.H{"token": token, "device_id": rm.Caller})
}

type roomIDBody struct {
	RoomID string `json:"room_id"`
	Reason string `json:"reason"`
}

// POST /v1/call/cancel — caller only.
func (s *Server) postCallCancel(c *gin.Context) {
	self := currentDeviceID(c)
	var body roomIDBody
	if err := c.ShouldBindJSON(&body); err != nil || body.RoomID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 room_id")
		return
	}
	_, err := s.rooms.End(c.Request.Context(), body.RoomID, self, true, "cancel")
	if err != nil {
		s.callError(c, err)
		return
	}
	apiresp.OK(c, nil)
}

// POST /v1/call/hangup — caller or answered_by.
func (s *Server) postCallHangup(c *gin.Context) {
	self := currentDeviceID(c)
	var body roomIDBody
	if err := c.ShouldBindJSON(&body); err != nil || body.RoomID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 room_id")
		return
	}
	reason := body.Reason
	if reason == "" {
		reason = "hangup"
	}
	_, err := s.rooms.End(c.Request.Context(), body.RoomID, self, false, reason)
	if err != nil {
		s.callError(c, err)
		return
	}
	apiresp.OK(c, nil)
}

// POST /v1/call/reject — must be one of room.targets.
func (s *Server) postCallReject(c *gin.Context) {
	self := currentDeviceID(c)
	var body roomIDBody
	if err := c.ShouldBindJSON(&body); err != nil || body.RoomID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 room_id")
		return
	}
	reason := body.Reason
	if reason == "" {
		reason = "decline"
	}
	_, err := s.rooms.Reject(c.Request.Context(), body.RoomID, self, reason)
	if err != nil {
		s.callError(c, err)
		return
	}

	apiresp.OK(c, nil)
}

// GET /v1/call/room — 查询当前设备所在房间（崩溃恢复用）。
func (s *Server) getDeviceRoom(c *gin.Context) {
	self := currentDeviceID(c)
	rm, err := s.rooms.Current(c.Request.Context(), self)
	if err != nil {
		s.callError(c, err)
		return
	}
	if rm == nil {
		apiresp.OK(c, nil)
		return
	}

	role := "callee"
	if rm.Caller == self {
		role = "caller"
	}
	apiresp.OK(c, gin.H{
		"room_id":   rm.RoomID,
		"status":    rm.Status,
		"caller":    rm.Caller,
		"call_type": rm.CallType,
		"role":      role,
	})
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Server) callError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, call.ErrNotFound):
		apiresp.Fail(c, apiresp.ErrNotFound, "通话房间不存在")
	case errors.Is(err, call.ErrForbidden):
		apiresp.Fail(c, apiresp.ErrForbidden, "当前设备无权操作该通话房间")
	case errors.Is(err, call.ErrAnswered):
		apiresp.Fail(c, apiresp.ErrAlreadyAnswered, "该通话已被其他设备接听")
	default:
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
	}
}
