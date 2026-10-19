package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"thing-connect/internal/tirtcapi"
)

// Fixture follows the browser payload and the live cloud validation: TTS model
// and voice must travel together, independently of the LLM's model.
const roleTTSBody = `{
	"name":"测试角色","avatar":"avatar","parent_role_id":"parent",
	"agent_config":{"prompt":"测试提示词","welcome_text":"你好","ali_memory":{"enable":false},"mcp_tools":[],"device_plugins":[],"ali_rag":null},
	"service_config":{"name":"default","type":"custom-compose","config":{
		"llm":{"provider":"ali-llm","provider_params":{"model":"qwen3.7-plus"}},
		"tts":{"provider":"ali-tts","provider_params":{"model":"cosyvoice-v3-flash","voice":"longanli_v3","rate":1,"pitch":1,"volume":70,"language_hints":["zh"]}},
		"future_component":{"setting":"preserved"}
	}},
	"user_params":{"description":"测试","voice":"longanli_v3"}
}`

func TestRoleTTSHTTPRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, method, updateResponse string
		detailFails                  bool
	}{
		{name: "create", method: http.MethodPost},
		{name: "create_detail_temporarily_unavailable", method: http.MethodPost, detailFails: true},
		{name: "update_no_data", method: http.MethodPut, updateResponse: `{"code":200}`},
		{name: "update_null_data", method: http.MethodPut, updateResponse: `{"code":200,"data":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input tirtcapi.RoleInput
			if err := json.Unmarshal([]byte(roleTTSBody), &input); err != nil {
				t.Fatal(err)
			}
			stored := tirtcapi.Role{ID: "role-1", Name: input.Name, Avatar: input.Avatar, ParentRoleID: input.ParentRoleID,
				AgentConfig: input.AgentConfig, ServiceConfig: input.ServiceConfig, UserParams: input.UserParams}
			received := make(chan tirtcapi.RoleInput, 2)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					if tc.detailFails {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(w, `{"code":503}`)
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": stored})
					}
					return
				}
				if r.Method != tc.method {
					t.Errorf("upstream method=%s, want %s", r.Method, tc.method)
				}
				var got tirtcapi.RoleInput
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				received <- got
				var config tirtcapi.CustomComposeConfig
				if got.ServiceConfig == nil || json.Unmarshal(got.ServiceConfig.Config, &config) != nil ||
					config.TTS == nil || config.TTS.ProviderParams.Model == "" || config.TTS.ProviderParams.Voice == "" {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"code":400,"msg":"TTS配置无效: model和voice不能为空"}`)
					return
				}
				if tc.method == http.MethodPost {
					_, _ = io.WriteString(w, `{"code":200,"data":{"id":"role-1"}}`)
				} else {
					_, _ = io.WriteString(w, tc.updateResponse)
				}
			}))
			defer srv.Close()
			store := &mockUserRoleStore{roles: map[int64][]string{}}
			path := "/v1/ai/roles"
			if tc.method == http.MethodPut {
				store.roles[1] = []string{"role-1"}
				path += "/role-1"
			}
			client := tirtcapi.NewAgentAPIClient(tirtcapi.AgentAPIConfig{BaseURL: srv.URL}, srv.Client())
			router := setupTestRouter(&AgentHandler{agentAPI: client, userRoleStore: store})
			w := doRequest(router, tc.method, path, roleTTSBody, makeToken(1))
			if result := parseJSON(t, w); w.Code != 200 || result.Code != 200 {
				t.Fatalf("valid TTS pair failed: HTTP=%d %s", w.Code, w.Body.String())
			}
			select {
			case got := <-received:
				var wantConfig, gotConfig any
				_ = json.Unmarshal(input.ServiceConfig.Config, &wantConfig)
				_ = json.Unmarshal(got.ServiceConfig.Config, &gotConfig)
				if !reflect.DeepEqual(gotConfig, wantConfig) || got.ParentRoleID != input.ParentRoleID {
					t.Fatal("HTTP forwarding lost model, voice, extra settings or parent")
				}
			default:
				t.Fatal("no cloud write")
			}
			if !reflect.DeepEqual(store.roles[1], []string{"role-1"}) {
				t.Fatalf("local ownership lost or duplicated: %v", store.roles[1])
			}
			var result struct {
				Data tirtcapi.Role `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Data.ID != "role-1" {
				t.Fatal("response lost the saved role ID")
			}
			if !tc.detailFails {
				var config tirtcapi.CustomComposeConfig
				if result.Data.ServiceConfig == nil || json.Unmarshal(result.Data.ServiceConfig.Config, &config) != nil || config.TTS == nil ||
					config.TTS.ProviderParams.Model != "cosyvoice-v3-flash" || config.TTS.ProviderParams.Voice != "longanli_v3" {
					t.Fatal("saved response lost the TTS pair")
				}
			}
		})
	}
}

func TestRoleCloudValidationFailureDoesNotSaveLocalOwnership(t *testing.T) {
	for _, missing := range []string{"model", "voice"} {
		t.Run(missing, func(t *testing.T) {
			body := roleTTSBody
			if missing == "model" {
				body = strings.Replace(body, `"model":"cosyvoice-v3-flash",`, "", 1)
			} else {
				body = strings.Replace(body, `"voice":"longanli_v3",`, "", 1)
			}
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var got tirtcapi.RoleInput
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				var config tirtcapi.CustomComposeConfig
				if got.ServiceConfig == nil || json.Unmarshal(got.ServiceConfig.Config, &config) != nil || config.TTS == nil {
					t.Error("invalid TTS test fixture")
				} else if (missing == "model" && config.TTS.ProviderParams.Model != "") || (missing == "voice" && config.TTS.ProviderParams.Voice != "") {
					t.Error("fixture did not reproduce the missing field")
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"code":400,"msg":"TTS配置无效: model和voice不能为空; upstream-private-detail"}`)
			}))
			defer srv.Close()
			store := &mockUserRoleStore{roles: map[int64][]string{}}
			client := tirtcapi.NewAgentAPIClient(tirtcapi.AgentAPIConfig{BaseURL: srv.URL}, srv.Client())
			w := doRequest(setupTestRouter(&AgentHandler{agentAPI: client, userRoleStore: store}), http.MethodPost, "/v1/ai/roles", body, makeToken(1))
			result := parseJSON(t, w)
			if w.Code != http.StatusBadGateway || result.Code != 50200 || calls.Load() != 1 {
				t.Fatalf("unexpected cloud validation result: HTTP=%d body=%s calls=%d", w.Code, w.Body.String(), calls.Load())
			}
			if len(store.roles[1]) != 0 {
				t.Fatal("failed cloud creation left a local role mapping")
			}
			if strings.Contains(w.Body.String(), "upstream-private-detail") || strings.Contains(w.Body.String(), "model和voice") {
				t.Fatal("raw cloud error leaked to the public response")
			}
		})
	}
}

func TestRoleTTSUpdateChecksOwnershipBeforeCloudWrite(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"code":200}`)
	}))
	defer srv.Close()
	store := &mockUserRoleStore{roles: map[int64][]string{1: {"role-1"}}}
	client := tirtcapi.NewAgentAPIClient(tirtcapi.AgentAPIConfig{BaseURL: srv.URL}, srv.Client())
	w := doRequest(setupTestRouter(&AgentHandler{agentAPI: client, userRoleStore: store}), http.MethodPut, "/v1/ai/roles/role-1", roleTTSBody, makeToken(2))
	if w.Code != http.StatusForbidden || calls.Load() != 0 {
		t.Fatalf("unowned role reached cloud: HTTP=%d calls=%d", w.Code, calls.Load())
	}
}
