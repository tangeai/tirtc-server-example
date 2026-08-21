package installer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"gopkg.in/yaml.v3"

	adminapp "thing-connect/internal/admin"
	baseconfig "thing-connect/internal/config"
)

type FileBundleStore struct{ options Options }

func NewFileBundleStore(options Options) *FileBundleStore {
	return &FileBundleStore{options: options.normalize()}
}

type generatedSecrets struct {
	JWT          string
	Internal     string
	AdminJWT     string
	Encryption   string
	EncryptionID string
}

type bundleManifest struct {
	Version         int               `json:"version"`
	OperationID     string            `json:"operation_id"`
	CreatedAt       time.Time         `json:"created_at"`
	EnabledServices []string          `json:"enabled_services"`
	Files           map[string]string `json:"files"`
	Digest          string            `json:"digest"`
}

// Publish is a convenience used by tests and offline callers. Bootstrap uses
// Prepare -> database activation intent -> Activate to implement an outbox.
func (s *FileBundleStore) Publish(ctx context.Context, draft Draft, operationID string) (BundleReceipt, error) {
	receipt, err := s.Prepare(ctx, draft, operationID)
	if err != nil {
		return BundleReceipt{}, err
	}
	if err := s.Activate(ctx, operationID, receipt.Digest); err != nil {
		return BundleReceipt{}, err
	}
	return receipt, nil
}

// Prepare writes and fsyncs an immutable revision without changing the active
// config pointer. The database activation intent must commit before Activate.
func (s *FileBundleStore) Prepare(ctx context.Context, draft Draft, operationID string) (BundleReceipt, error) {
	select {
	case <-ctx.Done():
		return BundleReceipt{}, ctx.Err()
	default:
	}
	releases := filepath.Join(s.options.DeployRoot, "config-releases")
	finalDir := filepath.Join(releases, operationID)
	if target, err := os.Readlink(filepath.Join(s.options.DeployRoot, "config-current")); err == nil {
		resolved := target
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(s.options.DeployRoot, resolved)
		}
		if filepath.Clean(resolved) != filepath.Clean(finalDir) {
			return BundleReceipt{}, fmt.Errorf("已有其他配置 revision，首次安装拒绝覆盖")
		}
	} else if !os.IsNotExist(err) {
		return BundleReceipt{}, fmt.Errorf("读取当前配置指针失败: %w", err)
	}
	if receipt, ok := readExistingBundle(finalDir, operationID); ok {
		return receipt, nil
	}
	if fileExists(finalDir) {
		return BundleReceipt{}, fmt.Errorf("配置 revision 已存在但清单无效，拒绝覆盖")
	}
	if err := os.MkdirAll(releases, 0o700); err != nil {
		return BundleReceipt{}, fmt.Errorf("创建配置 revision 目录失败: %w", err)
	}
	tempDir := filepath.Join(releases, "."+operationID+".tmp")
	if err := removeOwnedTemp(tempDir, releases, operationID); err != nil {
		return BundleReceipt{}, err
	}
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		return BundleReceipt{}, fmt.Errorf("创建配置暂存目录失败: %w", err)
	}
	secrets, err := newGeneratedSecrets()
	if err != nil {
		return BundleReceipt{}, err
	}
	files, err := renderBundle(draft, secrets, s.options)
	if err != nil {
		return BundleReceipt{}, err
	}
	enabled, err := enabledServices(draft.OptionalServices)
	if err != nil {
		return BundleReceipt{}, err
	}
	manifest := bundleManifest{Version: 1, OperationID: operationID, CreatedAt: time.Now().UTC(), Files: map[string]string{}}
	for _, service := range enabled {
		manifest.EnabledServices = append(manifest.EnabledServices, service.Name)
	}
	combined := sha256.New()
	for _, service := range enabled {
		data := files[service.Name]
		directory := filepath.Join(tempDir, service.Name)
		if err := os.Mkdir(directory, 0o700); err != nil {
			return BundleReceipt{}, err
		}
		path := filepath.Join(directory, "config.yaml")
		if err := writeSynced(path, data, 0o600); err != nil {
			return BundleReceipt{}, err
		}
		digest := sha256.Sum256(data)
		manifest.Files[service.Name+"/config.yaml"] = hex.EncodeToString(digest[:])
		combined.Write([]byte(service.Name))
		combined.Write(digest[:])
	}
	manifest.Digest = hex.EncodeToString(combined.Sum(nil))
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BundleReceipt{}, err
	}
	manifestRaw = append(manifestRaw, '\n')
	if err := writeSynced(filepath.Join(tempDir, "manifest.json"), manifestRaw, 0o600); err != nil {
		return BundleReceipt{}, err
	}
	if err := validateRenderedBundle(tempDir, draft.OptionalServices); err != nil {
		return BundleReceipt{}, fmt.Errorf("生成配置校验失败: %w", err)
	}
	if err := syncDir(tempDir); err != nil {
		return BundleReceipt{}, fmt.Errorf("同步配置暂存目录失败: %w", err)
	}
	if err := os.Rename(tempDir, finalDir); err != nil {
		return BundleReceipt{}, fmt.Errorf("发布配置 revision 失败: %w", err)
	}
	if err := syncDir(releases); err != nil {
		return BundleReceipt{}, fmt.Errorf("同步配置 revision 目录失败: %w", err)
	}
	receipt, ok := readExistingBundle(finalDir, operationID)
	if !ok {
		return BundleReceipt{}, fmt.Errorf("发布后的配置 revision 校验失败")
	}
	return receipt, nil
}

