package mysql

import (
	"context"
	"github.com/jmoiron/sqlx"
	"thing-connect/internal/service"
)

type accountReader struct{ db *sqlx.DB }

func NewAccountReader(db *sqlx.DB) service.AccountReader { return &accountReader{db} }
func (r *accountReader) ReadAccount(ctx context.Context, id int64) (service.Account, error) {
	var account service.Account
	err := r.db.GetContext(ctx, &account, "SELECT id,email FROM users WHERE id=? AND status=1", id)
	return account, err
}
