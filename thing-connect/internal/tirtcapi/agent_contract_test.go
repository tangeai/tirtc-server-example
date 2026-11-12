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

func TestMCPCreatePersistsExplicitDisabledState(t *testing.T) {
	for _, mode := range []string{"disabled", "enabled", "default", "update-failure", "cancelled", "cleanup-failure", "delayed-read", "unconfirmed"} {
		t.Run(mode, func(t *testing.T) {
			enabled, deleted, updates := true, false, 0
			reads := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPost:
					// This cloud version defaults persistence to true even when the response
					// echoes false. The returned DTO alone is not evidence of persistence.
					_, _ = io.WriteString(w, `{"code":200,"data":{"id":"mcp-created","enabled":false}}`)
				case http.MethodPut:
					updates++
					if mode == "cancelled" {
						cancel()
						return
					}
					if mode == "update-failure" || mode == "cleanup-failure" {
						_, _ = io.WriteString(w, `{"code":500}`)
						return
					}
					var req AppMCPToolUpdateRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.Enabled == nil || *req.Enabled {
						t.Error("disabled create must persist false")
					}
					enabled = false
					_, _ = io.WriteString(w, `{"code":200}`)
				case http.MethodGet:
					reads++
					if mode == "unconfirmed" || (mode == "delayed-read" && reads < 3) {
						_, _ = io.WriteString(w, `{"code":200,"data":{"id":"mcp-created","enabled":true}}`)
						return
					}
					_, _ = fmt.Fprintf(w, `{"code":200,"data":{"id":"mcp-created","enabled":%t,"config":{"name":"test","url":"https://example.invalid"}}}`, enabled)
				case http.MethodDelete:
					deleted = true
					if mode == "cleanup-failure" {
						_, _ = io.WriteString(w, `{"code":500,"msg":"cleanup failed"}`)
						return
					}
					_, _ = io.WriteString(w, `{"code":200}`)
				default:
					t.Errorf("unexpected method %s", r.Method)
				}
			})
			disabled := false
			req := AppMCPToolCreateRequest{Config: &MCPToolExternalRuntimeConfig{Name: "test", URL: "https://example.invalid"}, Enabled: &disabled}
			if mode == "default" {
				req.Enabled = nil
			}
			if mode == "enabled" {
				yes := true
				req.Enabled = &yes
			}
			item, err := client.CreateAppMCPTool(ctx, req)
			switch mode {
			case "disabled", "delayed-read":
				if err != nil || item == nil || item.Enabled || enabled || updates != 1 || deleted {
					t.Fatalf("not persisted: item=%+v enabled=%v updates=%d deleted=%v err=%v", item, enabled, updates, deleted, err)
				}
			case "default", "enabled":
				if err != nil || updates != 0 || deleted {
					t.Fatalf("unexpected extra writes: %d %v %v", updates, deleted, err)
				}
			default:
				if err == nil || item != nil || !deleted {
					t.Fatalf("failed provisioning not compensated: item=%+v deleted=%v err=%v", item, deleted, err)
				}
				if mode == "cleanup-failure" && !strings.Contains(err.Error(), "cleanup") {
					t.Fatalf("cleanup failure was lost: %v", err)
				}
			}
		})
	}
}

func TestRoleRegionCreateAndRead(t *testing.T) {
	for _, region := range []string{"", "cn01", "sg01"} {
		t.Run(region, func(t *testing.T) {
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if region == "" {
						if _, ok := body["region"]; ok {
							t.Error("legacy creation should omit region")
						}
					} else if body["region"] != region {
						t.Error("region lost in request")
					}
					_, _ = io.WriteString(w, `{"code":200,"data":{"id":"regional-role"}}`)
					return
				}
				_, _ = fmt.Fprintf(w, `{"code":200,"data":{"id":"regional-role","region":%q}}`, region)
			})
			created, err := client.CreateRole(context.Background(), RoleInput{Name: "region test", Region: region})
			if err != nil {
				t.Fatal(err)
			}
			got, err := client.GetRole(context.Background(), created.ID)
			if err != nil || got == nil || got.Region != region {
				t.Fatalf("region response lost: role=%+v err=%v", got, err)
			}
		})
	}
}

func TestRoleVideoSettingForwarding(t *testing.T) {
	for _, raw := range []string{`{}`, `{"video":{"enable":true}}`, `{"video":{"enable":false}}`} {
		t.Run(raw, func(t *testing.T) {
			var config AgentConfig
			if err := json.Unmarshal([]byte(raw), &config); err != nil {
				t.Fatal(err)
			}
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					AgentConfig map[string]json.RawMessage `json:"agent_config"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				var expected map[string]json.RawMessage
				_ = json.Unmarshal([]byte(raw), &expected)
				if string(body.AgentConfig["video"]) != string(expected["video"]) {
					t.Errorf("video changed: %s", body.AgentConfig["video"])
				}
				_, _ = fmt.Fprintf(w, `{"code":200,"data":{"id":"role-1","agent_config":%s}}`, raw)
			})
			role, err := client.UpdateRole(context.Background(), "role-1", RoleInput{Name: "测试", AgentConfig: &config})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(role.AgentConfig.Video, config.Video) {
				t.Fatalf("video response changed: %+v", role.AgentConfig.Video)
			}
		})
	}
}

func TestRoleWelcomeSettingForwarding(t *testing.T) {
	for _, raw := range []string{`{}`, `{"welcome_text":"你好","welcome_prompt":""}`, `{"welcome_text":"","welcome_prompt":"自然地打招呼"}`, `{"welcome_text":"","welcome_prompt":""}`} {
		t.Run(raw, func(t *testing.T) {
			var config AgentConfig
			if err := json.Unmarshal([]byte(raw), &config); err != nil {
				t.Fatal(err)
			}
			client := contractClient(t, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					AgentConfig map[string]json.RawMessage `json:"agent_config"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				var expected map[string]json.RawMessage
				_ = json.Unmarshal([]byte(raw), &expected)
				for _, key := range []string{"welcome_text", "welcome_prompt"} {
					if string(body.AgentConfig[key]) != string(expected[key]) {
						t.Errorf("%s changed: %s", key, body.AgentConfig[key])
					}
				}
				_, _ = fmt.Fprintf(w, `{"code":200,"data":{"id":"role-1","agent_config":%s}}`, raw)
			})
			role, err := client.UpdateRole(context.Background(), "role-1", RoleInput{Name: "测试", AgentConfig: &config})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(role.AgentConfig, &config) {
				t.Fatalf("welcome response changed: %+v", role.AgentConfig)
			}
		})
	}
}
