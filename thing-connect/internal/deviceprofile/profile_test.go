package deviceprofile

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type storeStub struct {
	bound   bool
	written map[string]json.RawMessage
	profile json.RawMessage
}

func (s *storeStub) ReplaceScenes(_ context.Context, _ string, scenes map[string]json.RawMessage) (bool, error) {
	s.written = scenes
	return s.bound, nil
}

func (s *storeStub) Get(context.Context, string) (*Snapshot, error) {
	if s.profile == nil {
		return &Snapshot{}, nil
	}
	return &Snapshot{DeviceID: "device-1", Profile: s.profile}, nil
}

func TestValidateSceneSpecificFields(t *testing.T) {
	valid := json.RawMessage(`{"screen_width":640,"screen_height":480,"up_video_mt":"h264","down_video_mt":"mjpeg","down_audio_mt":"amr","audio_rate":8000,"audio_channels":1,"video_res_mode":"fit_screen","calling_timeout_sec":30}`)
	if err := Validate(map[string]json.RawMessage{"voip": valid}); err != nil {
		t.Fatal(err)
	}
	for _, scenes := range []map[string]json.RawMessage{
		{"stream": json.RawMessage(`{"screen_width":640}`)},
		{"voip": json.RawMessage(`{"up_video_mt":["h264"]}`)},
		{"voip": json.RawMessage(`{"up_audio_mt":"alaw"}`)},
		{"voip": json.RawMessage(`{"has_camera":true}`)},
		{"voip": json.RawMessage(`{"calling_timeout_sec":0}`)},
	} {
		if err := Validate(scenes); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid scenes: %s", scenes)
		}
	}
}

func TestServiceUsesOneStoreBoundary(t *testing.T) {
	store := &storeStub{bound: true}
	service := NewService(store)
	scenes := map[string]json.RawMessage{"voip": json.RawMessage(`{"no_video":true,"up_video_mt":"none","down_video_mt":"none"}`)}
	if err := service.Report(context.Background(), "device-1", scenes); err != nil {
		t.Fatal(err)
	}
	if string(store.written["voip"]) != string(scenes["voip"]) {
		t.Fatalf("written=%s", store.written["voip"])
	}
	store.bound = false
	if err := service.Report(context.Background(), "device-1", scenes); !errors.Is(err, ErrUnbound) {
		t.Fatalf("err=%v", err)
	}
}

func TestPublicAddsDerivedVoIPFieldsInsideScene(t *testing.T) {
	profile := `{"stream":{"up_video_mt":["h264"]},"voip":{"up_video_mt":"none","down_video_mt":"mjpeg","no_video":false}}`
	public := Public(&profile)
	voip := public["voip"]
	if string(voip["has_camera"]) != "false" || string(voip["has_screen"]) != "true" ||
		string(voip["voip_room_type"]) != `"video"` {
		t.Fatalf("voip=%v", voip)
	}
	if _, ok := public["stream"]["has_camera"]; ok {
		t.Fatal("derived VoIP field leaked into stream")
	}
}

func TestPublicKeepsDerivedStateForEmptyVoIPScene(t *testing.T) {
	profile := `{"voip":{}}`
	voip := Public(&profile)["voip"]
	if len(voip) != 3 || string(voip["has_camera"]) != "false" ||
		string(voip["has_screen"]) != "false" || string(voip["voip_room_type"]) != `"voice"` {
		t.Fatalf("voip=%v", voip)
	}
}

func TestPublicProjectsMigratedLegacyVoIP(t *testing.T) {
	profile := `{"voip":{"video_mt":"h264","down_audio_mt":"amr","camera_rotation":90,"device_id":"untrusted","future_option":true}}`
	voip := Public(&profile)["voip"]
	if string(voip["up_video_mt"]) != `"h264"` || string(voip["down_video_mt"]) != `"h264"` {
		t.Fatalf("voip=%v", voip)
	}
	if _, ok := voip["device_id"]; ok {
		t.Fatal("identity field exposed")
	}
	if _, ok := voip["future_option"]; ok {
		t.Fatal("unknown field exposed")
	}
}

func TestVoIPReadsOnlyVoIPScene(t *testing.T) {
	store := &storeStub{profile: json.RawMessage(`{"stream":{"audio_rate":8000},"voip":{"down_audio_mt":"amr"}}`)}
	raw, err := NewService(store).VoIP(context.Background(), "device-1")
	if err != nil || string(raw) != `{"down_audio_mt":"amr"}` {
		t.Fatalf("raw=%s err=%v", raw, err)
	}
}
