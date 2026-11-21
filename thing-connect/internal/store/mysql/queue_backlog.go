package mysql

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"thing-connect/internal/queuehealth"
)

type queueBacklog struct {
	db    *sqlx.DB
	query string
}

func (s *queueBacklog) Backlog(ctx context.Context) (queuehealth.Backlog, error) {
	var b queuehealth.Backlog
	var ageMS int64
	err := s.db.QueryRowxContext(ctx, s.query).Scan(&b.Pending, &b.Exhausted, &ageMS)
	b.OldestAge = time.Duration(max(0, ageMS)) * time.Millisecond
	return b, err
}

func NewRemarkBacklog(db *sqlx.DB) queuehealth.Source {
	return &queueBacklog{db, `SELECT COUNT(*),0,COALESCE(TIMESTAMPDIFF(MICROSECOND,MIN(pending_since),NOW(6)) DIV 1000,0) FROM voip_remark_outbox`}
}

func NewAICloudBacklog(db *sqlx.DB) queuehealth.Source {
	return &queueBacklog{db, `SELECT COUNT(*),0,COALESCE(TIMESTAMPDIFF(MICROSECOND,MIN(pending_since),NOW(6)) DIV 1000,0) FROM ai_cloud_role_target WHERE next_attempt_at<'9999-12-31 23:59:59'`}
}

func NewRecognitionBacklog(db *sqlx.DB) queuehealth.Source {
	return &queueBacklog{db, `SELECT COUNT(*),COALESCE(SUM(o.attempts>=8 AND o.next_at<=UTC_TIMESTAMP(6)),0),COALESCE(TIMESTAMPDIFF(MICROSECOND,MIN(o.pending_since),NOW(6)) DIV 1000,0) FROM recognition_outbox o JOIN device_bind d ON d.device_id=o.device_id AND CONCAT(d.id,':',d.binding_revision)=o.bind_id WHERE o.delivered=FALSE`}
}
