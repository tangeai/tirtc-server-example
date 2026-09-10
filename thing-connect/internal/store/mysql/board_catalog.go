package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/boardcatalog"
)

const boardColumns = `id,vendor,name,model,chip,summary,capabilities,adaptation_status,image_url,purchase_url,repository_url,firmware_url,flashing_guide_url,effect_video_url,detail_slug,sort_order,publish_status,revision,published_at,created_by,updated_by,created_at,updated_at`

type boardRow struct {
	boardcatalog.Board
	CapabilitiesJSON []byte `db:"capabilities"`
}

type BoardCatalogStore struct{ db *sqlx.DB }

func NewBoardCatalogStore(db *sqlx.DB) *BoardCatalogStore { return &BoardCatalogStore{db: db} }

func (s *BoardCatalogStore) ListPublished(ctx context.Context, limit int) ([]boardcatalog.Board, error) {
	if limit <= 0 || limit > boardcatalog.PublicLimit {
		limit = boardcatalog.PublicLimit
	}
	var rows []boardRow
	if err := s.db.SelectContext(ctx, &rows, `SELECT `+boardColumns+` FROM board_resources WHERE publish_status='published' ORDER BY sort_order,name,id LIMIT ?`, limit); err != nil {
		return nil, fmt.Errorf("list published boards: %w", err)
	}
	return decodeBoardRows(rows)
}

func (s *BoardCatalogStore) PublishedBySlug(ctx context.Context, slug string) (boardcatalog.Board, error) {
	var row boardRow
	if err := s.db.GetContext(ctx, &row, `SELECT `+boardColumns+` FROM board_resources WHERE detail_slug=? AND publish_status='published'`, slug); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return boardcatalog.Board{}, boardcatalog.ErrNotFound
		}
		return boardcatalog.Board{}, fmt.Errorf("get published board: %w", err)
	}
	return decodeBoardRow(row)
}

func decodeBoardRows(rows []boardRow) ([]boardcatalog.Board, error) {
	boards := make([]boardcatalog.Board, 0, len(rows))
	for _, row := range rows {
		board, err := decodeBoardRow(row)
		if err != nil {
			return nil, err
		}
		boards = append(boards, board)
	}
	return boards, nil
}

func decodeBoardRow(row boardRow) (boardcatalog.Board, error) {
	board := row.Board
	if err := json.Unmarshal(row.CapabilitiesJSON, &board.Capabilities); err != nil {
		return boardcatalog.Board{}, fmt.Errorf("decode board capabilities: %w", err)
	}
	if board.Capabilities == nil {
		board.Capabilities = []string{}
	}
	return board, nil
}
