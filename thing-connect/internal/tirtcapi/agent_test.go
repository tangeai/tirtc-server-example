package tirtcapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newDeleteTestClient(t *testing.T, status int, body string) *AgentAPIClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	cfg := AgentAPIConfig{
		BaseURL:     srv.URL,
		AppID:       "app",
		AccessKeyID: "ak",
		SecretKeyID: "sk",
	}
	return NewAgentAPIClient(cfg, srv.Client())
}

// A success envelope is {"code":200}; DeleteRole must treat it as success,
// not flip it into an error.
func TestDeleteRole_SuccessOnCode200(t *testing.T) {
	client := newDeleteTestClient(t, http.StatusOK, `{"code":200}`)
	if err := client.DeleteRole(context.Background(), "role_1"); err != nil {
		t.Fatalf("DeleteRole should succeed on code=200, got error: %v", err)
	}
}

func TestDeleteRole_ErrorOnNon200(t *testing.T) {
	client := newDeleteTestClient(t, http.StatusOK, `{"code":500,"msg":"no permission"}`)
	if err := client.DeleteRole(context.Background(), "role_1"); err == nil {
		t.Fatal("DeleteRole should return error on code=500, got nil")
	}
}

// TestUploadBinaryBodyPreserved 验证 Go 端发送的 multipart body 原样保留高位字节，
// 排除 Go http client 改写 body 的可能（高位字节 wrong signature 问题的 Go 端自证清白）。
func TestUploadBinaryBodyPreserved(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"file_id":"fake_id"}}`))
	}))
	defer srv.Close()

	cfg := AgentAPIConfig{BaseURL: srv.URL, AppID: "a", AccessKeyID: "k", SecretKeyID: "s"}
	client := NewAgentAPIClient(cfg, srv.Client())

	content := []byte{0xFF, 0x80, 0xC0, 0xD3, 0x41}
	if _, err := client.UploadKnowledgeFile(context.Background(), "hi.txt", content); err != nil {
		t.Fatalf("UploadKnowledgeFile: %v", err)
	}
	if !bytes.Contains(received, content) {
		t.Errorf("Go 发送的 multipart body 丢失了高位字节；received=% x", received)
	}
}
