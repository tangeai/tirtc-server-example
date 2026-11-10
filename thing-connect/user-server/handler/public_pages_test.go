package handler

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/boardcatalog"
	"thing-connect/internal/sitebrand"
)

func TestPublicPagesRenderCatalogAndMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	state, err := sitebrand.New(sitebrand.Config{Name: "自定义 <站点>", NameEN: "Custom", PublicURL: "https://public.example"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	boards := []boardcatalog.Board{{Name: "第一块开发板", DetailSlug: "first-board", Summary: "真实开发板介绍", Chip: "ESP32", Capabilities: []string{"实时音视频"}, FirmwareURL: "https://files.example/firmware", FlashingGuideURL: "https://docs.example/flash"}, {Name: "第二块"}, {Name: "第三块"}, {Name: "第四块"}}
	if err := RegisterPublicPages(router, "../static", state, boardcatalog.New(boardReaderStub{boards: boards})); err != nil {
		t.Fatal(err)
	}
	RegisterSite(router, state)
	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://attacker.example"+path, nil))
		return response
	}
	for _, path := range []string{"/", "/boards"} {
		response := request(path)
		html := response.Body.String()
		head := httptest.NewRecorder()
		router.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "https://public.example"+path, nil))
		if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != response.Header().Get("Content-Length") || head.Header().Get("Content-Type") != response.Header().Get("Content-Type") {
			t.Fatalf("HEAD %s incompatible: %d %v", path, head.Code, head.Header())
		}
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", path, response.Code, html)
		}
		for _, expected := range []string{"自定义 &lt;站点&gt;", "第一块开发板", "https://files.example/firmware", "https://docs.example/flash", `rel="canonical" href="https://public.example` + path + `"`} {
			if !strings.Contains(html, expected) {
				t.Errorf("%s missing %s", path, expected)
			}
		}
		if strings.Contains(html, "attacker.example") || strings.Contains(html, "{{.") {
			t.Fatal("unrendered or untrusted metadata")
		}
		for _, script := range regexp.MustCompile(`(?s)<script[^>]*type="application/(?:ld\+)?json"[^>]*>(.*?)</script>`).FindAllStringSubmatch(html, -1) {
			if !json.Valid([]byte(script[1])) {
				t.Errorf("invalid JSON script: %s", script[1])
			}
		}
		if path == "/" && strings.Contains(html, `class="board-capabilities"`) {
			t.Fatal("homepage cards must omit repeated capabilities")
		}
		if path == "/" && strings.Contains(html, "第四块") {
			t.Fatal("homepage exceeds three boards")
		}
		if path == "/boards" && !strings.Contains(html, "第四块") {
			t.Fatal("catalog absent from HTML")
		}
	}
	response := request("/sitemap.xml")
	var sitemap struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(response.Body.Bytes(), &sitemap); err != nil || len(sitemap.URLs) != 8 {
		t.Fatalf("invalid sitemap: %s", response.Body.String())
	}
	if !strings.Contains(request("/robots.txt").Body.String(), "Sitemap: https://public.example/sitemap.xml") {
		t.Fatal("sitemap discovery absent")
	}
	response = request("/v1/config/site")
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("site config unavailable")
	}
	if err := state.Apply([]byte(`{"name":"更新名称","name_en":"","public_url":""}`), 2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(request("/").Body.String(), "更新名称") {
		t.Fatal("published site name not reflected")
	}
	if request("/sitemap.xml").Code != 404 {
		t.Fatal("must not invent canonical origin")
	}
}

