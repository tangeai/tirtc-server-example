package admincache

import (
	"context"
	"github.com/redis/go-redis/v9"
	"thing-connect/internal/admin"
	"time"
)

type AdminPresenceReader struct{ client *redis.Client }

func NewAdminPresenceReader(client *redis.Client) *AdminPresenceReader {
	return &AdminPresenceReader{client: client}
}
func (r *AdminPresenceReader) Read(ctx context.Context, ids []string) (map[string]admin.DevicePresence, error) {
	result := make(map[string]admin.DevicePresence, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = "online:sn_" + id
	}
	values, err := r.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, value := range values {
		if value == nil {
			continue
		}
		v := admin.DevicePresence{Online: true}
		if raw, ok := value.(string); ok {
			if seen, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				v.LastSeenAt = &seen
			}
		}
		result[ids[i]] = v
	}
	return result, nil
}
