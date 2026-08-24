// Package tirtcapi 调用 tirtc-server-api（TGV1-HMAC-SHA256 鉴权）。
package tirtcapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	internaltirtcapi "thing-connect/internal/tirtcapi"
)

type tokenServiceResp struct {
	Code int32           `json:"code"`
	Msg  string          `json:"msg,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

type tokenServiceData struct {
	PeerID string `json:"peer_id"`
	Token  string `json:"token"`
}

const tokenWxvoipPath = "/v1/token/wxvoip"

// TokenWxvoipRequest combines the device-owned media profile with the
// server-owned fields required by POST /v1/token/wxvoip.
//
// Profile fields are forwarded without being enumerated here so TiRTC can add
// media parameters without requiring a matching voip-server release. Fields
// used only by the mini-program UI are removed, and server-owned fields always
// override values supplied by a device profile.
type TokenWxvoipRequest struct {
	Profile        json.RawMessage `json:"-"`
	WxSessionKey   string          `json:"wx_session_key"`
	WxRoomID       string          `json:"wx_room_id"`
	WxSessionToken string          `json:"wx_session_token"`
	WxAppID        string          `json:"wx_app_id"`
	DeviceID       string          `json:"device_id"`
	WxPayload      string          `json:"wx_payload,omitempty"`
	WxModelID      string          `json:"wx_model_id"`
}

var localProfileFields = [...]string{
	"camera_rotation",
	"aspect_ratio",
	"hor_mirror",
	"vert_mirror",
	"object_fit",
}

var serverOwnedTokenFields = [...]string{
	"wx_session_key",
	"wx_room_id",
	"wx_session_token",
	"wx_app_id",
	"device_id",
	"wx_payload",
	"wx_model_id",
}

type tokenWxvoipServerFields struct {
	WxSessionKey   string `json:"wx_session_key"`
	WxRoomID       string `json:"wx_room_id"`
	WxSessionToken string `json:"wx_session_token"`
	WxAppID        string `json:"wx_app_id"`
	DeviceID       string `json:"device_id"`
	WxPayload      string `json:"wx_payload,omitempty"`
	WxModelID      string `json:"wx_model_id"`
}

// MarshalJSON performs a controlled top-level merge. Profile values retain
// their original JSON types; only local UI fields are filtered and call-scoped
// server fields are authoritative.
func (r TokenWxvoipRequest) MarshalJSON() ([]byte, error) {
	params := make(map[string]json.RawMessage)
	profile := bytes.TrimSpace(r.Profile)
	if len(profile) > 0 {
		if err := json.Unmarshal(profile, &params); err != nil {
			return nil, fmt.Errorf("profile must be a JSON object: %w", err)
		}
		if params == nil {
			return nil, fmt.Errorf("profile must be a JSON object")
		}
	}
	normalizeLegacyVideoFields(params)

	for _, name := range localProfileFields {
		delete(params, name)
	}
	for _, name := range serverOwnedTokenFields {
		delete(params, name)
	}

	serverBody, err := json.Marshal(tokenWxvoipServerFields{
		WxSessionKey:   r.WxSessionKey,
		WxRoomID:       r.WxRoomID,
		WxSessionToken: r.WxSessionToken,
		WxAppID:        r.WxAppID,
		DeviceID:       r.DeviceID,
		WxPayload:      r.WxPayload,
		WxModelID:      r.WxModelID,
	})
	if err != nil {
		return nil, err
	}
	var serverFields map[string]json.RawMessage
	if err := json.Unmarshal(serverBody, &serverFields); err != nil {
		return nil, err
	}
	for name, value := range serverFields {
		params[name] = value
	}
	return json.Marshal(params)
}

// normalizeLegacyVideoFields preserves the historical video_mt compatibility
// rule while allowing all other profile fields to pass through. Empty
// directional fields were omitted by the previous typed request structure;
// non-empty directional fields take precedence over video_mt because TiRTC
// rejects requests that combine both forms.
func normalizeLegacyVideoFields(params map[string]json.RawMessage) {
	for _, name := range []string{"up_video_mt", "down_video_mt"} {
		raw, ok := params[name]
		if !ok {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			delete(params, name)
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil && value == "" {
			delete(params, name)
		}
	}
	if _, ok := params["up_video_mt"]; ok {
		delete(params, "video_mt")
		return
	}
	if _, ok := params["down_video_mt"]; ok {
		delete(params, "video_mt")
	}
}

// PostTokenService 向 tirtc-server-api 发起 POST /v1/token/wxvoip。
func PostTokenService(ctx context.Context, client *http.Client, baseURL, accessID, appID, secretKey string, req TokenWxvoipRequest) (peerID, token string, err error) {
	start := time.Now()
	var urlStr string
	defer func() {
		durMs := time.Since(start).Milliseconds()
		if err != nil {
			slog.WarnContext(ctx, "post token service failed", "url", urlStr, "dur_ms", durMs, "err", err)
		} else {
			slog.InfoContext(ctx, "post token service ok", "url", urlStr, "dur_ms", durMs)
		}
	}()

	body, err := json.Marshal(req)
	if err != nil {
		return "", "", fmt.Errorf("marshal token wxvoip request: %w", err)
	}

	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", "", fmt.Errorf("tirtc server api base_url is empty")
	}
	if strings.TrimSpace(accessID) == "" || strings.TrimSpace(secretKey) == "" {
		return "", "", fmt.Errorf("tirtc access_id or secret_key is empty")
	}
	if strings.TrimSpace(appID) == "" {
		return "", "", fmt.Errorf("tirtc app_id is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}

	path := tokenWxvoipPath
	urlStr = baseURL + path

	signing := time.Now().UTC().Truncate(time.Second)
	hdr := internaltirtcapi.SignTGV1Request(secretKey, accessID, appID, http.MethodPost, path, "", body, "", signing)

	reqHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	for k, vs := range hdr {
		if len(vs) > 0 {
			reqHTTP.Header.Set(k, vs[0])
		}
	}

	// 打印完整 curl 命令，方便手动复现
	var curlHdrs strings.Builder
	for k, vs := range reqHTTP.Header {
		if len(vs) > 0 {
			curlHdrs.WriteString(fmt.Sprintf(" -H '%s: %s'", k, vs[0]))
		}
	}
	slog.DebugContext(ctx, "post token service req", "url", urlStr, "curl_headers", curlHdrs.String(), "body", string(body))

	res, err := client.Do(reqHTTP)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	rb, err := io.ReadAll(res.Body)
	if err != nil {
		return "", "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("tirtc-server-api: HTTP %d: %s", res.StatusCode, string(rb))
	}

	var wrap tokenServiceResp
	if err := json.Unmarshal(rb, &wrap); err != nil {
		return "", "", fmt.Errorf("parse response: %w", err)
	}
	if wrap.Code != 0 {
		return "", "", fmt.Errorf("tirtc-server-api: code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var data tokenServiceData
	if err := json.Unmarshal(wrap.Data, &data); err != nil {
		return "", "", fmt.Errorf("parse data: %w", err)
	}
	if strings.TrimSpace(data.PeerID) == "" || strings.TrimSpace(data.Token) == "" {
		return "", "", fmt.Errorf("tirtc-server-api: empty peer_id or token in data")
	}
	return data.PeerID, data.Token, nil
}
