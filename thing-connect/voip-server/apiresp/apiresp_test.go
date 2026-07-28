package apiresp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFailSanitizesInternalAndUpstreamErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		code int
		want string
	}{
		{"internal", ErrInternal, "服务器内部错误，请稍后重试"},
		{"wechat", ErrWechatAPI, "微信接口调用失败，请稍后重试"},
		{"tirtc", ErrTirtcServerAPI, "实时音视频服务调用失败，请稍后重试"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/", func(c *gin.Context) {
				Fail(c, tt.code, "upstream secret at 10.0.0.8")
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
			if strings.Contains(w.Body.String(), "10.0.0.8") {
				t.Fatalf("raw error leaked: %s", w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tt.want) {
				t.Fatalf("want %q, got %s", tt.want, w.Body.String())
			}
		})
	}
}
