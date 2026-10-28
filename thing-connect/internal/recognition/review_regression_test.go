package recognition

import (
	"context"
	"encoding/binary"
	"testing"
)

type availabilityStore struct{ Store }

func (*availabilityStore) State(context.Context, Actor, string) (State, error) {
	return State{EffectiveEnabled: true}, nil
}
func (*availabilityStore) SetSelection(context.Context, Actor, string, bool, []Binding, int64) (State, error) {
	return State{EffectiveEnabled: true}, nil
}
func TestSavedAvailabilityMatchesRead(t *testing.T) {
	for _, key := range []string{"", "test"} {
		cfg := DefaultConfig()
		cfg.APIKey = key
		svc := New(&availabilityStore{}, nil, cfg)
		saved, e := svc.SetSelection(context.Background(), Actor{UserID: 1}, "dev", true, []Binding{{"type", 1}}, 0)
		if e != nil {
			t.Fatal(e)
		}
		read, e := svc.State(context.Background(), Actor{UserID: 1}, "dev")
		if e != nil || saved.EffectiveEnabled != read.EffectiveEnabled {
			t.Fatalf("save=%v read=%v err=%v", saved.EffectiveEnabled, read.EffectiveEnabled, e)
		}
	}
}
func TestRejectMalformedWAVChunks(t *testing.T) {
	original := wav(1)
	before := append(append(append([]byte{}, original[:12]...), original[36:]...), original[12:36]...)
	trailing := append(append([]byte{}, original...), 0)
	binary.LittleEndian.PutUint32(trailing[4:], uint32(len(trailing)-8))
	duplicate := append(append([]byte{}, original...), []byte{'d', 'a', 't', 'a', 0, 0, 0, 0}...)
	binary.LittleEndian.PutUint32(duplicate[4:], uint32(len(duplicate)-8))
	for name, data := range map[string][]byte{"data_before_fmt": before, "trailing_fragment": trailing, "duplicate_data": duplicate} {
		t.Run(name, func(t *testing.T) {
			if ValidAudio(data, Limits{5 << 20, 60}) {
				t.Fatal("malformed WAV accepted")
			}
		})
	}
}

func TestMissingKeyStillReachesReplayLookup(t *testing.T) {
	store := &submitStore{}
	svc := New(store, nil, DefaultConfig())
	_, _, err := svc.Submit(context.Background(), Actor{DeviceID: "dev"}, "dev", Request{ID: "req", MediaType: "audio", Data: wav(1)})
	if err != nil || store.calls != 1 || !store.request.RejectNew {
		t.Fatalf("calls=%d reject=%v err=%v", store.calls, store.request.RejectNew, err)
	}
}
