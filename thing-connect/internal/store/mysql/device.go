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

type deviceStore struct{ db *sqlx.DB }

func NewDeviceStore(db *sqlx.DB) store.DeviceStore { return &deviceStore{db} }

func (s *deviceStore) GetBindByDeviceID(ctx context.Context, deviceID string) (*model.DeviceBind, error) {
	var r model.DeviceBind
	err := s.db.GetContext(ctx, &r,
		`SELECT * FROM device_bind WHERE device_id=? LIMIT 1`, deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // absence is the DeviceStore lookup contract
	}
	if err != nil {
		return nil, fmt.Errorf("deviceStore.GetBindByDeviceID: %w", err)
	}
	return &r, nil
}

func (s *deviceStore) GetDeviceKey(ctx context.Context, deviceID string) (*model.DevicePool, error) {
	var r model.DevicePool
	err := s.db.GetContext(ctx, &r,
		`SELECT device_id, device_key FROM device_pool WHERE device_id=?`, deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // absence is the DeviceStore lookup contract
	}
	if err != nil {
		return nil, fmt.Errorf("deviceStore.GetDeviceKey: %w", err)
	}
	return &r, nil
}

func (s *deviceStore) UpdateActiveTimeIfEmpty(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE device_bind SET active_time=NOW() WHERE device_id=? AND active_time IS NULL AND user_id!=0`,
		deviceID)
	if err != nil {
		return fmt.Errorf("deviceStore.UpdateActiveTimeIfEmpty: %w", err)
	}
	return nil
}

func (s *deviceStore) GetBindByFingerprint(ctx context.Context, mac string, userID int64) (*model.DeviceBind, error) {
	return getBindByMAC(ctx, s.db, mac, userID)
}
