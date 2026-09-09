package handler

import (
	"context"
	_ "embed"
	"errors"
	"github.com/gin-gonic/gin"
	"log/slog"
	"net/http"
	intercom "thing-connect/internal/room"
)

//go:embed room_page.html
var roomPage string

type Intercom interface {
	Change(context.Context, intercom.Operation) (intercom.Assignment, error)
	Current(context.Context, string, int64) (intercom.Assignment, error)
	Connect(context.Context, string, intercom.Presence) (intercom.Credential, error)
	Report(context.Context, string, intercom.Presence) error
}

func RegisterIntercom(r *gin.Engine, secret string, service Intercom) {
	r.GET("/v1/call/group/page", func(c *gin.Context) { c.Data(200, "text/html; charset=utf-8", []byte(roomPage)) })
	web := r.Group("/v1/call/group/web", UserJWTAuth(secret))
	web.GET("/device/:device_id", func(c *gin.Context) {
		a, e := service.Current(c.Request.Context(), c.Param("device_id"), currentUserID(c))
		intercomResponse(c, a, e)
	})
	for _, kind := range []string{"create", "join", "leave"} {
		operation := kind
		web.POST("/device/:device_id/"+kind, func(c *gin.Context) {
			var o intercom.Operation
			if !roomBody(c, &o) {
				return
			}
			o.DeviceID = c.Param("device_id")
			o.UserID = currentUserID(c)
			o.Kind = operation
			o.IP = c.ClientIP()
			a, e := service.Change(c.Request.Context(), o)
			intercomResponse(c, a, e)
		})
	}
	device := r.Group("/v1/call/group/device", JWTAuth(secret))
	device.GET("/assignment", func(c *gin.Context) {
		a, e := service.Current(c.Request.Context(), currentDeviceID(c), 0)
		intercomResponse(c, a, e)
	})
	device.POST("/connect-token", func(c *gin.Context) {
		var p intercom.Presence
		if !roomBody(c, &p) {
			return
		}
		token, e := service.Connect(c.Request.Context(), currentDeviceID(c), p)
		c.Header("Cache-Control", "no-store")
		intercomResponse(c, token, e)
	})
	device.POST("/presence", func(c *gin.Context) {
		var p intercom.Presence
		if !roomBody(c, &p) {
			return
		}
		intercomResponse(c, nil, service.Report(c.Request.Context(), currentDeviceID(c), p))
	})
	for _, kind := range []string{"create", "join", "leave"} {
		operation := kind
		device.POST("/"+kind, func(c *gin.Context) {
			var o intercom.Operation
			if !roomBody(c, &o) {
				return
			}
			o.DeviceID = currentDeviceID(c)
			o.Kind = operation
			o.IP = c.ClientIP()
			a, e := service.Change(c.Request.Context(), o)
			intercomResponse(c, a, e)
		})
	}
}
func roomBody(c *gin.Context, dst any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if e := c.ShouldBindJSON(dst); e != nil {
		intercomResponse(c, nil, intercom.ErrInvalid)
		return false
	}
	return true
}
func intercomResponse(c *gin.Context, data any, err error) {
	c.Header("Cache-Control", "no-store")
	if err == nil {
		c.JSON(200, gin.H{"code": 200, "msg": "ok", "data": data})
		return
	}
	code := 50000
	message := "服务器内部错误，请稍后重试"
	for _, entry := range []struct {
		err  error
		code int
	}{{intercom.ErrInvalid, 40000}, {intercom.ErrForbidden, 40300}, {intercom.ErrNotFound, 40400}, {intercom.ErrPassword, 40320}, {intercom.ErrLocked, 42920}, {intercom.ErrLimited, 42900}, {intercom.ErrFull, 40920}, {intercom.ErrStale, 40921}, {intercom.ErrAssigned, 40923}, {intercom.ErrUnavailable, 50200}} {
		if errors.Is(err, entry.err) {
			code = entry.code
			message = entry.err.Error()
			break
		}
	}
	if code == 50000 {
		slog.ErrorContext(c.Request.Context(), "room request failed", "error", err)
	}
	c.JSON(200, gin.H{"code": code, "msg": message})
}
