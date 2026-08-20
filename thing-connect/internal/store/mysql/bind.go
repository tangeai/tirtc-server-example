// Package mysql implements MySQL and Redis adapters for shared service ports.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"thing-connect/internal/model"
	"thing-connect/internal/store"
)

// ── BindStore ────────────────────────────────────────────────────────────────

type bindStore struct{ db *sqlx.DB }

func NewBindStore(db *sqlx.DB) store.BindStore { return &bindStore{db} }

// getBindByMAC prioritizes rows owned by userID, then rows this user previously
// owned (last_user_id), then any unowned row — so a MAC collision with another
// user's device never hides the caller's own device.
func getBindByMAC(ctx context.Context, db sqlx.ExtContext, mac string, userID int64) (*model.DeviceBind, error) {
	var r model.DeviceBind
	err := sqlx.GetContext(ctx, db, &r,
		`SELECT * FROM device_bind WHERE mac=?
			 ORDER BY (user_id = ?) DESC, (user_id = 0 AND last_user_id = ?) DESC, (user_id = 0) DESC
			 LIMIT 1`,
		mac, userID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // absence is the BindStore lookup contract
	}
	if err != nil {
		return nil, fmt.Errorf("getBindByMAC: %w", err)
	}
	return &r, nil
}

func (s *bindStore) GetBindByFingerprint(ctx context.Context, mac string, userID int64) (*model.DeviceBind, error) {
	return getBindByMAC(ctx, s.db, mac, userID)
}

func (s *bindStore) GetBindByDeviceID(ctx context.Context, deviceID string) (*model.DeviceBind, error) {
	var r model.DeviceBind
	err := s.db.GetContext(ctx, &r,
		`SELECT * FROM device_bind WHERE device_id=? LIMIT 1`, deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // absence is the BindStore lookup contract
	}
	if err != nil {
		return nil, fmt.Errorf("bindStore.GetBindByDeviceID: %w", err)
	}
	return &r, nil
}

