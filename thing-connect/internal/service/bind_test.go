package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"thing-connect/internal/model"
	"thing-connect/internal/mqttc"
	"thing-connect/internal/store"
)

// ── fakeBindStore ────────────────────────────────────────────────────────────

type fakeBindStore struct {
	bindByFP       *model.DeviceBind // returned by GetBindByFingerprint
	bindByID       *model.DeviceBind // returned by GetBindByDeviceID
	devKey         *model.DevicePool
	claimErr       error
	claimedFP      model.Fingerprint
	cleanupTargets []string
}

func (f *fakeBindStore) GetBindByFingerprint(_ context.Context, _ string, _ int64) (*model.DeviceBind, error) {
	return f.bindByFP, nil
}
func (f *fakeBindStore) GetBindByDeviceID(_ context.Context, _ string) (*model.DeviceBind, error) {
	return f.bindByID, nil
}
func (f *fakeBindStore) CommitBindFromPool(_ context.Context, _ model.Fingerprint, _ int64) (string, error) {
	if f.claimErr != nil {
		return "", f.claimErr
	}
	return "new-dev-001", nil
}
func (f *fakeBindStore) CommitBindByDeviceID(_ context.Context, _ string, _ model.Fingerprint, _ int64) error {
	return f.claimErr
}
func (f *fakeBindStore) CommitClaim(_ context.Context, _ string, fp model.Fingerprint, _ int64) error {
	f.claimedFP = fp
	return f.claimErr
}
func (f *fakeBindStore) TouchRebind(_ context.Context, _ string, _ int64) error  { return nil }
func (f *fakeBindStore) CommitUnbind(_ context.Context, _ string, _ int64) error { return f.claimErr }
func (f *fakeBindStore) CommitUnbindWithCleanup(_ context.Context, _ string, _ int64, targets []string) error {
	f.cleanupTargets = append([]string(nil), targets...)
	return f.claimErr
}
func (f *fakeBindStore) GetDeviceKey(_ context.Context, _ string) (*model.DevicePool, error) {
	return f.devKey, nil
}
func (f *fakeBindStore) GetUserDevices(_ context.Context, _ int64) ([]model.DeviceBind, error) {
	return nil, nil
}

// ── fakeCache ────────────────────────────────────────────────────────────────

type fakeCache3 struct {
	code     string
	online   bool
	deviceID string // populated to simulate device_id in verify record
	mac      string
	chipUID  string
	rand     string

	delVerifyAndCodeCalled bool

	// BindByDeviceID front-gate fields
	reportFP    string // GetReportFingerprint return value ("" → gate fails)
	pendingBind string // GetPendingBind return value ("" → gate fails)
}

func newFakeCache3(code, mac, chipUID, rand string, online bool) *fakeCache3 {
	return &fakeCache3{
		code: code, mac: mac, chipUID: chipUID, rand: rand, online: online,
		reportFP:    "dummy_fp",     // gate passes by default
		pendingBind: "dummy_client", // gate passes by default
	}
}

