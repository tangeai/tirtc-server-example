package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"thing-connect/internal/call"
	"thing-connect/internal/queuehealth"
)

// An index entry without a message list (evicted or manually deleted key) is
// dropped instead of failing, otherwise the smallest due score would block
// every later room forever.
const callNotificationIndex = `
local function discard_orphan(room)
 redis.call('ZREM','call:outbox:due',room)
 redis.call('ZREM','call:outbox:age',room)
 redis.call('HDEL','call:outbox:leases',room)
 redis.call('HDEL','call:outbox:attempts',room)
 redis.call('INCR','call:outbox:recount')
end
`

var claimCallNotification = redis.NewScript(callNotificationIndex + `
local t=redis.call('TIME');local now=tonumber(t[1])*1000+math.floor(tonumber(t[2])/1000)
local scoped=ARGV[3]~=''
local room,raw
for _=1,16 do
 if scoped then
  local due=redis.call('ZSCORE','call:outbox:due',ARGV[3])
  if not due or tonumber(due)>now then return {} end
  room=ARGV[3]
 else
  local rows=redis.call('ZRANGEBYSCORE','call:outbox:due','-inf',now,'LIMIT',0,1)
  if #rows==0 then return {} end
  room=rows[1]
 end
 raw=redis.call('LINDEX','call:outbox:room:'..room,0)
 if raw then break end
 discard_orphan(room)
 if scoped then return {} end
end
if not raw then return {'more'} end
redis.call('HSET','call:outbox:leases',room,ARGV[1])
redis.call('ZADD','call:outbox:due',now+tonumber(ARGV[2]),room)
return {raw,redis.call('HGET','call:outbox:attempts',room) or '0'}
`)
var finishCallNotification = redis.NewScript(`
local room=ARGV[1]
if redis.call('HGET','call:outbox:leases',room)~=ARGV[3] then return 0 end
local key='call:outbox:room:'..room
local raw=redis.call('LINDEX',key,0)
if not raw or cjson.decode(raw).id~=ARGV[2] then return 0 end
local t=redis.call('TIME');local now=tonumber(t[1])*1000+math.floor(tonumber(t[2])/1000)
redis.call('HDEL','call:outbox:leases',room)
if ARGV[4]=='retry' then
 local attempts=tonumber(redis.call('HGET','call:outbox:attempts',room) or '0')
 redis.call('HSET','call:outbox:attempts',room,math.min(9,attempts+1))
 redis.call('ZADD','call:outbox:due',now+tonumber(ARGV[5]),room)
 return 1
end
redis.call('LPOP',key);redis.call('DECR','call:outbox:count')
redis.call('HDEL','call:outbox:attempts',room)
if ARGV[4]=='expire' then
 redis.call('ZADD','call:outbox:expired',now,ARGV[2])
 redis.call('ZREMRANGEBYSCORE','call:outbox:expired','-inf',now-3600000)
 redis.call('ZREMRANGEBYRANK','call:outbox:expired',0,-10001)
end
local next=redis.call('LINDEX',key,0)
if next then
 redis.call('ZADD','call:outbox:due',now,room)
 redis.call('ZADD','call:outbox:age',cjson.decode(next).created_at,room)
else
 redis.call('DEL',key)
 redis.call('ZREM','call:outbox:due',room)
 redis.call('ZREM','call:outbox:age',room)
 if redis.call('ZCARD','call:outbox:due')==0 then
  redis.call('SET','call:outbox:count',0)
  redis.call('DEL','call:outbox:recount')
 end
end
return 1
`)

