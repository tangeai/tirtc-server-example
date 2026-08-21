package admin

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAdminConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	content := "database:\n  dsn: test\ninternal:\n  key: 01234567890123456789012345678901\nadmin:\n  jwt_secret: 12345678901234567890123456789012\nsecurity:\n  config_encryption_key_id: test-v1\n  config_encryption_key: " + key + "\n" + body
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppConfigRejectsUnknownField(t *testing.T) {
	_, err := LoadAppConfig(writeAdminConfig(t, "server:\n  http_prt: 9000\n"))
	if err == nil || !strings.Contains(err.Error(), "http_prt") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestLoadAppConfigRejectsPlaceholderSecret(t *testing.T) {
	path := writeAdminConfig(t, "")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "12345678901234567890123456789012", "replace-with-a-separate-admin-jwt-secret-at-least-32-characters", 1))
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAppConfig(path); err == nil {
		t.Fatal("LoadAppConfig accepted a public placeholder secret")
	}
}
