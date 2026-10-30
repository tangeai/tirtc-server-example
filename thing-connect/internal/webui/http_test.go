package webui

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/weblanguage"
)

func TestServicePublishesOwnPolicyAndBootstrap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	for _, prefix := range []string{"/v1", "/v1/ai", "/v1/call"} {
		state, _ := weblanguage.New(weblanguage.Config{Default: "en", AllowSwitch: false})
		Register(router, prefix, state, `{"设备":"Devices"}`)
	}
	for _, prefix := range []string{"/v1", "/v1/ai", "/v1/call"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", prefix+"/config/language.js", nil))
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(response)
		}
		if !strings.Contains(response.Body.String(), `"default":"en"`) || !strings.Contains(response.Body.String(), `"allow_switch":false`) || !strings.Contains(response.Body.String(), `"revision":0`) || !strings.Contains(response.Body.String(), `"设备":"Devices"`) {
			t.Fatal("missing bootstrap policy or service dictionary")
		}
		if !strings.HasPrefix(response.Header().Get("Content-Type"), "text/javascript") || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal(response.Header())
		}
	}
}
