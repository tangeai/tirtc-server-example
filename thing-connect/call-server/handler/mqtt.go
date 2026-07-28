package handler

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"time"

	"thing-connect/internal/mqttc"
)

// snClientID returns the MQTT ClientID a bound device connects with
// (see device-sim device_flow.py: ClientID = sn_{device_id}).
func snClientID(deviceID string) string { return "sn_" + deviceID }

func (s *Server) isOnline(ctx context.Context, deviceID string) bool {
	return s.broker.IsOnline(ctx, snClientID(deviceID))
}

// envelope is the MQTT message wrapper (design doc §7.1).
type envelope struct {
	MsgID   string `json:"msg_id"`
	From    string `json:"from"`
	Type    string `json:"type"`
	Channel string `json:"channel"`
	Msg     any    `json:"payload"`
	Ts      string `json:"ts"`
}

func newEnvelope(from, msgType string, msg any) envelope {
	return envelope{
		MsgID:   uuidv4(),
		From:    from,
		Type:    msgType,
		Channel: "device",
		Msg:     msg,
		Ts:      time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

func (s *Server) publish(deviceID string, qos byte, topic string, msgType, from string, msg any) {
	if err := s.broker.Publish(topic, qos, newEnvelope(from, msgType, msg)); err != nil {
		log.Printf("call-server: publish %s to %s failed: %v", msgType, deviceID, err)
	}
}

func (s *Server) publishCallIncoming(target, roomID, callerID, callerName, callType string) {
	s.publish(target, 1, mqttc.DeviceCmdTopic(snClientID(target)), "call_incoming", callerID, map[string]any{
		"room_id":     roomID,
		"caller_id":   callerID,
		"caller_name": callerName,
		"call_type":   callType,
	})
}

func (s *Server) publishRoomCancel(target, roomID, reason string, qos byte) {
	s.publish(target, qos, mqttc.DeviceNotifyTopic(snClientID(target)), "room_cancel", "", map[string]any{
		"room_id": roomID,
		"reason":  reason,
	})
}

func (s *Server) publishCalleeAnswered(callerID, roomID, calleeID string) {
	s.publish(callerID, 1, mqttc.DeviceNotifyTopic(snClientID(callerID)), "callee_answered", "", map[string]any{
		"room_id":   roomID,
		"callee_id": calleeID,
	})
}

func (s *Server) publishCallReject(target, roomID, rejecterID, reason string) {
	s.publish(target, 1, mqttc.DeviceNotifyTopic(snClientID(target)), "call_reject", rejecterID, map[string]any{
		"room_id": roomID,
		"reason":  reason,
	})
}

func (s *Server) publishCallersUpdate(target, action, contactType, peerID string) {
	s.publish(target, 1, mqttc.DeviceNotifyTopic(snClientID(target)), "callers_update", "", map[string]any{
		"action":       action,
		"contact_type": contactType,
		"peer_id":      peerID,
	})
}

// uuidv4 hand-rolls a random UUID (RFC 4122 v4) using crypto/rand, matching the
// codebase's existing style of small local helpers over new dependencies
// (see internal/tirtcapi randB64).
func uuidv4() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read failing is effectively unrecoverable; fall back to a
		// zero UUID rather than panicking a request handler.
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
