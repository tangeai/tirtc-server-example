package handler

import (
	"testing"

	"thing-connect/internal/physid"
)

func TestParsePhysicalID(t *testing.T) {
	tests := []struct {
		name       string
		physicalID string
		wantMAC    string
	}{
		{
			name:       "normal MAC",
			physicalID: "mac_AA:BB:CC:DD:EE:FF",
			wantMAC:    "AA:BB:CC:DD:EE:FF",
		},
		{
			name:       "no mac_ prefix",
			physicalID: "AA:BB:CC:DD:EE:FF",
			wantMAC:    "",
		},
		{
			name:       "empty string",
			physicalID: "",
			wantMAC:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMAC := physid.Parse(tt.physicalID)
			if gotMAC != tt.wantMAC {
				t.Errorf("physid.Parse(%q)=%q, want %q", tt.physicalID, gotMAC, tt.wantMAC)
			}
		})
	}
}
