package tirtcapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoomTokenUsesSignedDeviceIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/token/room" || r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Authorization"), TGV1Alg) {
			t.Error("unsigned room request")
		}
		var body map[string]string
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		if body["device_id"] != "device-1" || body["room_id"] != "room-1" {
			t.Error(body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"peer_id":"opaque-peer","token":"opaque-token"}`))
	}))
	defer server.Close()
	client := NewRoomClient(AgentAPIConfig{BaseURL: server.URL, AppID: "app", AccessKeyID: "access", SecretKeyID: "secret"}, server.Client())
	credential, e := client.Issue(context.Background(), "room-1", "device-1")
	if e != nil || credential.PeerID != "opaque-peer" || credential.Token != "opaque-token" {
		t.Fatalf("%+v %v", credential, e)
	}
}
