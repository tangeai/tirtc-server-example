// Package cache creates Redis clients and infrastructure adapters.
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"thing-connect/internal/config"
)

// options makes a caller's context deadline bound both the pool wait and the
// socket read. Without ContextTimeoutEnabled go-redis waits for its own 3
// second read timeout even when the request has less time left.
func options(cfg config.RedisCfg) *redis.Options {
	return &redis.Options{
		Addr:                  cfg.Addr,
		Password:              cfg.Password,
		DB:                    cfg.DB,
		ContextTimeoutEnabled: true,
		// The use case/outbox owns retries. Replaying an uncertain write here
		// could increment counters twice or multiply retries across layers.
		MaxRetries: -1,
	}
}

func New(cfg config.RedisCfg) (*redis.Client, error) {
	rdb := redis.NewClient(options(cfg))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	return rdb, nil
}
