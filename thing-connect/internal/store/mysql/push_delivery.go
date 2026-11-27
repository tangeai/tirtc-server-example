package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

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

	var row struct {
		push.Delivery
		Remaining int64
		Age       int64
	}
	claimed := time.Now()
	preferRetry := s.claims.Add(1)%5 == 0
	const selectDue = `SELECT id,TIMESTAMPDIFF(MICROSECOND,UTC_TIMESTAMP(6),expires_at) AS remaining,TIMESTAMPDIFF(MICROSECOND,occurred_at,UTC_TIMESTAMP(6)) AS age,max_attempts AS maxattempts,attempts,device_id AS device,event_type AS kind,template_id AS template,reference_id AS reference,DATE_FORMAT(TIMESTAMPADD(HOUR,8,occurred_at),'%Y-%m-%d %H:%i:%s') AS time FROM push_delivery WHERE status='pending' AND next_at<=UTC_TIMESTAMP(6) AND expires_at>UTC_TIMESTAMP(6) AND attempts<max_attempts AND ((attempts>0)=?) ORDER BY next_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`
	err = tx.GetContext(ctx, &row, selectDue, preferRetry)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.GetContext(ctx, &row, selectDue, !preferRetry)
	}

	if errors.Is(err, sql.ErrNoRows) {
		return nil, push.ErrQueueEmpty
	}
	if err != nil {
		return nil, err
	}
	d = row.Delivery
	d.Occurred = claimed.Add(-time.Duration(row.Age) * time.Microsecond)

	d.Expires = claimed.Add(time.Duration(row.Remaining) * time.Microsecond)
	err = tx.QueryRowxContext(ctx, `SELECT r.open_id,r.app_id,e.label_text FROM push_delivery d JOIN push_event e ON e.id=d.event_id JOIN device_bind b ON d.device_id=b.device_id AND b.user_id=d.user_id AND CONCAT(b.id,':',b.binding_revision)=d.bind_id JOIN push_recipient r ON r.user_id=d.user_id AND r.generation=d.generation JOIN push_subscription s ON s.granted AND s.generation=d.generation AND s.template_id=d.template_id WHERE d.id=? FOR SHARE`, d.ID).Scan(&d.Open, &d.AppID, &d.LabelText)
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
	res, err := tx.ExecContext(ctx, `UPDATE push_delivery SET status='leased',lease=?,next_at=UTC_TIMESTAMP(6)+INTERVAL 30 SECOND WHERE id=? AND expires_at>UTC_TIMESTAMP(6) AND attempts<max_attempts`, d.Lease, d.ID)
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
	limited := result == push.Limited
	if limited || result == push.Deferred {
		result = push.Retry
	}
	res, err := tx.ExecContext(ctx, `UPDATE push_delivery SET status=IF(?='pending' AND attempts>=max_attempts,'failed',?),finished_at=IF(status='pending',NULL,UTC_TIMESTAMP(6)),next_at=TIMESTAMPADD(SECOND,1+FLOOR(RAND()*LEAST(30,1<<attempts)),UTC_TIMESTAMP(6)),lease='' WHERE id=? AND lease=? AND status IN ('leased','sending')`, result, result, d.ID, d.Lease)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		// Only the current probe may change an existing cooldown. Late
		// responses from the previous burst cannot release or extend it.
		switch {
		case limited:
			_, err = tx.ExecContext(ctx, `UPDATE push_channel_limit SET cooldown_seconds=LEAST(30,GREATEST(2,cooldown_seconds*2)),next_at=TIMESTAMPADD(SECOND,cooldown_seconds,UTC_TIMESTAMP(6)),probe_lease='',recovered_at=NULL WHERE app_id=? AND (cooldown_seconds=0 OR (probe_lease=? AND next_at>UTC_TIMESTAMP(6)))`, d.AppID, d.Lease)
		case result == push.Accepted:
			_, err = tx.ExecContext(ctx, `UPDATE push_channel_limit SET cooldown_seconds=0,probe_lease='',available_at=UTC_TIMESTAMP(6),next_at=UTC_TIMESTAMP(6),recovered_at=UTC_TIMESTAMP(6) WHERE app_id=? AND probe_lease=? AND next_at>UTC_TIMESTAMP(6)`, d.AppID, d.Lease)
		default:
			_, err = tx.ExecContext(ctx, `UPDATE push_channel_limit SET probe_lease='',next_at=TIMESTAMPADD(SECOND,cooldown_seconds,UTC_TIMESTAMP(6)) WHERE app_id=? AND probe_lease=? AND next_at>UTC_TIMESTAMP(6)`, d.AppID, d.Lease)
		}
		if err != nil {
			return err
		}
	}
	if n == 1 && result == push.Unsubscribed {
		_, err = tx.ExecContext(ctx, `UPDATE push_subscription s JOIN push_delivery d ON d.generation=s.generation AND d.template_id=s.template_id AND d.grant_id=s.grant_id SET s.granted=FALSE WHERE d.id=?`, d.ID)
		if err != nil {
			return err
		}
	}
	var outcome string
	if n == 1 {
		outcome = string(result)
		if result == push.Retry {
			if err = tx.GetContext(ctx, &outcome, "SELECT status FROM push_delivery WHERE id=?", d.ID); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if outcome != "" && outcome != "pending" {
		s.outcomes.add("delivery_"+outcome, 1)
	}
	return nil
}
func (s *PushStore) MaintainPush(ctx context.Context) error {
	for _, step := range []struct{ outcome, query string }{
		{"", `UPDATE push_delivery SET expires_at=occurred_at+INTERVAL 120 SECOND WHERE status='pending' AND expires_at IS NULL LIMIT 1000`},
		{"expired", `UPDATE push_delivery SET status='expired',finished_at=UTC_TIMESTAMP(6) WHERE status='pending' AND expires_at<=UTC_TIMESTAMP(6) LIMIT 1000`},
		{"", `UPDATE push_delivery SET status='pending',lease='' WHERE status='leased' AND next_at<=UTC_TIMESTAMP(6) LIMIT 1000`},
		{"failed", `UPDATE push_delivery SET status='failed',finished_at=UTC_TIMESTAMP(6) WHERE status='pending' AND attempts>=max_attempts LIMIT 1000`},
		{"unknown", `UPDATE push_delivery SET status='unknown',finished_at=UTC_TIMESTAMP(6) WHERE status='sending' AND next_at<=UTC_TIMESTAMP(6) LIMIT 1000`},
		{"", `DELETE FROM push_event WHERE received_at<=UTC_TIMESTAMP(6)-INTERVAL 72 HOUR LIMIT 1000`},
		{"", `DELETE FROM push_delivery WHERE finished_at<=UTC_TIMESTAMP(6)-INTERVAL 7 DAY LIMIT 1000`},
		{"", `DELETE FROM push_session WHERE expires_at<=UTC_TIMESTAMP(6) LIMIT 1000`},
	} {
		count, err := cleanPushBatch(ctx, s.db, step.query)
		if err != nil {
			return err
		}
		if step.outcome != "" && count > 0 {
			s.outcomes.add("delivery_"+step.outcome, count)
		}
	}
	return nil
}

// Use READ COMMITTED for range maintenance so empty ranges do not lock inserts.
func cleanPushBatch(ctx context.Context, db *sqlx.DB, query string) (int64, error) {
	tx, err := db.BeginTxx(ctx, claimTx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, query)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// ReserveSend fences each message HTTP call, including a retry after token refresh.
func (s *PushStore) ReserveSend(ctx context.Context, d push.Delivery) (bool, error) {
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// FinishPush takes the delivery lock before recording an AppID cooldown.
	var eligible bool
	err = tx.GetContext(ctx, &eligible, `SELECT lease=? AND status IN ('leased','sending') AND expires_at>UTC_TIMESTAMP(6) AND attempts<max_attempts FROM push_delivery WHERE id=? FOR UPDATE`, d.Lease, d.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || !eligible {
		return false, err
	}
	rate := push.DefaultDeliveryPolicy().WechatRate
	if p := s.policy.Load(); p != nil {
		rate = p.WechatRate
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO push_channel_limit(app_id,next_at) VALUES(?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE app_id=app_id`, d.AppID)
	if err != nil {
		return false, err
	}
	var cooldown, age int
	err = tx.QueryRowContext(ctx, `SELECT cooldown_seconds,COALESCE(TIMESTAMPDIFF(SECOND,recovered_at,UTC_TIMESTAMP(6)),-1) FROM push_channel_limit WHERE app_id=? FOR UPDATE`, d.AppID).Scan(&cooldown, &age)
	if err != nil {
		return false, err
	}
	var res sql.Result
	if cooldown > 0 {
		res, err = tx.ExecContext(ctx, `UPDATE push_channel_limit SET probe_lease=?,next_at=UTC_TIMESTAMP(6)+INTERVAL 30 SECOND WHERE app_id=? AND (next_at<=UTC_TIMESTAMP(6) OR probe_lease=?)`, d.Lease, d.AppID, d.Lease)
	} else {
		// The steady-state bucket permits one second of burst. During ramp-up
		// each slot is paced from now, so idle time cannot accumulate a burst.
		burst := 1000000
		if age >= 0 && (1<<min(age, 20)) < rate {
			rate = 1 << min(age, 20)
			burst = 0
		}
		res, err = tx.ExecContext(ctx, `UPDATE push_channel_limit SET available_at=TIMESTAMPADD(MICROSECOND,?,GREATEST(COALESCE(available_at,TIMESTAMPADD(MICROSECOND,-?,UTC_TIMESTAMP(6))),TIMESTAMPADD(MICROSECOND,-?,UTC_TIMESTAMP(6)))) WHERE app_id=? AND next_at<=UTC_TIMESTAMP(6) AND (available_at IS NULL OR available_at<=UTC_TIMESTAMP(6))`, (1000000+rate-1)/rate, burst, burst, d.AppID)
	}
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	res, err = tx.ExecContext(ctx, `UPDATE push_delivery SET attempts=attempts+1,status='sending' WHERE id=? AND lease=? AND status IN ('leased','sending') AND expires_at>UTC_TIMESTAMP(6) AND next_at>UTC_TIMESTAMP(6) AND attempts<max_attempts AND EXISTS(SELECT 1 FROM device_bind b WHERE b.device_id=push_delivery.device_id AND b.user_id=push_delivery.user_id AND CONCAT(b.id,':',b.binding_revision)=push_delivery.bind_id) AND EXISTS(SELECT 1 FROM push_recipient r JOIN push_subscription s ON s.granted AND s.generation=r.generation WHERE r.generation=push_delivery.generation AND r.user_id=push_delivery.user_id AND s.template_id=push_delivery.template_id)`, d.ID, d.Lease)
	if err != nil {
		return false, err
	}
	n, err = res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	return true, tx.Commit()
}
