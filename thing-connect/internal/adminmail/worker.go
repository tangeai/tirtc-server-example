// Package adminmail delivers audited administrator password-reset commands.
package adminmail

import (
	"context"
	"log/slog"
	"time"

	"thing-connect/internal/queuehealth"
)

const MaxAttempts = 5
const Lease = time.Minute

type Command struct {
	ID       int64
	Email    string
	Attempts int
}

type Queue interface {
	Claim(context.Context) (*Command, error)
	Finish(context.Context, Command, bool) error
	Backlog(context.Context) (queuehealth.Backlog, error)
}

type Worker struct {
	queue   Queue
	deliver func(context.Context, string) error
}

func NewWorker(q Queue, deliver func(context.Context, string) error) *Worker {
	if q == nil || deliver == nil {
		panic("admin mail requires a queue and delivery function")
	}
	return &Worker{queue: q, deliver: deliver}
}

func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		op, cancel := context.WithTimeout(ctx, 5*time.Second)
		cmd, err := w.queue.Claim(op)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("claim admin reset mail", "err", err)
		}
		if cmd == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		delivery, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		err = w.deliver(delivery, cmd.Email)
		stop()
		if err != nil {
			slog.Warn("admin reset mail delivery failed", "command", cmd.ID, "attempt", cmd.Attempts)
		}
		ack, finish := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		err = w.queue.Finish(ack, *cmd, err == nil)
		finish()
		if err != nil {
			slog.Warn("confirm admin reset mail", "command", cmd.ID, "err", err)
		}
	}
}

// CombinedBacklog includes the Redis commands issued by older Admin versions.
type CombinedBacklog struct{ Durable, Legacy queuehealth.Source }

func (q CombinedBacklog) Backlog(ctx context.Context) (queuehealth.Backlog, error) {
	a, err := q.Durable.Backlog(ctx)
	if err != nil {
		return a, err
	}
	b, err := q.Legacy.Backlog(ctx)
	if err != nil {
		return b, err
	}
	return queuehealth.Backlog{Pending: a.Pending + b.Pending, Exhausted: a.Exhausted + b.Exhausted, OldestAge: max(a.OldestAge, b.OldestAge)}, nil
}
