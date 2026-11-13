// Package presence owns ordered, bounded processing of device presence observations.
package presence

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"
)

const TTL = 150 * time.Second
const QueueSize = 16384
const BatchSize = 256

// Observation uses server receipt time; device-supplied clocks are not trusted.
type Observation struct {
	IP          string
	ConnectedAt time.Time
	ClientID    string
	At          time.Time
	Offline     bool
}

// ProjectionStore atomically updates online state and enqueues connection IP
// snapshots. Its queue methods operate on that same durable projection.
type ProjectionStore interface {
	ConnectionQueue
	Project(context.Context, []Observation) error
}

// ConnectionStore persists newer connection metadata idempotently.
type ConnectionStore interface {
	SaveConnections(context.Context, []Observation) error
}

// Projector has one writer. A failed batch is retried before any newer batch,
// preserving per-device order even after partial pipeline execution.
type Projector struct {
	connections   *connectionRecorder
	metadataGap   atomic.Bool
	store         ProjectionStore
	queue         chan Observation
	degradedUntil atomic.Int64
	lastSuccess   atomic.Int64
	rejected      atomic.Uint64
	failures      atomic.Uint64
}

func New(store ProjectionStore, connections ConnectionStore) *Projector {
	p := &Projector{store: store, queue: make(chan Observation, QueueSize)}
	if connections != nil {
		p.connections = &connectionRecorder{store: connections, queue: store}
	}
	return p
}

// Observe never blocks the MQTT callback or creates an unbounded goroutine.
func (p *Projector) Observe(o Observation) {

	select {
	case p.queue <- o:
	default:
		if p.rejected.Add(1)%4096 == 1 {
			slog.Error("presence queue overflow", "rejected", p.rejected.Load())
		}
		p.degradedUntil.Store(time.Now().Add(TTL).UnixNano())
		if o.IP != "" {
			p.metadataGap.Store(true)
		}
	}
}

func (p *Projector) Ping(ctx context.Context) error {
	if p.connections != nil {
		if err := p.connections.Ping(ctx); err != nil {
			return err
		}
	}
	if p.metadataGap.Load() {
		return errors.New("presence: connection event lost; reconciliation required")
	}
	now := time.Now()
	if now.UnixNano() < p.degradedUntil.Load() {
		return errors.New("presence: observations lost; awaiting refresh")
	}
	if last := p.lastSuccess.Load(); last == 0 || now.Sub(time.Unix(0, last)) > 5*time.Second {
		return errors.New("presence: projection is not progressing")
	}
	return nil
}

func (p *Projector) Run(ctx context.Context) {
	if p.connections != nil {
		done := make(chan struct{})
		go func() { defer close(done); p.connections.Run(ctx) }()
		defer func() { <-done }()
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	batch := make([]Observation, 0, BatchSize)
	flush := func() bool {
		for {
			callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := p.apply(callCtx, batch)
			cancel()
			if err == nil {
				p.lastSuccess.Store(time.Now().UnixNano())
				batch = batch[:0]
				return true
			}
			if p.failures.Add(1)%60 == 1 {
				slog.Error("presence batch failed; retrying", "err", err)
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return false
			case <-timer.C:
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			p.drain(batch)
			return
		case o := <-p.queue:
			batch = append(batch, o)
			if len(batch) == BatchSize && !flush() {
				p.drain(batch)
				return
			}
		case <-ticker.C:
			if !flush() {
				p.drain(batch)
				return
			}
		}
	}
}

// Stats supports operational diagnosis without logging individual heartbeats.
func (p *Projector) Stats() (queued int, rejected, failures uint64) {
	return len(p.queue), p.rejected.Load(), p.failures.Load()
}

// drain bounds shutdown work after the source has stopped. TTL remains the
// safety net if the store cannot accept the final observations.
func (p *Projector) drain(batch []Observation) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		if len(batch) == BatchSize {
			if err := p.apply(ctx, batch); err != nil {
				slog.Error("presence shutdown flush failed", "err", err)
				return
			}
			batch = batch[:0]
		}
		select {
		case o := <-p.queue:
			batch = append(batch, o)
		default:
			if len(batch) > 0 {
				if err := p.apply(ctx, batch); err != nil {
					slog.Error("presence shutdown flush failed", "err", err)
				}
			}
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (p *Projector) apply(ctx context.Context, batch []Observation) error {
	if p.metadataGap.Load() && p.connections != nil {
		if err := p.connections.queue.MarkGap(ctx); err != nil {
			return err
		}
	}
	return p.store.Project(ctx, batch)
}

// Metrics is a non-blocking snapshot for the existing service status reporter.
func (p *Projector) Metrics() map[string]int64 {
	queued, rejected, failures := p.Stats()
	result := map[string]int64{"presence_queued": int64(queued), "presence_rejected_total": int64(rejected), "presence_write_failures_total": int64(failures)}
	if r := p.connections; r != nil {
		result["connection_pending"] = r.pending.Load()
		result["connection_backlog_measured_at_ms"] = r.measuredMS.Load()
		age := int64(0)
		if oldest := r.oldestMS.Load(); oldest > 0 {
			age = max(0, time.Now().UnixMilli()-oldest)
		}
		result["connection_oldest_pending_age_ms"] = age
	}
	return result
}
