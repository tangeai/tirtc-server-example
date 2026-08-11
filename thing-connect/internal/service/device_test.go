package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"thing-connect/internal/model"
	"thing-connect/internal/physid"
	"thing-connect/internal/store"
)

// ── fake stores ───────────────────────────────────────────────────────────────

type fakeDeviceStore struct {
	deviceKey *model.DevicePool
	bindByID  *model.DeviceBind
	bindByFP  *model.DeviceBind // returned by GetBindByFingerprint
}

func (f *fakeDeviceStore) GetDeviceKey(_ context.Context, _ string) (*model.DevicePool, error) {
	return f.deviceKey, nil
}
func (f *fakeDeviceStore) GetBindByDeviceID(_ context.Context, _ string) (*model.DeviceBind, error) {
	return f.bindByID, nil
}
func (f *fakeDeviceStore) UpdateActiveTimeIfEmpty(_ context.Context, _ string) error {
	return nil
}

func (f *fakeDeviceStore) GetBindByFingerprint(_ context.Context, _ string, _ int64) (*model.DeviceBind, error) {
	return f.bindByFP, nil
}

type fakeCacheForDevice struct {
	attempt       int64
	verifyOK      bool
	nonceOK       bool
	replay        []byte
	fpAdded       bool
	fpCount       int64
	fpBlock       bool
	globalPending int64
	globalCap     int64
	reportFP      string // GetReportFingerprint return value
	pendingBind   string
	delVerify     bool
}

