package main

import (
	"encoding/json"
	"errors"

	"thing-connect/internal/config"
	"thing-connect/internal/dynamicconfig"
	voiphandler "thing-connect/voip-server/handler"
)

func voipDynamicConfig(client *dynamicconfig.Client, fallbackTiRTC config.TirtcCfg, server *voiphandler.Server) (*dynamicconfig.Client, []dynamicconfig.Ref, error) {
	if client == nil {
		return nil, nil, errors.New("dynamic config client is unavailable")
	}
	refs := []dynamicconfig.Ref{
		{Namespace: "voip-server", Key: "wechat.apps", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				DefaultAppID string `json:"default_app_id"`
				Apps         map[string]struct {
					Enabled bool   `json:"enabled"`
					ModelID string `json:"model_id"`
				} `json:"apps"`
			}
			var secrets struct {
				Apps map[string]struct {
					Secret         string `json:"secret"`
					Token          string `json:"token"`
					EncodingAESKey string `json:"encoding_aes_key"`
				} `json:"apps"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			_ = json.Unmarshal(snapshot.Secrets, &secrets)
			apps := make(map[string]config.WxApp, len(value.Apps))
			for appID, public := range value.Apps {
				private := secrets.Apps[appID]
				enabled := public.Enabled
				apps[appID] = config.WxApp{Enabled: &enabled, ModelID: public.ModelID, Secret: private.Secret, Token: private.Token, EncodingAESKey: private.EncodingAESKey}
			}
			current := server.Config()
			current.Wechat = config.WechatCfg{DefaultAppID: value.DefaultAppID, Apps: apps}
			server.UpdateConfig(current)
			return nil
		}},
		{Namespace: "common", Key: "tirtc", Reload: "restart", Apply: func(snapshot dynamicconfig.Snapshot) error {
			resolved, err := dynamicconfig.ResolveTiRTC(snapshot, fallbackTiRTC)
			if err != nil {
				return err
			}
			current := server.Config()
			current.Tirtc = resolved
			server.UpdateConfig(current)
			return nil
		}},
	}
	return client, refs, nil
}
