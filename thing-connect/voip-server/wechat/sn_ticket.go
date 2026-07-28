package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type snTicketResp struct {
	SnTicket string `json:"sn_ticket"`
	Errcode  int    `json:"errcode"`
	Errmsg   string `json:"errmsg"`
}

// GetSnTicket 调用微信 getsnticket 获取设备票据。
func GetSnTicket(ctx context.Context, accessToken, sn, modelID string) (string, error) {
	if accessToken == "" {
		return "", fmt.Errorf("access_token is required")
	}
	if sn == "" {
		return "", fmt.Errorf("sn is required")
	}
	if modelID == "" {
		return "", fmt.Errorf("model_id is required")
	}
	urlStr := fmt.Sprintf("https://api.weixin.qq.com/wxa/getsnticket?access_token=%s", accessToken)
	body, err := json.Marshal(map[string]string{"sn": sn, "model_id": modelID})
	if err != nil {
		return "", err
	}
	slog.DebugContext(ctx, "wechat getsnticket req", "url", urlStr, "body", string(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	var r snTicketResp
	if err := json.Unmarshal(respBody, &r); err != nil {
		return "", fmt.Errorf("wechat response: %w", err)
	}
	if r.Errcode != 0 {
		slog.WarnContext(ctx, "wechat getsnticket failed", "errcode", r.Errcode, "errmsg", r.Errmsg)
		return "", fmt.Errorf("wechat err %d: %s", r.Errcode, r.Errmsg)
	}
	if r.SnTicket == "" {
		return "", fmt.Errorf("wechat: empty sn_ticket in response")
	}
	slog.DebugContext(ctx, "wechat getsnticket resp", "status", res.StatusCode, "body", string(respBody))
	return r.SnTicket, nil
}
