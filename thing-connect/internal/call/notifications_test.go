package call

import (
	"context"
	"errors"
	"testing"
)

type notificationRoom struct {
	Rooms
	current *Room
	err     error
}

func (r notificationRoom) Get(context.Context, string) (*Room, error) { return r.current, r.err }

type publisherFunc func(context.Context, Notification) error

func (f publisherFunc) PublishCallNotification(ctx context.Context, n Notification) error {
	return f(ctx, n)
}

func TestPendingNotificationUsesCurrentParticipants(t *testing.T) {
	active := &Room{Status: Active}
	answered := &Room{Status: Answered, AnsweredBy: "winner"}
	unavailable := errors.New("room unavailable")
	for _, tc := range []struct {
		name, kind string
		room       *Room
		lookupErr  error
		send       bool
	}{
		{"live invitation", "call_incoming", active, nil, true},
		{"expired room", "call_incoming", nil, nil, false},
		{"already answered invitation", "call_incoming", answered, nil, false},
		{"rejected invitation", "call_incoming", &Room{Status: Active, RejectedTargets: []string{"target"}}, nil, false},
		{"confirmed answer", "callee_answered", answered, nil, true},
		{"superseded answer", "callee_answered", &Room{Status: Answered, AnsweredBy: "other"}, nil, false},
		{"missing room still sends cancel", "room_cancel", nil, nil, true},
		{"lookup failure retries", "call_incoming", nil, unavailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			s := NewService(notificationRoom{current: tc.room, err: tc.lookupErr}, publisherFunc(func(context.Context, Notification) error { called = true; return nil }))
			err := s.publish(context.Background(), Notification{Kind: tc.kind, Target: "target", Payload: map[string]string{"callee_id": "winner"}})
			if !errors.Is(err, tc.lookupErr) || called != tc.send {
				t.Fatalf("published=%t err=%v", called, err)
			}
		})
	}
}
