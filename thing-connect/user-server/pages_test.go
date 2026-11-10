package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestUserPagesServeOneApplicationWithoutAPIFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "app"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "index.html"), []byte("<html>user-app</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	if err := registerUserPages(r, dir); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/login", "/register", "/forgot-password", "/devices", "/bind", "/player", "/contacts", "/agent", "/room"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path+"?device_id=old-device", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "user-app") || w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for old, target := range map[string]string{"/static/public-guide.html": "/getting-started", "/static/auth.html": "/login", "/static/agent.html": "/agent", "/static/room.html": "/room", "/static/player.html": "/player"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", old+"?device_id=A%2FB", nil))
		if w.Code != 302 || w.Header().Get("Location") != target+"?device_id=A%2FB" {
			t.Fatalf("legacy page %s: %d %s", old, w.Code, w.Header().Get("Location"))
		}
	}
	for _, path := range []string{"/v1/missing", "/v1/internal/missing", "/missing"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("%s must not resolve to a page: %d", path, w.Code)
		}
	}
}
func TestUserPagesRequireBuildArtifact(t *testing.T) {
	if err := registerUserPages(gin.New(), t.TempDir()); err == nil {
		t.Fatal("missing frontend build must be detected")
	}
}
