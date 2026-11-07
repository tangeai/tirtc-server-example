package adminclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoleTypesClientContract(t *testing.T) {
	status, body := 200, `{"code":200,"data":{"items":[{"id":"one","name":"角色","prompt":"设定","revision":2}]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Key") != "test-key" || r.URL.Path != "/v1/internal/ai-role-types" {
			t.Error("invalid internal request")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	c, err := NewRoleTypes(server.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.Published(context.Background())
	if err != nil || len(items) != 1 || items[0].Prompt != "设定" {
		t.Fatalf("items=%v err=%v", items, err)
	}
	for _, bad := range []string{`{"code":50000}`, `{"code":200,"data":{}}`, `not json`} {
		body = bad
		if _, err = c.Published(context.Background()); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
	status = 503
	if _, err = c.Published(context.Background()); err == nil {
		t.Fatal("HTTP failure accepted")
	}
}
