package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"thing-connect/voip-server/apiresp"
	"thing-connect/voip-server/wechat"
)

// postDeviceProfile upserts the device's media profile JSON.
// The body is stored verbatim as VARCHAR(512).
func (s *Server) postDeviceProfile(c *gin.Context) {
	deviceID := currentDeviceID(c)

	var body json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		apiresp.Fail(c, apiresp.ErrBadParam, "请求 JSON 格式错误")
		return
	}
	profileStr := string(body)
	if len(profileStr) > 512 {
		apiresp.Fail(c, apiresp.ErrBadParam, "profile 不能超过 512 字节")
		return
	}
	if err := validateVideoUIProfile(body); err != nil {
		apiresp.Fail(c, apiresp.ErrBadParam, err.Error())
		return
	}

	_, err := s.db.ExecContext(c.Request.Context(),
		`INSERT INTO voip_device_profile (device_id, profile, updated_at)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE profile=VALUES(profile), updated_at=VALUES(updated_at)`,
		deviceID, profileStr, time.Now())
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "save profile: "+err.Error())
		return
	}
	apiresp.OK(c, nil)
}

// GetDeviceProfile returns the stored profile JSON for a device.
// Returns ("", nil) when no profile exists.
func (s *Server) GetDeviceProfile(ctx context.Context, deviceID string) (string, error) {
	var profile string
	err := s.db.QueryRowContext(ctx,
		`SELECT profile FROM voip_device_profile WHERE device_id=?`, deviceID).Scan(&profile)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return profile, err
}

// GetDeviceVoipContactRemark returns the name this device should display for
// an authorized mini-program caller. The global profile is authoritative and
// the per-device authorization remark remains a compatibility fallback.
func (s *Server) GetDeviceVoipContactRemark(
	ctx context.Context, deviceID, wxOpenID, wxAppID string,
) (string, error) {
	var remark string
	err := s.db.GetContext(ctx, &remark,
		`SELECT COALESCE(profile.remark, auth.remark)
		   FROM voip_device_auth auth
		   JOIN device_bind bind_record
		     ON bind_record.device_id=auth.device_id AND bind_record.user_id<>0
		   LEFT JOIN voip_user_profile profile
		     ON profile.wx_open_id=auth.wx_open_id AND profile.wx_app_id=auth.wx_app_id
			  WHERE auth.device_id=? AND auth.wx_open_id=? AND auth.wx_app_id=?
			    AND auth.auth_status=?
			  LIMIT 1`,
		deviceID, wxOpenID, wxAppID, voipAuthStatusActive)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return remark, err
}

type deviceVoipContact struct {
	WxOpenID  string `json:"wx_open_id"`
	WxAppID   string `json:"wx_app_id"`
	WxModelID string `json:"wx_model_id"`
	Remark    string `json:"remark"`
	CreatedAt string `json:"created_at"`
}

const voipAuthStatusActive = "active"
const deviceCallGuardTTL = 30 * time.Second
const contactCallGuardTTL = 30 * time.Second

func deviceCallGuardKey(deviceID string) string {
	return "voip:device-call:" + deviceID
}

func contactCallGuardKey(wxAppID, wxOpenID string) string {
	return "voip:contact-call:" + wxAppID + ":" + wxOpenID
}

func newVoipCallID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

type activeDeviceAuth struct {
	WxAppID   string `db:"wx_app_id"`
	WxModelID string `db:"wx_model_id"`
	Remark    string `db:"remark"`
}

func (s *Server) getActiveDeviceAuth(
	ctx context.Context, deviceID, wxOpenID, wxAppID string,
) (*activeDeviceAuth, error) {
	var auth activeDeviceAuth
	err := s.db.GetContext(ctx, &auth,
		`SELECT auth.wx_app_id, auth.wx_model_id,
		        COALESCE(profile.remark, auth.remark) AS remark
		   FROM voip_device_auth auth
		   JOIN device_bind bind_record
		     ON bind_record.device_id=auth.device_id AND bind_record.user_id<>0
		   LEFT JOIN voip_user_profile profile
		     ON profile.wx_open_id=auth.wx_open_id AND profile.wx_app_id=auth.wx_app_id
		  WHERE auth.device_id=?
		    AND auth.wx_open_id=?
		    AND auth.wx_app_id=?
		    AND auth.auth_status=?
		  LIMIT 1`,
		deviceID, wxOpenID, wxAppID, voipAuthStatusActive)
	if err != nil {
		return nil, err
	}
	return &auth, nil
}

