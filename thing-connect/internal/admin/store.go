package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

var (
	ErrNotFound       = errors.New("admin: not found")
	ErrConflict       = errors.New("admin: conflict")
	ErrSessionReuse   = errors.New("admin: refresh token reuse")
	ErrSessionExpired = errors.New("admin: session expired")
)

type Store struct{ db *sqlx.DB }

func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

func (s *Store) DB() *sqlx.DB { return s.db }

func (s *Store) SeedDefaults(ctx context.Context) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	roleIDs := make(map[string]int64, len(defaultRoles))
	for _, role := range defaultRoles {
		if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO admin_roles (code,name,sort_no,remark) VALUES (?,?,?,?)`, role.Code, role.Name, role.Sort, role.Remark); err != nil {
			return fmt.Errorf("seed role %s: %w", role.Code, err)
		}
		var roleID int64
		if err := tx.GetContext(ctx, &roleID, `SELECT id FROM admin_roles WHERE code=?`, role.Code); err != nil {
			return fmt.Errorf("load seeded role %s: %w", role.Code, err)
		}
		roleIDs[role.Code] = roleID
		for _, permission := range role.Permissions {
			if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO admin_role_permissions (role_id,permission_code) VALUES (?,?)`, roleID, permission); err != nil {
				return fmt.Errorf("seed role %s permission %s: %w", role.Code, permission, err)
			}
		}
	}
	menuIDs := make(map[string]int64, len(defaultMenus))
	for _, menu := range defaultMenus {
		parentID := menuIDs[menu.ParentCode]
		if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO admin_menus (parent_id,menu_code,name,path,permission_code,menu_type,sort_no) VALUES (?,?,?,?,?,?,?)`, parentID, menu.Code, menu.Name, menu.Path, menu.Permission, menu.MenuType, menu.Sort); err != nil {
			return fmt.Errorf("seed menu %s: %w", menu.Code, err)
		}
		var menuID int64
		if err := tx.GetContext(ctx, &menuID, `SELECT id FROM admin_menus WHERE menu_code=?`, menu.Code); err != nil {
			return fmt.Errorf("load seeded menu %s: %w", menu.Code, err)
		}
		menuIDs[menu.Code] = menuID
	}
	for _, role := range defaultRoles {
		roleID := roleIDs[role.Code]
		if role.Code == "super_admin" {
			if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO admin_role_menus (role_id,menu_id) SELECT ?,id FROM admin_menus`, roleID); err != nil {
				return err
			}
			continue
		}
		for _, menuCode := range role.Menus {
			menuID, ok := menuIDs[menuCode]
			if !ok {
				return fmt.Errorf("seed role %s references unknown menu %s", role.Code, menuCode)
			}
			if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO admin_role_menus (role_id,menu_id) VALUES (?,?)`, roleID, menuID); err != nil {
				return fmt.Errorf("seed role %s menu %s: %w", role.Code, menuCode, err)
			}
		}
	}
	return tx.Commit()
}

func (s *Store) BootstrapAdmin(ctx context.Context, email, nickName, passwordHash string) (int64, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.GetContext(ctx, &count, `SELECT COUNT(*) FROM admin_users FOR UPDATE`); err != nil {
		return 0, err
	}
	if count != 0 {
		return 0, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO admin_users (email,password,nick_name,password_updated_at) VALUES (?,?,?,NOW())`, strings.ToLower(strings.TrimSpace(email)), passwordHash, strings.TrimSpace(nickName))
	if err != nil {
		return 0, err
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_user_roles (admin_user_id,role_id) SELECT ?,id FROM admin_roles WHERE code='super_admin'`, userID); err != nil {
		return 0, err
	}
	return userID, tx.Commit()
}

const adminUserColumns = `id,email,password,nick_name,status,auth_revision,must_change_password,password_updated_at,last_login_ip,last_login_at,remark,created_at,updated_at`

func (s *Store) AdminByEmail(ctx context.Context, email string) (*AdminUser, error) {
	var user AdminUser
	err := s.db.GetContext(ctx, &user, `SELECT `+adminUserColumns+` FROM admin_users WHERE email=?`, strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &user, err
}

func (s *Store) AdminByID(ctx context.Context, id int64) (*AdminUser, error) {
	var user AdminUser
	err := s.db.GetContext(ctx, &user, `SELECT `+adminUserColumns+` FROM admin_users WHERE id=?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &user, err
}

func (s *Store) AdminRoles(ctx context.Context, id int64) ([]string, error) {
	var roles []string
	err := s.db.SelectContext(ctx, &roles, `SELECT r.code FROM admin_roles r JOIN admin_user_roles ur ON ur.role_id=r.id WHERE ur.admin_user_id=? AND r.status=1 ORDER BY r.sort_no,r.id`, id)
	return roles, err
}