// CommitBindFromPool implements case A: grab a free device from pool, bind, write log.
// Uses SELECT ... FOR UPDATE to safely pick one unallocated row without TOCTOU races.
func (s *bindStore) CommitBindFromPool(ctx context.Context, fp model.Fingerprint, userID int64) (string, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("bindStore.CommitBindFromPool begin: %w", err)
	}
	defer tx.Rollback()

	// 0. Atomic quota deduction also serializes concurrent binds for the same user.
	// This lock must be acquired before the MAC gap lock below: taking the gap lock
	// first lets two same-user requests hold compatible gap locks and then deadlock
	// while one waits for the user row and the other tries to insert into the gap.
	// Returning before commit rolls this deduction back, so conflicts do not burn
	// quota.
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET bind_quota = bind_quota - 1 WHERE id = ? AND bind_quota > 0`, userID)
	if err != nil {
		return "", fmt.Errorf("bindStore.CommitBindFromPool quota: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", store.ErrQuotaEmpty
	}

	// 1. Per-user MAC uniqueness: if (mac, user_id) is already bound, hand back
	// the existing device_id instead of allocating a new one. The user-row lock
	// above serializes same-user requests; SELECT ... FOR UPDATE additionally
	// protects the matching row or gap until this transaction completes.
	if fp.MAC != "" {
		var existingID string
		err = tx.QueryRowContext(ctx,
			`SELECT device_id FROM device_bind WHERE mac=? AND user_id=? FOR UPDATE`,
			fp.MAC, userID).Scan(&existingID)
		if err == nil {
			return existingID, store.ErrMACAlreadyBound
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("bindStore.CommitBindFromPool mac check: %w", err)
		}
	}

	// 2. Safely grab one free, never-bound device: lock the row first, then update.
	// LEFT JOIN device_bind excludes any device that has ever been bound — even
	// after unbind the device_bind row is kept (user_id=0), so a device_id, once
	// assigned to a user, is never reallocated to another. 禁止流转: 现实中 MAC 不
	// 重复，故 device_id 也不重复，绑定关系终身固定。Only brand-new pool devices qualify.
	var poolID int64
	var deviceID string
	err = tx.QueryRowContext(ctx,
		`SELECT p.id, p.device_id
		   FROM device_pool p
		   LEFT JOIN device_bind b ON b.device_id = p.device_id
		  WHERE p.status = 0 AND b.device_id IS NULL
		  ORDER BY p.id LIMIT 1 FOR UPDATE`,
	).Scan(&poolID, &deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrPoolEmpty
	}
	if err != nil {
		return "", fmt.Errorf("bindStore.CommitBindFromPool select pool: %w", err)
	}

	if _, err = tx.ExecContext(ctx,
		`UPDATE device_pool SET status=1, updated_at=NOW() WHERE id=?`, poolID); err != nil {
		return "", fmt.Errorf("bindStore.CommitBindFromPool update pool: %w", err)
	}

	// 3. Insert the never-bound device. The duplicate branch is a defensive
	// concurrency guard; the selection above excludes every existing binding
	// row, including released devices whose pool status is 0.
	res2, err := tx.ExecContext(ctx,
		`INSERT INTO device_bind (device_id, mac, chip_uid, device_rand, assign, user_id, last_user_id, bind_time)
		 VALUES (?, ?, ?, ?, 'dynamic', ?, ?, NOW())
		 ON DUPLICATE KEY UPDATE
		     mac          = IF(user_id = 0, VALUES(mac),          mac),
		     chip_uid     = IF(user_id = 0, VALUES(chip_uid),     chip_uid),
		     device_rand  = IF(user_id = 0, VALUES(device_rand),  device_rand),
		     assign       = IF(user_id = 0, VALUES(assign),       assign),
		     user_id      = IF(user_id = 0, VALUES(user_id),      user_id),
		     last_user_id = IF(user_id = 0, VALUES(last_user_id), last_user_id),
		     bind_time    = IF(user_id = 0, NOW(),                bind_time),
		     unbind_time  = IF(user_id = 0, NULL,                 unbind_time)`,
		deviceID, fp.MAC, fp.ChipUID, fp.DeviceRand, userID, userID)
	if err != nil {
		return "", fmt.Errorf("bindStore.CommitBindFromPool insert bind: %w", err)
	}
	// ON DUPLICATE returned 0 rows affected → it matched an existing (mac,user_id)
	// row (uq_mac_user) but the UPDATE was a no-op because that row is already
	// owned (user_id!=0). That's a cross-instance concurrent bind that slipped
	// past the FOR UPDATE. Hand back the existing device_id.
	if n, _ := res2.RowsAffected(); n == 0 && fp.MAC != "" {
		var existingID string
		if e := tx.QueryRowContext(ctx, `SELECT device_id FROM device_bind WHERE mac=? AND user_id=?`, fp.MAC, userID).Scan(&existingID); e == nil {
			return existingID, store.ErrMACAlreadyBound
		}
		return "", store.ErrMACAlreadyBound
	}

	// 4. Write log
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO device_bind_log (device_id, user_id, action, mac, chip_uid, device_rand, assign)
		 VALUES (?, ?, 1, ?, ?, ?, 'dynamic')`,
		deviceID, userID, fp.MAC, fp.ChipUID, fp.DeviceRand); err != nil {
		return "", fmt.Errorf("bindStore.CommitBindFromPool log: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("bindStore.CommitBindFromPool commit: %w", err)
	}
	return deviceID, nil
}

