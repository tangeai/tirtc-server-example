package admin

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	baseconfig "thing-connect/internal/config"
)

type ServerConfig struct {
	HTTPPort       int      `yaml:"http_port"`
	StaticDir      string   `yaml:"static_dir"`
	TrustedProxies []string `yaml:"trusted_proxies"`
}

type AdminAuthConfig struct {
	JWTSecret    string        `yaml:"jwt_secret"`
	Issuer       string        `yaml:"issuer"`
	AccessTTL    time.Duration `yaml:"access_ttl"`
	RefreshTTL   time.Duration `yaml:"refresh_ttl"`
	ChallengeTTL time.Duration `yaml:"challenge_ttl"`
	MFAEnabled   *bool         `yaml:"mfa_enabled"`
	CookieSecure bool          `yaml:"cookie_secure"`
}

type SecurityConfig struct {
	ConfigEncryptionKeyID string `yaml:"config_encryption_key_id"`
	ConfigEncryptionKey   string `yaml:"config_encryption_key"`
}

type JobConfig struct {
	StorageDir string `yaml:"storage_dir"`
	MaxBytes   int64  `yaml:"max_bytes"`
}

type AppConfig struct {
	Server   ServerConfig           `yaml:"server"`
	Log      baseconfig.LogCfg      `yaml:"log"`
	Database baseconfig.DatabaseCfg `yaml:"database"`
	Redis    baseconfig.RedisCfg    `yaml:"redis"`
	Internal baseconfig.InternalCfg `yaml:"internal"`
	Admin    AdminAuthConfig        `yaml:"admin"`
	Security SecurityConfig         `yaml:"security"`
	Job      JobConfig              `yaml:"job"`
}

func LoadAppConfig(path string) (*AppConfig, error) {
	path = baseconfig.ResolvePath(path)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read admin config %s: %w", path, err)
	}
	var cfg AppConfig
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse admin config: %w", err)
	}
	if cfg.Server.HTTPPort == 0 {
		cfg.Server.HTTPPort = 9010
	}
	if cfg.Server.StaticDir == "" {
		cfg.Server.StaticDir = "admin/admin-web/dist"
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "text"
	}
	if cfg.Redis.Addr == "" {
		cfg.Redis.Addr = "127.0.0.1:6379"
	}
	if cfg.Job.StorageDir == "" {
		cfg.Job.StorageDir = "var/admin-jobs"
	}
	if cfg.Job.MaxBytes <= 0 {
		cfg.Job.MaxBytes = 10 * 1024 * 1024
	}
	if cfg.Database.DSN == "" {
		return nil, fmt.Errorf("database.dsn is required")
	}
	if len(cfg.Internal.Key) < 32 {
		return nil, fmt.Errorf("internal.key must be at least 32 characters")
	}
	if baseconfig.IsPlaceholderSecret(cfg.Internal.Key) {
		return nil, fmt.Errorf("internal.key still uses a public placeholder")
	}
	if len(cfg.Admin.JWTSecret) < 32 {
		return nil, fmt.Errorf("admin.jwt_secret must be at least 32 characters")
	}
	if baseconfig.IsPlaceholderSecret(cfg.Admin.JWTSecret) {
		return nil, fmt.Errorf("admin.jwt_secret still uses a public placeholder")
	}
	if cfg.Admin.Issuer == "" {
		cfg.Admin.Issuer = "ThingConnect Admin"
	}
	if cfg.Admin.AccessTTL == 0 {
		cfg.Admin.AccessTTL = 15 * time.Minute
	}
	if cfg.Admin.RefreshTTL == 0 {
		cfg.Admin.RefreshTTL = 7 * 24 * time.Hour
	}
	if cfg.Admin.ChallengeTTL == 0 {
		cfg.Admin.ChallengeTTL = 5 * time.Minute
	}
	if cfg.Security.ConfigEncryptionKeyID == "" {
		return nil, fmt.Errorf("security.config_encryption_key_id is required")
	}
	key, err := base64.StdEncoding.DecodeString(cfg.Security.ConfigEncryptionKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("security.config_encryption_key must be Base64-encoded 32 random bytes")
	}
	return &cfg, nil
}

func (c AdminAuthConfig) MFAIsEnabled() bool {
	return c.MFAEnabled == nil || *c.MFAEnabled
}
