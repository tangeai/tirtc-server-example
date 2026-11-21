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
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM schema_migrations WHERE component IN ('core','admin')`); err != nil || count != 20 {
		t.Errorf("schema_migrations entries: n=%d err=%v", count, err)
	}
	if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='call_requests'`); err != nil || count != 0 {
		t.Errorf("unreleased call_requests table exists: n=%d err=%v", count, err)
	}
	for _, table := range []string{"call_rooms", "call_room_codes", "call_assignments", "call_leases", "call_outbox", "device_profile", "board_resources", "ai_role_types"} {
		if err := sqlDB.Get(&count, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name IN ('created_at','updated_at') AND is_nullable='NO'`, table); err != nil || count != 2 {
			t.Errorf("%s audit timestamps: n=%d err=%v", table, count, err)
		}
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

func TestQueueObservationUpgradePreservesRowsAndActiveLeases(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	defer db.Close()
	resetTestSchema(t, db)
	if err := migrate.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := migrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"recognition_outbox", "voip_remark_outbox", "ai_cloud_role_target"} {
			if _, err := db.Exec(`DELETE FROM ` + table + ` WHERE device_id IN ('upgrade','leased')`); err != nil {
				t.Error(err)
			}
		}
	}()
	// Reconstruct the immediately preceding schema only inside the guarded
	// disposable test database, including legacy UTC+8 first attempts.
	for _, table := range []string{"recognition_outbox", "voip_remark_outbox", "ai_cloud_role_target"} {
		if _, err := db.Exec(`ALTER TABLE ` + table + ` DROP COLUMN pending_since`); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`DELETE FROM schema_migrations WHERE component='core' AND version=13`,
		`INSERT INTO recognition_outbox(device_id,bind_id,payload,attempts,next_at) VALUES('upgrade','1:1','{}',0,DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 8 HOUR)),('leased','1:1','{}',1,DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 HOUR))`,
		`INSERT INTO voip_remark_outbox(device_id,peer_id) VALUES('upgrade','peer')`,
		`INSERT INTO ai_cloud_role_target(device_id,user_id,binding_revision,role_id) VALUES('upgrade',1,1,'role')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"recognition_outbox", "voip_remark_outbox", "ai_cloud_role_target"} {
		var count int
		if err := db.Get(&count, `SELECT COUNT(*) FROM `+table+` WHERE device_id='upgrade' AND pending_since IS NOT NULL`); err != nil || count != 1 {
			t.Fatalf("%s lost existing data: count=%d err=%v", table, count, err)
		}
	}
	var due, held int
	if err := db.Get(&due, `SELECT COUNT(*) FROM recognition_outbox WHERE device_id='upgrade' AND next_at<=UTC_TIMESTAMP(6)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&held, `SELECT COUNT(*) FROM recognition_outbox WHERE device_id='leased' AND attempts=1 AND next_at>UTC_TIMESTAMP(6)`); err != nil {
		t.Fatal(err)
	}
	if due != 1 || held != 1 {
		t.Fatalf("legacy due=%d active lease=%d", due, held)
	}
}
