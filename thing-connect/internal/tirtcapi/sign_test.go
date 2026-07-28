package tirtcapi

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSignTGV1Request_Deterministic(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h1 := SignTGV1Request("secret", "access123", "app456", http.MethodPost, "/v1/token/wxvoip", "", []byte(`{"k":"v"}`), "", signing)
	h2 := SignTGV1Request("secret", "access123", "app456", http.MethodPost, "/v1/token/wxvoip", "", []byte(`{"k":"v"}`), "", signing)
	if h1.Get("Authorization") != h2.Get("Authorization") {
		t.Error("same inputs must produce same Authorization header")
	}
}

func TestSignTGV1Request_DifferentSecretsDifferentSig(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h1 := SignTGV1Request("secret1", "access", "app", http.MethodPost, "/path", "", []byte("body"), "", signing)
	h2 := SignTGV1Request("secret2", "access", "app", http.MethodPost, "/path", "", []byte("body"), "", signing)
	if h1.Get("Authorization") == h2.Get("Authorization") {
		t.Error("different secrets must produce different signatures")
	}
}

func TestSignTGV1Request_ContainsAlgorithm(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h := SignTGV1Request("secret", "access", "app", http.MethodPost, "/v1/token/wxvoip", "", []byte("{}"), "", signing)
	auth := h.Get("Authorization")
	if !strings.HasPrefix(auth, TGV1Alg) {
		t.Errorf("Authorization header must start with %q, got %q", TGV1Alg, auth)
	}
}

func TestSignTGV1Request_SetsRequiredHeaders(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h := SignTGV1Request("secret", "access", "app", http.MethodPost, "/v1/token/wxvoip", "", []byte("{}"), "", signing)
	for _, name := range []string{"X-Tg-Algorithm", "X-Tg-Date", "X-Tg-App-Id", "X-Tg-Content-Sha256", "Content-Type"} {
		if h.Get(name) == "" {
			t.Errorf("header %s should not be empty", name)
		}
	}
}

// JSON 路径回归：空 contentType 必须默认 application/json，保证 token/roles 等
// 所有 JSON 请求的签名字节级不变。
func TestSignTGV1Request_EmptyContentTypeDefaultsJSON(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h := SignTGV1Request("secret", "access", "app", http.MethodPost, "/path", "", []byte("{}"), "", signing)
	if h.Get("Content-Type") != "application/json" {
		t.Errorf("空 contentType 应默认 application/json，got %q", h.Get("Content-Type"))
	}
}

// multipart 上传：签名必须基于真实请求体，而不是空 body。
func TestSignTGV1Request_MultipartUsesRealBody(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	body := []byte("multipart body bytes")
	h := SignTGV1Request("secret", "access", "app", http.MethodPost, "/ai/aigcrtc/knowledge/files", "", body, "multipart/form-data; boundary=abc", signing)
	if h.Get("X-Tg-Content-Sha256") != SHA256Hex(body) {
		t.Error("multipart 上传签名应用真实 body 的 sha256")
	}
	if h.Get("X-Tg-Content-Sha256") == SHA256Hex(nil) {
		t.Error("不应等于空 body 的 sha256")
	}
}

// 不同 content-type 必须产生不同签名，证明 content-type 进了签名串。
func TestSignTGV1Request_ContentTypeAffectsSignature(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	body := []byte("body")
	hJSON := SignTGV1Request("secret", "access", "app", http.MethodPost, "/p", "", body, "", signing)
	hMP := SignTGV1Request("secret", "access", "app", http.MethodPost, "/p", "", body, "multipart/form-data; boundary=x", signing)
	if hJSON.Get("Authorization") == hMP.Get("Authorization") {
		t.Error("不同 content-type 必须产生不同签名")
	}
}

func TestSignTGV1Request_DeleteWithBodySignsContentHeaders(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"device_ids":["dev1"]}`)
	h := SignTGV1Request("secret", "access", "app", http.MethodDelete, "/v2/ai/device-roles", "", body, "", signing)

	if h.Get("Content-Type") != "application/json" {
		t.Fatalf("DELETE with body should default Content-Type to application/json, got %q", h.Get("Content-Type"))
	}
	if h.Get("Content-Length") != "23" {
		t.Fatalf("DELETE with body should set Content-Length, got %q", h.Get("Content-Length"))
	}
	if !strings.Contains(h.Get("X-Tg-Signed-Headers"), "content-type") {
		t.Fatalf("DELETE with body should sign content-type, got %q", h.Get("X-Tg-Signed-Headers"))
	}
	if !strings.Contains(h.Get("X-Tg-Signed-Headers"), "content-length") {
		t.Fatalf("DELETE with body should sign content-length, got %q", h.Get("X-Tg-Signed-Headers"))
	}
}

func TestSignTGV1Request_PostWithoutBodyStillSignsContentHeaders(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h := SignTGV1Request("secret", "access", "app", http.MethodPost, "/p", "", nil, "", signing)

	if h.Get("Content-Type") != "application/json" {
		t.Fatalf("POST without body should still default Content-Type to application/json, got %q", h.Get("Content-Type"))
	}
	if h.Get("Content-Length") != "0" {
		t.Fatalf("POST without body should still set Content-Length, got %q", h.Get("Content-Length"))
	}
	if !strings.Contains(h.Get("X-Tg-Signed-Headers"), "content-type") {
		t.Fatalf("POST without body should sign content-type, got %q", h.Get("X-Tg-Signed-Headers"))
	}
	if !strings.Contains(h.Get("X-Tg-Signed-Headers"), "content-length") {
		t.Fatalf("POST without body should sign content-length, got %q", h.Get("X-Tg-Signed-Headers"))
	}
}
