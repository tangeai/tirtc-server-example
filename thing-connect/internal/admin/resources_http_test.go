package admin

import "testing"

func TestDevicePresenceKeyUsesFormalMQTTClientID(t *testing.T) {
	const deviceID = "TIRE88CRGXG4"
	if got, want := devicePresenceKey(deviceID), "online:sn_"+deviceID; got != want {
		t.Fatalf("devicePresenceKey(%q) = %q, want %q", deviceID, got, want)
	}
}
