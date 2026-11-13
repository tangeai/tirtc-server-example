package cache_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"thing-connect/internal/cache"
	"thing-connect/internal/presence"
	"thing-connect/internal/testenv"
)

func TestPresenceBatchRedisCompatibility(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	id := "sn_presence_batch_test"
	key := "online:" + id
	t.Cleanup(func() { rdb.Del(ctx, key) })
	store := cache.PresenceStore{Client: rdb}
	now := time.Now().UTC()
	online := presence.Observation{ClientID: id, At: now}
	if err := store.Project(ctx, []presence.Observation{online}); err != nil {
		t.Fatal(err)
	}
	if got := rdb.Get(ctx, key).Val(); got != now.Format(time.RFC3339Nano) {
		t.Fatalf("timestamp=%q", got)
	}
	if ttl := rdb.PTTL(ctx, key).Val(); ttl < 149*time.Second || ttl > presence.TTL {
		t.Fatalf("ttl=%s", ttl)
	}
	offline := presence.Observation{ClientID: id, At: now, Offline: true}
	if err := store.Project(ctx, []presence.Observation{offline, online}); err != nil {
		t.Fatal(err)
	}
	if rdb.Exists(ctx, key).Val() != 1 {
		t.Fatal("reconnect lost")
	}
	if err := store.Project(ctx, []presence.Observation{online, offline}); err != nil {
		t.Fatal(err)
	}
	if rdb.Exists(ctx, key).Val() != 1 {
		t.Fatal("unversioned disconnect deleted current connection")
	}
	online.At = now.Add(-2 * presence.TTL)
	if err := store.Project(ctx, []presence.Observation{online}); err != nil {
		t.Fatal(err)
	}
	if rdb.Exists(ctx, key).Val() != 0 {
		t.Fatal("retry revived expired observation")
	}
}

func TestPendingConnectionsSurviveRestartAndCompareAck(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	id := "sn_presence_pending_test"
	t.Cleanup(func() {
		rdb.Del(ctx, "online:"+id)
		rdb.HDel(ctx, "presence:connections:pending", id)
		rdb.ZRem(ctx, "presence:connections:pending_since", id)
	})
	first := &cache.PresenceStore{Client: rdb}
	now := time.Now().UTC().Truncate(time.Millisecond)
	old := presence.Observation{ClientID: id, IP: "192.0.2.1", At: now, ConnectedAt: now}
	if err := first.Project(ctx, []presence.Observation{old}); err != nil {
		t.Fatal(err)
	}
	// Older deployments have pending snapshots without an age index.
	if err := rdb.ZRem(ctx, "presence:connections:pending_since", id).Err(); err != nil {
		t.Fatal(err)
	}
	restarted := &cache.PresenceStore{Client: rdb}
	batch, err := restarted.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var saved presence.Observation
	for _, o := range batch {
		if o.ClientID == id {
			saved = o
		}
	}
	if _, err := rdb.ZScore(ctx, "presence:connections:pending_since", id).Result(); err != nil {
		t.Fatal("legacy snapshot age not recovered:", err)
	}
	if saved.IP != old.IP {
		t.Fatal("restart lost pending connection")
	}
	newer := old
	newer.IP = "2001:db8::1"
	newer.ConnectedAt = now.Add(time.Second)
	if err := restarted.Project(ctx, []presence.Observation{newer}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Ack(ctx, []presence.Observation{saved}); err != nil {
		t.Fatal(err)
	}
	// An old broker event cannot replace a newer pending snapshot.
	if err := restarted.Project(ctx, []presence.Observation{old}); err != nil {
		t.Fatal(err)
	}
	batch, err = restarted.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range batch {
		if o.ClientID == id {
			found = true
			if o.IP != newer.IP {
				t.Fatal("old event overwrote new IP")
			}
			if err := restarted.Ack(ctx, []presence.Observation{o}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("old acknowledgement deleted newer snapshot")
	}
	if rdb.HExists(ctx, "presence:connections:pending", id).Val() {
		t.Fatal("successful snapshot not acknowledged")
	}
}

type stalledConnectionWriter struct{ calls atomic.Int64 }

func (s *stalledConnectionWriter) SaveConnections(ctx context.Context, batch []presence.Observation) error {
	s.calls.Add(1)
	<-ctx.Done()
	return ctx.Err()
}
func TestRedisProjectionProgressesWhileSQLIsBlocked(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = rdb.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const id = "sn_projection_isolation_test"
	const second = "sn_projection_second_test"
	t.Cleanup(func() {
		rdb.Del(context.Background(), "online:"+id, "online:"+second)
		rdb.HDel(context.Background(), "presence:connections:pending", id)
		rdb.ZRem(context.Background(), "presence:connections:pending_since", id)
	})
	adapter := &cache.PresenceStore{Client: rdb}
	writer := &stalledConnectionWriter{}
	projector := presence.New(adapter, writer)
	done := make(chan struct{})
	go func() { projector.Run(ctx); close(done) }()
	now := time.Now().UTC()
	projector.Observe(presence.Observation{ClientID: id, IP: "192.0.2.3", At: now, ConnectedAt: now})
	deadline := time.Now().Add(3 * time.Second)
	for writer.calls.Load() == 0 {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("SQL worker never received pending IP")
		}
		time.Sleep(10 * time.Millisecond)
	}
	projector.Observe(presence.Observation{ClientID: second, At: time.Now()})
	deadline = time.Now().Add(time.Second)
	for rdb.Exists(context.Background(), "online:"+second).Val() == 0 {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("SQL failure stalled heartbeats")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("workers did not stop")
	}
	if !rdb.HExists(context.Background(), "presence:connections:pending", id).Val() {
		t.Fatal("shutdown discarded pending IP")
	}
}

func TestConnectionBacklogAgeAndConditionalAcknowledgement(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	const id = "sn_backlog_metrics_test"
	t.Cleanup(func() {
		rdb.Del(ctx, "online:"+id)
		rdb.HDel(ctx, "presence:connections:pending", id)
		rdb.ZRem(ctx, "presence:connections:pending_since", id)
	})
	s := &cache.PresenceStore{Client: rdb}
	now := time.Now().UTC().Truncate(time.Millisecond)
	old := presence.Observation{ClientID: id, IP: "192.0.2.1", At: now, ConnectedAt: now}
	if err := s.Project(ctx, []presence.Observation{old}); err != nil {
		t.Fatal(err)
	}
	score, err := rdb.ZScore(ctx, "presence:connections:pending_since", id).Result()
	if err != nil {
		t.Fatal(err)
	}
	newer := old
	newer.IP = "192.0.2.2"
	newer.ConnectedAt = now.Add(time.Second)
	if err := s.Project(ctx, []presence.Observation{newer}); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(ctx, []presence.Observation{old}); err != nil {
		t.Fatal(err)
	}
	if got := rdb.ZScore(ctx, "presence:connections:pending_since", id).Val(); got != score {
		t.Fatal("new snapshot reset waiting age or old ACK deleted index")
	}
	metrics, err := s.Backlog(ctx)
	if err != nil || metrics.Pending < 1 || metrics.OldestAt.IsZero() {
		t.Fatalf("backlog=%+v err=%v", metrics, err)
	}
	if err := s.Ack(ctx, []presence.Observation{newer}); err != nil {
		t.Fatal(err)
	}
	if _, err := rdb.ZScore(ctx, "presence:connections:pending_since", id).Result(); err == nil {
		t.Fatal("successful ACK retained age index")
	}
}
