package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"thing-connect/internal/model"
	"thing-connect/internal/mqttc"
	"thing-connect/internal/physid"
	"thing-connect/internal/store"
)

// MQTTPublisher is the minimal MQTT interface needed by BindService.
// *mqttc.Broker satisfies this interface.
type MQTTPublisher interface {
	Publish(topic string, qos byte, payload any) error
	PublishAndWaitACK(downTopic, ackTopic string, payload any, timeout time.Duration) error
	KickClient(clientID string)
}

// BindService handles device binding and reset.
type BindService struct {
	bind  store.BindStore
	cache store.CacheStore
	mqtt  MQTTPublisher // nil = no MQTT (test / offline mode)
	cfg   ServiceConfig
}

func NewBindService(bind store.BindStore, cache store.CacheStore, mqtt MQTTPublisher, cfg ServiceConfig) *BindService {
	return &BindService{bind: bind, cache: cache, mqtt: mqtt, cfg: cfg}
}

// Bind processes a scan-code flow (no device_id).
// Implements cases B, D, A from design §3.1.
func (s *BindService) Bind(ctx context.Context, userID int64, code string) (string, error) {
	// Step 1: code → physHash
	physHash, err := s.cache.GetDeviceCodeLookup(ctx, code)
	if err != nil {
		return "", fmt.Errorf("service.Bind GetDeviceCodeLookup: %w", err)
	}
	if physHash == "" {
		return "", ErrCodeExpired
	}

	// Step 2: physHash → verify record
	raw, err := s.cache.GetVerifyRecord(ctx, physHash)
	if err != nil {
		return "", fmt.Errorf("service.Bind GetVerifyRecord: %w", err)
	}
	if raw == nil {
		return "", ErrCodeExpired
	}
	var vdata struct {
		Code         string `json:"code"`
		TempToken    string `json:"temp_token"`
		PhysicalID   string `json:"physical_id"`
		DeviceID     string `json:"device_id"`      // set only for pre-flashed devices
		TempClientID string `json:"temp_client_id"` // mac_{MAC} or mac_{code}
	}
	if err := json.Unmarshal(raw, &vdata); err != nil {
		return "", fmt.Errorf("service.Bind unmarshal verify: %w", err)
	}
	if vdata.Code != code {
		return "", ErrCodeExpired
	}
	// TRACE: log full verify record to see actual device_id and physical_id
	slog.InfoContext(ctx, "Bind verify record trace",
		"code", code, "vdata.DeviceID", vdata.DeviceID, "vdata.PhysicalID", vdata.PhysicalID,
		"vdata.TempClientID", vdata.TempClientID, "physHash", physHash)

	mac := physid.Parse(vdata.PhysicalID)

	fp := model.Fingerprint{MAC: mac}
	if fp.IsEmpty() {
		return "", ErrEmptyFingerprint
	}

	// Pre-burned device: verify record carries a specific device_id.
	// Call bindByDeviceIDNoNotify to skip the normal auth_grant (which would send full credentials),
	// then send auth_grant with empty payload so the device uses its local Flash credentials.
	if vdata.DeviceID != "" {
		// device_id↔mac 终身绑定：扫码 pre-burned 路径不经过 bindByDeviceIDInternal 的前置校验，
		// 需在此处独立校验。br==nil 时为首次预烧绑定，放行。
		if br, err := s.bind.GetBindByDeviceID(ctx, vdata.DeviceID); err != nil {
			return "", fmt.Errorf("service.Bind pre-burned GetBindByDeviceID: %w", err)
		} else if br != nil && macConflicts(br.MAC, fp.MAC) {
			return "", ErrFingerprintMismatch
		}
		slog.InfoContext(ctx, "bind pre-burned",
			"device_id", vdata.DeviceID, "mac", fp.MAC, "user", userID)
		if err := s.bindByDeviceIDNoNotify(ctx, userID, vdata.DeviceID, fp); err != nil {
			slog.ErrorContext(ctx, "bind pre-burned failed", "device_id", vdata.DeviceID, "err", err)
			return "", err
		}
		// empty payload = pre-burned, use local credentials.
		// Only clear the verify/code cache entries once the device has actually
		// ack'd — on timeout the code stays valid within its TTL, so the user can
		// retry without re-Reporting (consistent with Case A below).
		ackErr := s.publishAndKick(ctx, vdata.TempClientID, "", "")
		if ackErr != nil {
			slog.WarnContext(ctx, "bind pre-burned bound in DB but ack failed", "device_id", vdata.DeviceID, "err", ackErr)
			return "", ackErr
		}
		s.cache.DelVerifyAndCode(ctx, physHash, code)     //nolint:errcheck
		s.cache.DelPendingBind(ctx, vdata.DeviceID)       //nolint:errcheck
		s.cache.DelReportFingerprint(ctx, vdata.DeviceID) //nolint:errcheck
		slog.InfoContext(ctx, "bind pre-burned ok", "device_id", vdata.DeviceID, "auth_grant_to", vdata.TempClientID)
		slog.InfoContext(ctx, "Bind returning", "case", "pre-burned", "device_id", vdata.DeviceID)
		return vdata.DeviceID, nil
	}

	// Lookup by fingerprint
	row, err := s.bind.GetBindByFingerprint(ctx, fp.MAC, userID)
	if err != nil {
		return "", fmt.Errorf("service.Bind GetBindByFingerprint: %w", err)
	}

	// Case B: own active device — re-deliver
	if row != nil && row.UserID == userID {
		deviceID, err := s.redeliverCredentials(ctx, row.DeviceID, vdata.TempClientID)
		if err == nil {
			s.cache.DelVerifyAndCode(ctx, physHash, code) //nolint:errcheck
		}
		slog.InfoContext(ctx, "Bind returning", "case", "B", "device_id", deviceID)
		return deviceID, err
	}

	// Case D: own unowned device (last_user_id == self)
	if row != nil && row.UserID == 0 && row.LastUserID == userID {
		if err := s.bind.CommitClaim(ctx, row.DeviceID, fp, userID); err != nil {
			if errors.Is(err, store.ErrSlotConflict) {
				return "", ErrBoundByOther
			}
			return "", fmt.Errorf("service.Bind CommitClaim(D): %w", err)
		}
		deviceID, err := s.redeliverCredentials(ctx, row.DeviceID, vdata.TempClientID)
		if err == nil {
			s.cache.DelVerifyAndCode(ctx, physHash, code) //nolint:errcheck
		}
		return deviceID, err
	}

	// Case A: all other cases → new device from pool
	// (row == nil, or row.UserID != 0 != userID, or row.UserID==0 && last_user_id != userID)
	// tempClientID is mac_{MAC} for MAC-bearing devices, mac_{code} for MAC-less devices.
	online, err := s.cache.IsDeviceOnline(ctx, vdata.TempClientID)
	if err != nil {
		return "", fmt.Errorf("service.Bind IsDeviceOnline: %w", err)
	}
	if !online {
		return "", ErrDeviceMQTTOffline
	}

	newDevID, err := s.bind.CommitBindFromPool(ctx, fp, userID)
	if errors.Is(err, store.ErrMACAlreadyBound) {
		// (mac, user_id) is already bound — a concurrent or repeated bind of the
		// same MAC. CommitBindFromPool returned the existing device_id (newDevID)
		// without allocating a new one or consuming quota. Fall through to
		// GetDeviceKey + publishAndKick so the caller receives the one device_id
		// for this MAC (per-user MAC uniqueness — "永远只下发一个 device_id").
		slog.InfoContext(ctx, "Bind Case A MAC already bound, redelivering existing", "device_id", newDevID, "mac", fp.MAC)
	} else if err != nil {
		if errors.Is(err, store.ErrQuotaEmpty) {
			return "", ErrQuotaEmpty
		}
		if errors.Is(err, store.ErrPoolEmpty) {
			return "", ErrPoolExhausted
		}
		return "", fmt.Errorf("service.Bind CommitBindFromPool: %w", err)
	}

	gpool, err := s.bind.GetDeviceKey(ctx, newDevID)
	if err != nil || gpool == nil {
		return "", fmt.Errorf("service.Bind GetDeviceKey: %w", err)
	}

	// Only clear the verify/code cache entries once the device has actually
	// ack'd — on timeout the code stays valid within its TTL, so a user retry
	// re-resolves via GetBindByFingerprint into Case B (redeliver to the row
	// just committed above) instead of allocating a second device from pool.
	if err := s.publishAndKick(ctx, vdata.TempClientID, gpool.DeviceID, gpool.DeviceKey); err != nil {
		return "", err
	}
	s.cache.DelVerifyAndCode(ctx, physHash, code) //nolint:errcheck

	slog.InfoContext(ctx, "Bind returning", "case", "A", "device_id", gpool.DeviceID)
	return gpool.DeviceID, nil
}

