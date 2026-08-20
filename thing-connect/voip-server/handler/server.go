package handler

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"thing-connect/internal/config"
)

type MQTTPublisher interface {
	Publish(topic string, qos byte, payload any) error
}

type mqttOnlineChecker interface {
	IsOnline(ctx context.Context, clientID string) bool
}

type Server struct {
	mu     sync.RWMutex
	cfg    *config.Config
	db     *sqlx.DB
	rdb    *redis.Client
	broker MQTTPublisher
}

func (s *Server) Config() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copy := *s.cfg
	return &copy
}
func (s *Server) UpdateConfig(cfg *config.Config) { s.mu.Lock(); s.cfg = cfg; s.mu.Unlock() }

func NewServer(cfg *config.Config, db *sqlx.DB, rdb *redis.Client, broker MQTTPublisher) *Server {
	return &Server{cfg: cfg, db: db, rdb: rdb, broker: broker}
}

// IsDeviceOnline allows the WeChat callback path to reject a call before
// creating a TiRTC token when the MQTT target is known to be offline. Test and
// compatibility publishers that do not expose presence keep the old behavior.
func (s *Server) IsDeviceOnline(ctx context.Context, deviceID string) bool {
	checker, ok := s.broker.(mqttOnlineChecker)
	return !ok || checker.IsOnline(ctx, "sn_"+deviceID)
}

func (s *Server) Register(r *gin.Engine) {
	v1 := r.Group("/v1/voip")

	// WeChat callback — no auth
	v1.GET("/notification/:wx_app_id", s.notification)
	v1.POST("/notification/:wx_app_id", s.notification)

	// Device endpoints — JWT required (device_id claim)
	dev := v1.Group("/device", JWTAuth(s.Config().JWTSecret))
	dev.POST("/profile", s.postDeviceProfile)
	dev.GET("/contacts", s.getDeviceVoipContacts)
	// Deprecated compatibility alias. New device clients should use /contacts.
	dev.GET("/callers", s.getDeviceCallers)
	dev.POST("/call", s.postDeviceCall)

	// User / mini-program endpoints — user JWT required (user_id claim)
	usr := v1.Group("/user", UserJWTAuth(s.Config().JWTSecret))
	usr.POST("/sn-ticket", s.postSnTicket)
	usr.POST("/wechat-mini-login", s.postWeChatMiniLogin)
	usr.POST("/cancel", s.postUserCancel)
	usr.GET("/auth-list", s.getUserAuthList)
	usr.GET("/contact-remark", s.getUserContactRemark)
	usr.PUT("/contact-remark", s.putUserContactRemark)
	usr.GET("/contacts", s.getUserVoipContacts)
	usr.POST("/report-auth", s.postReportAuth)
	usr.POST("/delete-auth", s.postDeleteAuth)

	// Internal — service-to-service, X-Internal-Key auth
	v1.POST("/internal/unbind", s.postInternalUnbind)
}

// JWTAuth validates Bearer JWT and sets device_id in context.
func JWTAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "缺少设备登录凭证"})
			return
		}
		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})
		if err != nil || !tok.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "设备登录凭证无效或已过期"})
			return
		}
		claims, ok := tok.Claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "设备登录凭证内容无效"})
			return
		}
		deviceID, _ := claims["device_id"].(string)
		if deviceID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "设备登录凭证缺少 device_id"})
			return
		}
		c.Set("device_id", deviceID)
		c.Next()
	}
}

func currentDeviceID(c *gin.Context) string {
	v, _ := c.Get("device_id")
	s, _ := v.(string)
	return s
}

// UserJWTAuth validates Bearer JWT and sets user_id in context.
func UserJWTAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "缺少用户登录凭证"})
			return
		}
		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})
		if err != nil || !tok.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "用户登录凭证无效或已过期"})
			return
		}
		claims, ok := tok.Claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "用户登录凭证内容无效"})
			return
		}
		uid, ok := claims["user_id"].(float64)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "用户登录凭证缺少 user_id"})
			return
		}
		c.Set("user_id", int64(uid))
		c.Next()
	}
}

func currentUserID(c *gin.Context) int64 {
	v, _ := c.Get("user_id")
	id, _ := v.(int64)
	return id
}