func (f *fakeCache3) codeHash() string { return "hash01" }
func (f *fakeCache3) verifyBytes() []byte {
	pid := "mac_" + f.mac
	val, _ := json.Marshal(map[string]string{
		"code": f.code, "temp_token": "tok", "physical_id": pid, "device_id": f.deviceID,
	})
	return val
}
func (f *fakeCache3) IncrReportAttempt(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 1, nil
}
func (f *fakeCache3) SetReportReplay(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}
func (f *fakeCache3) GetReportReplay(_ context.Context, _ string) ([]byte, error) { return nil, nil }
func (f *fakeCache3) SetVerifyRecord(_ context.Context, _ string, _ []byte, _ time.Duration) (bool, error) {
	return true, nil
}
func (f *fakeCache3) GetVerifyRecord(_ context.Context, ph string) ([]byte, error) {
	if ph == f.codeHash() {
		return f.verifyBytes(), nil
	}
	return nil, nil
}
func (f *fakeCache3) ReserveDeviceCode(_ context.Context, _, _ string, _ time.Duration) (bool, error) {
	return true, nil
}
func (f *fakeCache3) GetDeviceCodeLookup(_ context.Context, code string) (string, error) {
	if code == f.code {
		return f.codeHash(), nil
	}
	return "", nil
}
func (f *fakeCache3) DelDeviceCodeLookup(_ context.Context, _ string) error { return nil }
func (f *fakeCache3) SetEmailCode(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}
func (f *fakeCache3) GetEmailCode(_ context.Context, _ string) (string, error) { return "", nil }
func (f *fakeCache3) ConsumeEmailCode(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}
func (f *fakeCache3) IsDeviceOnline(_ context.Context, _ string) (bool, error) {
	return f.online, nil
}
func (f *fakeCache3) IsInCall(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (f *fakeCache3) DelVerifyAndCode(_ context.Context, _, _ string) error {
	f.delVerifyAndCodeCalled = true
	return nil
}
func (f *fakeCache3) DelEmailCode(_ context.Context, _ string) error { return nil }
func (f *fakeCache3) IncrPasswordResetAttempt(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 1, nil
}
func (f *fakeCache3) SetNonce(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

// ── fakeMQTT ─────────────────────────────────────────────────────────────────

// fakeMQTT simulates a device that never acks — PublishAndWaitACK behaves
// like mqttc.Broker on a real timeout.
type fakeMQTT struct {
	ackErr        error
	kickedClients []string
}

func (f *fakeMQTT) Publish(_ string, _ byte, _ any) error { return nil }
func (f *fakeMQTT) PublishAndWaitACK(_, _ string, _ any, _ time.Duration) error {
	return f.ackErr
}
func (f *fakeMQTT) KickClient(clientID string) {
	f.kickedClients = append(f.kickedClients, clientID)
}

// compile-time interface checks
var _ store.BindStore = (*fakeBindStore)(nil)
var _ store.CacheStore = (*fakeCache3)(nil)
var _ MQTTPublisher = (*fakeMQTT)(nil)

// ── helpers ──────────────────────────────────────────────────────────────────

func newBindSvc(bs *fakeBindStore, cache *fakeCache3) *BindService {
	return NewBindService(bs, cache, nil, DefaultServiceConfig())
}

// ── Scan (no device_id) tests ─────────────────────────────────────────────

// Case B: own device still bound → re-deliver, no quota cost
func TestBind_CaseB_SelfRedeliver(t *testing.T) {
	bs := &fakeBindStore{
		bindByFP: &model.DeviceBind{DeviceID: "dev-001", UserID: 1},
		devKey:   &model.DevicePool{DeviceID: "dev-001", DeviceKey: "key-001"},
	}
	cache := newFakeCache3("123456", "AA:BB", "", "", true)
	svc := newBindSvc(bs, cache)
	id, err := svc.Bind(context.Background(), 1, "123456")
	if err != nil {
		t.Fatalf("Case B: want nil, got %v", err)
	}
	if id != "dev-001" {
		t.Errorf("Case B: want dev-001, got %s", id)
	}
}

// Case D: own device unbound (last_user_id == self) → re-claim
func TestBind_CaseD_SelfReclaim(t *testing.T) {
	bs := &fakeBindStore{
		bindByFP: &model.DeviceBind{DeviceID: "dev-002", UserID: 0, LastUserID: 1},
		devKey:   &model.DevicePool{DeviceID: "dev-002", DeviceKey: "key-002"},
	}
	cache := newFakeCache3("123456", "AA:CC", "", "", true)
	svc := newBindSvc(bs, cache)
	id, err := svc.Bind(context.Background(), 1, "123456")
	if err != nil {
		t.Fatalf("Case D: want nil, got %v", err)
	}
	if id != "dev-002" {
		t.Errorf("Case D: want dev-002, got %s", id)
	}
}

// Case A: no fingerprint match at all → new device from pool
func TestBind_CaseA_NewFromPool(t *testing.T) {
	bs := &fakeBindStore{
		bindByFP: nil,
		devKey:   &model.DevicePool{DeviceID: "new-dev-001", DeviceKey: "key-new"},
	}
	cache := newFakeCache3("123456", "BB:CC", "", "", true)
	svc := newBindSvc(bs, cache)
	id, err := svc.Bind(context.Background(), 2, "123456")
	if err != nil {
		t.Fatalf("Case A: want nil, got %v", err)
	}
	if id != "new-dev-001" {
		t.Errorf("Case A: want new-dev-001, got %s", id)
	}
}

// Case A: fingerprint matches a row owned by someone else → still issue new device
func TestBind_CaseA_OtherBound_IssueNew(t *testing.T) {
	bs := &fakeBindStore{
		bindByFP: &model.DeviceBind{DeviceID: "dev-other", UserID: 99}, // other user
		devKey:   &model.DevicePool{DeviceID: "new-dev-001", DeviceKey: "key-new"},
	}
	cache := newFakeCache3("123456", "BB:CC", "", "", true)
	svc := newBindSvc(bs, cache)
	id, err := svc.Bind(context.Background(), 2, "123456")
	if err != nil {
		t.Fatalf("Case A(other bound): want nil, got %v", err)
	}
	if id == "dev-other" {
		t.Error("Case A(other bound): must NOT return the other user's device_id")
	}
}

// Case A: ack never arrives → Bind must report failure, not fabricate success,
// and must leave the code/verify record intact so the user can retry.
func TestBind_CaseA_AckTimeout_ReportsFailure(t *testing.T) {
	bs := &fakeBindStore{
		bindByFP: nil,
		devKey:   &model.DevicePool{DeviceID: "new-dev-001", DeviceKey: "key-new"},
	}
	cache := newFakeCache3("123456", "BB:CC", "", "", true)
	mqtt := &fakeMQTT{ackErr: mqttc.ErrACKTimeout}
	svc := NewBindService(bs, cache, mqtt, DefaultServiceConfig())

	id, err := svc.Bind(context.Background(), 2, "123456")
	if !errors.Is(err, ErrMQTTTimeout) {
		t.Fatalf("want ErrMQTTTimeout, got id=%q err=%v", id, err)
	}
	if !errors.Is(err, ErrMQTTAckTimeout) {
		t.Fatalf("want actionable ErrMQTTAckTimeout, got %v", err)
	}
	if id != "" {
		t.Errorf("want empty device_id on failure, got %q", id)
	}
	if cache.delVerifyAndCodeCalled {
		t.Error("verify/code record must NOT be cleared on ack timeout — user needs to retry with the same code")
	}
	if len(mqtt.kickedClients) != 1 {
		t.Errorf("want temp client kicked exactly once so device can reconnect, got %v", mqtt.kickedClients)
	}
}

func TestBind_CaseA_MQTTPublishFailure_ReportsActionableFailure(t *testing.T) {
	bs := &fakeBindStore{
		bindByFP: nil,
		devKey:   &model.DevicePool{DeviceID: "new-dev-001", DeviceKey: "key-new"},
	}
	cache := newFakeCache3("123456", "BB:CC", "", "", true)
	mqtt := &fakeMQTT{ackErr: mqttc.ErrPublishFailed}
	svc := NewBindService(bs, cache, mqtt, DefaultServiceConfig())

	_, err := svc.Bind(context.Background(), 2, "123456")
	if !errors.Is(err, ErrMQTTPublishFailed) || !errors.Is(err, ErrMQTTTimeout) {
		t.Fatalf("want ErrMQTTPublishFailed retaining ErrMQTTTimeout, got %v", err)
	}
}

// ErrEmptyFingerprint: all three fields blank
func TestBind_EmptyFingerprint(t *testing.T) {
	bs := &fakeBindStore{}
	// all fields empty
	cache := newFakeCache3("123456", "", "", "", true)
	svc := newBindSvc(bs, cache)
	_, err := svc.Bind(context.Background(), 1, "123456")
	if !errors.Is(err, ErrEmptyFingerprint) {
		t.Errorf("want ErrEmptyFingerprint, got %v", err)
	}
}

// ErrCodeExpired
func TestBind_CodeExpired(t *testing.T) {
	bs := &fakeBindStore{}
	cache := &fakeCache3{} // empty → GetDeviceCodeLookup returns ""
	svc := newBindSvc(bs, cache)
	_, err := svc.Bind(context.Background(), 1, "000000")
	if !errors.Is(err, ErrCodeExpired) {
		t.Errorf("want ErrCodeExpired, got %v", err)
	}
}

// ── BindByDeviceID tests ──────────────────────────────────────────────────

// Case E: new pre-flashed device
func TestBindByDeviceID_CaseE_FirstBind(t *testing.T) {
	bs := &fakeBindStore{bindByID: nil, devKey: &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"}} // no existing record
	svc := newBindSvc(bs, &fakeCache3{reportFP: "dummy", pendingBind: "dummy", online: true})
	err := svc.BindByDeviceID(context.Background(), 1, "dev-E", model.Fingerprint{MAC: "CC:DD"})
	if err != nil {
		t.Fatalf("Case E: want nil, got %v", err)
	}
}

// Case F: same user re-binds, fingerprint matches
func TestBindByDeviceID_CaseF_SameUserRebind(t *testing.T) {
	bs := &fakeBindStore{
		bindByID: &model.DeviceBind{DeviceID: "dev-F", UserID: 1, MAC: "CC:DD"},
		devKey:   &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"},
	}
	svc := newBindSvc(bs, &fakeCache3{reportFP: "dummy", pendingBind: "dummy", online: true})
	err := svc.BindByDeviceID(context.Background(), 1, "dev-F", model.Fingerprint{MAC: "CC:DD"})
	if err != nil {
		t.Fatalf("Case F: want nil, got %v", err)
	}
}

// Case F-clone: same user, fingerprint mismatch
func TestBindByDeviceID_CaseF_Clone(t *testing.T) {
	bs := &fakeBindStore{
		bindByID: &model.DeviceBind{DeviceID: "dev-F2", UserID: 1, MAC: "CC:DD"},
		devKey:   &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"},
	}
	svc := newBindSvc(bs, &fakeCache3{reportFP: "dummy", pendingBind: "dummy", online: true})
	err := svc.BindByDeviceID(context.Background(), 1, "dev-F2", model.Fingerprint{MAC: "EE:FF"})
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Errorf("Case F clone: want ErrFingerprintMismatch, got %v", err)
	}
}

// Case G: 取消换板豁免——stored MAC 非空 + 上报不同 MAC → ErrFingerprintMismatch
func TestBindByDeviceID_CaseG_MACChange_Rejected(t *testing.T) {
	bs := &fakeBindStore{
		bindByID: &model.DeviceBind{DeviceID: "dev-G", UserID: 0, LastUserID: 99, MAC: "OLD:MAC"},
		devKey:   &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"},
	}
	svc := newBindSvc(bs, &fakeCache3{reportFP: "dummy", pendingBind: "dummy", online: true})
	err := svc.BindByDeviceID(context.Background(), 2, "dev-G", model.Fingerprint{MAC: "NEW:MAC"})
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Errorf("Case G mac change: want ErrFingerprintMismatch, got %v", err)
	}
}

// Case H: owned by another user, fingerprint matches → ErrBoundByOther
func TestBindByDeviceID_CaseH_BoundByOther(t *testing.T) {
	bs := &fakeBindStore{
		bindByID: &model.DeviceBind{DeviceID: "dev-H", UserID: 99, MAC: "CC:DD"},
		devKey:   &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"},
	}
	svc := newBindSvc(bs, &fakeCache3{reportFP: "dummy", pendingBind: "dummy", online: true})
	err := svc.BindByDeviceID(context.Background(), 2, "dev-H", model.Fingerprint{MAC: "CC:DD"})
	if !errors.Is(err, ErrBoundByOther) {
		t.Errorf("Case H: want ErrBoundByOther, got %v", err)
	}
}

// Case H-clone: owned by another, fingerprint mismatch → ErrCloneConflict
func TestBindByDeviceID_CaseH_Clone(t *testing.T) {
	bs := &fakeBindStore{
		bindByID: &model.DeviceBind{DeviceID: "dev-H2", UserID: 99, MAC: "CC:DD"},
		devKey:   &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"},
	}
	svc := newBindSvc(bs, &fakeCache3{reportFP: "dummy", pendingBind: "dummy", online: true})
	err := svc.BindByDeviceID(context.Background(), 2, "dev-H2", model.Fingerprint{MAC: "XX:YY"})
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Errorf("Case H clone: want ErrFingerprintMismatch, got %v", err)
	}
}

