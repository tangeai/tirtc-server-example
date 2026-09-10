package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/deviceprofile"
)

type profileStoreStub struct {
	deviceID string
	scenes   map[string]json.RawMessage
}

func (s *profileStoreStub) ReplaceScenes(_ context.Context, deviceID string, scenes map[string]json.RawMessage) (bool, error) {
	s.deviceID, s.scenes = deviceID, scenes
	return true, nil
}

func (*profileStoreStub) Get(context.Context, string) (*deviceprofile.Snapshot, error) {
	return &deviceprofile.Snapshot{}, nil
}

func TestLegacyVoIPSceneTranslatesVideoAndDropsIdentity(t *testing.T) {
	raw, err := legacyVoIPScene(json.RawMessage(`{"video_mt":"h264","audio_rate":"legacy-value","audio_channels":1,"device_id":"untrusted","future_option":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var scene map[string]json.RawMessage
	if err = json.Unmarshal(raw, &scene); err != nil {
		t.Fatal(err)
	}
	if string(scene["up_video_mt"]) != `"h264"` || string(scene["down_video_mt"]) != `"h264"` {
		t.Fatalf("scene=%s", raw)
	}
	if _, ok := scene["device_id"]; ok {
		t.Fatal("identity field persisted")
	}
	if _, ok := scene["future_option"]; !ok {
		t.Fatal("deprecated endpoint dropped a legacy extension field")
	}
	if string(scene["audio_rate"]) != `"legacy-value"` {
		t.Fatal("deprecated endpoint tightened a legacy media-field constraint")
	}
}

func TestDeprecatedProfileEndpointWritesUnifiedScene(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &profileStoreStub{}
	server := NewServer(nil, nil, nil, nil, deviceprofile.NewService(store))
	router := gin.New()
	router.POST("/profile", func(c *gin.Context) {
		c.Set("device_id", "device-1")
		server.postDeviceProfile(c)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(`{"up_video_mt":"none","down_video_mt":"none","down_audio_mt":"alaw","audio_rate":8000,"audio_channels":1,"no_video":true}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Deprecation") != "true" {
		t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if store.deviceID != "device-1" || len(store.scenes["voip"]) == 0 {
		t.Fatalf("device=%q scenes=%v", store.deviceID, store.scenes)
	}
}
