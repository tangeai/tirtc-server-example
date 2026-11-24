package mysql

import (
	"context"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/call"
)

type callCleanupStore struct{ db *sqlx.DB }

func NewCallCleanupStore(db *sqlx.DB) call.ContactCleanup { return &callCleanupStore{db} }
func (s *callCleanupStore) UnboundRevision(ctx context.Context, device string) (int64, error) {
	return unboundRevision(ctx, s.db, device, false)
}
func (s *callCleanupStore) DeleteUnboundContacts(ctx context.Context, device string, revision int64) ([]string, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := unboundRevision(ctx, tx, device, true)
	if err != nil {
		return nil, err
	}
	if current != revision {
		return nil, nil
	}
	var peers []string
	err = tx.SelectContext(ctx, &peers, `SELECT IF(device_id_a=?,device_id_b,device_id_a) FROM call_contact
		WHERE (device_id_a=? OR device_id_b=?) AND status<>3 FOR UPDATE`, device, device, device)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM call_contact WHERE device_id_a=? OR device_id_b=?`, device, device); err != nil {
		return nil, err
	}
	return peers, tx.Commit()
}
