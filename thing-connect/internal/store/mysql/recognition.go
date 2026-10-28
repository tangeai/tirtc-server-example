package mysql

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jmoiron/sqlx"
	r "thing-connect/internal/recognition"
	"time"
)

type RecognitionStore struct{ db *sqlx.DB }

func NewRecognitionStore(db *sqlx.DB) *RecognitionStore { return &RecognitionStore{db} }
func recID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func recJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func recOwner(ctx context.Context, tx *sqlx.Tx, a r.Actor, id string) (string, error) {
	var v struct {
		ID   string `db:"id"`
		User int64  `db:"user_id"`
	}
	e := tx.GetContext(ctx, &v, `SELECT CONCAT(id,':',binding_revision) AS id,user_id FROM device_bind WHERE device_id=? FOR UPDATE`, id)
	if errors.Is(e, sql.ErrNoRows) {
		return "", r.ErrForbidden
	}
	if e != nil {
		return "", e
	}
	if v.User <= 0 || a.DeviceID != "" && a.DeviceID != id || a.DeviceID == "" && a.UserID != v.User {
		return "", r.ErrForbidden
	}
	return v.ID, nil
}
func recPolicy(ctx context.Context, tx *sqlx.Tx, id string, bind string) (r.Policy, error) {
	p := r.DefaultPolicy()
	var row struct {
		Bind string `db:"bind_id"`
		JSON string `db:"policy"`
	}
	e := tx.GetContext(ctx, &row, `SELECT bind_id,policy FROM recognition_device WHERE device_id=? FOR UPDATE`, id)
	if e == nil && row.Bind == bind {
		e = json.Unmarshal([]byte(row.JSON), &p)
		return p, e
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return p, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO recognition_device(device_id,bind_id,policy) VALUES(?,?,?) ON DUPLICATE KEY UPDATE bind_id=VALUES(bind_id),policy=VALUES(policy)`, id, bind, recJSON(p))
	return p, e
}
func recSchemes(ctx context.Context, tx *sqlx.Tx, b []r.Binding, strict bool) ([]r.Scheme, error) {
	out := []r.Scheme{}
	for _, v := range b {
		var row struct {
			Raw     string `db:"snapshot"`
			Enabled bool   `db:"enabled"`
		}
		e := tx.GetContext(ctx, &row, `SELECT v.snapshot,s.enabled FROM recognition_scheme_version v JOIN recognition_scheme s ON s.id=v.scheme_id WHERE v.scheme_id=? AND v.version=? FOR SHARE`, v.ID, v.Version)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, r.ErrConflict
		}
		if e != nil {
			return nil, e
		}
		var scheme r.Scheme
		if e = json.Unmarshal([]byte(row.Raw), &scheme); e != nil {
			return nil, e
		}
		if strict && !row.Enabled {
			return nil, r.ErrConflict
		}
		scheme.Enabled = row.Enabled
		out = append(out, scheme)
	}
	return out, nil
}
func recUsage(ctx context.Context, tx *sqlx.Tx, id string) (r.Usage, error) {
	u := r.Usage{Month: time.Now().UTC().Format("2006-01")}
	e := tx.GetContext(ctx, &u.Used, `SELECT COALESCE(SUM(units),0) FROM recognition_task WHERE device_id=? AND month=?`, id, u.Month)
	if e != nil {
		return u, e
	}
	var rows []struct {
		Usage sql.NullString `db:"usage_json"`
		Stage string         `db:"stage"`
	}
	e = tx.SelectContext(ctx, &rows, `SELECT c.usage_json,c.stage FROM recognition_call c JOIN recognition_task t ON t.id=c.task_id WHERE t.device_id=? AND t.month=?`, id, u.Month)
	for _, v := range rows {
		var usage r.CallUsage
		if !v.Usage.Valid || json.Unmarshal([]byte(v.Usage.String), &usage) != nil {
			u.Unknown++
			continue
		}
		if usage.Input == nil && usage.Characters == nil {
			u.Unknown++
		}
		if v.Stage == "analysis" {
			if usage.Input != nil {
				u.InputTokens += *usage.Input
			}
			if usage.Output != nil {
				u.OutputTokens += *usage.Output
			}
		} else if usage.Characters != nil {
			u.TTSCharacters += *usage.Characters
		}
	}
	return u, e
}
func recState(ctx context.Context, tx *sqlx.Tx, id string, bind string) (r.State, error) {
	p, e := recPolicy(ctx, tx, id, bind)
	if e != nil {
		return r.State{}, e
	}
	v := r.State{Policy: p, Limits: r.Limits{MaxFileBytes: 5 << 20, MaxAudioSeconds: 60}, Schemes: []r.Scheme{}}
	schemes, e := recSchemes(ctx, tx, p.Schemes, false)
	if e != nil && !errors.Is(e, r.ErrConflict) {
		return v, e
	}
	v.EffectiveEnabled = e == nil && p.Enabled && p.Allowed && len(schemes) > 0
	for _, x := range schemes {
		if !x.Enabled {
			v.EffectiveEnabled = false
		}
		v.Schemes = append(v.Schemes, r.PublicScheme(x))
		if x.Limits.MaxFileBytes < v.Limits.MaxFileBytes {
			v.Limits.MaxFileBytes = x.Limits.MaxFileBytes
		}
		if x.Limits.MaxAudioSeconds < v.Limits.MaxAudioSeconds {
			v.Limits.MaxAudioSeconds = x.Limits.MaxAudioSeconds
		}
	}
	v.Usage, e = recUsage(ctx, tx, id)
	v.EffectiveEnabled = v.EffectiveEnabled && v.Usage.Used+int64(len(schemes)) <= p.MonthlyLimit
	return v, e
}
func (s *RecognitionStore) State(c context.Context, a r.Actor, id string) (r.State, error) {
	tx, e := s.db.BeginTxx(c, nil)
	if e != nil {
		return r.State{}, e
	}
	defer tx.Rollback()
	b, e := recOwner(c, tx, a, id)
	if e != nil {
		return r.State{}, e
	}
	v, e := recState(c, tx, id, b)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func recEvent(c context.Context, tx *sqlx.Tx, id string, bind string, kind string, data any) error {
	body := map[string]any{"type": kind, "channel": "recognition", "event_id": recID(), "payload": map[string]any{"code": 200, "msg": "ok", "data": data}}
	_, e := tx.ExecContext(c, `INSERT INTO recognition_outbox(device_id,bind_id,payload,next_at) VALUES(?,?,?,UTC_TIMESTAMP(6))`, id, bind, recJSON(body))
	return e
}
func (s *RecognitionStore) SetSelection(c context.Context, a r.Actor, id string, on bool, b []r.Binding, rev int64) (r.State, error) {
	tx, e := s.db.BeginTxx(c, nil)
	if e != nil {
		return r.State{}, e
	}
	defer tx.Rollback()
	owner, e := recOwner(c, tx, a, id)
	if e != nil {
		return r.State{}, e
	}
	p, e := recPolicy(c, tx, id, owner)
	if e != nil {
		return r.State{}, e
	}
	if p.Revision != rev {
		return r.State{}, r.ErrConflict
	}
	if _, e = recSchemes(c, tx, b, on); e != nil {
		return r.State{}, e
	}
	p.Enabled = on
	p.Schemes = b
	if p.Schemes == nil {
		p.Schemes = []r.Binding{}
	}
	p.Revision++
	if _, e = tx.ExecContext(c, `UPDATE recognition_device SET policy=? WHERE device_id=?`, recJSON(p), id); e != nil {
		return r.State{}, e
	}
	if e = recEvent(c, tx, id, owner, "recognition_config_changed", map[string]any{"device_id": id, "revision": p.Revision}); e != nil {
		return r.State{}, e
	}
	v, e := recState(c, tx, id, owner)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (s *RecognitionStore) Types(c context.Context) ([]r.Scheme, error) {
	var raws []string
	e := s.db.SelectContext(c, &raws, `SELECT v.snapshot FROM recognition_scheme s JOIN recognition_scheme_version v ON v.scheme_id=s.id AND v.version=s.version WHERE s.enabled=TRUE ORDER BY s.id LIMIT 200`)
	out := []r.Scheme{}
	for _, raw := range raws {
		var v r.Scheme
		if e = json.Unmarshal([]byte(raw), &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, e
}

type recSnapshot = r.Snapshot
type recRow struct {
	ID             int64  `db:"id"`
	Bind           string `db:"bind_id"`
	Fingerprint    string `db:"fingerprint"`
	ConfigRevision int64  `db:"config_revision"`
	Snapshot       string `db:"snapshot"`
	Lease          string `db:"lease"`
	Media          []byte `db:"media"`
}

func recDecode(row recRow) (recSnapshot, error) {
	var v recSnapshot
	e := json.Unmarshal([]byte(row.Snapshot), &v)
	return v, e
}
func (s *RecognitionStore) Submit(c context.Context, a r.Actor, id string, req r.Request, hash string) (r.Task, bool, error) {
	tx, e := s.db.BeginTxx(c, nil)
	if e != nil {
		return r.Task{}, false, e
	}
	defer tx.Rollback()
	bind, e := recOwner(c, tx, a, id)
	if e != nil {
		return r.Task{}, false, e
	}
	p, e := recPolicy(c, tx, id, bind)
	if e != nil {
		return r.Task{}, false, e
	}
	var row recRow
	e = tx.GetContext(c, &row, `SELECT id,bind_id,fingerprint,config_revision,snapshot FROM recognition_task WHERE device_id=? AND request_id=?`, id, req.ID)
	if e == nil {
		if row.Bind != bind || row.Fingerprint != hash || row.ConfigRevision != req.Revision {
			return r.Task{}, false, r.ErrConflict
		}
		v, e := recDecode(row)
		return v.PublicTask(), false, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return r.Task{}, false, e
	}
	if e = r.ValidatePolicy(p, req.Revision); e != nil {
		return r.Task{}, false, e
	}
	schemes, e := recSchemes(c, tx, p.Schemes, true)
	if e != nil {
		return r.Task{}, false, e
	}
	u, e := recUsage(c, tx, id)
	if e != nil {
		return r.Task{}, false, e
	}
	v, e := r.NewSnapshot(id, req, p, schemes, u, time.Now().UTC())
	if e != nil {
		return r.Task{}, false, e
	}
	_, e = tx.ExecContext(c, `INSERT INTO recognition_task(device_id,request_id,bind_id,fingerprint,config_revision,month,units,status,snapshot,media,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, req.ID, bind, hash, req.Revision, u.Month, len(schemes), "queued", recJSON(v), req.Data, v.Task.CreatedAt)
	if e != nil {
		return r.Task{}, false, e
	}
	return v.Task, true, tx.Commit()
}
func (s *RecognitionStore) owned(c context.Context, a r.Actor, id, rid string) (recRow, recSnapshot, error) {
	tx, e := s.db.BeginTxx(c, nil)
	if e != nil {
		return recRow{}, recSnapshot{}, e
	}
	defer tx.Rollback()
	bind, e := recOwner(c, tx, a, id)
	if e != nil {
		return recRow{}, recSnapshot{}, e
	}
	var row recRow
	e = tx.GetContext(c, &row, `SELECT id,bind_id,fingerprint,config_revision,snapshot FROM recognition_task WHERE device_id=? AND request_id=? AND bind_id=?`, id, rid, bind)
	if errors.Is(e, sql.ErrNoRows) {
		e = r.ErrNotFound
	}
	if e != nil {
		return row, recSnapshot{}, e
	}
	v, e := recDecode(row)
	return row, v, e
}
func (s *RecognitionStore) Get(c context.Context, a r.Actor, id, rid string) (r.Task, error) {
	_, v, e := s.owned(c, a, id, rid)
	return v.PublicTask(), e
}
func (s *RecognitionStore) Audio(c context.Context, a r.Actor, id, rid, item string) ([]byte, error) {
	row, v, e := s.owned(c, a, id, rid)
	if e != nil {
		return nil, e
	}
	ready := false
	for _, x := range v.Task.Results {
		if x.ID == item && x.Speech.Status == "ready" {
			ready = true
		}
	}
	if !ready {
		return nil, r.ErrNotFound
	}
	var b []byte
	e = s.db.GetContext(c, &b, `SELECT a.data FROM recognition_audio a JOIN recognition_task t ON t.id=a.task_id JOIN device_bind d ON CONCAT(d.id,':',d.binding_revision)=t.bind_id AND d.device_id=t.device_id WHERE a.task_id=? AND a.item_id=?`, row.ID, item)
	if errors.Is(e, sql.ErrNoRows) {
		e = r.ErrNotFound
	}
	return b, e
}
func (s *RecognitionStore) List(c context.Context, a r.Actor, id string, cursor int64) ([]r.Task, error) {
	tx, e := s.db.BeginTxx(c, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	bind, e := recOwner(c, tx, a, id)
	if e != nil {
		return nil, e
	}
	var rows []recRow
	e = tx.SelectContext(c, &rows, `SELECT id,snapshot FROM recognition_task WHERE device_id=? AND bind_id=? AND id>? ORDER BY id LIMIT 50`, id, bind, cursor)
	if e != nil {
		return nil, e
	}
	out := []r.Task{}
	for _, row := range rows {
		v, e := recDecode(row)
		if e != nil {
			return nil, e
		}
		task := v.PublicTask()
		task.Cursor = row.ID
		out = append(out, task)
	}
	return out, nil
}

func recSave(c context.Context, tx *sqlx.Tx, row recRow, v recSnapshot) error {
	v.Task.Revision++
	_, e := tx.ExecContext(c, `UPDATE recognition_task SET status=?,snapshot=?,lease='',lease_until=NULL,media=IF(? IN ('queued','processing'),media,NULL) WHERE id=?`, v.Task.Status, recJSON(v), v.Task.Status, row.ID)
	if e != nil {
		return e
	}
	return recEvent(c, tx, v.Task.DeviceID, row.Bind, "recognition_result", v.PublicTask())
}
func (s *RecognitionStore) Claim(c context.Context) (*r.Work, error) {
	tx, e := s.db.BeginTxx(c, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var row recRow
	e = tx.GetContext(c, &row, `SELECT id,bind_id,fingerprint,config_revision,snapshot,media FROM recognition_task WHERE status='queued' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, r.ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	v, e := recDecode(row)
	if e != nil {
		return nil, e
	}
	var ownerCount int
	e = tx.GetContext(c, &ownerCount, `SELECT COUNT(*) FROM device_bind WHERE CONCAT(id,':',binding_revision)=? AND device_id=? AND user_id>0`, row.Bind, v.Task.DeviceID)
	if e != nil {
		return nil, e
	}
	if ownerCount == 0 {
		v.Cancel()
		if e = recSave(c, tx, row, v); e != nil {
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		return nil, r.ErrNotFound
	}
	for i := range v.Task.Results {
		x := &v.Task.Results[i]
		stage := v.NextStage(i)
		if stage == "" {
			continue
		}
		var enabled bool
		e = tx.GetContext(c, &enabled, `SELECT enabled FROM recognition_scheme WHERE id=?`, x.SchemeID)
		if e != nil {
			return nil, e
		}
		if !enabled {
			v.DisableItem(i)
			if e = recSave(c, tx, row, v); e != nil {
				return nil, e
			}
			if e = tx.Commit(); e != nil {
				return nil, e
			}
			return nil, r.ErrNotFound
		}
		lease := recID()
		v.Start(i, stage)
		_, e = tx.ExecContext(c, `UPDATE recognition_task SET status='processing',snapshot=?,lease=?,lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 120 SECOND) WHERE id=?`, recJSON(v), lease, row.ID)
		if e != nil {
			return nil, e
		}
		_, e = tx.ExecContext(c, `INSERT INTO recognition_call(task_id,item_id,stage,status,started_at) VALUES(?,?,?,'processing',UTC_TIMESTAMP(6))`, row.ID, x.ID, stage)
		if e != nil {
			return nil, e
		}
		w := &r.Work{TaskKey: row.ID, ItemIndex: i, Item: *x, Scheme: v.Schemes[i], Data: row.Media, Stage: stage, InternalResult: v.Internal[i], Lease: lease}
		return w, tx.Commit()
	}
	v.Aggregate()
	if e = recSave(c, tx, row, v); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return nil, r.ErrNotFound
}
func (s *RecognitionStore) Complete(c context.Context, done r.Completion) error {
	tx, e := s.db.BeginTxx(c, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var row recRow
	e = tx.GetContext(c, &row, `SELECT id,bind_id,snapshot,lease FROM recognition_task WHERE id=? FOR UPDATE`, done.Work.TaskKey)
	if e != nil {
		return e
	}
	if row.Lease != done.Work.Lease {
		return r.ErrConflict
	}
	v, e := recDecode(row)
	if e != nil {
		return e
	}
	x := &v.Task.Results[done.Work.ItemIndex]
	if done.Work.Stage == "tts" && !done.Failed {
		if _, e = tx.ExecContext(c, `INSERT INTO recognition_audio(task_id,item_id,data) VALUES(?,?,?)`, row.ID, x.ID, done.Audio); e != nil {
			return e
		}
	}
	status := v.Complete(done)
	_, e = tx.ExecContext(c, `UPDATE recognition_call SET status=?,usage_json=?,finished_at=UTC_TIMESTAMP(6) WHERE task_id=? AND item_id=? AND stage=?`, status, recJSON(done.Usage), row.ID, x.ID, done.Work.Stage)
	if e != nil {
		return e
	}
	if e = recSave(c, tx, row, v); e != nil {
		return e
	}
	if v.Task.Status != "queued" && v.Task.Status != "processing" {
		if _, e = tx.ExecContext(c, `UPDATE recognition_task SET media=NULL WHERE id=?`, row.ID); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *RecognitionStore) Recover(c context.Context) error {
	tx, e := s.db.BeginTxx(c, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var rows []recRow
	e = tx.SelectContext(c, &rows, `SELECT id,bind_id,snapshot FROM recognition_task WHERE status='processing' AND lease_until<UTC_TIMESTAMP(6) LIMIT 20 FOR UPDATE SKIP LOCKED`)
	if e != nil {
		return e
	}
	for _, row := range rows {
		v, e := recDecode(row)
		if e != nil {
			return e
		}
		v.Expire()
		if _, e = tx.ExecContext(c, `UPDATE recognition_call SET status='unknown',finished_at=UTC_TIMESTAMP(6) WHERE task_id=? AND status='processing'`, row.ID); e != nil {
			return e
		}
		if e = recSave(c, tx, row, v); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *RecognitionStore) Events(c context.Context) ([]r.Event, error) {
	var rows []struct {
		ID      int64  `db:"id"`
		Device  string `db:"device_id"`
		Payload []byte `db:"payload"`
	}
	e := s.db.SelectContext(c, &rows, `SELECT o.id,o.device_id,o.payload FROM recognition_outbox o JOIN device_bind d ON CONCAT(d.id,':',d.binding_revision)=o.bind_id AND d.device_id=o.device_id WHERE o.delivered=FALSE AND o.attempts<8 AND (o.attempts=0 OR o.next_at<=UTC_TIMESTAMP(6)) ORDER BY o.id LIMIT 20`)
	out := []r.Event{}
	for _, v := range rows {
		out = append(out, r.Event{ID: v.ID, DeviceID: v.Device, Payload: v.Payload})
	}
	return out, e
}
func (s *RecognitionStore) Deliver(c context.Context, v r.Event, ok bool) error {
	_, e := s.db.ExecContext(c, `UPDATE recognition_outbox SET delivered=?,attempts=attempts+1,next_at=TIMESTAMPADD(SECOND,LEAST(300,POW(2,attempts)),UTC_TIMESTAMP(6)) WHERE id=? AND delivered=FALSE`, ok, v.ID)
	return e
}
