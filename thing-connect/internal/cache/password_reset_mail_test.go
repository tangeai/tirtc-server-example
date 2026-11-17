package cache_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"thing-connect/internal/cache"
	"thing-connect/internal/testenv"
)

func TestPasswordResetMailGateAdmitsOneAndReleasesOnlyOwnReservation(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	client := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	email := fmt.Sprintf("gate-%d@example.test", time.Now().UnixNano())
	key := "password_reset_mail:pending:" + email
	t.Cleanup(func() { _ = client.Del(ctx, key).Err() })
	first, second := cache.NewPasswordResetMailGate(client), cache.NewPasswordResetMailGate(client)

	token, acquired, err := first.Acquire(ctx, email, time.Minute)
	if err != nil || !acquired || token == "" {
		t.Fatalf("first Acquire token=%q acquired=%v err=%v", token, acquired, err)
	}
	if ttl, err := client.TTL(ctx, key).Result(); err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("reservation ttl=%v err=%v", ttl, err)
	}
	if _, acquired, err = second.Acquire(ctx, email, time.Minute); err != nil || acquired {
		t.Fatalf("second instance acquired a pending email: acquired=%v err=%v", acquired, err)
	}
	if err = second.Release(ctx, email, "stale-token"); err != nil {
		t.Fatal(err)
	}
	if n, err := client.Exists(ctx, key).Result(); err != nil || n != 1 {
		t.Fatalf("foreign release removed the reservation: exists=%d err=%v", n, err)
	}
	if err = first.Release(ctx, email, token); err != nil {
		t.Fatal(err)
	}
	if _, acquired, err = second.Acquire(ctx, email, time.Minute); err != nil || !acquired {
		t.Fatalf("Acquire after release acquired=%v err=%v", acquired, err)
	}
}
