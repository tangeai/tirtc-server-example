package admin

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/ai/roletypes"
)

func TestRoleTypeInternalAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	s := &HTTPServer{roleTypes: NewRoleTypeService(&typeStoreStub{}, &voicesStub{})}
	s.registerRoleTypesInternal(router, "test-key")
	for _, key := range []string{"", "bad", "test-key"} {
		r := httptest.NewRequest("GET", "/v1/internal/ai-role-types", nil)
		r.Header.Set("X-Internal-Key", key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		want := 403
		if key == "test-key" {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("key result: %d", w.Code)
		}
	}
}
func TestRoleTypeErrorsDoNotExposeUpstream(t *testing.T) {
	for _, err := range []error{errors.New("secret mysql connection"), errors.Join(roletypes.ErrUnavailable, errors.New("secret upstream token"))} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		writeRoleTypeResult(c, nil, err)
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal(w.Body.String())
		}
	}
}
