package call

import (
	"context"
	"log/slog"
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
		s.flush(ctx)
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
		s.flush(ctx)
	}
	return result, err
}
func (s *Service) End(ctx context.Context, id, self string, only bool, reason string) (*Room, error) {
	r, err := s.store.End(ctx, id, self, only, reason)
	if err == nil {
		s.flush(ctx)
	}
	return r, err
}
func (s *Service) Reject(ctx context.Context, id, self, reason string) (RejectResult, error) {
	result, err := s.store.Reject(ctx, id, self, reason)
	if err == nil {
		s.flush(ctx)
	}
	return result, err
}
func (s *Service) flush(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.Dispatch(ctx, 32); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "call notification remains pending", "err", err)
	}
}

// Dispatch claims one message at a time. A failed/leased head blocks only its
// room, so no later notification for that room overtakes a pending predecessor.
func (s *Service) Dispatch(ctx context.Context, limit int) error {
	for i := 0; i < limit && ctx.Err() == nil; i++ {
		claimCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		event, found, err := s.store.ClaimNotification(claimCtx, 30*time.Second)
		cancel()
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		deliveryCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		obsolete := false
		if event.Kind == "call_incoming" || event.Kind == "callee_answered" {
			r, e := s.store.Get(deliveryCtx, event.RoomID)
			err = e
			if e == nil {
				obsolete = r == nil || (event.Kind == "call_incoming" && r.Status != Active) || (event.Kind == "callee_answered" && (r.Status != Answered || r.AnsweredBy != event.Payload["callee_id"]))
			}
			if e == nil && r != nil && event.Kind == "call_incoming" {
				for _, id := range r.RejectedTargets {
					if id == event.Target {
						obsolete = true
					}
				}
			}
		}
		if err == nil && !obsolete {
			err = s.publisher.PublishCallNotification(deliveryCtx, event)
		}
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
