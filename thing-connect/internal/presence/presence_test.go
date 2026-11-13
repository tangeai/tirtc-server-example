package presence

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	durableQueue
	mu       sync.Mutex
	attempts [][]Observation
	fail     bool
	calls    chan struct{}
}

func (s *memoryStore) Project(ctx context.Context, items []Observation) error {
	s.mu.Lock()
	if len(items) > 0 {
		s.attempts = append(s.attempts, append([]Observation(nil), items...))
	}
	fail := s.fail
	s.fail = false
	s.mu.Unlock()
	select {
	case s.calls <- struct{}{}:
	default:
	}
	if fail {
		return errors.New("unavailable")
	}
	return ctx.Err()
}
func TestRetryPreservesBatchOrderAndCancellation(t *testing.T) {
	s := &memoryStore{fail: true, calls: make(chan struct{}, 100)}
	p := New(s, nil)
	if p.Ping(context.Background()) == nil {
		t.Fatal("unstarted projector is ready")
	}
	for i := 0; i < BatchSize; i++ {
		p.Observe(Observation{ClientID: "sn_a", At: time.Now(), Offline: i%2 == 0})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	select {
	case <-s.calls:
	case <-time.After(time.Second):
		t.Fatal("no flush")
	}
	p.Observe(Observation{ClientID: "sn_new", At: time.Now()})
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		count := len(s.attempts)
		s.mu.Unlock()
		if count >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry did not progress")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.attempts[0]) != BatchSize || len(s.attempts[1]) != BatchSize {
		t.Fatal("failed batch lost")
	}
	for i, o := range s.attempts[0] {
		if o != s.attempts[1][i] {
			t.Fatal("retry changed order")
		}
	}
	if len(s.attempts[2]) != 1 || s.attempts[2][0].ClientID != "sn_new" {
		t.Fatal("new event overtook retry")
	}
}
func TestOverflowIsBoundedAndUnhealthy(t *testing.T) {
	p := New(&memoryStore{}, nil)
	for i := 0; i < QueueSize+7; i++ {
		p.Observe(Observation{})
	}
	queued, rejected, _ := p.Stats()
	if queued != QueueSize || rejected != 7 {
		t.Fatalf("queue=%d rejected=%d", queued, rejected)
	}
	p.lastSuccess.Store(time.Now().UnixNano())
	if p.Ping(context.Background()) == nil {
		t.Fatal("lost observations reported healthy")
	}
}

func TestShutdownDrainsQueuedObservations(t *testing.T) {
	s := &memoryStore{}
	p := New(s, nil)
	for i := 0; i < 3; i++ {
		p.Observe(Observation{ClientID: "sn_a", At: time.Now()})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, batch := range s.attempts {
		count += len(batch)
	}
	if count != 3 {
		t.Fatalf("shutdown persisted %d observations", count)
	}
}
