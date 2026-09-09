package handler

import (
	"github.com/gin-gonic/gin"
	"thing-connect/internal/webnav"
)

func RegisterNavigation(r *gin.Engine, state *webnav.State) {
	r.GET("/v1/config/navigation", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(200, gin.H{"code": 200, "msg": "ok", "data": state.Current()})
	})
}
