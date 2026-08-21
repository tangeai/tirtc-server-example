// Package migrate owns ThingConnect's MySQL DDL, migration locks, ownership
// checks and version ledger. Only explicit installer/deployment entry points
// invoke it; long-running services use its read-only Require functions.
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

// IsIgnorableDDLError reports whether a DDL error means that an idempotent
// migration statement has already been applied. Permission errors are never
// ignored: production installations must provision the schema with a migration
// account before starting services with a restricted application account.
func IsIgnorableDDLError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	switch mysqlErr.Number {
	case 1060, 1061:
		return true
	default:
		return false
	}
}

// execIgnoreDup executes a DDL statement and ignores errors that IsIgnorableDDLError
// classifies as benign, so ALTER TABLE statements are idempotent on repeated Migrate
// calls.
type migrationConn interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	GetContext(context.Context, any, string, ...any) error
	SelectContext(context.Context, any, string, ...any) error
}

func execIgnoreDup(ctx context.Context, db migrationConn, stmt string) error {
	_, err := db.ExecContext(ctx, stmt)
	if err == nil || IsIgnorableDDLError(err) {
		return nil
	}
	return err
}

var coreV1MigrationFiles = []string{
	"migrations/core/001_user.sql",
	"migrations/core/001_device.sql",
	"migrations/core/001_voip.sql",
	"migrations/core/001_ai.sql",
	"migrations/core/001_call.sql",
}

func runMigrationFiles(ctx context.Context, conn migrationConn, component string, version int, paths ...string) error {
	statements, err := statementsFromFiles(paths...)
	if err != nil {
		return fmt.Errorf("migrate %s version %d: %w", component, version, err)
	}
	return runMigration(ctx, conn, component, version, statements)
}

func claimEmptyMigrationTarget(ctx context.Context, conn migrationConn) error {
	var count int
	if err := conn.GetContext(ctx, &count, `SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema=DATABASE() AND table_type='BASE TABLE'`); err != nil {
		return fmt.Errorf("migrate: inspect empty target: %w", err)
	}
	if count != 0 {
		return nil
	}
	statements, err := statementsFromFiles("migrations/admin/004_installation_state.sql")
	if err != nil {
		return err
	}
	for index, statement := range statements {
		if err := execIgnoreDup(ctx, conn, statement); err != nil {
			return fmt.Errorf("migrate: claim empty target statement %d: %w", index+1, err)
		}
	}
	return nil
}

// Migrate applies the schema owned by the five business services from an
// explicit migration or installer process. A MySQL named lock serializes
// concurrent operators; long-running services only call RequireSchemaCurrent.
func Migrate(db *sqlx.DB) error {
	return MigrateContext(context.Background(), db)
}

func MigrateContext(ctx context.Context, db *sqlx.DB) error {
	return withMigrationLock(ctx, db, func(conn *sqlx.Conn) error {
		if err := requireOwnedMigrationTarget(ctx, conn); err != nil {
			return err
		}
		if err := claimEmptyMigrationTarget(ctx, conn); err != nil {
			return err
		}
		if err := runMigrationFiles(ctx, conn, "core", 1, coreV1MigrationFiles...); err != nil {
			return err
		}
		return runMigrationFiles(ctx, conn, "core", 2, "migrations/core/002_user_device_sorting.sql")
	})
}

// MigrateAdmin applies the business schema followed by the tables owned by
// admin-server. Keeping this entry point separate prevents business-only
// deployments from creating Admin tables.
func MigrateAdmin(db *sqlx.DB) error {
	return MigrateAdminContext(context.Background(), db)
}

func MigrateAdminContext(ctx context.Context, db *sqlx.DB) error {
	return withMigrationLock(ctx, db, func(conn *sqlx.Conn) error {
		if err := requireOwnedMigrationTarget(ctx, conn); err != nil {
			return err
		}
		if err := claimEmptyMigrationTarget(ctx, conn); err != nil {
			return err
		}
		if err := runMigrationFiles(ctx, conn, "core", 1, coreV1MigrationFiles...); err != nil {
			return err
		}
		if err := runMigrationFiles(ctx, conn, "core", 2, "migrations/core/002_user_device_sorting.sql"); err != nil {
			return err
		}
		if err := runMigrationFiles(ctx, conn, "admin", 1, "migrations/admin/001_schema.sql"); err != nil {
			return err
		}
		if err := runMigrationFiles(ctx, conn, "admin", 2, "migrations/admin/002_job_leases.sql"); err != nil {
			return err
		}
		if err := runMigrationFiles(ctx, conn, "admin", 3, "migrations/admin/003_plaintext_secrets.sql"); err != nil {
			return err
		}
		return runMigrationFiles(ctx, conn, "admin", 4, "migrations/admin/004_installation_state.sql")
	})
}

