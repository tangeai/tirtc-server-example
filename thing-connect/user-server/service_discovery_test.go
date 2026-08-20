package main

import (
	"testing"

	"thing-connect/internal/config"
)

func validDiscoveryConfig() config.DiscoveryCfg {
	return config.DiscoveryCfg{
		Enabled: true, DeviceServerURL: "https://example.com/", UserServerURL: "https://example.com",
		VoIPServerURL: "https://example.com", AIServerURL: "https://example.com", CallServerURL: "https://example.com",
		MQTTURL: "mqtts://mqtt.example.com:8883", TiRTCEndpoint: "https://rtc.example.com",
	}
}

func TestDiscoveryPayload(t *testing.T) {
	payload, err := discoveryPayload(validDiscoveryConfig())
	if err != nil {
		t.Fatal(err)
	}
	if payload["device-srv"] != "https://example.com" || payload["mqtt-srv"] != "mqtts://mqtt.example.com:8883" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestDiscoveryPayloadRejectsPartialOrUnsafeConfig(t *testing.T) {
	cfg := validDiscoveryConfig()
	cfg.CallServerURL = ""
	if _, err := discoveryPayload(cfg); err == nil {
		t.Fatal("accepted incomplete discovery config")
	}
	cfg = validDiscoveryConfig()
	cfg.MQTTURL = "https://mqtt.example.com"
	if _, err := discoveryPayload(cfg); err == nil {
		t.Fatal("accepted non-MQTT discovery URL")
	}
}
