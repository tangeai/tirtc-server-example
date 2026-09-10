package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"thing-connect/internal/boardcatalog"
)

var ErrInvalidBoardCommand = errors.New("admin: invalid board command")

type BoardMutation struct {
	Board            boardcatalog.Board
	ExpectedRevision int64
	Audit            AuditEvent
}

type BoardStatusMutation struct {
	ID               string
	Status           string
	ExpectedRevision int64
	UpdatedBy        int64
	Audit            AuditEvent
}

type BoardDeleteMutation struct {
	ID               string
	ExpectedRevision int64
	Audit            AuditEvent
}

type BoardCommandStore interface {
	List(ctx context.Context) ([]boardcatalog.Board, error)
	Create(ctx context.Context, mutation BoardMutation) (boardcatalog.Board, error)
	Update(ctx context.Context, mutation BoardMutation) (boardcatalog.Board, error)
	SetStatus(ctx context.Context, mutation BoardStatusMutation) (boardcatalog.Board, error)
	Delete(ctx context.Context, mutation BoardDeleteMutation) error
}

type BoardRequestMeta struct {
	Actor     AccessIdentity
	RequestID string
	Method    string
	Path      string
	ClientIP  string
	UserAgent string
	Reason    string
}

type BoardService struct{ store BoardCommandStore }

func NewBoardService(store BoardCommandStore) *BoardService { return &BoardService{store: store} }

func (s *BoardService) List(ctx context.Context) ([]boardcatalog.Board, error) {
	return s.store.List(ctx)
}

func (s *BoardService) Create(ctx context.Context, board boardcatalog.Board, meta BoardRequestMeta) (boardcatalog.Board, error) {
	board = boardcatalog.Normalize(board)
	board.ID = uuid.NewString()
	board.PublishStatus = "draft"
	board.Revision = 1
	board.CreatedBy, board.UpdatedBy = meta.Actor.UserID, meta.Actor.UserID
	if err := validateBoardCommand(board, meta); err != nil {
		return boardcatalog.Board{}, err
	}
	audit := boardAudit(meta, "board.create", board.ID, "", marshalSafe(board))
	return s.store.Create(ctx, BoardMutation{Board: board, Audit: audit})
}

func (s *BoardService) Update(ctx context.Context, board boardcatalog.Board, expectedRevision int64, meta BoardRequestMeta) (boardcatalog.Board, error) {
	board = boardcatalog.Normalize(board)
	board.UpdatedBy = meta.Actor.UserID
	if expectedRevision <= 0 || strings.TrimSpace(board.ID) == "" {
		return boardcatalog.Board{}, ErrInvalidBoardCommand
	}
	if err := validateBoardCommand(board, meta); err != nil {
		return boardcatalog.Board{}, err
	}
	audit := boardAudit(meta, "board.update", board.ID, fmt.Sprintf(`{"revision":%d}`, expectedRevision), marshalSafe(board))
	return s.store.Update(ctx, BoardMutation{Board: board, ExpectedRevision: expectedRevision, Audit: audit})
}

func (s *BoardService) SetStatus(ctx context.Context, id, status string, expectedRevision int64, meta BoardRequestMeta) (boardcatalog.Board, error) {
	id, status = strings.TrimSpace(id), strings.TrimSpace(status)
	if id == "" || expectedRevision <= 0 || (status != "published" && status != "offline") || !validBoardMeta(meta) {
		return boardcatalog.Board{}, ErrInvalidBoardCommand
	}
	audit := boardAudit(meta, "board.status", id, fmt.Sprintf(`{"revision":%d}`, expectedRevision), fmt.Sprintf(`{"publish_status":%q}`, status))
	return s.store.SetStatus(ctx, BoardStatusMutation{ID: id, Status: status, ExpectedRevision: expectedRevision, UpdatedBy: meta.Actor.UserID, Audit: audit})
}

func (s *BoardService) Delete(ctx context.Context, id string, expectedRevision int64, meta BoardRequestMeta) error {
	id = strings.TrimSpace(id)
	if id == "" || expectedRevision <= 0 || !validBoardMeta(meta) {
		return ErrInvalidBoardCommand
	}
	return s.store.Delete(ctx, BoardDeleteMutation{ID: id, ExpectedRevision: expectedRevision, Audit: boardAudit(meta, "board.delete", id, fmt.Sprintf(`{"revision":%d}`, expectedRevision), `{"deleted":true}`)})
}

func validateBoardCommand(board boardcatalog.Board, meta BoardRequestMeta) error {
	if !validBoardMeta(meta) {
		return ErrInvalidBoardCommand
	}
	if err := boardcatalog.Validate(board); err != nil {
		return errors.Join(ErrInvalidBoardCommand, err)
	}
	return nil
}

func validBoardMeta(meta BoardRequestMeta) bool {
	return meta.Actor.UserID > 0 && strings.TrimSpace(meta.Reason) != ""
}

func boardAudit(meta BoardRequestMeta, action, id, before, after string) AuditEvent {
	return AuditEvent{AdminUserID: meta.Actor.UserID, RoleCodes: strings.Join(meta.Actor.Roles, ","), RequestID: meta.RequestID, Method: meta.Method, Path: meta.Path, HTTPStatus: 200, Action: action, Resource: "board_resource", ResourceID: id, Reason: strings.TrimSpace(meta.Reason), Before: before, After: after, ClientIP: meta.ClientIP, UserAgent: meta.UserAgent, Success: true}
}
