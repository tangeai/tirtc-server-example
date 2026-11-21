package presence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	durableQueue
	mu       sync.Mutex
	attempts [][]Observation
	written  map[string]Observation
	failures int
	calls    chan struct{}
	onWrite  func()
}

func (s *memoryStore) Project(ctx context.Context, items []Observation) error {
	s.mu.Lock()
	if len(items) > 0 {
		s.attempts = append(s.attempts, append([]Observation(nil), items...))
	}
	fail := s.failures > 0
	if fail {
		s.failures--
	} else {
		if s.written == nil {
			s.written = map[string]Observation{}
		}
		for _, o := range items {
			if old, ok := s.written[o.ClientID]; !ok || o.ExpiresAt.After(old.ExpiresAt) {
				s.written[o.ClientID] = o
			}
		}
	}
	onWrite := s.onWrite
	s.mu.Unlock()
	if s.calls != nil {
		select {
		case s.calls <- struct{}{}:
		default:
		}
	}
	if onWrite != nil {
		onWrite()
	}
	if fail {
		return errors.New("unavailable")
	}
	return ctx.Err()
}

func (s *memoryStore) snapshot() map[string]Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]Observation, len(s.written))
	for k, v := range s.written {
		result[k] = v
	}
	return result
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFailedWritesKeepReceivingAndMergeLatestState(t *testing.T) {
	s := &memoryStore{failures: 3, calls: make(chan struct{}, 100)}
	p := New(s, nil, nil)
	if p.Ping(context.Background()) == nil {
		t.Fatal("unstarted projector is ready")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	base := time.Now()
	const devices = 3 * BatchSize
	for round := 0; round < 2; round++ {
		for i := 0; i < devices; i++ {
			p.Observe(Observation{ClientID: fmt.Sprintf("sn_%d", i), At: base.Add(time.Duration(round) * time.Second)})
		}
	}
	p.Observe(Observation{ClientID: "sn_0", Offline: true, At: base.Add(time.Hour)})
	waitFor(t, "receive queue stalled while writes failed", func() bool {
		queued, _, _ := p.Stats()
		return queued == 0
	})
	waitFor(t, "merged state not written after recovery", func() bool { return len(s.snapshot()) == devices })
	for id, o := range s.snapshot() {
		if !o.At.Equal(base.Add(time.Second)) {
			t.Fatalf("%s kept stale observation %s", id, o.At)
		}
	}
	_, rejected, failures := p.Stats()
	if rejected != 0 || failures < 3 {
		t.Fatalf("rejected=%d failures=%d", rejected, failures)
	}
	waitFor(t, "recovered projector not ready", func() bool { return p.Ping(context.Background()) == nil })
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
}

func TestMergeKeepsLatestExpiryAndNewestConnection(t *testing.T) {
	w := &writer{p: New(&memoryStore{}, nil, nil), pending: map[string]Observation{}}
	at := time.Unix(1000, 0)
	w.merge(Observation{ClientID: "sn_a", At: at.Add(time.Minute), ExpiresAt: at.Add(time.Minute + TTL)})
	w.merge(Observation{ClientID: "sn_a", At: at, ExpiresAt: at.Add(TTL), IP: "192.0.2.2", ConnectedAt: at.Add(2 * time.Second)})
	w.merge(Observation{ClientID: "sn_a", At: at, ExpiresAt: at.Add(TTL), IP: "192.0.2.1", ConnectedAt: at.Add(time.Second)})
	got := w.pending["sn_a"]
	if !got.ExpiresAt.Equal(at.Add(time.Minute+TTL)) || got.IP != "192.0.2.2" || !got.ConnectedAt.Equal(at.Add(2*time.Second)) {
		t.Fatalf("merged=%+v", got)
	}
}

func TestMergeBufferIsBounded(t *testing.T) {
	p := New(&memoryStore{}, nil, nil)
	w := &writer{p: p, pending: map[string]Observation{}}
	for i := 0; i < MaxPendingDevices; i++ {
		w.merge(Observation{ClientID: fmt.Sprintf("sn_%d", i)})
	}
	w.merge(Observation{ClientID: "sn_0", At: time.Now()})
	w.merge(Observation{ClientID: "sn_overflow", IP: "192.0.2.1"})
	if len(w.pending) != MaxPendingDevices {
		t.Fatalf("pending=%d", len(w.pending))
	}
	if _, rejected, _ := p.Stats(); rejected != 1 || !p.unresolved(p.lostIPAt.Load()) {
		t.Fatalf("overflow not recorded: rejected=%d", rejected)
	}
}

func TestOverflowIsBoundedAndUnhealthy(t *testing.T) {
	p := New(&memoryStore{}, nil, nil)
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

func TestTemporaryConnectionStaysOnlineForPairingCode(t *testing.T) {
	codeTTL := 190 * time.Second
	p := New(&memoryStore{}, nil, func() time.Duration { return codeTTL })
	at := time.Unix(1000, 0)
	p.Observe(Observation{ClientID: "tmp_a1b2c3d4", At: at})
	p.Observe(Observation{ClientID: "sn_device", At: at})
	codeTTL = 60 * time.Second
	p.Observe(Observation{ClientID: "tmp_a1b2c3d4", At: at})
	want := []time.Duration{190 * time.Second, TTL, TTL}
	for i, ttl := range want {
		if got := (<-p.queue).ExpiresAt.Sub(at); got != ttl {
			t.Fatalf("observation %d ttl=%s want %s", i, got, ttl)
		}
	}
}

func TestShutdownDrainsQueuedObservations(t *testing.T) {
	s := &memoryStore{}
	p := New(s, nil, nil)
	for i := 0; i < 3; i++ {
		p.Observe(Observation{ClientID: fmt.Sprintf("sn_%d", i), At: time.Now()})
	}
	p.Observe(Observation{ClientID: "sn_0", At: time.Now().Add(time.Second)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx)
	if got := len(s.snapshot()); got != 3 {
		t.Fatalf("shutdown persisted %d devices", got)
	}
}

type brokerList struct {
	pages  [][]Observation
	err    error
	during func()
}

func (b *brokerList) ListConnected(_ context.Context, visit func([]Observation) error) error {
	for _, page := range b.pages {
		if b.during != nil {
			b.during()
		}
		if err := visit(append([]Observation(nil), page...)); err != nil {
			return err
		}
	}
	return b.err
}

func TestReconcileRestoresBrokerStateAndResolvesEarlierLoss(t *testing.T) {
	projection := &memoryStore{}
	p := New(projection, &failingConnections{}, func() time.Duration { return 190 * time.Second })
	p.lastSuccess.Store(time.Now().UnixNano())
	p.connections.healthy.Store(true)
	for i := 0; i < QueueSize; i++ {
		p.Observe(Observation{})
	}
	p.Observe(Observation{ClientID: "sn_a", IP: "192.0.2.1"})
	if err := p.apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !p.NeedsReconcile() || p.Ping(context.Background()) == nil {
		t.Fatal("lost connection event not reported")
	}
	time.Sleep(2 * time.Millisecond)
	connectedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
	list := &brokerList{pages: [][]Observation{
		{{ClientID: "sn_a", IP: "192.0.2.9", ConnectedAt: connectedAt}},
		{{ClientID: "tmp_a1b2c3d4"}},
	}}
	if err := p.Reconcile(context.Background(), list); err != nil {
		t.Fatal(err)
	}
	written := projection.snapshot()
	if got := written["sn_a"]; got.IP != "192.0.2.9" || got.ExpiresAt.Sub(got.At) != TTL {
		t.Fatalf("formal connection=%+v", got)
	}
	if got := written["tmp_a1b2c3d4"]; got.ExpiresAt.Sub(got.At) != 190*time.Second {
		t.Fatalf("temporary connection=%+v", got)
	}
	if err := p.connections.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.NeedsReconcile() {
		t.Fatal("reconciled loss still pending")
	}
	if err := p.Ping(context.Background()); err != nil {
		t.Fatal("reconciled projector not ready:", err)
	}
	if m := p.Metrics(); m["presence_reconcile_clients"] != 2 || m["presence_reconcile_last_ms"] == 0 {
		t.Fatalf("metrics=%v", m)
	}
}

func TestLossDuringReconcileStaysUnresolved(t *testing.T) {
	projection := &memoryStore{}
	p := New(projection, &failingConnections{}, nil)
	list := &brokerList{pages: [][]Observation{{{ClientID: "sn_a"}}}, during: func() {
		time.Sleep(2 * time.Millisecond)
		p.lose(Observation{ClientID: "sn_b", IP: "192.0.2.2"})
		_ = p.apply(context.Background(), nil)
	}}
	if err := p.Reconcile(context.Background(), list); err != nil {
		t.Fatal(err)
	}
	if !p.NeedsReconcile() {
		t.Fatal("loss during listing was cleared")
	}
	if gap, _ := projection.HasGap(context.Background()); !gap {
		t.Fatal("durable gap cleared by an older listing")
	}
}

func TestFailedReconcileKeepsLoss(t *testing.T) {
	p := New(&memoryStore{}, nil, nil)
	p.lose(Observation{ClientID: "sn_a"})
	if err := p.Reconcile(context.Background(), &brokerList{err: errors.New("api down")}); err == nil {
		t.Fatal("broker failure hidden")
	}
	if !p.NeedsReconcile() || p.Metrics()["presence_reconcile_failures_total"] != 1 {
		t.Fatal("failed listing resolved loss")
	}
}

func TestReconcilerRunsAtStartupAndAfterLoss(t *testing.T) {
	projection := &memoryStore{}
	p := New(projection, nil, nil)
	var mu sync.Mutex
	calls := 0
	list := &brokerList{pages: [][]Observation{{{ClientID: "sn_a"}}}, during: func() {
		mu.Lock()
		calls++
		mu.Unlock()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { p.RunReconciler(ctx, func() BrokerClients { return list }); close(done) }()
	count := func() int { mu.Lock(); defer mu.Unlock(); return calls }
	waitFor(t, "no startup reconciliation", func() bool { return count() == 1 })
	cancel()
	<-done
	if count() != 1 {
		t.Fatal("reconciled without a loss")
	}
}

func TestSubscriptionGapRequestsReconciliation(t *testing.T) {
	p := New(&memoryStore{}, nil, nil)
	p.lastSuccess.Store(time.Now().UnixNano())
	p.SubscriptionGap()
	if !p.NeedsReconcile() || p.Ping(context.Background()) == nil || p.Metrics()["presence_subscription_gaps_total"] != 1 {
		t.Fatal("subscription gap not reported")
	}
	time.Sleep(2 * time.Millisecond)
	if err := p.Reconcile(context.Background(), &brokerList{}); err != nil {
		t.Fatal(err)
	}
	if p.NeedsReconcile() || p.Ping(context.Background()) != nil {
		t.Fatal("reconciled gap still reported")
	}
}