// BindByDeviceID handles cases E, F, G, H from design §3.2.
// bindByDeviceIDNoNotify is like BindByDeviceID but skips the MQTT notification.
// Used by the pre-burned scan-code path where the caller handles the notification.
func (s *BindService) bindByDeviceIDNoNotify(ctx context.Context, userID int64, deviceID string, fp model.Fingerprint) error {
	return s.bindByDeviceIDInternal(ctx, userID, deviceID, fp, false)
}

func (s *BindService) BindByDeviceID(ctx context.Context, userID int64, deviceID string, fp model.Fingerprint) error {
	return s.bindByDeviceIDInternal(ctx, userID, deviceID, fp, true)
}

// requireDeviceOnline checks that deviceID has proven identity via a signed
// report and is currently online on its temp MQTT connection. Used only for
// cases E (first bind) and G (unowned/reclaim) — these are the only cases
// where a caller could bind an arbitrary device_id with a made-up fingerprint
// and no prior trust relationship to fall back on. Cases F (already the
// caller's own device) and H (owned by someone else) are protected by their
// own fingerprint/ownership checks and don't need this.
func (s *BindService) requireDeviceOnline(ctx context.Context, deviceID string) error {
	reportFP, err := s.cache.GetReportFingerprint(ctx, deviceID)
	if err != nil {
		return fmt.Errorf("service.requireDeviceOnline GetReportFingerprint: %w", err)
	}
	if reportFP == "" {
		return ErrDeviceReportProofMissing
	}
	tempClientID, err := s.cache.GetPendingBind(ctx, deviceID)
	if err != nil {
		return fmt.Errorf("service.requireDeviceOnline GetPendingBind: %w", err)
	}
	if tempClientID == "" {
		return ErrDevicePendingBindMissing
	}
	online, err := s.cache.IsDeviceOnline(ctx, tempClientID)
	if err != nil {
		return fmt.Errorf("service.requireDeviceOnline IsDeviceOnline: %w", err)
	}
	if !online {
		return ErrDeviceMQTTOffline
	}
	return nil
}

