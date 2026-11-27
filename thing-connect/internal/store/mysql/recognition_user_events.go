package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/push"
	r "thing-connect/internal/recognition"
)

func (s *RecognitionStore) ClaimUserEvents(ctx context.Context, limit int) ([]r.UserEvent, error) {
	policy := s.source.Load()
	if policy == nil {
		return nil, nil
	}
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var ready bool
	if err = tx.GetContext(ctx, &ready, `SELECT NOT paused AND generation=? FROM recognition_push_target WHERE target='push-server' FOR SHARE`, policy.generation); err != nil || !ready {
		return nil, err
	}
	var rows []struct {
		ID                                                          int64
		Device, Bind, Source, Kind, Registered, Occurred, LabelText string
		Checking                                                    bool
		Expires                                                     string
	}
	err = tx.SelectContext(ctx, &rows, `SELECT id,device_id AS device,bind_id AS bind,source_event_id AS source,event_type AS kind,label_text AS labeltext,
 DATE_FORMAT(registered_at,'%Y-%m-%dT%H:%i:%s.%fZ') AS registered,
 DATE_FORMAT(occurred_at,'%Y-%m-%dT%H:%i:%s.%fZ') AS occurred,
 DATE_FORMAT(expires_at,'%Y-%m-%dT%H:%i:%s.%fZ') AS expires,status='checking' AS checking
 FROM recognition_user_event_outbox WHERE next_at<=UTC_TIMESTAMP(6) AND ((status='pending' AND lease='' AND expires_at>UTC_TIMESTAMP(6) AND attempts<max_attempts AND generation=? AND ?) OR (status='checking' AND expires_at IS NOT NULL AND confirm_attempts<3 AND registered_at>UTC_TIMESTAMP(6)-INTERVAL 24 HOUR))
 ORDER BY next_at,id LIMIT ? FOR UPDATE SKIP LOCKED`, policy.generation, policy.Enabled, limit)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []r.UserEvent{}, nil
	}
	events := make([]r.UserEvent, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	lease := recID()
	for _, row := range rows {
		v := r.UserEvent{ID: row.ID, Lease: lease, Checking: row.Checking}
		v.Event.Expires, err = time.Parse(time.RFC3339Nano, row.Expires)
		if err != nil {
			return nil, err
		}
		v.Event.ID = row.Source
		v.Event.Device = row.Device
		v.Event.Bind = row.Bind
		v.Event.Kind = row.Kind
		v.Event.LabelText = row.LabelText
		v.Event.Registered, err = time.Parse(time.RFC3339Nano, row.Registered)
		if err != nil {
			return nil, err
		}
		v.Event.Occurred, err = time.Parse(time.RFC3339Nano, row.Occurred)
		if err != nil {
			return nil, err
		}
		events = append(events, v)
		ids = append(ids, row.ID)
	}
	query, args, err := sqlx.In(`UPDATE recognition_user_event_outbox SET lease=?,next_at=UTC_TIMESTAMP(6)+INTERVAL 15 SECOND,attempts=attempts+(status='pending'),reserved=(status='pending'),
 confirm_attempts=confirm_attempts+(status='checking') WHERE id IN (?)`, lease, ids)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return nil, err
	}
	return events, tx.Commit()
}

