package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	adminapp "thing-connect/internal/admin"
	"thing-connect/internal/installer"
)

func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(securityHeaders())
	router.GET("/admin/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	for name, want := range map[string]string{
		"Content-Security-Policy": "script-src 'self'",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
	} {
		if got := recorder.Header().Get(name); !strings.Contains(got, want) {
			t.Errorf("%s = %q, want to contain %q", name, got, want)
		}
	}
}

func TestSetupErrorReportsDependencyWithoutLeakingRawCause(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		message string
	}{
		{name: "redis", err: installer.ErrRedisUnavailable, message: "Redis 连接检查失败"},
		{name: "mqtt", err: installer.ErrMQTTUnavailable, message: "MQTT 连接或认证失败"},
		{name: "mysql", err: installer.ErrMySQLUnavailable, message: "MySQL 连接检查失败"},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.GET("/setup", func(c *gin.Context) {
				setupError(c, fmt.Errorf("%w: %v", test.err, errors.New("sensitive raw dependency cause")))
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/setup", nil))
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d", recorder.Code)
			}
			if !strings.Contains(recorder.Body.String(), test.message) {
				t.Fatalf("body = %s", recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "sensitive raw dependency cause") {
				t.Fatalf("raw dependency error leaked: %s", recorder.Body.String())
			}
			var body struct {
				Data struct {
					Code        string   `json:"code"`
					Message     string   `json:"message"`
					Suggestions []string `json:"suggestions"`
				} `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Data.Code == "" || body.Data.Message == "" || len(body.Data.Suggestions) == 0 {
				t.Fatalf("dependency error has no customer guidance: %s", recorder.Body.String())
			}
		})
	}
}

func TestAdminAPIIsNotCacheable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(securityHeaders())
	router.GET("/v1/admin/me", func(c *gin.Context) { c.Status(http.StatusOK) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/admin/me", nil))
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestAdminMQTTProbeUsesTemporaryClientID(t *testing.T) {
	username := adminMQTTProbeConfig(adminapp.MQTTConnection{
		Broker: "mqtt://broker.example.com:1883", Username: "usersrv", Password: "secret",
	})
	if username.AuthMode() != "username" || username.Username != "usersrv" || username.ClientID != "" {
		t.Fatalf("username probe config = %+v", username)
	}

	fixed := adminMQTTProbeConfig(adminapp.MQTTConnection{
		Broker: "mqtt://broker.example.com:1883", ClientID: "voipsrv", Password: "secret",
	})
	if fixed.AuthMode() != "username" || fixed.Username != "voipsrv" || fixed.ClientID != "" {
		t.Fatalf("fixed ClientID probe could disconnect the live client: %+v", fixed)
	}
}
