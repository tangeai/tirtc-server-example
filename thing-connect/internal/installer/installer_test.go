package installer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	return Options{
		DeployRoot: root, ConfigPath: filepath.Join(root, "admin-server", "config.yaml"),
		StaticDir: "static", HTTPPort: 9000,
	}
}

func testDraft() Draft {
	return Draft{
		Database: DatabaseInput{
			Host: "127.0.0.1", Port: 3306, Name: "thing_connect", MigrationUser: "migration",
			MigrationPassword: "migration-password", RuntimeUser: "runtime", RuntimePassword: "runtime-password", TLS: "false",
		},
		Redis:   RedisInput{Host: "127.0.0.1", Port: 6379, DB: 0},
		MQTT:    MQTTInput{Broker: "mqtts://mqtt.example.com:8883", Username: "services", Password: "mqtt-password"},
		Network: NetworkInput{PublicBaseURL: "https://example.com", CookieSecure: true},
		Admin:   FirstAdminInput{Email: "admin@example.com", NickName: "Admin", Password: "AdminPassword123!"},
	}
}

func TestPrepareFirstRunStoresOnlyTokenHash(t *testing.T) {
	options := testOptions(t)
	token, err := PrepareFirstRun(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 {
		t.Fatalf("token length = %d", len(token))
	}
	bootstrap := New(options, Dependencies{})
	if err := bootstrap.Authenticate(token); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if err := bootstrap.Authenticate(token + "x"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate bad token = %v", err)
	}
	raw, err := os.ReadFile(options.TokenHashPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("token was persisted in plaintext")
	}
	if mode, err := DetectMode(options); err != nil || mode != ModeFresh {
		t.Fatalf("DetectMode = %s, %v", mode, err)
	}
}

func TestSetupStatusPublishesSharedServiceCatalog(t *testing.T) {
	options := testOptions(t)
	if _, err := PrepareFirstRun(options); err != nil {
		t.Fatal(err)
	}
	state, err := New(options, Dependencies{}).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.AvailableServices) != len(serviceCatalog) {
		t.Fatalf("available services = %d, want %d", len(state.AvailableServices), len(serviceCatalog))
	}
	for index, service := range serviceCatalog {
		got := state.AvailableServices[index]
		if got.Name != service.Name || got.Required != service.Required || got.UsesMQTT != service.UsesMQTT {
			t.Fatalf("available service %d = %+v, want %+v", index, got, service)
		}
	}
}

func TestPrepareFirstRunRotatesLostTokenBeforeConfigCommit(t *testing.T) {
	options := testOptions(t)
	first, err := PrepareFirstRun(options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareFirstRun(options)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("setup token was not rotated")
	}
	bootstrap := New(options, Dependencies{})
	if err := bootstrap.Authenticate(first); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old token authentication = %v", err)
	}
	if err := bootstrap.Authenticate(second); err != nil {
		t.Fatalf("new token authentication = %v", err)
	}
}

func TestExecuteDoesNotAcceptCredentialsAfterConfigCommit(t *testing.T) {
	options := testOptions(t)
	if _, err := PrepareFirstRun(options); err != nil {
		t.Fatal(err)
	}
	bootstrap := New(options, Dependencies{Bundles: fakeBundle{active: true}})
	state := journal{
		Snapshot:     Snapshot{Mode: ModeRecovery, OperationID: "operation-1", Phase: "awaiting_admin_restart", Percent: 70},
		ConfigDigest: strings.Repeat("a", 64),
	}
	if err := bootstrap.writeJournal(state); err != nil {
		t.Fatal(err)
	}
	draft := testDraft()
	if _, err := bootstrap.Execute(context.Background(), ExecuteRequest{Draft: &draft}); !errors.Is(err, ErrInstallBusy) {
		t.Fatalf("Execute after config commit = %v, want ErrInstallBusy", err)
	}
}

