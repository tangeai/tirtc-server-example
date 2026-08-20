package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	gocache "github.com/patrickmn/go-cache"

	aichatapi "thing-connect/ai-server/tirtcapi"
	"thing-connect/internal/apiresp"
	"thing-connect/internal/store"
)

// fetchFn is the function signature for fetching AI-chat credentials.
type fetchFn func(ctx context.Context, deviceID, roleID string) (peerID, token string, upstreamCode int32, upstreamMsg string, err error)

// Server holds handler dependencies.
type Server struct {
	mu            sync.RWMutex
	jwtSecret     string
	defaultRoleID string
	roleStore     store.RoleBindingStore
	cache         *gocache.Cache
	fetch         fetchFn
}

func (s *Server) UpdateRuntime(defaultRoleID, baseURL, accessKeyID, appID, secretKeyID string) {
	client := &http.Client{Timeout: 10 * time.Second}
	s.mu.Lock()
	s.defaultRoleID = defaultRoleID
	s.fetch = func(ctx context.Context, deviceID, roleID string) (string, string, int32, string, error) {
		return aichatapi.PostTokenAichat(ctx, client, baseURL, accessKeyID, appID, secretKeyID, deviceID, roleID)
	}
	s.cache.Flush()
	s.mu.Unlock()
}

func (s *Server) runtime() (string, fetchFn) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaultRoleID, s.fetch
}

// NewServer creates a production Server wired to the real upstream.
func NewServer(jwtSecret, defaultRoleID, baseURL, accessKeyID, appID, secretKeyID string, roleStore store.RoleBindingStore) *Server {
	client := &http.Client{Timeout: 10 * time.Second}
	return &Server{
		jwtSecret:     jwtSecret,
		defaultRoleID: defaultRoleID,
		roleStore:     roleStore,
		cache:         gocache.New(60*time.Second, 120*time.Second),
		fetch: func(ctx context.Context, deviceID, roleID string) (string, string, int32, string, error) {
			return aichatapi.PostTokenAichat(ctx, client, baseURL, accessKeyID, appID, secretKeyID, deviceID, roleID)
		},
	}
}

// Register mounts routes on r.
func (s *Server) Register(r *gin.Engine) {
	v1 := r.Group("/v1")
	v1.GET("/ai/token", s.getAIToken)
}

func (s *Server) getAIToken(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		apiresp.Unauthorized(c)
		return
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(s.jwtSecret), nil
	})
	if err != nil || !tok.Valid {
		apiresp.Unauthorized(c)
		return
	}

	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		apiresp.Unauthorized(c)
		return
	}
	deviceID, _ := claims["device_id"].(string)
	if deviceID == "" {
		apiresp.Unauthorized(c)
		return
	}

	roleID, fetch := s.runtime()
	if s.roleStore != nil {
		if bound, err := s.roleStore.GetDeviceRole(c.Request.Context(), deviceID); err == nil && bound != "" {
			roleID = bound
		}
	}
	cacheKey := deviceID + ":" + roleID

	if cached, found := s.cache.Get(cacheKey); found {
		c.JSON(http.StatusOK, cached)
		return
	}

	// No singleflight: concurrent cache misses for the same key each call upstream.
	// Acceptable for current scale; add golang.org/x/sync/singleflight if needed.
	peerID, token, upstreamCode, upstreamMsg, err := fetch(c.Request.Context(), deviceID, roleID)
	if err != nil {
		slog.WarnContext(c.Request.Context(), "fetch AI chat token failed", "device_id", deviceID, "err", err)
		c.JSON(http.StatusOK, apiresp.JSON{Code: -1, Msg: "获取 AI 会话凭证失败：AI 云服务请求异常，请稍后重试"})
		return
	}
	if upstreamCode != 0 {
		slog.WarnContext(c.Request.Context(), "AI chat token upstream rejected", "device_id", deviceID, "upstream_code", upstreamCode, "upstream_msg", upstreamMsg)
		c.JSON(http.StatusOK, apiresp.JSON{Code: int(upstreamCode), Msg: "获取 AI 会话凭证失败：AI 云服务返回错误"})
		return
	}

	resp := apiresp.JSON{
		Code: apiresp.CodeOK,
		Msg:  "ok",
		Data: gin.H{
			"peer_id": peerID,
			"token":   token,
			"role_id": roleID,
		},
	}
	s.cache.Set(cacheKey, resp, gocache.DefaultExpiration)
	c.JSON(http.StatusOK, resp)
}