func (f *fakeCacheForDevice) IncrReportAttempt(_ context.Context, _ string, _ time.Duration) (int64, error) {
	if f.attempt == 0 {
		return 1, nil
	}
	return f.attempt, nil
}
func (f *fakeCacheForDevice) SetReportReplay(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}
func (f *fakeCacheForDevice) GetReportReplay(_ context.Context, _ string) ([]byte, error) {
	return f.replay, nil
}
func (f *fakeCacheForDevice) SetVerifyRecord(_ context.Context, _ string, _ []byte, _ time.Duration) (bool, error) {
	return f.verifyOK, nil
}
func (f *fakeCacheForDevice) GetVerifyRecord(_ context.Context, _ string) ([]byte, error) {
	return nil, errors.New("not used")
}
func (f *fakeCacheForDevice) ReserveDeviceCode(_ context.Context, _, _ string, _ time.Duration) (bool, error) {
	return true, nil
}
func (f *fakeCacheForDevice) GetDeviceCodeLookup(_ context.Context, _ string) (string, error) {
	return "", errors.New("not used")
}
func (f *fakeCacheForDevice) DelDeviceCodeLookup(_ context.Context, _ string) error { return nil }
func (f *fakeCacheForDevice) SetEmailCode(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}
func (f *fakeCacheForDevice) GetEmailCode(_ context.Context, _ string) (string, error) {
	return "", errors.New("not used")
}
func (f *fakeCacheForDevice) ConsumeEmailCode(_ context.Context, _, _ string) (bool, error) {
	return false, errors.New("not used")
}
func (f *fakeCacheForDevice) IsDeviceOnline(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (f *fakeCacheForDevice) IsInCall(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (f *fakeCacheForDevice) DelVerifyAndCode(_ context.Context, _, _ string) error {
	f.delVerify = true
	return nil
}
func (f *fakeCacheForDevice) DelEmailCode(_ context.Context, _ string) error { return nil }
func (f *fakeCacheForDevice) IncrPasswordResetAttempt(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 1, nil
}
func (f *fakeCacheForDevice) SetNonce(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return f.nonceOK, nil
}
func (f *fakeCacheForDevice) SetPendingBind(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}
func (f *fakeCacheForDevice) GetPendingBind(_ context.Context, _ string) (string, error) {
	return f.pendingBind, nil
}
func (f *fakeCacheForDevice) DelPendingBind(_ context.Context, _ string) error { return nil }

func (f *fakeCacheForDevice) AddIPFingerprint(_ context.Context, _, _ string, _ time.Duration) (bool, int64, error) {
	if f.fpBlock {
		return false, 0, errors.New("redis down")
	}
	return f.fpAdded, f.fpCount, nil
}
func (f *fakeCacheForDevice) IncrGlobalPending(_ context.Context) (int64, error) {
	f.globalPending++
	return f.globalPending, nil
}
func (f *fakeCacheForDevice) DecrGlobalPending(_ context.Context) error {
	f.globalPending--
	return nil
}
func (f *fakeCacheForDevice) ReconcileGlobalPending(_ context.Context) error { return nil }
func (f *fakeCacheForDevice) SetReportFingerprint(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}
func (f *fakeCacheForDevice) GetReportFingerprint(_ context.Context, _ string) (string, error) {
	return f.reportFP, nil
}
func (f *fakeCacheForDevice) DelReportFingerprint(_ context.Context, _ string) error {
	return nil
}

// ── helpers ────────────────────────────────────────────────────────────────────

const testIP = "10.0.0.1"

var noHdr = ReportHeaders{} // unsigned request: no headers present

type ttsAuthCache struct {
	store.CacheStore
	codeToHash map[string]string
	verify     map[string][]byte
}

func (f *ttsAuthCache) GetDeviceCodeLookup(_ context.Context, code string) (string, error) {
	return f.codeToHash[code], nil
}

func (f *ttsAuthCache) GetVerifyRecord(_ context.Context, physHash string) ([]byte, error) {
	return f.verify[physHash], nil
}

func TestDeviceService_AuthorizeTTS(t *testing.T) {
	const (
		secret   = "tts-secret"
		code     = "386236"
		physHash = "physical-hash"
		clientID = "tmp_a1b2c3d4"
	)
	record, _ := json.Marshal(map[string]string{"code": code, "temp_client_id": clientID})
	cache := &ttsAuthCache{
		codeToHash: map[string]string{code: physHash},
		verify:     map[string][]byte{physHash: record},
	}
	svc := NewDeviceService(nil, cache, secret, DefaultServiceConfig())
	token, err := issueMQTTToken(clientID, secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.AuthorizeTTS(context.Background(), code, token); err != nil {
		t.Fatalf("matching code/token should pass: %v", err)
	}

	otherToken, _ := issueMQTTToken("tmp_other", secret, time.Minute)
	if err := svc.AuthorizeTTS(context.Background(), code, otherToken); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("token from another report: want ErrInvalidCode, got %v", err)
	}
	if err := svc.AuthorizeTTS(context.Background(), code, "not-a-jwt"); !errors.Is(err, ErrSigFail) {
		t.Fatalf("malformed token: want ErrSigFail, got %v", err)
	}
	if err := svc.AuthorizeTTS(context.Background(), "12345x", token); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("non-six-digit code: want ErrInvalidCode, got %v", err)
	}
}

// signedHdr creates a valid ReportHeaders with all fields present.
func signedHdr(deviceID, ts, nonce, sig string) ReportHeaders {
	return ReportHeaders{
		DeviceID:     deviceID,
		Timestamp:    ts,
		Nonce:        nonce,
		Signature:    sig,
		HasDeviceID:  true,
		HasTimestamp: true,
		HasNonce:     true,
		HasSignature: true,
	}
}

func newDefaultSvc(fake *fakeCacheForDevice) *DeviceService {
	return NewDeviceService(&fakeDeviceStore{}, fake, "secret", DefaultServiceConfig())
}

// ── 情况2: unsigned report tests ────────────────────────────────────────────────

func TestDeviceService_Report_EmptyFingerprint(t *testing.T) {
	svc := newDefaultSvc(&fakeCacheForDevice{})
	_, err := svc.Report(context.Background(), testIP, noHdr, "", "")
	if !errors.Is(err, ErrEmptyFingerprint) {
		t.Errorf("want ErrEmptyFingerprint, got %v", err)
	}
}

func TestDeviceService_Report_RateLimit(t *testing.T) {
	cfg := DefaultServiceConfig()
	svc := NewDeviceService(&fakeDeviceStore{}, &fakeCacheForDevice{attempt: int64(cfg.RateLimitMaxHits) + 1}, "secret", cfg)
	_, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:FF", "")
	if !errors.Is(err, ErrRateLimit) {
		t.Errorf("want ErrRateLimit, got %v", err)
	}
}

func TestDeviceService_Report_RateLimit_EvenWithReplayCached(t *testing.T) {
	cfg := DefaultServiceConfig()
	cached := ReportResult{Code: "654321", TempToken: "tok", TempClientID: "tmp_x"}
	raw, _ := json.Marshal(cached)
	svc := NewDeviceService(&fakeDeviceStore{}, &fakeCacheForDevice{attempt: int64(cfg.RateLimitMaxHits) + 1, replay: raw}, "secret", cfg)
	_, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:FF", "")
	if !errors.Is(err, ErrRateLimit) {
		t.Errorf("want ErrRateLimit, got %v", err)
	}
}

func TestDeviceService_Report_ReplayWithinWindow(t *testing.T) {
	cached := ReportResult{Code: "123456", TempToken: "cached-token", TempClientID: "tmp_cached"}
	raw, _ := json.Marshal(cached)
	svc := newDefaultSvc(&fakeCacheForDevice{attempt: 3, replay: raw})
	result, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:FF", "")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if *result != cached {
		t.Errorf("want replayed result %+v, got %+v", cached, *result)
	}
}

