// Package handler provides authenticated recognition HTTP transport.
package handler

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"io"
	"net/http"
	"strconv"
	"strings"
	"thing-connect/internal/apiresp"
	r "thing-connect/internal/recognition"
)

type Service interface {
	State(context.Context, r.Actor, string) (r.State, error)
	Types(context.Context) ([]r.Scheme, error)
	SetSelection(context.Context, r.Actor, string, bool, []r.Binding, int64) (r.State, error)
	Submit(context.Context, r.Actor, string, r.Request) (r.Task, bool, error)
	Get(context.Context, r.Actor, string, string) (r.Task, error)
	List(context.Context, r.Actor, string, int64) ([]r.Task, error)
	Audio(context.Context, r.Actor, string, string, string) ([]byte, error)
}

func Register(engine *gin.Engine, secret string, s Service) {
	g := engine.Group("/v1/recognition", func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		t, e := jwt.Parse(strings.TrimPrefix(h, "Bearer "), func(*jwt.Token) (any, error) { return []byte(secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
		if e != nil || !t.Valid {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		claims, ok := t.Claims.(jwt.MapClaims)
		if !ok {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		device, _ := claims["device_id"].(string)
		uid, _ := claims["user_id"].(float64)
		if strings.HasPrefix(device, "tmp_") || (device == "" && uid <= 0) || device != "" && uid > 0 {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		c.Set("actor", r.Actor{DeviceID: device, UserID: int64(uid)})
		c.Header("Cache-Control", "no-store")
	})
	actor := func(c *gin.Context) r.Actor { return c.MustGet("actor").(r.Actor) }
	g.GET("/config", func(c *gin.Context) {
		a := actor(c)
		v, e := s.State(c.Request.Context(), a, a.DeviceID)
		write(c, v, e)
	})
	g.GET("/types", func(c *gin.Context) {
		if actor(c).UserID <= 0 {
			write(c, nil, r.ErrForbidden)
			return
		}
		v, e := s.Types(c.Request.Context())
		write(c, v, e)
	})
	g.GET("/devices/:device_id/config", func(c *gin.Context) {
		if actor(c).UserID <= 0 {
			write(c, nil, r.ErrForbidden)
			return
		}
		v, e := s.State(c.Request.Context(), actor(c), c.Param("device_id"))
		write(c, v, e)
	})
	g.PUT("/devices/:device_id/config", func(c *gin.Context) {
		var v struct {
			Enabled  *bool       `json:"enabled"`
			Schemes  []r.Binding `json:"schemes"`
			Revision *int64      `json:"expected_revision"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
		if c.ShouldBindJSON(&v) != nil || v.Enabled == nil || v.Revision == nil {
			write(c, nil, r.ErrInvalid)
			return
		}
		out, e := s.SetSelection(c.Request.Context(), actor(c), c.Param("device_id"), *v.Enabled, v.Schemes, *v.Revision)
		write(c, out, e)
	})
	g.POST("/requests", func(c *gin.Context) {
		a := actor(c)
		if a.DeviceID == "" {
			write(c, nil, r.ErrForbidden)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, r.MaxUploadBytes)
		if e := c.Request.ParseMultipartForm(r.MaxUploadBytes); e != nil {
			var limit *http.MaxBytesError
			if errors.As(e, &limit) {
				c.JSON(413, apiresp.JSON{Code: 40000, Msg: "音频文件超过上传上限"})
			} else {
				write(c, nil, r.ErrInvalid)
			}
			return
		}
		defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
		f := c.Request.MultipartForm.File["files"]
		rev, e := strconv.ParseInt(c.PostForm("config_revision"), 10, 64)
		if e != nil || len(f) != 1 || len(c.Request.MultipartForm.File) != 1 {
			write(c, nil, r.ErrInvalid)
			return
		}
		sampleRate, channels := 0, 0
		for field, target := range map[string]*int{"sample_rate": &sampleRate, "channels": &channels} {
			if values, present := c.Request.MultipartForm.Value[field]; present {
				if len(values) != 1 {
					write(c, nil, r.ErrInvalid)
					return
				}
				n, err := strconv.Atoi(values[0])
				if err != nil || n <= 0 {
					write(c, nil, r.ErrInvalid)
					return
				}
				*target = n
			}
		}
		file, e := f[0].Open()
		if e != nil {
			write(c, nil, r.ErrInvalid)
			return
		}
		defer file.Close()
		b, e := io.ReadAll(io.LimitReader(file, 5<<20+1))
		if e != nil {
			write(c, nil, r.ErrInvalid)
			return
		}
		v, fresh, e := s.Submit(c.Request.Context(), a, a.DeviceID, r.Request{ID: c.PostForm("request_id"), Revision: rev, MediaType: c.PostForm("media_type"), AudioFormat: c.PostForm("audio_format"), SampleRate: sampleRate, Channels: channels, Data: b})
		if e != nil {
			write(c, nil, e)
			return
		}
		status := 200
		if fresh {
			status = 202
		}
		c.JSON(status, apiresp.JSON{Code: 200, Msg: "ok", Data: v})
	})
	g.GET("/requests/:request_id", func(c *gin.Context) {
		a := actor(c)
		v, e := s.Get(c.Request.Context(), a, a.DeviceID, c.Param("request_id"))
		write(c, v, e)
	})
	g.GET("/requests", func(c *gin.Context) {
		cursor, err := strconv.ParseInt(c.DefaultQuery("cursor", "0"), 10, 64)
		if err != nil || cursor < 0 {
			write(c, nil, r.ErrInvalid)
			return
		}
		a := actor(c)
		v, e := s.List(c.Request.Context(), a, a.DeviceID, cursor)
		var next any
		if len(v) == 50 {
			next = v[len(v)-1].Cursor
		}
		write(c, map[string]any{"items": v, "next_cursor": next}, e)
	})
	g.GET("/requests/:request_id/audio/:item_id", func(c *gin.Context) {
		a := actor(c)
		b, e := s.Audio(c.Request.Context(), a, a.DeviceID, c.Param("request_id"), c.Param("item_id"))
		if e != nil {
			write(c, nil, e)
			return
		}
		c.Data(200, "audio/wav", b)
	})
}
func write(c *gin.Context, v any, e error) {
	if e == nil {
		apiresp.OK(c, v)
		return
	}
	status, code, msg := 500, 50000, "识别服务处理失败，请稍后重试"
	switch {
	case errors.Is(e, r.ErrInvalid):
		status, code, msg = 400, 40000, "请检查音频格式、大小、时长和识别配置"
	case errors.Is(e, r.ErrForbidden):
		status, code, msg = 403, 40300, "无权访问该设备"
	case errors.Is(e, r.ErrDisabled):
		status, code, msg = 409, 40901, "AI识别未开启，请在设备设置中开启"
	case errors.Is(e, r.ErrConflict):
		status, code, msg = 409, 40901, "配置或请求已变更，请刷新后重试"
	case errors.Is(e, r.ErrQuota):
		status, code, msg = 429, 429, "设备本月识别额度已用完"
	case errors.Is(e, r.ErrNotFound):
		status, code, msg = 404, 40400, "识别结果不存在"
	case errors.Is(e, r.ErrUnavailable):
		status, code, msg = 503, 50300, "识别模型尚未配置，请联系管理员"
	}
	c.JSON(status, apiresp.JSON{Code: code, Msg: msg})
}
