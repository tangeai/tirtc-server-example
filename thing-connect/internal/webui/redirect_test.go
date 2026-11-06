package webui

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLegacyPageRedirectPreservesDeviceContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RedirectPage(r, "/v1/ai/agent", "/agent")
	RedirectPage(r, "/v1/call/group/page", "/room")
	for old, target := range map[string]string{"/v1/ai/agent": "/agent", "/v1/call/group/page": "/room"} {
		for _, query := range []string{"", "?device_id=A%2FB&source=miniprogram", "?next=https%3A%2F%2Fexample.com"} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", old+query, nil))
			if w.Code != 302 || w.Header().Get("Location") != target+query || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s: status=%d location=%q", old+query, w.Code, w.Header().Get("Location"))
			}
		}
	}
}
