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

// The outbox is leased through next_attempt_at, as in cleanup_outbox: every
// event is a new row and its lease time is the acknowledgement snapshot.
func (s *voipRemarkStore) Save(ctx context.Context, deviceID, peerID, remark string, userID int64, lease time.Duration) (voip.RemarkEvent, error) {
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
	result, err := tx.ExecContext(ctx, `INSERT INTO voip_remark_outbox (device_id,peer_id,next_attempt_at) VALUES (?,?,DATE_ADD(NOW(),INTERVAL ? SECOND))`, deviceID, peerID, int(lease/time.Second))
	if err != nil {
		return event, err
	}
	event.ID, err = result.LastInsertId()
	if err != nil {
		return event, err
	}
	if err = tx.GetContext(ctx, &event.Lease, `SELECT DATE_FORMAT(next_attempt_at,'`+leaseFormat+`') FROM voip_remark_outbox WHERE id=?`, event.ID); err != nil {
		return event, err
	}
	return event, tx.Commit()
}

func (s *voipRemarkStore) Claim(ctx context.Context, limit int, lease time.Duration) ([]voip.RemarkEvent, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var ids []int64
	if err := tx.SelectContext(ctx, &ids, `SELECT id FROM voip_remark_outbox WHERE next_attempt_at<=NOW() ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED`, limit); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, tx.Commit()
	}
	query, args, err := sqlx.In(`UPDATE voip_remark_outbox SET next_attempt_at=DATE_ADD(NOW(),INTERVAL ? SECOND) WHERE id IN (?)`, int(lease/time.Second), ids)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, tx.Rebind(query), args...); err != nil {
		return nil, err
	}
	query, args, err = sqlx.In(`SELECT id,device_id,peer_id,attempts,DATE_FORMAT(next_attempt_at,'`+leaseFormat+`') AS lease FROM voip_remark_outbox WHERE id IN (?) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	var events []voip.RemarkEvent
	if err := tx.SelectContext(ctx, &events, tx.Rebind(query), args...); err != nil {
		return nil, err
	}
	return events, tx.Commit()
}

func (s *voipRemarkStore) Complete(ctx context.Context, event voip.RemarkEvent) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM voip_remark_outbox WHERE id=? AND next_attempt_at=?`, event.ID, event.Lease)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *voipRemarkStore) Retry(ctx context.Context, event voip.RemarkEvent, delay time.Duration) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE voip_remark_outbox SET attempts=LEAST(attempts+1,7),next_attempt_at=DATE_ADD(NOW(),INTERVAL ? SECOND) WHERE id=? AND next_attempt_at=?`, int(delay.Seconds()), event.ID, event.Lease)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}
