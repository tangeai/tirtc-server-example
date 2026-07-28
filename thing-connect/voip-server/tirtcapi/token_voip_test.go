package tirtcapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── PostTokenService 参数校验 ────────────────────────────────────────────────

func TestPostTokenService_EmptyBaseURL(t *testing.T) {
	_, _, err := PostTokenService(context.Background(), nil, "", "aid", "app", "sec", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("want base_url error, got %v", err)
	}
}

func TestPostTokenService_EmptyAccessID(t *testing.T) {
	_, _, err := PostTokenService(context.Background(), nil, "https://example.com", "", "app", "sec", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "access_id") {
		t.Errorf("want access_id error, got %v", err)
	}
}

func TestPostTokenService_EmptyAppID(t *testing.T) {
	_, _, err := PostTokenService(context.Background(), nil, "https://example.com", "aid", "", "sec", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "app_id") {
		t.Errorf("want app_id error, got %v", err)
	}
}

func TestPostTokenService_EmptySecretKey(t *testing.T) {
	_, _, err := PostTokenService(context.Background(), nil, "https://example.com", "aid", "app", "", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "secret_key") {
		t.Errorf("want secret_key error, got %v", err)
	}
}

func TestPostTokenService_ServerErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	_, _, err := PostTokenService(context.Background(), srv.Client(), srv.URL, "aid", "app", "sec", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("want HTTP 500 error, got %v", err)
	}
}

func TestPostTokenService_BusinessErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := tokenServiceResp{Code: 1001, Msg: "invalid token"}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	_, _, err := PostTokenService(context.Background(), srv.Client(), srv.URL, "aid", "app", "sec", TokenWxvoipRequest{})
	if err == nil || !strings.Contains(err.Error(), "1001") {
		t.Errorf("want code=1001 error, got %v", err)
	}
}

func TestPostTokenService_Success(t *testing.T) {
	data, _ := json.Marshal(tokenServiceData{PeerID: "peer1", Token: "tok1"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := tokenServiceResp{Code: 0, Data: data}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	peerID, token, err := PostTokenService(context.Background(), srv.Client(), srv.URL, "aid", "app", "sec", TokenWxvoipRequest{})
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if peerID != "peer1" || token != "tok1" {
		t.Errorf("want peer1/tok1, got %s/%s", peerID, token)
	}
}
