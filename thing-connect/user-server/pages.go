package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

// Page groups select independently so a player rollback preserves console sessions.
func userPageEntry(staticDir, setting, directory string) (string, error) {
	switch os.Getenv(setting) {
	case "", "legacy":
		directory = "app"
	case "new":
	default:
		return "", fmt.Errorf("%s must be legacy or new", setting)
	}
	entry := filepath.Join(staticDir, directory, "index.html")
	info, err := os.Stat(entry)
	if err != nil {
		return "", fmt.Errorf("%s: %w", setting, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s: expected regular file at %s", setting, entry)
	}
	return entry, nil
}

func registerUserPages(r *gin.Engine, staticDir string) error {
	console, err := userPageEntry(staticDir, "USER_CONSOLE_ENTRY", "console")
	if err != nil {
		return err
	}
	player, err := userPageEntry(staticDir, "USER_PLAYER_ENTRY", "player")
	if err != nil {
		return err
	}
	legacy := map[string]string{
		"/static/public-guide.html": "/getting-started",
		"/static/index.html":        "/", "/static/boards.html": "/boards",
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
	serve := func(entry string) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("Cache-Control", "no-cache")
			c.Header("X-Robots-Tag", "noindex, nofollow")
			c.File(entry)
		}
	}
	r.GET("/player", serve(player))
	for _, path := range []string{"/login", "/register", "/forgot-password", "/devices", "/bind", "/contacts", "/agent", "/room"} {
		r.GET(path, serve(console))
	}
	if os.Getenv("USER_CONSOLE_ENTRY") == "new" {
		for _, path := range []string{"/account", "/legal", "/history", "/recognition", "/notifications"} {
			r.GET(path, serve(console))
		}
	}
	// TiRTC loads these assets from fixed root paths.
	r.StaticFile("/librender.wasm", filepath.Join(staticDir, "js", "librender.wasm"))
	r.StaticFile("/plugin.wasm", filepath.Join(staticDir, "js", "plugin.wasm"))
	return nil
}
