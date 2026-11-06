package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"github.com/jmoiron/sqlx"
	"sort"
	"thing-connect/internal/ai"
	"time"
)

type aiRoleLifecycleStore struct{ db *sqlx.DB }

type roleConnectionKey struct{}
type roleExecutor interface {
	GetContext(context.Context, any, string, ...any) error
	SelectContext(context.Context, any, string, ...any) error
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	BeginTxx(context.Context, *sql.TxOptions) (*sqlx.Tx, error)
}

// All protected reads/writes use the connection that owns GET_LOCK. A lost
// connection must fail the operation rather than silently use an unlocked one.
func (s *aiRoleLifecycleStore) executor(ctx context.Context) roleExecutor {
	if conn, ok := ctx.Value(roleConnectionKey{}).(*sqlx.Conn); ok {
		return conn
	}
	return s.db
}

func NewRoleLifecycleStore(db *sqlx.DB) ai.RoleLifecycleStore { return &aiRoleLifecycleStore{db: db} }
func (s *aiRoleLifecycleStore) WithAccount(ctx context.Context, user int64, fn func(context.Context) error) error {
	return s.withLocks(ctx, []string{fmt.Sprintf("tc:ai-role:%d", user)}, fn)
}
func (s *aiRoleLifecycleStore) WithDevices(ctx context.Context, devices []string, fn func(context.Context) error) error {
	keys := make([]string, 0, len(devices))
	seen := map[string]bool{}
	for _, id := range devices {
		key := fmt.Sprintf("tc:ai-dev:%x", sha256.Sum256([]byte(id)))[:60]
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	sort.Strings(keys)
	return s.withLocks(ctx, keys, fn)
}
func (s *aiRoleLifecycleStore) withLocks(ctx context.Context, keys []string, fn func(context.Context) error) error {
	conn, shared := ctx.Value(roleConnectionKey{}).(*sqlx.Conn)
	if !shared {
		var err error
		conn, err = s.db.Connx(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
	}
	locked := []string{}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for i := len(locked) - 1; i >= 0; i-- {
			if _, err := conn.ExecContext(c, `SELECT RELEASE_LOCK(?)`, locked[i]); err != nil {
				_ = conn.Conn.Raw(func(any) error { return driver.ErrBadConn })
				break
			}
		}
	}()
	for _, key := range keys {
		var ok sql.NullInt64
		if err := conn.GetContext(ctx, &ok, `SELECT GET_LOCK(?,5)`, key); err != nil {
			return err
		}
		if !ok.Valid || ok.Int64 != 1 {
			return ai.ErrRoleBusy
		}
		locked = append(locked, key)
	}
	return fn(context.WithValue(ctx, roleConnectionKey{}, conn))
}
func (s *aiRoleLifecycleStore) CheckRole(ctx context.Context, user int64, role string, allowDeleting bool) error {
	var deleting bool
	err := s.executor(ctx).GetContext(ctx, &deleting, `SELECT deleting FROM ai_user_role WHERE user_id=? AND role_id=?`, user, role)
	if errors.Is(err, sql.ErrNoRows) {
		return ai.ErrRoleForbidden
	}
	if err != nil {
		return err
	}
	if deleting && !allowDeleting {
		return ai.ErrRoleDeleting
	}
	return nil
}
func (s *aiRoleLifecycleStore) Devices(ctx context.Context, user int64) ([]string, error) {
	var ids []string
	err := s.executor(ctx).SelectContext(ctx, &ids, `SELECT device_id FROM device_bind WHERE user_id=? ORDER BY device_id`, user)
	return ids, err
}
func (s *aiRoleLifecycleStore) Usage(ctx context.Context, role string) ([]ai.RoleDevice, error) {
	var rows []ai.RoleDevice
	err := s.executor(ctx).SelectContext(ctx, &rows, `SELECT DISTINCT refs.device_id,COALESCE(d.device_name,'') AS device_name FROM (SELECT device_id FROM ai_device_role WHERE role_id=? UNION SELECT device_id FROM ai_cloud_role_ref WHERE role_id=?) refs LEFT JOIN device_bind d ON d.device_id=refs.device_id`, role, role)
	return rows, err
}
func (s *aiRoleLifecycleStore) MarkDeleting(ctx context.Context, user int64, role string) error {
	_, err := s.executor(ctx).ExecContext(ctx, `UPDATE ai_user_role SET deleting=1 WHERE user_id=? AND role_id=?`, user, role)
	return err
}
func (s *aiRoleLifecycleStore) RemoveRole(ctx context.Context, user int64, role string) error {
	_, err := s.executor(ctx).ExecContext(ctx, `DELETE FROM ai_user_role WHERE user_id=? AND role_id=? AND deleting=1`, user, role)
	return err
}
func (s *aiRoleLifecycleStore) SetLocal(ctx context.Context, user int64, device, role string) error {
	tx, err := s.executor(ctx).BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockRoleDevice(ctx, tx, user, device); err != nil {
		return err
	}
	if role == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM ai_device_role WHERE device_id=?`, device)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_device_role(device_id,role_id,user_id,binding_revision) SELECT device_id,?,?,binding_revision FROM device_bind WHERE device_id=? ON DUPLICATE KEY UPDATE role_id=VALUES(role_id),user_id=VALUES(user_id),binding_revision=VALUES(binding_revision)`, role, user, device)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func lockRoleDevice(ctx context.Context, tx *sqlx.Tx, user int64, device string) error {
	var owner int64
	err := tx.GetContext(ctx, &owner, `SELECT user_id FROM device_bind WHERE device_id=? FOR UPDATE`, device)
	if errors.Is(err, sql.ErrNoRows) {
		return ai.ErrRoleForbidden
	}
	if err != nil {
		return err
	}
	if owner != user {
		return ai.ErrRoleForbidden
	}
	return nil
}

// PrepareCloud persists the intended mutation and deletion protection before
// any remote work. Device row locks fence ownership changes during this commit.
func (s *aiRoleLifecycleStore) PrepareCloud(ctx context.Context, user int64, devices []string, role string, remove bool) ([]ai.CloudTarget, error) {
	tx, err := s.executor(ctx).BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	targets := make([]ai.CloudTarget, 0, len(devices))
	seen := map[string]bool{}
	for _, device := range devices {
		if seen[device] {
			continue
		}
		seen[device] = true
		var owner, revision int64
		err = tx.QueryRowxContext(ctx, `SELECT user_id,binding_revision FROM device_bind WHERE device_id=? FOR UPDATE`, device).Scan(&owner, &revision)
		if errors.Is(err, sql.ErrNoRows) || err == nil && owner != user {
			return nil, ai.ErrRoleForbidden
		}
		if err != nil {
			return nil, err
		}
		// A previous generation must be reconciled before a new user operation can
		// replace its target; otherwise a delayed cleanup could erase the new role.
		var previous int64
		e := tx.GetContext(ctx, &previous, `SELECT binding_revision FROM ai_cloud_role_target WHERE device_id=?`, device)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if e == nil && previous != revision {
			return nil, ai.ErrRoleBusy
		}
		target := ai.CloudTarget{DeviceID: device, UserID: user, Revision: revision, RoleID: role, Remove: remove}
		if _, err = tx.ExecContext(ctx, `INSERT INTO ai_cloud_role_target(device_id,user_id,binding_revision,role_id,remove_role,next_attempt_at) VALUES(?,?,?,?,?,NOW()) ON DUPLICATE KEY UPDATE user_id=VALUES(user_id),binding_revision=VALUES(binding_revision),role_id=VALUES(role_id),remove_role=VALUES(remove_role),attempts=0,next_attempt_at=NOW()`, device, user, revision, role, remove); err != nil {
			return nil, err
		}
		if !remove {
			if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO ai_cloud_role_ref(device_id,role_id,user_id) VALUES(?,?,?)`, device, role, user); err != nil {
				return nil, err
			}
		}
		targets = append(targets, target)
	}
	return targets, tx.Commit()
}
func (s *aiRoleLifecycleStore) CloudTarget(ctx context.Context, device string, cleanup bool) (ai.CloudTarget, error) {
	tx, err := s.executor(ctx).BeginTxx(ctx, nil)
	if err != nil {
		return ai.CloudTarget{}, err
	}
	defer tx.Rollback()
	var owner, revision int64
	err = tx.QueryRowxContext(ctx, `SELECT user_id,binding_revision FROM device_bind WHERE device_id=? FOR UPDATE`, device).Scan(&owner, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ai.CloudTarget{}, err
	}
	target := ai.CloudTarget{}
	e := tx.GetContext(ctx, &target, `SELECT device_id,user_id,binding_revision,role_id,remove_role FROM ai_cloud_role_target WHERE device_id=?`, device)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return target, e
	}
	// Local roles carry the generation too, so an old cleanup cannot remove a
	// role selected after rebinding, even when the same account reclaims it.
	if cleanup || e == nil && target.Revision != revision {
		if _, err = tx.ExecContext(ctx, `DELETE FROM ai_device_role WHERE device_id=? AND (binding_revision<>? OR user_id<>? OR ?=0)`, device, revision, owner, owner); err != nil {
			return target, err
		}
	}
	if errors.Is(e, sql.ErrNoRows) && owner != 0 {
		if err = tx.Commit(); err != nil {
			return target, err
		}
		return target, ai.ErrCleanupSuperseded
	}
	if e != nil || target.Revision != revision || target.UserID != owner {
		target = ai.CloudTarget{DeviceID: device, UserID: owner, Revision: revision, Remove: true}
		if _, err = tx.ExecContext(ctx, `INSERT INTO ai_cloud_role_target(device_id,user_id,binding_revision,role_id,remove_role,next_attempt_at) VALUES(?,?,?,'',1,NOW()) ON DUPLICATE KEY UPDATE user_id=VALUES(user_id),binding_revision=VALUES(binding_revision),role_id='',remove_role=1,attempts=0,next_attempt_at=NOW()`, device, owner, revision); err != nil {
			return target, err
		}
	}
	return target, tx.Commit()
}
func (s *aiRoleLifecycleStore) CompleteCloud(ctx context.Context, target ai.CloudTarget) (bool, error) {
	tx, err := s.executor(ctx).BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var owner, revision int64
	err = tx.QueryRowxContext(ctx, `SELECT user_id,binding_revision FROM device_bind WHERE device_id=? FOR UPDATE`, target.DeviceID).Scan(&owner, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if owner != target.UserID || revision != target.Revision {
		return false, nil
	}
	if target.Remove {
		if target.RoleID == "" {
			_, err = tx.ExecContext(ctx, `DELETE FROM ai_cloud_role_ref WHERE device_id=?`, target.DeviceID)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM ai_cloud_role_ref WHERE device_id=? AND role_id=?`, target.DeviceID, target.RoleID)
		}
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM ai_cloud_role_ref WHERE device_id=? AND role_id<>?`, target.DeviceID, target.RoleID)
	}
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE ai_cloud_role_target SET attempts=0,next_attempt_at=DATE_ADD(NOW(),INTERVAL 30 SECOND) WHERE device_id=?`, target.DeviceID)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}
func (s *aiRoleLifecycleStore) DueCloud(ctx context.Context) ([]string, error) {
	var ids []string
	err := s.db.SelectContext(ctx, &ids, `SELECT device_id FROM ai_cloud_role_target WHERE next_attempt_at<=NOW() ORDER BY next_attempt_at,device_id LIMIT 100`)
	return ids, err
}
func (s *aiRoleLifecycleStore) DelayCloud(ctx context.Context, device string, failed bool) error {
	if !failed {
		return nil
	}
	_, err := s.executor(ctx).ExecContext(ctx, `UPDATE ai_cloud_role_target SET next_attempt_at=DATE_ADD(NOW(),INTERVAL LEAST(300,POW(2,LEAST(attempts,8))) SECOND),attempts=LEAST(attempts+1,9) WHERE device_id=?`, device)
	return err
}