func (s *FileBundleStore) Activate(ctx context.Context, operationID, digest string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	finalDir := filepath.Join(s.options.DeployRoot, "config-releases", operationID)
	receipt, ok := readExistingBundle(finalDir, operationID)
	if !ok || receipt.Digest != digest {
		return fmt.Errorf("配置 revision 与数据库激活意图不一致")
	}
	return s.activate(finalDir, operationID)
}

func (s *FileBundleStore) Active(operationID string) (BundleReceipt, bool) {
	return readActiveBundle(s.options, operationID)
}

// Prepared returns a fully verified immutable revision without requiring it to
// be active. Recovery uses the runtime DSN from this trusted bundle only to
// verify the durable database activation intent before replaying Activate.
func (s *FileBundleStore) Prepared(operationID string) (BundleReceipt, bool) {
	if operationID == "" || filepath.Base(operationID) != operationID || operationID == "." || operationID == ".." {
		return BundleReceipt{}, false
	}
	path := filepath.Join(s.options.DeployRoot, "config-releases", operationID)
	return readExistingBundle(path, operationID)
}

func (s *FileBundleStore) activate(finalDir, operationID string) error {
	current := filepath.Join(s.options.DeployRoot, "config-current")
	if target, err := os.Readlink(current); err == nil {
		resolved := target
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(current), resolved)
		}
		if filepath.Clean(resolved) == filepath.Clean(finalDir) {
			return nil
		}
		return fmt.Errorf("已有其他配置 revision，首次安装拒绝覆盖")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("读取当前配置指针失败: %w", err)
	}
	tempLink := filepath.Join(s.options.DeployRoot, ".config-current."+operationID)
	_ = os.Remove(tempLink)
	relative, err := filepath.Rel(s.options.DeployRoot, finalDir)
	if err != nil {
		return err
	}
	if err := os.Symlink(relative, tempLink); err != nil {
		return fmt.Errorf("创建配置指针失败: %w", err)
	}
	if err := os.Rename(tempLink, current); err != nil {
		return fmt.Errorf("激活配置 revision 失败: %w", err)
	}
	if err := syncDir(s.options.DeployRoot); err != nil {
		return fmt.Errorf("同步部署目录失败: %w", err)
	}
	return nil
}

