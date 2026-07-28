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

// TokenWxvoipRequest POST /v1/token/wxvoip 的 JSON 体。
type TokenWxvoipRequest struct {
	WxSessionKey      string `json:"wx_session_key"`
	WxRoomID          string `json:"wx_room_id"`
	WxSessionToken    string `json:"wx_session_token"`
	WxAppID           string `json:"wx_app_id"`
	DeviceID          string `json:"device_id"`
	WxPayload         string `json:"wx_payload,omitempty"`
	WxModelID         string `json:"wx_model_id"`
	CallingTimeoutSec *int   `json:"calling_timeout_sec,omitempty"`
	NoVideo           *bool  `json:"no_video,omitempty"`
	VideoMt           string `json:"video_mt,omitempty"`
	UpVideoMt         string `json:"up_video_mt,omitempty"`
	DownVideoMt       string `json:"down_video_mt,omitempty"`
	DownAudioMt       string `json:"down_audio_mt,omitempty"`
	ScreenWidth       *int   `json:"screen_width,omitempty"`
	ScreenHeight      *int   `json:"screen_height,omitempty"`
	AudioRate         int    `json:"audio_rate"`
	AudioChannels     int    `json:"audio_channels"`
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

