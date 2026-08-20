package main

import (
	"encoding/json"

	"github.com/redis/go-redis/v9"

	callhandler "thing-connect/call-server/handler"
	"thing-connect/internal/config"
	"thing-connect/internal/dynamicconfig"
)

func callDynamicConfig(cfg *config.Config, redisClient *redis.Client, server *callhandler.Server) (*dynamicconfig.Client, []dynamicconfig.Ref, error) {
	client, err := dynamicconfig.New(cfg.Admin.ServerURL, cfg.Internal.Key, redisClient)
	if err != nil {
		return nil, nil, err
	}
	refs := []dynamicconfig.Ref{
		{Namespace: "call-server", Key: "call.contact_policy", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				Max int `json:"max_contacts_per_device"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			current := server.Config()
			current.Service.MaxContactsPerDevice = value.Max
			server.UpdateConfig(current)
			return nil
		}},
		{Namespace: "call-server", Key: "call.room_policy", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				Hours int `json:"room_ttl_hours"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			current := server.Config()
			current.Service.RoomTTLHours = value.Hours
			server.UpdateConfig(current)
			return nil
		}},
		{Namespace: "common", Key: "tirtc", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				Endpoint string `json:"endpoint"`
				AppID    string `json:"app_id"`
			}
			var secrets struct {
				AccessKeyID string `json:"access_key_id"`
				SecretKeyID string `json:"secret_key_id"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			_ = json.Unmarshal(snapshot.Secrets, &secrets)
			current := server.Config()
			current.Tirtc = config.TirtcCfg{Endpoint: value.Endpoint, AppID: value.AppID, AccessKeyID: secrets.AccessKeyID, SecretKeyID: secrets.SecretKeyID}
			server.UpdateConfig(current)
			return nil
		}},
	}
	return client, refs, nil
}
