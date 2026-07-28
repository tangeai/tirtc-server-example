package tirtcsigning

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// --- Unit tests ---

func TestSignRequest_Deterministic(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h1 := SignRequest("access123", "secret", "app456", http.MethodPost, "/v1/token/wxvoip", "", []byte(`{"k":"v"}`), signing)
	h2 := SignRequest("access123", "secret", "app456", http.MethodPost, "/v1/token/wxvoip", "", []byte(`{"k":"v"}`), signing)
	if h1.Get("Authorization") != h2.Get("Authorization") {
		t.Error("same inputs must produce same Authorization header")
	}
}

func TestSignRequest_DifferentSecretsDifferentSig(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h1 := SignRequest("access", "secret1", "app", http.MethodPost, "/path", "", []byte("body"), signing)
	h2 := SignRequest("access", "secret2", "app", http.MethodPost, "/path", "", []byte("body"), signing)
	if h1.Get("Authorization") == h2.Get("Authorization") {
		t.Error("different secrets must produce different signatures")
	}
}

func TestSignRequest_ContainsAlgorithm(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h := SignRequest("access", "secret", "app", http.MethodPost, "/v1/token/wxvoip", "", []byte("{}"), signing)
	auth := h.Get("Authorization")
	if !strings.HasPrefix(auth, AlgTGV1) {
		t.Errorf("Authorization header must start with %q, got %q", AlgTGV1, auth)
	}
}

func TestSignRequest_SetsRequiredHeaders(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h := SignRequest("access", "secret", "app", http.MethodPost, "/v1/token/wxvoip", "", []byte("{}"), signing)
	for _, name := range []string{"X-Tg-Algorithm", "X-Tg-Date", "X-Tg-App-Id", "X-Tg-Content-Sha256", "Content-Type"} {
		if h.Get(name) == "" {
			t.Errorf("header %s should not be empty", name)
		}
	}
}

func TestSignRequest_GET_NoContentType(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h := SignRequest("access", "secret", "app", http.MethodGet, "/v1/devices", "status=online", []byte{}, signing)
	if h.Get("Content-Type") != "" {
		t.Error("GET request should not have Content-Type header")
	}
	if h.Get("Content-Length") != "" {
		t.Error("GET request should not have Content-Length header")
	}
	if h.Get("X-Tg-Content-Sha256") != sha256Hex([]byte{}) {
		t.Error("GET request should have SHA-256 of empty body")
	}
}

func TestSignRequest_URITrailingSlash(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	h1 := SignRequest("access", "secret", "app", http.MethodPost, "/path/", "", []byte("{}"), signing)
	h2 := SignRequest("access", "secret", "app", http.MethodPost, "/path", "", []byte("{}"), signing)
	if h1.Get("Authorization") != h2.Get("Authorization") {
		t.Error("URI trailing slash should be normalized (same sig for /path/ and /path)")
	}
}

// --- Test vector generation ---

type testVector struct {
	Description string            `json:"description"`
	AccessKey   string            `json:"accessKey"`
	AccessSecret string           `json:"accessSecret"`
	AppID       string            `json:"appId"`
	Method      string            `json:"method"`
	URIPath     string            `json:"uriPath"`
	Body        string            `json:"body"`
	RawQuery    string            `json:"rawQuery"`
	SigningTime string            `json:"signingTime"`
	Expected    map[string]string `json:"expected"`
}

