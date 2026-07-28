package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

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

	roomID := "d_roomid_" + strings.ReplaceAll(uuidv4(), "-", "")
	ok, existingRoomID, err := s.acquireLock(ctx, caller, roomID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if !ok {
		apiresp.FailWithData(c, apiresp.ErrPeerBusy, "设备已在通话中", gin.H{"room_id": existingRoomID})
		return
	}

	if err := s.createRoom(ctx, roomID, caller, body.Targets, body.CallType, offline); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}

	callerName := caller
	for _, target := range body.Targets {
		if !online[target] {
			continue
		}
		if row, err := s.getContactRow(ctx, caller, target); err == nil && row != nil {
			if remark := row.remarkFor(target); remark != "" {
				callerName = remark
			}
		}
		s.publishCallIncoming(target, roomID, caller, callerName, body.CallType)
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

	rm, err := s.getRoom(ctx, body.RoomID)
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

	// Auto-switch: if self is locked to a different room, release it first —
	// answering this call is an implicit hang-up of whatever self was in.
	// No "busy" branch here: a device that doesn't want to switch should call
	// /v1/call/reject on the incoming call instead of hitting this endpoint.
	if existingRoomID, err := s.rdb.Get(ctx, lockKey(self)).Result(); err == nil && existingRoomID != "" && existingRoomID != body.RoomID {
		oldRoom, err := s.getRoom(ctx, existingRoomID)
		if err != nil {
			apiresp.Fail(c, apiresp.ErrInternal, err.Error())
			return
		}
		if oldRoom != nil {
			notify := []string{oldRoom.Caller}
			if oldRoom.AnsweredBy != "" && oldRoom.AnsweredBy != self {
				notify = []string{oldRoom.AnsweredBy}
			} else if oldRoom.Caller == self {
				notify = oldRoom.Targets
			}
			notify = removeString(notify, self)
			if err := s.releaseRoom(ctx, oldRoom, "caller_left", notify); err != nil {
				apiresp.Fail(c, apiresp.ErrInternal, err.Error())
				return
			}
		} else {
			s.rdb.Del(ctx, lockKey(self))
		}
	}

	answeredOK, err := s.setAnswered(ctx, body.RoomID, self)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if !answeredOK {
		apiresp.Fail(c, apiresp.ErrAlreadyAnswered, "该通话已被接听")
		return
	}

	if ok, _, err := s.acquireLock(ctx, self, body.RoomID); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	} else if !ok {
		// Shouldn't happen (we just released any existing lock above), but
		// don't leave the room half-answered if it does.
		s.rdb.Set(ctx, lockKey(self), body.RoomID, s.roomTTL())
	}
	if err := s.setAnsweredBy(ctx, body.RoomID, self); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}

	// 通知主叫：被叫已接听（P2P 建连前的状态通知）
	s.publishCalleeAnswered(rm.Caller, body.RoomID, self)

	token, err := s.buildConnectToken(ctx, rm.Caller)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}

	for _, target := range rm.Targets {
		if target != self {
			s.publishRoomCancel(target, body.RoomID, "accepted_by_other", 1)
		}
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
	ctx := c.Request.Context()

	rm, err := s.getRoom(ctx, body.RoomID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if rm == nil {
		apiresp.Fail(c, apiresp.ErrNotFound, "通话房间不存在")
		return
	}
	if rm.Caller != self {
		apiresp.Fail(c, apiresp.ErrForbidden, "只有呼叫方可以取消通话")
		return
	}

	if err := s.releaseRoom(ctx, rm, "cancel", rm.Targets); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
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
	ctx := c.Request.Context()

	rm, err := s.getRoom(ctx, body.RoomID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if rm == nil {
		apiresp.Fail(c, apiresp.ErrNotFound, "通话房间不存在")
		return
	}
	if rm.Caller != self && rm.AnsweredBy != self {
		apiresp.Fail(c, apiresp.ErrForbidden, "当前设备不属于该通话房间")
		return
	}

	var notify []string
	switch {
	case rm.Caller == self && rm.AnsweredBy != "":
		notify = []string{rm.AnsweredBy}
	case rm.AnsweredBy == self:
		notify = []string{rm.Caller}
	default:
		notify = rm.Targets
	}
	if err := s.releaseRoom(ctx, rm, reason, notify); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
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
	ctx := c.Request.Context()

	rm, err := s.getRoom(ctx, body.RoomID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if rm == nil {
		apiresp.Fail(c, apiresp.ErrNotFound, "通话房间不存在")
		return
	}
	if !containsString(rm.Targets, self) {
		apiresp.Fail(c, apiresp.ErrForbidden, "当前设备不是该通话的被叫方")
		return
	}

	allRejected, err := s.addRejected(ctx, rm, self)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	s.publishCallReject(rm.Caller, body.RoomID, self, reason)

	if allRejected {
		if err := s.releaseRoom(ctx, rm, "all_rejected", []string{rm.Caller}); err != nil {
			apiresp.Fail(c, apiresp.ErrInternal, err.Error())
			return
		}
	}
	apiresp.OK(c, nil)
}

// GET /v1/call/room — 查询当前设备所在房间（崩溃恢复用）。
func (s *Server) getDeviceRoom(c *gin.Context) {
	self := currentDeviceID(c)
	ctx := c.Request.Context()

	roomID, err := s.rdb.Get(ctx, lockKey(self)).Result()
	if err == redis.Nil || roomID == "" {
		apiresp.OK(c, nil)
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}

	rm, err := s.getRoom(ctx, roomID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, err.Error())
		return
	}
	if rm == nil {
		// lock 存在但 room hash 已过期，清理孤儿 lock
		s.rdb.Del(ctx, lockKey(self))
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

func removeString(list []string, v string) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		if x != v && x != "" {
			out = append(out, x)
		}
	}
	return out
}
