package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
	"thing-connect/internal/deviceprofile"
)

type deviceProfileStore struct{ db *sqlx.DB }

func NewDeviceProfileStore(db *sqlx.DB) deviceprofile.Store { return &deviceProfileStore{db: db} }

func (s *deviceProfileStore) ReplaceScenes(ctx context.Context, deviceID string, profiles map[string]json.RawMessage) (bool, error) {
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
		return false, fmt.Errorf("lock device media binding: %w", err)
	}
	raw, err := json.Marshal(profiles)
	if err != nil {
		return false, err
	}
	// JSON_SET replaces complete scenes, unlike a recursive JSON merge. The bind
	// lock serializes reports and unbinding without holding a remote operation.
	keys := make([]string, 0, len(profiles))
	for key := range profiles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	clauses := make([]string, 0, len(keys))
	args := []any{deviceID, string(raw)}
	for _, key := range keys {
		clauses = append(clauses, "?, CAST(? AS JSON)")
		args = append(args, "$."+key, string(profiles[key]))
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO device_profile(device_id,profile) VALUES(?,?)
 ON DUPLICATE KEY UPDATE profile=JSON_SET(profile,`+strings.Join(clauses, ",")+`)`, args...)
	if err != nil {
		return false, fmt.Errorf("save device media: %w", err)
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