// 需求9(bind 侧): 同用户名下同 mac 已绑其它 device_id → ErrMACDuplicateBinding
func TestBindByDeviceID_CaseE_SameUserMACDuplicate(t *testing.T) {
	bs := &fakeBindStore{
		bindByID: nil,                                                               // dev-E 无记录（Case E）
		bindByFP: &model.DeviceBind{DeviceID: "dev-OTHER", UserID: 1, MAC: "CC:DD"}, // 同用户已绑同 mac 的另一个 device_id
		devKey:   &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"},
	}
	svc := newBindSvc(bs, &fakeCache3{reportFP: "dummy", pendingBind: "dummy", online: true})
	err := svc.BindByDeviceID(context.Background(), 1, "dev-E", model.Fingerprint{MAC: "CC:DD"})
	if !errors.Is(err, ErrMACDuplicateBinding) {
		t.Errorf("Case E dup mac: want ErrMACDuplicateBinding, got %v", err)
	}
}

// Case F: stored MAC 空（历史数据）+ 上报 MAC → 不报克隆冲突
func TestBindByDeviceID_CaseF_EmptyStoredMAC_OK(t *testing.T) {
	bs := &fakeBindStore{
		bindByID: &model.DeviceBind{DeviceID: "dev-F4", UserID: 1, MAC: ""},
		bindByFP: nil,
		devKey:   &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"},
	}
	svc := newBindSvc(bs, &fakeCache3{}) // Case F 不需 online proof
	err := svc.BindByDeviceID(context.Background(), 1, "dev-F4", model.Fingerprint{MAC: "CC:DD"})
	if err != nil {
		t.Fatalf("Case F empty stored mac: want nil, got %v", err)
	}
}

