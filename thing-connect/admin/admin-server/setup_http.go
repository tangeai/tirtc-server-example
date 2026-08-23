package main

import (
	"errors"
	"log/slog"
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
		abortSetupProblem(c, http.StatusGone, 410, installer.ErrAlreadyInstalled)
		return
	}
	if !sameOrigin(c.Request) {
		abortSetupProblem(c, http.StatusForbidden, 403, installer.ErrInvalidOrigin)
		return
	}
	if !h.allowAttempt(c.ClientIP()) {
		abortSetupProblem(c, http.StatusTooManyRequests, 429, installer.ErrTooManyAttempts)
		return
	}
	if err := h.bootstrap.Authenticate(c.GetHeader("X-Setup-Token")); err != nil {
		abortSetupProblem(c, http.StatusUnauthorized, 401, installer.ErrUnauthorized)
		return
	}
	c.Next()
}

func abortSetupProblem(c *gin.Context, status, code int, err error) {
	problem := installer.Explain(err)
	c.AbortWithStatusJSON(status, apiresp.JSON{Code: code, Msg: problem.Message, Data: problem})
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
	slog.WarnContext(c.Request.Context(), "setup request failed", "path", c.FullPath(), "err", err)
	problem := installer.Explain(err)
	status, code := http.StatusInternalServerError, 500
	switch {
	case errors.Is(err, installer.ErrInvalidInput):
		status, code = http.StatusBadRequest, 400
	case errors.Is(err, installer.ErrUnauthorized):
		status, code = http.StatusUnauthorized, 401
	case errors.Is(err, installer.ErrInstallBusy):
		status, code = http.StatusConflict, 409
	case errors.Is(err, installer.ErrPlanStale):
		status, code = http.StatusConflict, 409
	case errors.Is(err, installer.ErrAlreadyInstalled):
		status, code = http.StatusGone, 410
	case errors.Is(err, installer.ErrUnknownDatabase):
		status, code = http.StatusConflict, 409
	case errors.Is(err, installer.ErrSchemaFuture):
		status, code = http.StatusConflict, 409
	case errors.Is(err, installer.ErrSchemaDrift):
		status, code = http.StatusConflict, 409
	case errors.Is(err, installer.ErrRedisUnavailable):
		status, code = http.StatusServiceUnavailable, 503
	case errors.Is(err, installer.ErrMQTTUnavailable):
		status, code = http.StatusServiceUnavailable, 503
	case errors.Is(err, installer.ErrMySQLUnavailable):
		status, code = http.StatusServiceUnavailable, 503
	case errors.Is(err, installer.ErrMySQLRuntimeAccount):
		status, code = http.StatusServiceUnavailable, 503
	}
	c.JSON(status, apiresp.JSON{Code: code, Msg: problem.Message, Data: problem})
}
