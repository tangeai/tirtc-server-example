package main

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/config"
)

func registerServiceDiscovery(r *gin.Engine, cfg config.DiscoveryCfg) error {
	if !cfg.Enabled {
		return nil
	}
	payload, err := discoveryPayload(cfg)
	if err != nil {
		return err
	}
	r.GET("/services", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=60")
		c.JSON(200, payload)
	})
	return nil
}

func discoveryPayload(cfg config.DiscoveryCfg) (gin.H, error) {
	required := map[string]string{
		"device-srv": cfg.DeviceServerURL,
		"voip-srv":   cfg.VoIPServerURL,
		"ai-srv":     cfg.AIServerURL,
		"call-srv":   cfg.CallServerURL,
		"mqtt-srv":   cfg.MQTTURL,
		"tirtc-srv":  cfg.TiRTCEndpoint,
	}
	payload := gin.H{}
	for name, value := range required {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("discovery.%s is required when discovery is enabled", configKeyForDiscoveryField(name))
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("discovery value %s is not an absolute URL", name)
		}
		if name == "mqtt-srv" && parsed.Scheme != "mqtt" && parsed.Scheme != "mqtts" {
			return nil, fmt.Errorf("discovery.mqtt_url must use mqtt or mqtts")
		}
		if name != "mqtt-srv" && parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("discovery value %s must use http or https", name)
		}
		payload[name] = strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(cfg.UserServerURL); value != "" {
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("discovery.user_server_url must be an absolute HTTP URL")
		}
		payload["user-srv"] = strings.TrimRight(value, "/")
	}
	return payload, nil
}

func configKeyForDiscoveryField(responseName string) string {
	return map[string]string{
		"device-srv": "device_server_url",
		"voip-srv":   "voip_server_url",
		"ai-srv":     "ai_server_url",
		"call-srv":   "call_server_url",
		"mqtt-srv":   "mqtt_url",
		"tirtc-srv":  "tirtc_endpoint",
	}[responseName]
}
