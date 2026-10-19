package tirtcapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func contractClient(t *testing.T, handler http.HandlerFunc) *AgentAPIClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewAgentAPIClient(AgentAPIConfig{BaseURL: srv.URL, AppID: "test-app", AccessKeyID: "test-key", SecretKeyID: "test-secret"}, srv.Client())
}

func TestResourceUpdatesWithoutData(t *testing.T) {
	for _, body := range []string{`{"code":200}`, `{"code":200,"data":null}`} {
		t.Run(body, func(t *testing.T) {
			writes := 0
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `{"code":200,"data":{"id":"mcp-1","enabled":true,"config":{"name":"weather","url":"https://example.com/mcp"}}}`)
					return
				}
				if r.Method != http.MethodPut {
					t.Errorf("unexpected method %s", r.Method)
				}
				writes++
				_, _ = io.WriteString(w, body)
			})
			mcp, err := client.UpdateAppMCPTool(context.Background(), "mcp-1", AppMCPToolUpdateRequest{Config: &MCPToolExternalRuntimeConfig{Name: "renamed", URL: "https://example.com/mcp"}})
			if err != nil || mcp == nil || mcp.ID != "mcp-1" || !mcp.Enabled || mcp.Config == nil || mcp.Config.Name != "renamed" {
				t.Errorf("MCP update lost saved data: item=%+v err=%v", mcp, err)
			}
			plugin, err := client.UpdateDevicePlugin(context.Background(), "plugin-1", DevicePluginUpdateRequest{Name: "light", Action: "set_light", InputParams: []DevicePluginParam{{Name: "level", Type: "integer"}}})
			if err != nil || plugin == nil || plugin.ID != "plugin-1" || plugin.Name != "light" || plugin.Action != "set_light" || len(plugin.InputParams) != 1 {
				t.Errorf("plugin update lost saved data: item=%+v err=%v", plugin, err)
			}
			if writes != 2 {
				t.Errorf("writes=%d, want 2", writes)
			}
		})
	}
}

func TestKnowledgeUpdatePreservesExplicitEmptyFields(t *testing.T) {
	for _, body := range []string{`{"document_ids":[]}`, `{"description":""}`, `{"name":"renamed"}`} {
		t.Run(body, func(t *testing.T) {
			var sent map[string]any
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					t.Error(err)
				}
				_, _ = io.WriteString(w, `{"code":200,"data":{"index_id":"idx-1"}}`)
			})
			var input UpdateKnowledgeIndexRequest
			if err := json.Unmarshal([]byte(body), &input); err != nil {
				t.Fatal(err)
			}
			if _, err := client.UpdateKnowledgeIndex(context.Background(), "idx-1", input); err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			_ = json.Unmarshal([]byte(body), &want)
			if !reflect.DeepEqual(sent, want) {
				t.Fatalf("forwarded %v, want %v", sent, want)
			}
		})
	}
}

func TestKnowledgePageNumber(t *testing.T) {
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query(); got.Get("page_number") != "3" || got.Get("page_size") != "2" || got.Has("page") {
			t.Errorf("incorrect pagination: %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"code":200,"data":{"items":[],"total_count":9}}`)
	})
	if _, total, err := client.ListKnowledgeIndexes(context.Background(), 3, 2); err != nil || total != 9 {
		t.Fatalf("indexes: total=%d err=%v", total, err)
	}
	if _, total, err := client.ListKnowledgeDocuments(context.Background(), "idx-1", 3, 2); err != nil || total != 9 {
		t.Fatalf("documents: total=%d err=%v", total, err)
	}
}

func TestKnowledgeFilesTraversesCursor(t *testing.T) {
	calls := 0
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != knowledgeFilesPath {
			t.Errorf("path=%s", r.URL.Path)
		}
		switch r.URL.Query().Get("next_token") {
		case "":
			_, _ = io.WriteString(w, `{"code":200,"data":{"items":[{"file_id":"other-user"}],"has_next":true,"next_token":"a+b/= token"}}`)
		case "a+b/= token":
			_, _ = io.WriteString(w, `{"code":200,"data":{"items":[{"file_id":"my-file"}],"has_next":false}}`)
		default:
			t.Errorf("incorrect cursor escaping: %s", r.URL.RawQuery)
			_, _ = io.WriteString(w, `{"code":500}`)
		}
	})
	files, err := client.ListKnowledgeFiles(context.Background())
	if err != nil || len(files) != 2 || calls != 2 || files[1].FileID != "my-file" {
		t.Fatalf("files=%+v calls=%d err=%v", files, calls, err)
	}
}