func TestPublicPageEmptyCatalogIsReadyForHydration(t *testing.T) {
	state, err := sitebrand.New(sitebrand.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	if err := RegisterPublicPages(router, "../static", state, boardcatalog.New(boardReaderStub{})); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	html := response.Body.String()
	if response.Code != 200 || !strings.Contains(html, `"boards":[]`) || !strings.Contains(html, `"status":"empty"`) || strings.Contains(html, `id="home-boards" aria-busy="true"`) {
		t.Fatal("empty catalog must render a completed empty array")
	}
}

func TestPublicContentDiscoveryAndStructuredAnswers(t *testing.T) {
	state, err := sitebrand.New(sitebrand.Config{Name: `站点 </script><script>alert(1)</script>`, PublicURL: "https://public.example"})
	if err != nil {
		t.Fatal(err)
	}
	reader := &publicCatalogStub{boards: []boardcatalog.Board{{Name: "测试开发板", DetailSlug: "test-board", Model: "Test-1", Chip: "ESP32-S3", Vendor: "测试厂商", Summary: "开发板的真实说明", ImageURL: "/v1/board-images/" + strings.Repeat("a", 64) + ".png", UpdatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), AdaptationStatus: "adapting", Capabilities: []string{"音视频"}, FirmwareURL: "https://files.example/test.bin"}}}
	router := gin.New()
	if err := RegisterPublicPages(router, "../static", state, boardcatalog.New(reader)); err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://untrusted.example"+path, nil))
		return response
	}
	var sitemap struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(get("/sitemap.xml").Body.Bytes(), &sitemap); err != nil {
		t.Fatal(err)
	}
	if len(sitemap.URLs) != 8 {
		t.Fatalf("incomplete sitemap: %+v", sitemap)
	}
	for _, entry := range sitemap.URLs {
		path := strings.TrimPrefix(entry.Loc, "https://public.example")
		response := get(path)
		html := response.Body.String()
		if response.Code != http.StatusOK || !strings.Contains(html, `rel="canonical" href="`+entry.Loc+`"`) {
			t.Fatalf("sitemap entry %s is not a canonical public page: %d", entry.Loc, response.Code)
		}
		if strings.Contains(html, "untrusted.example") || strings.Contains(html, "<script>alert(1)</script>") {
			t.Fatal("unsafe page metadata")
		}
		if strings.Count(html, "<h1") != 1 {
			t.Errorf("%s needs one main heading", path)
		}
		matches := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`).FindStringSubmatch(html)
		if len(matches) != 2 || !json.Valid([]byte(matches[1])) {
			t.Fatalf("invalid structured data at %s", path)
		}
		var graph struct {
			Nodes []map[string]any `json:"@graph"`
		}
		if err := json.Unmarshal([]byte(matches[1]), &graph); err != nil {
			t.Fatal(err)
		}
		if path == "/faq" {
			found := false
			for _, node := range graph.Nodes {
				if node["@type"] != "FAQPage" {
					continue
				}
				found = true
				questions := node["mainEntity"].([]any)
				if len(questions) != len(faqSections) {
					t.Fatal("FAQ count mismatch")
				}
				for _, value := range questions {
					question := value.(map[string]any)
					answer := question["acceptedAnswer"].(map[string]any)["text"].(string)
					if !strings.Contains(html, question["name"].(string)) || !strings.Contains(html, "<p>"+answer+"</p>") {
						t.Fatal("structured answer absent from visible content")
					}
				}
			}
			if !found {
				t.Fatal("FAQ structured data missing")
			}
		}
		if strings.HasPrefix(path, "/guides/") {
			for _, expected := range []string{`id="prerequisites"`, `id="troubleshooting"`, `id="sources"`, "如何确认这一步完成", `"@type":"TechArticle"`, `href="/getting-started"`} {
				if !strings.Contains(html, expected) {
					t.Errorf("tutorial %s missing %s", path, expected)
				}
			}
		}
		if path == "/boards/test-board" {
			for _, expected := range []string{"Test-1", "ESP32-S3", "适配中", "开发板的真实说明", `datetime="2026-10-01T12:00:00Z"`, `content="https://public.example/v1/board-images/` + strings.Repeat("a", 64) + `.png"`, `href="https://files.example/test.bin"`} {
				if !strings.Contains(html, expected) {
					t.Errorf("detail missing %s", expected)
				}
			}
		}
		head := httptest.NewRecorder()
		router.ServeHTTP(head, httptest.NewRequest(http.MethodHead, path, nil))
		if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != response.Header().Get("Content-Length") {
			t.Errorf("HEAD mismatch at %s", path)
		}
	}
	for _, path := range []string{"/", "/boards"} {
		if !strings.Contains(get(path).Body.String(), `href="/boards/test-board"`) {
			t.Errorf("%s lacks crawlable detail link", path)
		}
	}
	for _, path := range []string{"/boards/missing", "/boards/another-missing-board"} {
		if response := get(path); response.Code != 404 || response.Header().Get("X-Robots-Tag") != "noindex" {
			t.Errorf("missing board indexed: %s", path)
		}
	}
	if response := get("/guides/not-a-guide"); response.Code != 404 {
		t.Fatal("unknown tutorial must not become a soft 404")
	}
	if !strings.Contains(get("/sitemap.xml").Body.String(), "<lastmod>2026-10-01T12:00:00Z</lastmod>") {
		t.Fatal("board update missing from sitemap")
	}
	reader.boards = nil // Unpublishing removes both the detail page and sitemap entry.
	if get("/boards/test-board").Code != 404 || strings.Contains(get("/sitemap.xml").Body.String(), "test-board") {
		t.Fatal("unpublished board remains discoverable")
	}
	reader.err = errors.New("private database failure")
	for _, path := range []string{"/boards/test-board", "/sitemap.xml"} {
		response := get(path)
		if response.Code != 503 || response.Header().Get("Retry-After") == "" || strings.Contains(response.Body.String(), "database") {
			t.Errorf("incorrect transient failure at %s: %d", path, response.Code)
		}
	}
	for _, path := range []string{"/getting-started", "/faq", "/guides/simulator", "/guides/wechat-voip", "/guides/linux-device"} {
		if get(path).Code != 200 {
			t.Errorf("static content depends on catalog: %s", path)
		}
	}
	if err := state.Apply([]byte(`{"name":"新名称","name_en":"","public_url":""}`), 2); err != nil {
		t.Fatal(err)
	}
	html := get("/faq").Body.String()
	if strings.Contains(html, `rel="canonical"`) || strings.Contains(html, "https://untrusted.example") || !strings.Contains(html, "新名称") {
		t.Fatal("invalid metadata after dynamic config update")
	}
}

type publicCatalogStub struct {
	boards []boardcatalog.Board
	err    error
}

func (s *publicCatalogStub) ListPublished(context.Context, int) ([]boardcatalog.Board, error) {
	return s.boards, s.err
}
func (s *publicCatalogStub) PublishedBySlug(_ context.Context, slug string) (boardcatalog.Board, error) {
	if s.err != nil {
		return boardcatalog.Board{}, s.err
	}
	for _, board := range s.boards {
		if board.DetailSlug == slug {
			return board, nil
		}
	}
	return boardcatalog.Board{}, boardcatalog.ErrNotFound
}