// CommitBindByDeviceID implements case E: pre-flashed device first bind.
func (s *bindStore) CommitBindByDeviceID(ctx context.Context, deviceID string, fp model.Fingerprint, userID int64) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("bindStore.CommitBindByDeviceID begin: %w", err)
	}
	defer tx.Rollback()

	// Quota deduction
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET bind_quota = bind_quota - 1 WHERE id = ? AND bind_quota > 0`, userID)
	if err != nil {
		return fmt.Errorf("bindStore.CommitBindByDeviceID quota: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrQuotaEmpty
	}

	// The device must remain marked allocated in the same transaction as its
	// binding row. Updating a missing pool row is valid for legacy pre-burn data.
	if _, err := tx.ExecContext(ctx, `UPDATE device_pool SET status=1, updated_at=NOW() WHERE device_id=?`, deviceID); err != nil {
		return fmt.Errorf("bindStore.CommitBindByDeviceID update pool: %w", err)
	}

	// INSERT OR skip if already bound
	res, err = tx.ExecContext(ctx,
		`INSERT INTO device_bind (device_id, mac, chip_uid, device_rand, assign, user_id, last_user_id, bind_time)
		 VALUES (?, ?, ?, ?, 'preburn', ?, ?, NOW())
		 ON DUPLICATE KEY UPDATE
		     user_id      = IF(user_id = 0, VALUES(user_id), user_id),
		     last_user_id = IF(user_id = 0, VALUES(last_user_id), last_user_id),
		     bind_time    = IF(user_id = 0, NOW(), bind_time),
		     assign       = IF(user_id = 0, VALUES(assign), assign)`,
		deviceID, fp.MAC, fp.ChipUID, fp.DeviceRand, userID, userID)
	if err != nil {
		return fmt.Errorf("bindStore.CommitBindByDeviceID upsert: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrSlotConflict
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO device_bind_log (device_id, user_id, action, mac, chip_uid, device_rand, assign)
		 VALUES (?, ?, 1, ?, ?, ?, 'preburn')`,
		deviceID, userID, fp.MAC, fp.ChipUID, fp.DeviceRand); err != nil {
		return fmt.Errorf("bindStore.CommitBindByDeviceID log: %w", err)
	}

	return tx.Commit()
}

// CommitClaim implements cases D (scan re-claim) and G (transfer/board swap).
// Caller must guarantee the row is currently unowned (user_id=0).
func (s *bindStore) CommitClaim(ctx context.Context, deviceID string, fp model.Fingerprint, userID int64) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("bindStore.CommitClaim begin: %w", err)
	}
	defer tx.Rollback()

	// Quota deduction
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET bind_quota = bind_quota - 1 WHERE id = ? AND bind_quota > 0`, userID)
	if err != nil {
		return fmt.Errorf("bindStore.CommitClaim quota: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrQuotaEmpty
	}

	// Capture assign for log before the UPDATE overwrites (or row is claimed)
	var oldAssign string
	if err := tx.QueryRowContext(ctx, `SELECT assign FROM device_bind WHERE device_id=?`, deviceID).Scan(&oldAssign); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrSlotConflict
		}
		return fmt.Errorf("bindStore.CommitClaim read assign: %w", err)
	}

	// Optimistic claim: only succeeds if still unowned
	res, err = tx.ExecContext(ctx,
		`UPDATE device_bind
		 SET user_id=?, last_user_id=?, bind_time=NOW(),
		     mac=?, chip_uid=?, device_rand=?
		 WHERE device_id=? AND user_id=0`,
		userID, userID, fp.MAC, fp.ChipUID, fp.DeviceRand, deviceID)
	if err != nil {
		return fmt.Errorf("bindStore.CommitClaim update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrSlotConflict
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO device_bind_log (device_id, user_id, action, mac, chip_uid, device_rand, assign)
		 VALUES (?, ?, 1, ?, ?, ?, ?)`,
		deviceID, userID, fp.MAC, fp.ChipUID, fp.DeviceRand, oldAssign); err != nil {
		return fmt.Errorf("bindStore.CommitClaim log: %w", err)
	}

	return tx.Commit()
}

