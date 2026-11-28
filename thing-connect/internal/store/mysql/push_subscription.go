package mysql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"thing-connect/internal/push"
)

type recipient struct {
	User       int64
	Generation string
	App        string
	Open       string
}

func recipients(ctx context.Context, tx *sqlx.Tx, user int64, app, open string) (current, other recipient, err error) {
	var rows []recipient
	err = tx.SelectContext(ctx, &rows, `SELECT user_id AS user, generation,app_id AS app, open_id AS open FROM push_recipient WHERE user_id=? OR (app_id=? AND open_id=?) ORDER BY user_id FOR UPDATE`, user, app, open)
	for _, r := range rows {
		if r.User == user {
			current = r
		} else {
			other = r
		}
	}
	return
}

func (s *PushStore) Prepare(ctx context.Context, user int64, app, open, template string) (string, bool, error) {
	return s.PrepareDevice(ctx, user, app, open, template, "", "")
}

func (s *PushStore) PrepareDevice(ctx context.Context, user int64, app, open, template, device, model string) (string, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	bind := ""
	if device != "" {
		b, err := lockPushDevice(ctx, tx, device)
		if errors.Is(err, sql.ErrNoRows) || err == nil && b.Owner != user {
			return "", false, push.ErrForbidden
		}
		if err != nil {
			return "", false, err
		}
		bind = b.Bind
	}
	current, other, err := recipients(ctx, tx, user, app, open)
	if err != nil {
		return "", false, err
	}
	id := uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO push_session(id,user_id,app_id,open_id,template_id,device_id,bind_id,model_id,old_generation,other_generation,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP(6)+INTERVAL 5 MINUTE)`, id, user, app, open, template, device, bind, model, current.Generation, other.Generation)
	if err != nil {
		return "", false, err
	}
	return id, (current.User != 0 && (current.Open != open || current.App != app)) || other.User != 0, tx.Commit()
}

type subscriptionSession struct {
	Device, Bind, Model string
	Seq                 int64
	App                 string
	Open                string
	Template            string
	Old                 string
	Other               string
	Generation          string
	Decision            string
}

func lockSession(ctx context.Context, tx *sqlx.Tx, user int64, id string) (subscriptionSession, error) {
	var v subscriptionSession
	err := tx.GetContext(ctx, &v, `SELECT device_id AS device,bind_id AS bind,model_id AS model, session_seq AS seq,app_id AS app,open_id AS open,template_id AS template,old_generation AS old,other_generation AS other,generation,decision FROM push_session WHERE id=? AND user_id=? AND expires_at>UTC_TIMESTAMP(6) FOR UPDATE`, id, user)
	if errors.Is(err, sql.ErrNoRows) {
		err = push.ErrExpired
	}
	if err == nil && v.Device != "" {
		b, lockErr := lockPushDevice(ctx, tx, v.Device)
		if errors.Is(lockErr, sql.ErrNoRows) || lockErr == nil && (b.Owner != user || b.Bind != v.Bind) {
			return v, push.ErrBinding
		}
		if lockErr != nil {
			return v, lockErr
		}
	}
	return v, err
}

func (s *PushStore) Confirm(ctx context.Context, user int64, id string, replace bool) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	v, err := lockSession(ctx, tx, user, id)
	if err != nil {
		return err
	}
	cur, other, err := recipients(ctx, tx, user, v.App, v.Open)
	if err != nil {
		return err
	}
	if v.Generation != "" {
		if cur.Generation != v.Generation {
			return push.ErrConflict
		}
		return tx.Commit()
	}
	if cur.Generation != v.Old || other.Generation != v.Other {
		return push.ErrConflict
	}
	if !replace && (other.User != 0 || cur.User != 0 && (cur.Open != v.Open || cur.App != v.App)) {
		return push.ErrConflict
	}
	generation := cur.Generation
	if (cur.Open != v.Open || cur.App != v.App) || other.User != 0 {
		for _, r := range []recipient{cur, other} {
			if r.User == 0 {
				continue
			}
			if _, err = tx.ExecContext(ctx, `UPDATE push_delivery SET status='cancelled',finished_at=UTC_TIMESTAMP(6) WHERE generation=? AND status IN ('leased','pending')`, r.Generation); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM push_subscription WHERE generation=?`, r.Generation); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM push_recipient WHERE generation=? AND user_id=?`, r.Generation, r.User); err != nil {
				return err
			}
		}
		generation = uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO push_recipient(user_id,app_id,open_id,generation) VALUES(?,?,?,?)`, user, v.App, v.Open, generation)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE push_session SET generation=? WHERE id=?`, generation, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *PushStore) Report(ctx context.Context, user int64, id, decision string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	v, err := lockSession(ctx, tx, user, id)
	if err != nil {
		return err
	}
	if v.Decision != "" {
		if v.Decision != decision {
			return push.ErrConflict
		}
		return tx.Commit()
	}
	cur, _, err := recipients(ctx, tx, user, v.App, v.Open)
	if err != nil {
		return err
	}
	if v.Generation == "" || cur.Generation != v.Generation {
		return push.ErrConflict
	}
	if decision == "accept" || decision == "reject" {
		// Keep a rejection as a watermark even after the short-lived session is cleaned.
		_, err = tx.ExecContext(ctx, `INSERT INTO push_subscription(generation,template_id,bind_id,model_id,grant_id,decision_seq,granted) VALUES(?,?,?,?,?,?,?)
 ON DUPLICATE KEY UPDATE model_id=IF(decision_seq<VALUES(decision_seq),VALUES(model_id),model_id),grant_id=IF(decision_seq<VALUES(decision_seq),VALUES(grant_id),grant_id),
 granted=IF(decision_seq<VALUES(decision_seq),VALUES(granted),granted),decision_seq=GREATEST(decision_seq,VALUES(decision_seq))`, v.Generation, v.Template, v.Bind, v.Model, id, v.Seq, decision == "accept")
		if err != nil {
			return err
		}
	}
	// The recipient row serializes first reports for this generation. Cancel
	// compares against the latest grant decision without advancing its sequence.
	var newer bool
	if err = tx.GetContext(ctx, &newer, `SELECT EXISTS(SELECT 1 FROM push_subscription WHERE generation=? AND template_id=? AND bind_id=? AND decision_seq>?)`, v.Generation, v.Template, v.Bind, v.Seq); err != nil {
		return err
	}
	if newer {
		return push.ErrConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE push_session SET decision=? WHERE id=?`, decision, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PushStore) HasGrant(ctx context.Context, user int64, app, template string) (bool, error) {
	var granted bool
	err := s.db.GetContext(ctx, &granted, `SELECT EXISTS(
		SELECT 1 FROM push_recipient r JOIN push_subscription s ON s.generation=r.generation
		WHERE r.user_id=? AND r.app_id=? AND s.template_id=? AND s.bind_id='' AND s.granted
	)`, user, app, template)
	return granted, err
}

func (s *PushStore) HasDeviceGrant(ctx context.Context, user int64, app, template, device string) (bool, error) {
	var granted bool
	err := s.db.GetContext(ctx, &granted, `SELECT EXISTS(SELECT 1 FROM push_subscription s JOIN push_recipient r ON s.generation=r.generation WHERE r.user_id=b.user_id AND r.app_id=? AND s.template_id=? AND s.bind_id=CONCAT(b.id,':',b.binding_revision) AND s.granted) FROM device_bind b WHERE b.device_id=? AND b.user_id=?`, app, template, device, user)
	if errors.Is(err, sql.ErrNoRows) {
		return false, push.ErrForbidden
	}
	return granted, err
}