func TestKnowledgeFilesDoesNotReturnPartialResults(t *testing.T) {
	for _, next := range []string{`{"code":500}`, `{"code":200,"data":{"has_next":true,"next_token":"repeat"}}`, `{"code":200,"data":{"has_next":true}}`} {
		t.Run(next, func(t *testing.T) {
			calls := 0
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls > 3 {
					_, _ = io.WriteString(w, `{"code":500}`)
					return
				}
				if calls == 1 {
					_, _ = io.WriteString(w, `{"code":200,"data":{"items":[{"file_id":"first"}],"has_next":true,"next_token":"repeat"}}`)
				} else {
					_, _ = io.WriteString(w, next)
				}
			})
			files, err := client.ListKnowledgeFiles(context.Background())
			if err == nil || files != nil || calls > 3 {
				t.Fatalf("partial/looping traversal: files=%v calls=%d err=%v", files, calls, err)
			}
		})
	}
}

func TestRoleListAcceptsArrayAndLegacyPage(t *testing.T) {
	for _, data := range []string{`[{"id":"role-1"}]`, `{"items":[{"id":"role-1"}],"total":1}`} {
		t.Run(data, func(t *testing.T) {
			client := newDeleteTestClient(t, http.StatusOK, fmt.Sprintf(`{"code":200,"data":%s}`, data))
			roles, _, err := client.ListRoles(context.Background(), 1, 10)
			if err != nil || len(roles) != 1 || roles[0].ID != "role-1" {
				t.Fatalf("roles=%v err=%v", roles, err)
			}
		})
	}
}

func TestRoleUpdateKeepsCompleteConfiguration(t *testing.T) {
	input := RoleInput{Name: "客服", Avatar: "https://example.com/avatar.png", ParentRoleID: "parent", AgentConfig: &AgentConfig{AliMemory: &AliMemoryCfg{Enable: false}}, ServiceConfig: &ServiceConfig{Type: "coze", Config: json.RawMessage(`{"app_id":"app","bot_id":"bot","api_key":"secret"}`)}}
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		var got RoleInput
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.ParentRoleID != "parent" || got.ServiceConfig.Type != "coze" || !strings.Contains(string(got.ServiceConfig.Config), `"api_key":"secret"`) {
			t.Errorf("lost role configuration")
		}
		_, _ = io.WriteString(w, `{"code":200}`)
	})
	role, err := client.UpdateRole(context.Background(), "role-1", input)
	if err != nil || role == nil || role.ParentRoleID != input.ParentRoleID || role.Avatar != input.Avatar || role.ID != "role-1" {
		t.Fatalf("incomplete saved role: %+v err=%v", role, err)
	}
}

func TestVoiceFiltersAreEncoded(t *testing.T) {
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("language") != "zh+en" || r.URL.Query().Get("model") != "model/1&2" {
			t.Errorf("filters not encoded: %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"code":200,"data":{"items":[{"id":"voice-1"}]}}`)
	})
	voices, err := client.ListVoices(context.Background(), "zh+en", "model/1&2")
	if err != nil || len(voices) != 1 {
		t.Fatalf("voices=%v err=%v", voices, err)
	}
}

func TestMCPEnableOnlyUpdatePreservesConfig(t *testing.T) {
	client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"code":200,"data":{"id":"mcp-1","enabled":true,"config":{"name":"weather","url":"https://example.com/mcp","type":"streamableHttp"}}}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !reflect.DeepEqual(body, map[string]any{"enabled": false}) {
			t.Errorf("unexpected MCP patch: %+v", body)
		}
		_, _ = io.WriteString(w, `{"code":200}`)
	})
	disabled := false
	item, err := client.UpdateAppMCPTool(context.Background(), "mcp-1", AppMCPToolUpdateRequest{Enabled: &disabled})
	if err != nil || item == nil || item.Enabled || item.Config == nil || item.Config.Type != "streamableHttp" {
		t.Fatalf("partial MCP update lost fields: %+v err=%v", item, err)
	}
}

func TestResourceUpdateFailuresRemainFailures(t *testing.T) {
	for _, body := range []string{`{"code":500}`, `broken JSON`} {
		client := newDeleteTestClient(t, 200, body)
		enabled := true
		if _, err := client.UpdateAppMCPTool(context.Background(), "mcp-1", AppMCPToolUpdateRequest{Config: &MCPToolExternalRuntimeConfig{Name: "name", URL: "https://example.com"}, Enabled: &enabled}); err == nil {
			t.Errorf("MCP accepted %s", body)
		}
		if _, err := client.UpdateDevicePlugin(context.Background(), "plugin-1", DevicePluginUpdateRequest{Name: "light", Action: "set_light"}); err == nil {
			t.Errorf("plugin accepted %s", body)
		}
	}
}
