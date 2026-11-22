package mysql

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"thing-connect/internal/testenv"
	"time"
)

func TestEmailDeliveryGenerationProtectsNewAndIdenticalCodes(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../tests/testdata/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	defer rdb.Close()
	store := NewCacheStore(rdb)
	ctx := context.Background()
	email := uuid.NewString() + "@example.test"
	t.Cleanup(func() { rdb.Del(ctx, "email_code:"+email, "email_code_delivery:"+email) })
	for _, code := range []string{"222222", "111111"} {
		if err := store.SetEmailCodeDelivery(ctx, email, "111111", "old", time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := store.SetEmailCodeDelivery(ctx, email, code, "new", time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := store.DiscardEmailCodeDelivery(ctx, email, "old"); err != nil {
			t.Fatal(err)
		}
		if got, err := store.GetEmailCode(ctx, email); err != nil || got != code {
			t.Fatalf("new code removed: %q %v", got, err)
		}
		if err := store.DiscardEmailCodeDelivery(ctx, email, "new"); err != nil {
			t.Fatal(err)
		}
		if got, err := store.GetEmailCode(ctx, email); err != nil || got != "" {
			t.Fatalf("own failed code retained: %q %v", got, err)
		}
	}
	// A legacy/manual write invalidates the preceding delivery generation.
	if err := store.SetEmailCodeDelivery(ctx, email, "111111", "old", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEmailCode(ctx, email, "333333", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.DiscardEmailCodeDelivery(ctx, email, "old"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetEmailCode(ctx, email); err != nil || got != "333333" {
		t.Fatalf("legacy write removed: %q %v", got, err)
	}
}

func TestEmailDeliveryConsumedGenerationCannotDeleteReplacement(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../tests/testdata/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	defer rdb.Close()
	cache := NewCacheStore(rdb)
	ctx := context.Background()
	email := uuid.NewString() + "@example.test"
	t.Cleanup(func() { rdb.Del(ctx, "email_code:"+email, "email_code_delivery:"+email) })
	if err := cache.SetEmailCodeDelivery(ctx, email, "123456", "old", time.Minute); err != nil {
		t.Fatal(err)
	}
	if consumed, err := cache.ConsumeEmailCode(ctx, email, "123456"); err != nil || !consumed {
		t.Fatalf("consume=%v err=%v", consumed, err)
	}
	// Even an identical numeric code belongs to a different send after consumption.
	if err := cache.SetEmailCodeDelivery(ctx, email, "123456", "new", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := cache.DiscardEmailCodeDelivery(ctx, email, "old"); err != nil {
		t.Fatal(err)
	}
	if consumed, err := cache.ConsumeEmailCode(ctx, email, "123456"); err != nil || !consumed {
		t.Fatalf("replacement lost: consume=%v err=%v", consumed, err)
	}
	if consumed, err := cache.ConsumeEmailCode(ctx, email, "123456"); err != nil || consumed {
		t.Fatalf("code reused: consume=%v err=%v", consumed, err)
	}
}
