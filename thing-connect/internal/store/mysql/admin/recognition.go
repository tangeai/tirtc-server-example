package adminmysql

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jmoiron/sqlx"
	a "thing-connect/internal/admin"
	r "thing-connect/internal/recognition"
	m "thing-connect/internal/store/mysql"
)

type RecognitionStore struct{ db *sqlx.DB }

func NewRecognitionStore(db *sqlx.DB) *RecognitionStore { return &RecognitionStore{db} }
func (s *RecognitionStore) State(c context.Context, id string) (r.State, error) {
	var uid int64
	e := s.db.GetContext(c, &uid, `SELECT user_id FROM device_bind WHERE device_id=?`, id)
	if errors.Is(e, sql.ErrNoRows) {
		return r.State{}, a.ErrNotFound
	}
	if e != nil {
		return r.State{}, e
	}
	return m.NewRecognitionStore(s.db).State(c, r.Actor{UserID: uid}, id)
}
func (s *RecognitionStore) Update(c context.Context, v a.RecognitionMutation) (r.State, error) {
	tx, e := s.db.BeginTxx(c, nil)
	if e != nil {
		return r.State{}, e
	}
	defer tx.Rollback()
	var bind string
	e = tx.GetContext(c, &bind, `SELECT CONCAT(id,':',binding_revision) FROM device_bind WHERE device_id=? AND user_id>0 FOR UPDATE`, v.DeviceID)
	if errors.Is(e, sql.ErrNoRows) {
		return r.State{}, a.ErrNotFound
	}
	if e != nil {
		return r.State{}, e
	}
	p := r.DefaultPolicy()
	var row struct {
		Bind string `db:"bind_id"`
		Raw  string `db:"policy"`
	}
	e = tx.GetContext(c, &row, `SELECT bind_id,policy FROM recognition_device WHERE device_id=? FOR UPDATE`, v.DeviceID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return r.State{}, e
	}
	if e == nil && row.Bind == bind {
		if e = json.Unmarshal([]byte(row.Raw), &p); e != nil {
			return r.State{}, e
		}
	}
	if p.Revision != v.ExpectedRevision {
		return r.State{}, a.ErrConflict
	}
	before, _ := json.Marshal(p)
	p.Allowed = v.Allowed
	p.MonthlyLimit = v.Limit
	p.Revision++
	after, _ := json.Marshal(p)
	_, e = tx.ExecContext(c, `INSERT INTO recognition_device(device_id,bind_id,policy) VALUES(?,?,?) ON DUPLICATE KEY UPDATE bind_id=VALUES(bind_id),policy=VALUES(policy)`, v.DeviceID, bind, string(after))
	if e != nil {
		return r.State{}, e
	}
	payload, _ := json.Marshal(map[string]any{"type": "recognition_config_changed", "event_id": fmt.Sprintf("recognition-config-%s-%s-%d", v.DeviceID, bind, p.Revision), "channel": "recognition", "payload": map[string]any{"code": 200, "msg": "ok", "data": map[string]any{"device_id": v.DeviceID, "revision": p.Revision}}})
	if _, e = tx.ExecContext(c, `INSERT INTO recognition_outbox(device_id,bind_id,payload) VALUES(?,?,?)`, v.DeviceID, bind, string(payload)); e != nil {
		return r.State{}, e
	}
	v.Audit.Before = string(before)
	v.Audit.After = string(after)
	if e = insertAuditTx(c, tx, v.Audit); e != nil {
		return r.State{}, e
	}
	if e = tx.Commit(); e != nil {
		return r.State{}, e
	}
	return s.State(c, v.DeviceID)
}
func (s *RecognitionStore) Schemes(c context.Context) ([]r.Scheme, error) {
	var rows []struct {
		ID       string `db:"id"`
		Revision int64  `db:"revision"`
		Version  int64  `db:"version"`
		Enabled  bool   `db:"enabled"`
		Raw      string `db:"draft"`
	}
	if e := s.db.SelectContext(c, &rows, `SELECT id,revision,version,enabled,draft FROM recognition_scheme ORDER BY updated_at DESC LIMIT 200`); e != nil {
		return nil, e
	}
	out := []r.Scheme{}
	for _, x := range rows {
		var v r.Scheme
		if e := json.Unmarshal([]byte(x.Raw), &v); e != nil {
			return nil, e
		}
		v.ID = x.ID
		v.Revision = x.Revision
		v.Version = x.Version
		v.Enabled = x.Enabled
		out = append(out, v)
	}
	return out, nil
}
func (s *RecognitionStore) SaveScheme(c context.Context, m a.RecognitionSchemeMutation) (r.Scheme, error) {
	tx, e := s.db.BeginTxx(c, nil)
	if e != nil {
		return r.Scheme{}, e
	}
	defer tx.Rollback()
	v := m.Scheme
	var old struct {
		Revision int64  `db:"revision"`
		Version  int64  `db:"version"`
		Enabled  bool   `db:"enabled"`
		Raw      string `db:"draft"`
	}
	if v.ID == "" {
		if m.Action != "save" || m.Revision != 0 {
			return v, a.ErrConflict
		}
		var b [16]byte
		if _, e = rand.Read(b[:]); e != nil {
			return v, e
		}
		v.ID = hex.EncodeToString(b[:])
		v.Enabled = false
		v.Version = 0
	} else {
		e = tx.GetContext(c, &old, `SELECT revision,version,enabled,draft FROM recognition_scheme WHERE id=? FOR UPDATE`, v.ID)
		if errors.Is(e, sql.ErrNoRows) {
			return v, a.ErrNotFound
		}
		if e != nil {
			return v, e
		}
		if old.Revision != m.Revision {
			return v, a.ErrConflict
		}
		if m.Action != "save" {
			id := v.ID
			enabled := v.Enabled
			if e = json.Unmarshal([]byte(old.Raw), &v); e != nil {
				return v, e
			}
			v.ID = id
			v.Enabled = enabled
		} else {
			v.Enabled = old.Enabled
		}
		v.Version = old.Version
	}
	v.Revision = m.Revision + 1
	if m.Action == "publish" {
		if e = r.ValidateScheme(v); e != nil {
			return v, e
		}
		v.Version++
		v.Enabled = true
		raw, _ := json.Marshal(v)
		if _, e = tx.ExecContext(c, `INSERT INTO recognition_scheme_version(scheme_id,version,snapshot) VALUES(?,?,?)`, v.ID, v.Version, string(raw)); e != nil {
			return v, e
		}
	}
	if m.Action == "status" && v.Version == 0 {
		return v, r.ErrInvalid
	}
	raw, _ := json.Marshal(v)
	_, e = tx.ExecContext(c, `INSERT INTO recognition_scheme(id,revision,version,enabled,draft) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE revision=VALUES(revision),version=VALUES(version),enabled=VALUES(enabled),draft=VALUES(draft)`, v.ID, v.Revision, v.Version, v.Enabled, string(raw))
	if e != nil {
		return v, e
	}
	m.Audit.ResourceID = v.ID
	m.Audit.Before = old.Raw
	m.Audit.After = string(raw)
	if e = insertAuditTx(c, tx, m.Audit); e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (s *RecognitionStore) Records(c context.Context, id string) (any, error) {
	var rows []struct {
		ID  int64  `db:"id"`
		Raw string `db:"snapshot"`
	}
	if e := s.db.SelectContext(c, &rows, `SELECT id,snapshot FROM recognition_task WHERE (?='' OR device_id=?) ORDER BY id DESC LIMIT 50`, id, id); e != nil {
		return nil, e
	}
	out := []map[string]any{}
	for _, row := range rows {
		var snap struct {
			Task r.Task `json:"task"`
		}
		if e := json.Unmarshal([]byte(row.Raw), &snap); e != nil {
			return nil, e
		}
		calls := []map[string]any{}
		rr, e := s.db.QueryxContext(c, `SELECT item_id,stage,status,usage_json,started_at,finished_at FROM recognition_call WHERE task_id=? ORDER BY id`, row.ID)
		if e != nil {
			return nil, e
		}
		for rr.Next() {
			var v struct {
				Item     string         `db:"item_id"`
				Stage    string         `db:"stage"`
				Status   string         `db:"status"`
				Usage    sql.NullString `db:"usage_json"`
				Started  sql.NullTime   `db:"started_at"`
				Finished sql.NullTime   `db:"finished_at"`
			}
			if e = rr.StructScan(&v); e != nil {
				rr.Close()
				return nil, e
			}
			var u any
			if v.Usage.Valid {
				if e = json.Unmarshal([]byte(v.Usage.String), &u); e != nil {
					rr.Close()
					return nil, e
				}
			}
			calls = append(calls, map[string]any{"item_id": v.Item, "stage": v.Stage, "status": v.Status, "usage": u, "started_at": v.Started.Time, "finished_at": v.Finished.Time})
		}
		e = rr.Err()
		rr.Close()
		if e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"task": snap.Task, "calls": calls})
	}
	return out, nil
}
