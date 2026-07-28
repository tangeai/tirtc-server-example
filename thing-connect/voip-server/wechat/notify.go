package wechat

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"thing-connect/voip-server/tirtcapi"

	"github.com/gin-gonic/gin"
)

const (
	ErrCodeWxaUnexpectedEvent    = 2
	ErrCodeWxaAppNotConfigured   = 3
	ErrCodeWxaUnexpectedAction   = 4
	ErrCodeWxaInvalidSignature   = 5
	ErrCodeWxaInvalidBody        = 9
	ErrCodeWxaPushToDeviceFailed = 10
)

// MQTTPublisher publishes a message to a device's down topic.
type MQTTPublisher interface {
	Publish(topic string, qos byte, payload any) error
}

// DeviceProfileGetter fetches the device context needed by an incoming call.
type DeviceProfileGetter interface {
	GetDeviceProfile(ctx context.Context, deviceID string) (string, error)
	GetDeviceVoipContactRemark(ctx context.Context, deviceID, wxOpenID, wxAppID string) (string, error)
}

// DeviceOnlineChecker is optional. An offline device cannot receive a
// non-retained call notification, even when publishing to the broker succeeds.
type DeviceOnlineChecker interface {
	IsDeviceOnline(ctx context.Context, deviceID string) bool
}

// NotificationDeduper is optional. Implementations prevent WeChat retries from
// creating duplicate TiRTC sessions and duplicate call_incoming messages.
type NotificationDeduper interface {
	AcquireVoipNotification(ctx context.Context, wxAppID, roomID string) (bool, error)
	IsVoipNotificationComplete(ctx context.Context, wxAppID, roomID string) (bool, error)
	CompleteVoipNotification(ctx context.Context, wxAppID, roomID string) error
	ReleaseVoipNotification(ctx context.Context, wxAppID, roomID string)
}

// OutgoingCallGuardReleaser is optional. A successful room notification means
// the device's outbound HTTP request is no longer a duplicate in flight.
type OutgoingCallGuardReleaser interface {
	ReleaseOutgoingCallGuards(ctx context.Context, wxAppID, deviceID, wxOpenID, callID string)
}

// WxAppCfg 单个微信小程序配置，由调用方从配置中传入。
type WxAppCfg struct {
	AppID          string
	AppSecret      string
	Token          string
	EncodingAESKey string
	ModelID        string
}

// TirtcServerCfg tirtc-server-api 配置。
type TirtcServerCfg struct {
	BaseURL   string
	AccessID  string
	AppID     string
	SecretKey string
}

type voipCallPayload struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	To       string `json:"to"`
	RoomType string `json:"room_type"`
}

type voipMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string
	FromUserName string
	CreateTime   int64
	MsgType      string
	Event        string
	Action       string
	Payload      string
	RoomId       string
	SessionKey   string
	ServerToken  string
	ModelId      string
	Sn           string
	OpenID       string           `xml:"-"`
	PayloadData  *voipCallPayload `xml:"-"`
}

func (m *voipMessage) isVoipNotify() bool {
	return m.MsgType == "event" && m.Event == "iot_voip_notify"
}