func TestDeviceService_Report_AlreadyBound_StillReturnsCodeOnly(t *testing.T) {
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-abc"},
	}
	svc := NewDeviceService(devStore, &fakeCacheForDevice{verifyOK: true}, "secret", DefaultServiceConfig())
	result, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:FF", "")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if result.Code == "" {
		t.Error("want non-empty Code")
	}
	if result.TempToken == "" {
		t.Error("want non-empty TempToken")
	}
}

func TestDeviceService_Report_Unbound_ReturnsCodeAndToken(t *testing.T) {
	svc := NewDeviceService(&fakeDeviceStore{}, &fakeCacheForDevice{verifyOK: true}, "secret", DefaultServiceConfig())
	result, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:FF", "")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if result.Code == "" {
		t.Error("want non-empty Code")
	}
	if result.TempToken == "" {
		t.Error("want non-empty TempToken")
	}
}

func TestDeviceService_Report_VerifyPending(t *testing.T) {
	svc := NewDeviceService(&fakeDeviceStore{}, &fakeCacheForDevice{verifyOK: false}, "secret", DefaultServiceConfig())
	_, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:FF", "")
	if !errors.Is(err, ErrVerifyPending) {
		t.Errorf("want ErrVerifyPending, got %v", err)
	}
}

// ── L2: IP fingerprint diversity tests ─────────────────────────────────────────

func TestDeviceService_Report_IPFingerprint_WithinLimit(t *testing.T) {
	cfg := DefaultServiceConfig()
	svc := NewDeviceService(&fakeDeviceStore{}, &fakeCacheForDevice{verifyOK: true, fpAdded: true, fpCount: int64(cfg.IPRateLimitMaxFingerprints)}, "secret", cfg)
	result, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:01", "")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if result.Code == "" {
		t.Error("want non-empty Code")
	}
}

func TestDeviceService_Report_IPFingerprint_ExceededLimit(t *testing.T) {
	cfg := DefaultServiceConfig()
	svc := NewDeviceService(&fakeDeviceStore{}, &fakeCacheForDevice{fpAdded: true, fpCount: int64(cfg.IPRateLimitMaxFingerprints) + 1}, "secret", cfg)
	_, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:02", "")
	if !errors.Is(err, ErrIPFingerprintLimit) {
		t.Errorf("want ErrIPFingerprintLimit, got %v", err)
	}
}

func TestDeviceService_Report_IPFingerprint_ExistingOverLimit_StillReplays(t *testing.T) {
	cfg := DefaultServiceConfig()
	cached := ReportResult{Code: "999999", TempToken: "tok", TempClientID: "tmp_old"}
	raw, _ := json.Marshal(cached)
	svc := NewDeviceService(&fakeDeviceStore{}, &fakeCacheForDevice{fpAdded: false, fpCount: 100, attempt: 2, replay: raw}, "secret", cfg)
	result, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:01", "")
	if err != nil {
		t.Fatalf("existing fingerprint should be allowed: got %v", err)
	}
	if result.Code != "999999" {
		t.Errorf("want replayed code 999999, got %s", result.Code)
	}
}

// ── L4: global pending cap tests ───────────────────────────────────────────────

func TestDeviceService_Report_GlobalPending_WithinCap(t *testing.T) {
	cfg := DefaultServiceConfig()
	svc := NewDeviceService(&fakeDeviceStore{}, &fakeCacheForDevice{verifyOK: true, globalPending: int64(cfg.GlobalMaxPendingCodes - 1)}, "secret", cfg)
	result, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:03", "")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if result.Code == "" {
		t.Error("want non-empty Code")
	}
}

