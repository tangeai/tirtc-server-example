package main

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
	"thing-connect/internal/config"
)

func TestPrepareCopiesRuntimeIdentityAndUses9006(t *testing.T) {
	c := &config.Config{JWTSecret: "business-secret", Database: config.DatabaseCfg{DSN: "runtime-dsn"}, Redis: config.RedisCfg{Addr: "localhost:6379", DB: 3}}
	c.Internal.Key = "internal-key"
	c.Admin.ServerURL = "http://localhost:9000"
	c.MQTT = config.MQTTCfg{Broker: "mqtt://localhost:1883", ClientID: "must-not-copy", Password: "must-not-copy"}
	out := prepare(c)
	if out.Server.HTTPPort != 9006 || out.JWT != c.JWTSecret || out.Database != c.Database || out.Redis != c.Redis || out.Admin.URL != c.Admin.ServerURL || out.Internal.Key != c.Internal.Key {
		t.Fatal("runtime identity or port differs")
	}
	raw, err := yaml.Marshal(out)
	if err != nil || strings.Contains(string(raw), "mqtt:") || strings.Contains(string(raw), "must-not-copy") {
		t.Fatal("MQTT credentials copied into base config")
	}
	if out.Recognition.APIKey != "" {
		t.Fatal("model must remain unconfigured")
	}
}