// HandleNotification GET|POST /:wx_app_id 微信回调入口。
// proxyEndpointFor: 返回非空字符串时，将原始请求透传到该 endpoint，不做本地处理。
func HandleNotification(
	c *gin.Context,
	wxAppID string,
	appCfg WxAppCfg,
	tirtcCfg TirtcServerCfg,
	publisher MQTTPublisher,
	profiler DeviceProfileGetter,
	proxyEndpointFor func(deviceID string) string,
) {
	slog.InfoContext(c.Request.Context(), "voip notify received", "wx_app_id", wxAppID, "method", c.Request.Method, "encrypt_type", c.Query("encrypt_type"))

	if echo := c.Query("echostr"); echo != "" {
		ok := checkSignature(c.Query("signature"), appCfg.Token, c.Query("timestamp"), c.Query("nonce"))
		slog.InfoContext(c.Request.Context(), "voip notify echostr verify", "wx_app_id", wxAppID, "ok", ok)
		if !ok {
			c.JSON(200, gin.H{"errcode": ErrCodeWxaInvalidSignature, "errmsg": "签名无效"})
			return
		}
		c.String(200, echo)
		return
	}

	body, readErr := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	_ = c.Request.Body.Close()
	if readErr != nil {
		slog.ErrorContext(c.Request.Context(), "voip notify read body failed", "wx_app_id", wxAppID, "err", readErr)
		c.JSON(200, gin.H{"errcode": ErrCodeWxaInvalidBody, "errmsg": "读取回调请求体失败"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	slog.DebugContext(c.Request.Context(), "voip notify body", "len", len(body), "wx_app_id", wxAppID)
	if len(bytes.TrimSpace(body)) == 0 {
		c.JSON(200, gin.H{"errcode": ErrCodeWxaInvalidBody, "errmsg": "回调请求体为空"})
		return
	}

	if !checkSignature(c.Query("signature"), appCfg.Token, c.Query("timestamp"), c.Query("nonce")) {
		slog.WarnContext(c.Request.Context(), "voip notify invalid signature", "wx_app_id", wxAppID)
		c.JSON(200, gin.H{"errcode": ErrCodeWxaInvalidSignature, "errmsg": "签名无效"})
		return
	}
	msg, err := parsePostBody(c.Query("encrypt_type"), body, appCfg, wxAppID, c.Query("msg_signature"), c.Query("timestamp"), c.Query("nonce"))
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "voip notify parse body failed", "wx_app_id", wxAppID, "err", err)
		c.JSON(200, gin.H{"errcode": ErrCodeWxaInvalidBody, "errmsg": "解析回调请求体失败"})
		return
	}
	msg.OpenID = c.Query("openid")
	if msg.OpenID == "" && msg.PayloadData != nil {
		msg.OpenID = msg.PayloadData.To
	}
	slog.InfoContext(c.Request.Context(), "voip notify parsed msg",
		"wx_app_id", wxAppID, "msg_type", msg.MsgType, "event", msg.Event, "action", msg.Action,
		"sn", msg.Sn, "openid", msg.OpenID, "room_id", msg.RoomId)

	if !msg.isVoipNotify() {
		slog.InfoContext(c.Request.Context(), "voip notify not voip notify", "wx_app_id", wxAppID, "msg_type", msg.MsgType, "event", msg.Event)
		c.JSON(200, gin.H{"errcode": ErrCodeWxaUnexpectedEvent, "errmsg": "微信回调事件类型不正确，应为 iot_voip_notify"})
		return
	}
	uuid := msg.Sn
	if msg.Action != "join_voip_room" {
		slog.InfoContext(c.Request.Context(), "voip notify unexpected action", "wx_app_id", wxAppID, "action", msg.Action)
		c.JSON(200, gin.H{"errcode": ErrCodeWxaUnexpectedAction, "errmsg": "微信回调 action 不正确，应为 join_voip_room"})
		return
	}
	if proxyEndpointFor != nil {
		if endpoint := proxyEndpointFor(uuid); endpoint != "" {
			slog.InfoContext(c.Request.Context(), "voip notify proxying", "sn", uuid, "endpoint", endpoint)
			proxyNotification(c, endpoint, body)
			return
		}
	}
	var deduper NotificationDeduper
	if candidate, ok := profiler.(NotificationDeduper); ok && msg.RoomId != "" {
		acquired, acquireErr := candidate.AcquireVoipNotification(
			c.Request.Context(), wxAppID, msg.RoomId,
		)
		if acquireErr != nil {
			slog.ErrorContext(c.Request.Context(), "voip notify dedupe failed",
				"wx_app_id", wxAppID, "room_id", msg.RoomId, "err", acquireErr)
			c.JSON(200, gin.H{"errcode": ErrCodeWxaPushToDeviceFailed, "errmsg": "记录 VoIP 回调处理状态失败"})
			return
		}
		if !acquired {
			complete, completeErr := candidate.IsVoipNotificationComplete(
				c.Request.Context(), wxAppID, msg.RoomId,
			)
			if completeErr != nil {
				slog.ErrorContext(c.Request.Context(), "voip notify dedupe state failed",
					"wx_app_id", wxAppID, "room_id", msg.RoomId, "err", completeErr)
				c.JSON(200, gin.H{
					"errcode": ErrCodeWxaPushToDeviceFailed,
					"errmsg":  "查询 VoIP 回调处理状态失败",
				})
				return
			}
			if complete {
				slog.InfoContext(c.Request.Context(), "voip notify completed duplicate ignored",
					"wx_app_id", wxAppID, "room_id", msg.RoomId)
				c.JSON(200, gin.H{"errcode": 0, "errmsg": "ok"})
				return
			}
			slog.InfoContext(c.Request.Context(), "voip notify duplicate still processing",
				"wx_app_id", wxAppID, "room_id", msg.RoomId)
			c.JSON(200, gin.H{
				"errcode": ErrCodeWxaPushToDeviceFailed,
				"errmsg":  "回调仍在处理中，请稍后重试",
			})
			return
		}
		deduper = candidate
	}
	slog.InfoContext(c.Request.Context(), "voip notify pushing to device", "sn", uuid, "room_id", msg.RoomId, "openid", msg.OpenID)
	if err := pushJoinToDevice(c, appCfg, tirtcCfg, wxAppID, uuid, msg, publisher, profiler); err != nil {
		if deduper != nil {
			deduper.ReleaseVoipNotification(c.Request.Context(), wxAppID, msg.RoomId)
		}
		slog.ErrorContext(c.Request.Context(), "voip notify push to device failed", "sn", uuid, "err", err)
		c.JSON(200, gin.H{"errcode": ErrCodeWxaPushToDeviceFailed, "errmsg": "向设备下发 VoIP 通知失败"})
		return
	}
	if releaser, ok := profiler.(OutgoingCallGuardReleaser); ok {
		callID := ""
		if msg.PayloadData != nil {
			callID = msg.PayloadData.ID
		}
		releaser.ReleaseOutgoingCallGuards(
			c.Request.Context(), wxAppID, uuid, msg.OpenID, callID,
		)
	}
	if deduper != nil {
		if completeErr := deduper.CompleteVoipNotification(
			c.Request.Context(), wxAppID, msg.RoomId,
		); completeErr != nil {
			// MQTT publish already succeeded. Return success to WeChat so a
			// transient Redis error does not create a second device session.
			slog.ErrorContext(c.Request.Context(), "voip notify mark complete failed",
				"wx_app_id", wxAppID, "room_id", msg.RoomId, "err", completeErr)
			deduper.ReleaseVoipNotification(c.Request.Context(), wxAppID, msg.RoomId)
		}
	}
	slog.InfoContext(c.Request.Context(), "voip notify push to device ok", "sn", uuid)
	c.JSON(200, gin.H{"errcode": 0, "errmsg": "ok"})
}

// encryptedXMLMsg 安全模式下微信回调的 XML 消息体。
type encryptedXMLMsg struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	EncryptedMsg string   `xml:"Encrypt"`
}

