package main

import (
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"

	"thing-connect/internal/config"
	"thing-connect/internal/dynamicconfig"
	"thing-connect/internal/service"
)

func deviceDynamicConfig(cfg *config.Config, redisClient *redis.Client, devices *service.DeviceService) (*dynamicconfig.Client, []dynamicconfig.Ref, error) {
	client, err := dynamicconfig.New(cfg.Admin.ServerURL, cfg.Internal.Key, redisClient)
	if err != nil {
		return nil, nil, err
	}
	refs := []dynamicconfig.Ref{
		{Namespace: "device-server", Key: "device.code_policy", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				CodeTTL                    string `json:"code_ttl"`
				RateLimitWindow            string `json:"rate_limit_window"`
				RateLimitMaxHits           int    `json:"rate_limit_max_hits"`
				IPRateLimitWindow          string `json:"ip_rate_limit_window"`
				IPRateLimitMaxFingerprints int    `json:"ip_rate_limit_max_fingerprints"`
				GlobalMaxPendingCodes      int    `json:"global_max_pending_codes"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			codeTTL, err := time.ParseDuration(value.CodeTTL)
			if err != nil {
				return err
			}
			rateWindow, err := time.ParseDuration(value.RateLimitWindow)
			if err != nil {
				return err
			}
			ipWindow, err := time.ParseDuration(value.IPRateLimitWindow)
			if err != nil {
				return err
			}
			current := devices.Config()
			current.CodeTTL = codeTTL
			current.RateLimitWindow = rateWindow
			current.RateLimitMaxHits = value.RateLimitMaxHits
			current.IPRateLimitWindow = ipWindow
			current.IPRateLimitMaxFingerprints = value.IPRateLimitMaxFingerprints
			current.GlobalMaxPendingCodes = value.GlobalMaxPendingCodes
			devices.UpdateConfig(current)
			return nil
		}},
		{Namespace: "device-server", Key: "device.token_policy", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				TokenExpiry string `json:"token_expiry"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			duration, err := time.ParseDuration(value.TokenExpiry)
			if err != nil {
				return err
			}
			current := devices.Config()
			current.TokenExpiry = duration
			devices.UpdateConfig(current)
			return nil
		}},
		{Namespace: "device-server", Key: "mqtt.ack_policy", Apply: func(snapshot dynamicconfig.Snapshot) error {
			var value struct {
				Timeout string `json:"timeout"`
			}
			if err := json.Unmarshal(snapshot.Value, &value); err != nil {
				return err
			}
			duration, err := time.ParseDuration(value.Timeout)
			if err != nil {
				return err
			}
			current := devices.Config()
			current.MQTTACKTimeout = duration
			devices.UpdateConfig(current)
			return nil
		}},
	}
	return client, refs, nil
}
