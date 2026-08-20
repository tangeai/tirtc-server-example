package mqttc

import (
	"testing"
	"time"
)

func TestPresenceValueUsesUTCHeartbeatTimestamp(t *testing.T) {
	want := time.Date(2026, 8, 20, 12, 30, 45, 123, time.FixedZone("UTC+8", 8*60*60))
	got, err := time.Parse(time.RFC3339Nano, presenceValue(want))
	if err != nil {
		t.Fatalf("presenceValue is not RFC3339: %v", err)
	}
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("presenceValue() = %v, want same instant in UTC", got)
	}
}