func renderBundle(draft Draft, secrets generatedSecrets, options Options) (map[string][]byte, error) {
	runtimeUser := strings.TrimSpace(draft.Database.RuntimeUser)
	runtimePassword := draft.Database.RuntimePassword
	runtimeDSN := formatDSN(draft.Database, runtimeUser, runtimePassword)
	redisAddr := net.JoinHostPort(strings.TrimSpace(draft.Redis.Host), strconv.Itoa(draft.Redis.Port))
	mqttAuth, err := normalizeMQTTAuth(draft.MQTT, draft.OptionalServices)
	if err != nil {
		return nil, err
	}
	common := func(service serviceSpec) map[string]any {
		config := map[string]any{
			"server":     map[string]any{"http_port": service.HTTPPort, "trusted_proxies": draft.Network.TrustedProxies},
			"log":        map[string]any{"level": "info", "format": "text"},
			"database":   map[string]any{"dsn": runtimeDSN},
			"redis":      map[string]any{"addr": redisAddr, "password": draft.Redis.Password, "db": draft.Redis.DB},
			"jwt_secret": secrets.JWT,
			"internal":   map[string]any{"key": secrets.Internal},
			"admin":      map[string]any{"server_url": "http://127.0.0.1:9000"},
		}
		if service.UsesMQTT {
			config["mqtt"] = mqttAuth.configFor(service.Name)
		}
		return config
	}
	services, err := enabledBusinessServices(draft.OptionalServices)
	if err != nil {
		return nil, err
	}
	configs := map[string]map[string]any{}
	for _, service := range services {
		configs[service.Name] = common(service)
	}
	for _, service := range services {
		switch service.Name {
		case "call-server":
			configs["user-server"]["call"] = map[string]any{"server_url": "http://127.0.0.1:9005"}
		case "ai-server":
			configs["user-server"]["ai"] = map[string]any{"server_url": "http://127.0.0.1:9004"}
		case "voip-server":
			configs["user-server"]["voip"] = map[string]any{"server_url": "http://127.0.0.1:9003"}
		}
	}
	// The existing /services protocol requires every optional business endpoint
	// and TiRTC. Keep discovery disabled for a partial installation instead of
	// advertising endpoints for services the operator deliberately omitted.
	if strings.TrimSpace(draft.Network.PublicBaseURL) != "" && len(draft.OptionalServices) == 3 {
		base := strings.TrimRight(draft.Network.PublicBaseURL, "/")
		configs["user-server"]["discovery"] = map[string]any{
			"enabled": true, "device_server_url": base, "user_server_url": base,
			"voip_server_url": base, "ai_server_url": base, "call_server_url": base,
			"mqtt_url": mqttAuth.broker, "tirtc_endpoint": base,
		}
	}
	adminConfig := map[string]any{
		"server":   map[string]any{"http_port": options.HTTPPort, "static_dir": options.StaticDir, "trusted_proxies": draft.Network.TrustedProxies},
		"log":      map[string]any{"level": "info", "format": "text"},
		"database": map[string]any{"dsn": runtimeDSN},
		"redis":    map[string]any{"addr": redisAddr, "password": draft.Redis.Password, "db": draft.Redis.DB},
		"internal": map[string]any{"key": secrets.Internal},
		"admin": map[string]any{
			"jwt_secret": secrets.AdminJWT, "issuer": "ThingConnect Admin", "access_ttl": "15m",
			"refresh_ttl": "168h", "challenge_ttl": "5m", "mfa_enabled": true,
			"cookie_secure": draft.Network.CookieSecure,
		},
		"security": map[string]any{"config_encryption_key_id": secrets.EncryptionID, "config_encryption_key": secrets.Encryption},
		"job":      map[string]any{"storage_dir": filepath.Join(options.DeployRoot, "admin-server", "var", "admin-jobs"), "max_bytes": 10485760},
	}
	result := make(map[string][]byte, 6)
	for service, value := range configs {
		raw, err := yaml.Marshal(value)
		if err != nil {
			return nil, err
		}
		result[service] = raw
	}
	raw, err := yaml.Marshal(adminConfig)
	if err != nil {
		return nil, err
	}
	result["admin-server"] = raw
	return result, nil
}

