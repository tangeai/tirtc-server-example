package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

func roomKey(roomID string) string     { return "room:" + roomID }
func answeredKey(roomID string) string { return "room:" + roomID + ":answered" }
func rejectedKey(roomID string) string { return "room:" + roomID + ":rejected_by" }
func lockKey(deviceID string) string   { return "room:lock:" + deviceID }

const (
	roomStatusActive   = "active"
	roomStatusAnswered = "answered"
)

type room struct {
	RoomID     string
	Caller     string
	Targets    []string
	Status     string
	AnsweredBy string
	CallType   string
	CreatedAt  string
}

func (s *Server) roomTTL() time.Duration {
	return time.Duration(s.cfg.Service.RoomTTLHours) * time.Hour
}

// createRoom persists the room hash and pre-marks offlineTargets as rejected
// (they never receive call_incoming and can never call /v1/call/reject themselves).
func (s *Server) createRoom(ctx context.Context, roomID, caller string, targets []string, callType string, offlineTargets []string) error {
	targetsJSON, err := json.Marshal(targets)
	if err != nil {
		return fmt.Errorf("createRoom: marshal targets: %w", err)
	}
	ttl := s.roomTTL()
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, roomKey(roomID), map[string]any{
		"caller":      caller,
		"targets":     string(targetsJSON),
		"status":      roomStatusActive,
		"answered_by": "",
		"call_type":   callType,
		"created_at":  time.Now().UTC().Format(time.RFC3339),
	})
	pipe.Expire(ctx, roomKey(roomID), ttl)
	if len(offlineTargets) > 0 {
		members := make([]any, len(offlineTargets))
		for i, id := range offlineTargets {
			members[i] = id
		}
		pipe.SAdd(ctx, rejectedKey(roomID), members...)
		pipe.Expire(ctx, rejectedKey(roomID), ttl)
	}
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("createRoom: %w", err)
	}
	return nil
}

// getRoom returns nil (no error) if the room doesn't exist.
func (s *Server) getRoom(ctx context.Context, roomID string) (*room, error) {
	res, err := s.rdb.HGetAll(ctx, roomKey(roomID)).Result()
	if err != nil {
		return nil, fmt.Errorf("getRoom: %w", err)
	}
	if len(res) == 0 {
		return nil, nil
	}
	var targets []string
	if err := json.Unmarshal([]byte(res["targets"]), &targets); err != nil {
		return nil, fmt.Errorf("getRoom: unmarshal targets: %w", err)
	}
	return &room{
		RoomID:     roomID,
		Caller:     res["caller"],
		Targets:    targets,
		Status:     res["status"],
		AnsweredBy: res["answered_by"],
		CallType:   res["call_type"],
		CreatedAt:  res["created_at"],
	}, nil
}

// acquireLock atomically claims room:lock:{deviceID} for roomID. If the device
// already holds a lock, ok=false and existingRoomID is the room it's locked to
// (which may equal roomID if this is a retry of the same request).
func (s *Server) acquireLock(ctx context.Context, deviceID, roomID string) (ok bool, existingRoomID string, err error) {
	ok, err = s.rdb.SetNX(ctx, lockKey(deviceID), roomID, s.roomTTL()).Result()
	if err != nil {
		return false, "", fmt.Errorf("acquireLock: %w", err)
	}
	if ok {
		return true, "", nil
	}
	existing, err := s.rdb.Get(ctx, lockKey(deviceID)).Result()
	if err != nil && err != redis.Nil {
		return false, "", fmt.Errorf("acquireLock: get existing: %w", err)
	}
	return false, existing, nil
}

// setAnswered atomically claims room:{roomID}:answered for deviceID.
func (s *Server) setAnswered(ctx context.Context, roomID, deviceID string) (bool, error) {
	ok, err := s.rdb.SetNX(ctx, answeredKey(roomID), deviceID, s.roomTTL()).Result()
	if err != nil {
		return false, fmt.Errorf("setAnswered: %w", err)
	}
	return ok, nil
}

// setAnsweredBy updates the room hash's answered_by/status fields after setAnswered succeeds.
func (s *Server) setAnsweredBy(ctx context.Context, roomID, deviceID string) error {
	if err := s.rdb.HSet(ctx, roomKey(roomID), map[string]any{
		"answered_by": deviceID,
		"status":      roomStatusAnswered,
	}).Err(); err != nil {
		return fmt.Errorf("setAnsweredBy: %w", err)
	}
	return nil
}

// addRejected adds deviceIDs to the room's rejected_by set and reports whether
// every target has now rejected (online rejections plus offline ones pre-added
// at room creation — see createRoom).
func (s *Server) addRejected(ctx context.Context, rm *room, deviceIDs ...string) (allRejected bool, err error) {
	members := make([]any, len(deviceIDs))
	for i, id := range deviceIDs {
		members[i] = id
	}
	pipe := s.rdb.TxPipeline()
	pipe.SAdd(ctx, rejectedKey(rm.RoomID), members...)
	pipe.Expire(ctx, rejectedKey(rm.RoomID), s.roomTTL())
	card := pipe.SCard(ctx, rejectedKey(rm.RoomID))
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("addRejected: %w", err)
	}
	return card.Val() >= int64(len(rm.Targets)), nil
}

// releaseRoom deletes every Redis key belonging to rm and notifies notifyIDs
// via MQTT room_cancel{reason}. Every leave/cancel/all_rejected/auto-switch path
// goes through this so lock cleanup never gets special-cased per caller.
func (s *Server) releaseRoom(ctx context.Context, rm *room, reason string, notifyIDs []string) error {
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, roomKey(rm.RoomID))
	pipe.Del(ctx, answeredKey(rm.RoomID))
	pipe.Del(ctx, rejectedKey(rm.RoomID))
	pipe.Del(ctx, lockKey(rm.Caller))
	if rm.AnsweredBy != "" {
		pipe.Del(ctx, lockKey(rm.AnsweredBy))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("releaseRoom: %w", err)
	}
	qos := byte(1)
	if rm.Status == roomStatusAnswered {
		qos = 0
	}
	for _, id := range notifyIDs {
		s.publishRoomCancel(id, rm.RoomID, reason, qos)
	}
	return nil
}
