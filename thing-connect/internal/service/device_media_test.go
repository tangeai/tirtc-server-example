package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type mediaStoreStub struct {
	called bool
	bound  bool
	err    error
}

func (s *mediaStoreStub) ReportMedia(context.Context, string, map[string]json.RawMessage) (bool, error) {
	s.called = true
	return s.bound, s.err
}
func mediaInput(raw string) map[string]json.RawMessage {
	var value map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &value)
	return value
}

func TestDeviceMediaValidationAndStoreBoundary(t *testing.T) {
	for _, raw := range []string{`{}`, `{"ai":{}}`, `{"stream":null}`, `{"call":{"token":"secret"}}`, `{"stream":{"audio_rate":0}}`, `{"stream":{"hor_mirror":0}}`, `{"stream":{"camera_rotation":45}}`, `{"stream":{"down_audio_mt":["h264"]}}`, `{"stream":{"up_video_mt":"opus"}}`, `{"call":{"audio_channels":null}}`} {
		store := &mediaStoreStub{bound: true}
		err := NewDeviceMediaService(store).Report(context.Background(), "device", mediaInput(raw))
		if !errors.Is(err, ErrInvalidMediaProfile) || store.called {
			t.Errorf("input %s: err=%v store called=%v", raw, err, store.called)
		}
	}
	valid := mediaInput(`{"stream":{"up_audio_mt":"alaw","down_audio_mt":["opus","amr"],"camera_rotation":0,"hor_mirror":false,"up_video_mt":[],"audio_rate":16000,"aspect_ratio":1.3333333},"call":{}}`)
	store := &mediaStoreStub{bound: true}
	if err := NewDeviceMediaService(store).Report(context.Background(), "device", valid); err != nil || !store.called {
		t.Fatal(err)
	}
	store.bound = false
	if err := NewDeviceMediaService(store).Report(context.Background(), "device", valid); !errors.Is(err, ErrDeviceReset) {
		t.Fatal(err)
	}
	store.err = errors.New("db unavailable")
	if err := NewDeviceMediaService(store).Report(context.Background(), "device", valid); !errors.Is(err, store.err) {
		t.Fatal(err)
	}
}

func TestReportedMediaProfilesUseSceneReplacement(t *testing.T) {
	legacy := `{"down_audio_mt":"amr","camera_rotation":90}`
	report := `{"stream":{"up_audio_mt":"pcm","camera_rotation":0,"hor_mirror":false},"voip":{}}`
	profiles := reportedDeviceMediaProfiles(&report, &legacy)
	if _, ok := profiles["call"]; ok {
		t.Fatal("unreported call invented")
	}
	if len(profiles["voip"]) != 0 {
		t.Fatal("cleared scene fell back to stale VoIP data")
	}
	if string(profiles["stream"]["camera_rotation"]) != "0" || string(profiles["stream"]["hor_mirror"]) != "false" {
		t.Fatal(profiles)
	}
	report = `{"call":{"down_audio_mt":["opus","amr"]}}`
	profiles = reportedDeviceMediaProfiles(&report, &legacy)
	if string(profiles["voip"]["down_audio_mt"]) != `"amr"` {
		t.Fatal("legacy VoIP compatibility lost")
	}
}

func TestDeviceMediaFieldBoundaries(t *testing.T) {
	cases := []struct {
		name, fields string
		valid        bool
	}{
		{"all scenes may be cleared", `{}`, true},
		{"codec aliases and preference", `{"down_audio_mt":"opus/amr;pcm|alaw g711a","up_video_mt":["h264","h265","mjpeg"]}`, true},
		{"maximum codecs", `{"down_audio_mt":["opus","amr","pcm","alaw","g711a","amr_nb","amr_wb","aac"]}`, true},
		{"too many codecs", `{"down_audio_mt":["opus","opus","opus","opus","opus","opus","opus","opus","opus"]}`, false},
		{"codec null element", `{"down_audio_mt":[null]}`, false},
		{"codec nested element", `{"down_audio_mt":[["opus"]]}`, false},
		{"codec number", `{"up_audio_mt":123}`, false},
		{"codec unknown", `{"up_audio_mt":"madeup"}`, false},
		{"explicit no media", `{"up_audio_mt":[],"up_video_mt":[],"no_video":true}`, true},
		{"string boolean", `{"no_video":"false"}`, false},
		{"null boolean", `{"hor_mirror":null}`, false},
		{"numeric rate string", `{"audio_rate":"16000"}`, false},
		{"invalid channels", `{"audio_channels":3}`, false},
		{"fractional rotation", `{"camera_rotation":0.5}`, false},
		{"negative rotation", `{"camera_rotation":-90}`, false},
		{"valid display", `{"camera_rotation":270,"hor_mirror":false,"vert_mirror":true,"aspect_ratio":"16:9","object_fit":"cover"}`, true},
		{"numeric ratio", `{"aspect_ratio":1.7777777778}`, true},
		{"zero ratio", `{"aspect_ratio":0}`, false},
		{"negative ratio", `{"aspect_ratio":-1}`, false},
		{"infinite ratio", `{"aspect_ratio":1e999}`, false},
		{"zero divisor", `{"aspect_ratio":"16:0"}`, false},
		{"ratio exceeds bounds", `{"aspect_ratio":"10000:1"}`, false},
		{"unknown display mode", `{"object_fit":"stretch"}`, false},
		{"unexpected sensitive field", `{"token":"private"}`, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMediaProfiles(map[string]json.RawMessage{"call": json.RawMessage(tt.fields)})
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
		})
	}
}
