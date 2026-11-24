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

	"github.com/google/uuid"

	"thing-connect/internal/cache"
	"thing-connect/internal/testenv"
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
	if req.ForceRefresh {
		f.token = "rotated"
	}
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
	err := CallWithAccessToken(context.Background(), "wx-app", "secret", grantRefresh, func(token string) error {
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
	err := CallWithAccessToken(context.Background(), "wx-app", "secret", grantRefresh, func(string) error {
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
	err := CallWithAccessToken(context.Background(), "wx-app", "secret", grantRefresh, func(string) error {
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

func TestRejectionKeepsFresherToken(t *testing.T) {
	fake := useFakeStableToken(t, "fresh")
	if _, err := GetAccessToken(context.Background(), "wx-app", "secret"); err != nil {
		t.Fatal(err)
	}
	token, err := getTokenState("wx-app", "secret").replacementToken(context.Background(), "wx-app", "secret", "stale", grantRefresh)
	if err != nil || token != "fresh" {
		t.Fatalf("token=%q err=%v, want cached fresh", token, err)
	}
	if got := fake.issued.Load(); got != 1 {
		t.Fatalf("token requests=%d, want 1", got)
	}
}

func TestRejectedStableTokenNeedsForceRefresh(t *testing.T) {
	fake := useFakeStableToken(t, "rejected")
	calls := 0
	err := CallWithAccessToken(context.Background(), "wx-app", "secret", grantRefresh, func(token string) error {
		calls++
		if token != "rotated" {
			return &APIError{Errcode: 40014}
		}
		return nil
	})
	if err != nil || calls != 2 || fake.forced.Load() != 1 {
		t.Fatalf("calls=%d forced=%d err=%v", calls, fake.forced.Load(), err)
	}
}

func grantRefresh(context.Context, string) (bool, error) { return true, nil }

func TestPeerOwnsRefresh(t *testing.T) {
	fake := useFakeStableToken(t, "old")
	calls := 0
	err := CallWithAccessToken(context.Background(), "wx-app", "secret", func(context.Context, string) (bool, error) {
		fake.setToken("from-peer")
		return false, nil
	}, func(token string) error {
		calls++
		if token != "from-peer" {
			return &APIError{Errcode: 42001}
		}
		return nil
	})
	if err != nil || fake.forced.Load() != 0 || calls != 2 {
		t.Fatalf("err=%v forced=%d calls=%d", err, fake.forced.Load(), calls)
	}
}

func TestConcurrentInstancesRecoverOneRejectedToken(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = rdb.Close() })
	fake := useFakeStableToken(t, "old")
	appID := uuid.NewString()
	reserve := cache.WeChatRefreshGate(rdb)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			instance := tokenState{lock: make(chan struct{}, 1)}
			<-start
			token, err := instance.replacementToken(context.Background(), appID, "secret", "old", reserve)
			if err != nil || token != "rotated" {
				t.Errorf("expected rotated token: %v", err)
			}
		}()
	}
	close(start)
	workers.Wait()
	if got := fake.forced.Load(); got != 1 {
		t.Fatalf("forced refreshes=%d, want 1", got)
	}
}
