package main

import (
	"encoding/json"
	"errors"

	callhandler "thing-connect/call-server/handler"
	"thing-connect/internal/config"
	"thing-connect/internal/dynamicconfig"
)

func callDynamicConfig(client *dynamicconfig.Client, fallbackTiRTC config.TirtcCfg, server *callhandler.Server) (*dynamicconfig.Client, []dynamicconfig.Ref, error) {
	if client == nil {
		return nil, nil, errors.New("dynamic config client is unavailable")
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