func (s *Store) Navigation(ctx context.Context, userID int64) ([]Menu, error) {
	var directRoleIDs []int64
	if err := s.db.SelectContext(ctx, &directRoleIDs, `SELECT r.id FROM admin_roles r JOIN admin_user_roles ur ON ur.role_id=r.id WHERE ur.admin_user_id=? AND r.status=1`, userID); err != nil {
		return nil, err
	}
	if len(directRoleIDs) == 0 {
		return []Menu{}, nil
	}
	type roleParent struct {
		ID       int64 `db:"id"`
		ParentID int64 `db:"parent_id"`
	}
	var roleRows []roleParent
	if err := s.db.SelectContext(ctx, &roleRows, `SELECT id,parent_id FROM admin_roles WHERE status=1`); err != nil {
		return nil, err
	}
	parents := make(map[int64]int64, len(roleRows))
	for _, role := range roleRows {
		parents[role.ID] = role.ParentID
	}
	effective := map[int64]bool{}
	for _, roleID := range directRoleIDs {
		for roleID > 0 && !effective[roleID] {
			effective[roleID] = true
			roleID = parents[roleID]
		}
	}
	roleIDs := make([]int64, 0, len(effective))
	for roleID := range effective {
		roleIDs = append(roleIDs, roleID)
	}
	query, args, err := sqlx.In(`
		SELECT DISTINCT m.id,m.parent_id,m.menu_code,m.name,m.icon,m.path,m.permission_code,
		       m.menu_type,m.sort_no,m.visible,m.status,m.created_at,m.updated_at
		FROM admin_menus m
		JOIN admin_role_menus rm ON rm.menu_id=m.id
		WHERE rm.role_id IN (?) AND m.status=1 AND m.visible=1
		ORDER BY m.sort_no,m.id`, roleIDs)
	if err != nil {
		return nil, err
	}
	var menus []Menu
	err = s.db.SelectContext(ctx, &menus, s.db.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	var allMenus []Menu
	if err := s.db.SelectContext(ctx, &allMenus, `SELECT id,parent_id,menu_code,name,icon,path,permission_code,menu_type,sort_no,visible,status,created_at,updated_at FROM admin_menus WHERE status=1 AND visible=1`); err != nil {
		return nil, err
	}
	byID := make(map[int64]Menu, len(allMenus))
	selected := make(map[int64]bool, len(menus))
	for _, menu := range allMenus {
		byID[menu.ID] = menu
	}
	for _, menu := range menus {
		selected[menu.ID] = true
	}
	assignedMenus := append([]Menu(nil), menus...)
	for _, menu := range assignedMenus {
		parentID := menu.ParentID
		for parentID > 0 && !selected[parentID] {
			parent, ok := byID[parentID]
			if !ok {
				break
			}
			menus = append(menus, parent)
			selected[parent.ID] = true
			parentID = parent.ParentID
		}
	}
	sort.Slice(menus, func(i, j int) bool {
		if menus[i].ParentID != menus[j].ParentID {
			return menus[i].ParentID < menus[j].ParentID
		}
		if menus[i].SortNo != menus[j].SortNo {
			return menus[i].SortNo < menus[j].SortNo
		}
		return menus[i].ID < menus[j].ID
	})
	return menus, err
}

func (s *Store) RecordLogin(ctx context.Context, userID int64, email, ip, userAgent, message string, success bool) error {
	status := 0
	if success {
		status = 1
		_, _ = s.db.ExecContext(ctx, `UPDATE admin_users SET last_login_ip=?,last_login_at=NOW() WHERE id=?`, ip, userID)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO admin_login_log (admin_user_id,email,client_ip,user_agent,status,message) VALUES (?,?,?,?,?,?)`, userID, email, ip, userAgent, status, message)
	return err
}

func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) (Session, error) {
	familyID := uuid.NewString()
	result, err := s.db.ExecContext(ctx, `INSERT INTO admin_sessions (admin_user_id,family_id,token_hash,expires_at) VALUES (?,?,?,?)`, userID, familyID, tokenHash, expiresAt)
	if err != nil {
		return Session{}, err
	}
	id, err := result.LastInsertId()
	return Session{ID: id, AdminUserID: userID, FamilyID: familyID, TokenHash: tokenHash, ExpiresAt: expiresAt}, err
}

// TrimSessions revokes the oldest active sessions above the per-admin limit.
func (s *Store) TrimSessions(ctx context.Context, userID int64, maxSessions int) error {
	if maxSessions <= 0 {
		return nil
	}
	var ids []int64
	if err := s.db.SelectContext(ctx, &ids, `SELECT id FROM admin_sessions WHERE admin_user_id=? AND revoked_at IS NULL AND expires_at>NOW() ORDER BY created_at DESC,id DESC`, userID); err != nil {
		return err
	}
	if len(ids) <= maxSessions {
		return nil
	}
	query, args, err := sqlx.In(`UPDATE admin_sessions SET revoked_at=NOW() WHERE id IN (?)`, ids[maxSessions:])
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.db.Rebind(query), args...)
	return err
}

func (s *Store) RotateSession(ctx context.Context, oldHash, newHash string, expiresAt time.Time) (Session, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	var old Session
	err = tx.GetContext(ctx, &old, `SELECT id,admin_user_id,family_id,token_hash,replaced_by_id,expires_at,revoked_at,revoked_reason,created_at,updated_at FROM admin_sessions WHERE token_hash=? FOR UPDATE`, oldHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if old.RevokedAt != nil {
		_, _ = tx.ExecContext(ctx, `UPDATE admin_sessions SET revoked_at=COALESCE(revoked_at,NOW()),revoked_reason='refresh token reuse' WHERE family_id=?`, old.FamilyID)
		_ = tx.Commit()
		return Session{}, ErrSessionReuse
	}
	if time.Now().After(old.ExpiresAt) {
		return Session{}, ErrSessionExpired
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO admin_sessions (admin_user_id,family_id,token_hash,expires_at) VALUES (?,?,?,?)`, old.AdminUserID, old.FamilyID, newHash, expiresAt)
	if err != nil {
		return Session{}, err
	}
	newID, err := result.LastInsertId()
	if err != nil {
		return Session{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE admin_sessions SET revoked_at=NOW(),revoked_reason='rotated',replaced_by_id=? WHERE id=?`, newID, old.ID); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return Session{ID: newID, AdminUserID: old.AdminUserID, FamilyID: old.FamilyID, TokenHash: newHash, ExpiresAt: expiresAt}, nil
}

func (s *Store) RevokeSessions(ctx context.Context, userID int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE admin_sessions SET revoked_at=COALESCE(revoked_at,NOW()),revoked_reason=IF(revoked_reason='',?,revoked_reason) WHERE admin_user_id=?`, reason, userID)
	return err
}

func (s *Store) RevokeSessionToken(ctx context.Context, tokenHash, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE admin_sessions SET revoked_at=COALESCE(revoked_at,NOW()),revoked_reason=IF(revoked_reason='',?,revoked_reason) WHERE token_hash=?`, reason, tokenHash)
	return err
}

func (s *Store) ActiveMFAFactor(ctx context.Context, userID int64) (*MFAFactor, error) {
	var factor MFAFactor
	err := s.db.GetContext(ctx, &factor, `SELECT id,admin_user_id,factor_type,secret_enc,status,last_used_step,confirmed_at,created_at,updated_at FROM admin_mfa_factors WHERE admin_user_id=? AND factor_type='totp' AND status=1`, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &factor, err
}

func (s *Store) MFAFactor(ctx context.Context, userID int64) (*MFAFactor, error) {
	var factor MFAFactor
	err := s.db.GetContext(ctx, &factor, `SELECT id,admin_user_id,factor_type,secret_enc,status,last_used_step,confirmed_at,created_at,updated_at FROM admin_mfa_factors WHERE admin_user_id=? AND factor_type='totp'`, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &factor, err
}

func (s *Store) SavePendingMFA(ctx context.Context, userID int64, secretEnc string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO admin_mfa_factors (admin_user_id,factor_type,secret_enc,status) VALUES (?,'totp',?,0) ON DUPLICATE KEY UPDATE secret_enc=VALUES(secret_enc),status=0,last_used_step=0,confirmed_at=NULL,created_at=NOW(),updated_at=NOW()`, userID, secretEnc)
	return err
}

func (s *Store) ConfirmMFA(ctx context.Context, userID, step int64, codeHashes []string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE admin_mfa_factors SET status=1,last_used_step=?,confirmed_at=NOW() WHERE admin_user_id=? AND factor_type='totp' AND status=0`, step, userID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_mfa_recovery_codes WHERE admin_user_id=?`, userID); err != nil {
		return err
	}
	for _, hash := range codeHashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_mfa_recovery_codes (admin_user_id,code_hash) VALUES (?,?)`, userID, hash); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET auth_revision=auth_revision+1 WHERE id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkMFAStep(ctx context.Context, factorID, expectedLast, step int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE admin_mfa_factors SET last_used_step=? WHERE id=? AND last_used_step=? AND status=1`, step, factorID, expectedLast)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID int64, hash string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE admin_mfa_recovery_codes SET used_at=NOW() WHERE admin_user_id=? AND code_hash=? AND used_at IS NULL`, userID, hash)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID int64, hashes []string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_mfa_recovery_codes WHERE admin_user_id=?`, userID); err != nil {
		return err
	}
	for _, hash := range hashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_mfa_recovery_codes (admin_user_id,code_hash) VALUES (?,?)`, userID, hash); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Audit(ctx context.Context, event AuditEvent) error {
	success := 0
	if event.Success {
		success = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO admin_audit_log (admin_user_id,role_codes,request_id,method,path,http_status,latency_ms,action,resource_type,resource_id,reason,before_value,after_value,client_ip,user_agent,success,error_message) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, event.AdminUserID, event.RoleCodes, event.RequestID, event.Method, event.Path, event.HTTPStatus, event.LatencyMS, event.Action, event.Resource, event.ResourceID, event.Reason, nullableText(event.Before), nullableText(event.After), event.ClientIP, event.UserAgent, success, event.Error)
	return err
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
