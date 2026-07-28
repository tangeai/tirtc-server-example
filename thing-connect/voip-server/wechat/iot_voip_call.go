package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type iotVoipCallResp struct {
	Errcode int    `json:"errcode"`
	Errmsg  string `json:"errmsg"`
}

// APIError preserves the WeChat business error code so callers can apply
// targeted recovery (notably errcode=9 for an invalid device authorization).
type APIError struct {
	Errcode int
	Errmsg  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("wechat err %d: %s", e.Errcode, e.Errmsg)
}

func parseIotVoipCallResponse(statusCode int, body []byte) error {
	var r iotVoipCallResp
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("wechat response: %w", err)
	}
	if r.Errcode != 0 {
		return &APIError{Errcode: r.Errcode, Errmsg: r.Errmsg}
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("wechat HTTP %d: %s", statusCode, string(body))
	}
	return nil
}

// IotVoipCall 调用微信 iot/voip/call，由 App 端触发呼叫设备。
func IotVoipCall(ctx context.Context, accessToken string, payload map[string]any) error {
	if accessToken == "" {
		return fmt.Errorf("access_token is required")
	}
	urlStr := fmt.Sprintf("https://api.weixin.qq.com/wxa/business/iot/voip/call?access_token=%s", accessToken)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "wechat iot/voip/call req", "url", urlStr, "body", string(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "wechat iot/voip/call network error", "err", err)
		return err
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "wechat iot/voip/call resp", "status", res.StatusCode, "body", string(respBody))
	if err := parseIotVoipCallResponse(res.StatusCode, respBody); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			slog.WarnContext(ctx, "wechat iot/voip/call errcode",
				"errcode", apiErr.Errcode, "errmsg", apiErr.Errmsg)
		}
		return err
	}
	slog.InfoContext(ctx, "wechat iot/voip/call ok", "openid", payload["openid"])
	return nil
}
