package adminmysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	adminapp "thing-connect/internal/admin"
	"thing-connect/internal/ai/roletypes"
)

const roleTypeColumns = "id,name,voice_id,voice_model,prompt,sort_order,status,revision,language,source_voice_id,match_kind"

type RoleTypeStore struct{ db *sqlx.DB }

func NewRoleTypeStore(db *sqlx.DB) *RoleTypeStore { return &RoleTypeStore{db: db} }
func (s *RoleTypeStore) List(ctx context.Context, published bool) ([]roletypes.Type, error) {
	items := []roletypes.Type{}
	query := "SELECT " + roleTypeColumns + " FROM ai_role_types"
	if published {
		query += " WHERE status='published' AND voice_id<>'' AND prompt<>'' AND voice_model IN ('cosyvoice-v3','cosyvoice-v3-flash','seed-tts-2.0')"
	}
	query += " ORDER BY sort_order,name,id"
	if err := s.db.SelectContext(ctx, &items, query); err != nil {
		return nil, fmt.Errorf("list role types: %w", err)
	}
	return items, nil
}
func (s *RoleTypeStore) Get(ctx context.Context, id string) (roletypes.Type, error) {
	return roleTypeByID(ctx, s.db, id, false)
}
func roleTypeByID(ctx context.Context, q queryer, id string, lock bool) (roletypes.Type, error) {
	var v roletypes.Type
	query := "SELECT " + roleTypeColumns + " FROM ai_role_types WHERE id=?"
	if lock {
		query += " FOR UPDATE"
	}
	err := q.GetContext(ctx, &v, query, id)
	if errors.Is(err, sql.ErrNoRows) {
		err = adminapp.ErrNotFound
	}
	return v, err
}
func (s *RoleTypeStore) Save(ctx context.Context, m adminapp.RoleTypeMutation) (roletypes.Type, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return roletypes.Type{}, err
	}
	defer tx.Rollback()
	v := m.Type
	if m.Action == "create" {
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_role_types (id,name,voice_id,voice_model,prompt,sort_order,status,revision,created_by,updated_by,language,source_voice_id,match_kind) VALUES (?,?,?,?,?,?,'draft',1,?,?,?,?,?)`, v.ID, v.Name, v.VoiceID, v.VoiceModel, v.Prompt, v.SortOrder, m.Audit.AdminUserID, m.Audit.AdminUserID, v.Language, v.SourceVoiceID, v.MatchKind)
	} else {
		before, e := roleTypeByID(ctx, tx, v.ID, true)
		if e != nil {
			return roletypes.Type{}, e
		}
		if before.Revision != m.ExpectedRevision {
			return roletypes.Type{}, adminapp.ErrConflict
		}
		m.Audit.Before = roleTypeJSON(before)
		_, err = tx.ExecContext(ctx, `UPDATE ai_role_types SET name=?,voice_id=?,voice_model=?,prompt=?,sort_order=?,status=?,revision=revision+1,updated_by=?,language=?,source_voice_id=?,match_kind=? WHERE id=? AND revision=?`, v.Name, v.VoiceID, v.VoiceModel, v.Prompt, v.SortOrder, v.Status, m.Audit.AdminUserID, v.Language, v.SourceVoiceID, v.MatchKind, v.ID, m.ExpectedRevision)
	}
	if err != nil {
		var duplicate *mysql.MySQLError
		if errors.As(err, &duplicate) && duplicate.Number == 1062 {
			return roletypes.Type{}, adminapp.ErrConflict
		}
		return roletypes.Type{}, err
	}
	saved, err := roleTypeByID(ctx, tx, v.ID, false)
	if err != nil {
		return roletypes.Type{}, err
	}
	m.Audit.After = roleTypeJSON(saved)
	if err = insertAuditTx(ctx, tx, m.Audit); err != nil {
		return roletypes.Type{}, err
	}
	if err = tx.Commit(); err != nil {
		return roletypes.Type{}, err
	}
	return saved, nil
}
func roleTypeJSON(v roletypes.Type) string { raw, _ := json.Marshal(v); return string(raw) }
