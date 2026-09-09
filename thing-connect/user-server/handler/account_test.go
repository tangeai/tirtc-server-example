package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/service"
)

type accountReader struct {
	id   int64
	fail bool
}

func (a *accountReader) ReadAccount(_ context.Context, id int64) (service.Account, error) {
	a.id = id
	if a.fail {
		return service.Account{}, errors.New("SQL private connection detail")
	}
	return service.Account{ID: id, Email: "account@example.com"}, nil
}
func TestCurrentAccountUsesAuthenticatedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reader := &accountReader{}
	r := gin.New()
	RegisterAccount(r, func(c *gin.Context) { c.Set(ctxUserID, int64(42)) }, service.NewAccountService(reader))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/user/me?user_id=99", nil))
	if reader.id != 42 || w.Code != 200 || !strings.Contains(w.Body.String(), `"user_id":42`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String(), reader.id)
	}
	reader.fail = true
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/user/me", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
}
