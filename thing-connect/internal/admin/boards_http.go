package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/boardcatalog"
	"thing-connect/internal/logging"
)

const boardImageRequestBytes int64 = MaxBoardImageBytes + (1 << 20)

type boardWriteRequest struct {
	Board            boardcatalog.Board `json:"board" binding:"required"`
	ExpectedRevision int64              `json:"expected_revision"`
	Reason           string             `json:"reason" binding:"required"`
}

type boardStatusRequest struct {
	Status           string `json:"status" binding:"required"`
	ExpectedRevision int64  `json:"expected_revision" binding:"required"`
	Reason           string `json:"reason" binding:"required"`
}

type boardDeleteRequest struct {
	ExpectedRevision int64  `json:"expected_revision" binding:"required"`
	Reason           string `json:"reason" binding:"required"`
}

func (s *HTTPServer) listBoards(c *gin.Context) {
	boards, err := s.boards.List(c.Request.Context())
	if err != nil {
		apiresp.Internal(c, "读取开发板目录失败")
		return
	}
	apiresp.OK(c, gin.H{"items": boards})
}

func (s *HTTPServer) createBoard(c *gin.Context) {
	var request boardWriteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	board, err := s.boards.Create(c.Request.Context(), request.Board, boardMeta(c, request.Reason))
	writeBoardResult(c, board, err)
}

func (s *HTTPServer) uploadBoardImage(c *gin.Context) {
	if s.boardImages == nil {
		apiresp.Internal(c, "开发板图片服务未初始化")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, boardImageRequestBytes)
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		apiresp.BadParam(c, "请选择不超过 5 MB 的 JPG、PNG 或 WebP 图片")
		return
	}
	defer file.Close()
	result, err := s.boardImages.Save(file)
	if errors.Is(err, ErrInvalidBoardImage) {
		apiresp.BadParam(c, "图片须为 JPG、PNG 或 WebP 格式，且不超过 5 MB")
		return
	}
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "save board image", "err", err)
		apiresp.Internal(c, "保存开发板图片失败")
		return
	}
	apiresp.OK(c, result)
}

func (s *HTTPServer) updateBoard(c *gin.Context) {
	var request boardWriteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	request.Board.ID = c.Param("id")
	board, err := s.boards.Update(c.Request.Context(), request.Board, request.ExpectedRevision, boardMeta(c, request.Reason))
	writeBoardResult(c, board, err)
}

func (s *HTTPServer) updateBoardStatus(c *gin.Context) {
	var request boardStatusRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	board, err := s.boards.SetStatus(c.Request.Context(), c.Param("id"), request.Status, request.ExpectedRevision, boardMeta(c, request.Reason))
	writeBoardResult(c, board, err)
}

func (s *HTTPServer) deleteBoard(c *gin.Context) {
	var request boardDeleteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	err := s.boards.Delete(c.Request.Context(), c.Param("id"), request.ExpectedRevision, boardMeta(c, request.Reason))
	if err != nil {
		writeBoardResult(c, boardcatalog.Board{}, err)
		return
	}
	apiresp.OK(c, nil)
}

func boardMeta(c *gin.Context, reason string) BoardRequestMeta {
	identity, _ := identityFromContext(c)
	return BoardRequestMeta{Actor: identity, RequestID: logging.RequestIDFrom(c.Request.Context()), Method: c.Request.Method, Path: c.Request.URL.Path, ClientIP: c.ClientIP(), UserAgent: c.Request.UserAgent(), Reason: strings.TrimSpace(reason)}
}

func writeBoardResult(c *gin.Context, board boardcatalog.Board, err error) {
	switch {
	case errors.Is(err, ErrInvalidBoardCommand):
		apiresp.BadParam(c, "开发板操作参数不正确")
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, apiresp.JSON{Code: 40400, Msg: "开发板不存在"})
	case errors.Is(err, ErrConflict):
		c.JSON(http.StatusConflict, apiresp.JSON{Code: 40901, Msg: "开发板型号或详情地址重复、记录已被修改，或者已达到 100 条上架上限"})
	case err != nil:
		apiresp.Internal(c, "保存开发板失败")
	default:
		apiresp.OK(c, board)
	}
}
