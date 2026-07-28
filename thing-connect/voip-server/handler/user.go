package handler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"thing-connect/voip-server/apiresp"
	"thing-connect/voip-server/wechat"
)

const wxLoginBindingTTL = 24 * time.Hour

func wxLoginBindingKey(userID int64, wxAppID string) string {
	return fmt.Sprintf("voip:wx-login:%d:%s", userID, wxAppID)
}

func (s *Server) userOwnsDevice(ctx context.Context, userID int64, deviceID string) (bool, error) {
	var owned bool
	if err := s.db.GetContext(ctx, &owned,
		`SELECT EXISTS(
		   SELECT 1 FROM device_bind WHERE device_id=? AND user_id=?
		 )`, deviceID, userID); err != nil {
		return false, err
	}
	return owned, nil
}

func (s *Server) requireOwnedDevice(c *gin.Context, deviceID string) bool {
	owned, err := s.userOwnsDevice(c.Request.Context(), currentUserID(c), deviceID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "check device ownership: "+err.Error())
		return false
	}
	if !owned {
		apiresp.Fail(c, apiresp.ErrForbidden, "设备不属于当前用户")
		return false
	}
	return true
}

func (s *Server) ownedDeviceName(
	ctx context.Context, userID int64, deviceID string,
) (string, error) {
	var deviceName string
	err := s.db.GetContext(ctx, &deviceName,
		`SELECT device_name FROM device_bind WHERE device_id=? AND user_id=?`,
		deviceID, userID)
	return deviceName, err
}

func (s *Server) verifyWeChatLogin(ctx context.Context, userID int64, wxAppID, wxOpenID string) (bool, error) {
	expected, err := s.rdb.Get(ctx, wxLoginBindingKey(userID, wxAppID)).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return expected == wxOpenID, nil
}

func (s *Server) currentWeChatOpenID(ctx context.Context, userID int64, wxAppID string) (string, error) {
	return s.rdb.Get(ctx, wxLoginBindingKey(userID, wxAppID)).Result()
}

// ── /v1/voip/user/sn-ticket ──────────────────────────────────────────────────

func (s *Server) postSnTicket(c *gin.Context) {
	var req struct {
		DeviceID string `json:"device_id"`
		WxAppID  string `json:"wx_app_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.ErrBadParam, "请求参数格式错误，请检查必填字段和字段类型")
		return
	}
	if req.DeviceID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id")
		return
	}
	userID := currentUserID(c)
	if !s.requireOwnedDevice(c, req.DeviceID) {
		return
	}
	deviceName, err := s.ownedDeviceName(c.Request.Context(), userID, req.DeviceID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "query device_name: "+err.Error())
		return
	}
	deviceName = strings.TrimSpace(deviceName)
	if deviceName == "" {
		// Compatibility with deployed mini-programs and existing bindings.
		// New clients ask the user to name the device before authorization;
		// old clients can keep authorizing with the device SN as the name.
		deviceName = req.DeviceID
	}
	wxAppID := req.WxAppID
	if wxAppID == "" {
		wxAppID = s.cfg.DefaultVoipAppID()
	}
	app, ok := s.cfg.WxAppFor(wxAppID)
	if !ok || app.Secret == "" || app.ModelID == "" {
		apiresp.Fail(c, apiresp.ErrWechatCfg, "未配置微信应用或 model_id")
		return
	}
	accessToken, err := wechat.GetAccessToken(c.Request.Context(), wxAppID, app.Secret)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrWechatAPI, err.Error())
		return
	}
	ticket, err := wechat.GetSnTicket(c.Request.Context(), accessToken, req.DeviceID, app.ModelID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrWechatAPI, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"sn_ticket": ticket, "device_name": deviceName})
}

// ── /v1/voip/user/wechat-mini-login ──────────────────────────────────────────

func (s *Server) postWeChatMiniLogin(c *gin.Context) {
	var req struct {
		Code    string `json:"code"`
		WxAppID string `json:"wx_app_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.ErrBadParam, "请求参数格式错误，请检查必填字段和字段类型")
		return
	}
	if req.Code == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 code")
		return
	}
	wxAppID := req.WxAppID
	if wxAppID == "" {
		wxAppID = s.cfg.DefaultVoipAppID()
	}
	app, ok := s.cfg.WxAppFor(wxAppID)
	if !ok {
		apiresp.Fail(c, apiresp.ErrWechatCfg, "未配置微信应用")
		return
	}
	openid, err := wechat.Jscode2session(c.Request.Context(), wxAppID, app.Secret, req.Code)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrWechatAPI, err.Error())
		return
	}
	userID := currentUserID(c)
	if err := s.rdb.Set(c.Request.Context(), wxLoginBindingKey(userID, wxAppID), openid, wxLoginBindingTTL).Err(); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "save wechat login: "+err.Error())
		return
	}
	apiresp.OK(c, gin.H{"wx_user_openid": openid})
}