func TestDetectModeNeverTreatsExistingConfigAsFresh(t *testing.T) {
	options := testOptions(t)
	if err := os.MkdirAll(filepath.Dir(options.ConfigPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(options.StateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(options.AllowPath(), []byte("allowed"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(options.ConfigPath, []byte("damaged: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	mode, err := DetectMode(options)
	if err != nil || mode != ModeNormal {
		t.Fatalf("DetectMode = %s, %v; existing damaged config must fail closed", mode, err)
	}
}

func TestFileBundleStorePublishesOneAtomicRevision(t *testing.T) {
	options := testOptions(t)
	store := NewFileBundleStore(options)
	receipt, err := store.Publish(context.Background(), testDraft(), "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Digest) != 64 {
		t.Fatalf("digest = %q", receipt.Digest)
	}
	current, err := os.Readlink(filepath.Join(options.DeployRoot, "config-current"))
	if err != nil {
		t.Fatal(err)
	}
	if current != filepath.Join("config-releases", "operation-1") {
		t.Fatalf("current = %q", current)
	}
	for _, service := range []string{"admin-server", "device-server", "user-server", "voip-server", "ai-server", "call-server"} {
		path := filepath.Join(receipt.Path, service, "config.yaml")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o", service, info.Mode().Perm())
		}
	}
	repeated, err := store.Publish(context.Background(), testDraft(), "operation-1")
	if err != nil || repeated.Digest != receipt.Digest {
		t.Fatalf("idempotent publish = %+v, %v", repeated, err)
	}
	if _, err := store.Publish(context.Background(), testDraft(), "operation-2"); err == nil {
		t.Fatal("publisher replaced an already active first-run revision")
	}
}

func TestFileBundleStoreAlwaysIncludesEveryBusinessServiceBaseConfig(t *testing.T) {
	options := testOptions(t)
	draft := testDraft()
	draft.OptionalServices = []string{"ai-server", "call-server"}
	receipt, err := NewFileBundleStore(options).Publish(context.Background(), draft, "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"admin-server", "device-server", "user-server", "voip-server", "ai-server", "call-server"} {
		if _, err := os.Stat(filepath.Join(receipt.Path, service, "config.yaml")); err != nil {
			t.Fatalf("selected service %s config: %v", service, err)
		}
	}
}

func TestPreviewRequiresAdminPasswordPolicyWhenCreatingFirstAdmin(t *testing.T) {
	assessment := DatabaseAssessment{Class: DatabaseAbsent, Versions: map[string]int{}, CreateAdmin: true}
	bootstrap := New(testOptions(t), Dependencies{
		Database: &fakeProvisioner{inspect: assessment},
		Probes:   noopProbe{},
	})

	for _, password := range []string{"Abcdef1", "abcdefgh", "ABCDEFG1", "Abcdefgh"} {
		draft := testDraft()
		draft.Admin.Password = password
		if _, err := bootstrap.Preview(context.Background(), draft); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("Preview password %q = %v, want ErrInvalidInput", password, err)
		}
	}

	draft := testDraft()
	draft.Admin.Password = "Abcdefg1"
	if _, err := bootstrap.Preview(context.Background(), draft); err != nil {
		t.Fatalf("Preview valid eight-character password: %v", err)
	}
}

func TestPreviewPreservesDatabaseStateErrors(t *testing.T) {
	for _, problem := range []error{
		ErrInvalidInput,
		ErrAlreadyInstalled,
		ErrUnknownDatabase,
		ErrSchemaFuture,
		ErrSchemaDrift,
	} {
		t.Run(problem.Error(), func(t *testing.T) {
			bootstrap := New(testOptions(t), Dependencies{
				Database: &fakeProvisioner{inspectErr: problem},
				Probes:   noopProbe{},
			})

			_, err := bootstrap.Preview(context.Background(), testDraft())
			if !errors.Is(err, problem) {
				t.Fatalf("Preview error = %v, want %v", err, problem)
			}
			if errors.Is(err, ErrMySQLUnavailable) || strings.Contains(err.Error(), "mysql unavailable") {
				t.Fatalf("Preview misclassified database state as unavailable: %v", err)
			}
		})
	}
}

func TestPreviewClassifiesDatabaseDependencyFailureAsUnavailable(t *testing.T) {
	dependencyErr := errors.New("database ping failed")
	bootstrap := New(testOptions(t), Dependencies{
		Database: &fakeProvisioner{inspectErr: dependencyErr},
		Probes:   noopProbe{},
	})

	_, err := bootstrap.Preview(context.Background(), testDraft())
	if !errors.Is(err, ErrMySQLUnavailable) || !errors.Is(err, dependencyErr) {
		t.Fatalf("Preview error = %v, want MySQL unavailable wrapping root cause", err)
	}
}

func TestPreviewRejectsUnavailableRuntimeAccountBeforeInstallation(t *testing.T) {
	runtimeErr := errors.New("access denied for runtime account")
	database := &fakeProvisioner{
		inspect:    DatabaseAssessment{Class: DatabaseAbsent, Versions: map[string]int{}, CreateAdmin: true},
		runtimeErr: runtimeErr,
	}
	bootstrap := New(testOptions(t), Dependencies{Database: database, Probes: noopProbe{}})

	_, err := bootstrap.Preview(context.Background(), testDraft())
	if !errors.Is(err, runtimeErr) {
		t.Fatalf("Preview error = %v, want runtime account root cause", err)
	}
	if !database.runtimeVerified {
		t.Fatal("Preview did not verify the runtime account")
	}
	problem := Explain(err)
	if problem.Code != "MYSQL_RUNTIME_ACCOUNT_INVALID" || problem.Message != "MySQL 运行账号检查失败" {
		t.Fatalf("runtime account problem = %+v", problem)
	}
	if strings.Contains(problem.Message, "access denied") || len(problem.Suggestions) < 2 {
		t.Fatalf("runtime account problem is not safe and actionable: %+v", problem)
	}
}

func TestPreviewTreatsEveryExistingDatabaseAsReadOnly(t *testing.T) {
	for _, class := range []DatabaseClass{DatabaseManagedOlder, DatabaseManagedCurrent} {
		assessment := DatabaseAssessment{Class: class, TableCount: 23, Versions: map[string]int{"core": 1}}
		database := &fakeProvisioner{inspect: assessment}
		bootstrap := New(testOptions(t), Dependencies{Database: database, Probes: noopProbe{}})
		_, err := bootstrap.Preview(context.Background(), testDraft())
		if !errors.Is(err, ErrExistingDatabase) {
			t.Fatalf("Preview %s = %v, want ErrExistingDatabase", class, err)
		}
		if database.runtimeVerified {
			t.Fatalf("Preview %s checked credentials after refusing the existing database", class)
		}
		problem := Explain(err)
		if problem.Code != "DATABASE_ALREADY_IN_USE" || !strings.Contains(problem.Message, "未执行任何写入") {
			t.Fatalf("existing database problem = %+v", problem)
		}
	}
}

func TestPreviewOnlyResumesMatchingLocalDatabaseOperation(t *testing.T) {
	options := testOptions(t)
	assessment := DatabaseAssessment{
		Class: DatabaseManagedOlder, TableCount: 2, Versions: map[string]int{"admin": 1},
		RecoveryOperationID: "operation-1",
	}
	bootstrap := New(options, Dependencies{Database: &fakeProvisioner{inspect: assessment}, Probes: noopProbe{}})
	if err := bootstrap.writeJournal(journal{
		Snapshot: Snapshot{Mode: ModeRecovery, OperationID: "operation-1"}, DatabaseName: "thing_connect",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.Preview(context.Background(), testDraft()); err != nil {
		t.Fatalf("matching recovery operation rejected: %v", err)
	}
	assessment.RecoveryOperationID = "foreign-operation"
	bootstrap.database = &fakeProvisioner{inspect: assessment}
	if _, err := bootstrap.Preview(context.Background(), testDraft()); !errors.Is(err, ErrExistingDatabase) {
		t.Fatalf("foreign recovery operation = %v, want ErrExistingDatabase", err)
	}
}

func TestFileBundleStoreDefersAllMQTTCredentialsToAdmin(t *testing.T) {
	options := testOptions(t)
	draft := testDraft()
	draft.OptionalServices = []string{"voip-server", "ai-server", "call-server"}
	draft.MQTT = MQTTInput{
		Broker: "mqtt://127.0.0.1:1883", AuthMode: "clientid", Password: "mqtt-password",
		ClientIDs: map[string]string{
			"device-server": "devicesrv", "user-server": "usrsrv",
			"voip-server": "voipsrv", "call-server": "callsrv",
		},
	}
	receipt, err := NewFileBundleStore(options).Publish(context.Background(), draft, "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"device-server", "user-server", "voip-server", "ai-server", "call-server"} {
		raw, err := os.ReadFile(filepath.Join(receipt.Path, service, "config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var rendered map[string]any
		if err := yaml.Unmarshal(raw, &rendered); err != nil {
			t.Fatal(err)
		}
		if _, exists := rendered["mqtt"]; exists {
			t.Fatalf("%s base config unexpectedly contains MQTT credentials", service)
		}
	}
}

func TestFileBundlePrepareDoesNotActivateBeforeDatabaseIntent(t *testing.T) {
	options := testOptions(t)
	store := NewFileBundleStore(options)
	receipt, err := store.Prepare(context.Background(), testDraft(), "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(options.DeployRoot, "config-current")); !os.IsNotExist(err) {
		t.Fatalf("config-current exists before activation intent: %v", err)
	}
	if err := store.Activate(context.Background(), "operation-1", receipt.Digest); err != nil {
		t.Fatal(err)
	}
	if active, ok := store.Active("operation-1"); !ok || active.Digest != receipt.Digest {
		t.Fatalf("active receipt = %+v, %v", active, ok)
	}
}

type fakeProvisioner struct {
	inspect         DatabaseAssessment
	inspectErr      error
	runtimeErr      error
	runtimeVerified bool
	claim           *fakeClaim
	configRecorded  bool
	intentVerified  bool
}

func (f *fakeProvisioner) Inspect(context.Context, DatabaseInput) (DatabaseAssessment, error) {
	return f.inspect, f.inspectErr
}
func (f *fakeProvisioner) VerifyRuntimeLogin(context.Context, DatabaseInput) error {
	f.runtimeVerified = true
	return f.runtimeErr
}
func (f *fakeProvisioner) Claim(context.Context, DatabaseInput, string, string) (DatabaseClaim, error) {
	return f.claim, nil
}
func (f *fakeProvisioner) RecordConfiguration(context.Context, string, string, string) error {
	f.configRecorded = true
	return nil
}
func (f *fakeProvisioner) VerifyConfigurationIntent(context.Context, string, string, string) error {
	f.intentVerified = true
	return nil
}
func (f *fakeProvisioner) Seal(context.Context, string, string, string) error { return nil }

type fakeClaim struct {
	assessment DatabaseAssessment
	prepared   bool
	prepareErr error
	recorded   bool
	events     *[]string
	recordErr  error
}

func (f *fakeClaim) Assessment() DatabaseAssessment { return f.assessment }
func (f *fakeClaim) InstanceID() string             { return "instance-id" }
func (f *fakeClaim) Prepare(context.Context, FirstAdminInput, []string) error {
	f.prepared = true
	return f.prepareErr
}
func (f *fakeClaim) Record(context.Context, string, string, string) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	f.recorded = true
	if f.events != nil {
		*f.events = append(*f.events, "database_intent")
	}
	return nil
}
func (f *fakeClaim) Close() error { return nil }

type noopProbe struct{}

func (noopProbe) Probe(context.Context, Draft) error { return nil }

type fakeBundle struct {
	active   bool
	prepared bool
	events   *[]string
}

func (fakeBundle) Prepare(context.Context, Draft, string) (BundleReceipt, error) {
	return BundleReceipt{Digest: strings.Repeat("a", 64), Path: "config", OptionalServices: installOptionalServices()}, nil
}
func (bundle fakeBundle) Activate(context.Context, string, string) error {
	if bundle.events != nil {
		*bundle.events = append(*bundle.events, "filesystem_activate")
	}
	return nil
}
func (bundle fakeBundle) Active(string) (BundleReceipt, bool) {
	return BundleReceipt{Digest: strings.Repeat("a", 64), RuntimeDatabaseDSN: "runtime-dsn", OptionalServices: installOptionalServices()}, bundle.active
}
func (bundle fakeBundle) Prepared(string) (BundleReceipt, bool) {
	return BundleReceipt{Digest: strings.Repeat("a", 64), RuntimeDatabaseDSN: "runtime-dsn", OptionalServices: installOptionalServices()}, bundle.prepared
}

func TestReconcileRuntimeSealsAdminOnlyInstallWithoutStartingServices(t *testing.T) {
	options := testOptions(t)
	options.RuntimeDatabaseDSN = "runtime-dsn"
	store := NewFileBundleStore(options)
	receipt, err := store.Publish(context.Background(), testDraft(), "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := New(options, Dependencies{Database: &fakeProvisioner{}, Bundles: store})
	if err := bootstrap.writeJournal(journal{Snapshot: Snapshot{
		Mode: ModeRecovery, OperationID: "operation-1", Phase: "config_activated", Percent: 68,
	}, InstanceID: "instance-id", ConfigDigest: receipt.Digest, EnabledServices: installOptionalServices()}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := bootstrap.ReconcileRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Phase != "installed" || snapshot.Mode != ModeInstalled || snapshot.CanResume {
		t.Fatalf("snapshot = %+v, want sealed Admin-only installation", snapshot)
	}
	if _, err := os.Stat(options.InstalledPath()); err != nil {
		t.Fatalf("installed marker: %v", err)
	}
}

func TestReconcileRuntimeDoesNotRestartInstalledServices(t *testing.T) {
	options := testOptions(t)
	draft := testDraft()
	draft.OptionalServices = []string{"ai-server"}
	store := NewFileBundleStore(options)
	receipt, err := store.Publish(context.Background(), draft, "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := New(options, Dependencies{Bundles: store})
	if err := bootstrap.writeJournal(journal{Snapshot: Snapshot{
		Mode: ModeInstalled, OperationID: "operation-1", Phase: "installed", Percent: 100,
	}, ConfigDigest: receipt.Digest, EnabledServices: installOptionalServices()}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := bootstrap.ReconcileRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Mode != ModeInstalled || snapshot.CanResume {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestReconcileRuntimeDoesNotStartConfiguredServicesWithoutInstallerJournal(t *testing.T) {
	options := testOptions(t)
	draft := testDraft()
	draft.OptionalServices = []string{"voip-server", "call-server"}
	if _, err := NewFileBundleStore(options).Publish(context.Background(), draft, "operation-1"); err != nil {
		t.Fatal(err)
	}
	bootstrap := New(options, Dependencies{})
	snapshot, err := bootstrap.ReconcileRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Mode != ModeNormal {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestReconcileRuntimeRepairsDatabaseRecordAndCompletesAdminInstall(t *testing.T) {
	options := testOptions(t)
	options.RuntimeDatabaseDSN = "runtime-dsn"
	store := NewFileBundleStore(options)
	receipt, err := store.Publish(context.Background(), testDraft(), "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	provisioner := &fakeProvisioner{}
	bootstrap := New(options, Dependencies{Database: provisioner, Bundles: store})
	if err := bootstrap.writeJournal(journal{Snapshot: Snapshot{
		Mode: ModeRecovery, OperationID: "operation-1", Phase: "config_activated", Percent: 65,
	}, InstanceID: "instance-id", ConfigDigest: receipt.Digest, EnabledServices: installOptionalServices()}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := bootstrap.ReconcileRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !provisioner.configRecorded || snapshot.Phase != "installed" || snapshot.Mode != ModeInstalled {
		t.Fatalf("snapshot=%+v configRecorded=%v", snapshot, provisioner.configRecorded)
	}
	var installed map[string]any
	raw, readErr := os.ReadFile(options.InstalledPath())
	if readErr != nil || json.Unmarshal(raw, &installed) != nil || installed["config_digest"] != receipt.Digest {
		t.Fatalf("installed marker = %s, %v", raw, readErr)
	}
}

func TestExecuteReplaysCommittedPendingActivationWithoutDraft(t *testing.T) {
	options := testOptions(t)
	store := NewFileBundleStore(options)
	receipt, err := store.Prepare(context.Background(), testDraft(), "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	provisioner := &fakeProvisioner{}
	bootstrap := New(options, Dependencies{Database: provisioner, Bundles: store})
	state := journal{Snapshot: Snapshot{
		Mode: ModeRecovery, OperationID: "operation-1", Phase: "config_activation_pending", Percent: 64,
	}, ConfigDigest: receipt.Digest, InstanceID: "instance-id", EnabledServices: installOptionalServices()}
	if err := bootstrap.writeJournal(state); err != nil {
		t.Fatal(err)
	}
	resumed, err := bootstrap.Execute(context.Background(), ExecuteRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Phase != "awaiting_admin_restart" || !provisioner.intentVerified || !provisioner.configRecorded {
		t.Fatalf("resumed=%+v intentVerified=%v configRecorded=%v", resumed, provisioner.intentVerified, provisioner.configRecorded)
	}
	if active, ok := store.Active("operation-1"); !ok || active.Digest != receipt.Digest {
		t.Fatalf("pending revision was not activated: %+v %v", active, ok)
	}
	select {
	case <-bootstrap.RestartRequested():
	default:
		t.Fatal("setup process did not request restart after replaying activation")
	}
}

func TestReconcileRuntimeRejectsTamperedOrForeignBundle(t *testing.T) {
	for _, test := range []struct {
		name      string
		journalID string
		tamper    bool
	}{
		{name: "foreign operation", journalID: "operation-2"},
		{name: "tampered file", journalID: "operation-1", tamper: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := testOptions(t)
			options.RuntimeDatabaseDSN = "runtime-dsn"
			store := NewFileBundleStore(options)
			receipt, err := store.Publish(context.Background(), testDraft(), "operation-1")
			if err != nil {
				t.Fatal(err)
			}
			if test.tamper {
				if err := os.WriteFile(filepath.Join(receipt.Path, "user-server", "config.yaml"), []byte("tampered\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			bootstrap := New(options, Dependencies{Database: &fakeProvisioner{}, Bundles: store})
			if err := bootstrap.writeJournal(journal{Snapshot: Snapshot{
				Mode: ModeRecovery, OperationID: test.journalID, Phase: "config_activated", Percent: 65,
			}}); err != nil {
				t.Fatal(err)
			}
			if _, err := bootstrap.ReconcileRuntime(context.Background()); !errors.Is(err, ErrPlanStale) {
				t.Fatalf("ReconcileRuntime = %v, want ErrPlanStale", err)
			}
		})
	}
}

func TestBootstrapExecuteRechecksPlanInsideClaim(t *testing.T) {
	options := testOptions(t)
	assessment := DatabaseAssessment{Class: DatabaseAbsent, Versions: map[string]int{}, CreateAdmin: true}
	var events []string
	claim := &fakeClaim{assessment: assessment, events: &events}
	provisioner := &fakeProvisioner{inspect: assessment, claim: claim}
	bootstrap := New(options, Dependencies{Database: provisioner, Bundles: fakeBundle{events: &events}, Probes: noopProbe{}})
	plan, err := bootstrap.Preview(context.Background(), testDraft())
	if err != nil {
		t.Fatal(err)
	}
	state, err := bootstrap.Execute(context.Background(), ExecuteRequest{Draft: draftPointer(testDraft()), PlanDigest: plan.Digest})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-bootstrap.RestartRequested():
	case <-time.After(2 * time.Second):
		t.Fatal("installer did not reach config commit")
	}
	if !claim.prepared || !claim.recorded {
		t.Fatalf("claim calls: prepared=%v recorded=%v", claim.prepared, claim.recorded)
	}
	if len(events) < 2 || events[0] != "database_intent" || events[1] != "filesystem_activate" {
		t.Fatalf("activation order = %v", events)
	}
	raw, err := os.ReadFile(options.JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	var saved journal
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.OperationID != state.OperationID || saved.Phase != "awaiting_admin_restart" || saved.ConfigDigest == "" {
		t.Fatalf("saved state = %+v", saved)
	}
}

func TestExecutePersistsActionableRuntimeAccountProblem(t *testing.T) {
	assessment := DatabaseAssessment{Class: DatabaseAbsent, Versions: map[string]int{}, CreateAdmin: true}
	rootCause := errors.New("access denied for runtime account at application host")
	claim := &fakeClaim{
		assessment: assessment,
		prepareErr: errors.Join(ErrMySQLRuntimeAccount, rootCause),
	}
	bootstrap := New(testOptions(t), Dependencies{
		Database: &fakeProvisioner{inspect: assessment, claim: claim},
		Bundles:  fakeBundle{},
		Probes:   noopProbe{},
	})
	plan, err := bootstrap.Preview(context.Background(), testDraft())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.Execute(context.Background(), ExecuteRequest{
		Draft: draftPointer(testDraft()), PlanDigest: plan.Digest,
	}); err != nil {
		t.Fatal(err)
	}
	waitForProblemCode(t, bootstrap, "MYSQL_RUNTIME_ACCOUNT_INVALID")
	state, err := bootstrap.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != "database_claimed" || !claim.prepared {
		t.Fatalf("runtime account failure state = %+v, prepared=%v", state, claim.prepared)
	}
	if state.Problem == nil || state.Problem.Message != "MySQL 运行账号检查失败" || len(state.Problem.Suggestions) < 2 {
		t.Fatalf("runtime account problem is not actionable: %+v", state.Problem)
	}
	visible := state.Message + state.Problem.Message + strings.Join(state.Problem.Suggestions, " ")
	if strings.Contains(strings.ToLower(visible), "access denied") {
		t.Fatalf("runtime account root cause leaked to setup status: %s", visible)
	}
}

func TestLegacyOptionalSelectionDoesNotChangeAdminOnlyInstallPlan(t *testing.T) {
	assessment := DatabaseAssessment{Class: DatabaseAbsent, Versions: map[string]int{}, CreateAdmin: true}
	bootstrap := New(testOptions(t), Dependencies{Database: &fakeProvisioner{inspect: assessment}, Probes: noopProbe{}})
	first := testDraft()
	first.OptionalServices = []string{"ai-server"}
	firstPlan, err := bootstrap.Preview(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	changed := testDraft()
	changed.OptionalServices = []string{"voip-server", "call-server"}
	changedPlan, err := bootstrap.Preview(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if firstPlan.Digest != changedPlan.Digest {
		t.Fatalf("legacy optional selection changed plan digest: %s != %s", firstPlan.Digest, changedPlan.Digest)
	}
}

func waitForProblemCode(t *testing.T, bootstrap *Bootstrap, code string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := bootstrap.Status(context.Background())
		if state.Problem != nil && state.Problem.Code == code {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, _ := bootstrap.Status(context.Background())
	t.Fatalf("problem code did not become %s: %+v", code, state)
}

func TestBootstrapStopsWhenLockedAssessmentDiffers(t *testing.T) {
	options := testOptions(t)
	previewAssessment := DatabaseAssessment{Class: DatabaseAbsent, Versions: map[string]int{}, CreateAdmin: true}
	claim := &fakeClaim{assessment: DatabaseAssessment{Class: DatabaseEmpty, Versions: map[string]int{}, CreateAdmin: true}}
	bootstrap := New(options, Dependencies{
		Database: &fakeProvisioner{inspect: previewAssessment, claim: claim}, Bundles: fakeBundle{}, Probes: noopProbe{},
	})
	plan, err := bootstrap.Preview(context.Background(), testDraft())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.Execute(context.Background(), ExecuteRequest{Draft: draftPointer(testDraft()), PlanDigest: plan.Digest}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := bootstrap.Status(context.Background())
		if state.Problem != nil {
			if state.Problem.Code != "PLAN_STALE" || claim.prepared {
				t.Fatalf("state=%+v prepared=%v", state, claim.prepared)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("plan-stale result not persisted")
}

func draftPointer(value Draft) *Draft { return &value }
