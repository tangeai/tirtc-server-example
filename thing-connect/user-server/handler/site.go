package handler

import (
	"github.com/gin-gonic/gin"
	"thing-connect/internal/sitebrand"
)

func RegisterSite(r *gin.Engine, state *sitebrand.State) {
	r.GET("/v1/config/site", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(200, gin.H{"code": 200, "msg": "ok", "data": state.Current()})
	})
}
