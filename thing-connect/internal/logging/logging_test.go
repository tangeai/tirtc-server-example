package logging

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRedactJSONRecursivelyRemovesCredentials(t *testing.T) {
	raw := `{"email":"admin@example.com","password":"PLAIN_CREDENTIAL_VALUE","nested":{"access_token":"JWT_CREDENTIAL_VALUE","device_key":"DEVICE_CREDENTIAL_VALUE"},"items":[{"current_mfa_code":"123456"}]}`
	redacted := RedactJSON(raw)
	for _, secret := range []string{"PLAIN_CREDENTIAL_VALUE", "JWT_CREDENTIAL_VALUE", "DEVICE_CREDENTIAL_VALUE", "123456"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("credential %q remained in %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, "admin@example.com") || strings.Count(redacted, "[REDACTED]") != 4 {
		t.Fatalf("unexpected redacted JSON: %s", redacted)
	}
}

func TestBodyLogPreservesBodiesLargerThanLogLimit(t *testing.T) {
	previous := slog.Default()
	slog.SetDefault(newLoggerWith(io.Discard, "debug", "text"))
	defer slog.SetDefault(previous)
	gin.SetMode(gin.TestMode)
	payload := `{"value":"` + strings.Repeat("x", maxBodyBytes*2) + `"}`
	router := gin.New()
	router.Use(BodyLog())
	router.POST("/body", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil || string(body) != payload {
			c.Status(http.StatusBadRequest)
			return
		}
		c.JSON(http.StatusOK, gin.H{"received": len(body)})
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/body", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("handler received a truncated body: HTTP %d", recorder.Code)
	}
}

func TestBodyForLogSuppressesSensitiveRoutesAndInvalidJSON(t *testing.T) {
	if got := bodyForLog("/v1/admin/auth/login", `{"email":"a","password":"b"}`); got != "[REDACTED]" {
		t.Fatalf("login body was not suppressed: %s", got)
	}
	if got := bodyForLog("/v1/admin/configs/user-server/smtp", `{"value":{}}`); got != "[REDACTED]" {
		t.Fatalf("config body was not suppressed: %s", got)
	}
	if got := RedactJSON(`not-json-secret`); got != "[UNAVAILABLE]" {
		t.Fatalf("invalid JSON leaked: %s", got)
	}
}

func TestMaskHeaderHandlesShortBearerAndMAC(t *testing.T) {
	if got := maskHeader("Authorization", "Bearer x"); got != "Bearer ***" {
		t.Fatalf("short bearer mask: %q", got)
	}
	if got := maskHeader("X-MAC", "sensitive-signature"); strings.Contains(got, "sensitive") {
		t.Fatalf("MAC header was not masked: %q", got)
	}
}
