package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_LogDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n  http_port: 9003\njwt_secret: test\n"), 0644); err != nil {
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
	if err := os.WriteFile(p, []byte("server:\n  http_port: 9003\njwt_secret: test\nlog:\n  level: debug\n  format: json\n"), 0644); err != nil {
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
