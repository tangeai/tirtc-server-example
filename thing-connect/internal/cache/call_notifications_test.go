package cache_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
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

func TestCallNotificationsOrphanIndexDoesNotBlockQueue(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	live := f.create(t, "live", "caller", "callee")
	orphan := f.prefix + "orphan"
	for _, key := range []string{"call:outbox:due", "call:outbox:age"} {
		if err := f.rdb.ZAdd(ctx, key, redis.Z{Score: -1, Member: orphan}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.rdb.HSet(ctx, "call:outbox:attempts", orphan, 3).Err(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := f.store.ClaimRoomNotification(ctx, time.Minute, orphan); err != nil || found {
		t.Fatalf("scoped orphan claim: found=%v err=%v", found, err)
	}
	if !errors.Is(f.rdb.ZScore(ctx, "call:outbox:due", orphan).Err(), redis.Nil) || f.rdb.HExists(ctx, "call:outbox:attempts", orphan).Val() {
		t.Fatal("scoped claim kept orphan index")
	}
	for _, key := range []string{"call:outbox:due", "call:outbox:age"} {
		if err := f.rdb.ZAdd(ctx, key, redis.Z{Score: -1, Member: orphan}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	sink := &callCapture{}
	if err := call.NewService(f.store, sink).Dispatch(ctx, 32); err != nil {
		t.Fatalf("orphan index blocked dispatch: %v", err)
	}
	found := false
	for _, e := range sink.events {
		found = found || e.RoomID == live.RoomID
	}
	if !found {
		t.Fatalf("live room not delivered: %+v", sink.events)
	}
	if !errors.Is(f.rdb.ZScore(ctx, "call:outbox:due", orphan).Err(), redis.Nil) || !errors.Is(f.rdb.ZScore(ctx, "call:outbox:age", orphan).Err(), redis.Nil) {
		t.Fatal("orphan index retained")
	}
}

func TestCallNotificationsRepairOrphanCountWhileOtherRoomIsLeased(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	live := f.create(t, "still-pending", "caller", "a", "b")
	if _, ok, err := f.store.ClaimRoomNotification(ctx, time.Hour, live.RoomID); err != nil || !ok {
		t.Fatalf("lease: %v %v", ok, err)
	}
	lost := f.create(t, "evicted", "other", "x", "y", "z")
	if err := f.rdb.Del(ctx, "call:outbox:room:"+lost.RoomID).Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.store.ClaimRoomNotification(ctx, time.Hour, lost.RoomID); err != nil || ok {
		t.Fatalf("orphan: %v %v", ok, err)
	}
	restarted := cache.NewCallRooms(f.rdb)
	b, err := restarted.Backlog(ctx)
	if err != nil || b.Pending != 2 {
		t.Fatalf("surviving backlog: %+v %v", b, err)
	}
	if count, err := f.rdb.Get(ctx, "call:outbox:count").Int64(); err != nil || count != 2 {
		t.Fatalf("persistent count: %d %v", count, err)
	}
}

func TestCallNotificationsAppendReplacesEvictedLeasedList(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	r := f.create(t, "lost", "caller", "a", "b")
	old, ok, err := f.store.ClaimRoomNotification(ctx, time.Hour, r.RoomID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err := f.rdb.Del(ctx, "call:outbox:room:"+r.RoomID).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Answer(ctx, r.RoomID, r.Caller, r.Targets[0]); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompleteNotification(ctx, old); err != nil {
		t.Fatal(err)
	}
	b, err := f.store.Backlog(ctx)
	if err != nil || b.Pending != 2 {
		t.Fatalf("replaced backlog: %+v %v", b, err)
	}
	next, ok, err := f.store.ClaimRoomNotification(ctx, time.Hour, r.RoomID)
	if err != nil || !ok || next.Kind != "callee_answered" {
		t.Fatalf("replacement delayed by lost lease: %+v %v %v", next, ok, err)
	}
}

func TestCallNotificationsRequestFlushOnlyDeliversChangedRoom(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	backlog := f.create(t, "backlog", "caller-a", "callee-a")
	delivered := []string{}
	service := call.NewService(f.store, notificationPublishFunc(func(_ context.Context, e call.Notification) error {
		delivered = append(delivered, e.RoomID)
		return nil
	}))
	r := call.Room{RoomID: f.prefix + "request", Caller: f.prefix + "caller-b", Targets: []string{f.prefix + "callee-b"}, CallType: "audio", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if existing, err := service.Create(ctx, r, nil, time.Hour); err != nil || existing != "" {
		t.Fatalf("create: %q %v", existing, err)
	}
	if !reflect.DeepEqual(delivered, []string{r.RoomID}) {
		t.Fatalf("request flush delivered %v", delivered)
	}
	if n := f.rdb.LLen(ctx, "call:outbox:room:"+backlog.RoomID).Val(); n != 1 {
		t.Fatalf("request flush drained another room: %d", n)
	}

	// Auto-switch cancels the answering device's previous room in the same request.
	if _, err := service.Answer(ctx, r.RoomID, r.Caller, r.Targets[0]); err != nil {
		t.Fatal(err)
	}
	switched := f.create(t, "switched", "caller-c", "callee-b")
	delivered = nil
	result, err := service.Answer(ctx, switched.RoomID, switched.Caller, r.Targets[0])
	if err != nil || result.Previous == nil || result.Previous.RoomID != r.RoomID {
		t.Fatalf("auto-switch: %+v %v", result, err)
	}
	rooms := map[string]bool{}
	for _, id := range delivered {
		rooms[id] = true
	}
	if !rooms[r.RoomID] || !rooms[switched.RoomID] || rooms[backlog.RoomID] {
		t.Fatalf("auto-switch flush delivered %v", delivered)
	}
	for _, id := range []string{r.RoomID, switched.RoomID} {
		if n := f.rdb.LLen(ctx, "call:outbox:room:"+id).Val(); n != 0 {
			t.Fatalf("room %s kept %d notifications", id, n)
		}
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

func TestCallNotificationsManyOrphansDoNotStarveLiveRoom(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	live := f.create(t, "behind-orphans", "caller", "callee")
	for i := 0; i < 40; i++ {
		orphan := f.prefix + "orphan-" + strconv.Itoa(i)
		for _, key := range []string{"call:outbox:due", "call:outbox:age"} {
			if err := f.rdb.ZAdd(ctx, key, redis.Z{Score: -1, Member: orphan}).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	sink := &callCapture{}
	if err := call.NewService(f.store, sink).Dispatch(ctx, 32); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range sink.events {
		found = found || e.RoomID == live.RoomID
	}
	if !found {
		t.Fatalf("orphan index entries starved live room: %+v", sink.events)
	}
}

func TestCallNotificationsRecountConvergesUnderChurn(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	for i := 0; i < 300; i++ {
		f.create(t, "recount-"+strconv.Itoa(i), "caller-"+strconv.Itoa(i), "callee-"+strconv.Itoa(i))
	}
	if err := f.rdb.Set(ctx, "call:outbox:recount", 1, 0).Err(); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	churned := make(chan struct{})
	go func() {
		defer close(churned)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = f.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Incr(ctx, "call:outbox:count")
				pipe.Decr(ctx, "call:outbox:count")
				return nil
			})
		}
	}()
	for i := 0; i < 3; i++ {
		if _, err := f.store.Backlog(ctx); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	<-churned
	if f.rdb.Exists(ctx, "call:outbox:recount").Val() != 0 {
		t.Fatal("recount marker never cleared under concurrent queue changes")
	}
	rooms, err := f.rdb.ZRange(ctx, "call:outbox:due", 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	var want int64
	for _, room := range rooms {
		want += f.rdb.LLen(ctx, "call:outbox:room:"+room).Val()
	}
	b, err := f.store.Backlog(ctx)
	if err != nil || b.Pending != want {
		t.Fatalf("pending=%+v want=%d err=%v", b, want, err)
	}
}

func TestCallNotificationsExpiredSignalIsDroppedAndAlerts(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	r := f.create(t, "stale-signal", "caller", "callee")
	key := "call:outbox:room:" + r.RoomID
	var head call.Notification
	if err := json.Unmarshal([]byte(f.rdb.LIndex(ctx, key, 0).Val()), &head); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Minute).UnixMilli()
	head.CreatedAt = old
	body, err := json.Marshal(head)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.rdb.LSet(ctx, key, 0, body).Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.rdb.ZAdd(ctx, "call:outbox:age", redis.Z{Score: float64(old), Member: r.RoomID}).Err(); err != nil {
		t.Fatal(err)
	}
	sink := &callCapture{}
	if err := call.NewService(f.store, sink).Dispatch(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("stale call signaling delivered: %+v", sink.events)
	}
	if n := f.rdb.LLen(ctx, key).Val(); n != 0 {
		t.Fatalf("stale notification retained: %d", n)
	}
	if !errors.Is(f.rdb.ZScore(ctx, "call:outbox:age", r.RoomID).Err(), redis.Nil) {
		t.Fatal("dropped notification kept backlog age")
	}
	if score, err := f.rdb.ZScore(ctx, "call:outbox:expired", head.ID).Result(); err != nil || score <= 0 {
		t.Fatalf("drop not recorded: %v %v", score, err)
	}
	b, err := f.store.Backlog(ctx)
	if err != nil || b.Exhausted < 1 {
		t.Fatalf("drop not reported: %+v %v", b, err)
	}
	f.rdb.ZRem(ctx, "call:outbox:expired", head.ID)
}