func (s *Server) isDeviceBound(ctx context.Context, deviceID string) (bool, error) {
	var bound bool
	err := s.db.GetContext(ctx, &bound,
		`SELECT EXISTS(
		     SELECT 1 FROM device_bind WHERE device_id=? AND user_id<>0
		   )`,
		deviceID)
	return bound, err
}

func (s *Server) invalidateDeviceAuth(
	ctx context.Context, deviceID, wxOpenID, wxAppID, reason string,
) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE voip_device_auth
		    SET auth_status='invalid', invalid_reason=?, invalid_at=NOW()
		  WHERE device_id=? AND wx_open_id=? AND wx_app_id=?`,
		reason, deviceID, wxOpenID, wxAppID)
	return err
}

func (s *Server) listDeviceVoipContacts(ctx context.Context, deviceID string) ([]deviceVoipContact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT auth.wx_open_id, auth.wx_app_id, auth.wx_model_id,
		        COALESCE(profile.remark, auth.remark) AS remark, auth.created_at
		   FROM voip_device_auth auth
		   JOIN device_bind bind_record
		     ON bind_record.device_id=auth.device_id AND bind_record.user_id<>0
		   LEFT JOIN voip_user_profile profile
		     ON profile.wx_open_id=auth.wx_open_id AND profile.wx_app_id=auth.wx_app_id
			  WHERE auth.device_id=? AND auth.auth_status=?
			  ORDER BY auth.created_at DESC`,
		deviceID, voipAuthStatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]deviceVoipContact, 0)
	for rows.Next() {
		var item deviceVoipContact
		var createdAt time.Time
		if err := rows.Scan(&item.WxOpenID, &item.WxAppID, &item.WxModelID, &item.Remark, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		list = append(list, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

// getDeviceVoipContacts returns only mini-program VoIP contacts for the
// authenticated device. Device-to-device contacts remain in call-server.
func (s *Server) getDeviceVoipContacts(c *gin.Context) {
	list, err := s.listDeviceVoipContacts(c.Request.Context(), currentDeviceID(c))
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "query contacts: "+err.Error())
		return
	}
	apiresp.OK(c, gin.H{"contacts": list})
}

// getDeviceCallers is the legacy response shape kept for deployed devices.
func (s *Server) getDeviceCallers(c *gin.Context) {
	list, err := s.listDeviceVoipContacts(c.Request.Context(), currentDeviceID(c))
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "query auth: "+err.Error())
		return
	}
	apiresp.OK(c, gin.H{"list": list})
}