func (s *BindService) bindByDeviceIDInternal(ctx context.Context, userID int64, deviceID string, fp model.Fingerprint, notify bool) error {
	// Device must exist in device_pool — prevents binding arbitrary strings.
	pool, err := s.bind.GetDeviceKey(ctx, deviceID)
	if err != nil {
		return fmt.Errorf("service.BindByDeviceID GetDeviceKey: %w", err)
	}
	if pool == nil {
		return ErrDeviceNotFound
	}

	row, err := s.bind.GetBindByDeviceID(ctx, deviceID)
	if err != nil {
		return fmt.Errorf("service.BindByDeviceID GetBindByDeviceID: %w", err)
	}

	// 需求9: 同用户名下同 mac 不能关联多个 device_id（对 Case E/G 生效）
	if !fp.IsEmpty() {
		if dup, err := s.bind.GetBindByFingerprint(ctx, fp.MAC, userID); err != nil {
			return fmt.Errorf("service.BindByDeviceID dup-mac check: %w", err)
		} else if dup != nil && dup.UserID == userID && dup.DeviceID != deviceID {
			return ErrMACDuplicateBinding
		}
	}

	// device_id↔mac 终身绑定（需求2/3/5）：无论走什么 Case，stored MAC 与 reported MAC 冲突即拦。
	// 这一道防线独立于 Case 分支，也独立于 reportSigned 的校验——防止通过 reportUnsigned
	// 绕过 reportSigned 的持久化校验后直接调 BindByDeviceID。
	if row != nil && macConflicts(row.MAC, fp.MAC) {
		return ErrFingerprintMismatch
	}
	// Case E: no record yet — first bind of pre-flashed device.
	// No prior fingerprint on file to compare against, so this is the case an
	// attacker could exploit by guessing a device_id and making up a
	// fingerprint. Require proof the device is alive (signed report + online)
	// before trusting the claim — only for the direct API path (notify=true);
	// the scan-code path (notify=false) has its own code+verify-record gate.
	if row == nil {
		if notify {
			if err := s.requireDeviceOnline(ctx, deviceID); err != nil {
				return err
			}
		}
		if err := s.bind.CommitBindByDeviceID(ctx, deviceID, fp, userID); err != nil {
			if errors.Is(err, store.ErrQuotaEmpty) {
				return ErrQuotaEmpty
			}
			if errors.Is(err, store.ErrSlotConflict) {
				return ErrBoundByOther
			}
			return fmt.Errorf("service.BindByDeviceID CommitBindByDeviceID(E): %w", err)
		}
		// Use MAC from fingerprint; if empty the device has no temp MQTT connection to notify.
		if notify {
			s.notifyBindSuccess(ctx, deviceID, fp.MAC)
		}
		return nil
	}

	if row.UserID == userID {
		// Case F: own device re-bind — device is already online, no notification needed.
		if macConflicts(row.MAC, fp.MAC) {
			return ErrCloneConflict // iron law 1
		}
		return s.bind.TouchRebind(ctx, deviceID, userID)
	}

	if row.UserID == 0 {
		// Case G: unowned — claim (严格终身绑定：stored MAC 非空时禁止换 MAC).
		// Same exploit risk as Case E: no owner to validate the claim against,
		// so require online proof for the direct API path.
		if notify {
			if err := s.requireDeviceOnline(ctx, deviceID); err != nil {
				return err
			}
		}
		// 严格终身绑定（需求2/5）：stored MAC 非空时禁止换 MAC
		if macConflicts(row.MAC, fp.MAC) {
			return ErrFingerprintMismatch
		}
		// The public bind-by-id UI only submits device_id. Preserve the
		// lifetime-bound fingerprint instead of overwriting it with blanks.
		claimFP := fp
		if claimFP.MAC == "" {
			claimFP.MAC = row.MAC
		}
		if claimFP.ChipUID == "" {
			claimFP.ChipUID = row.ChipUID
		}
		if claimFP.DeviceRand == "" {
			claimFP.DeviceRand = row.DeviceRand
		}
		if err := s.bind.CommitClaim(ctx, deviceID, claimFP, userID); err != nil {
			if errors.Is(err, store.ErrQuotaEmpty) {
				return ErrQuotaEmpty
			}
			if errors.Is(err, store.ErrSlotConflict) {
				return ErrBoundByOther
			}
			return fmt.Errorf("service.BindByDeviceID CommitClaim(G): %w", err)
		}
		if notify {
			// Prefer fp.MAC (new board's MAC); fall back to old row.MAC if fp has no MAC.
			notifyMAC := fp.MAC
			if notifyMAC == "" {
				notifyMAC = row.MAC
			}
			s.notifyBindSuccess(ctx, deviceID, notifyMAC)
		}
		return nil
	}

	// Case H: owned by another user
	// macConflicts is the inverse of the old Matches for non-empty MACs:
	//   - MAC match (no conflict) → device legitimately belongs to other user → ErrBoundByOther
	//   - MAC differs (conflict) → suspected clone attempt → ErrCloneConflict
	if macConflicts(row.MAC, fp.MAC) {
		return ErrCloneConflict
	}
	return ErrBoundByOther
}

