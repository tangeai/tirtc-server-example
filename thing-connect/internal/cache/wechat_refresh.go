package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// WeChatRefreshWindow is the minimum interval between forced access_token
// refreshes of one application across VoIP instances. The grant is never
// released early, even when the forced request fails or times out: WeChat may
// already have rotated the token, and instances pick that up from stable reads.
// It is short enough that a failed refresh is retried well within a minute.
const WeChatRefreshWindow = 30 * time.Second

// WeChatRefreshGate grants one forced refresh per application per window.
// Redis stores no tokens.
func WeChatRefreshGate(client *redis.Client) func(context.Context, string) (bool, error) {
	if client == nil {
		panic("wechat refresh gate requires Redis")
	}
	return func(ctx context.Context, appID string) (bool, error) {
		return client.SetNX(ctx, "voip:wechat:refresh:"+appID, 1, WeChatRefreshWindow).Result()
	}
}
