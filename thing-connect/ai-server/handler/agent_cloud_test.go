package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"thing-connect/internal/model"
	"thing-connect/internal/tirtcapi"
)

func TestResourceHTTPContract(t *testing.T) {
	for _, tc := range []struct{ path, resourceType, body string }{
		{"mcp/app-tools", "mcp", `{"config":{"name":"updated","url":"https://example.com/mcp","enable":false},"enabled":false}`},
		{"plugins", "device_plugin", `{"name":"updated","action":"set_light"}`},
		{"knowledge/indexes", "kb", `{"document_ids":[]}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			var received map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut {
					t.Errorf("method=%s", r.Method)
				}
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Error(err)
				}
				if tc.resourceType == "kb" {
					_, _ = io.WriteString(w, `{"code":200,"data":{"index_id":"mine"}}`)
				} else {
					_, _ = io.WriteString(w, `{"code":200}`)
				}
			}))
			defer srv.Close()
			store := &mockUserResourceStore{rows: map[string][]model.UserResource{
				"1:" + tc.resourceType: {{UserID: 1, Type: tc.resourceType, ResourceID: "mine"}},
			}}
			client := tirtcapi.NewAgentAPIClient(tirtcapi.AgentAPIConfig{BaseURL: srv.URL}, srv.Client())
			router := setupTestRouter(&AgentHandler{agentAPI: client, userResourceStore: store})
			w := doRequest(router, http.MethodPut, "/v1/ai/"+tc.path+"/mine", tc.body, makeToken(1))
			if resp := parseJSON(t, w); w.Code != 200 || resp.Code != 200 || resp.Data == nil {
				t.Fatalf("saved resource reported as failure: HTTP=%d %s", w.Code, w.Body.String())
			}
			if tc.resourceType == "kb" {
				ids, ok := received["document_ids"].([]any)
				if !ok || len(ids) != 0 {
					t.Fatalf("clear-all lost in HTTP chain: %v", received)
				}
			}
			if tc.resourceType == "mcp" && received["config"].(map[string]any)["enable"] != false {
				t.Fatal("explicit runtime disable was dropped")
			}
		})
	}
}

func TestFileListFiltersOwnershipAfterAllPages(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("next_token") == "next" {
			_, _ = io.WriteString(w, `{"code":200,"data":{"items":[{"file_id":"mine"}],"has_next":false}}`)
		} else {
			_, _ = io.WriteString(w, `{"code":200,"data":{"items":[{"file_id":"foreign"}],"has_next":true,"next_token":"next"}}`)
		}
	}))
	defer srv.Close()
	store := &mockUserResourceStore{rows: map[string][]model.UserResource{
		"1:kb_file": {{UserID: 1, Type: "kb_file", ResourceID: "mine"}},
	}}
	client := tirtcapi.NewAgentAPIClient(tirtcapi.AgentAPIConfig{BaseURL: srv.URL}, srv.Client())
	w := doRequest(setupTestRouter(&AgentHandler{agentAPI: client, userResourceStore: store}), http.MethodGet, "/v1/ai/knowledge/files", "", makeToken(1))
	var result struct {
		Code int `json:"code"`
		Data struct {
			Items []tirtcapi.KnowledgeFileInfo `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != 200 || calls != 2 || len(result.Data.Items) != 1 || result.Data.Items[0].FileID != "mine" {
		t.Fatalf("lost owned file or leaked foreign file: calls=%d body=%s", calls, w.Body.String())
	}
}