// ── /v1/voip/user/cancel ─────────────────────────────────────────────────────

func (s *Server) postUserCancel(c *gin.Context) {
	var req struct {
		DeviceID string `json:"device_id"`
		WxRoomID string `json:"wx_room_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.ErrBadParam, "请求参数格式错误，请检查必填字段和字段类型")
		return
	}
	if req.DeviceID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id")
		return
	}
	if !s.requireOwnedDevice(c, req.DeviceID) {
		return
	}
	userID := currentUserID(c)
	topic := "device/sn_" + req.DeviceID + "/notify"
	envelope := map[string]any{
		"type":    "call_cancel",
		"channel": "wx",
		"payload": map[string]any{"wx_room_id": req.WxRoomID},
	}
	if err := s.broker.Publish(topic, 1, envelope); err != nil {
		slog.ErrorContext(c.Request.Context(), "user-cancel publish failed",
			"user_id", userID, "device_id", req.DeviceID, "err", err)
		apiresp.Fail(c, apiresp.ErrInternal, "publish: "+err.Error())
		return
	}
	apiresp.OK(c, nil)
}

// ── /v1/voip/user/auth-list ──────────────────────────────────────────────────

type userAuthItem struct {
	DeviceID             string `db:"device_id" json:"device_id"`
	Remark               string `db:"remark" json:"remark"`
	AuthorizedDeviceName string `db:"authorized_device_name" json:"authorized_device_name"`
	AuthStatus           string `db:"auth_status" json:"auth_status"`
}

func (s *Server) getUserAuthList(c *gin.Context) {
	userID := currentUserID(c)
	wxAppID := c.Query("wx_app_id")
	if wxAppID == "" {
		wxAppID = s.cfg.DefaultVoipAppID()
	}

	wxOpenID, err := s.rdb.Get(
		c.Request.Context(),
		wxLoginBindingKey(userID, wxAppID),
	).Result()
	if err == redis.Nil {
		apiresp.Fail(c, apiresp.ErrWechatLoginInvalid, "需要先完成微信登录")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "get wechat login: "+err.Error())
		return
	}

	list := make([]userAuthItem, 0)
	if err := s.db.SelectContext(c.Request.Context(), &list,
		`SELECT auth.device_id, COALESCE(profile.remark, auth.remark) AS remark,
		        auth.authorized_device_name, auth.auth_status
		   FROM voip_device_auth auth
		   JOIN device_bind bind_record ON bind_record.device_id=auth.device_id
		   LEFT JOIN voip_user_profile profile
		     ON profile.wx_open_id=auth.wx_open_id AND profile.wx_app_id=auth.wx_app_id
		  WHERE bind_record.user_id=?
		    AND auth.wx_open_id=?
		    AND auth.wx_app_id=?
		    AND auth.auth_status=?
		  ORDER BY auth.created_at DESC`,
		userID, wxOpenID, wxAppID, voipAuthStatusActive); err != nil {
		slog.ErrorContext(c.Request.Context(), "user auth list query failed",
			"user_id", userID, "wx_app_id", wxAppID, "err", err)
		apiresp.Fail(c, apiresp.ErrInternal, "query auth list: "+err.Error())
		return
	}

	apiresp.OK(c, gin.H{"list": list})
}

func (s *Server) globalVoipRemark(ctx context.Context, wxOpenID, wxAppID string) (string, error) {
	var remark string
	err := s.db.GetContext(ctx, &remark,
		`SELECT remark FROM voip_user_profile WHERE wx_open_id=? AND wx_app_id=?`,
		wxOpenID, wxAppID)
	if err == nil {
		return remark, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}

	err = s.db.GetContext(ctx, &remark,
		`SELECT remark FROM voip_device_auth
		  WHERE wx_open_id=? AND wx_app_id=? AND remark<>''
		  ORDER BY created_at DESC, id DESC LIMIT 1`,
		wxOpenID, wxAppID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return remark, err
}

func (s *Server) setGlobalVoipRemark(ctx context.Context, wxOpenID, wxAppID, remark string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO voip_user_profile (wx_open_id, wx_app_id, remark)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE remark=VALUES(remark)`,
		wxOpenID, wxAppID, remark); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE voip_device_auth SET remark=? WHERE wx_open_id=? AND wx_app_id=?`,
		remark, wxOpenID, wxAppID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) publishGlobalVoipRemarkUpdate(ctx context.Context, wxOpenID, wxAppID string) {
	var deviceIDs []string
	if err := s.db.SelectContext(ctx, &deviceIDs,
		`SELECT device_id FROM voip_device_auth WHERE wx_open_id=? AND wx_app_id=?`,
		wxOpenID, wxAppID); err != nil {
		slog.ErrorContext(ctx, "query global remark devices failed",
			"wx_app_id", wxAppID, "err", err)
		return
	}
	for _, deviceID := range deviceIDs {
		s.publishAuthUpdate(ctx, deviceID)
	}
}

func (s *Server) getUserContactRemark(c *gin.Context) {
	userID := currentUserID(c)
	wxAppID := c.Query("wx_app_id")
	if wxAppID == "" {
		wxAppID = s.cfg.DefaultVoipAppID()
	}
	wxOpenID, err := s.currentWeChatOpenID(c.Request.Context(), userID, wxAppID)
	if err == redis.Nil {
		apiresp.Fail(c, apiresp.ErrWechatLoginInvalid, "需要先完成微信登录")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "get wechat login: "+err.Error())
		return
	}
	remark, err := s.globalVoipRemark(c.Request.Context(), wxOpenID, wxAppID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "query contact remark: "+err.Error())
		return
	}
	apiresp.OK(c, gin.H{"wx_open_id": wxOpenID, "remark": remark})
}

func (s *Server) putUserContactRemark(c *gin.Context) {
	var req struct {
		WxAppID string `json:"wx_app_id"`
		Remark  string `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.ErrBadParam, "请求参数格式错误，请检查必填字段和字段类型")
		return
	}
	remark, valid := normalizeAuthRemark(req.Remark)
	if !valid || remark == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "remark 不能为空且不能超过 64 个字符")
		return
	}
	if req.WxAppID == "" {
		req.WxAppID = s.cfg.DefaultVoipAppID()
	}
	userID := currentUserID(c)
	wxOpenID, err := s.currentWeChatOpenID(c.Request.Context(), userID, req.WxAppID)
	if err == redis.Nil {
		apiresp.Fail(c, apiresp.ErrWechatLoginInvalid, "需要先完成微信登录")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "get wechat login: "+err.Error())
		return
	}
	if err := s.setGlobalVoipRemark(c.Request.Context(), wxOpenID, req.WxAppID, remark); err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "save contact remark: "+err.Error())
		return
	}
	s.publishGlobalVoipRemarkUpdate(c.Request.Context(), wxOpenID, req.WxAppID)
	apiresp.OK(c, nil)
}

