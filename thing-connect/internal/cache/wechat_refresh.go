package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// WeChatRefreshGate keeps the grant until expiry even if the forced request
// times out: WeChat might already have rotated the token. Redis stores no tokens.
func WeChatRefreshGate(client *redis.Client) func(context.Context, string) (bool, error) {
	if client == nil {
		panic("wechat refresh gate requires Redis")
	}
	return func(ctx context.Context, appID string) (bool, error) {
		return client.SetNX(ctx, "voip:wechat:refresh:"+appID, 1, time.Minute).Result()
	}
}
