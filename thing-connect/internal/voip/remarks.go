package voip

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrContactMissing = errors.New("联系人不存在")
var ErrRemarkInvalid = errors.New("备注不能超过 64 个字符")

type RemarkEvent struct {
	ID       int64  `db:"id"`
	DeviceID string `db:"device_id"`
	PeerID   string `db:"peer_id"`
	Attempts int    `db:"attempts"`
}

type RemarkStore interface {
	// Save verifies the device and active authorization under lock and atomically
	// writes the independent name and a durable cache invalidation event.
	Save(context.Context, string, string, string, int64) (RemarkEvent, error)
	Pending(context.Context) ([]RemarkEvent, error)
	Complete(context.Context, int64) error
	Retry(context.Context, int64, time.Duration) error
}

type RemarkNotifier interface {
	RemarkChanged(context.Context, RemarkEvent) error
}

type Remarks struct {
	store    RemarkStore
	notifier RemarkNotifier
}

func NewRemarks(store RemarkStore, notifier RemarkNotifier) *Remarks {
	return &Remarks{store: store, notifier: notifier}
}
func (s *Remarks) Set(ctx context.Context, deviceID, peerID, value string, userID int64) error {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > 64 {
		return ErrRemarkInvalid
	}
	event, err := s.store.Save(ctx, deviceID, peerID, value, userID)
	if err != nil {
		return err
	}
	// Delivery failure leaves the event durable; the successful write is not
	// reported as failed, which would encourage clients to overwrite newer edits.
	s.deliver(ctx, event)
	return nil
}
func (s *Remarks) deliver(ctx context.Context, event RemarkEvent) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if err := s.notifier.RemarkChanged(ctx, event); err != nil {
		attempt := event.Attempts
		if attempt > 6 {
			attempt = 6
		}
		if e := s.store.Retry(ctx, event.ID, time.Duration(1<<attempt)*time.Second); e != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "contact notification retry failed", "err", e)
		}
		return
	}
	if err := s.store.Complete(ctx, event.ID); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "contact notification completion failed", "err", err)
	}
}
func (s *Remarks) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		events, err := s.store.Pending(ctx)
		if err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "contact notifications unavailable", "err", err)
		}
		for _, event := range events {
			if ctx.Err() != nil {
				return
			}
			s.deliver(ctx, event)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
