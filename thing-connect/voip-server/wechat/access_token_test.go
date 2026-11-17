package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

type fakeStableTokenServer struct {
	issued  atomic.Int32
	mu      sync.Mutex
	token   string
	forced  atomic.Int32
	errcode int
}

func (f *fakeStableTokenServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GrantType    string `json:"grant_type"`
		AppID        string `json:"appid"`
		Secret       string `json:"secret"`
		ForceRefresh bool   `json:"force_refresh"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil ||
		req.GrantType != "client_credential" || req.AppID == "" || req.Secret == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if r.URL.RawQuery != "" {
		http.Error(w, "credentials must not be in the URL", http.StatusBadRequest)
		return
	}
	if req.ForceRefresh {
		f.forced.Add(1)
	}
	f.issued.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errcode != 0 {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": f.errcode, "errmsg": "denied"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": f.token, "expires_in": 7200})
}

func (f *fakeStableTokenServer) setToken(token string) {
	f.mu.Lock()
	f.token = token
	f.mu.Unlock()
}

func useFakeStableToken(t *testing.T, token string) *fakeStableTokenServer {
	t.Helper()
	fake := &fakeStableTokenServer{token: token}
	server := httptest.NewServer(fake)
	previousURL := stableTokenURL
	stableTokenURL = server.URL
	t.Cleanup(func() {
		stableTokenURL = previousURL
		server.Close()
		tokensMu.Lock()
		tokens = map[string]*tokenState{}
		tokensMu.Unlock()
	})
	return fake
}

func TestGetAccessTokenUsesStableTokenWithoutForceRefresh(t *testing.T) {
	fake := useFakeStableToken(t, "stable-1")
	var wg sync.WaitGroup
	results := make([]string, 16)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token, err := GetAccessToken(context.Background(), "wx-app", "secret")
			if err != nil {
				t.Errorf("GetAccessToken: %v", err)
			}
			results[i] = token
		}(i)
	}
	wg.Wait()
	for _, token := range results {
		if token != "stable-1" {
			t.Fatalf("token=%q, want stable-1", token)
		}
	}
	if got := fake.issued.Load(); got != 1 {
		t.Fatalf("token requests=%d, want 1", got)
	}
	if got := fake.forced.Load(); got != 0 {
		t.Fatalf("force_refresh requests=%d, want 0", got)
	}
}

func TestGetAccessTokenReturnsAPIError(t *testing.T) {
	fake := useFakeStableToken(t, "")
	fake.errcode = 40013
	_, err := GetAccessToken(context.Background(), "wx-app", "secret")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Errcode != 40013 {
		t.Fatalf("err=%v, want APIError 40013", err)
	}
}

func TestCallWithAccessTokenRefetchesOnceWhenTokenRejected(t *testing.T) {
	for _, code := range []int{40001, 40014, 42001} {
		t.Run(fmt.Sprint(code), func(t *testing.T) { testRefetchOnRejection(t, code) })
	}
}

func testRefetchOnRejection(t *testing.T, code int) {
	t.Helper()
	fake := useFakeStableToken(t, "old")
	if _, err := GetAccessToken(context.Background(), "wx-app", "secret"); err != nil {
		t.Fatal(err)
	}
	fake.setToken("new")
	var used []string
	err := CallWithAccessToken(context.Background(), "wx-app", "secret", func(token string) error {
		used = append(used, token)
		if token == "old" {
			return &APIError{Errcode: code, Errmsg: "rejected"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("CallWithAccessToken: %v", err)
	}
	if len(used) != 2 || used[0] != "old" || used[1] != "new" {
		t.Fatalf("tokens used=%v, want [old new]", used)
	}
	if got := fake.forced.Load(); got != 0 {
		t.Fatalf("force_refresh requests=%d, want 0", got)
	}
}

func TestCallWithAccessTokenRetriesOnlyOnce(t *testing.T) {
	useFakeStableToken(t, "token")
	calls := 0
	err := CallWithAccessToken(context.Background(), "wx-app", "secret", func(string) error {
		calls++
		return &APIError{Errcode: 40001, Errmsg: "rejected"}
	})
	if !tokenRejected(err) || calls != 2 {
		t.Fatalf("err=%v calls=%d, want token rejection after 2 calls", err, calls)
	}
}

func TestCallWithAccessTokenDoesNotRetryOtherErrors(t *testing.T) {
	fake := useFakeStableToken(t, "token")
	calls := 0
	err := CallWithAccessToken(context.Background(), "wx-app", "secret", func(string) error {
		calls++
		return &APIError{Errcode: 9, Errmsg: "device not authorized"}
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Errcode != 9 || calls != 1 {
		t.Fatalf("err=%v calls=%d, want errcode 9 after 1 call", err, calls)
	}
	if got := fake.issued.Load(); got != 1 {
		t.Fatalf("token requests=%d, want 1", got)
	}
}

func TestInvalidateAccessTokenKeepsFresherToken(t *testing.T) {
	fake := useFakeStableToken(t, "fresh")
	if _, err := GetAccessToken(context.Background(), "wx-app", "secret"); err != nil {
		t.Fatal(err)
	}
	invalidateAccessToken("wx-app", "secret", "stale")
	token, err := GetAccessToken(context.Background(), "wx-app", "secret")
	if err != nil || token != "fresh" {
		t.Fatalf("token=%q err=%v, want cached fresh", token, err)
	}
	if got := fake.issued.Load(); got != 1 {
		t.Fatalf("token requests=%d, want 1", got)
	}
}