func parsePostBody(encryptType string, raw []byte, appCfg WxAppCfg, wxAppID, msgSignature, timestamp, nonce string) (*voipMessage, error) {
	if encryptType == "aes" {
		if appCfg.EncodingAESKey == "" {
			return nil, fmt.Errorf("wx_encoding_aes_key not configured")
		}
		if appCfg.Token == "" {
			return nil, fmt.Errorf("wx_token not configured")
		}
		var encXML encryptedXMLMsg
		if err := xml.Unmarshal(raw, &encXML); err != nil {
			return nil, fmt.Errorf("parse encrypted xml: %w", err)
		}
		genSig := wxSignature(appCfg.Token, timestamp, nonce, encXML.EncryptedMsg)
		if msgSignature != genSig {
			return nil, fmt.Errorf("invalid msg_signature")
		}
		_, rawXML, err := wxDecryptMsg(wxAppID, encXML.EncryptedMsg, appCfg.EncodingAESKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt: %w", err)
		}
		var msg voipMessage
		if err := xml.Unmarshal(rawXML, &msg); err != nil {
			return nil, err
		}
		if err := parsePayload(&msg); err != nil {
			return nil, err
		}
		return &msg, nil
	}
	var msg voipMessage
	if err := xml.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	if err := parsePayload(&msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// wxSignature 计算微信消息签名（含 EncryptedMsg 的四参数版本）。
func wxSignature(token, timestamp, nonce, encryptedMsg string) string {
	arr := []string{token, timestamp, nonce, encryptedMsg}
	sort.Strings(arr)
	h := sha1.Sum([]byte(strings.Join(arr, "")))
	return hex.EncodeToString(h[:])
}

// wxDecryptMsg 解密微信 AES-CBC 消息。
// aesKey 为 43 字节的 Base64 编码 EncodingAESKey。
func wxDecryptMsg(appID, encryptedMsg, encodingAESKey string) (random, rawXMLBytes []byte, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("panic: %v", e)
		}
	}()
	if len(encodingAESKey) != 43 {
		return nil, nil, fmt.Errorf("encodingAESKey length must be 43")
	}
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil || len(key) != 32 {
		return nil, nil, fmt.Errorf("encodingAESKey decode error: %v", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encryptedMsg)
	if err != nil {
		return nil, nil, fmt.Errorf("encryptedMsg base64 decode: %w", err)
	}
	const blockSize = 32
	if len(ciphertext) < blockSize || len(ciphertext)%blockSize != 0 {
		return nil, nil, fmt.Errorf("ciphertext length invalid: %d", len(ciphertext))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key[:16]).CryptBlocks(plaintext, ciphertext)

	amountToPad := int(plaintext[len(plaintext)-1])
	if amountToPad < 1 || amountToPad > blockSize {
		return nil, nil, fmt.Errorf("invalid pkcs7 padding: %d", amountToPad)
	}
	plaintext = plaintext[:len(plaintext)-amountToPad]
	if len(plaintext) <= 20 {
		return nil, nil, fmt.Errorf("plaintext too short after decrypt")
	}
	msgLen := int(plaintext[16])<<24 | int(plaintext[17])<<16 | int(plaintext[18])<<8 | int(plaintext[19])
	appIDOffset := 20 + msgLen
	if appIDOffset > len(plaintext) {
		return nil, nil, fmt.Errorf("msgLen %d exceeds plaintext", msgLen)
	}
	if string(plaintext[appIDOffset:]) != appID {
		return nil, nil, fmt.Errorf("appID mismatch in decrypted message")
	}
	return plaintext[:16], plaintext[20:appIDOffset], nil
}

func parsePayload(m *voipMessage) error {
	payload := strings.TrimSpace(m.Payload)
	if payload == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		p := payload
		if len(p) > 200 {
			p = p[:200]
		}
		slog.Warn("voip notify payload not base64, raw json", "payload", p)
		raw = []byte(payload)
	}
	var p voipCallPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		// payload is a developer-defined pass-through field. Correlation data
		// is optional, so an older device's arbitrary payload must not prevent
		// an otherwise valid WeChat room notification from reaching the device.
		slog.Warn("voip notify payload has no correlation json", "payload", payload, "err", err)
		return nil
	}
	m.PayloadData = &p
	return nil
}

