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

var claimCallNotification = redis.NewScript(`
local t=redis.call('TIME');local now=tonumber(t[1])*1000+math.floor(tonumber(t[2])/1000)
local rows=redis.call('ZRANGEBYSCORE','call:outbox:due','-inf',now,'LIMIT',0,1)
if #rows==0 then return {} end
local room=rows[1]
local raw=redis.call('LINDEX','call:outbox:room:'..room,0)
if not raw then return redis.error_reply('call outbox index has no message') end
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
local next=redis.call('LINDEX',key,0)
if next then
 redis.call('ZADD','call:outbox:due',now,room)
 redis.call('ZADD','call:outbox:age',cjson.decode(next).created_at,room)
else
 redis.call('DEL',key)
 redis.call('ZREM','call:outbox:due',room)
 redis.call('ZREM','call:outbox:age',room)
end
return 1
`)
var callNotificationBacklog = redis.NewScript(`
local t=redis.call('TIME');local now=tonumber(t[1])*1000+math.floor(tonumber(t[2])/1000)
local oldest=redis.call('ZRANGE','call:outbox:age',0,0,'WITHSCORES')
local age=0;if #oldest>0 then age=math.max(0,now-tonumber(oldest[2])) end
return {tonumber(redis.call('GET','call:outbox:count') or '0'),age}
`)

func (s *CallRooms) ClaimNotification(ctx context.Context, lease time.Duration) (call.Notification, bool, error) {
	var event call.Notification
	if lease.Milliseconds() <= 0 {
		return event, false, fmt.Errorf("invalid call notification lease")
	}
	token := uuid.NewString()
	rows, err := claimCallNotification.Run(ctx, s.client, nil, token, lease.Milliseconds()).Slice()
	if err != nil {
		return event, false, err
	}
	if len(rows) == 0 {
		return event, false, nil
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
func (s *CallRooms) RetryNotification(ctx context.Context, event call.Notification, delay time.Duration) error {
	return finishCallNotification.Run(ctx, s.client, nil, event.RoomID, event.ID, event.Lease, "retry", max(0, delay.Milliseconds())).Err()
}
func (s *CallRooms) Backlog(ctx context.Context) (queuehealth.Backlog, error) {
	var result queuehealth.Backlog
	values, err := callNotificationBacklog.Run(ctx, s.client, nil).Int64Slice()
	if err != nil {
		return result, err
	}
	if len(values) != 2 {
		return result, fmt.Errorf("invalid call notification backlog")
	}
	result.Pending = values[0]
	result.OldestAge = time.Duration(values[1]) * time.Millisecond
	return result, nil
}
