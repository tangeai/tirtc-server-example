package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/boardcatalog"
)

type boardReaderStub struct{ boards []boardcatalog.Board }

func (stub boardReaderStub) ListPublished(context.Context, int) ([]boardcatalog.Board, error) {
	return stub.boards, nil
}
func (stub boardReaderStub) PublishedBySlug(_ context.Context, slug string) (boardcatalog.Board, error) {
	for _, board := range stub.boards {
		if board.DetailSlug == slug {
			return board, nil
		}
	}
	return boardcatalog.Board{}, boardcatalog.ErrNotFound
}

func TestBoardRoutesExposePublishedCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterBoards(router, boardcatalog.New(boardReaderStub{boards: []boardcatalog.Board{{ID: "one", Name: "一号板", DetailSlug: "one", Capabilities: []string{}}}}))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/boards", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list response = %d %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/boards/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("detail response = %d %s", response.Code, response.Body.String())
	}
}

func TestBoardImageRouteOnlyServesManagedNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	name := strings.Repeat("a", 64) + ".png"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterBoardImages(router, dir)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/board-images/"+name, nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("image response = %d headers=%v", response.Code, response.Header())
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/board-images/not-managed.png", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("invalid image response = %d", response.Code)
	}
}
