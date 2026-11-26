// Package queuehealth samples durable backlogs independently of delivery so a
// slow batch cannot hide stale or growing work behind successful deliveries.
package queuehealth

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

type Backlog struct {
	Pending   int64
	Exhausted int64
	OldestAge time.Duration
}

// Source includes leased and delayed work, but excludes completed or obsolete
// work. Replacing a pending item must preserve its original enqueue time.
type Source interface {
	Backlog(context.Context) (Backlog, error)
}

type sample struct {
	Backlog
	at time.Time
}

type Monitor struct {
	source Source
	maxAge time.Duration
	prefix string
	latest atomic.Pointer[sample]
	now    func() time.Time
}

func New(prefix string, source Source) *Monitor {
	return NewWithMaxAge(prefix, source, 15*time.Minute)
}

func NewWithMaxAge(prefix string, source Source, maxAge time.Duration) *Monitor {
	if prefix == "" || source == nil || maxAge <= 0 {
		panic("queue health requires a name and source")
	}
	return &Monitor{source: source, prefix: prefix, now: time.Now, maxAge: maxAge}
}

func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		m.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Monitor) poll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := m.source.Backlog(ctx)
	if err == nil {
		m.latest.Store(&sample{Backlog: b, at: m.now()})
	}
}

func (m *Monitor) Metrics() map[string]int64 {
	values := map[string]int64{m.prefix + "_pending": 0, m.prefix + "_exhausted": 0, m.prefix + "_oldest_pending_age_ms": 0, m.prefix + "_backlog_measured_at_ms": 0}
	if s := m.latest.Load(); s != nil {
		values[m.prefix+"_pending"] = s.Pending
		values[m.prefix+"_exhausted"] = s.Exhausted
		values[m.prefix+"_backlog_measured_at_ms"] = s.at.UnixMilli()
		if s.Pending > 0 {
			values[m.prefix+"_oldest_pending_age_ms"] = max(0, (s.OldestAge + m.now().Sub(s.at)).Milliseconds())
		}
	}
	return values
}

// Health is a service-status probe, not a readiness gate: removing healthy
// delivery workers from traffic does not repair their downstream dependencies.
func (m *Monitor) Health(ctx context.Context) error { return m.HealthWithMaxAge(ctx, m.maxAge) }

// HealthWithMaxAge evaluates the latest sample against the current published threshold.
func (m *Monitor) HealthWithMaxAge(_ context.Context, maxAge time.Duration) error {
	s := m.latest.Load()
	if s == nil || m.now().Sub(s.at) > time.Minute {
		return errors.New("queue backlog sample is stale")
	}
	if s.Exhausted > 0 {
		return errors.New("queue delivery attempts exhausted")
	}
	if s.Pending > 0 && s.OldestAge+m.now().Sub(s.at) > maxAge {
		return errors.New("queue oldest pending age exceeds threshold")
	}
	return nil
}
