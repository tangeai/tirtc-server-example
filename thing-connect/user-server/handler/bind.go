package handler

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/bindlimit"
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
		writeBindError(c, err)
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
		writeBindError(c, err)
		return
	}
	apiresp.OK(c, gin.H{"device_id": req.DeviceID, "msg": "bind success"})
}

func writeBindError(c *gin.Context, err error) {
	var locked *bindlimit.LockedError
	if errors.As(err, &locked) {
		c.Header("Retry-After", strconv.Itoa(int((locked.RetryAfter+time.Second-1)/time.Second)))
	}
	apiresp.FromError(c, err)
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
	if err := s.bindSvc.ResetWithCleanup(c.Request.Context(), deviceID, currentUserID(c), s.cleanupTargets); err != nil {
		apiresp.FromError(c, err)
		return
	}
	apiresp.OK(c, gin.H{"msg": "reset success"})
}
