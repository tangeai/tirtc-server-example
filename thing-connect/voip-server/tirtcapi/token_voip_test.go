package tirtcapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── PostTokenService 参数校验 ────────────────────────────────────────────────

func TestPostTokenService_EmptyBaseURL(t *testing.T) {
	_, _, err := PostTokenService(context.Background(), nil, "", "aid", "app", "sec", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("want base_url error, got %v", err)
	}
}

func TestPostTokenService_EmptyAccessID(t *testing.T) {
	_, _, err := PostTokenService(context.Background(), nil, "https://example.com", "", "app", "sec", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "access_id") {
		t.Errorf("want access_id error, got %v", err)
	}
}

func TestPostTokenService_EmptyAppID(t *testing.T) {
	_, _, err := PostTokenService(context.Background(), nil, "https://example.com", "aid", "", "sec", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "app_id") {
		t.Errorf("want app_id error, got %v", err)
	}
}

func TestPostTokenService_EmptySecretKey(t *testing.T) {
	_, _, err := PostTokenService(context.Background(), nil, "https://example.com", "aid", "app", "", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "secret_key") {
		t.Errorf("want secret_key error, got %v", err)
	}
}

func TestPostTokenService_ServerErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	_, _, err := PostTokenService(context.Background(), srv.Client(), srv.URL, "aid", "app", "sec", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("want HTTP 500 error, got %v", err)
	}
}

func TestPostTokenService_BusinessErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := tokenServiceResp{Code: 1001, Msg: "invalid token"}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	_, _, err := PostTokenService(context.Background(), srv.Client(), srv.URL, "aid", "app", "sec", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "1001") {
		t.Errorf("want code=1001 error, got %v", err)
	}
}

func TestPostTokenService_Success(t *testing.T) {
	data, _ := json.Marshal(tokenServiceData{PeerID: "peer1", Token: "tok1"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := tokenServiceResp{Code: 0, Data: data}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	peerID, token, err := PostTokenService(context.Background(), srv.Client(), srv.URL, "aid", "app", "sec", TokenWxvoipRequest{})
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if peerID != "peer1" || token != "tok1" {
		t.Errorf("want peer1/tok1, got %s/%s", peerID, token)
	}
}

func TestTokenWxvoipRequest_MergesProfile(t *testing.T) {
	req := TokenWxvoipRequest{
		Profile: json.RawMessage(`{
			"video_res_mode":"fit_screen",
			"future_media_option":{"enabled":true},
			"camera_rotation":90,
			"aspect_ratio":1.3333333333,
			"hor_mirror":true,
			"vert_mirror":false,
			"object_fit":"contain",
			"device_id":"profile-device",
			"wx_payload":"profile-payload"
		}`),
		WxSessionKey:   "session-key",
		WxRoomID:       "room-id",
		WxSessionToken: "session-token",
		WxAppID:        "wx-app-id",
		DeviceID:       "server-device",
		WxPayload:      "server-payload",
		WxModelID:      "model-id",
	}

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	for key, want := range map[string]any{
		"video_res_mode": "fit_screen",
		"device_id":      "server-device",
		"wx_payload":     "server-payload",
		"wx_session_key": "session-key",
		"wx_room_id":     "room-id",
		"wx_model_id":    "model-id",
	} {
		if got[key] != want {
			t.Errorf("%s=%v, want %v", key, got[key], want)
		}
	}
	if option, ok := got["future_media_option"].(map[string]any); !ok || option["enabled"] != true {
		t.Errorf("future profile field not preserved: %v", got["future_media_option"])
	}
	for _, key := range localProfileFields {
		if _, ok := got[key]; ok {
			t.Errorf("local UI field %s must not be forwarded", key)
		}
	}
}

func TestTokenWxvoipRequest_RejectsNonObjectProfile(t *testing.T) {
	for _, profile := range []string{"null", `[]`, `"profile"`} {
		t.Run(profile, func(t *testing.T) {
			_, err := json.Marshal(TokenWxvoipRequest{Profile: json.RawMessage(profile)})
			if err == nil || !strings.Contains(err.Error(), "profile must be a JSON object") {
				t.Fatalf("want object error, got %v", err)
			}
		})
	}
}

func TestTokenWxvoipRequest_NormalizesLegacyVideoFields(t *testing.T) {
	tests := []struct {
		name        string
		profile     string
		wantVideoMT bool
		wantUp      bool
		wantDown    bool
	}{
		{
			name:        "legacy only",
			profile:     `{"video_mt":"h264"}`,
			wantVideoMT: true,
		},
		{
			name:        "empty directional fields preserve legacy",
			profile:     `{"video_mt":"h264","up_video_mt":"","down_video_mt":null}`,
			wantVideoMT: true,
		},
		{
			name:     "explicit directional fields override legacy",
			profile:  `{"video_mt":"h264","up_video_mt":"h265","down_video_mt":"mjpeg"}`,
			wantUp:   true,
			wantDown: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(TokenWxvoipRequest{Profile: json.RawMessage(tt.profile)})
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if _, ok := got["video_mt"]; ok != tt.wantVideoMT {
				t.Errorf("video_mt present=%v, want %v; body=%s", ok, tt.wantVideoMT, body)
			}
			if _, ok := got["up_video_mt"]; ok != tt.wantUp {
				t.Errorf("up_video_mt present=%v, want %v; body=%s", ok, tt.wantUp, body)
			}
			if _, ok := got["down_video_mt"]; ok != tt.wantDown {
				t.Errorf("down_video_mt present=%v, want %v; body=%s", ok, tt.wantDown, body)
			}
		})
	}
}
