package presence

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"
)

// ConnectionQueue retains the latest pending connection per device until SQL
// confirms it. Ack must compare the exact snapshot so it cannot remove a newer
// connection. Gap markers survive restarts until explicit reconciliation.
type ConnectionQueue interface {
	Pending(context.Context) ([]Observation, error)
	Ack(context.Context, []Observation) error
	MarkGap(context.Context) error
	HasGap(context.Context) (bool, error)
	Backlog(context.Context) (ConnectionBacklog, error)
}

type ConnectionBacklog struct {
	Pending  int64
	OldestAt time.Time
}

// ConnectionMaxPendingAge bounds acceptable IP propagation delay, not capacity.
const ConnectionMaxPendingAge = 150 * time.Second

type connectionRecorder struct {
	pending    atomic.Int64
	oldestMS   atomic.Int64
	measuredMS atomic.Int64
	store      ConnectionStore
	queue      ConnectionQueue
	healthy    atomic.Bool
	gap        atomic.Bool
}

func (r *connectionRecorder) Ping(context.Context) error {
	if r.gap.Load() {
		return errors.New("presence: connection history gap requires reconciliation")
	}
	if !r.healthy.Load() {
		return errors.New("presence: connection persistence not ready")
	}
	if oldest := r.oldestMS.Load(); oldest > 0 && time.Since(time.UnixMilli(oldest)) > ConnectionMaxPendingAge {
		return errors.New("presence: connection backlog exceeds propagation deadline")
	}
	return nil
}

func (r *connectionRecorder) Run(ctx context.Context) {
	delay := time.Duration(0)
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := r.flush(callCtx)
		cancel()
		r.healthy.Store(err == nil)
		if err != nil {
			slog.Warn("presence connection persistence retry", "err", err)
			if delay < time.Second {
				delay = time.Second
			} else {
				delay *= 2
				if delay > 30*time.Second {
					delay = 30 * time.Second
				}
			}
		} else {
			delay = 100 * time.Millisecond
		}
	}
}

func (r *connectionRecorder) flush(ctx context.Context) error {
	gap, err := r.queue.HasGap(ctx)
	if err != nil {
		return err
	}
	r.gap.Store(gap)
	backlog, err := r.queue.Backlog(ctx)
	if err != nil {
		return err
	}
	r.pending.Store(backlog.Pending)
	oldest := int64(0)
	if !backlog.OldestAt.IsZero() {
		oldest = backlog.OldestAt.UnixMilli()
	}
	r.oldestMS.Store(oldest)
	r.measuredMS.Store(time.Now().UnixMilli())
	batch, err := r.queue.Pending(ctx)
	if err != nil || len(batch) == 0 {
		return err
	}
	if err := r.store.SaveConnections(ctx, batch); err != nil {
		return err
	}
	return r.queue.Ack(ctx, batch)
}
