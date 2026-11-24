package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"

	"thing-connect/internal/service"
)

const passwordResetMailPendingPrefix = "password_reset_mail:pending:"

type passwordResetMailGate struct{ client *redis.Client }

func NewPasswordResetMailGate(client *redis.Client) service.PasswordResetMailGate {
	return &passwordResetMailGate{client: client}
}

func (g *passwordResetMailGate) Acquire(ctx context.Context, email string, ttl time.Duration) (string, bool, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", false, err
	}
	token := hex.EncodeToString(raw[:])
	acquired, err := g.client.SetNX(ctx, passwordResetMailPendingPrefix+email, token, ttl).Result()
	return token, acquired, err
}

func (g *passwordResetMailGate) Release(ctx context.Context, email, token string) error {
	return g.client.Eval(ctx, `
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`, []string{passwordResetMailPendingPrefix + email}, token).Err()
}
