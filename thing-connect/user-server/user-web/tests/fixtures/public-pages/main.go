// Render the production public-page handlers with synthetic catalog data for browser tests.
package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/boardcatalog"
	"thing-connect/internal/sitebrand"
	"thing-connect/user-server/handler"
)

type catalog struct{}

func (catalog) ListPublished(context.Context, int) ([]boardcatalog.Board, error) {
	return []boardcatalog.Board{board()}, nil
}
func (catalog) PublishedBySlug(context.Context, string) (boardcatalog.Board, error) {
	return board(), nil
}
func board() boardcatalog.Board {
	return boardcatalog.Board{Name: "Fixture board", Model: "Demo", Chip: "ESP32", Vendor: "Fixture", Summary: "Published catalog content", DetailSlug: "demo", AdaptationStatus: "ready", Capabilities: []string{"Audio"}, UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}
func main() {
	gin.SetMode(gin.ReleaseMode)
	site, err := sitebrand.New(sitebrand.Config{Name: "测试站", NameEN: "Test site"})
	if err != nil {
		panic(err)
	}
	router := gin.New()
	if err := handler.RegisterPublicPages(router, "user-server/static", site, boardcatalog.New(catalog{})); err != nil {
		panic(err)
	}
	pages := map[string]string{}
	for _, route := range []string{"/", "/boards", "/getting-started", "/faq", "/guides/simulator", "/guides/wechat-voip", "/guides/linux-device", "/boards/demo"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", route, nil))
		if response.Code != 200 {
			panic(route)
		}
		pages[route] = response.Body.String()
	}
	if err := json.NewEncoder(os.Stdout).Encode(pages); err != nil {
		panic(err)
	}
}
