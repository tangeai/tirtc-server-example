package adminmysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	adminapp "thing-connect/internal/admin"
	"thing-connect/internal/boardcatalog"
)

const boardColumns = `id,vendor,name,model,chip,summary,capabilities,adaptation_status,image_url,purchase_url,repository_url,firmware_url,flashing_guide_url,effect_video_url,detail_slug,sort_order,publish_status,revision,published_at,created_by,updated_by,created_at,updated_at`

type boardRow struct {
	boardcatalog.Board
	CapabilitiesJSON []byte `db:"capabilities"`
}

type BoardCommandStore struct{ db *sqlx.DB }

func NewBoardCommandStore(db *sqlx.DB) *BoardCommandStore { return &BoardCommandStore{db: db} }

func (s *BoardCommandStore) List(ctx context.Context) ([]boardcatalog.Board, error) {
	var rows []boardRow
	if err := s.db.SelectContext(ctx, &rows, `SELECT `+boardColumns+` FROM board_resources ORDER BY sort_order,name,id`); err != nil {
		return nil, fmt.Errorf("admin list boards: %w", err)
	}
	return decodeBoards(rows)
}

func (s *BoardCommandStore) Create(ctx context.Context, mutation adminapp.BoardMutation) (boardcatalog.Board, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return boardcatalog.Board{}, err
	}
	defer tx.Rollback()
	capabilities, _ := json.Marshal(mutation.Board.Capabilities)
	_, err = tx.ExecContext(ctx, `INSERT INTO board_resources
		(id,vendor,name,model,chip,summary,capabilities,adaptation_status,image_url,purchase_url,repository_url,firmware_url,flashing_guide_url,effect_video_url,detail_slug,sort_order,publish_status,revision,created_by,updated_by)
		VALUES (?,?,?,?,?,?,CAST(? AS JSON),?,?,?,?,?,?,?,?,?,'draft',1,?,?)`,
		mutation.Board.ID, mutation.Board.Vendor, mutation.Board.Name, mutation.Board.Model, mutation.Board.Chip,
		mutation.Board.Summary, string(capabilities), mutation.Board.AdaptationStatus, mutation.Board.ImageURL,
		mutation.Board.PurchaseURL, mutation.Board.RepositoryURL, mutation.Board.FirmwareURL,
		mutation.Board.FlashingGuideURL, mutation.Board.EffectVideoURL, mutation.Board.DetailSlug,
		mutation.Board.SortOrder, mutation.Board.CreatedBy, mutation.Board.UpdatedBy)
	if err != nil {
		return boardcatalog.Board{}, boardWriteError(err)
	}
	if err := insertAuditTx(ctx, tx, mutation.Audit); err != nil {
		return boardcatalog.Board{}, fmt.Errorf("audit board create: %w", err)
	}
	board, err := boardByID(ctx, tx, mutation.Board.ID, false)
	if err != nil {
		return boardcatalog.Board{}, err
	}
	if err := tx.Commit(); err != nil {
		return boardcatalog.Board{}, err
	}
	return board, nil
}

func (s *BoardCommandStore) Update(ctx context.Context, mutation adminapp.BoardMutation) (boardcatalog.Board, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return boardcatalog.Board{}, err
	}
	defer tx.Rollback()
	before, err := boardByID(ctx, tx, mutation.Board.ID, true)
	if err != nil {
		return boardcatalog.Board{}, err
	}
	if before.Revision != mutation.ExpectedRevision {
		return boardcatalog.Board{}, adminapp.ErrConflict
	}
	capabilities, _ := json.Marshal(mutation.Board.Capabilities)
	result, err := tx.ExecContext(ctx, `UPDATE board_resources SET vendor=?,name=?,model=?,chip=?,summary=?,capabilities=CAST(? AS JSON),adaptation_status=?,image_url=?,purchase_url=?,repository_url=?,firmware_url=?,flashing_guide_url=?,effect_video_url=?,detail_slug=?,sort_order=?,updated_by=?,revision=revision+1 WHERE id=? AND revision=?`,
		mutation.Board.Vendor, mutation.Board.Name, mutation.Board.Model, mutation.Board.Chip, mutation.Board.Summary,
		string(capabilities), mutation.Board.AdaptationStatus, mutation.Board.ImageURL, mutation.Board.PurchaseURL,
		mutation.Board.RepositoryURL, mutation.Board.FirmwareURL, mutation.Board.FlashingGuideURL,
		mutation.Board.EffectVideoURL, mutation.Board.DetailSlug, mutation.Board.SortOrder, mutation.Board.UpdatedBy,
		mutation.Board.ID, mutation.ExpectedRevision)
	if err != nil {
		return boardcatalog.Board{}, boardWriteError(err)
	}
	if err := requireChanged(result); err != nil {
		return boardcatalog.Board{}, err
	}
	mutation.Audit.Before = boardJSON(before)
	board, err := boardByID(ctx, tx, mutation.Board.ID, false)
	if err != nil {
		return boardcatalog.Board{}, err
	}
	mutation.Audit.After = boardJSON(board)
	if err := insertAuditTx(ctx, tx, mutation.Audit); err != nil {
		return boardcatalog.Board{}, fmt.Errorf("audit board update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return boardcatalog.Board{}, err
	}
	return board, nil
}

