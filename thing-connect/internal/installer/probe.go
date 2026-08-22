package installer

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type StandardProber struct{}

func NewStandardProber() *StandardProber { return &StandardProber{} }

func (p *StandardProber) Probe(ctx context.Context, draft Draft) error {
	if err := validateDraft(draft); err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if err := probeRedis(probeCtx, draft.Redis); err != nil {
		return fmt.Errorf("%w: %v", ErrRedisUnavailable, err)
	}
	return nil
}

func probeRedis(ctx context.Context, input RedisInput) error {
	client := redis.NewClient(&redis.Options{
		Addr: net.JoinHostPort(input.Host, strconv.Itoa(input.Port)), Password: input.Password, DB: input.DB,
	})
	defer func() { _ = client.Close() }()
	return client.Ping(ctx).Err()
}
