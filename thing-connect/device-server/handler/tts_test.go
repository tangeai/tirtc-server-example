package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"thing-connect/internal/service"
	"thing-connect/internal/store"
)

type handlerTTSCache struct {
	store.CacheStore
	codeToHash map[string]string
	verify     map[string][]byte
}

func (f *handlerTTSCache) GetDeviceCodeLookup(_ context.Context, code string) (string, error) {
	return f.codeToHash[code], nil
}

func (f *handlerTTSCache) GetVerifyRecord(_ context.Context, physHash string) ([]byte, error) {
	return f.verify[physHash], nil
}

func mintTempToken(t *testing.T, secret, clientID string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"device_id": clientID,
		"exp":       time.Now().Add(time.Minute).Unix(),
	})
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestGetTTSRequiresMatchingTempToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		secret   = "tts-secret"
		code     = "386236"
		physHash = "physical-hash"
		clientID = "tmp_a1b2c3d4"
	)
	record, _ := json.Marshal(map[string]string{"code": code, "temp_client_id": clientID})
	cache := &handlerTTSCache{
		codeToHash: map[string]string{code: physHash},
		verify:     map[string][]byte{physHash: record},
	}
	svc := service.NewDeviceService(nil, cache, secret, service.DefaultServiceConfig())
	r := gin.New()
	NewServer(svc).Register(r)

	request := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/device/tts?code="+code+"&fmt=wav", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	if w := request(""); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d, want 401", w.Code)
	}
	if w := request(mintTempToken(t, secret, "tmp_other")); w.Code != http.StatusNotFound {
		t.Fatalf("mismatched token status=%d, want 404", w.Code)
	}
	w := request(mintTempToken(t, secret, clientID))
	if w.Code != http.StatusOK {
		t.Fatalf("matching token status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
	if got := w.Header().Get("Content-Type"); got != "audio/wav" {
		t.Fatalf("Content-Type=%q, want audio/wav", got)
	}
	if !bytes.HasPrefix(w.Body.Bytes(), []byte("RIFF")) {
		t.Fatal("WAV response is missing RIFF header")
	}
}