// getUserVoipContacts lets H5 query one owned device's mini-program VoIP
// contacts. It deliberately uses user JWT and an explicit device_id, unlike
// the device endpoint which derives device identity from its MQTT token.
func (s *Server) getUserVoipContacts(c *gin.Context) {
	deviceID := c.Query("device_id")
	if deviceID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id")
		return
	}
	if !s.requireOwnedDevice(c, deviceID) {
		return
	}

	list, err := s.listDeviceVoipContacts(c.Request.Context(), deviceID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "query contacts: "+err.Error())
		return
	}
	apiresp.OK(c, gin.H{"contacts": list})
}

// ── /v1/voip/user/report-auth ────────────────────────────────────────────────

const maxAuthRemarkChars = 64

func normalizeAuthRemark(raw string) (string, bool) {
	remark := strings.TrimSpace(raw)
	return remark, utf8.RuneCountInString(remark) <= maxAuthRemarkChars
}

func (s *Server) postReportAuth(c *gin.Context) {
	var req struct {
		DeviceID             string `json:"device_id"`
		WxAppID              string `json:"wx_app_id"`
		WxModelID            string `json:"wx_model_id"`
		WxOpenID             string `json:"wx_open_id"`
		Remark               string `json:"remark"`
		DeviceName           string `json:"device_name"`
		AuthorizationCreated bool   `json:"authorization_created"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.ErrBadParam, "请求参数格式错误，请检查必填字段和字段类型")
		return
	}
	if req.DeviceID == "" || req.WxOpenID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id 或 wx_open_id")
		return
	}
	remark, validRemark := normalizeAuthRemark(req.Remark)
	if !validRemark {
		apiresp.Fail(c, apiresp.ErrBadParam, "remark 不能超过 64 个字符")
		return
	}
	deviceName := strings.TrimSpace(req.DeviceName)
	if deviceName != "" && utf8.RuneCountInString(deviceName) > 13 {
		apiresp.Fail(c, apiresp.ErrBadParam, "device_name 不能超过 13 个字符")
		return
	}
	if deviceName == "" {
		// Compatibility with deployed mini-programs, which authorized with the
		// device SN before business device names were introduced.
		deviceName = req.DeviceID
	}
	writeGlobalRemark := remark != ""
	userID := currentUserID(c)
	if !s.requireOwnedDevice(c, req.DeviceID) {
		return
	}
	currentDeviceName, err := s.ownedDeviceName(c.Request.Context(), userID, req.DeviceID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "query device_name: "+err.Error())
		return
	}
	currentDeviceName = strings.TrimSpace(currentDeviceName)
	if req.AuthorizationCreated {
		if currentDeviceName == "" {
			apiresp.Fail(c, apiresp.ErrBadParam, "授权前必须设置 device_name")
			return
		}
		if deviceName != currentDeviceName {
			apiresp.Fail(c, apiresp.ErrBadParam, "授权时的 device_name 与当前设备名称不一致")
			return
		}
	}
	wxAppID := req.WxAppID
	if wxAppID == "" {
		wxAppID = s.cfg.DefaultVoipAppID()
	}
	validLogin, err := s.verifyWeChatLogin(c.Request.Context(), userID, wxAppID, req.WxOpenID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "verify wechat login: "+err.Error())
		return
	}
	if !validLogin {
		apiresp.Fail(c, apiresp.ErrWechatLoginInvalid, "需要先完成微信登录，或 wx_open_id 与当前登录用户不一致")
		return
	}
	wxModelID := req.WxModelID
	if wxModelID == "" {
		if app, ok := s.cfg.WxAppFor(wxAppID); ok {
			wxModelID = app.ModelID
		}
	}

	if remark == "" {
		if saved, lookupErr := s.globalVoipRemark(c.Request.Context(), req.WxOpenID, wxAppID); lookupErr != nil {
			apiresp.Fail(c, apiresp.ErrInternal, "query contact remark: "+lookupErr.Error())
			return
		} else {
			remark = saved
		}
	}

	tx, err := s.db.BeginTxx(c.Request.Context(), nil)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "begin auth save: "+err.Error())
		return
	}
	defer tx.Rollback()

	globalRemarkChanged := false
	if writeGlobalRemark {
		var currentGlobalRemark string
		globalRemarkErr := tx.GetContext(c.Request.Context(), &currentGlobalRemark,
			`SELECT remark
			   FROM voip_user_profile
			  WHERE wx_open_id=? AND wx_app_id=?
			  FOR UPDATE`,
			req.WxOpenID, wxAppID)
		if globalRemarkErr != nil && globalRemarkErr != sql.ErrNoRows {
			apiresp.Fail(c, apiresp.ErrInternal, "query existing contact remark: "+globalRemarkErr.Error())
			return
		}
		globalRemarkChanged = globalRemarkErr == sql.ErrNoRows || currentGlobalRemark != remark

		var staleRemarkAuthIDs []int64
		if err := tx.SelectContext(c.Request.Context(), &staleRemarkAuthIDs,
			`SELECT id
			   FROM voip_device_auth
			  WHERE wx_open_id=? AND wx_app_id=? AND remark<>?
			  ORDER BY id
			  FOR UPDATE`,
			req.WxOpenID, wxAppID, remark); err != nil {
			apiresp.Fail(c, apiresp.ErrInternal, "query stale authorization remarks: "+err.Error())
			return
		}
		globalRemarkChanged = globalRemarkChanged || len(staleRemarkAuthIDs) > 0
	}

	var existingAuth struct {
		WxModelID            string `db:"wx_model_id"`
		Remark               string `db:"remark"`
		AuthorizedDeviceName string `db:"authorized_device_name"`
		AuthStatus           string `db:"auth_status"`
	}
	existingAuthErr := tx.GetContext(c.Request.Context(), &existingAuth,
		`SELECT wx_model_id, remark, authorized_device_name, auth_status
		   FROM voip_device_auth
		  WHERE device_id=? AND wx_open_id=? AND wx_app_id=?
		  FOR UPDATE`,
		req.DeviceID, req.WxOpenID, wxAppID)
	if existingAuthErr != nil && existingAuthErr != sql.ErrNoRows {
		apiresp.Fail(c, apiresp.ErrInternal, "query existing auth: "+existingAuthErr.Error())
		return
	}
	desiredAuthorizedDeviceName := existingAuth.AuthorizedDeviceName
	if req.AuthorizationCreated || desiredAuthorizedDeviceName == "" {
		desiredAuthorizedDeviceName = deviceName
	}
	authChanged := existingAuthErr == sql.ErrNoRows ||
		existingAuth.WxModelID != wxModelID ||
		existingAuth.Remark != remark ||
		existingAuth.AuthorizedDeviceName != desiredAuthorizedDeviceName ||
		existingAuth.AuthStatus != voipAuthStatusActive

	_, err = tx.ExecContext(c.Request.Context(),
		`INSERT INTO voip_device_auth (
		     device_id, wx_open_id, wx_app_id, wx_model_id, remark,
		     authorized_device_name, auth_status, invalid_reason,
		     invalid_at, last_verified_at, created_at
		 )
	         VALUES (?, ?, ?, ?, ?, ?, ?, '', NULL, ?, ?)
	         ON DUPLICATE KEY UPDATE
	           wx_model_id=VALUES(wx_model_id),
	           remark=VALUES(remark),
	           authorized_device_name=IF(? OR authorized_device_name='', VALUES(authorized_device_name), authorized_device_name),
	           auth_status=VALUES(auth_status),
	           invalid_reason='',
	           invalid_at=NULL,
	           last_verified_at=VALUES(last_verified_at)`,
		req.DeviceID, req.WxOpenID, wxAppID, wxModelID, remark,
		deviceName, voipAuthStatusActive, time.Now(), time.Now(),
		req.AuthorizationCreated)
	if err == nil && writeGlobalRemark {
		_, err = tx.ExecContext(c.Request.Context(),
			`INSERT INTO voip_user_profile (wx_open_id, wx_app_id, remark)
			 VALUES (?, ?, ?)
			 ON DUPLICATE KEY UPDATE remark=VALUES(remark)`,
			req.WxOpenID, wxAppID, remark)
	}
	if err == nil && writeGlobalRemark {
		_, err = tx.ExecContext(c.Request.Context(),
			`UPDATE voip_device_auth SET remark=? WHERE wx_open_id=? AND wx_app_id=?`,
			remark, req.WxOpenID, wxAppID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "report-auth save failed",
			"user_id", userID, "device_id", req.DeviceID, "err", err)
		apiresp.Fail(c, apiresp.ErrInternal, "save auth: "+err.Error())
		return
	}

	if globalRemarkChanged {
		s.publishGlobalVoipRemarkUpdate(c.Request.Context(), req.WxOpenID, wxAppID)
	} else if authChanged {
		s.publishAuthUpdate(c.Request.Context(), req.DeviceID)
	}
	apiresp.OK(c, nil)
}

// ── /v1/voip/user/delete-auth ────────────────────────────────────────────────

func (s *Server) postDeleteAuth(c *gin.Context) {
	var req struct {
		DeviceID string `json:"device_id"`
		WxAppID  string `json:"wx_app_id"`
		WxOpenID string `json:"wx_open_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.ErrBadParam, "请求参数格式错误，请检查必填字段和字段类型")
		return
	}
	if req.DeviceID == "" || req.WxOpenID == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id 或 wx_open_id")
		return
	}
	userID := currentUserID(c)
	if !s.requireOwnedDevice(c, req.DeviceID) {
		return
	}
	wxAppID := req.WxAppID
	if wxAppID == "" {
		wxAppID = s.cfg.DefaultVoipAppID()
	}
	validLogin, err := s.verifyWeChatLogin(c.Request.Context(), userID, wxAppID, req.WxOpenID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "verify wechat login: "+err.Error())
		return
	}
	if !validLogin {
		apiresp.Fail(c, apiresp.ErrWechatLoginInvalid, "需要先完成微信登录，或 wx_open_id 与当前登录用户不一致")
		return
	}

	result, err := s.db.ExecContext(c.Request.Context(),
		`DELETE FROM voip_device_auth WHERE device_id=? AND wx_open_id=? AND wx_app_id=?`,
		req.DeviceID, req.WxOpenID, wxAppID)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "delete-auth delete failed",
			"user_id", userID, "device_id", req.DeviceID, "err", err)
		apiresp.Fail(c, apiresp.ErrInternal, "delete auth: "+err.Error())
		return
	}

	if rowsAffected, rowsErr := result.RowsAffected(); rowsErr == nil && rowsAffected > 0 {
		s.publishAuthUpdate(c.Request.Context(), req.DeviceID)
	}
	apiresp.OK(c, nil)
}

// publishAuthUpdate sends callers_update notification to device.
// Best-effort: errors are only logged.
func (s *Server) publishAuthUpdate(ctx context.Context, deviceID string) {
	if s.broker == nil {
		return
	}
	topic := "device/sn_" + deviceID + "/notify"
	envelope := map[string]any{
		"type":    "callers_update",
		"channel": "wx",
		"payload": map[string]any{},
	}
	if err := s.broker.Publish(topic, 1, envelope); err != nil {
		slog.ErrorContext(ctx, "publishAuthUpdate failed", "device_id", deviceID, "err", err)
	}
}