func TestDeviceService_Report_GlobalPending_ExceededCap(t *testing.T) {
	cfg := DefaultServiceConfig()
	cache := &fakeCacheForDevice{verifyOK: true, globalPending: int64(cfg.GlobalMaxPendingCodes)}
	svc := NewDeviceService(&fakeDeviceStore{}, cache, "secret", cfg)
	_, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:04", "")
	if !errors.Is(err, ErrGlobalBusy) {
		t.Errorf("want ErrGlobalBusy, got %v", err)
	}
	if cache.globalPending != int64(cfg.GlobalMaxPendingCodes) {
		t.Errorf("global pending should be rolled back to %d, got %d", cfg.GlobalMaxPendingCodes, cache.globalPending)
	}
}

func TestDeviceService_Report_Replay_NoGlobalCheck(t *testing.T) {
	cfg := DefaultServiceConfig()
	cached := ReportResult{Code: "111111", TempToken: "replay-tok", TempClientID: "tmp_replay"}
	raw, _ := json.Marshal(cached)
	svc := NewDeviceService(&fakeDeviceStore{}, &fakeCacheForDevice{attempt: 2, replay: raw, globalPending: int64(cfg.GlobalMaxPendingCodes) + 100}, "secret", cfg)
	result, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:FF", "")
	if err != nil {
		t.Fatalf("replay should bypass global cap: got %v", err)
	}
	if result.Code != "111111" {
		t.Errorf("want replayed code 111111, got %s", result.Code)
	}
}

// ── 情况3: body device_id without signature → rejected ──────────────────────────

func TestDeviceService_Report_BodyDeviceID_NoSignature_Rejected(t *testing.T) {
	svc := NewDeviceService(&fakeDeviceStore{}, &fakeCacheForDevice{}, "secret", DefaultServiceConfig())
	_, err := svc.Report(context.Background(), testIP, noHdr, "AA:BB:CC:DD:EE:FF", "dev-001")
	if !errors.Is(err, ErrDeviceIDUntrusted) {
		t.Errorf("want ErrDeviceIDUntrusted, got %v", err)
	}
}

// ── 情况1: signed report tests ──────────────────────────────────────────────────

func TestDeviceService_Report_Signed_ReturnsCodeAndToken(t *testing.T) {
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "signed-nonce-1"
	sig := computeHMAC("key-xyz", "dev-001"+ts+nonce)
	hdr := signedHdr("dev-001", ts, nonce, sig)

	svc := NewDeviceService(devStore, &fakeCacheForDevice{verifyOK: true, nonceOK: true}, "secret", DefaultServiceConfig())
	result, err := svc.Report(context.Background(), testIP, hdr, "AA:BB:CC:DD:EE:FF", "")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if result.Code == "" {
		t.Error("want non-empty Code")
	}
	if result.TempToken == "" {
		t.Error("want non-empty TempToken")
	}
	if result.TempClientID == "" {
		t.Error("want non-empty TempClientID")
	}
}

func TestDeviceService_Report_Signed_HeaderIncomplete_Rejected(t *testing.T) {
	// hasAny but not hasAll → actionable detail while retaining ErrSigFail.
	hdr := ReportHeaders{
		HasDeviceID: true,
		// missing Timestamp, Nonce, Signature
	}
	svc := newDefaultSvc(&fakeCacheForDevice{})
	_, err := svc.Report(context.Background(), testIP, hdr, "AA:BB:CC:DD:EE:FF", "")
	if !errors.Is(err, ErrSigFail) {
		t.Errorf("want ErrSigFail, got %v", err)
	}
	if !errors.Is(err, ErrSigFieldsMissing) {
		t.Errorf("want ErrSigFieldsMissing, got %v", err)
	}
}

func TestDeviceService_Report_Signed_NonceReplay_Rejected(t *testing.T) {
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "replayed-report-nonce"
	hdr := signedHdr("dev-001", ts, nonce, computeHMAC("key-xyz", "dev-001"+ts+nonce))
	svc := NewDeviceService(devStore, &fakeCacheForDevice{nonceOK: false}, "secret", DefaultServiceConfig())
	_, err := svc.Report(context.Background(), testIP, hdr, "AA:BB:CC:DD:EE:FF", "")
	if !errors.Is(err, ErrSigNonceReplay) || !errors.Is(err, ErrSigFail) {
		t.Fatalf("want ErrSigNonceReplay retaining ErrSigFail, got %v", err)
	}
}

