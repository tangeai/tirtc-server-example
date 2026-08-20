package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"thing-connect/internal/physid"
	"thing-connect/internal/store"
)

// tokenReplayWindow is both the timestamp skew tolerance and the nonce TTL.
// Any request outside this window is rejected as a potential replay.
const tokenReplayWindow = 300 * time.Second

// DeviceService handles device report and token issuance.
type DeviceService struct {
	dev       store.DeviceStore
	cache     store.CacheStore
	cfg       atomic.Value
	jwtSecret string
}

func NewDeviceService(dev store.DeviceStore, cache store.CacheStore, jwtSecret string, cfg ServiceConfig) *DeviceService {
	service := &DeviceService{dev: dev, cache: cache, jwtSecret: jwtSecret}
	service.cfg.Store(cfg)
	return service
}

func (s *DeviceService) Config() ServiceConfig          { return s.cfg.Load().(ServiceConfig) }
func (s *DeviceService) UpdateConfig(cfg ServiceConfig) { s.cfg.Store(cfg) }

// CodeTTL returns the verification code TTL for use in Retry-After headers.
func (s *DeviceService) CodeTTL() time.Duration { return s.Config().CodeTTL }

// RateLimitWindow returns the L3 fingerprint rate-limit window for use in Retry-After headers.
func (s *DeviceService) RateLimitWindow() time.Duration { return s.Config().RateLimitWindow }

// IPRateLimitWindow returns the L2 IP fingerprint-diversity window for use in Retry-After headers.
func (s *DeviceService) IPRateLimitWindow() time.Duration { return s.Config().IPRateLimitWindow }

