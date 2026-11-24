package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"thing-connect/internal/testenv"
)

type commandRecorder struct {
	mu    sync.Mutex
	calls int
	err   error
	slow  time.Duration
}

func (r *commandRecorder) deliver(ctx context.Context, _ string) error {
	r.mu.Lock()
	r.calls++
	err, slow := r.err, r.slow
	r.mu.Unlock()
	if slow > 0 {
		select {
		case <-time.After(slow):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (r *commandRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func newCommandFixture(t *testing.T) (*redis.Client, string) {
	t.Helper()
	cfg := testenv.LoadConfigOrSkip(t, "../tests/testdata/config.yaml")
	client := testenv.OpenRedisOrSkip(t, cfg)
	stream := fmt.Sprintf("thingconnect:test:user-commands:%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = client.Del(context.Background(), stream, stream+":exhausted").Err()
		_ = client.Close()
	})
	if err := client.XGroupCreateMkStream(context.Background(), stream, adminUserCommandGroup, "0").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.XAdd(context.Background(), &redis.XAddArgs{Stream: stream, Values: map[string]any{"type": "password_reset_email", "email": "a@example.test"}}).Err(); err != nil {
		t.Fatal(err)
	}
	return client, stream
}

func (c *adminUserCommands) readOne(ctx context.Context, t *testing.T) {
	t.Helper()
	streams, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: c.group, Consumer: c.consumer, Streams: []string{c.stream, ">"}, Count: 1, Block: time.Second}).Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range streams {
		for _, m := range s.Messages {
			c.process(ctx, m)
		}
	}
}

func TestAdminUserCommandNotTakenOverWhileBeingDelivered(t *testing.T) {
	if adminUserCommandMinIdle <= adminUserCommandDeliveryTimeout {
		t.Fatalf("MinIdle %v must exceed the delivery timeout %v", adminUserCommandMinIdle, adminUserCommandDeliveryTimeout)
	}
	client, stream := newCommandFixture(t)
	ctx := context.Background()
	slow := &commandRecorder{slow: 400 * time.Millisecond}
	first := &adminUserCommands{client: client, stream: stream, group: adminUserCommandGroup, consumer: "first", minIdle: 300 * time.Millisecond, deliver: slow.deliver}
	other := &commandRecorder{}
	second := &adminUserCommands{client: client, stream: stream, group: adminUserCommandGroup, consumer: "second", minIdle: time.Second, deliver: other.deliver}

	done := make(chan struct{})
	go func() { first.readOne(ctx, t); close(done) }()
	time.Sleep(350 * time.Millisecond)
	second.reclaim(ctx)
	<-done
	if other.count() != 0 {
		t.Fatal("command taken over while its delivery was still running")
	}
	if n, err := client.XPending(ctx, stream, adminUserCommandGroup).Result(); err != nil || n.Count != 0 {
		t.Fatalf("delivered command still pending: %+v err=%v", n, err)
	}
}

func TestAdminUserCommandExhaustionAndRecovery(t *testing.T) {
	client, stream := newCommandFixture(t)
	ctx := context.Background()
	failing := &commandRecorder{err: errors.New("smtp down")}
	consumer := &adminUserCommands{client: client, stream: stream, group: adminUserCommandGroup, consumer: "only", minIdle: time.Millisecond, deliver: failing.deliver}
	if b, err := consumer.Backlog(ctx); err != nil || b.Pending != 1 || b.Exhausted != 0 {
		t.Fatalf("unread backlog: %+v %v", b, err)
	}
	consumer.readOne(ctx, t)
	if b, err := consumer.Backlog(ctx); err != nil || b.Pending != 1 || b.Exhausted != 0 {
		t.Fatalf("inflight backlog: %+v %v", b, err)
	}
	for i := 0; i < adminUserCommandMaxDeliveries+2; i++ {
		time.Sleep(5 * time.Millisecond)
		consumer.reclaim(ctx)
	}
	if got := failing.count(); got != adminUserCommandMaxDeliveries {
		t.Fatalf("delivered %d times, want %d", got, adminUserCommandMaxDeliveries)
	}
	if n, err := client.XPending(ctx, stream, adminUserCommandGroup).Result(); err != nil || n.Count != 0 {
		t.Fatalf("exhausted command still pending: %+v err=%v", n, err)
	}
	b, err := consumer.Backlog(ctx)
	if err != nil || b.Pending != 0 || b.Exhausted != 1 || b.OldestAge != 0 {
		t.Fatalf("terminal backlog: %+v %v", b, err)
	}
	ids, err := client.ZRange(ctx, stream+":exhausted", 0, -1).Result()
	if err != nil || len(ids) != 1 {
		t.Fatalf("failure record: %v %v", ids, err)
	}
	if err := client.ZAdd(ctx, stream+":exhausted", redis.Z{Member: ids[0], Score: float64(time.Now().Add(-adminCommandFailureWindow - time.Minute).UnixMilli())}).Err(); err != nil {
		t.Fatal(err)
	}
	b, err = consumer.Backlog(ctx)
	if err != nil || b.Exhausted != 0 {
		t.Fatalf("failure window remained active: %+v %v", b, err)
	}

}

func TestAdminCommandOldestAgeIncludesUnreadAndFailedDelivery(t *testing.T) {
	client, stream := newCommandFixture(t)
	ctx := context.Background()
	if err := client.Del(ctx, stream).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.XGroupCreateMkStream(ctx, stream, adminUserCommandGroup, "0").Err(); err != nil {
		t.Fatal(err)
	}
	old := fmt.Sprintf("%d-0", time.Now().Add(-20*time.Minute).UnixMilli())
	for _, id := range []string{old, "*"} {
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: stream, ID: id, Values: map[string]any{"type": "password_reset_email", "email": "a@example.test"}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	failing := &commandRecorder{err: errors.New("unavailable")}
	c := &adminUserCommands{client: client, stream: stream, group: adminUserCommandGroup, consumer: "one", minIdle: time.Minute, deliver: failing.deliver}
	for _, n := range []int64{2, 2, 1} {
		b, err := c.Backlog(ctx)
		if err != nil || b.Pending != n || b.OldestAge < 19*time.Minute {
			t.Fatalf("pending %d: %+v %v", n, b, err)
		}
		if n == 1 {
			break
		}
		c.readOne(ctx, t)
		c.deliver = (&commandRecorder{}).deliver
	}
	other := &adminUserCommands{client: client, stream: stream, group: c.group, consumer: "restarted", minIdle: time.Millisecond, deliver: (&commandRecorder{}).deliver}
	time.Sleep(5 * time.Millisecond)
	other.reclaim(ctx)
	b, err := other.Backlog(ctx)
	if err != nil || b.Pending != 0 || b.OldestAge != 0 {
		t.Fatalf("after recovery: %+v %v", b, err)
	}
}

func TestAdminCommandStaleOwnerCannotFinishOrExhaustAfterTakeover(t *testing.T) {
	client, stream := newCommandFixture(t)
	ctx := context.Background()
	group := adminUserCommandGroup
	msgs, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: "old", Streams: []string{stream, ">"}, Count: 1}).Result()
	if err != nil {
		t.Fatal(err)
	}
	id := msgs[0].Messages[0].ID
	if err := client.XClaim(ctx, &redis.XClaimArgs{Stream: stream, Group: group, Consumer: "new", Messages: []string{id}}).Err(); err != nil {
		t.Fatal(err)
	}
	if n, err := finishAdminCommand.Run(ctx, client, []string{stream}, group, id, "old").Int64(); err != nil || n != 0 {
		t.Fatalf("late finish: %d %v", n, err)
	}
	if n, err := exhaustAdminCommand.Run(ctx, client, []string{stream, stream + ":exhausted"}, group, id, "old", 0, 1, adminCommandFailureWindow.Milliseconds()).Int64(); err != nil || n != 0 {
		t.Fatalf("stale exhaustion: %d %v", n, err)
	}
	if n, err := finishAdminCommand.Run(ctx, client, []string{stream}, group, id, "new").Int64(); err != nil || n != 1 {
		t.Fatalf("new owner: %d %v", n, err)
	}
}

