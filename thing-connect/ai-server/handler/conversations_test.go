package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/ai/conversations"
)

type historyStub struct {
	calls                 int
	err                   error
	user                  int64
	device, id, reference string
	page, size            int
}

func (h *historyStub) List(_ context.Context, u int64, d string, p, s int) (conversations.Page, error) {
	h.calls++
	h.user = u
	h.device = d
	h.page = p
	h.size = s
	return conversations.Page{Items: []conversations.Entry{}, Page: p, PageSize: s}, h.err
}
func (h *historyStub) Detail(_ context.Context, u int64, d, id, ref string) ([]conversations.Message, error) {
	h.calls++
	h.user = u
	h.device = d
	h.id = id
	h.reference = ref
	return []conversations.Message{}, h.err
}
func (h *historyStub) Delete(_ context.Context, u int64, d, id, ref string) error {
	h.calls++
	h.user = u
	h.device = d
	h.id = id
	h.reference = ref
	return h.err
}
func TestConversationRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &historyStub{}
	r := gin.New()
	RegisterConversations(r, "test-secret", h)
	// Use the same signing helper as existing user-facing AI routes.
	call := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("X-Conversation-Reference", "signed-reference")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := call("GET", "/v1/ai/device/device/conversations", ""); w.Code != 401 || h.calls != 0 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/v1/ai/device/device/conversations?page=bad", makeToken(1)); w.Code != 400 || h.calls != 0 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/v1/ai/device/device/conversations?page=2&page_size=20", makeToken(1)); w.Code != 200 || h.user != 1 || h.device != "device" || h.page != 2 || h.size != 20 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Body.String())
	}
	for _, method := range []string{"GET", "DELETE"} {
		w := call(method, "/v1/ai/device/device/conversations/record", makeToken(1))
		if w.Code != 200 || h.id != "record" || h.reference != "signed-reference" {
			t.Fatal(w.Body.String())
		}
	}
	for _, tc := range []struct {
		err    error
		status int
	}{
		{conversations.ErrForbidden, 403}, {conversations.ErrInvalid, 400}, {conversations.ErrNotFound, 404}, {conversations.ErrUpstream, 502}, {errors.New("private database password"), 500},
	} {
		h.err = tc.err
		w := call(http.MethodGet, "/v1/ai/device/device/conversations", makeToken(1))
		if w.Code != tc.status || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
}