// TouchRebind implements case F: same user re-binds their own device (no quota change).
func (s *bindStore) TouchRebind(ctx context.Context, deviceID string, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE device_bind SET bind_time=NOW(), last_user_id=? WHERE device_id=? AND user_id=?`,
		userID, deviceID, userID)
	if err != nil {
		return fmt.Errorf("bindStore.TouchRebind: %w", err)
	}
	return nil
}

// CommitUnbind sets user_id=0 (unowned), increments quota, writes log.
// Reads the current fingerprint first (within the same tx) before zeroing user_id,
// so the log captures the fingerprint at time of unbind.
// last_user_id is preserved — not updated during unbind.
func (s *bindStore) CommitUnbind(ctx context.Context, deviceID string, userID int64) error {
	return s.commitUnbind(ctx, deviceID, userID, nil)
}

// CommitUnbindWithCleanup persists the unbind and its service-cleanup events in
// one MySQL transaction. The outbox is owned by user-server; target services
// remain isolated behind their internal HTTP APIs.
func (s *bindStore) CommitUnbindWithCleanup(ctx context.Context, deviceID string, userID int64, targets []string) error {
	return s.commitUnbind(ctx, deviceID, userID, targets)
}

func (s *bindStore) commitUnbind(ctx context.Context, deviceID string, userID int64, targets []string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("bindStore.CommitUnbind begin: %w", err)
	}
	defer tx.Rollback()

	if err := ApplyUnbindTx(ctx, tx, deviceID, userID, targets); err != nil {
		return err
	}
	return tx.Commit()
}

// ApplyUnbindTx applies the canonical device-unbind mutation inside a caller-
// owned transaction. It is shared by user-server and the Admin MySQL adapter
// so quota, history, pool state and cleanup outbox semantics cannot diverge.
func ApplyUnbindTx(ctx context.Context, tx *sqlx.Tx, deviceID string, userID int64, targets []string) error {
	// Lock and read the row before mutation so concurrent ownership changes are
	// detected and the history captures the original fingerprint.
	var currentUserID int64
	var mac, chipUID, devRand, oldAssign string
	err := tx.QueryRowContext(ctx,
		`SELECT user_id,mac,chip_uid,device_rand,assign FROM device_bind WHERE device_id=? FOR UPDATE`,
		deviceID,
	).Scan(&currentUserID, &mac, &chipUID, &devRand, &oldAssign)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrDeviceNotFound
	}
	if err != nil {
		return fmt.Errorf("bindStore.CommitUnbind read binding: %w", err)
	}
	if currentUserID != userID {
		return store.ErrSlotConflict
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE device_bind
		    SET user_id=0, device_name='', bind_time=NULL, unbind_time=NOW()
		  WHERE device_id=? AND user_id=?`,
		deviceID, userID)
	if err != nil {
		return fmt.Errorf("bindStore.CommitUnbind update: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return store.ErrSlotConflict
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET bind_quota=bind_quota+1 WHERE id=?`, userID); err != nil {
		return fmt.Errorf("bindStore.CommitUnbind quota: %w", err)
	}

	// status=0 means released. CommitBindFromPool also requires the absence of a
	// device_bind row, so a previously owned device is not allocated to another
	// user and remains available only for its explicit reclaim flow.
	if _, err := tx.ExecContext(ctx,
		`UPDATE device_pool SET status=0,updated_at=NOW() WHERE device_id=?`, deviceID); err != nil {
		return fmt.Errorf("bindStore.CommitUnbind pool: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO device_bind_log (device_id,user_id,action,mac,chip_uid,device_rand,assign)
		 VALUES (?,?,2,?,?,?,?)`,
		deviceID, userID, mac, chipUID, devRand, oldAssign); err != nil {
		return fmt.Errorf("bindStore.CommitUnbind log: %w", err)
	}

	for _, target := range uniqueCleanupTargets(targets) {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cleanup_outbox (device_id,target,next_attempt_at)
			VALUES (?,?,NOW())
			ON DUPLICATE KEY UPDATE attempts=0,next_attempt_at=NOW(),last_error=''`, deviceID, target); err != nil {
			return fmt.Errorf("bindStore.CommitUnbind cleanup outbox %s: %w", target, err)
		}
	}
	return nil
}

func uniqueCleanupTargets(targets []string) []string {
	seen := make(map[string]struct{}, len(targets))
	result := make([]string, 0, len(targets))
	for _, target := range targets {
		if target == "" {
			continue
		}
		if _, exists := seen[target]; exists {
			continue
		}
		seen[target] = struct{}{}
		result = append(result, target)
	}
	return result
}

func (s *bindStore) GetDeviceKey(ctx context.Context, deviceID string) (*model.DevicePool, error) {
	var r model.DevicePool
	err := s.db.GetContext(ctx, &r,
		`SELECT device_id, device_key FROM device_pool WHERE device_id=?`, deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // absence is the BindStore lookup contract
	}
	if err != nil {
		return nil, fmt.Errorf("bindStore.GetDeviceKey: %w", err)
	}
	return &r, nil
}

func (s *bindStore) GetUserDevices(ctx context.Context, userID int64) ([]model.DeviceBind, error) {
	var rows []model.DeviceBind
	err := s.db.SelectContext(ctx, &rows,
		`SELECT * FROM device_bind WHERE user_id=? ORDER BY bind_time DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("bindStore.GetUserDevices: %w", err)
	}
	return rows, nil
}