// Reset unbinds a device from the user.
func (s *BindService) Reset(ctx context.Context, deviceID string, userID int64) error {
	row, err := s.bind.GetBindByDeviceID(ctx, deviceID)
	if err != nil {
		return fmt.Errorf("service.Reset GetBindByDeviceID: %w", err)
	}
	if row == nil || row.UserID != userID {
		return ErrDeviceNotFound
	}
	if err := s.bind.CommitUnbind(ctx, deviceID, userID); err != nil {
		return fmt.Errorf("service.Reset CommitUnbind: %w", err)
	}
	if s.mqtt != nil {
		// 先通知设备已被解绑，让它清除本地凭证并主动断开。
		// 不等 ACK（设备可能已离线），best-effort。
		topic := "device/sn_" + deviceID + "/cmd"
		s.mqtt.Publish(topic, 1, map[string]any{"type": "unbind"}) //nolint:errcheck
		s.mqtt.KickClient("sn_" + deviceID)
	}
	return nil
}

// ExistsUserDevice checks whether a device belongs to the given user.
func (s *BindService) ExistsUserDevice(ctx context.Context, deviceID string, userID int64) (bool, error) {
	row, err := s.bind.GetBindByDeviceID(ctx, deviceID)
	if err != nil {
		return false, err
	}
	return row != nil && row.UserID == userID, nil
}

