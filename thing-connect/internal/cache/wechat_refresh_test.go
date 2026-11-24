package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"thing-connect/internal/cache"
	"thing-connect/internal/testenv"
)

func TestWeChatRefreshGateGrantsOncePerWindow(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	client := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	appID := uuid.NewString()
	key := "voip:wechat:refresh:" + appID
	t.Cleanup(func() { _ = client.Del(ctx, key).Err() })
	reserve := cache.WeChatRefreshGate(client)
	for i, want := range []bool{true, false} {
		granted, err := reserve(ctx, appID)
		if err != nil || granted != want {
			t.Fatalf("reserve #%d: granted=%v err=%v, want %v", i+1, granted, err, want)
		}
	}
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil || ttl <= 0 || ttl > cache.WeChatRefreshWindow || cache.WeChatRefreshWindow >= time.Minute {
		t.Fatalf("ttl=%v err=%v window=%v", ttl, err, cache.WeChatRefreshWindow)
	}
}