// ── CacheStore ───────────────────────────────────────────────────────────────

type cacheStore struct{ rdb *redis.Client }

func NewCacheStore(rdb *redis.Client) store.CacheStore { return &cacheStore{rdb} }

func (s *cacheStore) IncrReportAttempt(ctx context.Context, physHash string, window time.Duration) (int64, error) {
	key := "rate:" + physHash
	count, err := s.rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cacheStore.IncrReportAttempt: %w", err)
	}
	if count == 1 {
		if err := s.rdb.Expire(ctx, key, window).Err(); err != nil {
			return 0, fmt.Errorf("cacheStore.IncrReportAttempt Expire: %w", err)
		}
	}
	return count, nil
}

func (s *cacheStore) SetReportReplay(ctx context.Context, physHash string, val []byte, ttl time.Duration) error {
	if err := s.rdb.Set(ctx, "rate_reply:"+physHash, string(val), ttl).Err(); err != nil {
		return fmt.Errorf("cacheStore.SetReportReplay: %w", err)
	}
	return nil
}

func (s *cacheStore) GetReportReplay(ctx context.Context, physHash string) ([]byte, error) {
	val, err := s.rdb.Get(ctx, "rate_reply:"+physHash).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cacheStore.GetReportReplay: %w", err)
	}
	return val, nil
}

func (s *cacheStore) SetVerifyRecord(ctx context.Context, physHash string, val []byte, ttl time.Duration) (bool, error) {
	ok, err := s.rdb.SetNX(ctx, "verify:"+physHash, string(val), ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cacheStore.SetVerifyRecord: %w", err)
	}
	return ok, nil
}

func (s *cacheStore) GetVerifyRecord(ctx context.Context, physHash string) ([]byte, error) {
	val, err := s.rdb.Get(ctx, "verify:"+physHash).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cacheStore.GetVerifyRecord: %w", err)
	}
	return val, nil
}

func (s *cacheStore) ReserveDeviceCode(ctx context.Context, code, physHash string, ttl time.Duration) (bool, error) {
	ok, err := s.rdb.SetNX(ctx, "device_code_lookup:"+code, physHash, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cacheStore.ReserveDeviceCode: %w", err)
	}
	return ok, nil
}

func (s *cacheStore) GetDeviceCodeLookup(ctx context.Context, code string) (string, error) {
	val, err := s.rdb.Get(ctx, "device_code_lookup:"+code).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil // key not found — callers check for empty string
	}
	if err != nil {
		return "", fmt.Errorf("cacheStore.GetDeviceCodeLookup: %w", err)
	}
	return val, nil
}

