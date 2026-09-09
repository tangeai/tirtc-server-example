// Package signals adapts Redis admission control and MQTT assignment wakeups.
package signals

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/redis/go-redis/v9"
	"thing-connect/internal/room"
	"time"
)

type Broker interface {
	Publish(string, byte, any) error
	IsOnline(context.Context, string) bool
}
type Adapter struct {
	Redis  *redis.Client
	Broker Broker
}

var limitScript = redis.NewScript(`for i,key in ipairs(KEYS) do
 local count=redis.call('INCR',key)
 if count==1 then redis.call('EXPIRE',key,60) end
 if count>tonumber(ARGV[i]) then return 0 end
end
return 1`)

func (a *Adapter) Allow(ctx context.Context, o room.Operation) (bool, error) {
	keys := []string{}
	limits := []any{}
	add := func(value string, limit int) {
		keys = append(keys, fmt.Sprintf("intercom:limit:%x", sha256.Sum256([]byte(value))))
		limits = append(limits, limit)
	}
	add("device:"+o.DeviceID, 30)
	if o.UserID != 0 {
		add(fmt.Sprintf("user:%d", o.UserID), 120)
	}
	if o.IP != "" {
		add("ip:"+o.IP, 120)
	}
	if o.Code != "" {
		add("code:"+o.Code, 300)
	}
	n, e := limitScript.Run(ctx, a.Redis, keys, limits...).Int()
	return n == 1, e
}
func (a *Adapter) Online(ctx context.Context, device string) bool {
	return a.Broker.IsOnline(ctx, "sn_"+device)
}
func (a *Adapter) Notify(ctx context.Context, e room.Event, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.Broker.Publish("device/sn_"+e.DeviceID+"/cmd", 1, map[string]any{"type": "room_assignment_changed", "request_id": e.ID, "assignment_version": e.Version, "expires_at": time.Now().Add(ttl).Unix()})
}
