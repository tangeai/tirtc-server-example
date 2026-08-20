package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	aihandler "thing-connect/ai-server/handler"
	"thing-connect/internal/config"
	"thing-connect/internal/dynamicconfig"
	"thing-connect/internal/model"
	"thing-connect/internal/tirtcapi"
)

type aiDynamicState struct {
	mu            sync.Mutex
	defaultRoleID string
	baseURL       string
	rolesBaseURL  string
	appID         string
	accessKeyID   string
	secretKeyID   string
	quota         map[string]int
	resources     map[string][]model.ResourceRef
	legacy        *aihandler.Server
	agents        *aihandler.AgentHandler
}

func aiDynamicConfig(cfg *config.Config, redisClient *redis.Client, legacy *aihandler.Server, agents *aihandler.AgentHandler) (*dynamicconfig.Client, []dynamicconfig.Ref, error) {
	client, err := dynamicconfig.New(cfg.Admin.ServerURL, cfg.Internal.Key, redisClient)
	if err != nil {
		return nil, nil, err
	}
	state := &aiDynamicState{defaultRoleID: cfg.TirtcAichat.DefaultRoleID, baseURL: cfg.TirtcAichat.BaseURL, rolesBaseURL: cfg.TirtcAichat.RolesBaseURL, appID: cfg.Tirtc.AppID, accessKeyID: cfg.Tirtc.AccessKeyID, secretKeyID: cfg.Tirtc.SecretKeyID, quota: cfg.TirtcAichat.ResourceQuota, resources: cfg.TirtcAichat.DefaultResources, legacy: legacy, agents: agents}
	refs := []dynamicconfig.Ref{
		{Namespace: "ai-server", Key: "ai.role_policy", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				DefaultRoleID string `json:"default_role_id"`
				BaseURL       string `json:"base_url"`
				BaseRoleURL   string `json:"base_role_url"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			state.mu.Lock()
			state.defaultRoleID, state.baseURL, state.rolesBaseURL = value.DefaultRoleID, value.BaseURL, value.BaseRoleURL
			state.apply()
			state.mu.Unlock()
			return nil
		}},
		{Namespace: "ai-server", Key: "ai.resource_policy", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				ResourceQuota    map[string]int                 `json:"resource_quota"`
				DefaultResources map[string][]model.ResourceRef `json:"default_resources"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			state.mu.Lock()
			state.quota, state.resources = value.ResourceQuota, value.DefaultResources
			state.apply()
			state.mu.Unlock()
			return nil
		}},
		{Namespace: "common", Key: "tirtc", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				AppID string `json:"app_id"`
			}
			var secrets struct {
				AccessKeyID string `json:"access_key_id"`
				SecretKeyID string `json:"secret_key_id"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			_ = json.Unmarshal(snapshot.Secrets, &secrets)
			state.mu.Lock()
			state.appID, state.accessKeyID, state.secretKeyID = value.AppID, secrets.AccessKeyID, secrets.SecretKeyID
			state.apply()
			state.mu.Unlock()
			return nil
		}},
	}
	return client, refs, nil
}

func (s *aiDynamicState) apply() {
	s.legacy.UpdateRuntime(s.defaultRoleID, s.baseURL, s.accessKeyID, s.appID, s.secretKeyID)
	if s.agents == nil {
		return
	}
	rolesURL := s.rolesBaseURL
	if rolesURL == "" {
		rolesURL = s.baseURL
	}
	api := tirtcapi.NewAgentAPIClient(tirtcapi.AgentAPIConfig{BaseURL: rolesURL, AppID: s.appID, AccessKeyID: s.accessKeyID, SecretKeyID: s.secretKeyID}, &http.Client{Timeout: 10 * time.Second})
	s.agents.SetRuntime(api, s.defaultRoleID, copyIntMap(s.quota), copyResources(s.resources))
}

func copyIntMap(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
func copyResources(source map[string][]model.ResourceRef) map[string][]model.ResourceRef {
	result := make(map[string][]model.ResourceRef, len(source))
	for key, value := range source {
		result[key] = append([]model.ResourceRef(nil), value...)
	}
	return result
}
