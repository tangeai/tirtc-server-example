package handler

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

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
	boards := []boardcatalog.Board{{Name: "第一块开发板", Chip: "ESP32", Capabilities: []string{"实时音视频"}, FirmwareURL: "https://files.example/firmware", FlashingGuideURL: "https://docs.example/flash"}, {Name: "第二块"}, {Name: "第三块"}, {Name: "第四块"}}
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
	if err := xml.Unmarshal(response.Body.Bytes(), &sitemap); err != nil || len(sitemap.URLs) != 2 {
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
