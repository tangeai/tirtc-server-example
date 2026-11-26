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
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	current, other, err := recipients(ctx, tx, user, app, open)
	if err != nil {
		return "", false, err
	}
	id := uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO push_session(id,user_id,app_id,open_id,template_id,old_generation,other_generation,expires_at) VALUES(?,?,?,?,?,?,?,UTC_TIMESTAMP(6)+INTERVAL 5 MINUTE)`, id, user, app, open, template, current.Generation, other.Generation)
	if err != nil {
		return "", false, err
	}
	return id, (current.User != 0 && (current.Open != open || current.App != app)) || other.User != 0, tx.Commit()
}

type subscriptionSession struct {
	App        string
	Open       string
	Template   string
	Old        string
	Other      string
	Generation string
	Decision   string
}

func lockSession(ctx context.Context, tx *sqlx.Tx, user int64, id string) (subscriptionSession, error) {
	var v subscriptionSession
	err := tx.GetContext(ctx, &v, `SELECT app_id AS app,open_id AS open,template_id AS template,old_generation AS old,other_generation AS other,generation,decision FROM push_session WHERE id=? AND user_id=? AND expires_at>UTC_TIMESTAMP(6) FOR UPDATE`, id, user)
	if errors.Is(err, sql.ErrNoRows) {
		err = push.ErrExpired
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
			if _, err = tx.ExecContext(ctx, `UPDATE push_delivery SET status='cancelled',finished_at=UTC_TIMESTAMP(6) WHERE generation=? AND status='pending'`, r.Generation); err != nil {
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
	switch decision {
	case "accept":
		_, err = tx.ExecContext(ctx, `INSERT INTO push_subscription(generation,template_id,grant_id) VALUES(?,?,?) ON DUPLICATE KEY UPDATE grant_id=VALUES(grant_id)`, v.Generation, v.Template, id)
	case "reject":
		_, err = tx.ExecContext(ctx, `DELETE s FROM push_subscription s LEFT JOIN push_session previous ON previous.id=s.grant_id JOIN push_session reporting ON reporting.id=? WHERE s.generation=? AND s.template_id=? AND (previous.id IS NULL OR previous.expires_at<=reporting.expires_at)`, id, v.Generation, v.Template)
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE push_session SET decision=? WHERE id=?`, decision, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
