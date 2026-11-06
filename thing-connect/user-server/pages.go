package main

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

// registerUserPages serves one application entry for user-facing workflows.
// Business APIs retain their own routers; this is deliberately not an SPA
// catch-all, so unknown API paths continue to return 404.
func registerUserPages(r *gin.Engine, staticDir string) error {
	entry := filepath.Join(staticDir, "app", "index.html")
	if _, err := os.Stat(entry); err != nil {
		return err
	}
	legacy := map[string]string{
		"/static/auth.html": "/login", "/static/devices.html": "/devices",
		"/static/bind.html": "/bind", "/static/player.html": "/player",
		"/static/contacts.html": "/contacts", "/static/agent.html": "/agent",
		"/static/room.html": "/room",
	}
	r.Use(func(c *gin.Context) {
		if target, ok := legacy[c.Request.URL.Path]; ok && (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) {
			if c.Request.URL.RawQuery != "" {
				target += "?" + c.Request.URL.RawQuery
			}
			c.Header("Cache-Control", "no-store")
			c.Redirect(http.StatusFound, target)
			c.Abort()
			return
		}
		c.Next()
	})
	r.Static("/static", staticDir)
	r.StaticFile("/", filepath.Join(staticDir, "index.html"))
	r.StaticFile("/boards", filepath.Join(staticDir, "boards.html"))
	for _, path := range []string{"/login", "/register", "/forgot-password", "/devices", "/bind", "/player", "/contacts", "/agent", "/room"} {
		r.GET(path, func(c *gin.Context) {
			c.Header("Cache-Control", "no-cache")
			c.File(entry)
		})
	}
	// TiRTC loads these assets from fixed root paths.
	r.StaticFile("/librender.wasm", filepath.Join(staticDir, "js", "librender.wasm"))
	r.StaticFile("/plugin.wasm", filepath.Join(staticDir, "js", "plugin.wasm"))
	return nil
}
