package mysql

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/model"
	"thing-connect/internal/store"
)

type userResourceStore struct{ db *sqlx.DB }

func NewUserResourceStore(db *sqlx.DB) store.UserResourceStore { return &userResourceStore{db} }

func (s *userResourceStore) Add(ctx context.Context, userID int64, typ, resourceID, name string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT IGNORE INTO ai_user_resource (user_id, type, resource_id, name) VALUES (?,?,?,?)`,
		userID, typ, resourceID, name)
	if err != nil {
		return fmt.Errorf("userResourceStore.Add: %w", err)
	}
	return nil
}

func (s *userResourceStore) Remove(ctx context.Context, userID int64, typ, resourceID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM ai_user_resource WHERE user_id=? AND type=? AND resource_id=?`,
		userID, typ, resourceID)
	if err != nil {
		return fmt.Errorf("userResourceStore.Remove: %w", err)
	}
	return nil
}

func (s *userResourceStore) List(ctx context.Context, userID int64, typ string) ([]model.UserResource, error) {
	var rows []model.UserResource
	err := s.db.SelectContext(ctx, &rows,
		`SELECT id, user_id, type, resource_id, name, created_at
		 FROM ai_user_resource WHERE user_id=? AND type=? ORDER BY created_at DESC`,
		userID, typ)
	if err != nil {
		return nil, fmt.Errorf("userResourceStore.List: %w", err)
	}
	if rows == nil {
		rows = []model.UserResource{}
	}
	return rows, nil
}

func (s *userResourceStore) Count(ctx context.Context, userID int64, typ string) (int, error) {
	var n int
	err := s.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM ai_user_resource WHERE user_id=? AND type=?`,
		userID, typ)
	if err != nil {
		return 0, fmt.Errorf("userResourceStore.Count: %w", err)
	}
	return n, nil
}

func (s *userResourceStore) UpdateName(ctx context.Context, userID int64, typ, resourceID, name string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE ai_user_resource SET name=? WHERE user_id=? AND type=? AND resource_id=?`,
		name, userID, typ, resourceID)
	if err != nil {
		return fmt.Errorf("userResourceStore.UpdateName: %w", err)
	}
	return nil
}

func (s *userResourceStore) Exists(ctx context.Context, userID int64, typ, resourceID string) (bool, error) {
	var exists bool
	err := s.db.GetContext(ctx, &exists,
		`SELECT EXISTS(SELECT 1 FROM ai_user_resource WHERE user_id=? AND type=? AND resource_id=?)`,
		userID, typ, resourceID)
	if err != nil {
		return false, fmt.Errorf("userResourceStore.Exists: %w", err)
	}
	return exists, nil
}