type cancelAfterCommand struct {
	name   string
	cancel context.CancelFunc
}

func (h cancelAfterCommand) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h cancelAfterCommand) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h cancelAfterCommand) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if cmd.Name() == h.name {
			h.cancel()
		}
		return err
	}
}

func TestAdminCommandClaimedWhileStoppingIsStillAttempted(t *testing.T) {
	client, stream := newCommandFixture(t)
	if err := client.XReadGroup(context.Background(), &redis.XReadGroupArgs{Group: adminUserCommandGroup, Consumer: "crashed", Streams: []string{stream, ">"}, Count: 1}).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	client.AddHook(cancelAfterCommand{name: "xclaim", cancel: stop})
	delivered := &commandRecorder{}
	c := &adminUserCommands{client: client, stream: stream, group: adminUserCommandGroup, consumer: "stopping", minIdle: time.Millisecond, deliver: delivered.deliver}
	c.reclaim(ctx)
	if delivered.count() != 1 {
		t.Fatalf("claimed command delivered %d times while stopping, want 1", delivered.count())
	}
	if n, err := client.XPending(context.Background(), stream, adminUserCommandGroup).Result(); err != nil || n.Count != 0 {
		t.Fatalf("attempted command still pending: %+v err=%v", n, err)
	}
}

func TestAdminCommandReclaimReachesEntriesBeyondFirstPage(t *testing.T) {
	client, stream := newCommandFixture(t)
	ctx := context.Background()
	for i := 0; i < adminUserCommandReclaimPage; i++ {
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"type": "password_reset_email", "email": "a@example.test"}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	read, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: adminUserCommandGroup, Consumer: "crashed", Streams: []string{stream, ">"}, Count: adminUserCommandReclaimPage + 1}).Result()
	if err != nil || len(read[0].Messages) != adminUserCommandReclaimPage+1 {
		t.Fatalf("read %v err=%v", read, err)
	}
	messages := read[0].Messages
	minIdle := 200 * time.Millisecond
	time.Sleep(minIdle + 50*time.Millisecond)
	// Keep the first page busy so only the entry after it is reclaimable.
	busy := make([]string, 0, adminUserCommandReclaimPage)
	for _, m := range messages[:adminUserCommandReclaimPage] {
		busy = append(busy, m.ID)
	}
	if err := client.XClaimJustID(ctx, &redis.XClaimArgs{Stream: stream, Group: adminUserCommandGroup, Consumer: "busy", Messages: busy}).Err(); err != nil {
		t.Fatal(err)
	}
	delivered := &commandRecorder{}
	c := &adminUserCommands{client: client, stream: stream, group: adminUserCommandGroup, consumer: "recovering", minIdle: minIdle, deliver: delivered.deliver}
	for i := 0; i < 2 && delivered.count() == 0; i++ {
		c.reclaim(ctx)
	}
	if delivered.count() != 1 {
		t.Fatalf("entry beyond the first pending page delivered %d times, want 1", delivered.count())
	}
	last := messages[adminUserCommandReclaimPage].ID
	if p, err := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: adminUserCommandGroup, Start: last, End: last, Count: 1}).Result(); err != nil || len(p) != 0 {
		t.Fatalf("entry beyond the first page still pending: %+v err=%v", p, err)
	}
}
