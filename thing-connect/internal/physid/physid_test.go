package physid_test

import (
	"testing"
	"thing-connect/internal/physid"
)

func TestHashAndParse(t *testing.T) {
	mac := "AA:BB:CC:DD:EE:FF"
	h := physid.Hash(mac)
	if len(h) != 16 {
		t.Errorf("Hash length: want 16, got %d", len(h))
	}
	pid := physid.Physical(mac)
	if got := physid.Parse(pid); got != mac {
		t.Errorf("Parse: got %q, want %q", got, mac)
	}
}

func TestHashEmpty(t *testing.T) {
	h := physid.Hash("AA:BB")
	if h == "" {
		t.Error("Hash: empty result")
	}
	if physid.Hash("AA:BB") != h {
		t.Error("Hash: not deterministic")
	}
}

func TestParseNotMacPrefix(t *testing.T) {
	if got := physid.Parse("garbage"); got != "" {
		t.Errorf("Parse non-mac_ prefix: want empty, got %q", got)
	}
}