func TestDeviceService_Report_Signed_BadSignature_Rejected(t *testing.T) {
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	hdr := signedHdr("dev-001", ts, "nonce-1", "badsignature")
	svc := NewDeviceService(devStore, &fakeCacheForDevice{}, "secret", DefaultServiceConfig())
	_, err := svc.Report(context.Background(), testIP, hdr, "AA:BB:CC:DD:EE:FF", "")
	if !errors.Is(err, ErrSigFail) {
		t.Errorf("want ErrSigFail, got %v", err)
	}
}

func TestDeviceService_Token_NonceReplay(t *testing.T) {
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "replayed-token-nonce"
	sig := computeHMAC("key-xyz", "dev-001"+ts+nonce)
	svc := NewDeviceService(devStore, &fakeCacheForDevice{nonceOK: false}, "secret", DefaultServiceConfig())
	_, err := svc.Token(context.Background(), "dev-001", ts, nonce, sig, "")
	if !errors.Is(err, ErrSigNonceReplay) || !errors.Is(err, ErrSigFail) {
		t.Fatalf("want ErrSigNonceReplay retaining ErrSigFail, got %v", err)
	}
}

func TestDeviceService_Report_Signed_FingerprintMismatch(t *testing.T) {
	// First report sets baseline fingerprint. Second report has different
	// fingerprint → ErrFingerprintMismatch.
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "fp-mismatch-1"
	sig := computeHMAC("key-xyz", "dev-001"+ts+nonce)
	hdr := signedHdr("dev-001", ts, nonce, sig)

	// Stored baseline is for a different physHash
	cache := &fakeCacheForDevice{verifyOK: true, nonceOK: true, reportFP: "deadbeef00000000"}
	svc := NewDeviceService(devStore, cache, "secret", DefaultServiceConfig())
	_, err := svc.Report(context.Background(), testIP, hdr, "AA:BB:CC:DD:EE:FF", "")
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Errorf("want ErrFingerprintMismatch, got %v", err)
	}
}

func TestDeviceService_Report_Signed_ReplayWithinWindow(t *testing.T) {
	// Same physHash within 190s → replay cached result.
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "signed-replay-1"
	sig := computeHMAC("key-xyz", "dev-001"+ts+nonce)
	hdr := signedHdr("dev-001", ts, nonce, sig)

	cached := ReportResult{Code: "888888", TempToken: "replay-tok", TempClientID: "tmp_replay"}
	raw, _ := json.Marshal(cached)
	cache := &fakeCacheForDevice{
		verifyOK: true, nonceOK: true, attempt: 2, replay: raw,
		reportFP: physid.Hash("AA:BB:CC:DD:EE:FF"), pendingBind: "tmp_replay",
	}
	svc := NewDeviceService(devStore, cache, "secret", DefaultServiceConfig())
	result, err := svc.Report(context.Background(), testIP, hdr, "AA:BB:CC:DD:EE:FF", "")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if *result != cached {
		t.Errorf("want replayed result %+v, got %+v", cached, *result)
	}
}

