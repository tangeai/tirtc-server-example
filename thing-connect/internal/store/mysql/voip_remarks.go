package mysql

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jmoiron/sqlx"
	"thing-connect/internal/voip"
	"time"
)

type voipRemarkStore struct{ db *sqlx.DB }

func NewVoipRemarkStore(db *sqlx.DB) voip.RemarkStore { return &voipRemarkStore{db: db} }
func (s *voipRemarkStore) Save(ctx context.Context, deviceID, peerID, remark string, userID int64) (voip.RemarkEvent, error) {
	event := voip.RemarkEvent{DeviceID: deviceID, PeerID: peerID}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return event, err
	}
	defer tx.Rollback()
	var owner int64
	if err = tx.GetContext(ctx, &owner, `SELECT user_id FROM device_bind WHERE device_id=? FOR UPDATE`, deviceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = voip.ErrContactMissing
		}
		return event, err
	}
	if owner == 0 || (userID != 0 && owner != userID) {
		return event, voip.ErrContactMissing
	}
	var id int64
	// Keep the deployed peer_id-only selection rule (most recent active AppID).
	if err = tx.GetContext(ctx, &id, `SELECT id FROM voip_device_auth WHERE device_id=? AND wx_open_id=? AND auth_status='active' ORDER BY created_at DESC,id DESC LIMIT 1 FOR UPDATE`, deviceID, peerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = voip.ErrContactMissing
		}
		return event, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE voip_device_auth SET device_remark=? WHERE id=?`, remark, id); err != nil {
		return event, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO voip_remark_outbox (device_id,peer_id) VALUES (?,?)`, deviceID, peerID)
	if err != nil {
		return event, err
	}
	event.ID, err = result.LastInsertId()
	if err != nil {
		return event, err
	}
	return event, tx.Commit()
}
func (s *voipRemarkStore) Pending(ctx context.Context) ([]voip.RemarkEvent, error) {
	var events []voip.RemarkEvent
	err := s.db.SelectContext(ctx, &events, `SELECT id,device_id,peer_id,attempts FROM voip_remark_outbox WHERE next_attempt_at<=NOW() ORDER BY id LIMIT 20`)
	return events, err
}
func (s *voipRemarkStore) Complete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM voip_remark_outbox WHERE id=?`, id)
	return err
}
func (s *voipRemarkStore) Retry(ctx context.Context, id int64, delay time.Duration) error {
	_, err := s.db.ExecContext(ctx, `UPDATE voip_remark_outbox SET attempts=LEAST(attempts+1,7),next_attempt_at=DATE_ADD(NOW(),INTERVAL ? SECOND) WHERE id=?`, int(delay.Seconds()), id)
	return err
}
