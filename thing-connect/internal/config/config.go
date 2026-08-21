package config

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"thing-connect/internal/model"
)

type ServerCfg struct {
	HTTPPort       int      `yaml:"http_port"`
	TrustedProxies []string `yaml:"trusted_proxies"`
}

// LogCfg configures the slog logger shared by all servers.
type LogCfg struct {
	Level  string `yaml:"level"`  // debug|info|warn|error
	Format string `yaml:"format"` // text|json
}

type DatabaseCfg struct {
	DSN string `yaml:"dsn"`
}

type RedisCfg struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type MQTTCfg struct {
	Broker   string `yaml:"broker"`
	ClientID string `yaml:"client_id"` // clientid 认证模式：固定 ClientID，Username 自动等于 ClientID
	Username string `yaml:"username"`  // username 认证模式：固定 Username，ClientID 自动加 hostname 后缀
	Password string `yaml:"password"`
}

// AuthMode returns "clientid" when client_id is set, "username" when username is set.
func (m MQTTCfg) AuthMode() string {
	if m.Username != "" {
		return "username"
	}
	return "clientid"
}

type ServiceCfg struct {
	QuotaPerUser               int           `yaml:"quota_per_user"`
	CodeTTL                    time.Duration `yaml:"code_ttl"`
	RateLimitWindow            time.Duration `yaml:"rate_limit_window"`
	RateLimitMaxHits           int           `yaml:"rate_limit_max_hits"`
	IPRateLimitWindow          time.Duration `yaml:"ip_rate_limit_window"`
	IPRateLimitMaxFingerprints int           `yaml:"ip_rate_limit_max_fingerprints"`
	GlobalMaxPendingCodes      int           `yaml:"global_max_pending_codes"`
	TokenExpiry                time.Duration `yaml:"token_expiry"`
	MQTTACKTimeout             time.Duration `yaml:"mqtt_ack_timeout"`
	MaxContactsPerDevice       int           `yaml:"max_contacts_per_device"`
	RoomTTLHours               int           `yaml:"room_ttl_hours"`
}

// InternalCfg configures shared service-to-service credentials.
type InternalCfg struct {
	// Key guards ai/voip/call internal unbind endpoints via X-Internal-Key.
	// All services must use the same value.
	Key string `yaml:"key"`
}

// CallCfg configures call-server specific behavior.
type CallCfg struct {
	ServerURL string `yaml:"server_url"` // user-server → call-server base URL
}

// VoipCfg configures voip-server specific behavior.
type VoipCfg struct {
	ServerURL string `yaml:"server_url"` // user-server → voip-server base URL
}

// AiCfg configures ai-server specific behavior.
type AiCfg struct {
	ServerURL string `yaml:"server_url"` // user-server → ai-server base URL
}

type AdminCfg struct {
	ServerURL string `yaml:"server_url"`
}

// DiscoveryCfg publishes the bootstrap endpoints consumed by device
// reference implementations. It is hosted by user-server at /services.
type DiscoveryCfg struct {
	Enabled         bool   `yaml:"enabled"`
	DeviceServerURL string `yaml:"device_server_url"`
	UserServerURL   string `yaml:"user_server_url"`
	VoIPServerURL   string `yaml:"voip_server_url"`
	AIServerURL     string `yaml:"ai_server_url"`
	CallServerURL   string `yaml:"call_server_url"`
	MQTTURL         string `yaml:"mqtt_url"`
	TiRTCEndpoint   string `yaml:"tirtc_endpoint"`
}

type YidunCfg struct {
	CaptchaID string `yaml:"captcha_id"`
	SecretID  string `yaml:"secret_id"`
	SecretKey string `yaml:"secret_key"`
}

// CaptchaProviderCfg contains the credentials and public widget identifier for
// one human-verification provider. PublicConfig is returned to clients; never
// put secrets in it.
type CaptchaProviderCfg struct {
	CaptchaID            string            `yaml:"captcha_id"`
	SecretID             string            `yaml:"secret_id"`
	SecretKey            string            `yaml:"secret_key"`
	AppSecretKey         string            `yaml:"app_secret_key"`
	MiniProgramSecretKey string            `yaml:"mini_program_secret_key"`
	PublicConfig         map[string]string `yaml:"public_config"`
}

