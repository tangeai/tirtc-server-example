package apiresp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func decodeResponse(t *testing.T, w *httptest.ResponseRecorder) JSON {
	t.Helper()
	var resp JSON
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestInternalDoesNotExposeRawError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		Internal(c, "dial tcp mysql.internal:3306: connection refused")
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	resp := decodeResponse(t, w)
	if resp.Code != 50000 || resp.Msg != "服务器内部错误，请稍后重试" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if strings.Contains(w.Body.String(), "mysql.internal") {
		t.Fatal("raw internal error leaked to response")
	}
}

func TestFromErrorFallbackDoesNotExposeRawError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		FromError(c, errors.New("SELECT secret FROM private_table"))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	resp := decodeResponse(t, w)
	if resp.Code != 50000 || resp.Msg != "服务器内部错误，请稍后重试" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestBadParamErrorReturnsStableChineseMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		BadParamError(c, errors.New("Field validation failed on required tag"))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	resp := decodeResponse(t, w)
	if resp.Code != 40000 || resp.Msg != "请求参数格式错误，请检查必填字段和字段类型" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestUnauthorizedUsesChineseMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", Unauthorized)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	resp := decodeResponse(t, w)
	if resp.Code != 401 || resp.Msg != "未授权或登录凭证无效" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}
