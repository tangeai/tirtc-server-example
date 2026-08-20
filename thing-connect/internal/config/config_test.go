package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_LogDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n  http_port: 9003\njwt_secret: test-secret\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(p)
	if cfg.Log.Level != "info" {
		t.Errorf("default Log.Level want info, got %q", cfg.Log.Level)
	}
	if cfg.Log.Format != "text" {
		t.Errorf("default Log.Format want text, got %q", cfg.Log.Format)
	}
}

func TestLoad_LogOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n  http_port: 9003\njwt_secret: test-secret\nlog:\n  level: debug\n  format: json\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(p)
	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level want debug, got %q", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format want json, got %q", cfg.Log.Format)
	}
}

func TestLoad_InternalAndCallConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("jwt_secret: test-secret\ninternal:\n  key: shared-key\ncall:\n  server_url: http://call:9005\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(p)
	if cfg.Internal.Key != "shared-key" {
		t.Errorf("Internal.Key = %q, want shared-key", cfg.Internal.Key)
	}
	if cfg.Call.ServerURL != "http://call:9005" {
		t.Errorf("Call.ServerURL = %q, want http://call:9005", cfg.Call.ServerURL)
	}
}

func TestLoad_CaptchaProvider(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	data := "jwt_secret: test-secret\ncaptcha:\n  provider: yidun\n  providers:\n    yidun:\n      captcha_id: site-id\n      secret_id: access-id\n      secret_key: access-key\n      public_config:\n        mode: popup\n"
	if err := os.WriteFile(p, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(p)
	if got := cfg.Captcha.Providers["yidun"].CaptchaID; got != "site-id" {
		t.Errorf("captcha_id = %q, want site-id", got)
	}
}

func TestLoadFileRejectsUnknownField(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("jwt_secret: test-secret\nserver:\n  http_prt: 9003\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(p); err == nil {
		t.Fatal("LoadFile accepted an unknown YAML field")
	}
}

func TestLoadFileRejectsPlaceholderSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("jwt_secret: replace-with-a-strong-random-secret\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(p); err == nil {
		t.Fatal("LoadFile accepted a public placeholder secret")
	}
}
