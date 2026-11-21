package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRealTokenSigningAndBusinessFailure(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mac := hmac.New(sha256.New, []byte("test-secret"))
		_, _ = mac.Write([]byte("d1" + r.Header.Get("X-Timestamp") + r.Header.Get("X-Nonce")))
		if r.Method != "POST" || r.Header.Get("X-Device-Id") != "d1" || r.Header.Get("X-Signature") != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
			t.Error("invalid signed token request")
		}
		nonce := r.Header.Get("X-Nonce")
		if len(nonce) != 32 || seen[nonce] {
			t.Error("nonce reused")
		}
		seen[nonce] = true
		if len(seen) == 1 {
			_, _ = w.Write([]byte(`{"code":200,"data":{"mqtt_token":"signed-token"}}`))
		} else {
			_, _ = w.Write([]byte(`{"code":6006,"msg":"private detail"}`))
		}
	}))
	defer server.Close()
	c := tokenClient()
	defer c.CloseIdleConnections()
	token, err := fetchToken(context.Background(), c, server.URL, credential{"d1", "test-secret"})
	if err != nil || token != "signed-token" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if _, err := fetchToken(context.Background(), c, server.URL, credential{"d1", "test-secret"}); err == nil || err.Error() != "token request rejected" {
		t.Fatalf("business failure: %v", err)
	}
}

func TestTokenDoesNotForwardCredentialsAndHonorsCancellation(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	c := tokenClient()
	defer c.CloseIdleConnections()
	if _, err := fetchToken(context.Background(), c, redirect.URL, credential{"d1", "secret"}); err == nil || forwarded {
		t.Fatal("redirect accepted")
	}
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer hung.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := fetchToken(ctx, c, hung.URL, credential{"d1", "secret"}); err == nil || time.Since(start) > time.Second {
		t.Fatal("caller deadline ignored")
	}
}

func TestFixtureShardsHaveDisjointIdentities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.csv")
	if err := os.WriteFile(path, []byte("device_id,device_key\na,1\nb,2\nc,3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := loadCredentials(path, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadCredentials(path, 1, 2)
	if err != nil || len(a) != 2 || len(b) != 1 || a[0].id != "a" || a[1].id != "c" || b[0].id != "b" {
		t.Fatal("incorrect shards")
	}
}