// FinishUserEvents groups equal acknowledgements while fencing every member by its leased row ID.
func (s *RecognitionStore) FinishUserEvents(ctx context.Context, events []r.UserEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type group struct{ lease, result string }
	groups := map[group][]int64{}
	for _, v := range events {
		if v.Outcome == "not_found" {
			if err = s.finishMissing(ctx, tx, v); err != nil {
				return err
			}
			continue
		}
		k := group{v.Lease, v.Outcome}
		groups[k] = append(groups[k], v.ID)
	}
	counts := map[string]int64{}
	for k, ids := range groups {
		// The preflight returned before creating an HTTP request. This reservation
		// can be returned without changing the target's pause or generation.
		if k.result == "withheld" {
			q, args, err := sqlx.In(`UPDATE recognition_user_event_outbox SET attempts=attempts-1,reserved=FALSE,lease='',next_at=UTC_TIMESTAMP(6)+INTERVAL 1 SECOND WHERE id IN (?) AND status='pending' AND lease=? AND reserved`, ids, k.lease)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, q, args...); err != nil {
				return err
			}
			continue
		}
		query, args, err := sqlx.In(`UPDATE recognition_user_event_outbox SET status=?,reserved=FALSE,lease='',next_at=TIMESTAMPADD(SECOND,1+FLOOR(RAND()*LEAST(30,1<<LEAST(5,attempts))),UTC_TIMESTAMP(6)) WHERE id IN (?) AND status IN ('pending','checking') AND lease=?`, k.result, ids, k.lease)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		counts[k.result] += n
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	for label, count := range counts {
		s.outcomes.add(label, count)
	}
	return nil
}
func (s *RecognitionStore) CleanUserEvents(ctx context.Context) error {
	for _, q := range []string{
		`UPDATE recognition_user_event_outbox SET expires_at=occurred_at+INTERVAL 120 SECOND WHERE status IN ('pending','checking') AND expires_at IS NULL LIMIT 1000`,
		`UPDATE recognition_user_event_outbox SET status='checking',lease='',reserved=FALSE,next_at=UTC_TIMESTAMP(6)+INTERVAL 30 SECOND WHERE status='pending' AND lease<>'' AND next_at<=UTC_TIMESTAMP(6) LIMIT 1000`,
		`UPDATE recognition_user_event_outbox SET status=IF(attempts>0,'checking','expired'),next_at=IF(attempts>0,GREATEST(next_at,UTC_TIMESTAMP(6)+INTERVAL 30 SECOND),next_at) WHERE status='pending' AND lease='' AND (expires_at IS NULL OR expires_at<=UTC_TIMESTAMP(6)) LIMIT 1000`,
		`UPDATE recognition_user_event_outbox SET status='checking',next_at=UTC_TIMESTAMP(6)+INTERVAL 30 SECOND WHERE status='pending' AND lease='' AND attempts>=max_attempts LIMIT 1000`,
		`UPDATE recognition_user_event_outbox SET status=IF(attempts>0,'checking','cancelled'),next_at=UTC_TIMESTAMP(6)+INTERVAL 30 SECOND WHERE status='pending' AND lease='' AND generation<(SELECT generation FROM recognition_push_target WHERE target='push-server') LIMIT 1000`,
		`UPDATE recognition_user_event_outbox SET status='confirmation_unknown',lease='' WHERE status='checking' AND next_at<=UTC_TIMESTAMP(6) AND (confirm_attempts>=3 OR registered_at<=UTC_TIMESTAMP(6)-INTERVAL 24 HOUR) LIMIT 1000`,
		`DELETE FROM recognition_user_event_outbox WHERE status NOT IN ('pending','checking') AND registered_at<UTC_TIMESTAMP(6)-INTERVAL 72 HOUR ORDER BY registered_at,id LIMIT 1000`,
	} {
		if _, err := cleanPushBatch(ctx, s.db, q); err != nil {
			return err
		}
	}
	return nil
}

type sourcePolicy struct {
	push.DeliveryPolicy
	revision, generation int64
}

func (p sourcePolicy) deadline(t time.Time) time.Time {
	return t.UTC().Add(time.Duration(p.TTLSeconds) * time.Second)
}
func (s *RecognitionStore) ConfigureSource(ctx context.Context, p push.DeliveryPolicy, revision int64, hasAddress bool) error {
	if err := p.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO recognition_push_target(target) VALUES('push-server') ON DUPLICATE KEY UPDATE target=target`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE recognition_push_target SET generation=generation+(enabled AND NOT ?), enabled=?,revision=? WHERE target='push-server' AND revision<=?`, p.Enabled, p.Enabled, revision, revision)
	if err != nil {
		return err
	}
	var current sourcePolicy
	var dbRevision int64
	err = tx.QueryRowContext(ctx, `SELECT generation,revision FROM recognition_push_target WHERE target='push-server'`).Scan(&current.generation, &dbRevision)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	current.DeliveryPolicy = p
	current.Enabled = p.Enabled && hasAddress && dbRevision == revision
	current.revision = revision
	s.source.Store(&current)
	return nil
}
func (s *RecognitionStore) RefundAuth(ctx context.Context, v r.UserEvent) error {
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var paused bool
	if err = tx.GetContext(ctx, &paused, `SELECT paused FROM recognition_push_target WHERE target='push-server' FOR UPDATE`); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE recognition_user_event_outbox SET attempts=attempts-IF(reserved,1,0),reserved=FALSE,lease='',next_at=UTC_TIMESTAMP(6) WHERE id=? AND lease=? AND ((status='pending' AND reserved=TRUE) OR status='checking')`, v.ID, v.Lease)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		_, err = tx.ExecContext(ctx, `UPDATE recognition_push_target SET auth_epoch=auth_epoch+1,probe_at=IF(paused,probe_at,UTC_TIMESTAMP(6)+INTERVAL 1 SECOND),paused=TRUE WHERE target='push-server'`)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *RecognitionStore) ClaimProbe(ctx context.Context) (string, error) {
	lease := recID()
	res, err := s.db.ExecContext(ctx, `UPDATE recognition_push_target SET probe_epoch=auth_epoch,probe_lease=?,probe_at=UTC_TIMESTAMP(6)+INTERVAL 15 SECOND WHERE target='push-server' AND paused AND probe_at<=UTC_TIMESTAMP(6)`, lease)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if n == 0 {
		return "", err
	}
	return lease, err
}
func (s *RecognitionStore) FinishProbe(ctx context.Context, lease string, ok bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE recognition_push_target SET paused=NOT ?,failures=IF(?,0,LEAST(5,failures+1)),probe_at=TIMESTAMPADD(SECOND,LEAST(30,1<<failures),UTC_TIMESTAMP(6)),probe_lease='' WHERE target='push-server' AND paused AND probe_epoch=auth_epoch AND probe_lease=? AND probe_at>UTC_TIMESTAMP(6)`, ok, ok, lease)
	return err
}
func (s *RecognitionStore) MayPost(ctx context.Context, v r.UserEvent) (bool, error) {
	var ok bool
	err := s.db.GetContext(ctx, &ok, `SELECT t.enabled AND NOT t.paused AND t.generation=o.generation AND o.expires_at>UTC_TIMESTAMP(6) AND o.next_at>UTC_TIMESTAMP(6) FROM recognition_user_event_outbox o JOIN recognition_push_target t ON t.target='push-server' WHERE o.id=? AND o.lease=? AND o.reserved`, v.ID, v.Lease)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return ok, err
}

