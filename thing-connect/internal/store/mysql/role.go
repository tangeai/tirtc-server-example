package mysql

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/store"
)

type roleBindingStore struct{ db *sqlx.DB }

func NewRoleBindingStore(db *sqlx.DB) store.RoleBindingStore { return &roleBindingStore{db} }

func (s *roleBindingStore) GetDeviceRole(ctx context.Context, deviceID string) (string, error) {
	var roleID string
	err := s.db.GetContext(ctx, &roleID,
		`SELECT role_id FROM ai_device_role WHERE device_id=?`, deviceID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("roleBindingStore.GetDeviceRole: %w", err)
	}
	return roleID, nil
}

func (s *roleBindingStore) SetDeviceRole(ctx context.Context, deviceID, roleID string, userID int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_device_role (device_id, role_id, user_id)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE role_id=VALUES(role_id), user_id=VALUES(user_id)`,
		deviceID, roleID, userID)
	if err != nil {
		return fmt.Errorf("roleBindingStore.SetDeviceRole: %w", err)
	}
	return nil
}

func (s *roleBindingStore) DeleteDeviceRole(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM ai_device_role WHERE device_id=?`, deviceID)
	if err != nil {
		return fmt.Errorf("roleBindingStore.DeleteDeviceRole: %w", err)
	}
	return nil
}

func (s *roleBindingStore) UserOwnsDevice(ctx context.Context, deviceID string, userID int64) (bool, error) {
	var exists bool
	err := s.db.GetContext(ctx, &exists,
		`SELECT EXISTS(SELECT 1 FROM device_bind WHERE device_id=? AND user_id=?)`, deviceID, userID)
	if err != nil {
		return false, fmt.Errorf("roleBindingStore.UserOwnsDevice: %w", err)
	}
	return exists, nil
}

// ── UserRoleStore ──

type userRoleStore struct{ db *sqlx.DB }

func NewUserRoleStore(db *sqlx.DB) store.UserRoleStore { return &userRoleStore{db} }

func (s *userRoleStore) ListUserRoleIDs(ctx context.Context, userID int64) ([]string, error) {
	var ids []string
	err := s.db.SelectContext(ctx, &ids,
		`SELECT role_id FROM ai_user_role WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("userRoleStore.ListUserRoleIDs: %w", err)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

func (s *userRoleStore) AddUserRole(ctx context.Context, userID int64, roleID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT IGNORE INTO ai_user_role (user_id, role_id) VALUES (?, ?)`, userID, roleID)
	if err != nil {
		return fmt.Errorf("userRoleStore.AddUserRole: %w", err)
	}
	return nil
}

func (s *userRoleStore) RemoveUserRole(ctx context.Context, userID int64, roleID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM ai_user_role WHERE user_id=? AND role_id=?`, userID, roleID)
	if err != nil {
		return fmt.Errorf("userRoleStore.RemoveUserRole: %w", err)
	}
	return nil
}

func (s *userRoleStore) ExistsUserRole(ctx context.Context, userID int64, roleID string) (bool, error) {
	var exists bool
	err := s.db.GetContext(ctx, &exists,
		`SELECT EXISTS(SELECT 1 FROM ai_user_role WHERE user_id=? AND role_id=?)`, userID, roleID)
	if err != nil {
		return false, fmt.Errorf("userRoleStore.ExistsUserRole: %w", err)
	}
	return exists, nil
}
