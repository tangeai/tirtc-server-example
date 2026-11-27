package mysql

import (
	"context"
	"database/sql"
	"errors"
	"hash/crc32"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
)

func utcStamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000000") }

const queueBuckets = 32

func eventBucket(device, id string) int {
	return int(crc32.ChecksumIEEE([]byte(device+"/"+id)) % queueBuckets)
}

// Serialize admissions per shard. An indexed existence check against live rows
// also covers every cancellation and crash without a separate counter repair job.
func queueRoom(ctx context.Context, tx *sqlx.Tx, source bool, bucket, capacity int) (bool, error) {
	queue := "delivery"
	query := `SELECT id FROM push_delivery WHERE bucket=? AND status IN ('pending','leased','sending') LIMIT 1 OFFSET ?`
	if source {
		queue = "source"
		query = `SELECT id FROM recognition_user_event_outbox WHERE bucket=? AND status='pending' LIMIT 1 OFFSET ?`
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO push_queue_bucket(queue_name,bucket) VALUES(?,?) ON DUPLICATE KEY UPDATE bucket=bucket`, queue, bucket)
	if err != nil {
		return false, err
	}
	limit := capacity / queueBuckets
	if bucket < capacity%queueBuckets {
		limit++
	}
	var id int64
	err = tx.GetContext(ctx, &id, query, bucket, limit-1)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}

// Labels originate only from the local state machine.
type outcomeCounts struct{ counts sync.Map }

func (m *outcomeCounts) add(label string, n int64) {
	v, _ := m.counts.LoadOrStore(label, new(atomic.Int64))
	v.(*atomic.Int64).Add(n)
}
func (m *outcomeCounts) metrics(prefix string) map[string]int64 {
	out := map[string]int64{}
	m.counts.Range(func(k, v any) bool { out[prefix+k.(string)+"_total"] = v.(*atomic.Int64).Load(); return true })
	return out
}
