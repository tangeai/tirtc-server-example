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
	lock  chan struct{}
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
	s := &tokenState{lock: make(chan struct{}, 1)}
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
	select {
	case st.lock <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-st.lock }()
	if st.cache.token != "" && time.Now().Add(5*time.Minute).Before(st.cache.expiresAt) {
		return st.cache.token, nil
	}
	return st.fetch(ctx, appID, secret, false)
}

// fetch updates the cache while the caller holds the per-credential lock.
func (st *tokenState) fetch(ctx context.Context, appID, secret string, force bool) (string, error) {
	now := time.Now()
	body, err := json.Marshal(map[string]any{
		"grant_type":    "client_credential",
		"appid":         appID,
		"secret":        secret,
		"force_refresh": force,
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
	respBody, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return "", err
	}
	var r accessTokenResp
	if err := json.Unmarshal(respBody, &r); err != nil {
		return "", fmt.Errorf("wechat response: %w", err)
	}
	if r.Errcode != 0 {
		slog.WarnContext(ctx, "wechat get access_token failed", "errcode", r.Errcode)
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

// RefreshGate grants one forced refresh per application across VoIP instances.
type RefreshGate func(ctx context.Context, appID string) (bool, error)

func (st *tokenState) replacementToken(ctx context.Context, appID, secret, rejected string, reserve RefreshGate) (string, error) {
	select {
	case st.lock <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-st.lock }()
	if st.cache.token != "" && st.cache.token != rejected && time.Now().Add(5*time.Minute).Before(st.cache.expiresAt) {
		return st.cache.token, nil
	}
	st.cache = cachedToken{}
	token, err := st.fetch(ctx, appID, secret, false)
	if err != nil || token != rejected {
		return token, err
	}
	// A peer may have renewed the token between rejection and the stable read.
	// Only rotate when the stable endpoint still supplies the rejected token.
	granted, err := reserve(ctx, appID)
	if err != nil {
		return "", err
	}
	if granted {
		return st.fetch(ctx, appID, secret, true)
	}
	for delay := 100 * time.Millisecond; delay <= 800*time.Millisecond; delay *= 2 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
		token, err = st.fetch(ctx, appID, secret, false)
		if err != nil || token != rejected {
			return token, err
		}
	}
	return "", errors.New("wechat: token refresh in progress")
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

// CallWithAccessToken retries the business call once after a token rejection.
func CallWithAccessToken(ctx context.Context, appID, secret string, reserve RefreshGate, call func(accessToken string) error) error {
	token, err := GetAccessToken(ctx, appID, secret)
	if err != nil {
		return err
	}
	err = call(token)
	if !tokenRejected(err) {
		return err
	}
	slog.WarnContext(ctx, "wechat rejected cached access_token, refetching", "appid", appID)
	token, err = getTokenState(appID, secret).replacementToken(ctx, appID, secret, token, reserve)
	if err != nil {
		return err
	}
	return call(token)
}
