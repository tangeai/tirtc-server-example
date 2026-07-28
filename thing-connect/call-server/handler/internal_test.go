package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"thing-connect/call-server/apiresp"
	"thing-connect/internal/config"
)

func TestInternalUnbindInvalidCredentialUsesBusinessCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := NewServer(
		&config.Config{Call: config.CallCfg{InternalKey: "expected-key"}},
		nil,
		nil,
		nil,
		nil,
	)
	router := gin.New()
	router.POST("/", server.postInternalUnbind)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP=%d, want 200", recorder.Code)
	}
	var response struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != apiresp.ErrInternalCredential {
		t.Fatalf("code=%d, want %d", response.Code, apiresp.ErrInternalCredential)
	}
}
