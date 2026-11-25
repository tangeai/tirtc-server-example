package redisview

import (
	"encoding/json"
	"testing"
	"time"

	"thing-connect/internal/presence"
	"thing-connect/internal/servicestatus"
)

func TestMissingProjectionRequiresConsumerCoverage(t *testing.T) {
	now := time.Now().UTC()
	sample := func(start, last time.Time, reconciled int64, mqtt string) string {
		data, err := json.Marshal(servicestatus.Instance{Service: "device-server", StartedAt: start, LastHeartbeat: last,
			Dependencies: map[string]string{"mqtt": mqtt, "presence": "healthy", "redis": "healthy"},
			Metrics:      map[string]int64{"presence_reconcile_last_ms": reconciled}})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	established := sample(now.Add(-presence.TTL), now, 0, "healthy")
	fresh := sample(now.Add(-time.Second), now, 0, "healthy")
	for _, tc := range []struct {
		name    string
		samples []any
		want    bool
	}{
		{"no consumer", nil, false},
		{"expired index member", []any{nil}, false},
		{"corrupt sample", []any{"{}"}, false},
		{"cold restart", []any{fresh}, false},
		{"stale sample", []any{sample(now.Add(-time.Hour), now.Add(-time.Minute), 0, "healthy")}, false},
		{"disconnected consumer", []any{sample(now.Add(-time.Hour), now, 0, "unhealthy")}, false},
		{"rolling restart", []any{established, fresh, nil}, true},
		{"broker reconciliation", []any{sample(now.Add(-time.Second), now, now.UnixMilli(), "healthy")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := coversMissingDevices(tc.samples, now); got != tc.want {
				t.Fatalf("coverage=%v want %v", got, tc.want)
			}
		})
	}
}
