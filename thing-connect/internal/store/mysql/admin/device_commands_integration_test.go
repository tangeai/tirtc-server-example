package adminmysql

import (
	"context"
	"fmt"
	"testing"
	"time"

	adminapp "thing-connect/internal/admin"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestForceUnbindCommitsCanonicalMutationAndAudit(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../../tests/testdata/config.yaml")
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := mysqlmigrate.MigrateAdmin(sqlDB); err != nil {
		t.Fatalf("MigrateAdmin: %v", err)
	}

	ctx := context.Background()
	deviceID := fmt.Sprintf("admin-unbind-%d", time.Now().UnixNano())
	email := fmt.Sprintf("admin-unbind-%d@example.com", time.Now().UnixNano())
	userResult, err := sqlDB.ExecContext(ctx, `INSERT INTO users (email,password,bind_quota) VALUES (?, 'unused', 1)`, email)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := userResult.LastInsertId()
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM admin_audit_log WHERE resource_type='device' AND resource_id=?`, deviceID)
		_, _ = sqlDB.Exec(`DELETE FROM cleanup_outbox WHERE device_id=?`, deviceID)
		_, _ = sqlDB.Exec(`DELETE FROM device_bind_log WHERE device_id=?`, deviceID)
		_, _ = sqlDB.Exec(`DELETE FROM device_bind WHERE device_id=?`, deviceID)
		_, _ = sqlDB.Exec(`DELETE FROM device_pool WHERE device_id=?`, deviceID)
		_, _ = sqlDB.Exec(`DELETE FROM users WHERE id=?`, userID)
	})
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO device_pool (device_id,device_key,status) VALUES (?,'key',1)`, deviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO device_bind (device_id,mac,assign,device_name,user_id,last_user_id,bind_time) VALUES (?,'AA:BB','dynamic','客厅',?,?,NOW())`, deviceID, userID, userID); err != nil {
		t.Fatal(err)
	}

	mutation := adminapp.ForceUnbindMutation{
		DeviceID: deviceID, ExpectedUserID: userID, CleanupTargets: []string{"ai", "voip", "call"},
		Audit: adminapp.AuditEvent{AdminUserID: 1, RequestID: "admin-unbind-test", Method: "POST", Path: "/test", HTTPStatus: 200, Action: "device.unbind", Resource: "device", ResourceID: deviceID, Reason: "integration test", Before: fmt.Sprintf(`{"user_id":%d}`, userID), After: `{"user_id":0}`, Success: true},
	}
	if err := NewDeviceCommandStore(sqlDB).ForceUnbind(ctx, mutation); err != nil {
		t.Fatal(err)
	}

	var state struct {
		UserID     int64  `db:"user_id"`
		DeviceName string `db:"device_name"`
		PoolStatus int    `db:"pool_status"`
		Quota      int    `db:"quota"`
	}
	if err := sqlDB.GetContext(ctx, &state, `SELECT b.user_id,b.device_name,p.status pool_status,u.bind_quota quota FROM device_bind b JOIN device_pool p ON p.device_id=b.device_id JOIN users u ON u.id=? WHERE b.device_id=?`, userID, deviceID); err != nil {
		t.Fatal(err)
	}
	if state.UserID != 0 || state.DeviceName != "" || state.PoolStatus != 0 || state.Quota != 2 {
		t.Fatalf("unexpected unbind state: %+v", state)
	}
	var cleanupCount, auditCount int
	if err := sqlDB.GetContext(ctx, &cleanupCount, `SELECT COUNT(*) FROM cleanup_outbox WHERE device_id=?`, deviceID); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.GetContext(ctx, &auditCount, `SELECT COUNT(*) FROM admin_audit_log WHERE action='device.unbind' AND resource_id=?`, deviceID); err != nil {
		t.Fatal(err)
	}
	if cleanupCount != 3 || auditCount != 1 {
		t.Fatalf("cleanup=%d audit=%d, want 3/1", cleanupCount, auditCount)
	}
}
