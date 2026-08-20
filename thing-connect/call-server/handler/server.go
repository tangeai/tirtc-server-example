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
	"thing-connect/internal/store"
)

// mqttBroker is the subset of *mqttc.Broker call-server depends on. Kept as a
// small local interface (rather than the concrete type) so tests can inject a
// fake without dialing a real MQTT broker.
type mqttBroker interface {
	IsOnline(ctx context.Context, clientID string) bool
	Publish(topic string, qos byte, payload any) error
}

type Server struct {
	mu     sync.RWMutex
	cfg    *config.Config
	db     *sqlx.DB
	rdb    *redis.Client
	broker mqttBroker
	dev    store.DeviceStore
}

func (s *Server) Config() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copy := *s.cfg
	return &copy
}
func (s *Server) UpdateConfig(cfg *config.Config) { s.mu.Lock(); s.cfg = cfg; s.mu.Unlock() }

func NewServer(cfg *config.Config, db *sqlx.DB, rdb *redis.Client, broker mqttBroker, dev store.DeviceStore) *Server {
	return &Server{cfg: cfg, db: db, rdb: rdb, broker: broker, dev: dev}
}

func (s *Server) Register(r *gin.Engine) {
	v1 := r.Group("/v1")

	callDev := v1.Group("/call", JWTAuth(s.Config().JWTSecret))
	callDev.POST("/device/info", s.postDeviceInfo)
	callDev.POST("/request", s.postCallRequest)
	callDev.POST("/reject", s.postCallReject)
	callDev.POST("/hangup", s.postCallHangup) // 原 /v1/call/leave
	callDev.POST("/cancel", s.postCallCancel)
	callDev.GET("/room", s.getDeviceRoom) // NEW
	callDev.GET("/device/contacts", s.getDeviceContacts)
	callDev.GET("/device/contacts/pending", s.getDeviceContactsPending)
	callDev.POST("/device/contacts/request", s.postDeviceContactRequest)
	callDev.POST("/device/contacts/respond", s.postDeviceContactRespond)
	callDev.PUT("/device/contacts/remark", s.putDeviceContactRemark)
	callDev.DELETE("/device/contacts", s.deleteDeviceContact)

	callUser := v1.Group("/call/user", UserJWTAuth(s.Config().JWTSecret))
	callUser.GET("/contacts", s.getUserContacts)
	callUser.GET("/contacts/pending", s.getUserContactsPending)
	callUser.POST("/contacts/request", s.postUserContactRequest)
	callUser.POST("/contacts/respond", s.postUserContactRespond)
	callUser.PUT("/contacts/remark", s.putUserContactRemark)
	callUser.DELETE("/contacts/:id", s.deleteUserContact)

	v1.POST("/call/internal/unbind", s.postInternalUnbind)
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
