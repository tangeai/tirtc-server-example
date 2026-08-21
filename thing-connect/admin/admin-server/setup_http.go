package main

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/installer"
)

type setupHTTP struct {
	bootstrap *installer.Bootstrap
	mu        sync.Mutex
	attempts  map[string]setupAttempts
}

type setupAttempts struct {
	started time.Time
	count   int
}

func newSetupHTTP(bootstrap *installer.Bootstrap) *setupHTTP {
	return &setupHTTP{bootstrap: bootstrap, attempts: map[string]setupAttempts{}}
}

func (h *setupHTTP) Register(router *gin.Engine) {
	group := router.Group("/v1/setup")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	group.GET("/status", h.status)
	group.POST("/preview", h.authorize, h.preview)
	group.POST("/execute", h.authorize, h.execute)
}

func (h *setupHTTP) status(c *gin.Context) {
	state, err := h.bootstrap.Status(c.Request.Context())
	if err != nil {
		setupError(c, err)
		return
	}
	apiresp.OK(c, state)
}

func (h *setupHTTP) preview(c *gin.Context) {
	var draft installer.Draft
	if err := c.ShouldBindJSON(&draft); err != nil {
		setupError(c, installer.ErrInvalidInput)
		return
	}
	plan, err := h.bootstrap.Preview(c.Request.Context(), draft)
	if err != nil {
		setupError(c, err)
		return
	}
	apiresp.OK(c, plan)
}

func (h *setupHTTP) execute(c *gin.Context) {
	var request installer.ExecuteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		setupError(c, installer.ErrInvalidInput)
		return
	}
	state, err := h.bootstrap.Execute(c.Request.Context(), request)
	if err != nil {
		setupError(c, err)
		return
	}
	apiresp.OK(c, state)
}

func (h *setupHTTP) authorize(c *gin.Context) {
	if state, err := h.bootstrap.Status(c.Request.Context()); err == nil && (state.Mode == installer.ModeInstalled || state.Mode == installer.ModeNormal) {
		c.AbortWithStatusJSON(http.StatusGone, apiresp.JSON{Code: 410, Msg: "首次安装入口已永久关闭"})
		return
	}
	if !sameOrigin(c.Request) {
		c.AbortWithStatusJSON(http.StatusForbidden, apiresp.JSON{Code: 403, Msg: "安装请求来源无效"})
		return
	}
	if !h.allowAttempt(c.ClientIP()) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, apiresp.JSON{Code: 429, Msg: "安装验证请求过于频繁，请稍后重试"})
		return
	}
	if err := h.bootstrap.Authenticate(c.GetHeader("X-Setup-Token")); err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, apiresp.JSON{Code: 401, Msg: "安装令牌无效"})
		return
	}
	c.Next()
}

func (h *setupHTTP) allowAttempt(client string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if len(h.attempts) > 1024 {
		for address, attempt := range h.attempts {
			if now.Sub(attempt.started) >= time.Minute {
				delete(h.attempts, address)
			}
		}
	}
	entry, known := h.attempts[client]
	if !known && len(h.attempts) >= 4096 {
		return false
	}
	if entry.started.IsZero() || now.Sub(entry.started) >= time.Minute {
		entry = setupAttempts{started: now}
	}
	entry.count++
	h.attempts[client] = entry
	return entry.count <= 30
}

func sameOrigin(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.User == nil && strings.EqualFold(parsed.Host, request.Host)
}

func setupError(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, 500, "安装服务暂时不可用"
	switch {
	case errors.Is(err, installer.ErrInvalidInput):
		status, code, message = http.StatusBadRequest, 400, err.Error()
	case errors.Is(err, installer.ErrUnauthorized):
		status, code, message = http.StatusUnauthorized, 401, "安装令牌无效"
	case errors.Is(err, installer.ErrInstallBusy):
		status, code, message = http.StatusConflict, 409, "另一个安装或发布任务正在运行"
	case errors.Is(err, installer.ErrPlanStale):
		status, code, message = http.StatusConflict, 409, "数据库状态或安装计划已经变化，请重新预检"
	case errors.Is(err, installer.ErrAlreadyInstalled):
		status, code, message = http.StatusGone, 410, "首次安装入口已永久关闭"
	case errors.Is(err, installer.ErrUnknownDatabase):
		status, code, message = http.StatusConflict, 409, "目标数据库非空且无法确认属于 ThingConnect，未执行任何写入"
	case errors.Is(err, installer.ErrSchemaFuture):
		status, code, message = http.StatusConflict, 409, "数据库版本高于当前程序，请使用相同或更高版本"
	case errors.Is(err, installer.ErrSchemaDrift):
		status, code, message = http.StatusConflict, 409, "数据库结构与迁移记录不一致，已停止自动处理"
	}
	c.JSON(status, apiresp.JSON{Code: code, Msg: message})
}
