package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"thing-connect/internal/model"
	"thing-connect/internal/tirtcapi"
)

func TestDefaultRolePluginNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/roles/default-role"):
			io.WriteString(w, `{"code":200,"data":{"id":"default-role","agent_config":{"device_plugins":["linked","missing","linked"]}}}`)
		case strings.HasSuffix(r.URL.Path, "/linked"):
			io.WriteString(w, `{"code":200,"data":{"id":"linked","name":"设备呼叫","action":"call","description":"private detail"}}`)
		case strings.HasSuffix(r.URL.Path, "/missing"):
			w.WriteHeader(404)
			io.WriteString(w, `{"code":404}`)
		default:
			t.Errorf("unexpected cloud request: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	client := tirtcapi.NewAgentAPIClient(tirtcapi.AgentAPIConfig{BaseURL: srv.URL}, srv.Client())
	router := setupTestRouter(&AgentHandler{agentAPI: client, defaultRoleID: "default-role", userResourceStore: &mockUserResourceStore{}})
	w := doRequest(router, "GET", "/v1/ai/roles/default", "", makeToken(1))
	var response struct {
		Data struct {
			ID   string              `json:"id"`
			Refs []model.ResourceRef `json:"device_plugin_refs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || response.Data.ID != "default-role" || len(response.Data.Refs) != 2 || response.Data.Refs[0].Name != "设备呼叫" || response.Data.Refs[1].ID != "missing" {
		t.Fatalf("expected names alongside the unchanged role, with missing ID retained: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private detail") {
		t.Fatal("plugin details leaked")
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		w = doRequest(router, method, "/v1/ai/plugins/linked", `{}`, makeToken(1))
		if w.Code != 403 {
			t.Fatalf("display names must not grant resource access: %s got %d", method, w.Code)
		}
	}
}

func TestPluginMissingCloudResource(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusOK} {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			t.Run(http.StatusText(status)+"/"+method, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"code":404,"msg":"not found"}`)
				}))
				defer srv.Close()
				store := &mockUserResourceStore{rows: map[string][]model.UserResource{
					"1:device_plugin": {{UserID: 1, Type: model.ResourceTypeDevicePlugin, ResourceID: "stale-plugin"}},
				}}
				client := tirtcapi.NewAgentAPIClient(tirtcapi.AgentAPIConfig{BaseURL: srv.URL}, srv.Client())
				router := setupTestRouter(&AgentHandler{agentAPI: client, userResourceStore: store})
				w := doRequest(router, method, "/v1/ai/plugins/stale-plugin", "", makeToken(1))
				result := parseJSON(t, w)
				if method == http.MethodGet {
					if w.Code != 404 || result.Code != 40400 || len(store.rows["1:device_plugin"]) != 1 {
						t.Fatalf("missing detail must be 404 without modifying ownership: HTTP=%d %s", w.Code, w.Body.String())
					}
				} else if w.Code != 200 || result.Code != 200 || len(store.rows["1:device_plugin"]) != 0 {
					t.Fatalf("deleting a missing cloud plugin must remove its local index: HTTP=%d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestPluginDeletePreservesIndexOnUnconfirmedCloudFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"auth", 401, `{"code":401,"msg":"private auth details"}`},
		{"server", 500, `{"code":500,"msg":"private server details"}`},
		{"in_use", 400, `{"code":400,"msg":"plugin referenced by role"}`},
		{"gateway_404", 404, `<html>not found</html>`},
		{"mismatched_status", 500, `{"code":404,"msg":"not found"}`},
		{"malformed_success", 200, `broken JSON`},
		{"missing_success_code", 200, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			store := &mockUserResourceStore{rows: map[string][]model.UserResource{
				"1:device_plugin": {{UserID: 1, Type: model.ResourceTypeDevicePlugin, ResourceID: "plugin-1"}},
			}}
			client := tirtcapi.NewAgentAPIClient(tirtcapi.AgentAPIConfig{BaseURL: srv.URL}, srv.Client())
			w := doRequest(setupTestRouter(&AgentHandler{agentAPI: client, userResourceStore: store}), http.MethodDelete, "/v1/ai/plugins/plugin-1", "", makeToken(1))
			if result := parseJSON(t, w); w.Code != 502 || result.Code != 50200 || len(store.rows["1:device_plugin"]) != 1 {
				t.Fatalf("unconfirmed deletion must retain local index: HTTP=%d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPluginDeleteLocalFailureCanRetryAfterCloudDeletion(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_, _ = io.WriteString(w, `{"code":200}`)
		} else {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":404,"msg":"not found"}`)
		}
	}))
	defer srv.Close()
	store := &mockUserResourceStore{removeErr: errors.New("database temporarily unavailable"), rows: map[string][]model.UserResource{
		"1:device_plugin": {{UserID: 1, Type: model.ResourceTypeDevicePlugin, ResourceID: "plugin-1"}},
	}}
	client := tirtcapi.NewAgentAPIClient(tirtcapi.AgentAPIConfig{BaseURL: srv.URL}, srv.Client())
	router := setupTestRouter(&AgentHandler{agentAPI: client, userResourceStore: store})
	w := doRequest(router, http.MethodDelete, "/v1/ai/plugins/plugin-1", "", makeToken(1))
	if w.Code != 500 || parseJSON(t, w).Code != 50000 || len(store.rows["1:device_plugin"]) != 1 {
		t.Fatalf("local failure must be retryable, not reported as success: HTTP=%d %s", w.Code, w.Body.String())
	}
	store.removeErr = nil
	w = doRequest(router, http.MethodDelete, "/v1/ai/plugins/plugin-1", "", makeToken(1))
	if w.Code != 200 || parseJSON(t, w).Code != 200 || len(store.rows["1:device_plugin"]) != 0 {
		t.Fatalf("retry must converge after cloud resource was already removed: HTTP=%d %s", w.Code, w.Body.String())
	}
}
