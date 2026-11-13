package presence

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type durableQueue struct {
	mu    sync.Mutex
	items map[string]Observation
	gap   bool
}

func (q *durableQueue) Pending(context.Context) ([]Observation, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var r []Observation
	for _, o := range q.items {
		r = append(r, o)
	}
	return r, nil
}
func (q *durableQueue) Ack(_ context.Context, b []Observation) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, o := range b {
		if q.items[o.ClientID] == o {
			delete(q.items, o.ClientID)
		}
	}
	return nil
}
func (q *durableQueue) MarkGap(context.Context) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.gap = true
	return nil
}
func (q *durableQueue) HasGap(context.Context) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.gap, nil
}

type failingConnections struct {
	fail     bool
	received []Observation
}

func (s *failingConnections) SaveConnections(_ context.Context, b []Observation) error {
	if s.fail {
		return errors.New("mysql unavailable")
	}
	s.received = append(s.received, b...)
	return nil
}
func TestConnectionRecorderResumesPendingAfterRestart(t *testing.T) {
	o := Observation{ClientID: "sn_a", IP: "192.0.2.1", ConnectedAt: time.Now()}
	q := &durableQueue{items: map[string]Observation{"sn_a": o}}
	s := &failingConnections{fail: true}
	first := &connectionRecorder{store: s, queue: q}
	if first.flush(context.Background()) == nil {
		t.Fatal("write failure hidden")
	}
	if len(q.items) != 1 {
		t.Fatal("failed SQL discarded pending IP")
	}
	s.fail = false
	restarted := &connectionRecorder{store: s, queue: q}
	if err := restarted.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.received) != 1 || len(q.items) != 0 {
		t.Fatal("pending IP not recovered")
	}
}
func TestMetadataGapPersistsBeyondHeartbeatTTL(t *testing.T) {
	q := &durableQueue{items: map[string]Observation{}}
	projection := &memoryStore{}
	q = &projection.durableQueue
	p := New(projection, &failingConnections{})
	for i := 0; i < QueueSize; i++ {
		p.Observe(Observation{})
	}
	p.Observe(Observation{ClientID: "sn_a", IP: "192.0.2.1"})
	p.degradedUntil.Store(0)
	if err := p.apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	restarted := &connectionRecorder{store: &failingConnections{}, queue: q}
	if err := restarted.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted.healthy.Store(true)
	if restarted.Ping(context.Background()) == nil {
		t.Fatal("gap silently recovered after restart")
	}
}

func (q *durableQueue) Backlog(context.Context) (ConnectionBacklog, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := ConnectionBacklog{Pending: int64(len(q.items))}
	for _, o := range q.items {
		if result.OldestAt.IsZero() || o.At.Before(result.OldestAt) {
			result.OldestAt = o.At
		}
	}
	return result, nil
}

func TestSuccessfulWritesDoNotHideOverdueBacklog(t *testing.T) {
	old := time.Now().Add(-ConnectionMaxPendingAge - time.Second)
	projection := &memoryStore{durableQueue: durableQueue{items: map[string]Observation{"sn_a": {ClientID: "sn_a", At: old, ConnectedAt: old, IP: "192.0.2.1"}}}}
	p := New(projection, &failingConnections{})
	r := p.connections
	if err := r.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.healthy.Store(true)
	if r.Ping(context.Background()) == nil {
		t.Fatal("successful write masked overdue sampled backlog")
	}
	metrics := p.Metrics()
	if metrics["connection_pending"] != 1 || metrics["connection_oldest_pending_age_ms"] < ConnectionMaxPendingAge.Milliseconds() {
		t.Fatalf("metrics=%v", metrics)
	}
	if err := r.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Ping(context.Background()); err != nil {
		t.Fatal("drained backlog did not recover:", err)
	}
	if p.Metrics()["connection_pending"] != 0 || p.Metrics()["connection_oldest_pending_age_ms"] != 0 {
		t.Fatal("drained metrics stale")
	}
}
