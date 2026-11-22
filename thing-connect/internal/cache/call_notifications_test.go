package cache_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"thing-connect/internal/cache"
	"thing-connect/internal/call"
)

type callCapture struct {
	fail   bool
	events []call.Notification
}

func (p *callCapture) PublishCallNotification(ctx context.Context, event call.Notification) error {
	if _, ok := ctx.Deadline(); !ok {
		panic("delivery must have deadline")
	}
	p.events = append(p.events, event)
	if p.fail {
		return errors.New("broker unavailable")
	}
	return nil
}
func dueRoom(t *testing.T, f *roomFixture, id string) {
	t.Helper()
	if err := f.rdb.ZAdd(context.Background(), "call:outbox:due", redis.Z{Score: 0, Member: id}).Err(); err != nil {
		t.Fatal(err)
	}
}
func TestCallNotificationsFailureRestartAndStableID(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	r := f.create(t, "delivery", "caller", "callee")
	sink := &callCapture{fail: true}
	service := call.NewService(f.store, sink)
	if err := service.Dispatch(ctx, 1); err == nil {
		t.Fatal("expected broker failure")
	}
	first := sink.events[0]
	before, err := f.store.Backlog(ctx)
	if err != nil || before.Pending != 1 {
		t.Fatalf("failure lost event: %+v %v", before, err)
	}
	// A different process has only Redis and the publisher, no old memory.
	dueRoom(t, f, r.RoomID)
	sink.fail = false
	restarted := call.NewService(cache.NewCallRooms(f.rdb), sink)
	if err := restarted.Dispatch(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 2 || sink.events[1].ID != first.ID {
		t.Fatal("retry changed notification identity")
	}
	after, err := f.store.Backlog(ctx)
	if err != nil || after.Pending != 0 {
		t.Fatalf("completed event retained: %+v %v", after, err)
	}
}
func TestCallNotificationsLeaseSnapshotAndAppend(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	r := f.create(t, "lease", "caller", "callee")
	old, ok, err := f.store.ClaimNotification(ctx, time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := f.store.Answer(ctx, r.RoomID, r.Caller, r.Targets[0]); err != nil {
		t.Fatal(err)
	}
	// Appending an answer cannot reset the lease or overtake the incoming event.
	if _, ok, err := f.store.ClaimNotification(ctx, time.Minute); err != nil || ok {
		t.Fatalf("leased head overtaken: %v %v", ok, err)
	}
	dueRoom(t, f, r.RoomID)
	current, ok, err := f.store.ClaimNotification(ctx, time.Minute)
	if err != nil || !ok || current.ID != old.ID || current.Lease == old.Lease {
		t.Fatalf("reclaim: %+v %v", current, err)
	}
	if err := f.store.CompleteNotification(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RetryNotification(ctx, old, 0); err != nil {
		t.Fatal(err)
	}
	b, err := f.store.Backlog(ctx)
	if err != nil || b.Pending != 2 {
		t.Fatalf("stale owner changed queue: %+v %v", b, err)
	}
	if err := f.store.CompleteNotification(ctx, current); err != nil {
		t.Fatal(err)
	}
	next, ok, err := f.store.ClaimNotification(ctx, time.Minute)
	if err != nil || !ok || next.Kind != "callee_answered" {
		t.Fatalf("new event lost: %+v %v", next, err)
	}
}
func TestCallNotificationsCancelSuppressesObsoleteIncoming(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	r := f.create(t, "cancel", "caller", "callee")
	if _, err := f.store.End(ctx, r.RoomID, r.Caller, true, "cancel"); err != nil {
		t.Fatal(err)
	}
	sink := &callCapture{}
	s := call.NewService(f.store, sink)
	if err := s.Dispatch(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].Kind != "room_cancel" {
		t.Fatalf("stale incoming delivered: %+v", sink.events)
	}
}
func TestCallNotificationsHangupFailureSurvivesRoomDeletion(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	sink := &callCapture{}
	s := call.NewService(f.store, sink)
	r := f.create(t, "hangup", "caller", "callee")
	if err := s.Dispatch(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Answer(ctx, r.RoomID, r.Caller, r.Targets[0]); err != nil {
		t.Fatal(err)
	}
	sink.fail = true
	if _, err := s.End(ctx, r.RoomID, r.Caller, false, "hangup"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, r.RoomID); err != nil || got != nil {
		t.Fatal("room was not released")
	}
	last := sink.events[len(sink.events)-1]
	if last.Kind != "room_cancel" {
		t.Fatal(last.Kind)
	}
	if _, err := s.End(ctx, r.RoomID, r.Caller, false, "hangup"); !errors.Is(err, call.ErrNotFound) {
		t.Fatal(err)
	}
	dueRoom(t, f, r.RoomID)
	sink.fail = false
	if err := call.NewService(cache.NewCallRooms(f.rdb), sink).Dispatch(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if sink.events[len(sink.events)-1].ID != last.ID {
		t.Fatal("hangup was not retried after deletion")
	}
	b, err := f.store.Backlog(ctx)
	if err != nil || b.Pending != 0 {
		t.Fatalf("outbox not recovered: %+v %v", b, err)
	}
}
func TestCallNotificationsAgePersistsThroughRetryAndNewMessages(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	r := f.create(t, "age", "caller", "callee")
	oldTime := time.Now().Add(-20 * time.Minute).UnixMilli()
	if err := f.rdb.ZAdd(ctx, "call:outbox:age", redis.Z{Score: float64(oldTime), Member: r.RoomID}).Err(); err != nil {
		t.Fatal(err)
	}
	event, found, err := f.store.ClaimNotification(ctx, time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	if err := f.store.RetryNotification(ctx, event, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Answer(ctx, r.RoomID, r.Caller, r.Targets[0]); err != nil {
		t.Fatal(err)
	}
	b, err := f.store.Backlog(ctx)
	if err != nil || b.Pending != 2 || b.OldestAge < 19*time.Minute {
		t.Fatalf("age lost after append/retry: %+v %v", b, err)
	}
}
func TestCallNotificationsOrderingAndShutdown(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	r := f.create(t, "ordered", "caller", "b", "c")
	sink := &callCapture{}
	s := call.NewService(f.store, sink)
	if err := s.Dispatch(ctx, 10); err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, e := range sink.events {
		got = append(got, e.Target)
	}
	if !reflect.DeepEqual(got, r.Targets) {
		t.Fatalf("notification order=%v", got)
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.Run(stopped) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker ignored cancellation")
	}
}

// Cancellation can happen after MQTT accepts a message but before the HTTP
// request returns. Queue bookkeeping must outlive that request in both cases.
type notificationPublishFunc func(context.Context, call.Notification) error

func (f notificationPublishFunc) PublishCallNotification(ctx context.Context, e call.Notification) error {
	return f(ctx, e)
}

func TestCallNotificationsRequestCancellationKeepsBookkeeping(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "accepted"}[accepted], func(t *testing.T) {
			f := newRoomFixture(t)
			r := f.create(t, "cancel-request", "caller", "callee")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service := call.NewService(f.store, notificationPublishFunc(func(context.Context, call.Notification) error {
				cancel()
				if accepted {
					return nil
				}
				return context.Canceled
			}))
			if err := service.Dispatch(ctx, 1); !errors.Is(err, context.Canceled) {
				t.Fatalf("dispatch: %v", err)
			}
			pending, err := f.rdb.LLen(context.Background(), "call:outbox:room:"+r.RoomID).Result()
			if err != nil {
				t.Fatal(err)
			}
			want := int64(1)
			if accepted {
				want = 0
			}
			if pending != want {
				t.Fatalf("pending=%d want=%d", pending, want)
			}
			if n := f.rdb.HExists(context.Background(), "call:outbox:leases", r.RoomID).Val(); n {
				t.Fatal("cancelled request stranded lease")
			}
			if !accepted {
				dueRoom(t, f, r.RoomID)
				sink := &callCapture{}
				if err := call.NewService(f.store, sink).Dispatch(context.Background(), 1); err != nil {
					t.Fatal(err)
				}
				if len(sink.events) != 1 {
					t.Fatal("cancelled delivery was lost")
				}
			}
		})
	}
}

func TestCallNotificationsFailedRoomDoesNotBlockAnotherRoom(t *testing.T) {
	f := newRoomFixture(t)
	first := f.create(t, "first", "caller-a", "callee-a")
	second := f.create(t, "second", "caller-b", "callee-b")
	dueRoom(t, f, first.RoomID)
	delivered := []string{}
	service := call.NewService(f.store, notificationPublishFunc(func(_ context.Context, e call.Notification) error {
		if e.RoomID == first.RoomID {
			return errors.New("target publish failed")
		}
		delivered = append(delivered, e.RoomID)
		return nil
	}))
	if err := service.Dispatch(context.Background(), 32); err == nil {
		t.Fatal("expected first room failure")
	}
	if err := service.Dispatch(context.Background(), 32); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(delivered, []string{second.RoomID}) {
		t.Fatalf("other room blocked: %v", delivered)
	}
	if n := f.rdb.LLen(context.Background(), "call:outbox:room:"+first.RoomID).Val(); n != 1 {
		t.Fatal("failed room lost its retry")
	}
}

func TestCallNotificationsExpiredRoomDoesNotRing(t *testing.T) {
	f := newRoomFixture(t)
	r := f.create(t, "expired", "caller", "callee")
	if err := f.rdb.PExpireAt(context.Background(), "room:"+r.RoomID, time.Unix(1, 0)).Err(); err != nil {
		t.Fatal(err)
	}
	sink := &callCapture{}
	if err := call.NewService(f.store, sink).Dispatch(context.Background(), 32); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 0 {
		t.Fatal("expired room rang device")
	}
	if n := f.rdb.LLen(context.Background(), "call:outbox:room:"+r.RoomID).Val(); n != 0 {
		t.Fatal("obsolete notification retained")
	}
}
