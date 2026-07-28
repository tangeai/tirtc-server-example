package wechat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const clientCredentialTokenURL = "https://api.weixin.qq.com/cgi-bin/token"

type accessTokenResp struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	Errcode     int    `json:"errcode"`
	Errmsg      string `json:"errmsg"`
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// tokenState 每个 appID 一个锁，持锁期间进行 fetch，后续并发请求等待同一结果。
type tokenState struct {
	mu    sync.Mutex
	cache cachedToken
}

var (
	tokensMu sync.Mutex
	tokens   = map[string]*tokenState{}
)

func tokenCacheKey(appID, secret string) string {
	h := sha256.Sum256([]byte(appID + "\x00" + secret))
	return hex.EncodeToString(h[:16])
}

func getTokenState(appID, secret string) *tokenState {
	key := tokenCacheKey(appID, secret)
	tokensMu.Lock()
	defer tokensMu.Unlock()
	if s, ok := tokens[key]; ok {
		return s
	}
	s := &tokenState{}
	tokens[key] = s
	return s
}

// GetAccessToken 获取小程序 access_token（client_credential），带内存缓存。
// 同一 appID 并发调用时只发出一个 HTTP 请求，其余等待同一结果（per-appID 互斥锁）。
func GetAccessToken(ctx context.Context, appID, secret string) (string, error) {
	if appID == "" || secret == "" {
		return "", fmt.Errorf("app_id and app_secret are required")
	}

	st := getTokenState(appID, secret)
	st.mu.Lock()
	defer st.mu.Unlock()

	now := time.Now()
	if st.cache.token != "" && now.Add(5*time.Minute).Before(st.cache.expiresAt) {
		return st.cache.token, nil
	}

	u, err := url.Parse(clientCredentialTokenURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("grant_type", "client_credential")
	q.Set("appid", appID)
	q.Set("secret", secret)
	u.RawQuery = q.Encode()

	slog.DebugContext(ctx, "wechat get access_token req", "url", u.String())
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
	var r accessTokenResp
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("wechat response: %w", err)
	}
	if r.Errcode != 0 {
		slog.WarnContext(ctx, "wechat get access_token failed", "errcode", r.Errcode, "errmsg", r.Errmsg)
		return "", fmt.Errorf("wechat err %d: %s", r.Errcode, r.Errmsg)
	}
	if r.AccessToken == "" {
		return "", fmt.Errorf("wechat: empty access_token in response")
	}
	slog.DebugContext(ctx, "wechat get access_token resp", "status", res.StatusCode, "token_len", len(r.AccessToken))
	if r.ExpiresIn <= 0 {
		r.ExpiresIn = 7200
	}
	st.cache = cachedToken{
		token:     r.AccessToken,
		expiresAt: now.Add(time.Duration(r.ExpiresIn) * time.Second),
	}
	return st.cache.token, nil
}