// GetDeviceKey returns the device pool record (contains device_key).
func (s *BindService) GetDeviceKey(ctx context.Context, deviceID string) (*model.DevicePool, error) {
	return s.bind.GetDeviceKey(ctx, deviceID)
}

// IsInCall reports whether deviceID is currently locked into a call-server room
// (mid device-to-device call). See store.CacheStore.IsInCall.
func (s *BindService) IsInCall(ctx context.Context, deviceID string) (bool, error) {
	return s.cache.IsInCall(ctx, deviceID)
}

// ── helpers ───────────────────────────────────────────────────────────────

func (s *BindService) redeliverCredentials(ctx context.Context, deviceID, tempClientID string) (string, error) {
	gpool, err := s.bind.GetDeviceKey(ctx, deviceID)
	if err != nil {
		return "", fmt.Errorf("service.redeliverCredentials GetDeviceKey: %w", err)
	}
	if gpool == nil {
		return "", fmt.Errorf("service.redeliverCredentials: key not found for %s", deviceID)
	}
	online, err := s.cache.IsDeviceOnline(ctx, tempClientID)
	if err != nil {
		return "", fmt.Errorf("service.redeliverCredentials IsDeviceOnline: %w", err)
	}
	if !online {
		return "", ErrDeviceMQTTOffline
	}
	if err := s.publishAndKick(ctx, tempClientID, gpool.DeviceID, gpool.DeviceKey); err != nil {
		return "", err
	}
	return gpool.DeviceID, nil
}

// notifyBindSuccess sends auth_grant to the device's temp MQTT client, then kicks it.
// Used by BindByDeviceID (cases E/G): device already has its credentials (proved via
// signed report → requireDeviceOnline) but is waiting on the temp MQTT connection for
// the "you're bound, proceed" signal. Sends empty payload — device uses its existing
// device_key to call /token for a permanent JWT, same as the scan-code pre-burned path.
// Looks up tempClientID from pending_bind Redis key (set during Report).
func (s *BindService) notifyBindSuccess(ctx context.Context, deviceID, mac string) {
	if s.mqtt == nil {
		return
	}
	// Check pending_bind first (pre-burned device with mac_{code} or mac_{MAC})
	tempClientID, _ := s.cache.GetPendingBind(ctx, deviceID)
	if tempClientID == "" {
		// Fallback: old path using mac_{MAC} directly
		if mac == "" {
			return
		}
		tempClientID = "mac_" + mac
	}
	// Empty payload — device proved credential ownership via signed report.
	// It calls /token with its device_key to get a permanent JWT after receiving
	// this confirmation signal.
	s.publishAndKick(ctx, tempClientID, "", "")
	s.cache.DelPendingBind(ctx, deviceID)       //nolint:errcheck
	s.cache.DelReportFingerprint(ctx, deviceID) //nolint:errcheck
}

// publishAndKick sends auth_grant and waits for the device's ACK. If the ACK
// never arrives (device offline, dropped packet, etc.) it returns
// ErrMQTTTimeout — the caller must treat the bind as failed rather than
// assuming the device received its credentials. The device is kicked
// unconditionally so it drops its temp connection and can retry.
func (s *BindService) publishAndKick(ctx context.Context, tempClientID, deviceID, deviceKey string) error {
	if s.mqtt == nil {
		return nil
	}
	payload := map[string]any{"type": "auth_grant"}
	if deviceID != "" {
		payload["payload"] = map[string]string{"device_id": deviceID, "device_key": deviceKey}
	}
	err := s.mqtt.PublishAndWaitACK("device/"+tempClientID+"/cmd", "device/"+tempClientID+"/ack", payload, s.cfg.MQTTACKTimeout)
	s.mqtt.KickClient(tempClientID)
	if err != nil {
		slog.WarnContext(ctx, "bind auth_grant delivery failed", "temp_client_id", tempClientID, "device_id", deviceID, "err", err)
		if errors.Is(err, mqttc.ErrACKTimeout) {
			return ErrMQTTAckTimeout
		}
		if errors.Is(err, mqttc.ErrSubscribeFailed) ||
			errors.Is(err, mqttc.ErrPublishFailed) ||
			errors.Is(err, mqttc.ErrPublishTimeout) {
			return ErrMQTTPublishFailed
		}
		return ErrMQTTTimeout
	}
	return nil
}