func (s *cacheStore) DelDeviceCodeLookup(ctx context.Context, code string) error {
	if err := s.rdb.Del(ctx, "device_code_lookup:"+code).Err(); err != nil {
		return fmt.Errorf("cacheStore.DelDeviceCodeLookup: %w", err)
	}
	return nil
}

func (s *cacheStore) SetEmailCode(ctx context.Context, email, code string, ttl time.Duration) error {
	if err := s.rdb.Set(ctx, "email_code:"+email, code, ttl).Err(); err != nil {
		return fmt.Errorf("cacheStore.SetEmailCode: %w", err)
	}
	return nil
}

func (s *cacheStore) GetEmailCode(ctx context.Context, email string) (string, error) {
	val, err := s.rdb.Get(ctx, "email_code:"+email).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cacheStore.GetEmailCode: %w", err)
	}
	return val, nil
}

func (s *cacheStore) ConsumeEmailCode(ctx context.Context, email, code string) (bool, error) {
	result, err := s.rdb.Eval(ctx, `
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("DEL", KEYS[1])
		end
		return 0
	`, []string{"email_code:" + email}, code).Int64()
	if err != nil {
		return false, fmt.Errorf("cacheStore.ConsumeEmailCode: %w", err)
	}
	return result == 1, nil
}

func (s *cacheStore) IncrRateLimitAttempt(ctx context.Context, scope string, window time.Duration) (int64, error) {
	key := "password_reset_attempt:" + scope
	windowMS := window.Milliseconds()
	if windowMS < 1 {
		windowMS = 1
	}
	count, err := s.rdb.Eval(ctx, `
		local count = redis.call("INCR", KEYS[1])
		if count == 1 then
			redis.call("PEXPIRE", KEYS[1], ARGV[1])
		end
		return count
	`, []string{key}, windowMS).Int64()
	if err != nil {
		return 0, fmt.Errorf("cacheStore.IncrRateLimitAttempt: %w", err)
	}
	return count, nil
}

func (s *cacheStore) IsDeviceOnline(ctx context.Context, deviceID string) (bool, error) {
	val, err := s.rdb.Get(ctx, "online:"+deviceID).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("cacheStore.IsDeviceOnline: %w", err)
	}
	// Older deployments store "1" while current device heartbeats store the
	// observation timestamp. Any non-empty value with an active TTL means online.
	return val != "", nil
}

func (s *cacheStore) IsInCall(ctx context.Context, deviceID string) (bool, error) {
	_, err := s.rdb.Get(ctx, "room:lock:"+deviceID).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("cacheStore.IsInCall: %w", err)
	}
	return true, nil
}

func (s *cacheStore) DelVerifyAndCode(ctx context.Context, physHash, code string) error {
	// Clean up verify record, code lookup, rate counter, and replay cache
	// so the same fingerprint can get a fresh verification code on next Report.
	if err := s.rdb.Del(ctx,
		"verify:"+physHash,
		"device_code_lookup:"+code,
		"code_lookup:"+code, // remove a pre-isolation key during rolling upgrades
		"rate:"+physHash,
		"rate_reply:"+physHash,
	).Err(); err != nil {
		return fmt.Errorf("cacheStore.DelVerifyAndCode: %w", err)
	}
	// Release one slot from the global pending counter so a new device
	// can obtain a verification code.
	s.rdb.Decr(ctx, "global:pending_codes") //nolint:errcheck
	return nil
}

// DelEmailCode removes an email verification code (SendCode/Register flow).
// Unlike DelVerifyAndCode, it does NOT touch global:pending_codes — that
// counter tracks device report codes only, and email codes never increment it.
func (s *cacheStore) DelEmailCode(ctx context.Context, email string) error {
	if err := s.rdb.Del(ctx, "email_code:"+email).Err(); err != nil {
		return fmt.Errorf("cacheStore.DelEmailCode: %w", err)
	}
	return nil
}

