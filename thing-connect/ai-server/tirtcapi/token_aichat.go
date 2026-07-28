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

const tokenAichatPath = "/v1/token/aichat"

type aichatRequest struct {
	DeviceID string `json:"device_id"`
	RoleID   string `json:"role_id"`
}

type aichatServiceResp struct {
	Code int32           `json:"code"`
	Msg  string          `json:"msg,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

type aichatServiceData struct {
	PeerID string `json:"peer_id"`
	Token  string `json:"token"`
}

// PostTokenAichat calls POST /v1/token/aichat.
// On upstream business error (code != 0): returns "", "", upstreamCode, upstreamMsg, nil.
// On network/parse error: returns "", "", 0, "", err.
func PostTokenAichat(ctx context.Context, client *http.Client, baseURL, accessKeyID, appID, secretKeyID, deviceID, roleID string) (peerID, token string, upstreamCode int32, upstreamMsg string, err error) {
	start := time.Now()
	defer func() {
		durMs := time.Since(start).Milliseconds()
		if err != nil {
			slog.WarnContext(ctx, "aichat PostTokenAichat failed", "device", deviceID, "dur_ms", durMs, "err", err)
		} else if upstreamCode != 0 {
			slog.WarnContext(ctx, "aichat PostTokenAichat upstream error", "device", deviceID, "dur_ms", durMs, "upstream_code", upstreamCode, "msg", upstreamMsg)
		} else {
			slog.InfoContext(ctx, "aichat PostTokenAichat ok", "device", deviceID, "dur_ms", durMs)
		}
	}()

	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", "", 0, "", fmt.Errorf("aichat: base_url is empty")
	}
	if strings.TrimSpace(accessKeyID) == "" {
		return "", "", 0, "", fmt.Errorf("aichat: access_key_id is empty")
	}
	if strings.TrimSpace(appID) == "" {
		return "", "", 0, "", fmt.Errorf("aichat: app_id is empty")
	}
	if strings.TrimSpace(secretKeyID) == "" {
		return "", "", 0, "", fmt.Errorf("aichat: secret_key_id is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}

	body, err := json.Marshal(aichatRequest{DeviceID: deviceID, RoleID: roleID})
	if err != nil {
		return "", "", 0, "", fmt.Errorf("aichat: marshal request: %w", err)
	}

	urlStr := baseURL + tokenAichatPath
	signing := time.Now().UTC().Truncate(time.Second)
	hdr := internaltirtcapi.SignTGV1Request(secretKeyID, accessKeyID, appID, http.MethodPost, tokenAichatPath, "", body, "", signing)

	slog.DebugContext(ctx, "aichat PostTokenAichat req", "url", urlStr, "device", deviceID, "role", roleID, "body", string(body))
	reqHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(body))
	if err != nil {
		return "", "", 0, "", err
	}
	for k, vs := range hdr {
		if len(vs) > 0 {
			reqHTTP.Header.Set(k, vs[0])
		}
	}

	res, err := client.Do(reqHTTP)
	if err != nil {
		return "", "", 0, "", err
	}
	defer res.Body.Close()
	rb, err := io.ReadAll(res.Body)
	if err != nil {
		return "", "", 0, "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", "", 0, "", fmt.Errorf("aichat: HTTP %d: %s", res.StatusCode, string(rb))
	}

	var wrap aichatServiceResp
	if err := json.Unmarshal(rb, &wrap); err != nil {
		return "", "", 0, "", fmt.Errorf("aichat: parse response: %w", err)
	}
	if wrap.Code != 0 {
		return "", "", wrap.Code, wrap.Msg, nil
	}

	var data aichatServiceData
	if err := json.Unmarshal(wrap.Data, &data); err != nil {
		return "", "", 0, "", fmt.Errorf("aichat: parse data: %w", err)
	}
	if strings.TrimSpace(data.PeerID) == "" || strings.TrimSpace(data.Token) == "" {
		return "", "", 0, "", fmt.Errorf("aichat: empty peer_id or token in response")
	}
	return data.PeerID, data.Token, 0, "", nil
}