// AuthorizeTTS verifies that token is the temporary JWT issued in the same
// Report response as code. Any code/record/token mismatch is deliberately
// reported as ErrInvalidCode so a token from one device cannot be used as a
// validity oracle for another device's six-digit code.
func (s *DeviceService) AuthorizeTTS(ctx context.Context, code, token string) error {
	if !isSixDigitCode(code) {
		return ErrInvalidCode
	}

	tok, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		return []byte(s.jwtSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !tok.Valid {
		return ErrSigFail
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return ErrSigFail
	}
	tokenClientID, _ := claims["device_id"].(string)
	if tokenClientID == "" {
		return ErrSigFail
	}

	physHash, err := s.cache.GetDeviceCodeLookup(ctx, code)
	if err != nil {
		return fmt.Errorf("service.AuthorizeTTS GetDeviceCodeLookup: %w", err)
	}
	if physHash == "" {
		return ErrInvalidCode
	}
	raw, err := s.cache.GetVerifyRecord(ctx, physHash)
	if err != nil {
		return fmt.Errorf("service.AuthorizeTTS GetVerifyRecord: %w", err)
	}
	if raw == nil {
		return ErrInvalidCode
	}
	var record struct {
		Code         string `json:"code"`
		TempClientID string `json:"temp_client_id"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("service.AuthorizeTTS unmarshal verify record: %w", err)
	}
	if record.Code != code || record.TempClientID == "" || record.TempClientID != tokenClientID {
		return ErrInvalidCode
	}
	return nil
}

// ReportResult is the return value from Report: always a verification code +
// temp token. Report is an UNAUTHENTICATED endpoint, so it must never return
// device credentials — anyone could forge a MAC. Recovering an already-bound
// device goes through the authenticated scan-code bind flow on user-server.
type ReportResult struct {
	Code         string
	TempToken    string
	TempClientID string // tmp_{randHex}, used as MQTT ClientID/Username
}

// ReportHeaders carries the optional signature headers from a report request.
// Has* fields indicate whether the header key was present (regardless of value).
type ReportHeaders struct {
	DeviceID     string
	Timestamp    string
	Nonce        string
	Signature    string
	HasDeviceID  bool
	HasTimestamp bool
	HasNonce     bool
	HasSignature bool
}

// HasAny returns true if at least one header key is present.
func (h ReportHeaders) HasAny() bool {
	return h.HasDeviceID || h.HasTimestamp || h.HasNonce || h.HasSignature
}

// HasAll returns true if all four header keys are present.
func (h ReportHeaders) HasAll() bool {
	return h.HasDeviceID && h.HasTimestamp && h.HasNonce && h.HasSignature
}

// Report dispatches between signed (情况1) and unsigned (情况2/3) paths.
func (s *DeviceService) Report(ctx context.Context, clientIP string, hdr ReportHeaders, mac, bodyDeviceID string) (*ReportResult, error) {
	if hdr.HasAny() {
		if !hdr.HasAll() {
			return nil, ErrSigFieldsMissing
		}
		return s.reportSigned(ctx, hdr, mac)
	}
	if bodyDeviceID != "" {
		return nil, ErrDeviceIDUntrusted
	}
	return s.reportUnsigned(ctx, clientIP, mac, bodyDeviceID)
}

// reportSigned handles 情况1: the device has proven its identity via HMAC signature.
// Skips L1/L2/L4; applies fingerprint consistency (L3 for signed devices).
func (s *DeviceService) reportSigned(ctx context.Context, hdr ReportHeaders, mac string) (*ReportResult, error) {
	// ── Signature verification (same logic as Token) ──────────────────────
	ts, err := strconv.ParseInt(hdr.Timestamp, 10, 64)
	if err != nil {
		return nil, ErrSigTimestampInvalid
	}
	serverNow := time.Now().Unix()
	replayWindow := int64(tokenReplayWindow.Seconds())
	if ts < serverNow-replayWindow {
		return nil, ErrSigTimestampTooOld
	}
	if ts > serverNow+replayWindow {
		return nil, ErrSigTimestampTooNew
	}

	pool, err := s.dev.GetDeviceKey(ctx, hdr.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("service.reportSigned GetDeviceKey: %w", err)
	}
	if pool == nil {
		return nil, ErrSigFail
	}

	expected := computeHMAC(pool.DeviceKey, hdr.DeviceID+hdr.Timestamp+hdr.Nonce)
	if hdr.Signature != expected {
		return nil, ErrSigFail
	}

	ok, err := s.cache.SetNonce(ctx, hdr.Nonce, tokenReplayWindow)
	if err != nil {
		return nil, fmt.Errorf("service.reportSigned SetNonce: %w", err)
	}
	if !ok {
		return nil, ErrSigNonceReplay
	}

	// ── Fingerprint guard ─────────────────────────────────────────────────
	if mac == "" {
		return nil, ErrEmptyFingerprint
	}

	physHash := physid.Hash(mac)
	physicalID := physid.Physical(mac)

	// ── 需求3: device_id ↔ MAC 终身绑定（持久化校验） ────────────────────
	existing, err := s.dev.GetBindByDeviceID(ctx, hdr.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("service.reportSigned GetBindByDeviceID: %w", err)
	}
	if existing != nil && macConflicts(existing.MAC, mac) {
		return nil, ErrFingerprintMismatch
	}

	// ── L3 (signed): fingerprint consistency ──────────────────────────────
	// Store baseline fingerprint for this deviceID. If a baseline already
	// exists (from a previous signed report within CodeTTL), the physHash
	// must match — otherwise reject.
	storedFP, err := s.cache.GetReportFingerprint(ctx, hdr.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("service.reportSigned GetReportFingerprint: %w", err)
	}
	if storedFP != "" && storedFP != physHash {
		return nil, ErrFingerprintMismatch
	}

	// ── Replay check (same physHash within CodeTTL, same as unsigned path) ─
	cfg := s.Config()
	attempt, err := s.cache.IncrReportAttempt(ctx, physHash, cfg.RateLimitWindow)
	if err != nil {
		return nil, fmt.Errorf("service.reportSigned IncrReportAttempt: %w", err)
	}
	if attempt > int64(cfg.RateLimitMaxHits) {
		return nil, ErrRateLimit
	}
	if attempt > 1 {
		if replay, err := s.cache.GetReportReplay(ctx, physHash); err != nil {
			return nil, fmt.Errorf("service.reportSigned GetReportReplay: %w", err)
		} else if replay != nil {
			var result ReportResult
			if err := json.Unmarshal(replay, &result); err != nil {
				return nil, fmt.Errorf("service.reportSigned unmarshal replay: %w", err)
			}
			// Only reuse a replay that was created by an earlier signed report
			// for this device. Unsigned and signed reports share the physical
			// fingerprint key; returning an unsigned replay here would omit the
			// pending_bind:<device_id> proof and make bind-by-id report offline.
			pending, err := s.cache.GetPendingBind(ctx, hdr.DeviceID)
			if err != nil {
				return nil, fmt.Errorf("service.reportSigned GetPendingBind: %w", err)
			}
			if storedFP == physHash && pending != "" {
				return &result, nil
			}
			// Upgrade the outstanding anonymous report to a signed report. The
			// old six-digit code must no longer resolve to the anonymous record.
			if err := s.cache.DelVerifyAndCode(ctx, physHash, result.Code); err != nil {
				return nil, fmt.Errorf("service.reportSigned clear unsigned replay: %w", err)
			}
		}
	}

	// ── Generate new code ─────────────────────────────────────────────────
	// Track in global pending counter for ledger balance (DelVerifyAndCode
	// unconditionally decrements), but do NOT enforce the cap — signed devices
	// are trusted.
	s.cache.IncrGlobalPending(ctx) //nolint:errcheck

	code, err := s.reserveDeviceCode(ctx, physHash)
	if err != nil {
		s.cache.DecrGlobalPending(ctx) //nolint:errcheck
		return nil, fmt.Errorf("service.reportSigned reserveDeviceCode: %w", err)
	}
	tempClientID := "tmp_" + generateRandHex4()
	tempToken, err := issueMQTTToken(tempClientID, s.jwtSecret, cfg.CodeTTL)
	if err != nil {
		s.cache.DelDeviceCodeLookup(ctx, code) //nolint:errcheck
		s.cache.DecrGlobalPending(ctx)         //nolint:errcheck
		return nil, fmt.Errorf("service.reportSigned issueMQTTToken: %w", err)
	}

	// Verify record carries the real (trusted) device_id so Bind()'s
	// pre-burned-device branch triggers automatically for scan-code binds.
	verifyVal, err := json.Marshal(map[string]string{
		"code":           code,
		"temp_token":     tempToken,
		"physical_id":    physicalID,
		"device_id":      hdr.DeviceID,
		"temp_client_id": tempClientID,
	})
	if err != nil {
		s.cache.DelDeviceCodeLookup(ctx, code) //nolint:errcheck
		s.cache.DecrGlobalPending(ctx)         //nolint:errcheck
		return nil, fmt.Errorf("service.reportSigned marshal verify: %w", err)
	}

	set, err := s.cache.SetVerifyRecord(ctx, physHash, verifyVal, cfg.CodeTTL)
	if err != nil {
		s.cache.DelDeviceCodeLookup(ctx, code) //nolint:errcheck
		s.cache.DecrGlobalPending(ctx)         //nolint:errcheck
		return nil, fmt.Errorf("service.reportSigned SetVerifyRecord: %w", err)
	}
	if !set {
		s.cache.DelDeviceCodeLookup(ctx, code) //nolint:errcheck
		s.cache.DecrGlobalPending(ctx)         //nolint:errcheck
		return nil, ErrVerifyPending
	}

	// Store pending_bind so BindByDeviceID can find the temp MQTT client.
	if err := s.cache.SetPendingBind(ctx, hdr.DeviceID, tempClientID, cfg.CodeTTL); err != nil {
		return nil, fmt.Errorf("service.reportSigned SetPendingBind: %w", err)
	}

	// Store baseline fingerprint for consistency checks on subsequent reports.
	if err := s.cache.SetReportFingerprint(ctx, hdr.DeviceID, physHash, cfg.CodeTTL); err != nil {
		return nil, fmt.Errorf("service.reportSigned SetReportFingerprint: %w", err)
	}

	result := &ReportResult{Code: code, TempToken: tempToken, TempClientID: tempClientID}

	replayVal, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("service.reportSigned marshal replay: %w", err)
	}
	if err := s.cache.SetReportReplay(ctx, physHash, replayVal, cfg.CodeTTL); err != nil {
		return nil, fmt.Errorf("service.reportSigned SetReportReplay: %w", err)
	}

	return result, nil
}

// reportUnsigned handles 情况2: no signature headers, no trusted device_id.
// Applies the full four-layer rate limiting (L1 external, L2-L4).
func (s *DeviceService) reportUnsigned(ctx context.Context, clientIP, mac, deviceID string) (*ReportResult, error) {
	if mac == "" {
		return nil, ErrEmptyFingerprint
	}

	physHash := physid.Hash(mac)
	physicalID := physid.Physical(mac)

	// ── L2: IP fingerprint diversity ──────────────────────────────────────
	cfg := s.Config()
	isNewFP, fpCount, err := s.cache.AddIPFingerprint(ctx, clientIP, physHash, cfg.IPRateLimitWindow)
	if err != nil {
		return nil, fmt.Errorf("service.reportUnsigned AddIPFingerprint: %w", err)
	}
	if isNewFP && fpCount > int64(cfg.IPRateLimitMaxFingerprints) {
		return nil, ErrIPFingerprintLimit
	}

	// ── L3: fingerprint rate limit + replay ───────────────────────────────
	attempt, err := s.cache.IncrReportAttempt(ctx, physHash, cfg.RateLimitWindow)
	if err != nil {
		return nil, fmt.Errorf("service.reportUnsigned IncrReportAttempt: %w", err)
	}
	if attempt > int64(cfg.RateLimitMaxHits) {
		return nil, ErrRateLimit
	}
	if attempt > 1 {
		if replay, err := s.cache.GetReportReplay(ctx, physHash); err != nil {
			return nil, fmt.Errorf("service.reportUnsigned GetReportReplay: %w", err)
		} else if replay != nil {
			var result ReportResult
			if err := json.Unmarshal(replay, &result); err != nil {
				return nil, fmt.Errorf("service.reportUnsigned unmarshal replay: %w", err)
			}
			return &result, nil
		}
	}

	// ── L4: global pending verification code cap ──────────────────────────
	globalCount, err := s.cache.IncrGlobalPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("service.reportUnsigned IncrGlobalPending: %w", err)
	}
	if globalCount > int64(cfg.GlobalMaxPendingCodes) {
		s.cache.DecrGlobalPending(ctx) //nolint:errcheck
		return nil, ErrGlobalBusy
	}

	// ── Generate new verification code ────────────────────────────────────
	code, err := s.reserveDeviceCode(ctx, physHash)
	if err != nil {
		s.cache.DecrGlobalPending(ctx) //nolint:errcheck
		return nil, fmt.Errorf("service.reportUnsigned reserveDeviceCode: %w", err)
	}
	tempClientID := "tmp_" + generateRandHex4()
	tempToken, err := issueMQTTToken(tempClientID, s.jwtSecret, cfg.CodeTTL)
	if err != nil {
		s.cache.DelDeviceCodeLookup(ctx, code) //nolint:errcheck
		s.cache.DecrGlobalPending(ctx)         //nolint:errcheck
		return nil, fmt.Errorf("service.reportUnsigned issueMQTTToken: %w", err)
	}

	verifyVal, err := json.Marshal(map[string]string{
		"code":           code,
		"temp_token":     tempToken,
		"physical_id":    physicalID,
		"device_id":      deviceID,
		"temp_client_id": tempClientID,
	})
	if err != nil {
		s.cache.DelDeviceCodeLookup(ctx, code) //nolint:errcheck
		s.cache.DecrGlobalPending(ctx)         //nolint:errcheck
		return nil, fmt.Errorf("service.reportUnsigned marshal verify: %w", err)
	}

	set, err := s.cache.SetVerifyRecord(ctx, physHash, verifyVal, cfg.CodeTTL)
	if err != nil {
		s.cache.DelDeviceCodeLookup(ctx, code) //nolint:errcheck
		s.cache.DecrGlobalPending(ctx)         //nolint:errcheck
		return nil, fmt.Errorf("service.reportUnsigned SetVerifyRecord: %w", err)
	}
	if !set {
		s.cache.DelDeviceCodeLookup(ctx, code) //nolint:errcheck
		s.cache.DecrGlobalPending(ctx)         //nolint:errcheck
		return nil, ErrVerifyPending
	}

	// deviceID is always "" here: 情况3 (body device_id without signature)
	// is rejected by Report() before reportUnsigned is called.

	result := &ReportResult{Code: code, TempToken: tempToken, TempClientID: tempClientID}

	replayVal, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("service.reportUnsigned marshal replay: %w", err)
	}
	if err := s.cache.SetReportReplay(ctx, physHash, replayVal, cfg.CodeTTL); err != nil {
		return nil, fmt.Errorf("service.reportUnsigned SetReportReplay: %w", err)
	}

	return result, nil
}

// Token verifies HMAC signature and issues an MQTT JWT token.
func (s *DeviceService) Token(ctx context.Context, deviceID, tsStr, nonce, sigB64, mac string) (string, error) {
	if deviceID == "" || tsStr == "" || nonce == "" || sigB64 == "" {
		return "", ErrSigFieldsMissing
	}

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return "", ErrSigTimestampInvalid
	}
	serverNow := time.Now().Unix()
	replayWindow := int64(tokenReplayWindow.Seconds())
	if ts < serverNow-replayWindow {
		return "", ErrSigTimestampTooOld
	}
	if ts > serverNow+replayWindow {
		return "", ErrSigTimestampTooNew
	}

	pool, err := s.dev.GetDeviceKey(ctx, deviceID)
	if err != nil {
		return "", fmt.Errorf("service.Token GetDeviceKey: %w", err)
	}
	if pool == nil {
		return "", ErrSigFail
	}

	expected := computeHMAC(pool.DeviceKey, deviceID+tsStr+nonce)
	if sigB64 != expected {
		return "", ErrSigFail
	}

	// Consume nonce only after signature is verified to prevent nonce-burning attacks.
	ok, err := s.cache.SetNonce(ctx, nonce, tokenReplayWindow)
	if err != nil {
		return "", fmt.Errorf("service.Token SetNonce: %w", err)
	}
	if !ok {
		return "", ErrSigNonceReplay
	}

	rel, err := s.dev.GetBindByDeviceID(ctx, deviceID)
	if err != nil {
		return "", fmt.Errorf("service.Token GetBindByDeviceID: %w", err)
	}
	if rel == nil || rel.UserID == 0 {
		return "", ErrDeviceReset
	}

	// 需求3: device_id ↔ MAC 终身绑定 —— 已存 MAC 与上报 MAC 不一致则拒绝。
	if macConflicts(rel.MAC, mac) {
		return "", ErrFingerprintMismatch
	}

	// 需求9(token 侧): 同用户名下同 mac 不能关联多个 device_id
	if mac != "" {
		if dup, err := s.dev.GetBindByFingerprint(ctx, mac, rel.UserID); err != nil {
			return "", fmt.Errorf("service.Token GetBindByFingerprint: %w", err)
		} else if dup != nil && dup.UserID == rel.UserID && dup.DeviceID != deviceID {
			return "", ErrMACDuplicateBinding
		}
	}

	tok, err := issueMQTTToken(pool.DeviceID, s.jwtSecret, s.Config().TokenExpiry)
	if err != nil {
		return "", err
	}
	if rel.ActiveTime == nil {
		s.dev.UpdateActiveTimeIfEmpty(ctx, deviceID) //nolint:errcheck
	}
	return tok, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

// macConflicts reports whether storedMAC and reportedMAC are both non-empty and
// different. Empty storedMAC = first bind (no conflict). Used by reportSigned
// (device_id↔MAC lifetime binding) and bind (strict lifetime / clone checks).
func macConflicts(storedMAC, reportedMAC string) bool {
	return storedMAC != "" && reportedMAC != "" && storedMAC != reportedMAC
}

func generate6DigitCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

const deviceCodeReserveAttempts = 10

// reserveDeviceCode generates a code and atomically reserves its reverse
// lookup. Six-digit codes have a small key space, so generation must retry
// instead of overwriting an active device's lookup on collision.
func (s *DeviceService) reserveDeviceCode(ctx context.Context, physHash string) (string, error) {
	for attempt := 0; attempt < deviceCodeReserveAttempts; attempt++ {
		code, err := generate6DigitCode()
		if err != nil {
			return "", err
		}
		reserved, err := s.cache.ReserveDeviceCode(ctx, code, physHash, s.Config().CodeTTL)
		if err != nil {
			return "", err
		}
		if reserved {
			return code, nil
		}
	}
	return "", ErrGlobalBusy
}

func isSixDigitCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := range code {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

func generateRandHex4() string {
	var b [4]byte
	rand.Read(b[:]) //nolint:errcheck
	return fmt.Sprintf("%08x", b)
}

func computeHMAC(key, data string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func issueMQTTToken(deviceID, secret string, expiry time.Duration) (string, error) {
	claims := jwt.MapClaims{
		"device_id": deviceID,
		"exp":       time.Now().Add(expiry).Unix(),
		"iat":       time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("issueMQTTToken: %w", err)
	}
	return signed, nil
}