func TestDeviceService_Report_Signed_DoesNotReuseUnsignedReplay(t *testing.T) {
	devStore := &fakeDeviceStore{deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"}}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	hdr := signedHdr("dev-001", ts, "upgrade-replay", computeHMAC("key-xyz", "dev-001"+ts+"upgrade-replay"))
	cached := ReportResult{Code: "111111", TempToken: "anonymous", TempClientID: "tmp_anonymous"}
	raw, _ := json.Marshal(cached)
	cache := &fakeCacheForDevice{verifyOK: true, nonceOK: true, attempt: 2, replay: raw}

	result, err := NewDeviceService(devStore, cache, "secret", DefaultServiceConfig()).Report(
		context.Background(), testIP, hdr, "AA:BB:CC:DD:EE:FF", "")
	if err != nil {
		t.Fatalf("signed upgrade: %v", err)
	}
	if !cache.delVerify {
		t.Fatal("expected anonymous replay to be invalidated")
	}
	if result.Code == cached.Code || result.TempClientID == cached.TempClientID {
		t.Fatalf("signed report reused anonymous replay: %+v", result)
	}
}

// ── Token tests ─────────────────────────────────────────────────────────────────

func TestDeviceService_Token_MissingFields(t *testing.T) {
	svc := NewDeviceService(&fakeDeviceStore{}, &fakeCacheForDevice{}, "secret", DefaultServiceConfig())
	_, err := svc.Token(context.Background(), "", "123", "nonce", "sig", "")
	if !errors.Is(err, ErrSigFail) {
		t.Errorf("want ErrSigFail, got %v", err)
	}
	if !errors.Is(err, ErrSigFieldsMissing) {
		t.Errorf("want ErrSigFieldsMissing, got %v", err)
	}
}

func TestDeviceService_Token_TimestampErrorDetails(t *testing.T) {
	svc := NewDeviceService(
		&fakeDeviceStore{}, &fakeCacheForDevice{}, "secret",
		DefaultServiceConfig())
	tests := []struct {
		name      string
		timestamp string
		want      error
	}{
		{"invalid", "not-unix-seconds", ErrSigTimestampInvalid},
		{
			"too old",
			strconv.FormatInt(time.Now().Add(-6*time.Minute).Unix(), 10),
			ErrSigTimestampTooOld,
		},
		{
			"too new",
			strconv.FormatInt(time.Now().Add(6*time.Minute).Unix(), 10),
			ErrSigTimestampTooNew,
		},
		{
			"minimum int64 is safely rejected as too old",
			strconv.FormatInt(-1<<63, 10),
			ErrSigTimestampTooOld,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Token(
				context.Background(), "dev-001", tt.timestamp,
				"abcdef1234567890", "sig", "")
			if !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
			if !errors.Is(err, ErrSigFail) {
				t.Fatalf("timestamp error must remain code 6008: %v", err)
			}
			if tt.want != ErrSigTimestampInvalid && !errors.Is(err, ErrSigTimestampSkew) {
				t.Fatalf("time offset detail must retain ErrSigTimestampSkew: %v", err)
			}
		})
	}
}

func TestDeviceService_Report_MinimumTimestampSafelyRejected(t *testing.T) {
	timestamp := strconv.FormatInt(-1<<63, 10)
	hdr := signedHdr(
		"dev-001",
		timestamp,
		"abcdef1234567890",
		"signature-not-evaluated-before-time-check",
	)
	svc := NewDeviceService(
		&fakeDeviceStore{}, &fakeCacheForDevice{}, "secret",
		DefaultServiceConfig())

	_, err := svc.Report(
		context.Background(),
		testIP,
		hdr,
		"AA:BB:CC:DD:EE:FF",
		"",
	)
	if !errors.Is(err, ErrSigTimestampTooOld) || !errors.Is(err, ErrSigFail) {
		t.Fatalf("minimum int64 timestamp must be safely rejected: %v", err)
	}
}

func TestDeviceService_Token_DeviceReset(t *testing.T) {
	svc := NewDeviceService(
		&fakeDeviceStore{
			deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
			bindByID:  &model.DeviceBind{DeviceID: "dev-001", UserID: 0},
		},
		&fakeCacheForDevice{nonceOK: true},
		"secret",
		DefaultServiceConfig(),
	)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "abcdef1234567890"
	sig := computeHMAC("key-xyz", "dev-001"+ts+nonce)
	_, err := svc.Token(context.Background(), "dev-001", ts, nonce, sig, "")
	if !errors.Is(err, ErrDeviceReset) {
		t.Errorf("want ErrDeviceReset, got %v", err)
	}
}

// 需求9(token 侧): 同用户名下同 mac 已绑其它 device_id → ErrMACDuplicateBinding
func TestDeviceService_Token_SameUserMACDuplicate(t *testing.T) {
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
		bindByID:  &model.DeviceBind{DeviceID: "dev-001", UserID: 1, MAC: "AA:AA"},
		bindByFP:  &model.DeviceBind{DeviceID: "dev-OTHER", UserID: 1, MAC: "AA:AA"},
	}
	svc := NewDeviceService(devStore, &fakeCacheForDevice{nonceOK: true}, "secret", DefaultServiceConfig())
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "tokdup1"
	sig := computeHMAC("key-xyz", "dev-001"+ts+nonce)
	_, err := svc.Token(context.Background(), "dev-001", ts, nonce, sig, "AA:AA")
	if !errors.Is(err, ErrMACDuplicateBinding) {
		t.Errorf("want ErrMACDuplicateBinding, got %v", err)
	}
}

