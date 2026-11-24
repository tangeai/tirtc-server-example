package cache

import (
	"context"

	"github.com/redis/go-redis/v9"

	"thing-connect/internal/voip"
)

type voipCallGuards struct{ client *redis.Client }

func NewVoipCallGuards(client *redis.Client) voip.CallGuards { return &voipCallGuards{client: client} }

func (g *voipCallGuards) ReleaseDevice(ctx context.Context, deviceID string, revision int64, contacts []voip.Contact) error {
	keys := []string{"voip:device-call:" + deviceID}
	for _, c := range contacts {
		keys = append(keys, "voip:contact-call:"+c.AppID+":"+c.OpenID)
	}
	// Snapshot and compare atomically; never delete another device's contact guard.
	return g.client.Eval(ctx, unbindRevisionCheck+`
local marker=ARGV[1]
if already_cleaned(marker,ARGV[2]) then return 0 end
local call = redis.call('GET', KEYS[1])
if call then
 for _, key in ipairs(KEYS) do
  if redis.call('GET', key) == call then redis.call('DEL', key) end
 end
end
redis.call('SET',marker,ARGV[2])
return 1`, keys, "voip:unbound-revision:"+deviceID, revision).Err()
}
