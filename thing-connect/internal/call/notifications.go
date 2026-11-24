package call

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"thing-connect/internal/backoff"
)

type Notification struct {
	ID        string            `json:"id"`
	RoomID    string            `json:"room_id"`
	Target    string            `json:"target"`
	Kind      string            `json:"kind"`
	From      string            `json:"from"`
	Payload   map[string]string `json:"payload"`
	CreatedAt int64             `json:"created_at"`
	Lease     string            `json:"-"`
	Attempts  int               `json:"-"`
}

// NotificationQueue is part of the room store: transitions and their messages
// must commit in the same operation, never via a second enqueue call.
type NotificationQueue interface {
	ClaimNotification(context.Context, time.Duration) (Notification, bool, error)
	// ClaimRoomNotification claims only the due head of one room.
	ClaimRoomNotification(context.Context, time.Duration, string) (Notification, bool, error)
	CompleteNotification(context.Context, Notification) error
	RetryNotification(context.Context, Notification, time.Duration) error
}
type Publisher interface {
	PublishCallNotification(context.Context, Notification) error
}

// Service owns bounded delivery, obsolete-message suppression and recovery.
// A successful state mutation stays successful if immediate delivery fails;
// the room store retains the message for Run, including after process restart.
type Service struct {
	store     Rooms
	publisher Publisher
}

func NewService(store Rooms, publisher Publisher) *Service {
	if store == nil || publisher == nil {
		panic("call service requires room store and publisher")
	}
	return &Service{store: store, publisher: publisher}
}
func (s *Service) Create(ctx context.Context, r Room, offline []string, ttl time.Duration) (string, error) {
	existing, err := s.store.Create(ctx, r, offline, ttl)
	if err == nil && existing == "" {
		s.flush(ctx, r.RoomID)
	}
	return existing, err
}
func (s *Service) Get(ctx context.Context, id string) (*Room, error) { return s.store.Get(ctx, id) }
func (s *Service) Current(ctx context.Context, id string) (*Room, error) {
	return s.store.Current(ctx, id)
}
func (s *Service) Answer(ctx context.Context, id, caller, self string) (AnswerResult, error) {
	result, err := s.store.Answer(ctx, id, caller, self)
	if err == nil {
		if result.Previous != nil {
			s.flush(ctx, id, result.Previous.RoomID)
		} else {
			s.flush(ctx, id)
		}
	}
	return result, err
}
func (s *Service) End(ctx context.Context, id, self string, only bool, reason string) (*Room, error) {
	r, err := s.store.End(ctx, id, self, only, reason)
	if err == nil {
		s.flush(ctx, id)
	}
	return r, err
}
func (s *Service) Reject(ctx context.Context, id, self, reason string) (RejectResult, error) {
	result, err := s.store.Reject(ctx, id, self, reason)
	if err == nil {
		s.flush(ctx, id)
	}
	return result, err
}

// flush delivers only the rooms changed by this request; the global backlog
// belongs to Run so a slow broker cannot stall unrelated HTTP calls.
func (s *Service) flush(ctx context.Context, rooms ...string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, room := range rooms {
		err := s.dispatch(ctx, 32, func(ctx context.Context) (Notification, bool, error) {
			return s.store.ClaimRoomNotification(ctx, 30*time.Second, room)
		})
		if err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "call notification remains pending", "room_id", room, "err", err)
		}
	}
}

// Dispatch claims one message at a time. A failed/leased head blocks only its
// room, so no later notification for that room overtakes a pending predecessor.
func (s *Service) Dispatch(ctx context.Context, limit int) error {
	return s.dispatch(ctx, limit, func(ctx context.Context) (Notification, bool, error) {
		return s.store.ClaimNotification(ctx, 30*time.Second)
	})
}
func (s *Service) dispatch(ctx context.Context, limit int, claim func(context.Context) (Notification, bool, error)) error {
	for i := 0; i < limit && ctx.Err() == nil; i++ {
		claimCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		event, found, err := claim(claimCtx)
		cancel()
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		deliveryCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		err = s.publish(deliveryCtx, event)
		stop()
		// A cancelled HTTP request cannot suppress acknowledgement/retry bookkeeping.
		finishCtx, finish := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		if err == nil {
			err = s.store.CompleteNotification(finishCtx, event)
		} else {
			delay := backoff.Jitter(time.Second * time.Duration(1<<min(event.Attempts+1, 8)))
			retryErr := s.store.RetryNotification(finishCtx, event, delay)
			if retryErr != nil {
				err = retryErr
			}
		}
		finish()
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Only invitations and answer announcements depend on the live room.
func (s *Service) publish(ctx context.Context, event Notification) error {
	if event.Kind == "call_incoming" || event.Kind == "callee_answered" {
		r, err := s.store.Get(ctx, event.RoomID)
		if err != nil || r == nil {
			return err
		}
		if event.Kind == "call_incoming" && (r.Status != Active || slices.Contains(r.RejectedTargets, event.Target)) {
			return nil
		}
		if event.Kind == "callee_answered" && (r.Status != Answered || r.AnsweredBy != event.Payload["callee_id"]) {
			return nil
		}
	}
	return s.publisher.PublishCallNotification(ctx, event)
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		op, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := s.Dispatch(op, 32)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "call notification delivery pending", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
