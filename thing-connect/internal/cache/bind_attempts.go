package cache

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"thing-connect/internal/bindlimit"
)

// BindAttemptStore keeps a per-user failure counter and cooldown marker. The
// hash tag keeps both keys of one user in the same slot.
type BindAttemptStore struct{ Client *redis.Client }

var _ bindlimit.Store = (*BindAttemptStore)(nil)

func bindAttemptKeys(userID int64) []string {
	tag := "bind_attempt:{" + strconv.FormatInt(userID, 10) + "}"
	return []string{tag + ":count", tag + ":lock"}
}

var reserveBindAttempt = redis.NewScript(`
local ttl = redis.call('PTTL', KEYS[2])
if ttl > 0 then return ttl end
local n = redis.call('INCR', KEYS[1])
if redis.call('PTTL', KEYS[1]) < 0 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
if n > tonumber(ARGV[2]) then
  redis.call('SET', KEYS[2], '1', 'PX', ARGV[3])
  redis.call('DEL', KEYS[1])
  return tonumber(ARGV[3])
end
return 0`)

var releaseBindAttempt = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 and redis.call('DECR', KEYS[1]) <= 0 then
  redis.call('DEL', KEYS[1])
end
return 0`)

var failBindAttempt = redis.NewScript(`
local ttl = redis.call('PTTL', KEYS[2])
if ttl > 0 then return ttl end
if tonumber(redis.call('GET', KEYS[1]) or '0') >= tonumber(ARGV[1]) then
  redis.call('SET', KEYS[2], '1', 'PX', ARGV[2])
  redis.call('DEL', KEYS[1])
  return tonumber(ARGV[2])
end
return 0`)

func (s *BindAttemptStore) Reserve(ctx context.Context, userID int64, p bindlimit.Policy) (time.Duration, error) {
	ms, err := reserveBindAttempt.Run(ctx, s.Client, bindAttemptKeys(userID), p.Window.Milliseconds(), p.MaxFailures, p.Cooldown.Milliseconds()).Int64()
	return time.Duration(ms) * time.Millisecond, err
}

func (s *BindAttemptStore) Release(ctx context.Context, userID int64) error {
	return releaseBindAttempt.Run(ctx, s.Client, bindAttemptKeys(userID)).Err()
}

func (s *BindAttemptStore) Fail(ctx context.Context, userID int64, p bindlimit.Policy) (time.Duration, error) {
	ms, err := failBindAttempt.Run(ctx, s.Client, bindAttemptKeys(userID), p.MaxFailures, p.Cooldown.Milliseconds()).Int64()
	return time.Duration(ms) * time.Millisecond, err
}
