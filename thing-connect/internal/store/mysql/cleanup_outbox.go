package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/cleanup"
)

// cleanupOutboxStore leases cleanup_outbox rows without a schema change:
// a claim moves next_attempt_at to the lease expiry and that value is the
// claim snapshot. Re-enqueueing (CommitUnbindWithCleanup) allocates a new ID,
// so an acknowledgement carrying the old snapshot cannot delete the new task.
type cleanupOutboxStore struct {
	db *sqlx.DB
}

func NewCleanupOutboxStore(db *sqlx.DB) cleanup.Queue {
	return &cleanupOutboxStore{db: db}
}

// leaseFormat renders a DATETIME lease column as an exact comparison snapshot,
// independent of driver time zone conversion.
const leaseFormat = "%Y-%m-%d %H:%i:%s"

// claimTx runs queue claims under READ COMMITTED. Under REPEATABLE READ a
// SKIP LOCKED range scan also takes gap locks, and moving a claimed row's
// index key deadlocks against a concurrent claimer's gap locks.
var claimTx = &sql.TxOptions{Isolation: sql.LevelReadCommitted}

func (s *cleanupOutboxStore) Claim(ctx context.Context, limit int, lease time.Duration) ([]cleanup.Task, error) {
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return nil, fmt.Errorf("cleanup outbox claim begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var ids []int64
	if err := tx.SelectContext(ctx, &ids, `
		SELECT id FROM cleanup_outbox
		WHERE next_attempt_at<=NOW()
		ORDER BY id LIMIT ?
		FOR UPDATE SKIP LOCKED`, limit); err != nil {
		return nil, fmt.Errorf("cleanup outbox claim select: %w", err)
	}
	if len(ids) == 0 {
		return nil, tx.Commit()
	}
	query, args, err := sqlx.In(`UPDATE cleanup_outbox SET next_attempt_at=DATE_ADD(NOW(), INTERVAL ? SECOND) WHERE id IN (?)`, int(lease/time.Second), ids)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, tx.Rebind(query), args...); err != nil {
		return nil, fmt.Errorf("cleanup outbox claim lease: %w", err)
	}
	query, args, err = sqlx.In(`
		SELECT id, device_id, target, attempts, DATE_FORMAT(next_attempt_at, '`+leaseFormat+`') AS lease
		FROM cleanup_outbox WHERE id IN (?) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID       int64  `db:"id"`
		DeviceID string `db:"device_id"`
		Target   string `db:"target"`
		Attempts int    `db:"attempts"`
		Lease    string `db:"lease"`
	}
	if err := tx.SelectContext(ctx, &rows, tx.Rebind(query), args...); err != nil {
		return nil, fmt.Errorf("cleanup outbox claim read: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("cleanup outbox claim commit: %w", err)
	}
	tasks := make([]cleanup.Task, len(rows))
	for i, row := range rows {
		tasks[i] = cleanup.Task{ID: row.ID, DeviceID: row.DeviceID, Target: row.Target, Attempts: row.Attempts, Lease: row.Lease}
	}
	return tasks, nil
}

func (s *cleanupOutboxStore) Complete(ctx context.Context, task cleanup.Task) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM cleanup_outbox WHERE id=? AND next_attempt_at=?`, task.ID, task.Lease)
	if err != nil {
		return false, fmt.Errorf("cleanup outbox complete: %w", err)
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *cleanupOutboxStore) Retry(ctx context.Context, task cleanup.Task, delay time.Duration, lastError string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE cleanup_outbox
		SET attempts=attempts+1, last_error=?, next_attempt_at=DATE_ADD(NOW(), INTERVAL ? SECOND)
		WHERE id=? AND next_attempt_at=?`, lastError, int(delay/time.Second), task.ID, task.Lease)
	if err != nil {
		return false, fmt.Errorf("cleanup outbox retry: %w", err)
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *cleanupOutboxStore) Backlog(ctx context.Context) (cleanup.Backlog, error) {
	var row struct {
		Pending int64 `db:"pending"`
		Oldest  int64 `db:"oldest"`
	}
	if err := s.db.GetContext(ctx, &row, `
		SELECT COUNT(*) AS pending, COALESCE(TIMESTAMPDIFF(SECOND, MIN(created_at), NOW()), 0) AS oldest
		FROM cleanup_outbox`); err != nil {
		return cleanup.Backlog{}, fmt.Errorf("cleanup outbox backlog: %w", err)
	}
	return cleanup.Backlog{Pending: row.Pending, OldestAge: time.Duration(max(0, row.Oldest)) * time.Second}, nil
}