func validateRenderedBundle(root string, optional []string) error {
	services, err := enabledBusinessServices(optional)
	if err != nil {
		return err
	}
	for _, service := range services {
		if _, err := baseconfig.LoadFile(filepath.Join(root, service.Name, "config.yaml")); err != nil {
			return fmt.Errorf("%s: %w", service.Name, err)
		}
	}
	if _, err := adminapp.LoadAppConfig(filepath.Join(root, "admin-server", "config.yaml")); err != nil {
		return fmt.Errorf("admin-server: %w", err)
	}
	return nil
}

func newGeneratedSecrets() (generatedSecrets, error) {
	jwt, err := randomURLSecret(48)
	if err != nil {
		return generatedSecrets{}, err
	}
	internal, err := randomURLSecret(48)
	if err != nil {
		return generatedSecrets{}, err
	}
	adminJWT, err := randomURLSecret(48)
	if err != nil {
		return generatedSecrets{}, err
	}
	encryption := make([]byte, 32)
	if _, err := rand.Read(encryption); err != nil {
		return generatedSecrets{}, err
	}
	return generatedSecrets{
		JWT: jwt, Internal: internal, AdminJWT: adminJWT,
		Encryption: base64.StdEncoding.EncodeToString(encryption), EncryptionID: "installer-v1",
	}, nil
}

