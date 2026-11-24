// Package call owns the one-to-one room lifecycle contract. A room and its
// device ownership must change together, including auto-switch and rejection.
package call

import (
	"context"
	_ "embed"
	"errors"
	"time"
)

// RoomTransitions implements the atomic room policy for the Redis adapter.
//
//go:embed room_transitions.lua
var RoomTransitions string

var (
	ErrNotFound  = errors.New("call: room not found")
	ErrForbidden = errors.New("call: not a room participant")
	ErrAnswered  = errors.New("call: answered by another device")
)

const (
	Active   = "active"
	Answered = "answered"
)

type Room struct {
	RejectedTargets []string          `json:"rejected_targets,omitempty"`
	CallerNames     map[string]string `json:"caller_names,omitempty"`
	RoomID          string            `json:"room_id"`
	Caller          string            `json:"caller"`
	Targets         []string          `json:"targets"`
	Status          string            `json:"status"`
	AnsweredBy      string            `json:"answered_by"`
	CallType        string            `json:"call_type"`
	CreatedAt       string            `json:"created_at"`
}
type AnswerResult struct {
	Room     Room  `json:"room"`
	Previous *Room `json:"previous,omitempty"`
	First    bool  `json:"first"`
}
type RejectResult struct {
	Room        Room `json:"room"`
	Applied     bool `json:"applied"`
	AllRejected bool `json:"all_rejected"`
}

// Rooms owns atomic room/lock transitions. Implementations revalidate current
// membership at commit time; callers must never delete locks from a snapshot.
// Keys and TTL remain compatible with the existing device-call protocol.
type Rooms interface {
	NotificationQueue
	// Create returns the caller's existing room ID on contention.
	Create(context.Context, Room, []string, time.Duration) (string, error)
	Get(context.Context, string) (*Room, error)
	Current(context.Context, string) (*Room, error)
	// Answer is idempotent for the winning device. It atomically releases any
	// previous room and claims the new room, without recreating a cancelled room.
	Answer(context.Context, string, string, string) (AnswerResult, error)
	// End checks actor permissions against live state. callerOnly is for cancel.
	End(context.Context, string, string, bool, string) (*Room, error)
	// Reject cannot release an answered room and counts distinct targets.
	Reject(context.Context, string, string, string) (RejectResult, error)
}