// A missing list has unknown length, including lists created by older
// instances, so the count is rebuilt from the surviving queues. Rebuilding in
// the sampling script cannot be invalidated by concurrent appends or
// acknowledgements; the room bound keeps the script short.
var callNotificationBacklog = redis.NewScript(`
local t=redis.call('TIME');local now=tonumber(t[1])*1000+math.floor(tonumber(t[2])/1000)
local oldest=redis.call('ZRANGE','call:outbox:age',0,0,'WITHSCORES')
local age=0;if #oldest>0 then age=math.max(0,now-tonumber(oldest[2])) end
local pending=tonumber(redis.call('GET','call:outbox:count') or '0')
local repairing=redis.call('EXISTS','call:outbox:recount')
if repairing==1 and redis.call('ZCARD','call:outbox:due')<=tonumber(ARGV[1]) then
 pending=0
 for _,room in ipairs(redis.call('ZRANGE','call:outbox:due',0,-1)) do
  pending=pending+redis.call('LLEN','call:outbox:room:'..room)
 end
 redis.call('SET','call:outbox:count',pending)
 redis.call('DEL','call:outbox:recount')
 repairing=0
end
redis.call('ZREMRANGEBYSCORE','call:outbox:expired','-inf',now-3600000)
return {pending,age,repairing,redis.call('ZCARD','call:outbox:expired')}
`)

const callNotificationRecountRooms = 10000

func (s *CallRooms) ClaimNotification(ctx context.Context, lease time.Duration) (call.Notification, bool, error) {
	return s.claimNotification(ctx, lease, "")
}
func (s *CallRooms) ClaimRoomNotification(ctx context.Context, lease time.Duration, roomID string) (call.Notification, bool, error) {
	if roomID == "" {
		return call.Notification{}, false, fmt.Errorf("invalid call notification room")
	}
	return s.claimNotification(ctx, lease, roomID)
}
func (s *CallRooms) claimNotification(ctx context.Context, lease time.Duration, roomID string) (call.Notification, bool, error) {
	var event call.Notification
	if lease.Milliseconds() <= 0 {
		return event, false, fmt.Errorf("invalid call notification lease")
	}
	token := uuid.NewString()
	var rows []interface{}
	var err error
	for {
		rows, err = claimCallNotification.Run(ctx, s.client, nil, token, lease.Milliseconds(), roomID).Slice()
		if err != nil {
			return event, false, err
		}
		if len(rows) == 0 {
			return event, false, nil
		}
		// Each pass discards a bounded batch of orphan index entries.
		if len(rows) != 1 {
			break
		}
	}
	raw, ok := rows[0].(string)
	if !ok {
		return event, false, fmt.Errorf("invalid call notification")
	}
	if err = json.Unmarshal([]byte(raw), &event); err != nil {
		return event, false, err
	}
	event.Lease = token
	event.Attempts, err = strconv.Atoi(fmt.Sprint(rows[1]))
	return event, err == nil, err
}
func (s *CallRooms) CompleteNotification(ctx context.Context, event call.Notification) error {
	return finishCallNotification.Run(ctx, s.client, nil, event.RoomID, event.ID, event.Lease, "complete", 0).Err()
}
func (s *CallRooms) ExpireNotification(ctx context.Context, event call.Notification) error {
	return finishCallNotification.Run(ctx, s.client, nil, event.RoomID, event.ID, event.Lease, "expire", 0).Err()
}
func (s *CallRooms) RetryNotification(ctx context.Context, event call.Notification, delay time.Duration) error {
	return finishCallNotification.Run(ctx, s.client, nil, event.RoomID, event.ID, event.Lease, "retry", max(0, delay.Milliseconds())).Err()
}
func (s *CallRooms) Backlog(ctx context.Context) (queuehealth.Backlog, error) {
	var result queuehealth.Backlog
	values, err := callNotificationBacklog.Run(ctx, s.client, nil, callNotificationRecountRooms).Int64Slice()
	if err != nil {
		return result, err
	}
	if len(values) != 4 {
		return result, fmt.Errorf("invalid call notification backlog")
	}
	if values[2] != 0 {
		return result, fmt.Errorf("call notification backlog exceeds %d rooms; count cannot be rebuilt", callNotificationRecountRooms)
	}
	result.Pending = values[0]
	result.OldestAge = time.Duration(values[1]) * time.Millisecond
	result.Exhausted = values[3]
	return result, nil
}
