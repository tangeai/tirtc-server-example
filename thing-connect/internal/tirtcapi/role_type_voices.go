package tirtcapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"thing-connect/internal/ai/roletypes"
	"thing-connect/internal/config"
	"thing-connect/internal/dynamicconfig"
)

type roleTypeConfigLoader interface {
	Load(context.Context, string, string) (dynamicconfig.Snapshot, error)
}
type RoleTypeVoices struct {
	configs roleTypeConfigLoader
	http    *http.Client
}

func NewRoleTypeVoices(configs roleTypeConfigLoader) *RoleTypeVoices {
	return &RoleTypeVoices{configs: configs, http: &http.Client{Timeout: 8 * time.Second}}
}
func (s *RoleTypeVoices) Voices(ctx context.Context) ([]roletypes.Voice, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	snapshot, err := s.configs.Load(ctx, "common", "tirtc")
	if err != nil {
		return nil, err
	}
	creds, err := dynamicconfig.ResolveTiRTC(snapshot, config.TirtcCfg{})
	if err != nil {
		return nil, err
	}
	policy, err := s.configs.Load(ctx, "ai-server", "ai.role_policy")
	if err != nil {
		return nil, err
	}
	var value struct {
		BaseURL string `json:"base_role_url"`
	}
	if err = json.Unmarshal(policy.Value, &value); err != nil || value.BaseURL == "" {
		return nil, errors.New("role API URL unavailable")
	}
	api := NewAgentAPIClient(AgentAPIConfig{BaseURL: value.BaseURL, AppID: creds.AppID, AccessKeyID: creds.AccessKeyID, SecretKeyID: creds.SecretKeyID}, s.http)
	voices, err := api.ListVoices(ctx, "", "")
	if err != nil {
		return nil, err
	}
	out := []roletypes.Voice{}
	for _, voice := range voices {
		out = append(out, roletypes.Voice{ID: voice.ID, Name: voice.Name, Model: voice.Model, Languages: voice.Languages, SampleURL: voice.SampleURL})
	}
	return out, nil
}
