package wechat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// ─── checkSignature ───────────────────────────────────────────────────────────

func TestCheckSignature_Valid(t *testing.T) {
	// 微信文档示例：token=testtoken, timestamp=1614900000, nonce=123456
	// 排序后拼接: 123456 + 1614900000 + testtoken → sha1
	// 预先通过 Python 计算得到的值
	// sorted: "123456", "1614900000", "testtoken" → concat → sha1
	got := checkSignature("d4165bffb097ee301b4364296e6a865bd9562417", "testtoken", "1614900000", "123456")
	if !got {
		t.Error("want true, got false")
	}
}

func TestCheckSignature_InvalidToken(t *testing.T) {
	if checkSignature("anysig", "", "ts", "nonce") {
		t.Error("empty token should return false")
	}
}

func TestCheckSignature_WrongSig(t *testing.T) {
	if checkSignature("badhash", "token", "ts", "nonce") {
		t.Error("wrong signature should return false")
	}
}

func TestCheckSignature_CaseInsensitive(t *testing.T) {
	// sha1 hex 大写也应通过
	got := checkSignature("D4165BFFB097EE301B4364296E6A865BD9562417", "testtoken", "1614900000", "123456")
	if !got {
		t.Error("uppercase hex should still match")
	}
}

// ─── parsePayload ─────────────────────────────────────────────────────────────

func TestParsePayload_Empty(t *testing.T) {
	m := &voipMessage{}
	if err := parsePayload(m); err != nil {
		t.Errorf("empty payload should be no-op, got %v", err)
	}
	if m.PayloadData != nil {
		t.Error("PayloadData should be nil for empty payload")
	}
}

func TestParsePayload_PlainJSON(t *testing.T) {
	m := &voipMessage{
		Payload: `{"id":"call123","from":"device456","to":"openid123","room_type":"video"}`,
	}
	if err := parsePayload(m); err != nil {
		t.Fatalf("plain JSON should parse, got %v", err)
	}
	if m.PayloadData == nil {
		t.Fatal("PayloadData should not be nil")
	}
	if m.PayloadData.ID != "call123" ||
		m.PayloadData.From != "device456" ||
		m.PayloadData.To != "openid123" ||
		m.PayloadData.RoomType != "video" {
		t.Errorf("unexpected payload: %+v", m.PayloadData)
	}
}

func TestParsePayload_Base64JSON(t *testing.T) {
	raw := `{"id":"call789","from":"devXYZ"}`
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	m := &voipMessage{Payload: encoded}
	if err := parsePayload(m); err != nil {
		t.Fatalf("base64 JSON should parse, got %v", err)
	}
	if m.PayloadData.ID != "call789" {
		t.Errorf("want id=call789, got %s", m.PayloadData.ID)
	}
}

func TestParsePayload_InvalidJSON(t *testing.T) {
	m := &voipMessage{Payload: "notjson"}
	if err := parsePayload(m); err != nil {
		t.Fatalf("developer-defined payload should be accepted, got %v", err)
	}
	if m.PayloadData != nil {
		t.Fatalf("invalid correlation JSON should leave PayloadData nil, got %+v", m.PayloadData)
	}
}

// ─── wxDecryptMsg ─────────────────────────────────────────────────────────────

func TestWxDecryptMsg_BadKeyLength(t *testing.T) {
	_, _, err := wxDecryptMsg("appid", "cipher", "tooshort")
	if err == nil {
		t.Error("want error for short encodingAESKey")
	}
}

func TestWxDecryptMsg_InvalidBase64Cipher(t *testing.T) {
	// valid 43-char key
	key := "abcdefghijklmnopqrstuvwxyz1234567890ABCDEFG"
	_, _, err := wxDecryptMsg("appid", "not-valid-base64!!!", key)
	if err == nil {
		t.Error("want error for invalid base64 cipher")
	}
}

// --- stubs for pushJoinToDevice test ---

type stubProfiler struct {
	profile string
	remark  string
}

type offlineProfiler struct {
	stubProfiler
}

func (offlineProfiler) IsDeviceOnline(_ context.Context, _ string) bool {
	return false
}

func (s stubProfiler) GetDeviceProfile(_ context.Context, _ string) (string, error) {
	return s.profile, nil
}

func (s stubProfiler) GetDeviceVoipContactRemark(
	_ context.Context, _, _, _ string,
) (string, error) {
	return s.remark, nil
}

type stubPublisher struct {
	called  bool
	payload any
}

func (s *stubPublisher) Publish(_ string, _ byte, payload any) error {
	s.called = true
	s.payload = payload
	return nil
}

func TestPushJoinToDevice_RejectsKnownOfflineDevice(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	var pub stubPublisher
	prof := offlineProfiler{stubProfiler{profile: `{}`}}

	err := pushJoinToDevice(
		c,
		WxAppCfg{ModelID: "model"},
		TirtcServerCfg{
			BaseURL:   "https://tirtc.invalid",
			AccessID:  "id",
			AppID:     "app",
			SecretKey: "secret",
		},
		"wxapp",
		"offline-device",
		&voipMessage{},
		&pub,
		&prof,
	)
	if err == nil || err.Error() != "device offline-device is offline" {
		t.Fatalf("unexpected offline result: %v", err)
	}
	if pub.called {
		t.Fatal("offline call should not publish MQTT")
	}
}

