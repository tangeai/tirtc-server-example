package tirtcapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"thing-connect/internal/ai/conversations"
)

func TestConversationSignedRequests(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Tg-App-Id") != "app" || !strings.HasPrefix(r.Header.Get("Authorization"), "TGV1-HMAC-SHA256 ") {
			t.Error("missing signed application request")
		}
		switch r.URL.Path {
		case "/ai/aigcrtc/conversation/list":
			if r.URL.Query().Get("device_id") != "device &/+" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("page_size") != "10" {
				t.Errorf("query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"code":200,"data":[{"id":"id &/+","device_id":"device &/+","assistant_id":"role","started_at":null}]}`))
		case "/ai/aigcrtc/conversation/detail":
			if r.URL.Query().Get("id") != "id &/+" {
				t.Error("id encoding")
			}
			_, _ = w.Write([]byte(`{"code":200,"data":[{"role":"user","content":"你好","time":"2026-11-07T01:00:00Z"}]}`))
		case "/ai/aigcrtc/conversation/delete":
			if r.Method != http.MethodDelete || r.URL.Query().Get("id") != "id &/+" {
				t.Error("delete request")
			}
			_, _ = w.Write([]byte(`{"code":200}`))
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewAgentAPIClient(AgentAPIConfig{BaseURL: srv.URL, AppID: "app", AccessKeyID: "key", SecretKeyID: "secret"}, srv.Client())
	rows, err := c.ListConversations(context.Background(), "device &/+", 2, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
	messages, err := c.ConversationDetail(context.Background(), rows[0].ID)
	if err != nil || len(messages) != 1 || messages[0].Content != "你好" {
		t.Fatalf("%v %v", messages, err)
	}
	if err = c.DeleteConversation(context.Background(), rows[0].ID); err != nil || calls != 3 {
		t.Fatalf("%d %v", calls, err)
	}
}
func TestConversationResponseErrors(t *testing.T) {
	for _, tc := range []struct {
		body    string
		status  int
		missing bool
	}{
		{`{"code":500,"msg":"private SQL error"}`, 200, false}, {`{"code":404}`, 200, true}, {`not JSON`, 502, false}, {`{"code":200}`, 200, false}, {`{"code":200,"data":{}}`, 200, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			c := newDeleteTestClient(t, tc.status, tc.body)
			_, err := c.ConversationDetail(context.Background(), "id")
			if err == nil || strings.Contains(err.Error(), "private SQL") {
				t.Fatalf("unsafe response: %v", err)
			}
			if errors.Is(err, conversations.ErrNotFound) != tc.missing {
				t.Fatal(err)
			}
		})
	}
}
