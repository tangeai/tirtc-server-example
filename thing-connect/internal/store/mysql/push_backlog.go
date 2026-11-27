package mysql

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/queuehealth"
)

type PushBacklog struct {
	db     *sqlx.DB
	query  string
	prefix string
	latest atomic.Pointer[pushQueueSample]
}

func NewPushBacklog(db *sqlx.DB, source bool) *PushBacklog {
	table := "push_delivery"
	statuses := "'pending','leased','sending'"
	prefix := "push_delivery"
	if source {
		table = "recognition_user_event_outbox"
		statuses = "'pending','checking'"
		prefix = "recognition_user_event"
	}
	query := `SELECT COUNT(*) AS pending,COALESCE(TIMESTAMPDIFF(SECOND,MIN(occurred_at),UTC_TIMESTAMP(6)),0) AS age,
 COALESCE(SUM(status='checking'),0) AS checking,COALESCE(SUM(status<>'checking' AND expires_at>UTC_TIMESTAMP(6)),0) AS viable,
 COALESCE(MIN(IF(status<>'checking' AND expires_at>UTC_TIMESTAMP(6),TIMESTAMPDIFF(MICROSECOND,UTC_TIMESTAMP(6),expires_at) DIV 1000,NULL)),0) AS remaining,
 COALESCE(MAX(IF(status<>'checking' AND expires_at>UTC_TIMESTAMP(6),100*TIMESTAMPDIFF(MICROSECOND,occurred_at,UTC_TIMESTAMP(6)) DIV NULLIF(TIMESTAMPDIFF(MICROSECOND,occurred_at,expires_at),0),0)),0) AS used FROM ` + table + " WHERE status IN (" + statuses + ")"
	return &PushBacklog{db: db, query: query, prefix: prefix}
}

type pushQueueSample struct{ Pending, Age, Checking, Viable, Remaining, Used int64 }

func (b *PushBacklog) Backlog(ctx context.Context) (queuehealth.Backlog, error) {
	var v pushQueueSample
	if err := b.db.GetContext(ctx, &v, b.query); err != nil {
		return queuehealth.Backlog{}, err
	}
	b.latest.Store(&v)
	return queuehealth.Backlog{Pending: v.Pending, OldestAge: time.Duration(v.Age) * time.Second}, nil
}
func (b *PushBacklog) Metrics() map[string]int64 {
	v := b.latest.Load()
	if v == nil {
		return nil
	}
	return map[string]int64{b.prefix + "_checking": v.Checking, b.prefix + "_eligible": v.Viable, b.prefix + "_min_remaining_ms": v.Remaining, b.prefix + "_max_budget_used_pct": v.Used}
}
func (b *PushBacklog) TimeBudgetHealth(context.Context) error {
	if v := b.latest.Load(); v != nil && v.Used >= 80 {
		return errors.New("最老有效提醒已消耗至少八成时效预算")
	}
	return nil
}
