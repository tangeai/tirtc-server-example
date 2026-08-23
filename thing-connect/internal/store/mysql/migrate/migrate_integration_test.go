package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestIsIgnorableDDLError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"dup column 1060", &mysql.MySQLError{Number: 1060}, true},
		{"dup key 1061", &mysql.MySQLError{Number: 1061}, true},
		{"alter denied 1142", &mysql.MySQLError{Number: 1142}, false},
		{"other mysql error", &mysql.MySQLError{Number: 1146}, false},
		{"non-mysql error", errors.New("boom"), false},
	}
	for _, test := range cases {
		if got := migrate.IsIgnorableDDLError(test.err); got != test.want {
			t.Errorf("%s: IsIgnorableDDLError = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestMigrateNewTables(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../../tests/testdata/config.yaml")
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	defer sqlDB.Close()
	resetTestSchema(t, sqlDB)
	if err := migrate.Migrate(sqlDB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var count int
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='device_bind'`); err != nil || count == 0 {
		t.Errorf("device_bind table missing: n=%d err=%v", count, err)
	}
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='device_bind_log'`); err != nil || count == 0 {
		t.Errorf("device_bind_log table missing: n=%d err=%v", count, err)
	}
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='users' AND column_name='bind_quota'`); err != nil || count == 0 {
		t.Errorf("users.bind_quota column missing: n=%d err=%v", count, err)
	}
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='voip_user_profile'`); err != nil || count == 0 {
		t.Errorf("voip_user_profile table missing: n=%d err=%v", count, err)
	}

	if err := migrate.Migrate(sqlDB); err != nil {
		t.Fatalf("Migrate (second call, idempotency check): %v", err)
	}
	if err := migrate.MigrateAdmin(sqlDB); err != nil {
		t.Fatalf("MigrateAdmin: %v", err)
	}
	if err := migrate.RequireSchemaCurrent(sqlDB); err != nil {
		t.Fatalf("RequireSchemaCurrent: %v", err)
	}
	if err := migrate.RequireAdminSchemaCurrent(sqlDB); err != nil {
		t.Fatalf("RequireAdminSchemaCurrent: %v", err)
	}
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='admin_users'`); err != nil || count == 0 {
		t.Errorf("admin_users table missing: n=%d err=%v", count, err)
	}
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM schema_migrations WHERE component IN ('core','admin')`); err != nil || count != 2 {
		t.Errorf("schema_migrations entries: n=%d err=%v", count, err)
	}
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='config_entries' AND column_name='secret_value'`); err != nil || count != 1 {
		t.Errorf("config plaintext secret column missing: n=%d err=%v", count, err)
	}
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND ((table_name='users' AND index_name IN ('idx_users_created','idx_users_status_created')) OR (table_name='device_bind' AND index_name IN ('idx_device_active_time','idx_device_bind_time')))`); err != nil || count < 8 {
		t.Errorf("admin list sorting indexes missing: rows=%d err=%v", count, err)
	}
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='admin_jobs' AND column_name IN ('worker_id','lease_until')`); err != nil || count != 2 {
		t.Errorf("admin job lease columns missing: n=%d err=%v", count, err)
	}
}

func resetTestSchema(t *testing.T, sqlDB *sqlx.DB) {
	t.Helper()
	ctx := context.Background()
	conn, err := sqlDB.Connx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var databaseName string
	if err := conn.GetContext(ctx, &databaseName, `SELECT DATABASE()`); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.ToLower(databaseName), "_test") {
		t.Fatalf("refusing to reset non-test database %q", databaseName)
	}
	var tables []string
	if err := conn.SelectContext(ctx, &tables, `SELECT table_name FROM information_schema.tables
		WHERE table_schema=DATABASE() AND table_type='BASE TABLE' ORDER BY table_name`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS=0`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), `SET FOREIGN_KEY_CHECKS=1`); err != nil {
			t.Errorf("restore foreign key checks: %v", err)
		}
	}()
	for _, table := range tables {
		quoted := "`" + strings.ReplaceAll(table, "`", "``") + "`"
		if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS `+quoted); err != nil {
			t.Fatalf("drop test table %s: %v", quoted, err)
		}
	}
}
