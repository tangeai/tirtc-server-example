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
		_ = client.Del(context.Background(), stream).Err()
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

func TestAdminUserCommandDroppedAfterMaxDeliveries(t *testing.T) {
	client, stream := newCommandFixture(t)
	ctx := context.Background()
	failing := &commandRecorder{err: errors.New("smtp down")}
	consumer := &adminUserCommands{client: client, stream: stream, group: adminUserCommandGroup, consumer: "only", minIdle: time.Millisecond, deliver: failing.deliver}
	consumer.readOne(ctx, t)
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
}
