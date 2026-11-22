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

func TestAITokenRejectsTemporaryCredential(t *testing.T) {
	calls := 0
	srv := newTestServer(func(context.Context, string, string) (string, string, int32, string, error) {
		calls++
		return "peer", "token", 0, "", nil
	})
	r := gin.New()
	srv.Register(r)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"device_id": "tmp_a1b2c3d4", "clientid": "tmp_a1b2c3d4", "exp": time.Now().Add(time.Minute).Unix()})
	signed, _ := tok.SignedString([]byte("test-secret"))
	req := httptest.NewRequest("GET", "/v1/ai/token", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if calls != 0 || w.Code != http.StatusUnauthorized {
		t.Fatalf("temporary device JWT was not rejected; status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestAITokenOldFetchCannotPopulateNewRuntimeCache(t *testing.T) {
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	srv := newTestServer(func(context.Context, string, string) (string, string, int32, string, error) {
		close(started)
		<-release
		return "old-peer", "old-token", 0, "", nil
	})
	r := gin.New()
	srv.Register(r)
	signed := makeJWT(t, "dev1", "test-secret", time.Hour)
	request := func() {
		req := httptest.NewRequest("GET", "/v1/ai/token", nil)
		req.Header.Set("Authorization", "Bearer "+signed)
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	go func() { defer close(done); request() }()
	<-started
	srv.UpdateRuntime("fin63bby1og0", "http://127.0.0.1:1", "new-access", "new-app", "new-secret")
	newCalls := 0
	srv.mu.Lock()
	srv.fetch = func(context.Context, string, string) (string, string, int32, string, error) {
		newCalls++
		return "new-peer", "new-token", 0, "", nil
	}
	srv.mu.Unlock()
	close(release)
	<-done
	if srv.cache.ItemCount() != 0 {
		t.Fatal("old runtime repopulated flushed cache")
	}
	request()
	request()
	if newCalls != 1 {
		t.Fatalf("new runtime calls=%d, want one fetch and one cache hit", newCalls)
	}
}

func TestAITokenRejectsInvalidDeviceClaims(t *testing.T) {
	for _, name := range []string{"missing-expiry", "user-token", "mixed-identity", "wrong-method"} {
		t.Run(name, func(t *testing.T) {
			srv := newTestServer(func(context.Context, string, string) (string, string, int32, string, error) {
				t.Error("invalid identity reached upstream")
				return "", "", 0, "", nil
			})
			claims := jwt.MapClaims{"device_id": "dev1", "exp": time.Now().Add(time.Hour).Unix()}
			method := jwt.SigningMethodHS256
			switch name {
			case "missing-expiry":
				delete(claims, "exp")
			case "user-token":
				delete(claims, "device_id")
				claims["user_id"] = 1
			case "mixed-identity":
				claims["user_id"] = 1
			case "wrong-method":
				method = jwt.SigningMethodHS384
			}
			token, err := jwt.NewWithClaims(method, claims).SignedString([]byte("test-secret"))
			if err != nil {
				t.Fatal(err)
			}
			r := gin.New()
			srv.Register(r)
			req := httptest.NewRequest("GET", "/v1/ai/token", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
