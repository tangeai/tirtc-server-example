package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/adminmail"
	"thing-connect/internal/queuehealth"
)

type resetMailStore struct{ db *sqlx.DB }

func NewResetMailStore(db *sqlx.DB) adminmail.Queue { return &resetMailStore{db: db} }

// Attempts are monotonically increasing claim tokens, including the last
// attempt if a worker dies before recording its result.
func (s *resetMailStore) Claim(ctx context.Context) (*adminmail.Command, error) {
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var c adminmail.Command
	err = tx.GetContext(ctx, &c, `SELECT id,email,attempts FROM admin_reset_mail_outbox
		WHERE next_at<=NOW(6) AND attempts<? ORDER BY next_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`, adminmail.MaxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // An empty due queue is a successful poll.
	}
	if err != nil {
		return nil, err
	}
	c.Attempts++
	_, err = tx.ExecContext(ctx, `UPDATE admin_reset_mail_outbox SET attempts=?,next_at=TIMESTAMPADD(SECOND,?,NOW(6)) WHERE id=?`, c.Attempts, int(adminmail.Lease/time.Second), c.ID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *resetMailStore) Finish(ctx context.Context, c adminmail.Command, delivered bool) error {
	if delivered {
		_, err := s.db.ExecContext(ctx, `DELETE FROM admin_reset_mail_outbox WHERE id=? AND attempts=?`, c.ID, c.Attempts)
		return err
	}
	delay := 1 << min(c.Attempts, 5)
	if c.Attempts >= adminmail.MaxAttempts {
		delay = 0
	}
	_, err := s.db.ExecContext(ctx, `UPDATE admin_reset_mail_outbox SET next_at=TIMESTAMPADD(SECOND,?,NOW(6)) WHERE id=? AND attempts=?`, delay, c.ID, c.Attempts)
	return err
}

func (s *resetMailStore) Backlog(ctx context.Context) (queuehealth.Backlog, error) {
	// Retain terminal IDs for operator lookup for one hour, including an
	// expired last lease left by a worker that died during delivery.
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_reset_mail_outbox WHERE attempts>=? AND next_at<=NOW(6)-INTERVAL 1 HOUR LIMIT 1000`, adminmail.MaxAttempts)
	if err != nil {
		return queuehealth.Backlog{}, err
	}
	var sample queuehealth.Backlog
	var ageMS int64
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(IF(pending,1,NULL)),
		COALESCE(TIMESTAMPDIFF(MICROSECOND,MIN(IF(pending,created_at,NULL)),NOW(6)) DIV 1000,0),
		COUNT(IF(NOT pending AND next_at>NOW(6)-INTERVAL 1 HOUR,1,NULL))
		FROM (SELECT created_at,next_at,(attempts<? OR next_at>NOW(6)) pending FROM admin_reset_mail_outbox) AS jobs`, adminmail.MaxAttempts).Scan(&sample.Pending, &ageMS, &sample.Exhausted)
	sample.OldestAge = time.Duration(max(ageMS, 0)) * time.Millisecond
	return sample, err
}