// TestPushJoinToDevice_ForwardsMediaFormats verifies the new up/down video and
// down audio format fields from the device profile are forwarded to the
// tirtc-server-api /v1/token/wxvoip request body.
func TestPushJoinToDevice_ForwardsMediaFormats(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"peer_id":"whips://x","token":"tok"}}`))
	}))
	defer srv.Close()

	prof := stubProfiler{
		profile: `{"screen_width":1,"screen_height":1,"audio_rate":8000,"audio_channels":1,"up_video_mt":"h264","down_video_mt":"mjpeg","down_audio_mt":"amr","calling_timeout_sec":30}`,
		remark:  "客厅联系人",
	}
	var pub stubPublisher

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	tirtcCfg := TirtcServerCfg{BaseURL: srv.URL, AccessID: "id", AppID: "app", SecretKey: "secret"}
	appCfg := WxAppCfg{ModelID: "model"}
	msg := &voipMessage{
		SessionKey:  "sk",
		RoomId:      "room",
		ServerToken: "st",
		OpenID:      "openid",
		PayloadData: &voipCallPayload{
			ID:       "call-1",
			From:     "dev1",
			RoomType: "video",
		},
	}

	if err := pushJoinToDevice(c, appCfg, tirtcCfg, "wxapp", "dev1", msg, &pub, &prof); err != nil {
		t.Fatalf("pushJoinToDevice: %v", err)
	}
	if !pub.called {
		t.Fatal("publisher.Publish was not called")
	}
	envelope, ok := pub.payload.(map[string]any)
	if !ok {
		t.Fatalf("published payload type=%T, want map[string]any", pub.payload)
	}
	push, ok := envelope["payload"].(map[string]any)
	if !ok {
		t.Fatalf("published envelope payload=%T, want map[string]any", envelope["payload"])
	}
	if push["wx_user_remark"] != "客厅联系人" || push["wx_user_nickname"] != "客厅联系人" {
		t.Fatalf("incoming caller remark not forwarded: %+v", push)
	}
	if push["wx_call_id"] != "call-1" ||
		push["wx_from"] != "dev1" ||
		push["wx_room_type"] != "video" {
		t.Fatalf("outgoing correlation fields not forwarded: %+v", push)
	}

	if gotBody["up_video_mt"] != "h264" {
		t.Errorf("up_video_mt not forwarded: want h264, got %v", gotBody["up_video_mt"])
	}
	if gotBody["down_video_mt"] != "mjpeg" {
		t.Errorf("down_video_mt not forwarded: want mjpeg, got %v", gotBody["down_video_mt"])
	}
	if gotBody["down_audio_mt"] != "amr" {
		t.Errorf("down_audio_mt not forwarded: want amr, got %v", gotBody["down_audio_mt"])
	}
	// video_mt must not leak when explicit up/down fields are set, otherwise
	// the cloud-side tirtc server rejects the request.
	if _, ok := gotBody["video_mt"]; ok {
		t.Errorf("video_mt should not be sent when up/down fields are set, got %v", gotBody["video_mt"])
	}
}

// TestPushJoinToDevice_LegacyVideoMt verifies the backward-compat path: when
// only the legacy video_mt field is in the device profile (no up_video_mt /
// down_video_mt), it is forwarded as video_mt to the token API.
func TestPushJoinToDevice_LegacyVideoMt(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"peer_id":"whips://x","token":"tok"}}`))
	}))
	defer srv.Close()

	prof := stubProfiler{profile: `{"screen_width":1,"screen_height":1,"audio_rate":8000,"audio_channels":1,"video_mt":"h264","calling_timeout_sec":30}`}
	var pub stubPublisher

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	tirtcCfg := TirtcServerCfg{BaseURL: srv.URL, AccessID: "id", AppID: "app", SecretKey: "secret"}
	appCfg := WxAppCfg{ModelID: "model"}
	msg := &voipMessage{SessionKey: "sk", RoomId: "room", ServerToken: "st", OpenID: "openid"}

	if err := pushJoinToDevice(c, appCfg, tirtcCfg, "wxapp", "dev2", msg, &pub, &prof); err != nil {
		t.Fatalf("pushJoinToDevice: %v", err)
	}
	if !pub.called {
		t.Fatal("publisher.Publish was not called")
	}

	if gotBody["video_mt"] != "h264" {
		t.Errorf("legacy video_mt not forwarded: want h264, got %v", gotBody["video_mt"])
	}
	if _, ok := gotBody["up_video_mt"]; ok {
		t.Errorf("up_video_mt should not be sent in legacy path, got %v", gotBody["up_video_mt"])
	}
	if _, ok := gotBody["down_video_mt"]; ok {
		t.Errorf("down_video_mt should not be sent in legacy path, got %v", gotBody["down_video_mt"])
	}
}