func checkSignature(got, token, timestamp, nonce string) bool {
	if token == "" {
		return false
	}
	arr := []string{token, timestamp, nonce}
	sort.Strings(arr)
	h := sha1.Sum([]byte(arr[0] + arr[1] + arr[2]))
	return strings.EqualFold(got, hex.EncodeToString(h[:]))
}

func pushJoinToDevice(c *gin.Context, appCfg WxAppCfg, tirtcCfg TirtcServerCfg, wxAppID, deviceID string, m *voipMessage, publisher MQTTPublisher, profiler DeviceProfileGetter) error {
	if tirtcCfg.BaseURL == "" || tirtcCfg.AccessID == "" || tirtcCfg.AppID == "" || tirtcCfg.SecretKey == "" {
		return fmt.Errorf("tirtc_server_api requires base_url, access_id, app_id, secret_key")
	}
	if strings.TrimSpace(appCfg.ModelID) == "" {
		return fmt.Errorf("wx_model_id is required for VoIP token")
	}
	modelID := strings.TrimSpace(appCfg.ModelID)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	if checker, ok := profiler.(DeviceOnlineChecker); ok &&
		!checker.IsDeviceOnline(ctx, deviceID) {
		return fmt.Errorf("device %s is offline", deviceID)
	}
	client := &http.Client{Timeout: 25 * time.Second}

	profileJSON, err := profiler.GetDeviceProfile(ctx, deviceID)
	if err != nil || profileJSON == "" {
		return fmt.Errorf("device %s has no media profile — call POST /v1/voip/device/profile first", deviceID)
	}
	var media struct {
		ScreenWidth       int    `json:"screen_width"`
		ScreenHeight      int    `json:"screen_height"`
		AudioRate         int    `json:"audio_rate"`
		AudioChannels     int    `json:"audio_channels"`
		VideoMt           string `json:"video_mt"`
		UpVideoMt         string `json:"up_video_mt"`
		DownVideoMt       string `json:"down_video_mt"`
		DownAudioMt       string `json:"down_audio_mt"`
		NoVideo           bool   `json:"no_video"`
		CallingTimeoutSec int    `json:"calling_timeout_sec"`
	}
	if err := json.Unmarshal([]byte(profileJSON), &media); err != nil {
		return fmt.Errorf("device %s profile parse error: %w", deviceID, err)
	}

	cts := media.CallingTimeoutSec
	sw, sh := media.ScreenWidth, media.ScreenHeight

	voipReq := tirtcapi.TokenWxvoipRequest{
		WxSessionKey:      m.SessionKey,
		WxRoomID:          m.RoomId,
		WxSessionToken:    m.ServerToken,
		WxAppID:           wxAppID,
		DeviceID:          deviceID,
		WxPayload:         m.Payload,
		WxModelID:         modelID,
		CallingTimeoutSec: &cts,
		UpVideoMt:         media.UpVideoMt,
		DownVideoMt:       media.DownVideoMt,
		DownAudioMt:       media.DownAudioMt,
		ScreenWidth:       &sw,
		ScreenHeight:      &sh,
		AudioRate:         media.AudioRate,
		AudioChannels:     media.AudioChannels,
	}
	// video_mt is a legacy compat field that sets both directions at once and must
	// not be combined with up_video_mt/down_video_mt (the cloud rejects the
	// request). Prefer the explicit up/down fields when the device reports them.
	if media.UpVideoMt == "" && media.DownVideoMt == "" {
		voipReq.VideoMt = media.VideoMt
	}
	if media.NoVideo {
		noVideo := true
		voipReq.NoVideo = &noVideo
	}

	slog.InfoContext(ctx, "voip notify calling tirtc token service",
		"device_id", deviceID,
		"room_id", m.RoomId,
		"audio_rate", media.AudioRate,
		"audio_channels", media.AudioChannels,
		"down_audio_mt", media.DownAudioMt,
		"up_video_mt", media.UpVideoMt,
		"down_video_mt", media.DownVideoMt,
		"no_video", media.NoVideo,
		"payload", string(m.Payload),
	)
	peerID, token, err := tirtcapi.PostTokenService(ctx, client, tirtcCfg.BaseURL, tirtcCfg.AccessID, tirtcCfg.AppID, tirtcCfg.SecretKey, voipReq)
	if err != nil {
		slog.ErrorContext(ctx, "voip notify tirtc token service failed", "device_id", deviceID, "err", err)
		return err
	}
	slog.InfoContext(ctx, "voip notify tirtc token service ok", "device_id", deviceID, "peer_id", peerID)

	remark, remarkErr := profiler.GetDeviceVoipContactRemark(ctx, deviceID, m.OpenID, wxAppID)
	if remarkErr != nil {
		slog.WarnContext(ctx, "voip notify contact remark lookup failed",
			"device_id", deviceID, "wx_open_id", m.OpenID, "wx_app_id", wxAppID, "err", remarkErr)
		remark = ""
	}
	push := map[string]any{
		"wx_app_id":        wxAppID,
		"wx_model_id":      modelID,
		"wx_room_id":       m.RoomId,
		"wx_user_openid":   m.OpenID,
		"wx_user_remark":   remark,
		"wx_user_nickname": remark,
		"wx_server_token":  m.ServerToken,
		"wx_session_key":   m.SessionKey,
		"wx_payload":       m.Payload,
		"peer_id":          peerID,
		"token":            token,
	}
	if p := m.PayloadData; p != nil {
		push["wx_call_id"] = p.ID
		push["wx_from"] = p.From
		push["wx_room_type"] = p.RoomType
	}
	pushJSON, _ := json.Marshal(push)
	slog.DebugContext(ctx, "voip notify sending call_incoming", "device_id", deviceID, "payload", string(pushJSON))

	topic := "device/sn_" + deviceID + "/cmd"
	envelope := map[string]any{"type": "call_incoming", "channel": "wx", "payload": push}
	return publisher.Publish(topic, 1, envelope)
}

