package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"thing-connect/internal/deviceprofile"
)

type deviceProfileStore struct{ db *sqlx.DB }

func NewDeviceProfileStore(db *sqlx.DB) deviceprofile.Store { return &deviceProfileStore{db: db} }

func (s *deviceProfileStore) Replace(ctx context.Context, deviceID string, report deviceprofile.ReportInput) (bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var owner int64
	err = tx.GetContext(ctx, &owner, `SELECT user_id FROM device_bind WHERE device_id=? FOR UPDATE`, deviceID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner == 0) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock device binding: %w", err)
	}
	// The binding lock serializes reports and unbinding for one device. Merge the
	// partial report with the stored document so omitted scenes and metadata are
	// retained, then skip an identical snapshot to avoid redo/binlog churn.
	stored := make(map[string]json.RawMessage)
	var previous string
	err = tx.GetContext(ctx, &previous, `SELECT profile FROM device_profile WHERE device_id=?`, deviceID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("get device profile for update: %w", err)
	}
	if err == nil {
		if unmarshalErr := json.Unmarshal([]byte(previous), &stored); unmarshalErr != nil {
			return false, fmt.Errorf("decode device profile: %w", unmarshalErr)
		}
	}
	previousCanonical, err := json.Marshal(stored)
	if err != nil {
		return false, err
	}
	for scene, raw := range report.Profiles {
		stored[scene] = raw
	}
	if report.Hardware != nil && (report.Hardware.ChipModel != nil || report.Hardware.BoardModel != nil) {
		hardware := make(map[string]json.RawMessage)
		if current := stored["hardware"]; len(current) > 0 {
			_ = json.Unmarshal(current, &hardware)
		}
		if report.Hardware.ChipModel != nil {
			hardware["chip_model"], _ = json.Marshal(*report.Hardware.ChipModel)
		}
		if report.Hardware.BoardModel != nil {
			hardware["board_model"], _ = json.Marshal(*report.Hardware.BoardModel)
		}
		stored["hardware"], _ = json.Marshal(hardware)
	}
	if report.FirmwareVersion != nil {
		stored["firmware_version"], _ = json.Marshal(*report.FirmwareVersion)
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return false, err
	}
	if string(raw) == string(previousCanonical) {
		if err = tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO device_profile(device_id,profile) VALUES(?,?)
 ON DUPLICATE KEY UPDATE profile=VALUES(profile)`, deviceID, string(raw))
	if err != nil {
		return false, fmt.Errorf("save device profile: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *deviceProfileStore) Get(ctx context.Context, deviceID string) (*deviceprofile.Snapshot, error) {
	var raw string
	err := s.db.GetContext(ctx, &raw, `SELECT profile FROM device_profile WHERE device_id=?`, deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return &deviceprofile.Snapshot{DeviceID: deviceID}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get device profile: %w", err)
	}
	return &deviceprofile.Snapshot{DeviceID: deviceID, Profile: json.RawMessage(raw)}, nil
}
