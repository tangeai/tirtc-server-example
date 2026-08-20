package db_test

import (
	"errors"
	"testing"

	"github.com/go-sql-driver/mysql"

	"thing-connect/internal/db"
	"thing-connect/internal/testenv"
)

// TestIsIgnorableDDLError verifies that only already-applied DDL is ignored.
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
	for _, c := range cases {
		if got := db.IsIgnorableDDLError(c.err); got != c.want {
			t.Errorf("%s: IsIgnorableDDLError = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMigrateNewTables(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	defer sqlDB.Close()
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// Check device_bind exists
	var n int
	if err := sqlDB.Get(&n, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='device_bind'`); err != nil || n == 0 {
		t.Errorf("device_bind table missing: n=%d err=%v", n, err)
	}
	// Check device_bind_log exists
	if err := sqlDB.Get(&n, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='device_bind_log'`); err != nil || n == 0 {
		t.Errorf("device_bind_log table missing: n=%d err=%v", n, err)
	}
	// Check users.bind_quota column
	if err := sqlDB.Get(&n, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='users' AND column_name='bind_quota'`); err != nil || n == 0 {
		t.Errorf("users.bind_quota column missing: n=%d err=%v", n, err)
	}
	if err := sqlDB.Get(&n, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='voip_user_profile'`); err != nil || n == 0 {
		t.Errorf("voip_user_profile table missing: n=%d err=%v", n, err)
	}

	// Idempotency: calling Migrate a second time must not return an error.
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("Migrate (second call, idempotency check): %v", err)
	}
	if err := db.MigrateAdmin(sqlDB); err != nil {
		t.Fatalf("MigrateAdmin: %v", err)
	}
	if err := sqlDB.Get(&n, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='admin_users'`); err != nil || n == 0 {
		t.Errorf("admin_users table missing: n=%d err=%v", n, err)
	}
	if err := sqlDB.Get(&n, `SELECT COUNT(*) FROM schema_migrations WHERE component IN ('core','admin')`); err != nil || n != 3 {
		t.Errorf("schema_migrations entries: n=%d err=%v", n, err)
	}
	if err := sqlDB.Get(&n, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='admin_jobs' AND column_name IN ('worker_id','lease_until')`); err != nil || n != 2 {
		t.Errorf("admin job lease columns missing: n=%d err=%v", n, err)
	}
}