// postDeviceCall initiates a WeChat VoIP call from a device to a user.
func (s *Server) postDeviceCall(c *gin.Context) {
	var req struct {
		DeviceID               string `json:"device_id"`
		WxAppID                string `json:"wx_app_id"`
		WxUserOpenid           string `json:"wx_user_openid"`
		WxModelID              string `json:"wx_model_id"`
		WxListenerName         string `json:"wx_listener_name"`
		WxVersionType          int    `json:"wx_version_type"`
		WxQuery                string `json:"wx_query"`
		WxRoomType             string `json:"wx_room_type"`
		WxCallerCameraStatus   int    `json:"wx_caller_camera_status"`
		WxListenerCameraStatus int    `json:"wx_listener_camera_status"`
		Payload                string `json:"payload"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.Fail(c, apiresp.ErrBadParam, "请求参数格式错误，请检查必填字段和字段类型")
		return
	}
	if jwtDevID := currentDeviceID(c); jwtDevID != req.DeviceID {
		apiresp.Fail(c, apiresp.ErrBadParam, "device_id 与设备登录凭证不一致")
		return
	}
	if req.DeviceID == "" || req.WxUserOpenid == "" {
		apiresp.Fail(c, apiresp.ErrBadParam, "缺少 device_id 或 wx_user_openid")
		return
	}
	if req.WxRoomType != "video" && req.WxRoomType != "voice" {
		apiresp.Fail(c, apiresp.ErrBadParam, "wx_room_type 必须为 video 或 voice")
		return
	}
	wxQuery := req.WxQuery
	if req.WxRoomType == "video" {
		profile, err := s.GetDeviceProfile(c.Request.Context(), req.DeviceID)
		if err != nil {
			apiresp.Fail(c, apiresp.ErrInternal, "query profile: "+err.Error())
			return
		}
		if strings.TrimSpace(profile) != "" {
			uiConfig := videoUIConfigFromProfile(profile)
			wxQuery = queryWithVideoUIConfig(wxQuery, uiConfig)
		}
	}
	wxAppID := req.WxAppID
	if wxAppID == "" {
		wxAppID = s.Config().DefaultVoipAppID()
	}
	bound, err := s.isDeviceBound(c.Request.Context(), req.DeviceID)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "query device binding: "+err.Error())
		return
	}
	if !bound {
		apiresp.Fail(c, apiresp.ErrDeviceUnbound, "设备已解绑，请重新完成设备绑定")
		return
	}
	auth, err := s.getActiveDeviceAuth(
		c.Request.Context(), req.DeviceID, req.WxUserOpenid, wxAppID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		apiresp.Fail(c, apiresp.ErrVoipAuthInvalid, "微信 VoIP 授权不存在或已失效，请让用户重新授权")
		return
	}
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "query VoIP authorization: "+err.Error())
		return
	}
	wxAppID = auth.WxAppID
	app, ok := s.Config().WxAppFor(wxAppID)
	if !ok {
		apiresp.Fail(c, apiresp.ErrWechatCfg, "未配置微信应用")
		return
	}
	callID, err := newVoipCallID()
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "generate call_id: "+err.Error())
		return
	}
	guardKey := deviceCallGuardKey(req.DeviceID)
	acquired, err := s.rdb.SetNX(
		c.Request.Context(), guardKey, callID, deviceCallGuardTTL,
	).Result()
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "reserve device call: "+err.Error())
		return
	}
	if !acquired {
		apiresp.Fail(c, apiresp.ErrConflict, "设备刚发起过 VoIP 呼叫，请稍后重试")
		return
	}
	releaseGuard := true
	guardKeys := []string{guardKey}
	defer func() {
		if releaseGuard {
			_ = s.rdb.Del(context.Background(), guardKeys...).Err()
		}
	}()
	contactGuardKey := contactCallGuardKey(wxAppID, req.WxUserOpenid)
	acquired, err = s.rdb.SetNX(
		c.Request.Context(), contactGuardKey, callID, contactCallGuardTTL,
	).Result()
	if err != nil {
		apiresp.Fail(c, apiresp.ErrInternal, "reserve contact call: "+err.Error())
		return
	}
	if !acquired {
		apiresp.Fail(c, apiresp.ErrConflict, "该联系人刚被呼叫过，请稍后重试")
		return
	}
	guardKeys = append(guardKeys, contactGuardKey)

	accessToken, err := wechat.GetAccessToken(c.Request.Context(), wxAppID, app.Secret)
	if err != nil {
		apiresp.Fail(c, apiresp.ErrWechatAPI, err.Error())
		return
	}
	wxPayload := req.Payload
	if strings.TrimSpace(wxPayload) == "" {
		encoded, marshalErr := json.Marshal(map[string]string{
			"id":        callID,
			"from":      req.DeviceID,
			"to":        req.WxUserOpenid,
			"room_type": req.WxRoomType,
		})
		if marshalErr != nil {
			apiresp.Fail(c, apiresp.ErrInternal, "marshal call payload: "+marshalErr.Error())
			return
		}
		wxPayload = string(encoded)
	}
	listenerName := req.WxListenerName
	if listenerName == "" {
		listenerName = auth.Remark
	}
	payload := map[string]any{
		"model_id":               auth.WxModelID,
		"sn":                     req.DeviceID,
		"openid":                 req.WxUserOpenid,
		"room_type":              req.WxRoomType,
		"listener_name":          listenerName,
		"query":                  wxQuery,
		"version_type":           req.WxVersionType,
		"caller_camera_status":   req.WxCallerCameraStatus,
		"listener_camera_status": req.WxListenerCameraStatus,
		"payload":                wxPayload,
	}
	if err := wechat.IotVoipCall(c.Request.Context(), accessToken, payload); err != nil {
		var wxErr *wechat.APIError
		if errors.As(err, &wxErr) && wxErr.Errcode == 9 {
			if updateErr := s.invalidateDeviceAuth(
				c.Request.Context(), req.DeviceID, req.WxUserOpenid, wxAppID,
				"wechat_errcode_9",
			); updateErr != nil {
				apiresp.Fail(c, apiresp.ErrInternal, "invalidate VoIP authorization: "+updateErr.Error())
				return
			}
			s.publishAuthUpdate(c.Request.Context(), req.DeviceID)
			apiresp.Fail(c, apiresp.ErrVoipAuthInvalid, "微信 VoIP 授权已失效，请让用户重新授权")
			return
		}
		apiresp.Fail(c, apiresp.ErrWechatAPI, err.Error())
		return
	}
	releaseGuard = false
	_, _ = s.db.ExecContext(c.Request.Context(),
		`UPDATE voip_device_auth SET last_verified_at=NOW()
		  WHERE device_id=? AND wx_open_id=? AND wx_app_id=?`,
		req.DeviceID, req.WxUserOpenid, wxAppID)
	apiresp.OK(c, gin.H{"call_id": callID})
}

func validCameraRotation(rotation int) bool {
	switch rotation {
	case 0, 90, 180, 270:
		return true
	default:
		return false
	}
}

type videoUIConfig struct {
	CameraRotation *int
	AspectRatio    *float64
	HorMirror      *bool
	VertMirror     *bool
	ObjectFit      *string
}

func validateVideoUIProfile(profile json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(profile, &fields); err != nil || fields == nil {
		return fmt.Errorf("profile 必须是 JSON 对象")
	}
	if raw, ok := fields["camera_rotation"]; ok {
		var rotation int
		if isJSONNull(raw) || json.Unmarshal(raw, &rotation) != nil || !validCameraRotation(rotation) {
			return fmt.Errorf("camera_rotation 必须为 0、90、180 或 270")
		}
	}
	if raw, ok := fields["aspect_ratio"]; ok {
		var ratio float64
		if isJSONNull(raw) || json.Unmarshal(raw, &ratio) != nil || ratio <= 0 {
			return fmt.Errorf("aspect_ratio 必须是大于 0 的数字")
		}
	}
	for _, name := range []string{"hor_mirror", "vert_mirror"} {
		if raw, ok := fields[name]; ok {
			var enabled bool
			if isJSONNull(raw) || json.Unmarshal(raw, &enabled) != nil {
				return fmt.Errorf("%s 必须为布尔值", name)
			}
		}
	}
	if raw, ok := fields["object_fit"]; ok {
		var objectFit string
		if isJSONNull(raw) || json.Unmarshal(raw, &objectFit) != nil ||
			(objectFit != "fill" && objectFit != "contain") {
			return fmt.Errorf("object_fit 必须为 fill 或 contain")
		}
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func videoUIConfigFromProfile(profile string) videoUIConfig {
	var config videoUIConfig
	if strings.TrimSpace(profile) == "" {
		return config
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(profile), &fields); err != nil {
		return config
	}
	if raw, ok := fields["camera_rotation"]; ok && !isJSONNull(raw) {
		var rotation int
		if json.Unmarshal(raw, &rotation) == nil && validCameraRotation(rotation) {
			config.CameraRotation = &rotation
		}
	}
	if raw, ok := fields["aspect_ratio"]; ok && !isJSONNull(raw) {
		var ratio float64
		if json.Unmarshal(raw, &ratio) == nil && ratio > 0 {
			config.AspectRatio = &ratio
		}
	}
	for name, target := range map[string]**bool{
		"hor_mirror":  &config.HorMirror,
		"vert_mirror": &config.VertMirror,
	} {
		if raw, ok := fields[name]; ok && !isJSONNull(raw) {
			var enabled bool
			if json.Unmarshal(raw, &enabled) == nil {
				*target = &enabled
			}
		}
	}
	if raw, ok := fields["object_fit"]; ok && !isJSONNull(raw) {
		var objectFit string
		if json.Unmarshal(raw, &objectFit) == nil &&
			(objectFit == "fill" || objectFit == "contain") {
			config.ObjectFit = &objectFit
		}
	}
	return config
}

func queryWithVideoUIConfig(rawQuery string, config videoUIConfig) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(rawQuery), "?")
	uiValues := make(url.Values)
	if config.CameraRotation != nil {
		uiValues.Set("camera_rotation", strconv.Itoa(*config.CameraRotation))
	}
	if config.AspectRatio != nil {
		uiValues.Set("aspect_ratio", strconv.FormatFloat(*config.AspectRatio, 'g', -1, 64))
	}
	if config.HorMirror != nil {
		uiValues.Set("hor_mirror", strconv.FormatBool(*config.HorMirror))
	}
	if config.VertMirror != nil {
		uiValues.Set("vert_mirror", strconv.FormatBool(*config.VertMirror))
	}
	if config.ObjectFit != nil {
		uiValues.Set("object_fit", *config.ObjectFit)
	}
	if len(uiValues) == 0 {
		return trimmed
	}
	values, err := url.ParseQuery(trimmed)
	if err != nil {
		if trimmed == "" {
			return uiValues.Encode()
		}
		return trimmed + "&" + uiValues.Encode()
	}
	for name, items := range uiValues {
		values[name] = items
	}
	return values.Encode()
}
