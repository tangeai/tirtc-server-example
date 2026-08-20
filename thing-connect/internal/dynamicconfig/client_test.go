package dynamicconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testInternalKey = "01234567890123456789012345678901"

func TestNewValidatesAdminEndpointAndInternalKey(t *testing.T) {
	tests := []struct {
		url string
		key string
	}{
		{url: "", key: testInternalKey},
		{url: "ftp://admin.example.com", key: testInternalKey},
		{url: "http://user:pass@admin.example.com", key: testInternalKey},
		{url: "http://admin.example.com", key: "short"},
	}
	for _, test := range tests {
		if _, err := New(test.url, test.key, nil); err == nil {
			t.Errorf("New(%q, key length %d) accepted invalid input", test.url, len(test.key))
		}
	}
	client, err := New(" https://admin.example.com/// ", testInternalKey, nil)
	if err != nil || client.baseURL != "https://admin.example.com" {
		t.Fatalf("valid endpoint rejected or not normalized: client=%+v err=%v", client, err)
	}
}

func TestLoadAuthenticatesAndDecodesSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Internal-Key") != testInternalKey {
			t.Errorf("missing internal key header")
		}
		if request.URL.Path != "/v1/internal/configs/user-server/smtp" || request.URL.Query().Get("scope_type") != "global" {
			t.Errorf("unexpected request URL: %s", request.URL.String())
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"code":200,"msg":"ok","data":{"value":{"enabled":false},"secrets":{},"revision":7}}`))
	}))
	defer server.Close()
	client, err := New(server.URL, testInternalKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Load(context.Background(), "user-server", "smtp")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 7 || string(snapshot.Value) != `{"enabled":false}` || string(snapshot.Secrets) != `{}` {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestLoadRejectsHTTPAndEnvelopeFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "http error", status: http.StatusUnauthorized, body: `{}`},
		{name: "bad json", status: http.StatusOK, body: `{`},
		{name: "business error", status: http.StatusOK, body: `{"code":500,"msg":"bad"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New(server.URL, testInternalKey, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Load(context.Background(), "system", "mfa.policy"); err == nil {
				t.Fatal("invalid admin response accepted")
			}
		})
	}
}

func TestApplyOnlyCommitsSuccessfulIncreasingRevisions(t *testing.T) {
	snapshot := Snapshot{Value: json.RawMessage(`{"enabled":true}`), Revision: 0}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(apiResponse{Code: 200, Msg: "ok", Data: snapshot})
	}))
	defer server.Close()
	client, err := New(server.URL, testInternalKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	applyCount := 0
	failApply := false
	ref := Ref{Namespace: "system", Key: "mfa.policy", Apply: func(got Snapshot) error {
		applyCount++
		if failApply {
			return context.Canceled
		}
		if got.Revision != snapshot.Revision {
			t.Fatalf("applied revision %d, want %d", got.Revision, snapshot.Revision)
		}
		return nil
	}}

	client.apply(context.Background(), ref, true)
	if applyCount != 0 {
		t.Fatal("registry default revision was applied over YAML")
	}
	snapshot.Revision = 2
	client.apply(context.Background(), ref, false)
	client.apply(context.Background(), ref, false)
	if applyCount != 1 || client.Revisions()["system/mfa.policy"] != 2 {
		t.Fatalf("revision deduplication failed: count=%d revisions=%v", applyCount, client.Revisions())
	}
	failApply = true
	snapshot.Revision = 3
	client.apply(context.Background(), ref, false)
	if client.Revisions()["system/mfa.policy"] != 2 {
		t.Fatal("failed application advanced the revision")
	}
	failApply = false
	snapshot.Revision = 4
	client.apply(context.Background(), ref, false)
	revisions := client.Revisions()
	if applyCount != 3 || revisions["system/mfa.policy"] != 4 {
		t.Fatalf("later valid revision was not applied: count=%d revisions=%v", applyCount, revisions)
	}
	delete(revisions, "system/mfa.policy")
	if len(client.Revisions()) != 1 {
		t.Fatal("Revisions exposed the client's internal map")
	}
	if !strings.Contains(client.baseURL, "127.0.0.1") {
		t.Fatal("test server URL was not retained")
	}
}
