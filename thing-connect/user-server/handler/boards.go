package handler

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/boardcatalog"
)

var boardImageNamePattern = regexp.MustCompile(`^[a-f0-9]{64}\.(?:jpg|png|webp)$`)

func RegisterBoards(r *gin.Engine, service *boardcatalog.Service) {
	r.GET("/v1/boards", func(c *gin.Context) {
		boards, err := service.List(c.Request.Context())
		if err != nil {
			apiresp.Internal(c, "读取开发板目录失败")
			return
		}
		c.Header("Cache-Control", "no-store")
		apiresp.OK(c, gin.H{"boards": boards})
	})
	r.GET("/v1/boards/:slug", func(c *gin.Context) {
		board, err := service.BySlug(c.Request.Context(), c.Param("slug"))
		if errors.Is(err, boardcatalog.ErrNotFound) {
			c.JSON(http.StatusNotFound, apiresp.JSON{Code: 40400, Msg: "开发板不存在或尚未上架"})
			return
		}
		if err != nil {
			apiresp.Internal(c, "读取开发板详情失败")
			return
		}
		c.Header("Cache-Control", "no-store")
		apiresp.OK(c, board)
	})
}

func RegisterBoardImages(r *gin.Engine, dir string) {
	r.GET("/v1/board-images/:name", func(c *gin.Context) {
		name := c.Param("name")
		if !boardImageNamePattern.MatchString(name) {
			c.Status(http.StatusNotFound)
			return
		}
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Header("X-Content-Type-Options", "nosniff")
		c.File(path)
	})
}