func (s *BoardCommandStore) SetStatus(ctx context.Context, mutation adminapp.BoardStatusMutation) (boardcatalog.Board, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return boardcatalog.Board{}, err
	}
	defer tx.Rollback()
	before, err := boardByID(ctx, tx, mutation.ID, true)
	if err != nil {
		return boardcatalog.Board{}, err
	}
	if before.Revision != mutation.ExpectedRevision {
		return boardcatalog.Board{}, adminapp.ErrConflict
	}
	if mutation.Status == "published" && before.PublishStatus != "published" {
		var publishedIDs []string
		if err := tx.SelectContext(ctx, &publishedIDs, `SELECT id FROM board_resources WHERE publish_status='published' FOR UPDATE`); err != nil {
			return boardcatalog.Board{}, fmt.Errorf("count published boards: %w", err)
		}
		if len(publishedIDs) >= boardcatalog.PublicLimit {
			return boardcatalog.Board{}, adminapp.ErrConflict
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE board_resources SET publish_status=?,published_at=CASE WHEN ?='published' THEN NOW(6) ELSE published_at END,updated_by=?,revision=revision+1 WHERE id=? AND revision=?`, mutation.Status, mutation.Status, mutation.UpdatedBy, mutation.ID, mutation.ExpectedRevision)
	if err != nil {
		return boardcatalog.Board{}, boardWriteError(err)
	}
	if err := requireChanged(result); err != nil {
		return boardcatalog.Board{}, err
	}
	board, err := boardByID(ctx, tx, mutation.ID, false)
	if err != nil {
		return boardcatalog.Board{}, err
	}
	mutation.Audit.Before = boardJSON(before)
	mutation.Audit.After = boardJSON(board)
	if err := insertAuditTx(ctx, tx, mutation.Audit); err != nil {
		return boardcatalog.Board{}, fmt.Errorf("audit board status: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return boardcatalog.Board{}, err
	}
	return board, nil
}

func (s *BoardCommandStore) Delete(ctx context.Context, mutation adminapp.BoardDeleteMutation) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, err := boardByID(ctx, tx, mutation.ID, true)
	if err != nil {
		return err
	}
	if before.Revision != mutation.ExpectedRevision {
		return adminapp.ErrConflict
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM board_resources WHERE id=? AND revision=?`, mutation.ID, mutation.ExpectedRevision)
	if err != nil {
		return boardWriteError(err)
	}
	if err := requireChanged(result); err != nil {
		return err
	}
	mutation.Audit.Before = boardJSON(before)
	if err := insertAuditTx(ctx, tx, mutation.Audit); err != nil {
		return fmt.Errorf("audit board delete: %w", err)
	}
	return tx.Commit()
}

type queryer interface {
	GetContext(context.Context, any, string, ...any) error
}

func boardByID(ctx context.Context, query queryer, id string, forUpdate bool) (boardcatalog.Board, error) {
	queryText := `SELECT ` + boardColumns + ` FROM board_resources WHERE id=?`
	if forUpdate {
		queryText += ` FOR UPDATE`
	}
	var row boardRow
	if err := query.GetContext(ctx, &row, queryText, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return boardcatalog.Board{}, adminapp.ErrNotFound
		}
		return boardcatalog.Board{}, err
	}
	return decodeBoard(row)
}

func decodeBoards(rows []boardRow) ([]boardcatalog.Board, error) {
	boards := make([]boardcatalog.Board, 0, len(rows))
	for _, row := range rows {
		board, err := decodeBoard(row)
		if err != nil {
			return nil, err
		}
		boards = append(boards, board)
	}
	return boards, nil
}

func decodeBoard(row boardRow) (boardcatalog.Board, error) {
	board := row.Board
	if err := json.Unmarshal(row.CapabilitiesJSON, &board.Capabilities); err != nil {
		return boardcatalog.Board{}, fmt.Errorf("decode board capabilities: %w", err)
	}
	if board.Capabilities == nil {
		board.Capabilities = []string{}
	}
	return board, nil
}

func requireChanged(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return adminapp.ErrConflict
	}
	return nil
}

func boardWriteError(err error) error {
	var mysqlError *mysqldriver.MySQLError
	if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
		return adminapp.ErrConflict
	}
	return err
}

func boardJSON(board boardcatalog.Board) string {
	raw, _ := json.Marshal(board)
	return string(raw)
}
