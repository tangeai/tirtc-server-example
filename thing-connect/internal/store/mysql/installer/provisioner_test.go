package installer

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"

	installapp "thing-connect/internal/installer"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
)

func TestProvisionerClassifiesRuntimeLoginFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	input := installapp.DatabaseInput{
		Host: "127.0.0.1", Port: port, Name: "thing_connect",
		RuntimeUser: "runtime", RuntimePassword: "runtime-password", TLS: "false",
	}

	err = New().VerifyRuntimeLogin(context.Background(), input)
	if !errors.Is(err, installapp.ErrMySQLRuntimeAccount) {
		t.Fatalf("VerifyRuntimeLogin error = %v, want %v", err, installapp.ErrMySQLRuntimeAccount)
	}
}

func TestRuntimeDMLTablesCoverSharedAccountConsumers(t *testing.T) {
	minimal, err := runtimeDMLTables(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, optionalTable := range []string{"voip_device_auth", "ai_user_resource", "call_contact"} {
		if !slices.Contains(minimal, optionalTable) {
			t.Fatalf("shared runtime account omitted %s privileges", optionalTable)
		}
	}
	if !slices.Contains(runtimeReadOnlyTables, "schema_migrations") {
		t.Fatal("runtime preflight omitted schema_migrations SELECT")
	}
	selected, err := runtimeDMLTables([]string{"ai-server"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(minimal, selected) {
		t.Fatalf("shared runtime grant changed with process selection: minimal=%v selected=%v", minimal, selected)
	}
	if _, err := runtimeDMLTables([]string{"ai-server", "ai-server"}); err == nil {
		t.Fatal("duplicate optional services were accepted")
	}
}

func TestBaselineAdminVersionRequiresInstallationStateTable(t *testing.T) {
	shape, err := mysqlmigrate.SchemaShapeForVersions(map[string]int{"core": 0, "admin": 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	tables := make(map[string]bool, len(shape))
	for table := range shape {
		tables[table] = true
	}
	delete(tables, "thingconnect_installation_state")

	if err := validateLegacyFingerprint(tables, map[string]int{"core": 0, "admin": 1}, true); !errors.Is(err, installapp.ErrSchemaDrift) {
		t.Fatalf("validateLegacyFingerprint without installation state = %v, want %v", err, installapp.ErrSchemaDrift)
	}
	tables["thingconnect_installation_state"] = true
	if err := validateLegacyFingerprint(tables, map[string]int{"core": 0, "admin": 1}, true); err != nil {
		t.Fatalf("validateLegacyFingerprint with complete baseline = %v", err)
	}
}
