package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	gocache "github.com/patrickmn/go-cache"
)

func makeJWT(t *testing.T, deviceID, secret string, expiry time.Duration) string {
	t.Helper()
	claims := jwt.MapClaims{
		"device_id": deviceID,
		"exp":       time.Now().Add(expiry).Unix(),
		"iat":       time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("makeJWT: %v", err)
	}
	return signed
}

func newTestServer(fn fetchFn) *Server {
	return &Server{
		jwtSecret:     "test-secret",
		defaultRoleID: "fin63bby1og0",
		cache:         gocache.New(60*time.Second, 120*time.Second),
		fetch:         fn,
	}
}

func TestGetAIToken_MissingAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := newTestServer(nil)
	r := gin.New()
	srv.Register(r)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/ai/token", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestGetAIToken_InvalidJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := newTestServer(nil)
	r := gin.New()
	srv.Register(r)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/ai/token", nil)
	req.Header.Set("Authorization", "Bearer invalid.jwt.token")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d: body=%s", w.Code, w.Body.String())
	}
}

func TestGetAIToken_ExpiredJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := newTestServer(nil)
	r := gin.New()
	srv.Register(r)

	tok := makeJWT(t, "dev1", "test-secret", -1*time.Hour)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/ai/token", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestGetAIToken_UpstreamBusinessError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := newTestServer(func(_ context.Context, _, _ string) (string, string, int32, string, error) {
		return "", "", 10023, "device not found", nil
	})
	r := gin.New()
	srv.Register(r)

	tok := makeJWT(t, "dev1", "test-secret", time.Hour)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/ai/token", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != 10023 || resp.Msg != "获取 AI 会话凭证失败：AI 云服务返回错误" {
		t.Errorf("want unchanged code=10023 and sanitized Chinese message, got code=%d msg=%s", resp.Code, resp.Msg)
	}
}

func TestGetAIToken_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	calls := 0
	srv := newTestServer(func(_ context.Context, _, _ string) (string, string, int32, string, error) {
		calls++
		return "peer1", "tok1", 0, "", nil
	})
	r := gin.New()
	srv.Register(r)

	tok := makeJWT(t, "dev1", "test-secret", time.Hour)

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/ai/token", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d body=%s", i+1, w.Code, w.Body.String())
		}
		var resp struct {
			Code int `json:"code"`
			Data struct {
				PeerID string `json:"peer_id"`
				Token  string `json:"token"`
				RoleID string `json:"role_id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.PeerID != "peer1" || resp.Data.Token != "tok1" || resp.Data.RoleID != "fin63bby1og0" {
			t.Errorf("request %d: unexpected data %+v", i+1, resp.Data)
		}
	}
	// Second request should hit cache — fetch called only once
	if calls != 1 {
		t.Errorf("want fetch called once (cache hit on 2nd), got %d calls", calls)
	}
}
