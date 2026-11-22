package cache_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"thing-connect/internal/cache"
	"thing-connect/internal/call"
	"thing-connect/internal/testenv"
)

type roomFixture struct {
	store  *cache.CallRooms
	rdb    *redis.Client
	prefix string
}

func newRoomFixture(t *testing.T) *roomFixture {
	t.Helper()
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	f := &roomFixture{cache.NewCallRooms(rdb), rdb, "test-call-" + uuid.NewString() + ":"}
	t.Cleanup(func() {
		ctx := context.Background()
		ids, err := rdb.ZRange(ctx, "call:outbox:age", 0, -1).Result()
		if err != nil {
			t.Error(err)
		}
		for _, id := range ids {
			if strings.HasPrefix(id, f.prefix) {
				n := rdb.LLen(ctx, "call:outbox:room:"+id).Val()
				rdb.Del(ctx, "call:outbox:room:"+id)
				rdb.ZRem(ctx, "call:outbox:due", id)
				rdb.ZRem(ctx, "call:outbox:age", id)
				rdb.HDel(ctx, "call:outbox:leases", id)
				rdb.HDel(ctx, "call:outbox:attempts", id)
				rdb.DecrBy(ctx, "call:outbox:count", n)
			}
		}
		for _, pattern := range []string{"room:" + f.prefix + "*", "room:lock:" + f.prefix + "*"} {
			var cursor uint64
			for {
				keys, next, err := rdb.Scan(ctx, cursor, pattern, 100).Result()
				if err != nil {
					t.Error(err)
					break
				}
				if len(keys) > 0 {
					if err := rdb.Del(ctx, keys...).Err(); err != nil {
						t.Error(err)
					}
				}
				cursor = next
				if cursor == 0 {
					break
				}
			}
		}
		_ = rdb.Close()
	})
	return f
}
func (f *roomFixture) create(t *testing.T, id, caller string, targets ...string) call.Room {
	t.Helper()
	r := call.Room{RoomID: f.prefix + id, Caller: f.prefix + caller, CallType: "audio", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, target := range targets {
		r.Targets = append(r.Targets, f.prefix+target)
	}
	existing, err := f.store.Create(context.Background(), r, nil, time.Hour)
	if err != nil || existing != "" {
		t.Fatalf("create: existing=%s err=%v", existing, err)
	}
	return r
}
func (f *roomFixture) lock(t *testing.T, device string) string {
	t.Helper()
	value, err := f.rdb.Get(context.Background(), "room:lock:"+device).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		t.Fatal(err)
	}
	return value
}
func TestCallRoomsStaleEndPreservesNewOwnership(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	old := f.create(t, "old", "a", "b")
	if _, err := f.store.Answer(ctx, old.RoomID, old.Caller, old.Targets[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.End(ctx, old.RoomID, old.Caller, false, "hangup"); err != nil {
		t.Fatal(err)
	}
	newer := f.create(t, "new", "a", "b")
	if _, err := f.store.Answer(ctx, newer.RoomID, newer.Caller, newer.Targets[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.End(ctx, old.RoomID, old.Caller, false, "hangup"); !errors.Is(err, call.ErrNotFound) {
		t.Fatal(err)
	}
	for _, id := range []string{newer.Caller, newer.Targets[0]} {
		if got := f.lock(t, id); got != newer.RoomID {
			t.Fatalf("late cleanup changed owner: %q", got)
		}
	}
	// Also cover a legacy room left behind while its device lock has moved on.
	legacy := f.create(t, "legacy", "other", "b")
	if err := f.rdb.HSet(ctx, "room:"+legacy.RoomID, "answered_by", newer.Targets[0], "status", "answered").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.End(ctx, legacy.RoomID, legacy.Caller, false, "hangup"); err != nil {
		t.Fatal(err)
	}
	if got := f.lock(t, newer.Targets[0]); got != newer.RoomID {
		t.Fatal("legacy cleanup removed a newer callee lock")
	}
}
func TestCallRoomsAnswerCancelRace(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		r := f.create(t, uuid.NewString(), "caller", "callee")
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() { <-start; _, err := f.store.Answer(ctx, r.RoomID, r.Caller, r.Targets[0]); errs <- err }()
		go func() { <-start; _, err := f.store.End(ctx, r.RoomID, r.Caller, true, "hangup"); errs <- err }()
		close(start)
		for n := 0; n < 2; n++ {
			if err := <-errs; err != nil && !errors.Is(err, call.ErrNotFound) {
				t.Fatal(err)
			}
		}
		if got, err := f.store.Get(ctx, r.RoomID); err != nil || got != nil {
			t.Fatalf("cancelled room recreated: %+v %v", got, err)
		}
		for _, id := range []string{r.Caller, r.Targets[0]} {
			if got := f.lock(t, id); got != "" {
				t.Fatalf("orphan lock after cancel: %s", got)
			}
		}
		if exists := f.rdb.Exists(ctx, "room:"+r.RoomID+":answered").Val(); exists != 0 {
			t.Fatal("orphan answer key")
		}
	}
}
func TestCallRoomsConcurrentWinnersAndIdempotentRetry(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	r := f.create(t, "race", "caller", "b", "c")
	var wg sync.WaitGroup
	results := make(chan call.AnswerResult, 2)
	errs := make(chan error, 2)
	for _, target := range r.Targets {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			res, err := f.store.Answer(ctx, r.RoomID, r.Caller, id)
			results <- res
			errs <- err
		}(target)
	}
	wg.Wait()
	close(results)
	close(errs)
	successes := 0
	winner := ""
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, call.ErrAnswered) {
			t.Fatal(err)
		}
	}
	for result := range results {
		if result.First {
			winner = result.Room.AnsweredBy
		}
	}
	if successes != 1 || winner == "" {
		t.Fatalf("successes=%d winner=%q", successes, winner)
	}
	before := f.rdb.PTTL(ctx, "room:"+r.RoomID).Val()
	again, err := f.store.Answer(ctx, r.RoomID, r.Caller, winner)
	if err != nil || again.First || again.Previous != nil {
		t.Fatalf("not idempotent: %+v %v", again, err)
	}
	if after := f.rdb.PTTL(ctx, "room:"+r.RoomID).Val(); after > before {
		t.Fatal("retry extended room lifetime")
	}
	for _, id := range r.Targets {
		res, err := f.store.Reject(ctx, r.RoomID, id, "decline")
		if err != nil || res.Applied || res.AllRejected {
			t.Fatalf("late rejection changed active room: %+v %v", res, err)
		}
	}
	if got := f.lock(t, winner); got != r.RoomID {
		t.Fatal("late rejection released winner")
	}
}
func TestCallRoomsConcurrentSwitchKeepsSingleOwner(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	a := f.create(t, "a", "caller-a", "callee")
	b := f.create(t, "b", "caller-b", "callee")
	errs := make(chan error, 2)
	for _, r := range []call.Room{a, b} {
		go func(r call.Room) { _, err := f.store.Answer(ctx, r.RoomID, r.Caller, r.Targets[0]); errs <- err }(r)
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	current, err := f.store.Current(ctx, a.Targets[0])
	if err != nil || current == nil {
		t.Fatalf("current: %+v %v", current, err)
	}
	for _, r := range []call.Room{a, b} {
		got, err := f.store.Get(ctx, r.RoomID)
		if err != nil {
			t.Fatal(err)
		}
		if r.RoomID == current.RoomID {
			if got == nil || f.lock(t, r.Caller) != r.RoomID {
				t.Fatal("winner lost caller lock")
			}
		} else if got != nil || f.lock(t, r.Caller) != "" {
			t.Fatal("old room survived auto-switch")
		}
	}
}
func TestCallRoomsLegacyDuplicateTargetsAndOrphanLock(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	r := f.create(t, "duplicates", "caller", "callee", "callee")
	result, err := f.store.Reject(ctx, r.RoomID, r.Targets[0], "decline")
	if err != nil || !result.AllRejected {
		t.Fatalf("distinct targets not accounted for: %+v %v", result, err)
	}
	if got := f.lock(t, r.Caller); got != "" {
		t.Fatal("caller remains locked")
	}
	if err := f.rdb.Set(ctx, "room:lock:"+r.Caller, r.RoomID, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.Current(ctx, r.Caller)
	if err != nil || got != nil || f.lock(t, r.Caller) != "" {
		t.Fatalf("orphan lock not recovered: %+v %v", got, err)
	}
}
