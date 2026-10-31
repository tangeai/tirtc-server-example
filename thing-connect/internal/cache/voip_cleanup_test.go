package cache_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"thing-connect/internal/cache"
	"thing-connect/internal/testenv"
	"thing-connect/internal/voip"
)

func TestVoipCleanupOnlyReleasesMatchingCall(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	client := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = client.Close() })
	id := fmt.Sprintf("cleanup-test-%d", time.Now().UnixNano())
	contacts := []voip.Contact{{AppID: id, OpenID: "same"}, {AppID: id, OpenID: "other"}}
	keys := []string{"voip:device-call:" + id, "voip:contact-call:" + id + ":same", "voip:contact-call:" + id + ":other"}
	ctx := context.Background()
	t.Cleanup(func() { _ = client.Del(ctx, keys...).Err() })
	for i, key := range keys {
		call := "old-call"
		if i == 2 {
			call = "another-call"
		}
		if err := client.Set(ctx, key, call, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	guards := cache.NewVoipCallGuards(client)
	for i := 0; i < 2; i++ {
		if err := guards.ReleaseDevice(ctx, id, contacts); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := client.Exists(ctx, keys[:2]...).Result(); err != nil || n != 0 {
		t.Fatalf("matching guards: count=%d err=%v", n, err)
	}
	if value, err := client.Get(ctx, keys[2]).Result(); err != nil || value != "another-call" {
		t.Fatalf("other call guard=%q err=%v", value, err)
	}
}
