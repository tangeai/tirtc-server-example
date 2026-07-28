package apiresp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFailInternalDoesNotExposeRawError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		Fail(c, ErrInternal, "query room: dial tcp mysql.internal:3306")
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if strings.Contains(w.Body.String(), "mysql.internal") {
		t.Fatalf("raw internal error leaked: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "服务器内部错误，请稍后重试") {
		t.Fatalf("missing stable Chinese message: %s", w.Body.String())
	}
}
