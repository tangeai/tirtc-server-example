// Package handler exposes push settings, subscription and internal event HTTP routes.
package handler

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"thing-connect/internal/apiresp"
	"thing-connect/internal/push"
)

func write(c *gin.Context, data any, err error) {
	if err == nil {
		apiresp.OK(c, data)
		return
	}
	status, code, msg := 500, 50000, "通知服务暂不可用"
	switch {
	case errors.Is(err, push.ErrInvalid):
		status, code, msg = 400, 40000, "请检查通知参数"
	case errors.Is(err, push.ErrExpired):
		status, code, msg = 410, 41000, "该提醒或订阅操作已失效"
	case errors.Is(err, push.ErrBinding):
		status, code, msg = 410, 6006, "设备绑定关系已变化"
	case errors.Is(err, push.ErrConflict):
		status, code, msg = 409, 40901, "信息已变化，请刷新后重试"
	case errors.Is(err, push.ErrForbidden):
		status, code, msg = 403, 40300, "无权访问此设备"
	case errors.Is(err, push.ErrUnavailable):
		status, code, msg = 503, 50300, "微信订阅暂不可用"
	}
	c.JSON(status, apiresp.JSON{Code: code, Msg: msg})
}
func Register(engine *gin.Engine, s *push.Service, key, secret string) {
	g := engine.Group("/v1/push", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
		c.Header("Cache-Control", "no-store")
	})
	g.POST("/internal/events", func(c *gin.Context) {
		if key == "" || subtle.ConstantTimeCompare([]byte(c.GetHeader("X-Internal-Key")), []byte(key)) != 1 {
			c.JSON(403, apiresp.JSON{Code: 40301, Msg: "内部身份无效"})
			return
		}
		var e push.Event
		if c.ShouldBindJSON(&e) != nil {
			write(c, nil, push.ErrInvalid)
			return
		}
		outcome, err := s.Receive(c.Request.Context(), e)
		write(c, gin.H{"outcome": outcome}, err)
	})
	u := g.Group("/user", func(c *gin.Context) {
		h, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		token, err := jwt.Parse(h, func(*jwt.Token) (any, error) { return []byte(secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
		if err != nil || !token.Valid {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		claims := token.Claims.(jwt.MapClaims)
		id, _ := claims["user_id"].(float64)
		if id <= 0 || claims["device_id"] != nil {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		c.Set("user", int64(id))
	})
	u.GET("/capabilities", func(c *gin.Context) {
		apiresp.OK(c, gin.H{"crying": s.Enabled("crying"), "quarrel": s.Enabled("quarrel")})
	})
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		u.Handle(method, "/preferences/:device/:kind", func(c *gin.Context) {
			kind := c.Param("kind")
			if kind != "crying" && kind != "quarrel" {
				write(c, nil, push.ErrInvalid)
				return
			}
			var input struct {
				Seconds *int `json:"interval_seconds"`
			}
			if c.Request.Method == "PUT" && (c.ShouldBindJSON(&input) != nil || input.Seconds == nil || *input.Seconds < 0 || *input.Seconds > 86400) {
				write(c, nil, push.ErrInvalid)
				return
			}
			val, err := s.Preference(c.Request.Context(), c.GetInt64("user"), c.Param("device"), kind, c.Request.Method != "GET", input.Seconds)
			write(c, gin.H{"interval_seconds": val, "enabled": s.Enabled(kind)}, err)
		})
	}
	u.POST("/subscriptions", func(c *gin.Context) {
		var b struct {
			App  string `json:"app_id"`
			Code string `json:"code"`
			Kind string `json:"event_type"`
		}
		if c.ShouldBindJSON(&b) != nil {
			write(c, nil, push.ErrInvalid)
			return
		}
		id, replace, err := s.Subscribe(c.Request.Context(), c.GetInt64("user"), b.App, b.Code, b.Kind)
		write(c, gin.H{"session_id": id, "replace": replace, "template_id": s.TemplateID()}, err)
	})
	u.POST("/subscriptions/:id/confirm", func(c *gin.Context) {
		var b struct {
			Replace bool `json:"replace"`
		}
		if c.ShouldBindJSON(&b) != nil {
			write(c, nil, push.ErrInvalid)
			return
		}
		write(c, nil, s.Confirm(c.Request.Context(), c.GetInt64("user"), c.Param("id"), b.Replace))
	})
	u.POST("/subscriptions/:id/result", func(c *gin.Context) {
		var b struct {
			Decision string `json:"decision"`
		}
		if c.ShouldBindJSON(&b) != nil || (b.Decision != "accept" && b.Decision != "reject" && b.Decision != "cancel") {
			write(c, nil, push.ErrInvalid)
			return
		}
		write(c, nil, s.Report(c.Request.Context(), c.GetInt64("user"), c.Param("id"), b.Decision))
	})
	u.GET("/references/:id", func(c *gin.Context) {
		device, err := s.Reference(c.Request.Context(), c.GetInt64("user"), c.Param("id"))
		write(c, gin.H{"device_id": device}, err)
	})
}
