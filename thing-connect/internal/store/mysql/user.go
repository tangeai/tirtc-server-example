package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/model"
	"thing-connect/internal/store"
)

type userStore struct{ db *sqlx.DB }

func NewUserStore(db *sqlx.DB) store.UserStore { return &userStore{db} }

func (s *userStore) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	var u model.User
	err := s.db.GetContext(ctx, &u, `SELECT * FROM users WHERE email=?`, email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // absence is the UserStore lookup contract
	}
	if err != nil {
		return nil, fmt.Errorf("userStore.GetUserByEmail: %w", err)
	}
	return &u, nil
}

func (s *userStore) CreateUser(ctx context.Context, email, passwordHash string, bindQuota int) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (email, password, bind_quota) VALUES (?, ?, ?)`, email, passwordHash, bindQuota)
	if err != nil {
		return 0, fmt.Errorf("userStore.CreateUser: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("userStore.CreateUser LastInsertId: %w", err)
	}
	return id, nil
}

func (s *userStore) UpdatePassword(ctx context.Context, userID int64, passwordHash string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET password=?,auth_revision=auth_revision+1 WHERE id=?`, passwordHash, userID); err != nil {
		return fmt.Errorf("userStore.UpdatePassword: %w", err)
	}
	return nil
}

func (s *userStore) GetQuota(ctx context.Context, userID int64) (int, error) {
	var q int
	if err := s.db.GetContext(ctx, &q,
		`SELECT bind_quota FROM users WHERE id=?`, userID); err != nil {
		return 0, fmt.Errorf("userStore.GetQuota: %w", err)
	}
	return q, nil
}

func (s *userStore) GetDeviceList(ctx context.Context, userID int64) ([]model.UserDeviceRow, error) {
	var rows []model.UserDeviceRow
	err := s.db.SelectContext(ctx, &rows, `
		SELECT db.device_id,
		       db.device_name,
		       1 AS status,
		       db.mac,
		       DATE_FORMAT(db.bind_time,'%Y-%m-%dT%T') AS bind_time,
		       vdp.profile AS voip_profile
		FROM device_bind db
		LEFT JOIN voip_device_profile vdp ON vdp.device_id = db.device_id
		WHERE db.user_id=?
		ORDER BY db.bind_time DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("userStore.GetDeviceList: %w", err)
	}
	return rows, nil
}

func (s *userStore) UpdateDeviceName(
	ctx context.Context, userID int64, deviceID, deviceName string,
) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE device_bind SET device_name=? WHERE device_id=? AND user_id=?`,
		deviceName, deviceID, userID)
	if err != nil {
		return false, fmt.Errorf("userStore.UpdateDeviceName: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("userStore.UpdateDeviceName rows affected: %w", err)
	}
	return n > 0, nil
}
