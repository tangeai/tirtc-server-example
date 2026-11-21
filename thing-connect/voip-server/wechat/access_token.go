package wechat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// stableTokenURL is a variable so tests can point it at a fake WeChat server.
//
// Every voip-server instance must use stable_token without force_refresh:
// within its validity window WeChat returns the same token to all callers,
// while client_credential /cgi-bin/token issues a new token per call and
// invalidates the previous one, so instances would revoke each other's tokens.
var stableTokenURL = "https://api.weixin.qq.com/cgi-bin/stable_token"

var tokenHTTPClient = &http.Client{Timeout: 10 * time.Second}

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

// GetAccessToken 获取小程序稳定版 access_token，带内存缓存。
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

	body, err := json.Marshal(map[string]any{
		"grant_type":    "client_credential",
		"appid":         appID,
		"secret":        secret,
		"force_refresh": false,
	})
	if err != nil {
		return "", err
	}
	slog.DebugContext(ctx, "wechat get stable access_token req", "appid", appID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, stableTokenURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := tokenHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	var r accessTokenResp
	if err := json.Unmarshal(respBody, &r); err != nil {
		return "", fmt.Errorf("wechat response: %w", err)
	}
	if r.Errcode != 0 {
		slog.WarnContext(ctx, "wechat get access_token failed", "errcode", r.Errcode, "errmsg", r.Errmsg)
		return "", &APIError{Errcode: r.Errcode, Errmsg: r.Errmsg}
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

// invalidateAccessToken drops the cached token only if it is still the one
// WeChat rejected, so a concurrent caller's fresher token survives.
func invalidateAccessToken(appID, secret, rejected string) {
	st := getTokenState(appID, secret)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.cache.token == rejected {
		st.cache = cachedToken{}
	}
}

// tokenRejected reports WeChat errors meaning the access_token itself is
// invalid or expired: 40001 invalid credential, 40014 invalid access_token,
// 42001 access_token expired.
func tokenRejected(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Errcode {
	case 40001, 40014, 42001:
		return true
	}
	return false
}

// CallWithAccessToken runs call with the cached access_token. When WeChat
// rejects that token it is dropped and call runs once more with a freshly
// fetched one. The refetch never forces a refresh, which would invalidate the
// token every other voip-server instance is using.
func CallWithAccessToken(ctx context.Context, appID, secret string, call func(accessToken string) error) error {
	token, err := GetAccessToken(ctx, appID, secret)
	if err != nil {
		return err
	}
	err = call(token)
	if !tokenRejected(err) {
		return err
	}
	slog.WarnContext(ctx, "wechat rejected cached access_token, refetching", "appid", appID, "err", err)
	invalidateAccessToken(appID, secret, token)
	token, err = GetAccessToken(ctx, appID, secret)
	if err != nil {
		return err
	}
	return call(token)
}
