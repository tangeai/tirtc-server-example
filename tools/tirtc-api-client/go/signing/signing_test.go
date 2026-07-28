package signing_test

import (
	"testing"
	"time"

	"github.com/tange-ai/tirtc-api-client/signing"
)

func TestSignRequest_PostWxVoip(t *testing.T) {
	// Same inputs as token-signing test-vectors.json vector #1,
	// but signed-headers only include content-length;content-type;x-tg-app-id;x-tg-date
	// (matching production demo behavior).
	got := signing.SignRequest(
		"test-access-key-123", "test-secret-456", "app-789",
		"POST", "/v1/token/wxvoip", "",
		[]byte(`{"device_id":"TESTDEVICE01","wx_session_key":"test-key","wx_room_id":"room-1","wx_session_token":"token-1","wx_app_id":"wxapp","wx_model_id":"model-1","audio_rate":8000,"audio_channels":1}`),
		mustParseTime("2024-01-15T12:00:00Z"),
	)

	check(t, got, "X-Tg-Algorithm", "TGV1-HMAC-SHA256")
	check(t, got, "X-Tg-App-Id", "app-789")
	check(t, got, "X-Tg-Date", "20240115T120000Z")
	check(t, got, "X-Tg-Content-Sha256", "b953c579ce8e6b9bd78395c0719967b395fdf150f56ab04e575b7f16a5164784")
	check(t, got, "Content-Length", "188")
	check(t, got, "Content-Type", "application/json")
	check(t, got, "X-Tg-Signed-Headers", "content-length;content-type;x-tg-app-id;x-tg-date")
	check(t, got, "Authorization",
		"TGV1-HMAC-SHA256 Credential=test-access-key-123/20240122/tgv1_request, SignedHeaders=content-length;content-type;x-tg-app-id;x-tg-date, Signature=e376a3aedba305d9a95b8edd719881efe5199db5a40ec2ceb12d33d95885b2ac")
}

func TestSignRequest_GetWithQuery(t *testing.T) {
	got := signing.SignRequest(
		"test-access-key-123", "test-secret-456", "app-789",
		"GET", "/v2/device/server/connection", "device_id=TESTDEVICE01&platform=web",
		nil,
		mustParseTime("2024-01-15T12:00:00Z"),
	)

	check(t, got, "X-Tg-Signed-Headers", "x-tg-app-id;x-tg-date")
	check(t, got, "Authorization",
		"TGV1-HMAC-SHA256 Credential=test-access-key-123/20240122/tgv1_request, SignedHeaders=x-tg-app-id;x-tg-date, Signature=7576511d6d03456fd172ea25fcbd670eb8bf1fb30047d12fd07dabb333b095f6")
}

func TestSignRequest_GetRootPath(t *testing.T) {
	got := signing.SignRequest(
		"test-access-key-123", "test-secret-456", "app-789",
		"GET", "/", "action=ping",
		nil,
		mustParseTime("2024-01-15T12:00:00Z"),
	)

	check(t, got, "X-Tg-Signed-Headers", "x-tg-app-id;x-tg-date")
	check(t, got, "Authorization",
		"TGV1-HMAC-SHA256 Credential=test-access-key-123/20240122/tgv1_request, SignedHeaders=x-tg-app-id;x-tg-date, Signature=6c45f7c62d8a694bfec75f03fc7fc97ae0a537525e39ee5eb88d4da0df8e88da")
}

func mustParseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func check(t *testing.T, h map[string][]string, key, want string) {
	t.Helper()
	got := ""
	if vs, ok := h[key]; ok && len(vs) > 0 {
		got = vs[0]
	}
	if got != want {
		t.Errorf("%s:\n  got  %q\n  want %q", key, got, want)
	}
}
