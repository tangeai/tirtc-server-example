package cache_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"thing-connect/internal/bindlimit"
	"thing-connect/internal/cache"
	"thing-connect/internal/testenv"
)

func TestBindAttemptStoreBoundsConcurrentGuesses(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	const user, other = int64(-9001), int64(-9002)
	keys := []string{"bind_attempt:{-9001}:count", "bind_attempt:{-9001}:lock", "bind_attempt:{-9002}:count", "bind_attempt:{-9002}:lock"}
	rdb.Del(ctx, keys...)
	t.Cleanup(func() { rdb.Del(ctx, keys...) })
	s := &cache.BindAttemptStore{Client: rdb}
	p := bindlimit.Policy{Window: time.Minute, MaxFailures: 10, Cooldown: time.Hour}

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			retry, err := s.Reserve(ctx, user, p)
			if err != nil {
				t.Error(err)
			} else if retry == 0 {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("allowed %d concurrent guesses", allowed.Load())
	}
	if ttl := rdb.PTTL(ctx, keys[1]).Val(); ttl <= 59*time.Minute || ttl > time.Hour {
		t.Fatalf("cooldown ttl=%s", ttl)
	}
	if retry, err := s.Reserve(ctx, user, p); err != nil || retry <= 59*time.Minute {
		t.Fatalf("locked reserve retry=%s err=%v", retry, err)
	}
	if retry, err := s.Reserve(ctx, other, p); err != nil || retry != 0 {
		t.Fatalf("other user locked: retry=%s err=%v", retry, err)
	}
}

func TestBindAttemptStoreRefundAndFailure(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	const user = int64(-9003)
	count, lock := "bind_attempt:{-9003}:count", "bind_attempt:{-9003}:lock"
	rdb.Del(ctx, count, lock)
	t.Cleanup(func() { rdb.Del(ctx, count, lock) })
	s := &cache.BindAttemptStore{Client: rdb}
	p := bindlimit.Policy{Window: time.Minute, MaxFailures: 2, Cooldown: time.Hour}

	if err := s.Release(ctx, user); err != nil {
		t.Fatal(err)
	}
	if rdb.Exists(ctx, count).Val() != 0 {
		t.Fatal("refund without a reservation created a negative counter")
	}
	if retry, err := s.Reserve(ctx, user, p); err != nil || retry != 0 {
		t.Fatalf("reserve retry=%s err=%v", retry, err)
	}
	if ttl := rdb.PTTL(ctx, count).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("window ttl=%s", ttl)
	}
	if err := s.Release(ctx, user); err != nil {
		t.Fatal(err)
	}
	if rdb.Exists(ctx, count).Val() != 0 {
		t.Fatal("refunded counter retained")
	}
	for i := 0; i < 2; i++ {
		if retry, err := s.Reserve(ctx, user, p); err != nil || retry != 0 {
			t.Fatalf("reserve %d retry=%s err=%v", i, retry, err)
		}
	}
	if retry, err := s.Fail(ctx, user, p); err != nil || retry != time.Hour {
		t.Fatalf("exhausting failure retry=%s err=%v", retry, err)
	}
	if rdb.Exists(ctx, count).Val() != 0 || rdb.Exists(ctx, lock).Val() != 1 {
		t.Fatal("cooldown did not replace the counter")
	}
}
