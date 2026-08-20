package handler

import (
	"sync"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/tirtcapi"

	"github.com/gin-gonic/gin"
)

var tirtcAppID string
var tirtcAccessKeyID string
var tirtcSecretKeyID string
var tirtcEndpoint string
var tirtcMu sync.RWMutex

func SetTirtcCredentials(appID, accessKeyID, secretKeyID, endpoint string) {
	tirtcMu.Lock()
	defer tirtcMu.Unlock()
	tirtcAppID = appID
	tirtcAccessKeyID = accessKeyID
	tirtcSecretKeyID = secretKeyID
	tirtcEndpoint = endpoint
}

// GET /v1/user/device/rtc-token?device_id=xxx
func (s *Server) getRtcToken(c *gin.Context) {
	deviceID := c.Query("device_id")
	if deviceID == "" {
		apiresp.BadParam(c, "缺少 device_id")
		return
	}

	userID := currentUserID(c)
	tirtcMu.RLock()
	appID, accessKeyID, secretKeyID, endpoint := tirtcAppID, tirtcAccessKeyID, tirtcSecretKeyID, tirtcEndpoint
	tirtcMu.RUnlock()

	ok, err := s.bindSvc.ExistsUserDevice(c.Request.Context(), deviceID, userID)
	if err != nil {
		apiresp.FromError(c, err)
		return
	}
	if !ok {
		apiresp.Fail(c, 403, 40300, "设备不存在或不属于当前用户")
		return
	}

	gpool, err := s.bindSvc.GetDeviceKey(c.Request.Context(), deviceID)
	if err != nil {
		apiresp.FromError(c, err)
		return
	}

	token, err := tirtcapi.BuildDeviceToken(accessKeyID, secretKeyID, gpool.DeviceKey, deviceID)
	if err != nil {
		apiresp.Internal(c, "failed to build token")
		return
	}

	// Still issue the token even if the device is mid device-to-device call —
	// just let the caller know, so H5 can decide whether to warn the user or
	// attempt the connection anyway.
	inCall, err := s.bindSvc.IsInCall(c.Request.Context(), deviceID)
	if err != nil {
		apiresp.FromError(c, err)
		return
	}

	apiresp.OK(c, gin.H{
		"token":    token,
		"app_id":   appID,
		"endpoint": endpoint,
		"in_call":  inCall,
	})
}