// 同 mac 仅绑本 device_id → 放行
func TestDeviceService_Token_SameMACOnlySelf_OK(t *testing.T) {
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
		bindByID:  &model.DeviceBind{DeviceID: "dev-001", UserID: 1, MAC: "AA:AA"},
		bindByFP:  &model.DeviceBind{DeviceID: "dev-001", UserID: 1, MAC: "AA:AA"},
	}
	svc := NewDeviceService(devStore, &fakeCacheForDevice{nonceOK: true}, "secret", DefaultServiceConfig())
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "tokself1"
	sig := computeHMAC("key-xyz", "dev-001"+ts+nonce)
	_, err := svc.Token(context.Background(), "dev-001", ts, nonce, sig, "AA:AA")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

// 不带 X-MAC → 跳过同用户校验，放行
func TestDeviceService_Token_NoMAC_SkipsCheck(t *testing.T) {
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
		bindByID:  &model.DeviceBind{DeviceID: "dev-001", UserID: 1, MAC: "AA:AA"},
	}
	svc := NewDeviceService(devStore, &fakeCacheForDevice{nonceOK: true}, "secret", DefaultServiceConfig())
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "toknomac1"
	sig := computeHMAC("key-xyz", "dev-001"+ts+nonce)
	_, err := svc.Token(context.Background(), "dev-001", ts, nonce, sig, "")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

// 需求3(token 侧): device_id 已绑 MAC=F2，Token 带 X-MAC=F3 → ErrFingerprintMismatch
func TestDeviceService_Token_MACConflict(t *testing.T) {
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
		bindByID:  &model.DeviceBind{DeviceID: "dev-001", UserID: 1, MAC: "AA:BB:CC:DD:EE:F2"},
	}
	svc := NewDeviceService(devStore, &fakeCacheForDevice{nonceOK: true}, "secret", DefaultServiceConfig())
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "tokmacconf"
	sig := computeHMAC("key-xyz", "dev-001"+ts+nonce)
	_, err := svc.Token(context.Background(), "dev-001", ts, nonce, sig, "AA:BB:CC:DD:EE:F3")
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Errorf("want ErrFingerprintMismatch, got %v", err)
	}
}

// ── Task 5: MAC lifetime-binding tests ────────────────────────────────────────────

// 需求3: device_id 已绑 MAC=A，signed report 带 MAC=B → ErrFingerprintMismatch
func TestDeviceService_Report_Signed_MACConflict(t *testing.T) {
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
		bindByID:  &model.DeviceBind{DeviceID: "dev-001", UserID: 1, MAC: "AA:AA"},
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "mac-conflict-1"
	sig := computeHMAC("key-xyz", "dev-001"+ts+nonce)
	hdr := signedHdr("dev-001", ts, nonce, sig)
	svc := NewDeviceService(devStore, &fakeCacheForDevice{verifyOK: true, nonceOK: true}, "secret", DefaultServiceConfig())
	_, err := svc.Report(context.Background(), testIP, hdr, "BB:BB", "")
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Errorf("want ErrFingerprintMismatch, got %v", err)
	}
}

// 已绑 MAC=A，signed report 仍带 MAC=A → 放行
func TestDeviceService_Report_Signed_SameMAC_OK(t *testing.T) {
	devStore := &fakeDeviceStore{
		deviceKey: &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-xyz"},
		bindByID:  &model.DeviceBind{DeviceID: "dev-001", UserID: 1, MAC: "AA:AA"},
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "mac-same-1"
	sig := computeHMAC("key-xyz", "dev-001"+ts+nonce)
	hdr := signedHdr("dev-001", ts, nonce, sig)
	svc := NewDeviceService(devStore, &fakeCacheForDevice{verifyOK: true, nonceOK: true}, "secret", DefaultServiceConfig())
	res, err := svc.Report(context.Background(), testIP, hdr, "AA:AA", "")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if res.Code == "" {
		t.Error("want non-empty Code")
	}
}