// ── Online-proof gate tests (Case E/G require it, F/H don't) ───────────────

// Case E without a signed report on file → rejected, no device_pool exploit.
func TestBindByDeviceID_CaseE_NoReportFP_Rejected(t *testing.T) {
	bs := &fakeBindStore{bindByID: nil, devKey: &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"}}
	svc := newBindSvc(bs, &fakeCache3{}) // reportFP="" → gate fails
	err := svc.BindByDeviceID(context.Background(), 1, "dev-E2", model.Fingerprint{MAC: "CC:DD"})
	if !errors.Is(err, ErrDeviceOffline) {
		t.Errorf("Case E no report_fp: want ErrDeviceOffline, got %v", err)
	}
	if !errors.Is(err, ErrDeviceReportProofMissing) {
		t.Errorf("Case E no report_fp: want ErrDeviceReportProofMissing, got %v", err)
	}
}

func TestBindByDeviceID_CaseE_NoPendingBind_Rejected(t *testing.T) {
	bs := &fakeBindStore{bindByID: nil, devKey: &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"}}
	svc := newBindSvc(bs, &fakeCache3{reportFP: "dummy"})
	err := svc.BindByDeviceID(context.Background(), 1, "dev-E-pending", model.Fingerprint{MAC: "CC:DD"})
	if !errors.Is(err, ErrDevicePendingBindMissing) || !errors.Is(err, ErrDeviceOffline) {
		t.Fatalf("want ErrDevicePendingBindMissing retaining ErrDeviceOffline, got %v", err)
	}
}

// Case E with report_fp but device not currently online → rejected.
func TestBindByDeviceID_CaseE_ReportFPButOffline_Rejected(t *testing.T) {
	bs := &fakeBindStore{bindByID: nil, devKey: &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"}}
	svc := newBindSvc(bs, &fakeCache3{reportFP: "dummy", pendingBind: "dummy", online: false})
	err := svc.BindByDeviceID(context.Background(), 1, "dev-E3", model.Fingerprint{MAC: "CC:DD"})
	if !errors.Is(err, ErrDeviceOffline) {
		t.Errorf("Case E offline: want ErrDeviceOffline, got %v", err)
	}
	if !errors.Is(err, ErrDeviceMQTTOffline) {
		t.Errorf("Case E offline: want ErrDeviceMQTTOffline, got %v", err)
	}
}

// Case G without online proof → rejected (same exploit risk as Case E).
func TestBindByDeviceID_CaseG_NoOnlineProof_Rejected(t *testing.T) {
	bs := &fakeBindStore{
		bindByID: &model.DeviceBind{DeviceID: "dev-G2", UserID: 0, LastUserID: 99, MAC: "OLD:MAC"},
		devKey:   &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"},
	}
	svc := newBindSvc(bs, &fakeCache3{}) // reportFP="" → gate fails
	err := svc.BindByDeviceID(context.Background(), 2, "dev-G2", model.Fingerprint{MAC: "NEW:MAC"})
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Errorf("Case G no online proof: want ErrFingerprintMismatch, got %v", err)
	}
}

// Case F does NOT require online proof — already-owned device, protected by
// fingerprint match instead. Regression guard for the gate being too broad.
func TestBindByDeviceID_CaseF_NoOnlineProof_StillSucceeds(t *testing.T) {
	bs := &fakeBindStore{
		bindByID: &model.DeviceBind{DeviceID: "dev-F3", UserID: 1, MAC: "CC:DD"},
		devKey:   &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"},
	}
	svc := newBindSvc(bs, &fakeCache3{}) // reportFP="" — gate would fail if applied here
	err := svc.BindByDeviceID(context.Background(), 1, "dev-F3", model.Fingerprint{MAC: "CC:DD"})
	if err != nil {
		t.Fatalf("Case F should not require online proof: got %v", err)
	}
}

func TestBindByDeviceID_CaseG_EmptyRequestPreservesFingerprint(t *testing.T) {
	stored := model.Fingerprint{MAC: "AA:BB", ChipUID: "chip", DeviceRand: "rand"}
	bs := &fakeBindStore{
		bindByID: &model.DeviceBind{
			DeviceID: "dev-G3", UserID: 0, MAC: stored.MAC,
			ChipUID: stored.ChipUID, DeviceRand: stored.DeviceRand,
		},
		devKey: &model.DevicePool{DeviceID: "dev-G3", DeviceKey: "key"},
	}
	svc := newBindSvc(bs, &fakeCache3{reportFP: "proof", pendingBind: "tmp", online: true})
	if err := svc.BindByDeviceID(context.Background(), 2, "dev-G3", model.Fingerprint{}); err != nil {
		t.Fatalf("BindByDeviceID: %v", err)
	}
	if bs.claimedFP != stored {
		t.Fatalf("claimed fingerprint = %+v, want %+v", bs.claimedFP, stored)
	}
}

// Case H does NOT require online proof — protected by ownership check instead.
func TestBindByDeviceID_CaseH_NoOnlineProof_StillRejectsCorrectly(t *testing.T) {
	bs := &fakeBindStore{
		bindByID: &model.DeviceBind{DeviceID: "dev-H3", UserID: 99, MAC: "CC:DD"},
		devKey:   &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"},
	}
	svc := newBindSvc(bs, &fakeCache3{}) // reportFP="" — must not surface as ErrDeviceOffline
	err := svc.BindByDeviceID(context.Background(), 2, "dev-H3", model.Fingerprint{MAC: "CC:DD"})
	if !errors.Is(err, ErrBoundByOther) {
		t.Errorf("Case H should reject via ownership check, not gate: want ErrBoundByOther, got %v", err)
	}
}

// Reset: device not found
func TestReset_NotFound(t *testing.T) {
	bs := &fakeBindStore{bindByID: nil, devKey: &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"}}
	svc := newBindSvc(bs, &fakeCache3{reportFP: "dummy", pendingBind: "dummy", online: true})
	err := svc.Reset(context.Background(), "dev-999", 1)
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("Reset not found: want ErrDeviceNotFound, got %v", err)
	}
}

// Reset: device not owned by this user
func TestReset_NotOwned(t *testing.T) {
	bs := &fakeBindStore{
		bindByID: &model.DeviceBind{DeviceID: "dev-R", UserID: 99},
		devKey:   &model.DevicePool{DeviceID: "test-dev", DeviceKey: "key-xxx"},
	}
	svc := newBindSvc(bs, &fakeCache3{reportFP: "dummy", pendingBind: "dummy", online: true})
	err := svc.Reset(context.Background(), "dev-R", 1)
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("Reset not owned: want ErrDeviceNotFound, got %v", err)
	}
}

func TestResetWithCleanup_PersistsTargetsWithUnbind(t *testing.T) {
	bs := &fakeBindStore{bindByID: &model.DeviceBind{DeviceID: "dev-R", UserID: 1}}
	svc := newBindSvc(bs, &fakeCache3{online: true})
	if err := svc.ResetWithCleanup(context.Background(), "dev-R", 1, []string{"ai", "voip", "call"}); err != nil {
		t.Fatalf("ResetWithCleanup: %v", err)
	}
	if got, want := bs.cleanupTargets, []string{"ai", "voip", "call"}; len(got) != len(want) {
		t.Fatalf("cleanup targets = %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("cleanup targets = %v, want %v", got, want)
			}
		}
	}
}

func (f *fakeCache3) SetPendingBind(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}
func (f *fakeCache3) GetPendingBind(_ context.Context, _ string) (string, error) {
	return f.pendingBind, nil
}
func (f *fakeCache3) DelPendingBind(_ context.Context, _ string) error { return nil }

func (f *fakeCache3) AddIPFingerprint(_ context.Context, _, _ string, _ time.Duration) (bool, int64, error) {
	return true, 1, nil
}
func (f *fakeCache3) IncrGlobalPending(_ context.Context) (int64, error) { return 1, nil }
func (f *fakeCache3) DecrGlobalPending(_ context.Context) error          { return nil }
func (f *fakeCache3) ReconcileGlobalPending(_ context.Context) error     { return nil }

func (f *fakeCache3) SetReportFingerprint(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}
func (f *fakeCache3) GetReportFingerprint(_ context.Context, _ string) (string, error) {
	return f.reportFP, nil
}
func (f *fakeCache3) DelReportFingerprint(_ context.Context, _ string) error { return nil }
