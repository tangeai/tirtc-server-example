package main

import (
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
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

func TestLocalPushUsesRuntimeIdentityOnly(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "user.yaml")
	target := filepath.Join(dir, "push.yaml")
	input := "server:\n  http_port: 9002\ndatabase:\n  dsn: runtime\njwt_secret: business\ninternal:\n  key: internal\nmqtt:\n  password: excluded\n"
	if err := os.WriteFile(source, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(source, target, "push-server"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var c config.Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.Server.HTTPPort != 9007 || c.Database.DSN != "runtime" || c.JWTSecret != "business" || c.Internal.Key != "internal" || strings.Contains(string(b), "excluded") || strings.Contains(string(b), "recognition:") {
		t.Fatal("incorrect local push configuration")
	}
	if err := run(source, target, "push-server"); err == nil {
		t.Fatal("overwrote an existing config")
	}
}