// CaptchaCfg selects a provider without coupling the application configuration
// to a particular vendor. The deprecated yidun section remains readable for
// existing deployments.
type CaptchaCfg struct {
	Provider  string                        `yaml:"provider"`
	Providers map[string]CaptchaProviderCfg `yaml:"providers"`
}

type SMTPCfg struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	TLSMode  string `yaml:"tls_mode"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
}

type TirtcCfg struct {
	AppID       string `yaml:"app_id"`
	AccessKeyID string `yaml:"access_key_id"`
	SecretKeyID string `yaml:"secret_key_id"`
	Endpoint    string `yaml:"endpoint"`
}

type TirtcAichatCfg struct {
	BaseURL          string                         `yaml:"base_url"`
	RolesBaseURL     string                         `yaml:"base_role_url"`
	DefaultRoleID    string                         `yaml:"default_role_id"`
	ResourceQuota    map[string]int                 `yaml:"resource_quota"`    // type -> per-user max (mcp/device_plugin/kb)
	DefaultResources map[string][]model.ResourceRef `yaml:"default_resources"` // type -> configured default {id,name}
}

type WsCfg struct {
	Endpoint   string   `yaml:"endpoint"`
	PathPrefix string   `yaml:"path_prefix"`
	DeviceIDs  []string `yaml:"device_ids"`
}

type WxApp struct {
	Enabled        *bool  `yaml:"enabled"`
	Secret         string `yaml:"secret"`
	Token          string `yaml:"token"`
	EncodingAESKey string `yaml:"encoding_aes_key"`
	ModelID        string `yaml:"model_id"`
}

type WechatCfg struct {
	DefaultAppID string           `yaml:"default_app_id"`
	Apps         map[string]WxApp `yaml:"apps"`
}

type Config struct {
	Server      ServerCfg      `yaml:"server"`
	Log         LogCfg         `yaml:"log"`
	Database    DatabaseCfg    `yaml:"database"`
	Redis       RedisCfg       `yaml:"redis"`
	MQTT        MQTTCfg        `yaml:"mqtt"`
	JWTSecret   string         `yaml:"jwt_secret"`
	Service     ServiceCfg     `yaml:"service"`
	Yidun       YidunCfg       `yaml:"yidun"`
	Captcha     CaptchaCfg     `yaml:"captcha"`
	SMTP        SMTPCfg        `yaml:"smtp"`
	Tirtc       TirtcCfg       `yaml:"tirtc"`
	TirtcAichat TirtcAichatCfg `yaml:"tirtc_aichat"`
	Wechat      WechatCfg      `yaml:"wechat"`
	Ws          WsCfg          `yaml:"ws"`
	Internal    InternalCfg    `yaml:"internal"`
	Call        CallCfg        `yaml:"call"`
	Voip        VoipCfg        `yaml:"voip"`
	Ai          AiCfg          `yaml:"ai"`
	Admin       AdminCfg       `yaml:"admin"`
	Discovery   DiscoveryCfg   `yaml:"discovery"`
}

func Load(path string) *Config {
	cfg, err := LoadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	return cfg
}

// LoadFile parses one service configuration and rejects unknown YAML fields.
// Returning an error makes validation reusable by tests and deployment tools,
// while Load retains the existing fail-fast server API.
func LoadFile(path string) (*Config, error) {
	path = ResolvePath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if cfg.Server.HTTPPort == 0 {
		cfg.Server.HTTPPort = 8080
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
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("config: jwt_secret must be set")
	}
	if IsPlaceholderSecret(cfg.JWTSecret) {
		return nil, fmt.Errorf("config: jwt_secret still uses a public placeholder")
	}
	if cfg.Internal.Key != "" && IsPlaceholderSecret(cfg.Internal.Key) {
		return nil, fmt.Errorf("config: internal.key still uses a public placeholder")
	}
	if cfg.Service.QuotaPerUser == 0 {
		cfg.Service.QuotaPerUser = 10
	}
	if cfg.Service.CodeTTL == 0 {
		cfg.Service.CodeTTL = 190 * time.Second
	}
	if cfg.Service.RateLimitWindow == 0 {
		cfg.Service.RateLimitWindow = 190 * time.Second
	}
	if cfg.Service.RateLimitMaxHits == 0 {
		cfg.Service.RateLimitMaxHits = 10
	}
	if cfg.Service.IPRateLimitWindow == 0 {
		cfg.Service.IPRateLimitWindow = 60 * time.Second
	}
	if cfg.Service.IPRateLimitMaxFingerprints == 0 {
		cfg.Service.IPRateLimitMaxFingerprints = 50
	}
	if cfg.Service.GlobalMaxPendingCodes == 0 {
		cfg.Service.GlobalMaxPendingCodes = 10000
	}
	if cfg.Service.MaxContactsPerDevice == 0 {
		cfg.Service.MaxContactsPerDevice = 200
	}
	if cfg.Service.RoomTTLHours == 0 {
		cfg.Service.RoomTTLHours = 12
	}
	if cfg.Service.TokenExpiry == 0 {
		cfg.Service.TokenExpiry = 7 * 24 * time.Hour
	}
	if cfg.Service.MQTTACKTimeout == 0 {
		cfg.Service.MQTTACKTimeout = 5 * time.Second
	}
	return &cfg, nil
}

// ResolvePath keeps existing per-service config files compatible while also
// supporting the first-run installer's atomically activated config bundle.
// A configured path always wins. The bundle fallback is used only when the
// configured file does not exist, so a damaged or unreadable production file
// can never silently switch to another configuration revision.
func ResolvePath(path string) string {
	// Lstat makes even a broken symlink an explicitly configured path. Falling
	// through to the bundle in that case would hide a damaged deployment.
	if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return path
	}
	serviceDir := filepath.Dir(path)
	serviceName := filepath.Base(serviceDir)
	if serviceName == "." || serviceName == string(filepath.Separator) || serviceName == "" {
		return path
	}
	return filepath.Join(filepath.Dir(serviceDir), "config-current", serviceName, filepath.Base(path))
}

// IsPlaceholderSecret identifies example values that must never be accepted
// as runtime credentials. It intentionally does not impose a new minimum
// length on existing deployments, which would invalidate otherwise compatible
// user sessions after an upgrade.
func IsPlaceholderSecret(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return normalized == "change-me-in-production" ||
		strings.HasPrefix(normalized, "replace-with-") ||
		strings.HasPrefix(normalized, "your-")
}

func ParseFlags() string {
	cfgPath := flag.String("c", "config.yaml", "config file path")
	flag.Parse()
	return *cfgPath
}

// DefaultVoipAppID returns the default WeChat app ID for VoIP.
func (c *Config) DefaultVoipAppID() string {
	return c.Wechat.DefaultAppID
}

// ProxyEndpointFor returns the proxy base URL for deviceID, or "" if not proxied.
// When path_prefix is set, the local "/v1/voip" prefix in the request URI is replaced
// with path_prefix so the remote server receives the correct path.
func (c *Config) ProxyEndpointFor(deviceID string) string {
	if c.Ws.Endpoint == "" {
		return ""
	}
	for _, id := range c.Ws.DeviceIDs {
		if id == deviceID {
			prefix := c.Ws.PathPrefix
			if prefix == "" {
				prefix = "/v1/voip"
			}
			return strings.TrimRight(c.Ws.Endpoint, "/") + prefix
		}
	}
	return ""
}

// WxAppFor returns the WxApp config for the given app ID.
// If appID is empty, the default app is used.
// Returns false if not configured.
func (c *Config) WxAppFor(appID string) (WxApp, bool) {
	if appID == "" {
		appID = c.Wechat.DefaultAppID
	}
	app, ok := c.Wechat.Apps[appID]
	if ok && app.Enabled != nil && !*app.Enabled {
		return WxApp{}, false
	}
	return app, ok
}
