package mysql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// Reading with a row lock serializes the final SQL deletion with rebind.
func unboundRevision(ctx context.Context, q sqlx.QueryerContext, device string, lock bool) (int64, error) {
	query := `SELECT binding_revision FROM device_bind WHERE device_id=? AND user_id=0`
	if lock {
		query += " FOR UPDATE"
	}
	var revision int64
	err := sqlx.GetContext(ctx, q, &revision, query, device)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return revision, err
}