func (s *cacheStore) SetNonce(ctx context.Context, nonce string, ttl time.Duration) (bool, error) {
	ok, err := s.rdb.SetNX(ctx, "nonce:"+nonce, "1", ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cacheStore.SetNonce: %w", err)
	}
	return ok, nil
}

func (s *cacheStore) SetPendingBind(ctx context.Context, deviceID, tempClientID string, ttl time.Duration) error {
	if err := s.rdb.Set(ctx, "pending_bind:"+deviceID, tempClientID, ttl).Err(); err != nil {
		return fmt.Errorf("cacheStore.SetPendingBind: %w", err)
	}
	return nil
}

func (s *cacheStore) GetPendingBind(ctx context.Context, deviceID string) (string, error) {
	val, err := s.rdb.Get(ctx, "pending_bind:"+deviceID).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cacheStore.GetPendingBind: %w", err)
	}
	return val, nil
}

func (s *cacheStore) DelPendingBind(ctx context.Context, deviceID string) error {
	s.rdb.Del(ctx, "pending_bind:"+deviceID) //nolint:errcheck
	return nil
}

func (s *cacheStore) AddIPFingerprint(ctx context.Context, ip, physHash string, window time.Duration) (bool, int64, error) {
	key := "rate_ip_fps:" + ip
	added, err := s.rdb.SAdd(ctx, key, physHash).Result()
	if err != nil {
		return false, 0, fmt.Errorf("cacheStore.AddIPFingerprint SAdd: %w", err)
	}
	// Set TTL on first add
	if added == 1 {
		if err := s.rdb.Expire(ctx, key, window).Err(); err != nil {
			return false, 0, fmt.Errorf("cacheStore.AddIPFingerprint Expire: %w", err)
		}
	}
	count, err := s.rdb.SCard(ctx, key).Result()
	if err != nil {
		return false, 0, fmt.Errorf("cacheStore.AddIPFingerprint SCard: %w", err)
	}
	return added > 0, count, nil
}

func (s *cacheStore) IncrGlobalPending(ctx context.Context) (int64, error) {
	count, err := s.rdb.Incr(ctx, "global:pending_codes").Result()
	if err != nil {
		return 0, fmt.Errorf("cacheStore.IncrGlobalPending: %w", err)
	}
	return count, nil
}

func (s *cacheStore) DecrGlobalPending(ctx context.Context) error {
	if err := s.rdb.Decr(ctx, "global:pending_codes").Err(); err != nil {
		return fmt.Errorf("cacheStore.DecrGlobalPending: %w", err)
	}
	return nil
}

func (s *cacheStore) ReconcileGlobalPending(ctx context.Context) error {
	var cursor uint64
	var count int64
	for {
		keys, nextCursor, err := s.rdb.Scan(ctx, cursor, "verify:*", 100).Result()
		if err != nil {
			return fmt.Errorf("cacheStore.ReconcileGlobalPending Scan: %w", err)
		}
		count += int64(len(keys))
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	if err := s.rdb.Set(ctx, "global:pending_codes", count, 0).Err(); err != nil {
		return fmt.Errorf("cacheStore.ReconcileGlobalPending Set: %w", err)
	}
	return nil
}

func (s *cacheStore) SetReportFingerprint(ctx context.Context, deviceID, physHash string, ttl time.Duration) error {
	if err := s.rdb.Set(ctx, "report_fp:"+deviceID, physHash, ttl).Err(); err != nil {
		return fmt.Errorf("cacheStore.SetReportFingerprint: %w", err)
	}
	return nil
}

func (s *cacheStore) GetReportFingerprint(ctx context.Context, deviceID string) (string, error) {
	val, err := s.rdb.Get(ctx, "report_fp:"+deviceID).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("cacheStore.GetReportFingerprint: %w", err)
	}
	return val, nil
}

func (s *cacheStore) DelReportFingerprint(ctx context.Context, deviceID string) error {
	s.rdb.Del(ctx, "report_fp:"+deviceID) //nolint:errcheck
	return nil
}
