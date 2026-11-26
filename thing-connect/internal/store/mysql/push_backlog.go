package mysql

import (
	"context"
	"github.com/jmoiron/sqlx"
	"thing-connect/internal/queuehealth"
	"time"
)

type PushBacklog struct {
	db    *sqlx.DB
	query string
}

func NewPushBacklog(db *sqlx.DB, source bool) *PushBacklog {
	query := `SELECT COUNT(*) AS pending,COALESCE(TIMESTAMPDIFF(SECOND,MIN(started_at),UTC_TIMESTAMP(6)),0) AS age FROM push_delivery WHERE status IN ('pending','sending')`
	if source {
		query = `SELECT COUNT(*) AS pending,COALESCE(TIMESTAMPDIFF(SECOND,MIN(registered_at),UTC_TIMESTAMP(6)),0) AS age FROM recognition_user_event_outbox WHERE status='pending'`
	}
	return &PushBacklog{db, query}
}
func (b *PushBacklog) Backlog(ctx context.Context) (queuehealth.Backlog, error) {
	var v struct{ Pending, Age int64 }
	err := b.db.GetContext(ctx, &v, b.query)
	return queuehealth.Backlog{Pending: v.Pending, OldestAge: time.Duration(v.Age) * time.Second}, err
}
