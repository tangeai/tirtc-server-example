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
	"time"

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
	// forceErrcode makes force_refresh fail without rotating the token.
	forceErrcode int
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
	if req.ForceRefresh && f.forceErrcode != 0 {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": f.forceErrcode, "errmsg": "system busy"})
		return
	}
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

func (f *fakeStableTokenServer) setForceErrcode(code int) {
	f.mu.Lock()
	f.forceErrcode = code
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

func TestSecondRejectedTokenCannotBeReused(t *testing.T) {
	useFakeStableToken(t, "old")
	ctx := context.Background()
	var used []string
	err := CallWithAccessToken(ctx, "wx-app", "secret", grantRefresh, func(token string) error {
		used = append(used, token)
		return &APIError{Errcode: 40014}
	})
	if !tokenRejected(err) || len(used) != 2 || used[0] == used[1] {
		t.Fatalf("tokens=%v err=%v", used, err)
	}
	if token, err := GetAccessToken(ctx, "wx-app", "secret"); token != "" || !errors.Is(err, ErrTokenRefreshInProgress) {
		t.Fatalf("stable endpoint still serves the rejected replacement: token=%q err=%v", token, err)
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
	token, err := getTokenState("wx-app", "secret").token(context.Background(), "wx-app", "secret", "stale", grantRefresh)
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
			token, err := instance.token(context.Background(), appID, "secret", "old", reserve)
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

func shortenRecovery(t *testing.T, backoffMin, backoffMax time.Duration, polls ...time.Duration) {
	t.Helper()
	prevMin, prevMax, prevPolls := recoveryBackoffMin, recoveryBackoffMax, peerRefreshPolls
	recoveryBackoffMin, recoveryBackoffMax, peerRefreshPolls = backoffMin, backoffMax, polls
	t.Cleanup(func() { recoveryBackoffMin, recoveryBackoffMax, peerRefreshPolls = prevMin, prevMax, prevPolls })
}

// memoryGate models the Redis gate: one grant per ttl window.
type memoryGate struct {
	mu     sync.Mutex
	ttl    time.Duration
	until  time.Time
	grants atomic.Int32
}

func (g *memoryGate) reserve(context.Context, string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Now().Before(g.until) {
		return false, nil
	}
	g.until = time.Now().Add(g.ttl)
	g.grants.Add(1)
	return true, nil
}

// rejectingCall fails with 40001 for bad and counts each use of bad.
func rejectingCall(bad string, badUses *atomic.Int32) func(string) error {
	return func(token string) error {
		if token == bad {
			badUses.Add(1)
			return &APIError{Errcode: 40001, Errmsg: "invalid credential"}
		}
		return nil
	}
}

func TestFailedForcedRefreshDoesNotReuseRejectedToken(t *testing.T) {
	shortenRecovery(t, time.Hour, time.Hour, time.Millisecond)
	fake := useFakeStableToken(t, "bad")
	fake.setForceErrcode(-1)
	gate := &memoryGate{ttl: time.Hour}
	var badUses atomic.Int32
	call := rejectingCall("bad", &badUses)
	err := CallWithAccessToken(context.Background(), "wx-app", "secret", gate.reserve, call)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Errcode != -1 || fake.forced.Load() != 1 {
		t.Fatalf("first call: err=%v forced=%d, want forced refresh error", err, fake.forced.Load())
	}
	issued := fake.issued.Load()
	for i := 0; i < 20; i++ {
		err := CallWithAccessToken(context.Background(), "wx-app", "secret", gate.reserve, call)
		if !errors.As(err, &apiErr) || apiErr.Errcode != -1 {
			t.Fatalf("call %d: err=%v, want the forced refresh error while backing off", i, err)
		}
	}
	if got := badUses.Load(); got != 1 {
		t.Fatalf("business calls with rejected token=%d, want 1", got)
	}
	if got := fake.issued.Load() - issued; got != 0 {
		t.Fatalf("stable_token requests during backoff=%d, want 0", got)
	}
	if _, err := GetAccessToken(context.Background(), "wx-app", "secret"); err == nil {
		t.Fatal("GetAccessToken returned a token during backoff")
	}
}

func TestFailedForcedRefreshRetriesAfterWindow(t *testing.T) {
	shortenRecovery(t, 10*time.Millisecond, 40*time.Millisecond, time.Millisecond)
	fake := useFakeStableToken(t, "bad")
	fake.setForceErrcode(-1)
	gate := &memoryGate{ttl: 150 * time.Millisecond}
	var badUses atomic.Int32
	call := rejectingCall("bad", &badUses)
	if err := CallWithAccessToken(context.Background(), "wx-app", "secret", gate.reserve, call); err == nil {
		t.Fatal("first call succeeded despite failed forced refresh")
	}
	failedAt := time.Now()
	issued := fake.issued.Load()
	fake.setForceErrcode(0)
	deadline := time.Now().Add(3 * time.Second)
	attempts := 0
	for {
		attempts++
		err := CallWithAccessToken(context.Background(), "wx-app", "secret", gate.reserve, call)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no recovery after %d attempts: %v", attempts, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if elapsed := time.Since(failedAt); elapsed < 100*time.Millisecond {
		t.Fatalf("recovered after %v, before the refresh window expired", elapsed)
	}
	if got := fake.forced.Load(); got != 2 || gate.grants.Load() != 2 {
		t.Fatalf("forced=%d grants=%d, want 2 each", got, gate.grants.Load())
	}
	if got := badUses.Load(); got != 1 {
		t.Fatalf("business calls with rejected token=%d, want 1", got)
	}
	// Backoff caps stable reads at about one per 40ms, far fewer than attempts.
	if got := fake.issued.Load() - issued; got > 15 || int(got) >= attempts {
		t.Fatalf("stable_token requests=%d over %d attempts", got, attempts)
	}
}

func TestPeerRotationAfterFailedRecoveryNeedsNoForce(t *testing.T) {
	shortenRecovery(t, 30*time.Millisecond, 30*time.Millisecond, time.Millisecond, time.Millisecond)
	fake := useFakeStableToken(t, "bad")
	peerHolds := func(context.Context, string) (bool, error) { return false, nil }
	var badUses atomic.Int32
	call := rejectingCall("bad", &badUses)
	err := CallWithAccessToken(context.Background(), "wx-app", "secret", peerHolds, call)
	if !errors.Is(err, ErrTokenRefreshInProgress) {
		t.Fatalf("err=%v, want ErrTokenRefreshInProgress", err)
	}
	issued := fake.issued.Load()
	err = CallWithAccessToken(context.Background(), "wx-app", "secret", peerHolds, call)
	if !errors.Is(err, ErrTokenRefreshInProgress) || fake.issued.Load() != issued {
		t.Fatalf("err=%v stable requests=%d, want fast failure without requests", err, fake.issued.Load()-issued)
	}
	fake.setToken("from-peer")
	time.Sleep(40 * time.Millisecond)
	var used string
	err = CallWithAccessToken(context.Background(), "wx-app", "secret", peerHolds, func(token string) error {
		used = token
		return call(token)
	})
	if err != nil || used != "from-peer" {
		t.Fatalf("err=%v token=%q, want from-peer", err, used)
	}
	if fake.forced.Load() != 0 || badUses.Load() != 1 {
		t.Fatalf("forced=%d bad uses=%d, want 0 and 1", fake.forced.Load(), badUses.Load())
	}
}

func TestConcurrentInstancesForceOnceWhenRefreshFails(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = rdb.Close() })
	shortenRecovery(t, time.Millisecond, time.Millisecond, time.Millisecond)
	fake := useFakeStableToken(t, "old")
	fake.setForceErrcode(-1)
	appID := uuid.NewString()
	t.Cleanup(func() { _ = rdb.Del(context.Background(), "voip:wechat:refresh:"+appID).Err() })
	reserve := cache.WeChatRefreshGate(rdb)
	instances := make([]*tokenState, 12)
	for i := range instances {
		instances[i] = &tokenState{lock: make(chan struct{}, 1)}
	}
	run := func() {
		start := make(chan struct{})
		var workers sync.WaitGroup
		for _, instance := range instances {
			workers.Add(1)
			go func(instance *tokenState) {
				defer workers.Done()
				<-start
				if token, err := instance.token(context.Background(), appID, "secret", "old", reserve); err == nil {
					t.Errorf("instance got token %q despite failed refresh", token)
				}
			}(instance)
		}
		close(start)
		workers.Wait()
	}
	run()
	fake.setForceErrcode(0)
	time.Sleep(5 * time.Millisecond)
	run()
	if got := fake.forced.Load(); got != 1 {
		t.Fatalf("forced refreshes=%d, want 1 within the gate window", got)
	}
}
