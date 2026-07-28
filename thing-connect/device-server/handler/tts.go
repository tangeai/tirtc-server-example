package handler

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"thing-connect/device-server/ttsdata"
	"thing-connect/internal/apiresp"
	"thing-connect/internal/service"
)

func (s *Server) getTTS(c *gin.Context) {
	c.Header("Cache-Control", "no-store")

	auth := c.GetHeader("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") == "" {
		apiresp.Unauthorized(c)
		return
	}
	token := strings.TrimPrefix(auth, "Bearer ")

	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		apiresp.BadParam(c, "需要提供 6 位验证码")
		return
	}

	// The temporary JWT and code must come from the same device/report flow.
	if err := s.devSvc.AuthorizeTTS(c.Request.Context(), code, token); err != nil {
		if errors.Is(err, service.ErrSigFail) {
			apiresp.Unauthorized(c)
			return
		}
		if errors.Is(err, service.ErrInvalidCode) {
			apiresp.Fail(c, http.StatusNotFound, apiresp.CodeInvalidCode, "验证码无效或已过期")
			return
		}
		log.Printf("[tts] authorization error: %v", err)
		apiresp.Internal(c, "tts authorization failed")
		return
	}

	// Build PCM audio from the digit code.
	pcm := ttsdata.Build(code)
	if len(pcm) == 0 {
		apiresp.Internal(c, "tts build returned empty audio")
		return
	}

	log.Printf("[tts] OK pcm_bytes=%d", len(pcm))

	// Device default: raw PCM. Browser/VLC: add ?fmt=wav for playable container.
	if c.Query("fmt") == "wav" {
		c.Data(http.StatusOK, "audio/wav", ttsdata.WAV(pcm))
		return
	}
	c.Data(http.StatusOK, "audio/pcm;rate=8000;channels=1;format=s16le", pcm)
}