const localVoipPrefix = "/v1/voip"

// proxyNotification 将原始微信回调请求原样转发给 endpoint，保留 query string。
// endpoint 已由 ProxyEndpointFor 处理好路径前缀替换，直接拼 /notification/... 部分即可。
func proxyNotification(c *gin.Context, endpoint string, body []byte) {
	reqURI := c.Request.URL.RequestURI()
	if strings.HasPrefix(reqURI, localVoipPrefix) {
		reqURI = reqURI[len(localVoipPrefix):]
	}
	target := strings.TrimRight(endpoint, "/") + reqURI
	slog.DebugContext(c.Request.Context(), "voip notify proxy req", "target", target, "body", string(body))
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "voip notify proxy build request failed", "err", err)
		c.JSON(200, gin.H{"errcode": ErrCodeWxaPushToDeviceFailed, "errmsg": "创建 VoIP 回调代理请求失败"})
		return
	}
	for k, vs := range c.Request.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "voip notify proxy request failed", "target", target, "err", err)
		c.JSON(200, gin.H{"errcode": ErrCodeWxaPushToDeviceFailed, "errmsg": "转发 VoIP 回调代理请求失败"})
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	slog.InfoContext(c.Request.Context(), "voip notify proxy done", "target", target, "status", resp.StatusCode)
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), rb)
}
