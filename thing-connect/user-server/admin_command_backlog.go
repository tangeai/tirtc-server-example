package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"thing-connect/internal/queuehealth"
)

// Redis 7 reports the number of unread entries as group lag. Capture the
// oldest pending and unread IDs in the same snapshot, including leased mail.
var adminCommandBacklog = redis.NewScript(`
local t=redis.call('TIME')
local now=t[1]*1000+math.floor(t[2]/1000)
local failed=redis.call('ZCOUNT',KEYS[2],'('..(now-tonumber(ARGV[2])),'+inf')
local pending=redis.call('XPENDING',KEYS[1],ARGV[1])
local groups=redis.call('XINFO','GROUPS',KEYS[1])
for _,fields in ipairs(groups) do
 local g={}
 for i=1,#fields,2 do g[fields[i]]=fields[i+1] end
 if g.name==ARGV[1] then
  local last=g['last-delivered-id']
  local entries=redis.call('XRANGE',KEYS[1],last,'+','COUNT',2)
  local first=entries[1]
  if first and first[1]==last then first=entries[2] end
  local oldest=pending[2]
  if not oldest and first then oldest=first[1] end
  local age=0
  if oldest then age=math.max(0,now-tonumber(string.match(oldest,'^%d+'))) end
  local lag=g.lag
  if not first then lag=0 end
  local upper=last
  if not lag then upper=redis.call('XREVRANGE',KEYS[1],'+','-','COUNT',1)[1][1] end
  return cjson.encode({pending=pending[1], lag=lag or -1, exhausted=failed, age_ms=age, last=last, upper=upper})
 end
end
return redis.error_reply('missing command group')`)

func (c *adminUserCommands) Backlog(ctx context.Context) (queuehealth.Backlog, error) {
	var sample struct {
		Pending, Lag, Exhausted int64
		AgeMS                   int64 `json:"age_ms"`
		Last, Upper             string
	}
	data, err := adminCommandBacklog.Run(ctx, c.client, []string{c.stream, c.stream + ":exhausted"}, c.group, adminCommandFailureWindow.Milliseconds()).Text()
	if err != nil {
		return queuehealth.Backlog{}, err
	}
	if err := json.Unmarshal([]byte(data), &sample); err != nil {
		return queuehealth.Backlog{}, err
	}
	if sample.Lag < 0 {
		// Legacy streams can have an unknown lag after trimming. Count unread
		// entries in small batches without blocking Redis for the full scan.
		sample.Lag, err = c.countUnread(ctx, sample.Last, sample.Upper)
		if err != nil {
			return queuehealth.Backlog{}, err
		}
	}
	return queuehealth.Backlog{Pending: sample.Pending + sample.Lag, Exhausted: sample.Exhausted, OldestAge: time.Duration(sample.AgeMS) * time.Millisecond}, nil
}
func (c *adminUserCommands) countUnread(ctx context.Context, cursor, upper string) (int64, error) {
	var count int64
	for {
		messages, err := c.client.XRangeN(ctx, c.stream, cursor, upper, 128).Result()
		if err != nil {
			return 0, err
		}
		for _, m := range messages {
			if m.ID != cursor {
				count++
			}
		}
		if len(messages) < 128 {
			return count, nil
		}
		cursor = messages[len(messages)-1].ID
	}
}
