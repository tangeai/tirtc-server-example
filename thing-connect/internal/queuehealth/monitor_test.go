package queuehealth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type sourceFunc func(context.Context) (Backlog, error)

func (f sourceFunc) Backlog(c context.Context) (Backlog, error) { return f(c) }

func TestAgeFailureStalenessAndRecovery(t *testing.T) {
	now := time.Now()
	b := Backlog{Pending: 2, OldestAge: 16 * time.Minute}
	var failure error
	m := New("test", sourceFunc(func(c context.Context) (Backlog, error) {
		if d, ok := c.Deadline(); !ok || time.Until(d) > 5*time.Second {
			t.Fatal("unbounded query")
		}
		return b, failure
	}))
	m.now = func() time.Time { return now }
	ctx := context.Background()
	if m.Health(ctx) == nil {
		t.Fatal("unmeasured queue must not be healthy")
	}
	m.poll(ctx)
	if m.Health(ctx) == nil {
		t.Fatal("successful sampling must not hide an old backlog")
	}
	before := m.Metrics()["test_backlog_measured_at_ms"]
	failure = errors.New("store offline")
	now = now.Add(2 * time.Minute)
	m.poll(ctx)
	if m.Health(ctx) == nil || m.Metrics()["test_backlog_measured_at_ms"] != before {
		t.Fatal("failure refreshed sample")
	}
	if m.Metrics()["test_oldest_pending_age_ms"] != (18 * time.Minute).Milliseconds() {
		t.Fatal("age stopped advancing")
	}
	failure = nil
	b = Backlog{Pending: 1, Exhausted: 1}
	m.poll(ctx)
	if m.Health(ctx) == nil {
		t.Fatal("exhausted work is unhealthy")
	}
	b = Backlog{}
	m.poll(ctx)
	if err := m.Health(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Metrics()["test_oldest_pending_age_ms"] != 0 {
		t.Fatal("empty age")
	}
}

func TestRunCancellationReleasesBlockedSample(t *testing.T) {
	entered := make(chan struct{})
	m := New("test", sourceFunc(func(ctx context.Context) (Backlog, error) { close(entered); <-ctx.Done(); return Backlog{}, ctx.Err() }))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sample did not cancel")
	}
}
