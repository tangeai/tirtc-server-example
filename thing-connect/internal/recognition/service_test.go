package recognition

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

func wav(seconds int) []byte {
	b := make([]byte, 44+16000*seconds)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 8000)
	binary.LittleEndian.PutUint32(b[28:], 16000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	return b
}
func TestAudioLimitsRejectCorruptAndWrongMedia(t *testing.T) {
	v := wav(2)
	if !ValidAudio(v, Limits{5 << 20, 2}) {
		t.Fatal("valid PCM rejected")
	}
	for _, limits := range []Limits{{10, 2}, {5 << 20, 1}} {
		if ValidAudio(v, limits) {
			t.Fatal("limit ignored")
		}
	}
	v[4] = 0
	if ValidAudio(v, Limits{5 << 20, 15}) {
		t.Fatal("bad RIFF size accepted")
	}
	if ValidAudio([]byte("image/png"), Limits{5 << 20, 15}) {
		t.Fatal("image accepted")
	}
}
func TestSchemaValidationAndDefaultDisabled(t *testing.T) {
	if DefaultPolicy().Enabled || len(DefaultPolicy().Schemes) > 0 {
		t.Fatal("recognition must be opt-in")
	}
	for _, raw := range []string{`{"summary":"ok","labels":[],"details":{}}`, `{"summary":"","alarm":false,"labels":[],"details":{}}`, `{"summary":"ok","alarm":false,"labels":null,"details":{}}`} {
		if _, e := ValidateResult([]byte(raw)); e == nil {
			t.Fatal("invalid result accepted")
		}
	}
	if _, e := ValidateResult([]byte(`{"summary":"疑似哭声","alarm":true,"labels":["crying"],"details":{}}`)); e != nil {
		t.Fatal(e)
	}
}

type workStore struct {
	Store
	work *Work
	done *Completion
}

func (s *workStore) Recover(context.Context) error                  { return nil }
func (s *workStore) Claim(context.Context) (*Work, error)           { return s.work, nil }
func (s *workStore) Complete(_ context.Context, c Completion) error { s.done = &c; return nil }

type fakeProvider struct {
	calls int
	err   error
}

func (p *fakeProvider) Analyze(context.Context, Config, Scheme, []byte) (Result, CallUsage, error) {
	p.calls++
	return Result{Summary: "疑似哭声", Labels: []string{"crying"}, Details: map[string]any{}}, CallUsage{}, p.err
}
func (p *fakeProvider) Synthesize(context.Context, Config, Scheme, string) ([]byte, CallUsage, error) {
	p.calls++
	return []byte("audio"), CallUsage{}, p.err
}
func TestWorkerPreservesUnknownAndDoesNotCallWithoutKey(t *testing.T) {
	for _, err := range []error{nil, ErrInvalid, context.DeadlineExceeded} {
		store := &workStore{work: &Work{Stage: "analysis"}}
		provider := &fakeProvider{err: err}
		cfg := DefaultConfig()
		svc := New(store, provider, cfg)
		if e := svc.step(context.Background()); e != nil {
			t.Fatal(e)
		}
		if provider.calls != 0 {
			t.Fatal("called without key")
		}
		cfg.APIKey = "test"
		if e := svc.Configure(cfg); e != nil {
			t.Fatal(e)
		}
		if e := svc.step(context.Background()); e != nil {
			t.Fatal(e)
		}
		if store.done == nil || store.done.Unknown != (err != nil && !errors.Is(err, ErrInvalid)) {
			t.Fatal("unknown classification lost")
		}
	}
}
func TestSelectionRejectsDevicesAndDuplicateTypes(t *testing.T) {
	svc := New(nil, nil, DefaultConfig())
	_, e := svc.SetSelection(context.Background(), Actor{DeviceID: "dev"}, "dev", true, nil, 0)
	if !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	_, e = svc.SetSelection(context.Background(), Actor{UserID: 1}, "dev", true, []Binding{{"a", 1}, {"a", 1}}, 0)
	if !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}

type submitStore struct {
	Store
	request Request
	hash    string
	calls   int
}

func (s *submitStore) Submit(_ context.Context, _ Actor, _ string, req Request, hash string) (Task, bool, error) {
	s.calls++
	s.request, s.hash = req, hash
	return Task{}, true, nil
}
func TestRawPCMSubmitPreservesSamplesAndFingerprintsMetadata(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIKey = "test"
	store := &submitStore{}
	svc := New(store, nil, cfg)
	raw := make([]byte, 64000)
	for i := range raw {
		raw[i] = byte(i)
	}
	req := Request{ID: "pcm1", Revision: 1, MediaType: "audio", AudioFormat: "pcm_s16le", SampleRate: 16000, Channels: 1, Data: raw}
	_, _, err := svc.Submit(context.Background(), Actor{DeviceID: "dev"}, "dev", req)
	if err != nil {
		t.Fatal(err)
	}
	if string(store.request.Data[44:]) != string(raw) || !ValidAudio(store.request.Data, Limits{5 << 20, 2}) {
		t.Fatal("samples changed or invalid WAV")
	}
	if ValidAudio(store.request.Data, Limits{5 << 20, 1}) {
		t.Fatal("per-type duration limit ignored")
	}
	if ValidAudio(store.request.Data, Limits{int64(len(raw)), 2}) {
		t.Fatal("container size limit ignored")
	}
	hash := store.hash
	_, _, err = svc.Submit(context.Background(), Actor{DeviceID: "dev"}, "dev", req)
	if err != nil || hash != store.hash {
		t.Fatal("retry fingerprint changed")
	}
	req.SampleRate = 8000
	_, _, err = svc.Submit(context.Background(), Actor{DeviceID: "dev"}, "dev", req)
	if err != nil || hash == store.hash {
		t.Fatal("sample rate missing from fingerprint")
	}
	req.SampleRate = 16000
	req.Channels = 2
	_, _, err = svc.Submit(context.Background(), Actor{DeviceID: "dev"}, "dev", req)
	if err != nil || hash == store.hash {
		t.Fatal("channels missing from fingerprint")
	}
}
func TestRawPCMRejectsInvalidFormatAndLimits(t *testing.T) {
	base := Request{AudioFormat: "pcm_s16le", SampleRate: 16000, Channels: 1, Data: make([]byte, 32000)}
	for _, change := range []func(*Request){
		func(r *Request) { r.SampleRate = 0 }, func(r *Request) { r.Channels = 0 },
		func(r *Request) { r.SampleRate = 48001 }, func(r *Request) { r.Channels = 3 },
		func(r *Request) { r.Data = r.Data[:31999] }, func(r *Request) { r.Data = make([]byte, 32001) },
		func(r *Request) { r.Data = make([]byte, 32000*61) }, func(r *Request) { r.Data = make([]byte, 5<<20) },
		func(r *Request) { r.AudioFormat = "alaw" }, func(r *Request) { r.AudioFormat = "mp3" },
	} {
		req := base
		change(&req)
		if _, err := prepareAudio(req); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid audio accepted")
		}
	}
	for _, format := range []string{"", "wav"} {
		data := wav(1)
		out, err := prepareAudio(Request{AudioFormat: format, Data: data})
		if err != nil || string(out) != string(data) {
			t.Fatal("legacy WAV changed")
		}
	}
}
