package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"thing-connect/internal/deviceprofile"
)

// profileStoreStub is a minimal fake for deviceprofile.Store.
type profileStoreStub struct {
	snap *deviceprofile.Snapshot
	err  error
}

func (s *profileStoreStub) ReplaceScenes(context.Context, string, map[string]json.RawMessage) (bool, error) {
	return false, nil
}

func (s *profileStoreStub) Get(context.Context, string) (*deviceprofile.Snapshot, error) {
	return s.snap, s.err
}

func TestRtcTokenProfiles(t *testing.T) {
	streamSnap := &deviceprofile.Snapshot{DeviceID: "d1",
		Profile: json.RawMessage(`{"stream": {"up_audio_mt": ["alaw"], "up_video_mt": ["h264"]}}`)}

	tests := []struct {
		name    string
		server  *Server
		wantNil bool
	}{
		{name: "nil store", server: &Server{}, wantNil: true},
		{name: "store error", server: &Server{profileSvc: deviceprofile.NewService(&profileStoreStub{err: errors.New("db down")})}, wantNil: true},
		{name: "no profile row", server: &Server{profileSvc: deviceprofile.NewService(&profileStoreStub{snap: &deviceprofile.Snapshot{DeviceID: "d1"}})}, wantNil: true},
		{name: "stream snapshot", server: &Server{profileSvc: deviceprofile.NewService(&profileStoreStub{snap: streamSnap})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got map[string]map[string]json.RawMessage
			if tt.server.profileSvc != nil {
				got = tt.server.profileSvc.OptionalPublicSnapshot(context.Background(), "d1")
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("want nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("want non-nil result")
			}
			if _, ok := got["stream"]; !ok {
				t.Fatalf("missing stream key in %v", got)
			}
		})
	}
}

func TestBuildRtcTokenData(t *testing.T) {
	profiles := map[string]map[string]json.RawMessage{
		"stream": {"aspect_ratio": json.RawMessage(`"4:3"`)},
	}

	data := buildRtcTokenData("tok", "app", "https://ep", false, profiles)
	if data["token"] != "tok" || data["app_id"] != "app" || data["endpoint"] != "https://ep" {
		t.Fatalf("unexpected base fields: %v", data)
	}
	if data["in_call"] != false {
		t.Fatalf("in_call = %v, want false", data["in_call"])
	}
	if _, ok := data["profiles"]; !ok {
		t.Fatal("profiles key missing")
	}

	noProfiles := buildRtcTokenData("tok", "app", "https://ep", true, nil)
	if _, ok := noProfiles["profiles"]; ok {
		t.Fatal("profiles key must be absent when nil")
	}
	if noProfiles["in_call"] != true {
		t.Fatalf("in_call = %v, want true", noProfiles["in_call"])
	}
}
