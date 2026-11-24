package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"thing-connect/internal/presence"
)

const connectionPendingKey = "presence:connections:pending"
const connectionPendingSinceKey = "presence:connections:pending_since"

const connectionGapKey = "presence:connections:gap"

// connectionGapClearedKey holds the start (unix ms) of the latest completed
// broker listing that cleared the gap marker.
const connectionGapClearedKey = "presence:connections:gap_cleared"

// PresenceStore uses a dedicated, deadline-aware Redis client. Online values
// retain the public key/format/TTL contract. Pending IP snapshots have no TTL.
// Multi-key scripts require the existing single-endpoint Redis deployment.
type PresenceStore struct {
	onlineUpdates, onlineIgnored, connectionUpdates, connectionIgnored atomic.Int64
	Client                                                             *redis.Client
	cursor                                                             uint64 // owned exclusively by the connection persistence worker
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

// writePresence only moves an online expiry forward, so retries, reordering
// and concurrent Device instances cannot shorten a newer observation. PTTL
// keeps the comparison compatible with Redis 6.
var writePresence = redis.NewScript(`
local exp=tonumber(ARGV[2])
local now=tonumber(ARGV[6])
local result=2
if exp>now then
 local ttl=redis.call('PTTL',KEYS[1])
 if ttl<0 or now+ttl<exp then
  redis.call('SET',KEYS[1],ARGV[1])
  redis.call('PEXPIREAT',KEYS[1],exp)
  result=1
 end
end
if ARGV[3]~='' then
 local old=redis.call('HGET',KEYS[2],ARGV[3])
 if not old or cjson.decode(old).at_ms<tonumber(ARGV[5]) then
  redis.call('HSET',KEYS[2],ARGV[3],ARGV[4])
  redis.call('ZADD',KEYS[3],'NX',ARGV[6],ARGV[3])
  result=result+4
 else
  result=result+8
 end
end
return result`)

// Metrics counts only acknowledged script results, not writes whose reply was lost.
// Ignored observations include equal, older and expired observations.
func (s *PresenceStore) Metrics() map[string]int64 {
	return map[string]int64{"presence_online_updates_total": s.onlineUpdates.Load(), "presence_online_ignored_total": s.onlineIgnored.Load(), "presence_connection_updates_total": s.connectionUpdates.Load(), "presence_connection_ignored_total": s.connectionIgnored.Load()}
}

func (s *PresenceStore) Project(ctx context.Context, batch []presence.Observation) error {
	if len(batch) == 0 {
		return s.Client.Ping(ctx).Err()
	}
	err := s.project(ctx, batch)
	if err != nil && redis.HasErrorPrefix(err, "NOSCRIPT") {
		// A restarted or failed-over Redis loses its script cache. Replaying
		// the batch is safe because every write is monotonic.
		if err := writePresence.Load(ctx, s.Client).Err(); err != nil {
			return err
		}
		err = s.project(ctx, batch)
	}
	return err
}

func (s *PresenceStore) project(ctx context.Context, batch []presence.Observation) error {
	now := time.Now().UnixMilli()
	pipe := s.Client.Pipeline()
	commands := make([]*redis.Cmd, 0, len(batch))
	for _, o := range batch {
		// Unversioned SYS disconnects cannot safely delete a current connection.
		// Heartbeat expiration is authoritative; late disconnects are harmless.
		if o.Offline {
			continue
		}
		pendingID, snapshot := "", ""
		if o.IP != "" {
			pendingID, snapshot = o.ClientID, connectionJSON(o)
		}
		commands = append(commands, writePresence.EvalSha(ctx, pipe, []string{"online:" + o.ClientID, connectionPendingKey, connectionPendingSinceKey}, o.At.UTC().Format(time.RFC3339Nano), o.ExpiresAt.UnixMilli(), pendingID, snapshot, o.ConnectedAt.UnixMilli(), now))
	}
	if pipe.Len() == 0 {
		return nil
	}
	_, err := pipe.Exec(ctx)
	for _, command := range commands {
		if result, readErr := command.Int64(); readErr == nil {
			if result&1 != 0 {
				s.onlineUpdates.Add(1)
			}
			if result&2 != 0 {
				s.onlineIgnored.Add(1)
			}
			if result&4 != 0 {
				s.connectionUpdates.Add(1)
			}
			if result&8 != 0 {
				s.connectionIgnored.Add(1)
			}
		}
	}
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

// MarkGap records the latest loss time. Legacy markers hold "1", which
// ClearGap treats as older than any reconciliation. A loss older than the
// start of the latest completed listing (from any instance) is already
// resolved, so a late write cannot resurrect a cleared marker.
func (s *PresenceStore) MarkGap(ctx context.Context, lostAt time.Time) error {
	return s.Client.Eval(ctx, `
local lost = tonumber(ARGV[1])
local covered = tonumber(redis.call('GET', KEYS[2])) or 0
if lost < covered then return 0 end
local previous = tonumber(redis.call('GET', KEYS[1])) or 0
if previous < lost then redis.call('SET', KEYS[1], ARGV[1]) end
return 1`, []string{connectionGapKey, connectionGapClearedKey}, lostAt.UnixMilli()).Err()
}

// ClearGap removes the marker only when the loss happened before a completed
// broker listing started; a loss during reconciliation keeps the marker.
func (s *PresenceStore) ClearGap(ctx context.Context, listedFrom time.Time) error {
	return s.Client.Eval(ctx, `
local started = tonumber(ARGV[1])
local covered = tonumber(redis.call('GET', KEYS[2])) or 0
if covered < started then redis.call('SET', KEYS[2], ARGV[1]) end
local gap = redis.call('GET', KEYS[1])
if gap and (tonumber(gap) or 0) < started then return redis.call('DEL', KEYS[1]) end
return 0`, []string{connectionGapKey, connectionGapClearedKey}, listedFrom.UnixMilli()).Err()
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
