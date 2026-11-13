package mysql

import (
	"context"
	"strings"

	"github.com/jmoiron/sqlx"
	"thing-connect/internal/presence"
)

type deviceConnectionStore struct{ db *sqlx.DB }

func NewDeviceConnectionStore(db *sqlx.DB) presence.ConnectionStore {
	return &deviceConnectionStore{db: db}
}

// SaveConnections persists only connection metadata, in a separate worker from heartbeat
// projection. One multi-row statement handles each batch. Delayed or duplicate
// observations cannot overwrite a newer connection's address.
func (s *deviceConnectionStore) SaveConnections(ctx context.Context, batch []presence.Observation) error {
	if len(batch) == 0 {
		return nil
	}
	values := make([]string, 0, len(batch))
	args := make([]any, 0, 3*len(batch))
	for _, o := range batch {
		if o.IP == "" || !strings.HasPrefix(o.ClientID, "sn_") || o.ConnectedAt.IsZero() {
			continue
		}
		values = append(values, "(?,?,?)")
		args = append(args, strings.TrimPrefix(o.ClientID, "sn_"), o.IP, o.ConnectedAt.UTC())
	}
	if len(values) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO device_connection (device_id,last_online_ip,last_online_at) VALUES `+strings.Join(values, ",")+`
 ON DUPLICATE KEY UPDATE last_online_ip=IF(VALUES(last_online_at)>last_online_at,VALUES(last_online_ip),last_online_ip),last_online_at=GREATEST(last_online_at,VALUES(last_online_at))`, args...)
	return err
}
