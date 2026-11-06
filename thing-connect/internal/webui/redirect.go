package webui

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RedirectPage keeps published browser entry points working while all user
// pages are served by user-server. The fixed destination stays on the public
// origin; query parameters are carried verbatim and fragments stay client-side.
func RedirectPage(r *gin.Engine, oldPath, page string) {
	r.GET(oldPath, func(c *gin.Context) {
		destination := page
		if c.Request.URL.RawQuery != "" {
			destination += "?" + c.Request.URL.RawQuery
		}
		c.Header("Cache-Control", "no-store")
		c.Redirect(http.StatusFound, destination)
	})
}