func randomURLSecret(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func formatDSN(input DatabaseInput, user, password string) string {
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(strings.TrimSpace(input.Host), strconv.Itoa(input.Port))
	cfg.DBName = input.Name
	cfg.ParseTime = true
	cfg.Loc = time.Local
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	if input.TLS != "" && input.TLS != "false" {
		cfg.TLSConfig = input.TLS
	}
	return cfg.FormatDSN()
}

func validateDraft(draft Draft) error {
	if _, err := enabledServices(draft.OptionalServices); err != nil {
		return err
	}
	migrationUser := strings.TrimSpace(draft.Database.MigrationUser)
	runtimeUser := strings.TrimSpace(draft.Database.RuntimeUser)
	if migrationUser == "" || draft.Database.MigrationPassword == "" {
		return fmt.Errorf("%w: MySQL 迁移账号和密码不能为空", ErrInvalidInput)
	}
	if runtimeUser == "" || draft.Database.RuntimePassword == "" {
		return fmt.Errorf("%w: MySQL DML 运行账号和密码不能为空", ErrInvalidInput)
	}
	if runtimeUser == migrationUser {
		return fmt.Errorf("%w: MySQL 运行账号必须与迁移账号分离", ErrInvalidInput)
	}
	if strings.TrimSpace(draft.Redis.Host) == "" || strings.ContainsAny(draft.Redis.Host, "/?#@") || draft.Redis.Port < 1 || draft.Redis.Port > 65535 {
		return fmt.Errorf("%w: Redis 地址或端口无效", ErrInvalidInput)
	}
	if draft.Redis.DB < 0 {
		return fmt.Errorf("%w: Redis DB 不能为负数", ErrInvalidInput)
	}
	if _, err := normalizeMQTTAuth(draft.MQTT, draft.OptionalServices); err != nil {
		return err
	}
	if strings.TrimSpace(draft.Network.PublicBaseURL) != "" {
		publicURL, err := url.Parse(draft.Network.PublicBaseURL)
		if err != nil || (publicURL.Scheme != "http" && publicURL.Scheme != "https") || publicURL.Host == "" || publicURL.User != nil {
			return fmt.Errorf("%w: 对外访问地址必须是完整 HTTP(S) 地址", ErrInvalidInput)
		}
	}
	for _, proxy := range draft.Network.TrustedProxies {
		if proxy == "" || proxy != strings.TrimSpace(proxy) {
			return fmt.Errorf("%w: 可信代理格式无效", ErrInvalidInput)
		}
		if net.ParseIP(proxy) == nil {
			if _, _, err := net.ParseCIDR(proxy); err != nil {
				return fmt.Errorf("%w: 可信代理必须是 IP 或 CIDR", ErrInvalidInput)
			}
		}
	}
	if strings.TrimSpace(draft.Admin.Email) == "" || !strings.Contains(draft.Admin.Email, "@") {
		return fmt.Errorf("%w: 管理员邮箱无效", ErrInvalidInput)
	}
	return nil
}

func writeSynced(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("创建 %s 失败: %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("同步 %s 失败: %w", path, err)
	}
	return file.Close()
}

func readExistingBundle(path, operationID string) (BundleReceipt, bool) {
	raw, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		return BundleReceipt{}, false
	}
	var manifest bundleManifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Version != 1 || manifest.OperationID != operationID {
		return BundleReceipt{}, false
	}
	enabled, ok := catalogSelection(manifest.EnabledServices)
	if !ok || len(manifest.Files) != len(enabled) {
		return BundleReceipt{}, false
	}
	combined := sha256.New()
	for _, service := range enabled {
		relative := service.Name + "/config.yaml"
		want, ok := manifest.Files[relative]
		if !ok {
			return BundleReceipt{}, false
		}
		data, err := os.ReadFile(filepath.Join(path, relative))
		if err != nil {
			return BundleReceipt{}, false
		}
		digest := sha256.Sum256(data)
		if want != hex.EncodeToString(digest[:]) {
			return BundleReceipt{}, false
		}
		combined.Write([]byte(service.Name))
		combined.Write(digest[:])
	}
	if manifest.Digest != hex.EncodeToString(combined.Sum(nil)) {
		return BundleReceipt{}, false
	}
	adminConfig, err := adminapp.LoadAppConfig(filepath.Join(path, "admin-server", "config.yaml"))
	if err != nil {
		return BundleReceipt{}, false
	}
	optional := make([]string, 0, len(enabled))
	for _, service := range enabled {
		if service.Business && !service.Required {
			optional = append(optional, service.Name)
		}
	}
	return BundleReceipt{
		Digest: manifest.Digest, Path: path, RuntimeDatabaseDSN: adminConfig.Database.DSN,
		OptionalServices: optional,
	}, true
}

func catalogSelection(names []string) ([]serviceSpec, bool) {
	if len(names) == 0 {
		return nil, false
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		if wanted[name] {
			return nil, false
		}
		wanted[name] = true
	}
	var result []serviceSpec
	for _, service := range serviceCatalog {
		if wanted[service.Name] {
			result = append(result, service)
			delete(wanted, service.Name)
		} else if service.Required {
			return nil, false
		}
	}
	return result, len(wanted) == 0
}

func readActiveBundle(options Options, operationID string) (BundleReceipt, bool) {
	if operationID == "" || filepath.Base(operationID) != operationID || operationID == "." || operationID == ".." {
		return BundleReceipt{}, false
	}
	current := filepath.Join(options.DeployRoot, "config-current")
	info, err := os.Lstat(current)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return BundleReceipt{}, false
	}
	target, err := os.Readlink(current)
	if err != nil {
		return BundleReceipt{}, false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(options.DeployRoot, target)
	}
	want := filepath.Join(options.DeployRoot, "config-releases", operationID)
	if filepath.Clean(target) != filepath.Clean(want) {
		return BundleReceipt{}, false
	}
	return readExistingBundle(want, operationID)
}

func removeOwnedTemp(path, releases, operationID string) error {
	if filepath.Dir(path) != filepath.Clean(releases) || filepath.Base(path) != "."+operationID+".tmp" {
		return fmt.Errorf("拒绝清理不受信任的暂存路径")
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("清理旧配置暂存目录失败: %w", err)
	}
	return nil
}
