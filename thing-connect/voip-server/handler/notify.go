package handler

import (
	"context"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"thing-connect/voip-server/wechat"
)

const voipNotificationDedupeTTL = 10 * time.Minute
const voipNotificationProcessing = "processing"
const voipNotificationComplete = "done"

func voipNotificationKey(wxAppID, roomID string) string {
	return "voip:notify:" + wxAppID + ":" + roomID
}

const releaseCallGuardScript = `
local deleted = 0
for i, key in ipairs(KEYS) do
  if redis.call("GET", key) == ARGV[1] then
    deleted = deleted + redis.call("DEL", key)
  end
end
return deleted
`

func (s *Server) AcquireVoipNotification(
	ctx context.Context, wxAppID, roomID string,
) (bool, error) {
	return s.rdb.SetNX(
		ctx,
		voipNotificationKey(wxAppID, roomID),
		voipNotificationProcessing,
		voipNotificationDedupeTTL,
	).Result()
}

func (s *Server) IsVoipNotificationComplete(
	ctx context.Context, wxAppID, roomID string,
) (bool, error) {
	value, err := s.rdb.Get(ctx, voipNotificationKey(wxAppID, roomID)).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	return value == voipNotificationComplete, err
}

func (s *Server) CompleteVoipNotification(
	ctx context.Context, wxAppID, roomID string,
) error {
	return s.rdb.Set(
		ctx,
		voipNotificationKey(wxAppID, roomID),
		voipNotificationComplete,
		voipNotificationDedupeTTL,
	).Err()
}

func (s *Server) ReleaseVoipNotification(ctx context.Context, wxAppID, roomID string) {
	_ = s.rdb.Del(ctx, voipNotificationKey(wxAppID, roomID)).Err()
}

// ReleaseOutgoingCallGuards ends the HTTP duplicate-request window once the
// corresponding WeChat room notification has reached the device. Incoming
// mini-program calls have no matching keys, so the same callback is harmless.
func (s *Server) ReleaseOutgoingCallGuards(
	ctx context.Context, wxAppID, deviceID, wxOpenID, callID string,
) {
	if callID == "" {
		return
	}
	_ = s.rdb.Eval(
		ctx,
		releaseCallGuardScript,
		[]string{
			deviceCallGuardKey(deviceID),
			contactCallGuardKey(wxAppID, wxOpenID),
		},
		callID,
	).Err()
}

func (s *Server) notification(c *gin.Context) {
	wxAppID := c.Param("wx_app_id")
	cfg := s.Config()
	app, ok := cfg.WxAppFor(wxAppID)
	if !ok {
		c.JSON(200, gin.H{"errcode": 3, "errmsg": "未配置微信应用"})
		return
	}

	appCfg := wechat.WxAppCfg{
		AppID:          wxAppID,
		AppSecret:      app.Secret,
		Token:          app.Token,
		EncodingAESKey: app.EncodingAESKey,
		ModelID:        app.ModelID,
	}
	tirtcCfg := wechat.TirtcServerCfg{
		BaseURL:   cfg.Tirtc.Endpoint,
		AccessID:  cfg.Tirtc.AccessKeyID,
		AppID:     cfg.Tirtc.AppID,
		SecretKey: cfg.Tirtc.SecretKeyID,
	}
	wechat.HandleNotification(c, wxAppID, appCfg, tirtcCfg, s.broker, s, cfg.ProxyEndpointFor)
}
