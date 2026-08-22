// Package installer owns the safety, ordering and recovery rules for the
// ThingConnect first-run installation. Transport handlers must use only the
// small Status/Preview/Execute surface and must not perform DDL or process
// control themselves.
package installer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ProductName = "thingconnect"

	ModeFresh     Mode = "fresh"
	ModeRecovery  Mode = "recovery"
	ModeInstalled Mode = "installed"
	ModeNormal    Mode = "normal"
)

var (
	ErrInvalidInput     = errors.New("installer: invalid input")
	ErrPlanStale        = errors.New("installer: plan is stale")
	ErrInstallBusy      = errors.New("installer: installation is running")
	ErrAlreadyInstalled = errors.New("installer: already installed")
	ErrUnauthorized     = errors.New("installer: invalid setup token")
	ErrInvalidOrigin    = errors.New("installer: invalid setup origin")
	ErrTooManyAttempts  = errors.New("installer: too many setup attempts")
	ErrUnknownDatabase  = errors.New("installer: unknown non-empty database")
	ErrSchemaFuture     = errors.New("installer: schema is newer than this binary")
	ErrSchemaDrift      = errors.New("installer: schema does not match its migration ledger")
	ErrRedisUnavailable = errors.New("installer: redis unavailable")
	ErrMQTTUnavailable  = errors.New("installer: mqtt unavailable")
	ErrMySQLUnavailable = errors.New("installer: mysql unavailable")
)

type Mode string

type Options struct {
	DeployRoot         string
	ConfigPath         string
	StaticDir          string
	HTTPPort           int
	SetupBind          string
	SupervisorCTL      string
	SupervisorGroup    string
	RuntimeDatabaseDSN string
}

func (o Options) StateDir() string       { return filepath.Join(o.DeployRoot, "var", "installer") }
func (o Options) AllowPath() string      { return filepath.Join(o.StateDir(), "first-run.allowed") }
func (o Options) TokenHashPath() string  { return filepath.Join(o.StateDir(), "token.sha256") }
func (o Options) JournalPath() string    { return filepath.Join(o.StateDir(), "state.json") }
func (o Options) InstalledPath() string  { return filepath.Join(o.StateDir(), "installed.json") }
func (o Options) DeployLockPath() string { return filepath.Join(o.DeployRoot, "deploy.lock") }

func (o Options) normalize() Options {
	if o.HTTPPort == 0 {
		o.HTTPPort = 9000
	}
	if strings.TrimSpace(o.SetupBind) == "" {
		o.SetupBind = "127.0.0.1"
	}
	if strings.TrimSpace(o.SupervisorCTL) == "" {
		o.SupervisorCTL = "supervisorctl"
	}
	if strings.TrimSpace(o.SupervisorGroup) == "" {
		o.SupervisorGroup = "thing-connect"
	}
	return o
}

type DatabaseInput struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Name              string `json:"name"`
	MigrationUser     string `json:"migration_user"`
	MigrationPassword string `json:"migration_password"`
	RuntimeUser       string `json:"runtime_user"`
	RuntimePassword   string `json:"runtime_password"`
	TLS               string `json:"tls"`
}

type RedisInput struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	DB       int    `json:"db"`
}

type MQTTInput struct {
	Broker    string            `json:"broker"`
	AuthMode  string            `json:"auth_mode,omitempty"`
	Username  string            `json:"username,omitempty"`
	ClientIDs map[string]string `json:"client_ids,omitempty"`
	Password  string            `json:"password"`
}

type NetworkInput struct {
	PublicBaseURL  string   `json:"public_base_url"`
	CookieSecure   bool     `json:"cookie_secure"`
	TrustedProxies []string `json:"trusted_proxies"`
}

type FirstAdminInput struct {
	Email    string `json:"email"`
	NickName string `json:"nick_name"`
	Password string `json:"password"`
}

type Draft struct {
	Database         DatabaseInput   `json:"database"`
	Redis            RedisInput      `json:"redis"`
	MQTT             MQTTInput       `json:"mqtt"`
	Network          NetworkInput    `json:"network"`
	Admin            FirstAdminInput `json:"admin"`
	OptionalServices []string        `json:"optional_services,omitempty"`
}

type DatabaseClass string

