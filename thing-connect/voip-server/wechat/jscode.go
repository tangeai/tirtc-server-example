package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const jscode2sessionURL = "https://api.weixin.qq.com/sns/jscode2session"

type jscode2sessionResp struct {
	Openid     string `json:"openid"`
	SessionKey string `json:"session_key"`
	Unionid    string `json:"unionid"`
	Errcode    int    `json:"errcode"`
	Errmsg     string `json:"errmsg"`
}

// Jscode2session 调用 jscode2session 返回 openid。
func Jscode2session(ctx context.Context, appID, secret, code string) (openid string, err error) {
	if appID == "" || secret == "" || code == "" {
		return "", fmt.Errorf("app_id, app_secret, code are required")
	}
	u, err := url.Parse(jscode2sessionURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("appid", appID)
	q.Set("secret", secret)
	q.Set("js_code", code)
	q.Set("grant_type", "authorization_code")
	u.RawQuery = q.Encode()
	slog.DebugContext(ctx, "wechat jscode2session req", "appid", appID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	var r jscode2sessionResp
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("wechat response: %w", err)
	}
	if r.Errcode != 0 {
		slog.WarnContext(ctx, "wechat jscode2session failed", "errcode", r.Errcode, "errmsg", r.Errmsg)
		return "", fmt.Errorf("wechat err %d: %s", r.Errcode, r.Errmsg)
	}
	if r.Openid == "" {
		return "", fmt.Errorf("wechat: empty openid in response")
	}
	slog.DebugContext(ctx, "wechat jscode2session resp", "status", res.StatusCode, "openid", r.Openid)
	return r.Openid, nil
}
