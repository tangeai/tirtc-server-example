package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"thing-connect/internal/room"
)

type RoomStore struct{ db *sqlx.DB }

func NewRoomStore(db *sqlx.DB) *RoomStore { return &RoomStore{db: db} }

type roomTx struct {
	tx  *sqlx.Tx
	ctx context.Context
}

func (s *RoomStore) Transaction(ctx context.Context, fn func(room.Tx) error) error {
	// Deadlocks from opposing device/room transitions are retried as whole local
	// transactions. The callback must not perform remote effects.
	for attempt := 0; attempt < 4; attempt++ {
		tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return err
		}
		err = fn(&roomTx{tx: tx, ctx: ctx})
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		var de *driver.MySQLError
		if !errors.As(err, &de) || (de.Number != 1213 && de.Number != 1205) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 10 * time.Millisecond):
		}
	}
	return errors.New("room transaction retry exhausted")
}
func (t *roomTx) Owner(device string) (int64, error) {
	var owner int64
	err := t.tx.GetContext(t.ctx, &owner, "SELECT user_id FROM device_bind WHERE device_id=? FOR UPDATE", device)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return owner, err
}
func (t *roomTx) Assignment(device string) (room.Assignment, error) {
	_, err := t.tx.ExecContext(t.ctx, "INSERT INTO call_assignments(device_id) VALUES(?) ON DUPLICATE KEY UPDATE device_id=VALUES(device_id)", device)
	if err != nil {
		return room.Assignment{}, err
	}
	var a room.Assignment
	err = t.tx.QueryRowContext(t.ctx, "SELECT device_id,owner_user_id,room_id,desired_state,assignment_version,state,session_id FROM call_assignments WHERE device_id=? FOR UPDATE", device).Scan(&a.DeviceID, &a.Owner, &a.RoomID, &a.Desired, &a.Version, &a.State, &a.SessionID)
	return a, err
}
func (t *roomTx) SaveAssignment(a room.Assignment) error {
	_, e := t.tx.ExecContext(t.ctx, "UPDATE call_assignments SET owner_user_id=?,room_id=?,desired_state=?,assignment_version=?,state=?,session_id=? WHERE device_id=?", a.Owner, a.RoomID, a.Desired, a.Version, a.State, a.SessionID, a.DeviceID)
	return e
}
func (t *roomTx) Room(id string, code bool) (room.Room, error) {
	if code {
		var resolved string
		e := t.tx.GetContext(t.ctx, &resolved, "SELECT room_id FROM call_room_codes WHERE room_code=?", id)
		if errors.Is(e, sql.ErrNoRows) {
			return room.Room{}, room.ErrNotFound
		}
		if e != nil {
			return room.Room{}, e
		}
		id = resolved
	}
	var r room.Room
	err := t.tx.QueryRowContext(t.ctx, "SELECT room_id,room_code,owner_user_id,password_verifier,status,empty_deadline,closed_at,created_at,participant_limit FROM call_rooms WHERE room_id=? FOR UPDATE", id).Scan(&r.ID, &r.Code, &r.Owner, &r.Verifier, &r.Status, &r.EmptyDeadline, &r.ClosedAt, &r.CreatedAt, &r.Limit)
	if errors.Is(err, sql.ErrNoRows) {
		err = room.ErrNotFound
	}
	return r, err
}
func (t *roomTx) CreateRoom(r room.Room) error {
	// Delete only a cooled reservation, then let the unique insert decide who wins.
	if _, e := t.tx.ExecContext(t.ctx, "DELETE FROM call_room_codes WHERE room_code=? AND reusable_at<=?", r.Code, r.CreatedAt); e != nil {
		return e
	}
	if _, e := t.tx.ExecContext(t.ctx, "INSERT INTO call_room_codes(room_code,room_id) VALUES(?,?)", r.Code, r.ID); e != nil {
		var de *driver.MySQLError
		if errors.As(e, &de) && de.Number == 1062 {
			return room.ErrCodeTaken
		}
		return e
	}
	_, e := t.tx.ExecContext(t.ctx, "INSERT INTO call_rooms(room_id,room_code,owner_user_id,password_verifier,status,empty_deadline,created_at,participant_limit) VALUES(?,?,?,?,?,?,?,?)", r.ID, r.Code, r.Owner, r.Verifier, r.Status, r.EmptyDeadline, r.CreatedAt, r.Limit)
	return e
}
func (t *roomTx) SaveRoom(r room.Room) error {
	_, e := t.tx.ExecContext(t.ctx, "UPDATE call_rooms SET status=?,empty_deadline=?,closed_at=? WHERE room_id=?", r.Status, r.EmptyDeadline, r.ClosedAt, r.ID)
	if e != nil {
		return e
	}
	if r.ClosedAt != nil {
		_, e = t.tx.ExecContext(t.ctx, "UPDATE call_room_codes SET reusable_at=? WHERE room_id=? AND reusable_at IS NULL", r.ReusableAt, r.ID)
	}
	return e
}
func (t *roomTx) Leases(id string) ([]room.Lease, error) {
	rows, e := t.tx.QueryContext(t.ctx, "SELECT device_id,room_id,session_id,assignment_version,state,expires_at FROM call_leases WHERE room_id=?", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var leases []room.Lease
	for rows.Next() {
		var l room.Lease
		if e = rows.Scan(&l.DeviceID, &l.RoomID, &l.SessionID, &l.Version, &l.State, &l.Expires); e != nil {
			return nil, e
		}
		leases = append(leases, l)
	}
	return leases, rows.Err()
}
func (t *roomTx) Lease(device string) (room.Lease, error) {
	var l room.Lease
	e := t.tx.QueryRowContext(t.ctx, "SELECT device_id,room_id,session_id,assignment_version,state,expires_at FROM call_leases WHERE device_id=?", device).Scan(&l.DeviceID, &l.RoomID, &l.SessionID, &l.Version, &l.State, &l.Expires)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return l, e
}
func (t *roomTx) SaveLease(l room.Lease) error {
	_, e := t.tx.ExecContext(t.ctx, `INSERT INTO call_leases(device_id,room_id,session_id,assignment_version,state,expires_at) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE room_id=VALUES(room_id),session_id=VALUES(session_id),assignment_version=VALUES(assignment_version),state=VALUES(state),expires_at=VALUES(expires_at)`, l.DeviceID, l.RoomID, l.SessionID, l.Version, l.State, l.Expires)
	return e
}
func (t *roomTx) PasswordFailures(device string) (int, time.Time, error) {
	var failures int
	var until sql.NullTime
	e := t.tx.QueryRowContext(t.ctx, "SELECT password_failures,locked_until FROM call_assignments WHERE device_id=?", device).Scan(&failures, &until)
	return failures, until.Time, e
}
func (t *roomTx) SetPasswordFailures(device string, n int, until time.Time) error {
	var nullable any
	if !until.IsZero() {
		nullable = until
	}
	_, e := t.tx.ExecContext(t.ctx, "UPDATE call_assignments SET password_failures=?,locked_until=? WHERE device_id=?", n, nullable, device)
	return e
}
func (t *roomTx) Notify(e room.Event) error {
	_, err := t.tx.ExecContext(t.ctx, "INSERT INTO call_outbox(event_id,device_id,assignment_version) VALUES(?,?,?)", e.ID, e.DeviceID, e.Version)
	return err
}
func (t *roomTx) CloseAssignments(id string) error {
	if _, e := t.tx.ExecContext(t.ctx, `INSERT INTO call_outbox(event_id,device_id,assignment_version) SELECT REPLACE(UUID(),'-',''),device_id,assignment_version+1 FROM call_assignments WHERE room_id=? AND desired_state='joined'`, id); e != nil {
		return e
	}
	_, e := t.tx.ExecContext(t.ctx, "UPDATE call_assignments SET desired_state='left',state='room_closed',assignment_version=assignment_version+1,session_id='' WHERE room_id=? AND desired_state='joined'", id)
	return e
}
func (s *RoomStore) DueRooms(ctx context.Context, limit int) ([]string, error) {
	var ids []string
	e := s.db.SelectContext(ctx, &ids, `SELECT room_id FROM call_rooms r WHERE status<>'closed' AND ((empty_deadline IS NOT NULL AND empty_deadline<=NOW(6)) OR (empty_deadline IS NULL AND NOT EXISTS(SELECT 1 FROM call_leases l WHERE l.room_id=r.room_id AND l.state<>'ended' AND l.expires_at>NOW(6)))) ORDER BY created_at LIMIT ?`, limit)
	return ids, e
}
func (s *RoomStore) Events(ctx context.Context, limit int) ([]room.Event, error) {
	tx, e := s.db.BeginTxx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer func() { _ = tx.Rollback() }()
	rows, e := tx.QueryContext(ctx, "SELECT event_id,device_id,assignment_version,attempts FROM call_outbox WHERE next_attempt_at<=NOW() ORDER BY next_attempt_at LIMIT ? FOR UPDATE SKIP LOCKED", limit)
	if e != nil {
		return nil, e
	}
	var events []room.Event
	for rows.Next() {
		var event room.Event
		if e = rows.Scan(&event.ID, &event.DeviceID, &event.Version, &event.Attempts); e != nil {
			_ = rows.Close()
			return nil, e
		}
		events = append(events, event)
	}
	e = rows.Err()
	_ = rows.Close()
	if e != nil {
		return nil, e
	}
	for _, event := range events {
		if _, e = tx.ExecContext(ctx, "UPDATE call_outbox SET next_attempt_at=DATE_ADD(NOW(), INTERVAL 5 MINUTE) WHERE event_id=?", event.ID); e != nil {
			return nil, e
		}
	}
	return events, tx.Commit()
}
func (s *RoomStore) CompleteEvent(ctx context.Context, event room.Event, ok bool) error {
	if ok {
		_, e := s.db.ExecContext(ctx, "DELETE FROM call_outbox WHERE event_id=?", event.ID)
		return e
	}
	attempts := event.Attempts + 1
	if attempts > 8 {
		attempts = 8
	}
	delay := 1 << attempts
	_, e := s.db.ExecContext(ctx, "UPDATE call_outbox SET attempts=?,next_attempt_at=DATE_ADD(NOW(), INTERVAL ? SECOND) WHERE event_id=?", attempts, delay, event.ID)
	return e
}

var _ room.Repository = (*RoomStore)(nil)