const (
	DatabaseAbsent          DatabaseClass = "absent"
	DatabaseEmpty           DatabaseClass = "empty"
	DatabaseManagedCurrent  DatabaseClass = "managed_current"
	DatabaseManagedOlder    DatabaseClass = "managed_older"
	DatabaseUnknownNonEmpty DatabaseClass = "unknown_nonempty"
	DatabaseFuture          DatabaseClass = "future"
	DatabaseDrift           DatabaseClass = "drift"
)

type DatabaseAssessment struct {
	Class       DatabaseClass  `json:"class"`
	TableCount  int            `json:"table_count"`
	Versions    map[string]int `json:"versions"`
	CreateAdmin bool           `json:"create_admin"`
	Description string         `json:"description"`
}

type Plan struct {
	Digest   string             `json:"digest"`
	Database DatabaseAssessment `json:"database"`
	Actions  []string           `json:"actions"`
	Warnings []string           `json:"warnings"`
}

type ExecuteRequest struct {
	Draft      *Draft `json:"draft,omitempty"`
	PlanDigest string `json:"plan_digest,omitempty"`
}

type Problem struct {
	Code        string   `json:"code"`
	Message     string   `json:"message"`
	Suggestions []string `json:"suggestions,omitempty"`
}

type ServiceState struct {
	Name    string   `json:"name"`
	State   string   `json:"state"`
	Problem *Problem `json:"problem,omitempty"`
}

type Snapshot struct {
	Mode        Mode           `json:"mode"`
	OperationID string         `json:"operation_id,omitempty"`
	Phase       string         `json:"phase,omitempty"`
	Percent     int            `json:"percent"`
	Message     string         `json:"message,omitempty"`
	Retryable   bool           `json:"retryable"`
	NeedsToken  bool           `json:"needs_token"`
	AdminURL    string         `json:"admin_url,omitempty"`
	Services    []ServiceState `json:"services,omitempty"`
	Problem     *Problem       `json:"problem,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at,omitempty"`
}

type journal struct {
	Snapshot
	DatabaseName    string   `json:"database_name,omitempty"`
	ConfigDigest    string   `json:"config_digest,omitempty"`
	InstanceID      string   `json:"instance_id,omitempty"`
	EnabledServices []string `json:"enabled_services,omitempty"`
}

type Bootstrap struct {
	opts     Options
	mu       sync.Mutex
	running  bool
	closed   bool
	restart  chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	database DatabaseProvisioner
	bundles  BundlePublisher
	probes   DependencyProbe
	runtime  RuntimeController
	now      func() time.Time
}

type RuntimeController interface {
	StartAndWait(context.Context, []string, func(ServiceState)) error
}

type BundleReceipt struct {
	Digest             string
	Path               string
	RuntimeDatabaseDSN string
	OptionalServices   []string
}

type BundlePublisher interface {
	Prepare(context.Context, Draft, string) (BundleReceipt, error)
	Activate(context.Context, string, string) error
	Prepared(string) (BundleReceipt, bool)
	Active(string) (BundleReceipt, bool)
}

type DependencyProbe interface {
	Probe(context.Context, Draft) error
}

type DatabaseProvisioner interface {
	Inspect(context.Context, DatabaseInput) (DatabaseAssessment, error)
	Claim(context.Context, DatabaseInput, string, string) (DatabaseClaim, error)
	VerifyConfigurationIntent(context.Context, string, string, string) error
	RecordConfiguration(context.Context, string, string, string) error
	Seal(context.Context, string, string, string) error
}

type DatabaseClaim interface {
	Assessment() DatabaseAssessment
	InstanceID() string
	Prepare(context.Context, FirstAdminInput, []string) error
	Record(context.Context, string, string, string) error
	Close() error
}

type Dependencies struct {
	Database DatabaseProvisioner
	Bundles  BundlePublisher
	Probes   DependencyProbe
	Runtime  RuntimeController
}

func New(options Options, dependencies Dependencies) *Bootstrap {
	ctx, cancel := context.WithCancel(context.Background())
	return &Bootstrap{
		opts: options.normalize(), database: dependencies.Database, bundles: dependencies.Bundles,
		probes: dependencies.Probes, runtime: dependencies.Runtime,
		restart: make(chan struct{}, 1), now: time.Now, ctx: ctx, cancel: cancel,
	}
}

func (b *Bootstrap) RestartRequested() <-chan struct{} { return b.restart }