// requireOwnedMigrationTarget runs before ensureMigrationTable so a typo in a
// migration DSN cannot claim an unrelated non-empty schema by writing a ledger
// into it. Empty schemas and the installer's sole ownership marker are the only
// targets allowed without an existing, contiguous ThingConnect ledger.
func requireOwnedMigrationTarget(ctx context.Context, conn migrationConn) error {
	var tables []string
	if err := conn.SelectContext(ctx, &tables, `SELECT table_name FROM information_schema.tables
		WHERE table_schema=DATABASE() AND table_type='BASE TABLE' ORDER BY table_name`); err != nil {
		return fmt.Errorf("migrate: inspect target tables: %w", err)
	}
	if len(tables) == 0 {
		return nil
	}
	hasLedger := false
	hasMarker := false
	known := map[string]bool{
		"schema_migrations": true, "thingconnect_installation_state": true,
		"users": true, "device_pool": true, "device_bind": true, "device_bind_log": true,
		"voip_device_profile": true, "voip_device_auth": true, "voip_user_profile": true,
		"ai_user_role": true, "ai_device_role": true, "ai_user_resource": true,
		"call_contact": true, "cleanup_outbox": true, "admin_users": true,
		"admin_roles": true, "admin_user_roles": true, "admin_role_permissions": true,
		"admin_menus": true, "admin_role_menus": true, "admin_sessions": true,
		"admin_mfa_factors": true, "admin_mfa_recovery_codes": true,
		"admin_login_log": true, "admin_dict_types": true, "admin_dict_items": true,
		"admin_audit_log": true, "admin_jobs": true, "admin_job_items": true,
		"config_entries": true, "config_publish_outbox": true,
	}
	for _, table := range tables {
		if !known[table] {
			return fmt.Errorf("migrate: target contains unknown table %q; refusing all writes", table)
		}
		if table == "schema_migrations" {
			hasLedger = true
		}
		if table == "thingconnect_installation_state" {
			hasMarker = true
		}
	}
	markerRecoverable := false
	if hasMarker {
		var marker struct {
			Product string `db:"product"`
			Status  string `db:"status"`
		}
		if err := conn.GetContext(ctx, &marker, `SELECT product,status FROM thingconnect_installation_state WHERE id=1`); err != nil || marker.Product != "thingconnect" {
			return fmt.Errorf("migrate: installer ownership marker is invalid; refusing all writes")
		}
		markerRecoverable = marker.Status == "installing" || marker.Status == "migration_only"
	}
	if !hasLedger {
		if len(tables) != 1 || tables[0] != "thingconnect_installation_state" || !markerRecoverable {
			return fmt.Errorf("migrate: non-empty target has no ThingConnect migration ledger; refusing all writes")
		}
		return nil
	}
	type versionRow struct {
		Component string `db:"component"`
		Version   int    `db:"version"`
	}
	var rows []versionRow
	if err := conn.SelectContext(ctx, &rows, `SELECT component,version FROM schema_migrations ORDER BY component,version`); err != nil {
		return fmt.Errorf("migrate: read migration ledger: %w", err)
	}
	if len(rows) == 0 {
		if markerRecoverable {
			return nil
		}
		return fmt.Errorf("migrate: empty migration ledger has no recoverable ThingConnect ownership marker; refusing all writes")
	}
	current := CurrentMigrationVersions()
	seen := make(map[string]map[int]bool, len(current))
	for _, row := range rows {
		ceiling, ok := current[row.Component]
		if !ok || row.Version < 1 || row.Version > ceiling {
			return fmt.Errorf("migrate: migration ledger contains unsupported %s version %d", row.Component, row.Version)
		}
		if seen[row.Component] == nil {
			seen[row.Component] = map[int]bool{}
		}
		seen[row.Component][row.Version] = true
	}
	for component, versions := range seen {
		maximum := 0
		for version := range versions {
			if version > maximum {
				maximum = version
			}
		}
		for version := 1; version <= maximum; version++ {
			if !versions[version] {
				return fmt.Errorf("migrate: migration ledger for %s has a gap at version %d", component, version)
			}
		}
	}
	return nil
}

