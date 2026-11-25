// Package redisview reads the Device-owned presence projection for user queries.
package redisview

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"thing-connect/internal/presence"
	"thing-connect/internal/servicestatus"
)

type DeviceList struct{ Client *redis.Client }

// DeviceOnline reads projection values and consumer heartbeats in one Redis
// snapshot. A missing projection is evidence of absence only while Device is
// consuming and has had time to rebuild the projection after a restart.
func (r DeviceList) DeviceOnline(ctx context.Context, ids []string) (map[string]bool, error) {
	online := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return online, nil
	}
	heartbeats, err := servicestatus.DeviceHeartbeatKeys(ctx, r.Client)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(ids)+len(heartbeats))
	for _, id := range ids {
		keys = append(keys, "online:sn_"+id)
	}
	values, err := r.Client.MGet(ctx, append(keys, heartbeats...)...).Result()
	if err != nil {
		return nil, err
	}
	missing := false
	for i, id := range ids {
		value, _ := values[i].(string)
		online[id] = value != ""
		missing = missing || value == ""
	}
	if missing && !coversMissingDevices(values[len(ids):], time.Now()) {
		return nil, errors.New("device presence coverage unavailable")
	}
	return online, nil
}

func coversMissingDevices(samples []any, now time.Time) bool {
	covered := false
	for _, sample := range samples {
		if sample == nil { // An expired index member is not a running instance.
			continue
		}
		raw, ok := sample.(string)
		var inst servicestatus.Instance
		if !ok || json.Unmarshal([]byte(raw), &inst) != nil || inst.Service != "device-server" || !inst.CurrentAt(now) {
			return false
		}
		if inst.Dependencies["mqtt"] != "healthy" || inst.Dependencies["presence"] != "healthy" || inst.Dependencies["redis"] != "healthy" {
			return false
		}
		if inst.StartedAt.IsZero() || inst.StartedAt.After(inst.LastHeartbeat) {
			return false
		}
		// One established consumer can cover a rolling restart. A freshly
		// started fleet needs a complete broker listing or one heartbeat TTL.
		reconciled := time.UnixMilli(inst.Metrics["presence_reconcile_last_ms"])
		listedSinceStart := !reconciled.Before(inst.StartedAt) && !reconciled.After(inst.LastHeartbeat)
		if listedSinceStart || inst.LastHeartbeat.Sub(inst.StartedAt) >= presence.TTL {
			covered = true
		}
	}
	return covered
}