func (s *RecognitionStore) recordAlarm(ctx context.Context, tx *sqlx.Tx, device, bind, id, label string, occurred time.Time) (string, error) {
	policy := s.source.Load()
	bucket := eventBucket(device, id)
	status := "pending"
	if policy == nil {
		policy = &sourcePolicy{DeliveryPolicy: push.DefaultDeliveryPolicy()}
		status = "policy_unavailable"
	} else if !policy.Enabled {
		status = "disabled"
	}
	deadline := policy.deadline(occurred)
	if status == "pending" {
		var current, expired bool
		err := tx.QueryRowContext(ctx, `SELECT enabled AND generation=?, UTC_TIMESTAMP(6)>=? FROM recognition_push_target WHERE target='push-server' FOR SHARE`, policy.generation, utcStamp(deadline)).Scan(&current, &expired)
		if err != nil {
			return "", err
		}
		switch {
		case !current:
			status = "cancelled"
		case expired:
			status = "expired"
		default:
			available, err := queueRoom(ctx, tx, true, bucket, policy.SourceCapacity)
			if err != nil {
				return "", err
			}
			if !available {
				status = "skipped_overload"
			}
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO recognition_user_event_outbox
 (device_id,bind_id,source_event_id,event_type,label_text,occurred_at,registered_at,next_at,expires_at,max_attempts,policy_revision,generation,status,bucket)
 VALUES(?,?,?,'alarm',?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),?,?,?,?,?,?) ON DUPLICATE KEY UPDATE id=id`, device, bind, id, label, utcStamp(occurred), utcStamp(deadline), policy.SourceAttempts, policy.revision, policy.generation, status, bucket)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return "", err
	}
	return status, nil
}

func (s *RecognitionStore) SourceHealth(ctx context.Context) error {
	if s.source.Load() == nil {
		return errors.New("等待来源投递策略")
	}
	var paused bool
	err := s.db.GetContext(ctx, &paused, `SELECT paused FROM recognition_push_target WHERE target='push-server'`)
	if err != nil {
		return err
	}
	if paused {
		return errors.New("内部鉴权异常，目标暂停投递")
	}
	return nil
}

// Reconciliation may reopen an internal idempotent POST only after the
// receiver explicitly reports absence. A missing response never reopens it.
func (s *RecognitionStore) finishMissing(ctx context.Context, tx *sqlx.Tx, v r.UserEvent) error {
	var gen int64
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT generation,enabled FROM recognition_push_target WHERE target='push-server' FOR SHARE`).Scan(&gen, &enabled); err != nil {
		return err
	}
	var row struct {
		Generation                int64
		Expired, Exhausted, Bound bool
		Bucket                    int
	}
	err := tx.GetContext(ctx, &row, `SELECT generation,expires_at<=UTC_TIMESTAMP(6) AS expired,attempts>=max_attempts AS exhausted,bucket,
 EXISTS(SELECT 1 FROM device_bind d WHERE d.device_id=o.device_id AND d.user_id>0 AND CONCAT(d.id,':',d.binding_revision)=o.bind_id) AS bound
 FROM recognition_user_event_outbox o WHERE id=? AND lease=? AND status='checking' FOR UPDATE`, v.ID, v.Lease)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	status := "pending"
	switch {
	case v.Event.Validate() != nil:
		status = "invalid"
	case row.Expired:
		status = "expired"
	case row.Exhausted:
		status = "attempts_exhausted"
	case !enabled || gen != row.Generation:
		status = "cancelled"
	case !row.Bound:
		status = "stale_binding"
	default:
		policy := s.source.Load()
		if policy == nil {
			return errors.New("等待来源投递策略")
		}
		room, err := queueRoom(ctx, tx, true, row.Bucket, policy.SourceCapacity)
		if err != nil {
			return err
		}
		if !room {
			status = "skipped_overload"
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE recognition_user_event_outbox SET status=?,lease='',reserved=FALSE,next_at=UTC_TIMESTAMP(6)+INTERVAL 1 SECOND WHERE id=? AND lease=? AND status='checking'`, status, v.ID, v.Lease)
	return err
}
