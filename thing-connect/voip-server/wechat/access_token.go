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

// ErrTokenRefreshInProgress means the stable endpoint still returns a token
// WeChat rejected and this instance may not force a refresh right now.
var ErrTokenRefreshInProgress = errors.New("wechat: token refresh in progress")

const rejectedTokenTTL = 2 * time.Hour

// Variables so tests can shorten the waits.
var (
	recoveryBackoffMin = time.Second
	recoveryBackoffMax = 5 * time.Second
	peerRefreshPolls   = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
)

// tokenState 每个 appID 一个锁，持锁期间进行 fetch，后续并发请求等待同一结果。
// 获取失败后在 retryAt 前直接返回 retryErr，使每个实例的 stable_token 读取有界。
type tokenState struct {
	lock       chan struct{}
	cache      cachedToken
	rejectedMu sync.Mutex
	rejected   map[string]time.Time
	retryAt    time.Time
	retryErr   error
	failures   int
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
// 它不会强制刷新：稳定接口仍返回已被拒绝的 token 时返回 ErrTokenRefreshInProgress。
func GetAccessToken(ctx context.Context, appID, secret string) (string, error) {
	if appID == "" || secret == "" {
		return "", fmt.Errorf("app_id and app_secret are required")
	}
	return getTokenState(appID, secret).token(ctx, appID, secret, "", nil)
}

// fetch performs one stable_token request; the caller holds the per-credential lock.
func (st *tokenState) fetch(ctx context.Context, appID, secret string, force bool) (cachedToken, error) {
	now := time.Now()
	body, err := json.Marshal(map[string]any{
		"grant_type":    "client_credential",
		"appid":         appID,
		"secret":        secret,
		"force_refresh": force,
	})
	if err != nil {
		return cachedToken{}, err
	}
	slog.DebugContext(ctx, "wechat get stable access_token req", "appid", appID, "force", force)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, stableTokenURL, bytes.NewReader(body))
	if err != nil {
		return cachedToken{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := tokenHTTPClient.Do(req)
	if err != nil {
		return cachedToken{}, err
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return cachedToken{}, err
	}
	var r accessTokenResp
	if err := json.Unmarshal(respBody, &r); err != nil {
		return cachedToken{}, fmt.Errorf("wechat response: %w", err)
	}
	if r.Errcode != 0 {
		slog.WarnContext(ctx, "wechat get access_token failed", "errcode", r.Errcode, "force", force)
		return cachedToken{}, &APIError{Errcode: r.Errcode, Errmsg: r.Errmsg}
	}
	if r.AccessToken == "" {
		return cachedToken{}, fmt.Errorf("wechat: empty access_token in response")
	}
	slog.DebugContext(ctx, "wechat get access_token resp", "status", res.StatusCode, "token_len", len(r.AccessToken))
	if r.ExpiresIn <= 0 {
		r.ExpiresIn = 7200
	}
	return cachedToken{token: r.AccessToken, expiresAt: now.Add(time.Duration(r.ExpiresIn) * time.Second)}, nil
}

// RefreshGate grants one forced refresh per application across VoIP instances
// within its window. A forced refresh invalidates the token other instances use.
type RefreshGate func(ctx context.Context, appID string) (bool, error)

// token returns a usable access_token. rejected, when set, is a token WeChat
// just refused: it is dropped from the cache and never accepted again until
// rejectedTokenTTL. A nil reserve never forces a refresh.
func (st *tokenState) token(ctx context.Context, appID, secret, rejected string, reserve RefreshGate) (string, error) {
	if rejected != "" {
		st.markRejected(rejected, time.Now())
	}
	select {
	case st.lock <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-st.lock }()
	now := time.Now()
	if st.cache.token != "" && now.Add(5*time.Minute).Before(st.cache.expiresAt) && !st.isRejected(st.cache.token, now) {
		return st.cache.token, nil
	}
	st.cache = cachedToken{}
	if now.Before(st.retryAt) {
		return "", st.retryErr
	}
	firstFailure := st.failures == 0
	fresh, err := st.fetch(ctx, appID, secret, false)
	if err != nil {
		return "", st.fail(ctx, err)
	}
	if !st.isRejected(fresh.token, now) {
		return st.accept(fresh), nil
	}
	// A peer may have renewed the token between rejection and the stable read.
	// Only rotate when the stable endpoint still supplies a rejected token.
	if reserve == nil {
		return "", st.fail(ctx, ErrTokenRefreshInProgress)
	}
	granted, err := reserve(ctx, appID)
	if err != nil {
		return "", st.fail(ctx, fmt.Errorf("wechat: reserve forced token refresh: %w", err))
	}
	if granted {
		slog.WarnContext(ctx, "wechat forcing access_token refresh", "appid", appID)
		fresh, err = st.fetch(ctx, appID, secret, true)
		if err != nil {
			// WeChat may have rotated anyway; later stable reads accept a new token.
			slog.WarnContext(ctx, "wechat forced access_token refresh failed", "appid", appID, "error", err)
			return "", st.fail(ctx, err)
		}
		if st.isRejected(fresh.token, now) {
			return "", st.fail(ctx, ErrTokenRefreshInProgress)
		}
		return st.accept(fresh), nil
	}
	// Wait briefly for the peer's forced refresh only once per failure streak;
	// later attempts read stable_token at most once per backoff interval.
	if firstFailure {
		for _, delay := range peerRefreshPolls {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
			fresh, err = st.fetch(ctx, appID, secret, false)
			if err != nil {
				return "", st.fail(ctx, err)
			}
			if !st.isRejected(fresh.token, time.Now()) {
				return st.accept(fresh), nil
			}
		}
	}
	return "", st.fail(ctx, ErrTokenRefreshInProgress)
}

func (st *tokenState) accept(fresh cachedToken) string {
	st.cache = fresh
	st.failures = 0
	st.retryAt = time.Time{}
	st.retryErr = nil
	return fresh.token
}

// fail starts or extends the exponential backoff; caller cancellation does not.
func (st *tokenState) fail(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return err
	}
	delay := recoveryBackoffMax
	if st.failures < 16 {
		delay = min(recoveryBackoffMin<<st.failures, recoveryBackoffMax)
		st.failures++
	}
	st.retryAt = time.Now().Add(delay)
	st.retryErr = err
	return err
}

func (st *tokenState) markRejected(token string, now time.Time) {
	st.rejectedMu.Lock()
	defer st.rejectedMu.Unlock()
	if st.rejected == nil {
		st.rejected = map[string]time.Time{}
	}
	for t, until := range st.rejected {
		if !now.Before(until) {
			delete(st.rejected, t)
		}
	}
	st.rejected[token] = now.Add(rejectedTokenTTL)
}

func (st *tokenState) isRejected(token string, now time.Time) bool {
	st.rejectedMu.Lock()
	defer st.rejectedMu.Unlock()
	until, ok := st.rejected[token]
	return ok && now.Before(until)
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
	if appID == "" || secret == "" {
		return fmt.Errorf("app_id and app_secret are required")
	}
	st := getTokenState(appID, secret)
	token, err := st.token(ctx, appID, secret, "", reserve)
	if err != nil {
		return err
	}
	err = call(token)
	if !tokenRejected(err) {
		return err
	}
	slog.WarnContext(ctx, "wechat rejected cached access_token, refetching", "appid", appID)
	token, err = st.token(ctx, appID, secret, token, reserve)
	if err != nil {
		return err
	}
	err = call(token)
	if tokenRejected(err) {
		st.markRejected(token, time.Now())
	}
	return err
}
