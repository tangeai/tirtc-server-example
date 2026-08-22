package installer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	installapp "thing-connect/internal/installer"
	"thing-connect/internal/testenv"
)

func TestProvisionerCreatesOnlyAbsentOrEmptyDatabase(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../../tests/testdata/config.yaml")
	parsed, err := mysql.ParseDSN(cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(parsed.Addr)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscan(portText, &port); err != nil {
		t.Fatal(err)
	}
	databaseName := "thingconnect_installer_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "") + "_test"
	runtimeUser := "tc_rt_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	runtimePassword := "RuntimeDMLPassword123!"
	input := installapp.DatabaseInput{
		Host: host, Port: port, Name: databaseName,
		MigrationUser: parsed.User, MigrationPassword: parsed.Passwd, TLS: parsed.TLSConfig,
		RuntimeUser: runtimeUser, RuntimePassword: runtimePassword,
	}
	serverConfig := *parsed
	serverConfig.DBName = ""
	server, err := sqlx.Open("mysql", serverConfig.FormatDSN())
	if err == nil {
		err = server.Ping()
	}
	if err != nil {
		if server != nil {
			_ = server.Close()
		}
		testenv.DependencyUnavailable(t, "MySQL server", err)
	}
	defer server.Close()
	if _, err := server.Exec(`CREATE USER '` + runtimeUser + `'@'%' IDENTIFIED BY '` + runtimePassword + `'`); err != nil {
		t.Fatalf("create DML-only runtime user: %v", err)
	}
	defer func() { _, _ = server.Exec(`DROP USER IF EXISTS '` + runtimeUser + `'@'%'`) }()
	if _, err := server.Exec(`GRANT SELECT,INSERT,UPDATE,DELETE ON ` + quoteIdentifier(databaseName) + `.* TO '` + runtimeUser + `'@'%'`); err != nil {
		t.Fatalf("grant DML-only runtime privileges: %v", err)
	}
	defer func() {
		if !strings.HasSuffix(databaseName, "_test") {
			t.Fatalf("refusing to clean non-test database %q", databaseName)
		}
		_, _ = server.Exec(`DROP DATABASE IF EXISTS ` + quoteIdentifier(databaseName))
	}()

	provisioner := New()
	assessment, err := provisioner.Inspect(context.Background(), input)
	if err != nil || assessment.Class != installapp.DatabaseAbsent {
		t.Fatalf("absent assessment = %+v, %v", assessment, err)
	}
	operationID := uuid.NewString()
	claim, err := provisioner.Claim(context.Background(), input, operationID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := claim.Prepare(context.Background(), installapp.FirstAdminInput{
		Email: "installer@example.com", NickName: "Installer", Password: "AdminPassword123!",
	}, []string{"voip-server", "ai-server", "call-server"}); err != nil {
		claim.Close()
		t.Fatal(err)
	}
	runtimeDB, err := openWithCredentials(input, runtimeUser, runtimePassword, true)
	if err != nil {
		claim.Close()
		t.Fatal(err)
	}
	if _, err := runtimeDB.Exec(`CREATE TABLE runtime_must_not_have_ddl (id BIGINT PRIMARY KEY)`); err == nil {
		_ = runtimeDB.Close()
		claim.Close()
		t.Fatal("DML-only runtime account unexpectedly created a table")
	}
	_ = runtimeDB.Close()
	digest := strings.Repeat("a", 64)
	if err := claim.Record(context.Background(), "config_committed", "installing", digest); err != nil {
		claim.Close()
		t.Fatal(err)
	}
	if err := claim.Record(context.Background(), "config_committed", "installing", digest); err != nil {
		claim.Close()
		t.Fatalf("idempotent Record: %v", err)
	}
	if err := claim.Close(); err != nil {
		t.Fatal(err)
	}
	assessment, err = provisioner.Inspect(context.Background(), input)
	if err != nil || assessment.Class != installapp.DatabaseManagedCurrent || assessment.CreateAdmin || assessment.RecoveryOperationID != operationID {
		t.Fatalf("managed assessment = %+v, %v", assessment, err)
	}
	runtimeConfig := *parsed
	runtimeConfig.DBName = databaseName
	runtimeDSN := runtimeConfig.FormatDSN()
	if err := provisioner.Seal(context.Background(), runtimeDSN, operationID, digest); err != nil {
		t.Fatal(err)
	}
	if err := provisioner.Seal(context.Background(), runtimeDSN, operationID, digest); err != nil {
		t.Fatalf("idempotent Seal: %v", err)
	}
}

func TestProvisionerRefusesUnknownNonEmptyDatabaseWithoutWriting(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../../tests/testdata/config.yaml")
	parsed, err := mysql.ParseDSN(cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(parsed.Addr)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscan(portText, &port); err != nil {
		t.Fatal(err)
	}
	databaseName := "thingconnect_unknown_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "") + "_test"
	input := installapp.DatabaseInput{Host: host, Port: port, Name: databaseName, MigrationUser: parsed.User, MigrationPassword: parsed.Passwd, TLS: parsed.TLSConfig}
	serverConfig := *parsed
	serverConfig.DBName = ""
	server, err := sqlx.Open("mysql", serverConfig.FormatDSN())
	if err == nil {
		err = server.Ping()
	}
	if err != nil {
		if server != nil {
			_ = server.Close()
		}
		testenv.DependencyUnavailable(t, "MySQL server", err)
	}
	defer server.Close()
	defer func() {
		if !strings.HasSuffix(databaseName, "_test") {
			t.Fatalf("refusing to clean non-test database %q", databaseName)
		}
		_, _ = server.Exec(`DROP DATABASE IF EXISTS ` + quoteIdentifier(databaseName))
	}()
	if _, err := server.Exec(`CREATE DATABASE ` + quoteIdentifier(databaseName)); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Exec(`CREATE TABLE ` + quoteIdentifier(databaseName) + `.foreign_records (id BIGINT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	assessment, err := New().Inspect(context.Background(), input)
	if !errors.Is(err, installapp.ErrUnknownDatabase) || assessment.Class != installapp.DatabaseUnknownNonEmpty {
		t.Fatalf("unknown assessment = %+v, %v", assessment, err)
	}
	var count int
	if err := server.Get(&count, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=? AND table_name='schema_migrations'`, databaseName); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("inspection wrote schema_migrations into an unknown database")
	}
}
