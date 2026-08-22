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
	for _, service := range []string{"admin-server", "device-server", "user-server"} {
		path := filepath.Join(receipt.Path, service, "config.yaml")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o", service, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(receipt.Path, "ai-server", "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("unselected ai-server config exists: %v", err)
	}
	repeated, err := store.Publish(context.Background(), testDraft(), "operation-1")
	if err != nil || repeated.Digest != receipt.Digest {
		t.Fatalf("idempotent publish = %+v, %v", repeated, err)
	}
	if _, err := store.Publish(context.Background(), testDraft(), "operation-2"); err == nil {
		t.Fatal("publisher replaced an already active first-run revision")
	}
}

func TestFileBundleStoreIncludesOnlySelectedOptionalServices(t *testing.T) {
	options := testOptions(t)
	draft := testDraft()
	draft.OptionalServices = []string{"ai-server", "call-server"}
	receipt, err := NewFileBundleStore(options).Publish(context.Background(), draft, "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"admin-server", "device-server", "user-server", "ai-server", "call-server"} {
		if _, err := os.Stat(filepath.Join(receipt.Path, service, "config.yaml")); err != nil {
			t.Fatalf("selected service %s config: %v", service, err)
		}
	}
	if _, err := os.Stat(filepath.Join(receipt.Path, "voip-server", "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("unselected voip-server config exists: %v", err)
	}
}

func TestNormalizeMQTTAuthKeepsLegacyUsernamePayloadCompatible(t *testing.T) {
	auth, err := normalizeMQTTAuth(testDraft().MQTT, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth.mode != mqttAuthUsername || auth.username != "services" {
		t.Fatalf("normalized auth = %+v", auth)
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

func TestNormalizeMQTTAuthRequiresDistinctClientIDsForEnabledServices(t *testing.T) {
	input := MQTTInput{
		Broker: "mqtt://127.0.0.1:1883", AuthMode: mqttAuthClientID, Password: "mqtt-password",
		ClientIDs: map[string]string{
			"device-server": "devicesrv", "user-server": "usrsrv", "voip-server": "usrsrv",
		},
	}
	if _, err := normalizeMQTTAuth(input, []string{"voip-server"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate client IDs error = %v", err)
	}
	delete(input.ClientIDs, "voip-server")
	if _, err := normalizeMQTTAuth(input, []string{"voip-server"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing client ID error = %v", err)
	}
}

func TestFileBundleStoreRendersPerServiceClientIDAuthentication(t *testing.T) {
	options := testOptions(t)
	draft := testDraft()
	draft.OptionalServices = []string{"voip-server", "ai-server", "call-server"}
	draft.MQTT = MQTTInput{
		Broker: "mqtt://127.0.0.1:1883", AuthMode: mqttAuthClientID, Password: "mqtt-password",
		ClientIDs: map[string]string{
			"device-server": "devicesrv", "user-server": "usrsrv",
			"voip-server": "voipsrv", "call-server": "callsrv",
		},
	}
	receipt, err := NewFileBundleStore(options).Publish(context.Background(), draft, "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"device-server": "devicesrv", "user-server": "usrsrv",
		"voip-server": "voipsrv", "call-server": "callsrv",
	}
	for service, clientID := range want {
		raw, err := os.ReadFile(filepath.Join(receipt.Path, service, "config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			MQTT struct {
				ClientID string `yaml:"client_id"`
				Username string `yaml:"username"`
			} `yaml:"mqtt"`
		}
		if err := yaml.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		if config.MQTT.ClientID != clientID || config.MQTT.Username != "" {
			t.Fatalf("%s mqtt auth = %+v", service, config.MQTT)
		}
	}
	aiRaw, err := os.ReadFile(filepath.Join(receipt.Path, "ai-server", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var aiConfig map[string]any
	if err := yaml.Unmarshal(aiRaw, &aiConfig); err != nil {
		t.Fatal(err)
	}
	if _, exists := aiConfig["mqtt"]; exists {
		t.Fatal("ai-server config unexpectedly contains MQTT credentials")
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
	inspect        DatabaseAssessment
	inspectErr     error
	claim          *fakeClaim
	configRecorded bool
	intentVerified bool
}

func (f *fakeProvisioner) Inspect(context.Context, DatabaseInput) (DatabaseAssessment, error) {
	return f.inspect, f.inspectErr
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
	recorded   bool
	events     *[]string
	recordErr  error
}

func (f *fakeClaim) Assessment() DatabaseAssessment { return f.assessment }
func (f *fakeClaim) InstanceID() string             { return "instance-id" }
func (f *fakeClaim) Prepare(context.Context, FirstAdminInput, []string) error {
	f.prepared = true
	return nil
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
	return BundleReceipt{Digest: strings.Repeat("a", 64), Path: "config"}, nil
}
func (bundle fakeBundle) Activate(context.Context, string, string) error {
	if bundle.events != nil {
		*bundle.events = append(*bundle.events, "filesystem_activate")
	}
	return nil
}
func (bundle fakeBundle) Active(string) (BundleReceipt, bool) {
	return BundleReceipt{Digest: strings.Repeat("a", 64), RuntimeDatabaseDSN: "runtime-dsn"}, bundle.active
}
func (bundle fakeBundle) Prepared(string) (BundleReceipt, bool) {
	return BundleReceipt{Digest: strings.Repeat("a", 64), RuntimeDatabaseDSN: "runtime-dsn"}, bundle.prepared
}

type readyRuntime struct{}

func (readyRuntime) StartAndWait(_ context.Context, _ []string, progress func(ServiceState)) error {
	progress(ServiceState{Name: "device-server", State: "ready"})
	return nil
}

type blockingRuntime struct{ started chan struct{} }

func (runtime blockingRuntime) StartAndWait(ctx context.Context, _ []string, _ func(ServiceState)) error {
	close(runtime.started)
	<-ctx.Done()
	return ctx.Err()
}

type capturingRuntime struct{ started chan []string }

func (runtime capturingRuntime) StartAndWait(_ context.Context, optional []string, progress func(ServiceState)) error {
	runtime.started <- append([]string(nil), optional...)
	progress(ServiceState{Name: "ai-server", State: "ready"})
	return nil
}

func TestResumeRuntimeRestartsOnlyInstalledOptionalServices(t *testing.T) {
	options := testOptions(t)
	draft := testDraft()
	draft.OptionalServices = []string{"ai-server"}
	store := NewFileBundleStore(options)
	receipt, err := store.Publish(context.Background(), draft, "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan []string, 1)
	bootstrap := New(options, Dependencies{Bundles: store, Runtime: capturingRuntime{started: started}})
	if err := bootstrap.writeJournal(journal{Snapshot: Snapshot{
		Mode: ModeInstalled, OperationID: "operation-1", Phase: "installed", Percent: 100,
	}, ConfigDigest: receipt.Digest, EnabledServices: []string{"ai-server"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.ResumeRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case optional := <-started:
		if !sameStrings(optional, []string{"ai-server"}) {
			t.Fatalf("started optional services = %v", optional)
		}
	case <-time.After(time.Second):
		t.Fatal("installed optional services were not restarted")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bootstrap.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestResumeRuntimeStartsConfiguredOptionalServicesWithoutInstallerJournal(t *testing.T) {
	options := testOptions(t)
	draft := testDraft()
	draft.OptionalServices = []string{"voip-server", "call-server"}
	if _, err := NewFileBundleStore(options).Publish(context.Background(), draft, "operation-1"); err != nil {
		t.Fatal(err)
	}
	started := make(chan []string, 1)
	bootstrap := New(options, Dependencies{Runtime: capturingRuntime{started: started}})
	if _, err := bootstrap.ResumeRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case optional := <-started:
		if !sameStrings(optional, []string{"voip-server", "call-server"}) {
			t.Fatalf("started optional services = %v", optional)
		}
	case <-time.After(time.Second):
		t.Fatal("configured optional services were not restarted")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bootstrap.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestResumeRuntimeRepairsDatabaseRecordFromVerifiedActiveBundle(t *testing.T) {
	options := testOptions(t)
	options.RuntimeDatabaseDSN = "runtime-dsn"
	store := NewFileBundleStore(options)
	receipt, err := store.Publish(context.Background(), testDraft(), "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	provisioner := &fakeProvisioner{}
	bootstrap := New(options, Dependencies{Database: provisioner, Bundles: store, Runtime: readyRuntime{}})
	if err := bootstrap.writeJournal(journal{Snapshot: Snapshot{
		Mode: ModeRecovery, OperationID: "operation-1", Phase: "config_activated", Percent: 65,
	}, InstanceID: "instance-id", ConfigDigest: receipt.Digest}); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.ResumeRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := bootstrap.Status(context.Background())
		if state.Mode == ModeInstalled {
			if !provisioner.configRecorded {
				t.Fatal("runtime recovery did not repair the database config record")
			}
			var installed map[string]any
			raw, readErr := os.ReadFile(options.InstalledPath())
			if readErr != nil || json.Unmarshal(raw, &installed) != nil || installed["config_digest"] != receipt.Digest {
				t.Fatalf("installed marker = %s, %v", raw, readErr)
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := bootstrap.Shutdown(shutdownCtx); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("runtime recovery did not finish")
}

func TestResumeRuntimeReplaysCommittedPendingActivationWithoutDraft(t *testing.T) {
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
	}, ConfigDigest: receipt.Digest, InstanceID: "instance-id"}
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

func TestResumeRuntimeRejectsTamperedOrForeignBundle(t *testing.T) {
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
			bootstrap := New(options, Dependencies{Database: &fakeProvisioner{}, Bundles: store, Runtime: readyRuntime{}})
			if err := bootstrap.writeJournal(journal{Snapshot: Snapshot{
				Mode: ModeRecovery, OperationID: test.journalID, Phase: "config_activated", Percent: 65,
			}}); err != nil {
				t.Fatal(err)
			}
			if _, err := bootstrap.ResumeRuntime(context.Background()); !errors.Is(err, ErrPlanStale) {
				t.Fatalf("ResumeRuntime = %v, want ErrPlanStale", err)
			}
		})
	}
}

func TestBootstrapShutdownCancelsAndWaitsForRuntimeWork(t *testing.T) {
	options := testOptions(t)
	options.RuntimeDatabaseDSN = "runtime-dsn"
	store := NewFileBundleStore(options)
	receipt, err := store.Publish(context.Background(), testDraft(), "operation-1")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	bootstrap := New(options, Dependencies{
		Database: &fakeProvisioner{}, Bundles: store, Runtime: blockingRuntime{started: started},
	})
	if err := bootstrap.writeJournal(journal{Snapshot: Snapshot{
		Mode: ModeRecovery, OperationID: "operation-1", Phase: "config_activated", Percent: 65,
	}, ConfigDigest: receipt.Digest}); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.ResumeRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("runtime work did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bootstrap.Shutdown(ctx); err != nil {
		t.Fatal(err)
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

func TestExecuteRejectsChangedOptionalSelectionForPreparedRevision(t *testing.T) {
	options := testOptions(t)
	assessment := DatabaseAssessment{Class: DatabaseAbsent, Versions: map[string]int{}, CreateAdmin: true}
	claim := &fakeClaim{assessment: assessment, recordErr: errors.New("injected activation intent failure")}
	bootstrap := New(options, Dependencies{
		Database: &fakeProvisioner{inspect: assessment, claim: claim},
		Bundles:  NewFileBundleStore(options), Probes: noopProbe{},
	})
	first := testDraft()
	first.OptionalServices = []string{"ai-server"}
	firstPlan, err := bootstrap.Preview(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.Execute(context.Background(), ExecuteRequest{Draft: &first, PlanDigest: firstPlan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitForProblemCode(t, bootstrap, "INSTALL_FAILED")

	changed := testDraft()
	changedPlan, err := bootstrap.Preview(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.Execute(context.Background(), ExecuteRequest{Draft: &changed, PlanDigest: changedPlan.Digest}); err != nil {
		t.Fatal(err)
	}
	waitForProblemCode(t, bootstrap, "PLAN_STALE")
	saved, err := bootstrap.loadJournal()
	if err != nil {
		t.Fatal(err)
	}
	if !sameStrings(saved.EnabledServices, []string{"ai-server"}) {
		t.Fatalf("journal selection = %v, want immutable manifest selection", saved.EnabledServices)
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
