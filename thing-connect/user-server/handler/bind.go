package handler

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/model"
)

type bindReq struct {
	Code string `json:"code" binding:"required,len=6"`
}

func (s *Server) postBind(c *gin.Context) {
	var req bindReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	deviceID, err := s.bindSvc.Bind(c.Request.Context(), currentUserID(c), req.Code)
	if err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, gin.H{"device_id": deviceID, "msg": "bind success"})
}

type bindByDeviceIDReq struct {
	DeviceID   string `json:"device_id"   binding:"required"`
	MAC        string `json:"mac"`
	ChipUID    string `json:"chip_uid"`
	DeviceRand string `json:"device_rand"`
}

func (s *Server) postBindByDeviceID(c *gin.Context) {
	var req bindByDeviceIDReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	fp := model.Fingerprint{MAC: req.MAC, ChipUID: req.ChipUID, DeviceRand: req.DeviceRand}
	if err := s.bindSvc.BindByDeviceID(c.Request.Context(), currentUserID(c), req.DeviceID, fp); err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, gin.H{"device_id": req.DeviceID, "msg": "bind success"})
}

type resetReq struct {
	DeviceID string `json:"device_id" binding:"required"`
}

func (s *Server) deleteReset(c *gin.Context) {
	var req resetReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	deviceID := req.DeviceID
	if err := s.bindSvc.Reset(c.Request.Context(), deviceID, currentUserID(c)); err != nil {
		apiresp.FromError(c, err)
		return
	}
	// Run cross-cutting cleanup callbacks (all best-effort).
	s.runUnbindCleanup(c.Request.Context(), deviceID)
	apiresp.OK(c, gin.H{"msg": "reset success"})
}

// runUnbindCleanup executes all registered cleanup callbacks after a successful
// unbind. Each callback runs in its own goroutine; failures are logged but never
// block the response.
func (s *Server) runUnbindCleanup(ctx context.Context, deviceID string) {
	if s.UnbindCleanup == nil {
		return
	}
	if s.UnbindCleanup.Enqueue != nil {
		if err := s.UnbindCleanup.Enqueue(ctx, deviceID); err != nil {
			log.Printf("[reset] enqueue durable cleanup device=%s: %v", deviceID, err)
		}
		return
	}
	// Synchronous: local DB cleanup (cheap, want it done before response in most cases).
	if s.UnbindCleanup.DeleteLocalRole != nil {
		if err := s.UnbindCleanup.DeleteLocalRole(ctx, deviceID); err != nil {
			log.Printf("[reset] cleanup local ai_device_role device=%s: %v", deviceID, err)
		}
	}
	// Asynchronous: cloud API calls and inter-service notifications.
	if s.UnbindCleanup.DeleteCloudRoles != nil {
		go func() {
			if err := s.UnbindCleanup.DeleteCloudRoles(context.Background(), deviceID); err != nil {
				log.Printf("[reset] cleanup cloud device-roles device=%s: %v", deviceID, err)
			}
		}()
	}
	if s.callServerURL != "" {
		go s.notifyCallServerUnbind(deviceID)
	}
	if s.UnbindCleanup.NotifyVoIP != nil {
		go s.UnbindCleanup.NotifyVoIP(deviceID)
	}
}

// notifyCallServerUnbind tells call-server to clean up contacts for the unbound
// device. Runs in its own goroutine with a 3s timeout; failures are logged but
// never block the unbind response (best-effort).
func (s *Server) notifyCallServerUnbind(deviceID string) {
	body, _ := json.Marshal(map[string]string{"device_id": deviceID})
	slog.Info("unbind notify call-server req", "url", s.callServerURL+"/v1/call/internal/unbind", "body", string(body))
	req, _ := http.NewRequest(http.MethodPost,
		s.callServerURL+"/v1/call/internal/unbind",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", s.internalKey)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		slog.Warn("unbind notify call-server failed", "device", deviceID, "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("[reset] call-server unbind device=%s returned %d", deviceID, resp.StatusCode)
	}
}