// Shutdown cancels installer-owned background work and waits for it to release
// database locks, subprocesses and readiness requests.
func (b *Bootstrap) Shutdown(ctx context.Context) error {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		b.cancel()
	}
	b.mu.Unlock()
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DetectMode never treats a parse failure as a fresh installation. Any
// existing config path or activated bundle forces the normal startup path,
// where strict config loading will fail closed if the file is damaged.
func DetectMode(options Options) (Mode, error) {
	options = options.normalize()
	if fileExists(options.InstalledPath()) {
		return ModeInstalled, nil
	}
	if hasAnyServiceConfig(options) || fileExists(filepath.Join(options.DeployRoot, "config-current")) {
		return ModeNormal, nil
	}
	if !fileExists(options.AllowPath()) {
		return ModeNormal, nil
	}
	if fileExists(options.JournalPath()) {
		return ModeRecovery, nil
	}
	return ModeFresh, nil
}

// PrepareFirstRun creates the explicit one-time authorization marker and
// returns a token that is never persisted in plaintext. Re-running the local
// command before a configuration bundle is committed rotates the token. This
// gives an operator a safe recovery path when the original terminal output is
// lost, without reopening an installed deployment.
func PrepareFirstRun(options Options) (string, error) {
	options = options.normalize()
	mode, err := DetectMode(options)
	if err != nil {
		return "", err
	}
	if mode == ModeInstalled || hasAnyServiceConfig(options) || fileExists(filepath.Join(options.DeployRoot, "config-current")) {
		return "", ErrAlreadyInstalled
	}
	if err := os.MkdirAll(options.StateDir(), 0o700); err != nil {
		return "", fmt.Errorf("prepare state directory: %w", err)
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate setup token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	hash := sha256.Sum256([]byte(token))
	if err := writeAtomicSecret(options.TokenHashPath(), []byte(hex.EncodeToString(hash[:])+"\n"), 0o400); err != nil {
		return "", err
	}
	if !fileExists(options.AllowPath()) {
		if err := writeExclusive(options.AllowPath(), []byte("ThingConnect first-run setup\n"), 0o400); err != nil {
			_ = os.Remove(options.TokenHashPath())
			return "", err
		}
	}
	if err := syncDir(options.StateDir()); err != nil {
		return "", err
	}
	return token, nil
}

func (b *Bootstrap) Authenticate(token string) error {
	raw, err := os.ReadFile(b.opts.TokenHashPath())
	if err != nil {
		return ErrUnauthorized
	}
	want, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(want) != sha256.Size {
		return ErrUnauthorized
	}
	got := sha256.Sum256([]byte(strings.TrimSpace(token)))
	if subtle.ConstantTimeCompare(want, got[:]) != 1 {
		return ErrUnauthorized
	}
	return nil
}

func (b *Bootstrap) Status(context.Context) (Snapshot, error) {
	mode, err := DetectMode(b.opts)
	if err != nil {
		return Snapshot{}, err
	}
	state, err := b.loadJournal()
	if err != nil && !os.IsNotExist(err) {
		return Snapshot{}, err
	}
	if state.OperationID != "" {
		if mode == ModeInstalled || state.Phase == "installed" {
			state.Mode = ModeInstalled
			state.NeedsToken = false
		} else {
			state.Mode = ModeRecovery
			state.NeedsToken = true
		}
		return state.Snapshot, nil
	}
	message := "ThingConnect 正常运行"
	switch mode {
	case ModeFresh:
		message = "等待首次安装"
	case ModeRecovery:
		message = "等待恢复未完成的安装"
	}
	return Snapshot{Mode: mode, Message: message, NeedsToken: mode == ModeFresh || mode == ModeRecovery}, nil
}

func (b *Bootstrap) loadJournal() (journal, error) {
	raw, err := os.ReadFile(b.opts.JournalPath())
	if err != nil {
		return journal{}, err
	}
	var state journal
	if err := json.Unmarshal(raw, &state); err != nil {
		return journal{}, fmt.Errorf("read installer state: %w", err)
	}
	return state, nil
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func hasAnyServiceConfig(options Options) bool {
	if fileExists(options.ConfigPath) {
		return true
	}
	for _, service := range []string{"admin-server", "device-server", "user-server", "voip-server", "ai-server", "call-server"} {
		if fileExists(filepath.Join(options.DeployRoot, service, "config.yaml")) {
			return true
		}
	}
	return false
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func writeAtomicSecret(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".token-*.tmp")
	if err != nil {
		return fmt.Errorf("create token temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("protect token temp file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write token temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync token temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close token temp file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("publish setup token hash: %w", err)
	}
	return syncDir(dir)
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
