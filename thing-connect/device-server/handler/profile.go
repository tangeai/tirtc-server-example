package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"thing-connect/internal/apiresp"
	"thing-connect/internal/service"
)

type MediaReporter interface {
	Report(context.Context, string, map[string]json.RawMessage) error
}

func RegisterDeviceProfile(r *gin.Engine, reporter MediaReporter, secret string) {
	r.POST("/v1/device/profile", func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			apiresp.Unauthorized(c)
			return
		}
		token, err := jwt.Parse(strings.TrimPrefix(auth, "Bearer "), func(*jwt.Token) (any, error) { return []byte(secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
		if err != nil || !token.Valid {
			apiresp.Unauthorized(c)
			return
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			apiresp.Unauthorized(c)
			return
		}
		deviceID, _ := claims["device_id"].(string)
		if deviceID == "" || len(deviceID) > 64 || strings.HasPrefix(deviceID, "tmp_") || claims["user_id"] != nil {
			apiresp.Unauthorized(c)
			return
		}
		var req struct {
			Profiles map[string]json.RawMessage `json:"profiles"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&req); err != nil {
			apiresp.BadParam(c, "媒体能力请求格式错误，正文不得超过 16 KiB")
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			apiresp.BadParam(c, "请求正文只能包含一个 JSON 对象")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if err = reporter.Report(ctx, deviceID, req.Profiles); err != nil {
			if errors.Is(err, service.ErrInvalidMediaProfile) {
				apiresp.BadParam(c, err.Error())
				return
			}
			apiresp.FromError(c, err)
			return
		}
		apiresp.OK(c, nil)
	})
}
