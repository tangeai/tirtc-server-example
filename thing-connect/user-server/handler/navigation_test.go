package handler

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
	"thing-connect/internal/webnav"
)

func TestPublicNavigationEmptyAndEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	state, _ := webnav.New(webnav.Config{})
	r := gin.New()
	RegisterNavigation(r, state)
	request := func() string {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/config/navigation", nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Header())
		}
		return w.Body.String()
	}
	if !strings.Contains(request(), `"links":[]`) {
		t.Fatal("empty navigation isn't an array")
	}
	if err := state.Apply([]byte(`{"links":[{"name":"文档","url":"https://docs.example.com","enabled":true},{"name":"隐藏","url":"https://example.com","enabled":false}]}`), 7); err != nil {
		t.Fatal(err)
	}
	body := request()
	if !strings.Contains(body, "文档") || strings.Contains(body, "隐藏") || !strings.Contains(body, `"revision":7`) {
		t.Fatal(body)
	}
}