func runMigration(ctx context.Context, db migrationConn, component string, version int, stmts []string) error {
	if err := ensureMigrationTable(ctx, db); err != nil {
		return fmt.Errorf("migrate %s: prepare schema_migrations: %w", component, err)
	}
	var applied int
	if err := db.GetContext(ctx, &applied, `SELECT COUNT(*) FROM schema_migrations WHERE component=? AND version=?`, component, version); err != nil {
		return fmt.Errorf("migrate %s: read version %d: %w", component, version, err)
	}
	if applied > 0 {
		return nil
	}
	for index, stmt := range stmts {
		if err := execIgnoreDup(ctx, db, stmt); err != nil {
			return fmt.Errorf("migrate %s version %d statement %d: %w", component, version, index+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (component,version) VALUES (?,?)`, component, version); err != nil {
		var mysqlErr *mysql.MySQLError
		if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1062 {
			return fmt.Errorf("migrate %s: record version %d: %w", component, version, err)
		}
	}
	return nil
}

func ensureMigrationTable(ctx context.Context, db migrationConn) error {
	var exists int
	if err := db.GetContext(ctx, &exists, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='schema_migrations'`); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	statements, err := statementsFromFiles("migrations/shared/schema_migrations.sql")
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if err := execIgnoreDup(ctx, db, statement); err != nil {
			return err
		}
	}
	return nil
}

func withMigrationLock(ctx context.Context, db *sqlx.DB, migrate func(*sqlx.Conn) error) error {
	lockCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	conn, err := db.Connx(lockCtx)
	if err != nil {
		return fmt.Errorf("migrate: reserve connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var database string
	if err := conn.GetContext(lockCtx, &database, `SELECT DATABASE()`); err != nil {
		return fmt.Errorf("migrate: read database: %w", err)
	}
	if database == "" {
		return fmt.Errorf("migrate: no database selected")
	}
	digest := sha256.Sum256([]byte(database))
	lockName := fmt.Sprintf("thingconnect:migrate:%x", digest[:16])
	var acquired int
	if err := conn.GetContext(lockCtx, &acquired, `SELECT GET_LOCK(?, 30)`, lockName); err != nil {
		return fmt.Errorf("migrate: acquire lock: %w", err)
	}
	if acquired != 1 {
		return fmt.Errorf("migrate: another migration is still running")
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer releaseCancel()
		var released any
		_ = conn.GetContext(releaseCtx, &released, `SELECT RELEASE_LOCK(?)`, lockName)
	}()
	if _, err := conn.ExecContext(ctx, `SET SESSION lock_wait_timeout=30`); err != nil {
		return fmt.Errorf("migrate: set metadata lock timeout: %w", err)
	}
	return migrate(conn)
}

// CurrentMigrationVersions is the compatibility ceiling used by the installer
// before it is allowed to touch an existing database.
func CurrentMigrationVersions() map[string]int {
	return map[string]int{"core": 2, "admin": 4}
}

// AdminMigrationsPending reports whether MigrateAdmin would add at least one
// ledger version. Callers use this only after the target ownership preflight;
// it does not replace MigrateAdmin's lock and safety checks.
func AdminMigrationsPending(database *sqlx.DB) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var exists int
	if err := database.GetContext(ctx, &exists, `SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema=DATABASE() AND table_name='schema_migrations'`); err != nil {
		return false, err
	}
	if exists == 0 {
		return true, nil
	}
	type versionRow struct {
		Component string `db:"component"`
		Version   int    `db:"version"`
	}
	var rows []versionRow
	if err := database.SelectContext(ctx, &rows, `SELECT component,version FROM schema_migrations`); err != nil {
		return false, err
	}
	seen := make(map[string]map[int]bool)
	for _, row := range rows {
		if seen[row.Component] == nil {
			seen[row.Component] = map[int]bool{}
		}
		seen[row.Component][row.Version] = true
	}
	for component, maximum := range CurrentMigrationVersions() {
		for version := 1; version <= maximum; version++ {
			if !seen[component][version] {
				return true, nil
			}
		}
	}
	return false, nil
}

// RequireSchemaCurrent verifies the migration ledger using read-only queries.
// Long-running services call this instead of attempting DDL with their runtime
// account; migrations remain an explicit deployment/installer responsibility.
func RequireSchemaCurrent(db *sqlx.DB) error {
	return requireMigrationVersions(db, []string{"core"})
}

// RequireAdminSchemaCurrent verifies both the shared and Admin-owned schema.
func RequireAdminSchemaCurrent(db *sqlx.DB) error {
	return requireMigrationVersions(db, []string{"core", "admin"})
}

func requireMigrationVersions(db *sqlx.DB, components []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type versionRow struct {
		Component string `db:"component"`
		Version   int    `db:"version"`
	}
	var rows []versionRow
	if err := db.SelectContext(ctx, &rows, `SELECT component,version FROM schema_migrations WHERE component IN ('core','admin') ORDER BY component,version`); err != nil {
		return fmt.Errorf("schema is not initialized; run admin-server -migrate-only: %w", err)
	}
	want := CurrentMigrationVersions()
	seen := make(map[string]map[int]bool, len(components))
	for _, component := range components {
		seen[component] = make(map[int]bool, want[component])
	}
	for _, row := range rows {
		versions, required := seen[row.Component]
		if !required {
			continue
		}
		if row.Version < 1 {
			return fmt.Errorf("schema component %s has invalid migration version %d", row.Component, row.Version)
		}
		if row.Version > want[row.Component] {
			return fmt.Errorf("schema component %s is newer than this binary: database=%d binary=%d", row.Component, row.Version, want[row.Component])
		}
		versions[row.Version] = true
	}
	for _, component := range components {
		for version := 1; version <= want[component]; version++ {
			if !seen[component][version] {
				return fmt.Errorf("schema component %s is missing migration %d; run admin-server -migrate-only", component, version)
			}
		}
	}
	return nil
}