func TestSignRequest_GenerateTestVectors(t *testing.T) {
	signing := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	vectors := []testVector{
		{
			Description:  "POST /v1/token/wxvoip (文档接口)",
			AccessKey:    "test-access-key-123",
			AccessSecret: "test-secret-456",
			AppID:        "app-789",
			Method:       "POST",
			URIPath:      "/v1/token/wxvoip",
			Body:         `{"device_id":"TESTDEVICE01","wx_session_key":"test-key","wx_room_id":"room-1","wx_session_token":"token-1","wx_app_id":"wxapp","wx_model_id":"model-1","audio_rate":8000,"audio_channels":1}`,
			RawQuery:     "",
			SigningTime:  "2024-01-15T12:00:00Z",
		},
		{
			Description:  "POST empty body",
			AccessKey:    "test-access-key-123",
			AccessSecret: "test-secret-456",
			AppID:        "app-789",
			Method:       "POST",
			URIPath:      "/v2/user/login",
			Body:         "",
			RawQuery:     "",
			SigningTime:  "2024-01-15T12:00:00Z",
		},
		{
			Description:  "GET with query params",
			AccessKey:    "test-access-key-123",
			AccessSecret: "test-secret-456",
			AppID:        "app-789",
			Method:       "GET",
			URIPath:      "/v2/device/server/connection",
			Body:         "",
			RawQuery:     "device_id=TESTDEVICE01&platform=web",
			SigningTime:  "2024-01-15T12:00:00Z",
		},
		{
			Description:  "GET with plus-in-query (replaced with %20)",
			AccessKey:    "test-access-key-123",
			AccessSecret: "test-secret-456",
			AppID:        "app-789",
			Method:       "GET",
			URIPath:      "/v2/search",
			Body:         "",
			RawQuery:     "q=hello+world",
			SigningTime:  "2024-01-15T12:00:00Z",
		},
		{
			Description:  "PUT with body",
			AccessKey:    "test-access-key-123",
			AccessSecret: "test-secret-456",
			AppID:        "app-789",
			Method:       "PUT",
			URIPath:      "/v2/device/attrs",
			Body:         `{"attrs":{"wakeup":"on"}}`,
			RawQuery:     "",
			SigningTime:  "2024-01-15T12:00:00Z",
		},
		{
			Description:  "DELETE without body",
			AccessKey:    "test-access-key-123",
			AccessSecret: "test-secret-456",
			AppID:        "app-789",
			Method:       "DELETE",
			URIPath:      "/v2/user/12345",
			Body:         "",
			RawQuery:     "",
			SigningTime:  "2024-01-15T12:00:00Z",
		},
		{
			Description:  "URI with trailing slash normalized",
			AccessKey:    "test-access-key-123",
			AccessSecret: "test-secret-456",
			AppID:        "app-789",
			Method:       "POST",
			URIPath:      "/v1/token/wxvoip/",
			Body:         `{"device_id":"TESTDEVICE01","wx_session_key":"test-key","wx_room_id":"room-1","wx_session_token":"token-1","wx_app_id":"wxapp","wx_model_id":"model-1","audio_rate":8000,"audio_channels":1}`,
			RawQuery:     "",
			SigningTime:  "2024-01-15T12:00:00Z",
		},
		{
			Description:  "root path /",
			AccessKey:    "test-access-key-123",
			AccessSecret: "test-secret-456",
			AppID:        "app-789",
			Method:       "GET",
			URIPath:      "/",
			Body:         "",
			RawQuery:     "action=ping",
			SigningTime:  "2024-01-15T12:00:00Z",
		},
	}

	// Compute expected values using the Go implementation
	for i := range vectors {
		v := &vectors[i]
		h := SignRequest(v.AccessKey, v.AccessSecret, v.AppID, v.Method, v.URIPath, v.RawQuery, []byte(v.Body), signing)

		expected := make(map[string]string)
		// Always-present headers
		expected["X-Tg-Algorithm"] = h.Get("X-Tg-Algorithm")
		expected["X-Tg-Date"] = h.Get("X-Tg-Date")
		expected["X-Tg-App-Id"] = h.Get("X-Tg-App-Id")
		expected["X-Tg-Content-Sha256"] = h.Get("X-Tg-Content-Sha256")
		expected["X-Tg-Signed-Headers"] = h.Get("X-Tg-Signed-Headers")
		expected["Authorization"] = h.Get("Authorization")
		// Conditionally present
		if ct := h.Get("Content-Type"); ct != "" {
			expected["Content-Type"] = ct
		}
		if cl := h.Get("Content-Length"); cl != "" {
			expected["Content-Length"] = cl
		}
		v.Expected = expected
	}

	// Write test-vectors.json to the parent directory
	data, err := json.MarshalIndent(map[string]interface{}{"vectors": vectors}, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal test vectors: %v", err)
	}

	outputPath := "../test-vectors.json"
	if err := os.WriteFile(outputPath, append(data, '\n'), 0644); err != nil {
		t.Fatalf("failed to write test vectors: %v", err)
	}
	fmt.Printf("Test vectors written to %s\n", outputPath)
}
