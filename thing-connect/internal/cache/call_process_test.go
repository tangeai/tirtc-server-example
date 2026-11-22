package cache_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"thing-connect/internal/call"
)

// Shorten only the claim lease so real process-death recovery stays fast.
type crashTestRooms struct{ call.Rooms }

func (s crashTestRooms) ClaimNotification(ctx context.Context, _ time.Duration) (call.Notification, bool, error) {
	return s.Rooms.ClaimNotification(ctx, 150*time.Millisecond)
}

func TestCallNotificationCrashWorker(t *testing.T) {
	if os.Getenv("THING_CONNECT_CRASH_WORKER") != "1" {
		return
	}
	f := newRoomFixture(t)
	receipt := os.Getenv("THING_CONNECT_CRASH_RECEIPT")
	publisher := notificationPublishFunc(func(ctx context.Context, e call.Notification) error {
		// Models broker acceptance followed by process death before queue completion.
		if err := f.rdb.Set(ctx, receipt, e.ID, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		os.Exit(42)
		return nil
	})
	if err := call.NewService(crashTestRooms{f.store}, publisher).Dispatch(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	t.Fatal("worker never reached the publish/ack crash point")
}

func TestCallNotificationProcessCrashAfterPublishRecovers(t *testing.T) {
	f := newRoomFixture(t)
	r := f.create(t, "process-crash", "caller", "callee")
	dueRoom(t, f, r.RoomID)
	receipt := f.prefix + "published"
	t.Cleanup(func() { f.rdb.Del(context.Background(), receipt) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCallNotificationCrashWorker$")
	command.Env = append(os.Environ(), "THING_CONNECT_CRASH_WORKER=1", "THING_CONNECT_CRASH_RECEIPT="+receipt)
	output, err := command.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 42 {
		t.Fatalf("crash worker: %v %s", err, output)
	}
	original, err := f.rdb.Get(ctx, receipt).Result()
	if err != nil {
		t.Fatal(err)
	}
	sink := &callCapture{}
	restarted := call.NewService(f.store, sink)
	deadline := time.Now().Add(3 * time.Second)
	for len(sink.events) == 0 && time.Now().Before(deadline) {
		if err := restarted.Dispatch(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if len(sink.events) == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if len(sink.events) != 1 || sink.events[0].ID != original {
		t.Fatalf("restart lost or changed event: %+v", sink.events)
	}
	if n, err := f.rdb.LLen(ctx, "call:outbox:room:"+r.RoomID).Result(); err != nil || n != 0 {
		t.Fatalf("queue did not drain: %d %v", n, err)
	}
}
