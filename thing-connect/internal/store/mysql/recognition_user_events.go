package mysql

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	r "thing-connect/internal/recognition"
)

func (s *RecognitionStore) ClaimUserEvents(ctx context.Context, limit int) ([]r.UserEvent, error) {
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var rows []struct {
		ID                                                          int64
		Device, Bind, Source, Kind, Registered, Occurred, LabelText string
		Final                                                       int
	}
	err = tx.SelectContext(ctx, &rows, `SELECT id,device_id AS device,bind_id AS bind,source_event_id AS source,event_type AS kind,label_text AS labeltext,
 DATE_FORMAT(registered_at,'%Y-%m-%dT%H:%i:%s.%fZ') AS registered,
 DATE_FORMAT(occurred_at,'%Y-%m-%dT%H:%i:%s.%fZ') AS occurred,
 IF(registered_at<=UTC_TIMESTAMP(6)-INTERVAL 24 HOUR,confirm_attempts+1,0) AS final
 FROM recognition_user_event_outbox WHERE status='pending' AND next_at<=UTC_TIMESTAMP(6)
 ORDER BY next_at,id LIMIT ? FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []r.UserEvent{}, nil
	}
	events := make([]r.UserEvent, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	lease := recID()
	for _, row := range rows {
		v := r.UserEvent{ID: row.ID, Lease: lease, FinalAttempt: row.Final}
		v.Event.ID = row.Source
		v.Event.Device = row.Device
		v.Event.Bind = row.Bind
		v.Event.Kind = row.Kind
		v.Event.LabelText = row.LabelText
		v.Event.Registered, err = time.Parse(time.RFC3339Nano, row.Registered)
		if err != nil {
			return nil, err
		}
		v.Event.Occurred, err = time.Parse(time.RFC3339Nano, row.Occurred)
		if err != nil {
			return nil, err
		}
		events = append(events, v)
		ids = append(ids, row.ID)
	}
	query, args, err := sqlx.In(`UPDATE recognition_user_event_outbox SET lease=?,next_at=UTC_TIMESTAMP(6)+INTERVAL 15 SECOND,attempts=attempts+1,
 confirm_attempts=IF(registered_at<=UTC_TIMESTAMP(6)-INTERVAL 24 HOUR,confirm_attempts+1,0) WHERE id IN (?)`, lease, ids)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return nil, err
	}
	return events, tx.Commit()
}

// FinishUserEvents groups equal acknowledgements while fencing every member by its leased row ID.
func (s *RecognitionStore) FinishUserEvents(ctx context.Context, events []r.UserEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.BeginTxx(ctx, claimTx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type group struct{ lease, result string }
	groups := map[group][]int64{}
	for _, v := range events {
		k := group{v.Lease, v.Outcome}
		groups[k] = append(groups[k], v.ID)
	}
	for k, ids := range groups {
		query, args, err := sqlx.In(`UPDATE recognition_user_event_outbox SET status=?,lease='',next_at=TIMESTAMPADD(SECOND,1<<LEAST(8,attempts),UTC_TIMESTAMP(6)) WHERE id IN (?) AND status='pending' AND lease=?`, k.result, ids, k.lease)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *RecognitionStore) CleanUserEvents(ctx context.Context) error {
	return cleanPushBatch(ctx, s.db, `DELETE FROM recognition_user_event_outbox WHERE status<>'pending' AND registered_at<UTC_TIMESTAMP(6)-INTERVAL 72 HOUR LIMIT 1000`)
}
