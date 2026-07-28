package tirtcapi

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildDeviceToken_Format(t *testing.T) {
	tok, err := BuildDeviceToken("access", "secret", "device-secret", "TIRZ00000001")
	if err != nil {
		t.Fatalf("BuildDeviceToken: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		t.Fatalf("expected v1.<payload>.<sig>, got %q", tok)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["scope"] != "connect:device://TIRZ00000001" {
		t.Errorf("scope = %v, want connect:device://TIRZ00000001", payload["scope"])
	}
}

func TestBuildDeviceToken_DifferentDeviceSecretDifferentSig(t *testing.T) {
	tok1, err := BuildDeviceToken("access", "secret", "device-secret-1", "TIRZ00000001")
	if err != nil {
		t.Fatalf("BuildDeviceToken: %v", err)
	}
	tok2, err := BuildDeviceToken("access", "secret", "device-secret-2", "TIRZ00000001")
	if err != nil {
		t.Fatalf("BuildDeviceToken: %v", err)
	}
	if tok1 == tok2 {
		t.Error("different device secrets must produce different tokens")
	}
}

func TestBuildDeviceToken_Deterministic_ExceptNonce(t *testing.T) {
	tok1, _ := BuildDeviceToken("access", "secret", "device-secret", "TIRZ00000001")
	tok2, _ := BuildDeviceToken("access", "secret", "device-secret", "TIRZ00000001")
	if tok1 == tok2 {
		t.Error("tokens should differ due to random nonce/timestamp")
	}
}
