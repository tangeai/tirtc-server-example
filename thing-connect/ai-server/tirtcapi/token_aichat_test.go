package tirtcapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type aichatResp struct {
	Code int32           `json:"code"`
	Msg  string          `json:"msg,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

type aichatData struct {
	PeerID string `json:"peer_id"`
	Token  string `json:"token"`
}

func TestPostTokenAichat_EmptyBaseURL(t *testing.T) {
	_, _, code, _, err := PostTokenAichat(context.Background(), nil, "", "aid", "app", "sec", "dev1", "role1")
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("want base_url error, got code=%d err=%v", code, err)
	}
}

func TestPostTokenAichat_EmptyAccessKeyID(t *testing.T) {
	_, _, _, _, err := PostTokenAichat(context.Background(), nil, "https://example.com", "", "app", "sec", "dev1", "role1")
	if err == nil || !strings.Contains(err.Error(), "access_key_id") {
		t.Errorf("want access_key_id error, got %v", err)
	}
}

func TestPostTokenAichat_EmptyAppID(t *testing.T) {
	_, _, _, _, err := PostTokenAichat(context.Background(), nil, "https://example.com", "aid", "", "sec", "dev1", "role1")
	if err == nil || !strings.Contains(err.Error(), "app_id") {
		t.Errorf("want app_id error, got %v", err)
	}
}

func TestPostTokenAichat_EmptySecretKeyID(t *testing.T) {
	_, _, _, _, err := PostTokenAichat(context.Background(), nil, "https://example.com", "aid", "app", "", "dev1", "role1")
	if err == nil || !strings.Contains(err.Error(), "secret_key_id") {
		t.Errorf("want secret_key_id error, got %v", err)
	}
}

func TestPostTokenAichat_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	_, _, _, _, err := PostTokenAichat(context.Background(), srv.Client(), srv.URL, "aid", "app", "sec", "dev1", "role1")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("want HTTP 500 error, got %v", err)
	}
}

func TestPostTokenAichat_UpstreamBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := aichatResp{Code: 10023, Msg: "device not found"}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	peerID, token, code, msg, err := PostTokenAichat(context.Background(), srv.Client(), srv.URL, "aid", "app", "sec", "dev1", "role1")
	if err != nil {
		t.Fatalf("want nil err for business error, got %v", err)
	}
	if peerID != "" || token != "" {
		t.Errorf("want empty peerID/token on business error")
	}
	if code != 10023 || msg != "device not found" {
		t.Errorf("want code=10023 msg='device not found', got code=%d msg=%s", code, msg)
	}
}

func TestPostTokenAichat_Success(t *testing.T) {
	data, _ := json.Marshal(aichatData{PeerID: "peer1", Token: "tok1"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request body contains device_id and role_id
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["device_id"] != "dev1" || body["role_id"] != "role1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp := aichatResp{Code: 0, Data: data}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	peerID, token, code, msg, err := PostTokenAichat(context.Background(), srv.Client(), srv.URL, "aid", "app", "sec", "dev1", "role1")
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if code != 0 || msg != "" {
		t.Errorf("want code=0, got code=%d msg=%s", code, msg)
	}
	if peerID != "peer1" || token != "tok1" {
		t.Errorf("want peer1/tok1, got %s/%s", peerID, token)
	}
}
