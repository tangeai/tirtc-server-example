package mysql

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"thing-connect/internal/push"
)

func (s *PushStore) ClaimPush(ctx context.Context) (*push.Delivery, error) {
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var d push.Delivery
	err = tx.GetContext(ctx, &d, `SELECT id,attempts+1 AS attempts,device_id AS device,event_type AS kind,template_id AS template,reference_id AS reference,DATE_FORMAT(TIMESTAMPADD(HOUR,8,occurred_at),'%Y-%m-%d %H:%i:%s') AS time FROM push_delivery WHERE status='pending' AND next_at<=UTC_TIMESTAMP(6) AND started_at>UTC_TIMESTAMP(6)-INTERVAL 10 MINUTE ORDER BY next_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, push.ErrQueueEmpty
	}
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowxContext(ctx, `SELECT r.open_id,r.app_id FROM push_delivery d JOIN device_bind b ON d.device_id=b.device_id AND b.user_id=d.user_id AND CONCAT(b.id,':',b.binding_revision)=d.bind_id JOIN push_recipient r ON r.user_id=d.user_id AND r.generation=d.generation JOIN push_subscription s ON s.generation=d.generation AND s.template_id=d.template_id WHERE d.id=? FOR SHARE`, d.ID).Scan(&d.Open, &d.AppID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `UPDATE push_delivery SET status='cancelled',finished_at=UTC_TIMESTAMP(6) WHERE id=?`, d.ID)
		if err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	d.Lease = uuid.NewString()
	res, err := tx.ExecContext(ctx, `UPDATE push_delivery SET status='sending',attempts=attempts+1,lease=?,next_at=UTC_TIMESTAMP(6)+INTERVAL 30 SECOND WHERE id=? AND started_at>UTC_TIMESTAMP(6)-INTERVAL 10 MINUTE`, d.Lease, d.ID)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, push.ErrQueueEmpty
	}
	return &d, tx.Commit()
}
func (s *PushStore) FinishPush(ctx context.Context, d push.Delivery, result push.Result) error {
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE push_delivery SET status=?,finished_at=IF(?='pending',NULL,UTC_TIMESTAMP(6)),next_at=TIMESTAMPADD(SECOND,1<<attempts,UTC_TIMESTAMP(6)),lease='' WHERE id=? AND lease=? AND status='sending'`, result, result, d.ID, d.Lease)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 && result == push.Unsubscribed {
		_, err = tx.ExecContext(ctx, `DELETE s FROM push_subscription s JOIN push_delivery d ON d.generation=s.generation AND d.template_id=s.template_id AND d.grant_id=s.grant_id WHERE d.id=?`, d.ID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *PushStore) MaintainPush(ctx context.Context) error {
	for _, q := range []string{
		`UPDATE push_delivery SET status='expired',finished_at=UTC_TIMESTAMP(6) WHERE status='pending' AND started_at<=UTC_TIMESTAMP(6)-INTERVAL 10 MINUTE LIMIT 1000`,
		`UPDATE push_delivery SET status='unknown',finished_at=UTC_TIMESTAMP(6) WHERE status='sending' AND next_at<=UTC_TIMESTAMP(6) LIMIT 1000`,
		`DELETE FROM push_event WHERE received_at<=UTC_TIMESTAMP(6)-INTERVAL 72 HOUR LIMIT 1000`,
		`DELETE FROM push_delivery WHERE finished_at<=UTC_TIMESTAMP(6)-INTERVAL 7 DAY LIMIT 1000`,
		`DELETE FROM push_session WHERE expires_at<=UTC_TIMESTAMP(6) LIMIT 1000`,
	} {
		if err := cleanPushBatch(ctx, s.db, q); err != nil {
			return err
		}
	}
	return nil
}

// Use READ COMMITTED for range maintenance so empty ranges do not lock inserts.
func cleanPushBatch(ctx context.Context, db *sqlx.DB, query string) error {
	tx, err := db.BeginTxx(ctx, claimTx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, query); err != nil {
		return err
	}
	return tx.Commit()
}
