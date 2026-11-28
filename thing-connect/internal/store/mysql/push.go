package mysql

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"thing-connect/internal/push"
)

type PushStore struct {
	db       *sqlx.DB
	policy   atomic.Pointer[push.DeliveryPolicy]
	claims   atomic.Uint64
	outcomes outcomeCounts
}

func (s *PushStore) SetDeliveryPolicy(p push.DeliveryPolicy) { s.policy.Store(&p) }
func (s *PushStore) Lookup(ctx context.Context, e push.Event) (string, error) {
	var outcome string
	err := s.db.GetContext(ctx, &outcome, `SELECT outcome FROM push_event WHERE device_id=? AND bind_id=? AND source_event_id=?`, e.Device, e.Bind, e.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return outcome, err
}

func NewPushStore(db *sqlx.DB) *PushStore { return &PushStore{db: db} }

type pushBinding struct {
	Bind  string
	Owner int64
	Name  string
}

func lockPushDevice(ctx context.Context, tx *sqlx.Tx, device string) (pushBinding, error) {
	var b pushBinding
	err := tx.GetContext(ctx, &b, `SELECT CONCAT(id,':',binding_revision) AS bind,user_id AS owner,device_name AS name FROM device_bind WHERE device_id=? FOR UPDATE`, device)
	return b, err
}

type pushWindow struct {
	Seconds sql.NullInt64
	Allowed bool
}

func lockPushWindow(ctx context.Context, tx *sqlx.Tx, user int64, device, bind, kind string) (pushWindow, error) {
	var w pushWindow
	_, err := tx.ExecContext(ctx, `INSERT INTO push_window(user_id,device_id,bind_id,event_type) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE user_id=user_id`, user, device, bind, kind)
	if err != nil {
		return w, err
	}
	err = tx.GetContext(ctx, &w, `SELECT interval_seconds AS seconds,(last_admitted IS NULL OR UTC_TIMESTAMP(6)>=TIMESTAMPADD(SECOND,GREATEST(COALESCE(interval_seconds,0),?),last_admitted)) AS allowed FROM push_window WHERE user_id=? AND device_id=? AND bind_id=? AND event_type=? FOR UPDATE`, push.MinIntervalSeconds, user, device, bind, kind)
	return w, err
}

// Preference serializes changes with admission even before the first saved preference.
func (s *PushStore) Preference(ctx context.Context, user int64, device, kind string, write bool, seconds *int) (*int, error) {
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	b, err := lockPushDevice(ctx, tx, device)
	if errors.Is(err, sql.ErrNoRows) || err == nil && b.Owner != user {
		return nil, push.ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	w, err := lockPushWindow(ctx, tx, user, device, b.Bind, kind)
	if err != nil {
		return nil, err
	}
	if write {
		_, err = tx.ExecContext(ctx, `UPDATE push_window SET interval_seconds=? WHERE user_id=? AND device_id=? AND bind_id=? AND event_type=?`, seconds, user, device, b.Bind, kind)
		if err != nil {
			return nil, err
		}
	} else if w.Seconds.Valid {
		n := max(push.MinIntervalSeconds, int(w.Seconds.Int64))
		seconds = &n
	}
	return seconds, tx.Commit()
}

func (s *PushStore) Admit(ctx context.Context, e push.Event, app, template string) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	b, ownerErr := lockPushDevice(ctx, tx, e.Device)
	if ownerErr != nil && !errors.Is(ownerErr, sql.ErrNoRows) {
		return "", ownerErr
	}
	var prior struct {
		Outcome string
		Same    bool
	}
	err = tx.GetContext(ctx, &prior, `SELECT outcome,(registered_at=? AND BINARY label_text=BINARY ?) AS same FROM push_event WHERE device_id=? AND bind_id=? AND source_event_id=? FOR UPDATE`, e.Registered.UTC().Format("2006-01-02 15:04:05.000000"), e.LabelText, e.Device, e.Bind, e.ID)
	if err == nil {
		if !prior.Same {
			return "", push.ErrConflict
		}
		if prior.Outcome == "expired" {
			return "", push.ErrExpired
		}
		return prior.Outcome, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	policy := push.DefaultDeliveryPolicy()
	if configured := s.policy.Load(); configured != nil {
		policy = *configured
	}
	if e.Expires.IsZero() {
		e.Expires = e.Occurred.Add(time.Duration(policy.TTLSeconds) * time.Second)
	}
	if e.Occurred.IsZero() {
		return "", push.ErrExpired
	}
	ttl := e.Expires.Sub(e.Occurred)
	if ttl < 30*time.Second || ttl > 600*time.Second {
		return "", push.ErrInvalid
	}
	if e.MaxAttempts == 0 {
		e.MaxAttempts = push.DefaultPolicy().MaxAttempts
	}
	var expired bool
	if err = tx.GetContext(ctx, &expired, `SELECT ?<=UTC_TIMESTAMP(6)`, e.Expires.UTC().Format("2006-01-02 15:04:05.000000")); err != nil {
		return "", err
	}
	if expired {
		_, err = tx.ExecContext(ctx, `INSERT INTO push_event(device_id,bind_id,source_event_id,registered_at,received_at,outcome,label_text) VALUES(?,?,?,?,UTC_TIMESTAMP(6),'expired',?)`, e.Device, e.Bind, e.ID, utcStamp(e.Registered), e.LabelText)
		if err != nil {
			return "", err
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
		s.outcomes.add("expired", 1)
		return "", push.ErrExpired
	}
	if ownerErr != nil || b.Owner <= 0 || b.Bind != e.Bind {
		return "", push.ErrBinding
	}
	w, err := lockPushWindow(ctx, tx, b.Owner, e.Device, b.Bind, e.Kind)
	if err != nil {
		return "", err
	}
	var target struct{ Generation, GrantID string }
	outcome := "queued"
	switch {
	case !w.Allowed:
		outcome = "interval"
	case template == "":
		outcome = "unconfigured"
	default:
		err = tx.GetContext(ctx, &target, `SELECT r.generation,s.grant_id AS grantid FROM push_recipient r JOIN push_subscription s ON s.granted AND s.generation=r.generation AND s.template_id=? AND s.bind_id=IF(?='alarm',?,'') WHERE r.user_id=? AND r.app_id=? FOR UPDATE`, template, e.Kind, b.Bind, b.Owner, app)
		if errors.Is(err, sql.ErrNoRows) {
			outcome = "unsubscribed"
		} else if err != nil {
			return "", err
		}
	}
	bucket := eventBucket(e.Device, e.ID)
	if outcome == "queued" {
		room, err := queueRoom(ctx, tx, false, bucket, policy.DeliveryCapacity)
		if err != nil {
			return "", err
		}
		if !room {
			outcome = "skipped_overload"
		}
	}
	if outcome == "queued" {
		if err = tx.GetContext(ctx, &expired, `SELECT ?<=UTC_TIMESTAMP(6)`, utcStamp(e.Expires)); err != nil {
			return "", err
		}
		if expired {
			outcome = "expired"
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO push_event(device_id,bind_id,source_event_id,registered_at,received_at,outcome,label_text) VALUES(?,?,?,?,UTC_TIMESTAMP(6),?,?)`, e.Device, e.Bind, e.ID, e.Registered.UTC().Format("2006-01-02 15:04:05.000000"), outcome, e.LabelText)
	if err != nil {
		return "", err
	}
	if outcome == "queued" {
		id, err := res.LastInsertId()
		if err != nil {
			return "", err
		}
		var occurred any
		if !e.Occurred.IsZero() {
			occurred = e.Occurred.UTC().Format("2006-01-02 15:04:05.000000")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO push_delivery(event_id,user_id,device_id,device_name,bind_id,event_type,generation,template_id,grant_id,occurred_at,reference_id,expires_at,max_attempts,bucket,started_at,status,next_at) VALUES(?,?,?,?,?,?,?,?,?,COALESCE(?,UTC_TIMESTAMP(6)),?,?,?,?,UTC_TIMESTAMP(6),'pending',UTC_TIMESTAMP(6))`, id, b.Owner, e.Device, b.Name, b.Bind, e.Kind, target.Generation, template, target.GrantID, occurred, uuid.NewString(), utcStamp(e.Expires), e.MaxAttempts, bucket)
		if err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `UPDATE push_window SET last_admitted=UTC_TIMESTAMP(6) WHERE user_id=? AND device_id=? AND bind_id=? AND event_type=?`, b.Owner, e.Device, b.Bind, e.Kind)
		if err != nil {
			return "", err
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	s.outcomes.add(outcome, 1)
	if outcome == "expired" {
		return "", push.ErrExpired
	}
	return outcome, nil
}

func (s *PushStore) Reference(ctx context.Context, user int64, ref string) (push.Reference, error) {
	var refData push.Reference
	err := s.db.GetContext(ctx, &refData, `SELECT d.device_id,e.label_text FROM push_delivery d JOIN push_event e ON e.id=d.event_id JOIN device_bind b ON d.device_id=b.device_id AND d.bind_id=CONCAT(b.id,':',b.binding_revision) WHERE d.reference_id=? AND b.user_id=? AND d.user_id=? AND e.received_at>UTC_TIMESTAMP(6)-INTERVAL 72 HOUR`, ref, user, user)
	if errors.Is(err, sql.ErrNoRows) {
		return refData, push.ErrExpired
	}
	return refData, err
}

func (s *PushStore) NotificationMetrics() map[string]int64 { return s.outcomes.metrics("push_") }
