package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"thing-connect/internal/presence"
)

const connectionPendingKey = "presence:connections:pending"
const connectionPendingSinceKey = "presence:connections:pending_since"

const connectionGapKey = "presence:connections:gap"

// PresenceStore uses a dedicated, deadline-aware Redis client. Online values
// retain the public key/format/TTL contract. Pending IP snapshots have no TTL.
// Multi-key scripts require the existing single-endpoint Redis deployment.
type PresenceStore struct {
	Client *redis.Client
	cursor uint64 // owned exclusively by the connection persistence worker
}

type connectionSnapshot struct {
	ClientID string `json:"client_id"`
	IP       string `json:"ip"`
	AtMS     int64  `json:"at_ms"`
}

func connectionJSON(o presence.Observation) string {
	data, _ := json.Marshal(connectionSnapshot{ClientID: o.ClientID, IP: o.IP, AtMS: o.ConnectedAt.UnixMilli()})
	return string(data)
}

const writePresence = `
redis.call('SET',KEYS[1],ARGV[1])
redis.call('PEXPIREAT',KEYS[1],ARGV[2])
if ARGV[3] ~= '' then
 local old=redis.call('HGET',KEYS[2],ARGV[3])
 if not old or cjson.decode(old).at_ms < tonumber(ARGV[5]) then
  redis.call('HSET',KEYS[2],ARGV[3],ARGV[4])
  redis.call('ZADD',KEYS[3],'NX',ARGV[6],ARGV[3])
 end
end
return 1`

func (s *PresenceStore) Project(ctx context.Context, batch []presence.Observation) error {
	if len(batch) == 0 {
		return s.Client.Ping(ctx).Err()
	}
	pipe := s.Client.Pipeline()
	for _, o := range batch {
		// Unversioned SYS disconnects cannot safely delete a current connection.
		// Heartbeat expiration is authoritative; late disconnects are harmless.
		if o.Offline {
			continue
		}
		if o.IP == "" {
			pipe.Eval(ctx, `redis.call('SET',KEYS[1],ARGV[1]);redis.call('PEXPIREAT',KEYS[1],ARGV[2]);return 1`, []string{"online:" + o.ClientID}, o.At.UTC().Format(time.RFC3339Nano), o.At.Add(presence.TTL).UnixMilli())
		} else {
			pipe.Eval(ctx, writePresence, []string{"online:" + o.ClientID, connectionPendingKey, connectionPendingSinceKey}, o.At.UTC().Format(time.RFC3339Nano), o.At.Add(presence.TTL).UnixMilli(), o.ClientID, connectionJSON(o), o.ConnectedAt.UnixMilli(), time.Now().UnixMilli())
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}
func (s *PresenceStore) Pending(ctx context.Context) ([]presence.Observation, error) {
	rows, next, err := s.Client.HScan(ctx, connectionPendingKey, s.cursor, "", presence.BatchSize).Result()
	if err != nil {
		return nil, err
	}
	s.cursor = next
	result := make([]presence.Observation, 0, len(rows)/2)
	ages := make([]redis.Z, 0, len(rows)/2)
	for i := 0; i < len(rows); i += 2 {
		var v connectionSnapshot
		if err := json.Unmarshal([]byte(rows[i+1]), &v); err != nil {
			return nil, fmt.Errorf("presence: invalid pending snapshot: %w", err)
		}
		if v.ClientID != rows[i] || v.IP == "" || v.AtMS <= 0 {
			return nil, fmt.Errorf("presence: invalid pending snapshot fields")
		}
		// Backfill the age index for snapshots created by the previous version.
		ages = append(ages, redis.Z{Score: float64(v.AtMS), Member: v.ClientID})
		result = append(result, presence.Observation{ClientID: v.ClientID, IP: v.IP, ConnectedAt: time.UnixMilli(v.AtMS).UTC()})
		if len(result) == presence.BatchSize {
			s.cursor = 0
			break
		}
	}
	if len(ages) > 0 {
		if err := s.Client.ZAddNX(ctx, connectionPendingSinceKey, ages...).Err(); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (s *PresenceStore) Ack(ctx context.Context, batch []presence.Observation) error {
	pipe := s.Client.Pipeline()
	for _, o := range batch {
		pipe.Eval(ctx, `if redis.call('HGET',KEYS[1],ARGV[1]) == ARGV[2] then redis.call('ZREM',KEYS[2],ARGV[1]);return redis.call('HDEL',KEYS[1],ARGV[1]) end;return 0`, []string{connectionPendingKey, connectionPendingSinceKey}, o.ClientID, connectionJSON(o))
	}
	_, err := pipe.Exec(ctx)
	return err
}
func (s *PresenceStore) MarkGap(ctx context.Context) error {
	return s.Client.Set(ctx, connectionGapKey, "1", 0).Err()
}
func (s *PresenceStore) HasGap(ctx context.Context) (bool, error) {
	n, err := s.Client.Exists(ctx, connectionGapKey).Result()
	return n != 0, err
}

func (s *PresenceStore) Backlog(ctx context.Context) (presence.ConnectionBacklog, error) {
	// One atomic snapshot keeps count and oldest age consistent with enqueue/ack.
	rows, err := s.Client.Eval(ctx, `local n=redis.call('HLEN',KEYS[1]);local oldest=redis.call('ZRANGE',KEYS[2],0,0,'WITHSCORES');return {n,oldest[2] or '0'}`, []string{connectionPendingKey, connectionPendingSinceKey}).Slice()
	if err != nil {
		return presence.ConnectionBacklog{}, err
	}
	count, ok := rows[0].(int64)
	if !ok {
		return presence.ConnectionBacklog{}, fmt.Errorf("presence: invalid backlog count")
	}
	raw, ok := rows[1].(string)
	if !ok {
		return presence.ConnectionBacklog{}, fmt.Errorf("presence: invalid backlog age")
	}
	oldest, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return presence.ConnectionBacklog{}, err
	}
	result := presence.ConnectionBacklog{Pending: count}
	if count > 0 && oldest > 0 {
		result.OldestAt = time.UnixMilli(oldest)
	}
	return result, nil
}
