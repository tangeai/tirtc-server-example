package mysql_test

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"

	mysqlstore "thing-connect/internal/store/mysql"
)

func seedDeviceRole(t *testing.T, sqlDB *sqlx.DB, deviceID, roleID string, userID int64) {
	t.Helper()
	_, err := sqlDB.Exec(`INSERT INTO ai_device_role (device_id, role_id, user_id) VALUES (?,?,?)`, deviceID, roleID, userID)
	if err != nil {
		t.Fatalf("seedDeviceRole: %v", err)
	}
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_device_role WHERE device_id=?`, deviceID) })
}

func seedUserRole(t *testing.T, sqlDB *sqlx.DB, userID int64, roleID string) {
	t.Helper()
	_, err := sqlDB.Exec(`INSERT INTO ai_user_role (user_id, role_id) VALUES (?,?)`, userID, roleID)
	if err != nil {
		t.Fatalf("seedUserRole: %v", err)
	}
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_role WHERE user_id=? AND role_id=?`, userID, roleID) })
}

// ── ai_device_role CRUD ──

func TestSetAndGetDeviceRole(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewRoleBindingStore(sqlDB)
	devID := uniqueDevID()
	ctx := context.Background()

	if err := store.SetDeviceRole(ctx, devID, "role-1", 1); err != nil {
		t.Fatalf("SetDeviceRole: %v", err)
	}
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_device_role WHERE device_id=?`, devID) })

	got, err := store.GetDeviceRole(ctx, devID)
	if err != nil {
		t.Fatalf("GetDeviceRole: %v", err)
	}
	if got != "role-1" {
		t.Errorf("GetDeviceRole: want role-1, got %q", got)
	}
}

func TestGetDeviceRole_NotBound(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewRoleBindingStore(sqlDB)
	ctx := context.Background()

	got, err := store.GetDeviceRole(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetDeviceRole: %v", err)
	}
	if got != "" {
		t.Errorf("GetDeviceRole: want empty, got %q", got)
	}
}

func TestSetDeviceRole_Upsert(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewRoleBindingStore(sqlDB)
	devID := uniqueDevID()
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_device_role WHERE device_id=?`, devID) })

	// First insert
	if err := store.SetDeviceRole(ctx, devID, "role-1", 1); err != nil {
		t.Fatalf("first SetDeviceRole: %v", err)
	}
	// Upsert with same device_id (different role)
	if err := store.SetDeviceRole(ctx, devID, "role-2", 2); err != nil {
		t.Fatalf("second SetDeviceRole: %v", err)
	}

	got, _ := store.GetDeviceRole(ctx, devID)
	if got != "role-2" {
		t.Errorf("after upsert: want role-2, got %q", got)
	}

	// Verify only one row exists
	var count int
	sqlDB.QueryRow(`SELECT COUNT(*) FROM ai_device_role WHERE device_id=?`, devID).Scan(&count)
	if count != 1 {
		t.Errorf("row count after upsert: want 1, got %d", count)
	}
}

func TestDeleteDeviceRole(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewRoleBindingStore(sqlDB)
	devID := uniqueDevID()
	ctx := context.Background()

	// Seed
	if err := store.SetDeviceRole(ctx, devID, "role-1", 1); err != nil {
		t.Fatalf("SetDeviceRole: %v", err)
	}
	// Delete
	if err := store.DeleteDeviceRole(ctx, devID); err != nil {
		t.Fatalf("DeleteDeviceRole: %v", err)
	}
	// Verify gone
	got, _ := store.GetDeviceRole(ctx, devID)
	if got != "" {
		t.Errorf("after delete: want empty, got %q", got)
	}
}

func TestDeleteDeviceRole_NoOp(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewRoleBindingStore(sqlDB)

	// Deleting non-existent device should not error.
	if err := store.DeleteDeviceRole(context.Background(), "nonexistent"); err != nil {
		t.Fatalf("DeleteDeviceRole on nonexistent: %v", err)
	}
}

// ── ai_user_role CRUD ──

func TestAddAndListUserRoles(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserRoleStore(sqlDB)
	userID := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_role WHERE user_id=?`, userID) })

	if err := store.AddUserRole(ctx, userID, "role-a"); err != nil {
		t.Fatalf("AddUserRole role-a: %v", err)
	}
	if err := store.AddUserRole(ctx, userID, "role-b"); err != nil {
		t.Fatalf("AddUserRole role-b: %v", err)
	}

	ids, err := store.ListUserRoleIDs(ctx, userID)
	if err != nil {
		t.Fatalf("ListUserRoleIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ListUserRoleIDs: want 2, got %d: %v", len(ids), ids)
	}
	// Check both IDs present; order isn't guaranteed within same second.
	foundA, foundB := false, false
	for _, id := range ids {
		if id == "role-a" { foundA = true }
		if id == "role-b" { foundB = true }
	}
	if !foundA || !foundB {
		t.Errorf("ListUserRoleIDs: missing IDs, got %v", ids)
	}
}

func TestAddUserRole_Duplicate(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserRoleStore(sqlDB)
	userID := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_role WHERE user_id=?`, userID) })

	if err := store.AddUserRole(ctx, userID, "role-x"); err != nil {
		t.Fatalf("first AddUserRole: %v", err)
	}
	// Duplicate should be silently ignored (INSERT IGNORE).
	if err := store.AddUserRole(ctx, userID, "role-x"); err != nil {
		t.Fatalf("duplicate AddUserRole: %v", err)
	}

	ids, _ := store.ListUserRoleIDs(ctx, userID)
	if len(ids) != 1 {
		t.Fatalf("after duplicate: want 1, got %d", len(ids))
	}
}

func TestRemoveUserRole(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserRoleStore(sqlDB)
	userID := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_role WHERE user_id=?`, userID) })

	store.AddUserRole(ctx, userID, "role-a")
	store.AddUserRole(ctx, userID, "role-b")

	if err := store.RemoveUserRole(ctx, userID, "role-a"); err != nil {
		t.Fatalf("RemoveUserRole: %v", err)
	}

	ids, _ := store.ListUserRoleIDs(ctx, userID)
	if len(ids) != 1 {
		t.Fatalf("after remove: want 1, got %d", len(ids))
	}
	if ids[0] != "role-b" {
		t.Errorf("after remove: want role-b, got %q", ids[0])
	}
}

func TestRemoveUserRole_NoOp(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserRoleStore(sqlDB)

	if err := store.RemoveUserRole(context.Background(), 99999, "nonexistent"); err != nil {
		t.Fatalf("RemoveUserRole on nonexistent: %v", err)
	}
}

func TestListUserRoleIDs_Empty(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserRoleStore(sqlDB)
	userID := seedUser(t, sqlDB)

	ids, err := store.ListUserRoleIDs(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListUserRoleIDs: %v", err)
	}
	if ids == nil {
		t.Error("ListUserRoleIDs: should return empty slice, not nil")
	}
	if len(ids) != 0 {
		t.Errorf("ListUserRoleIDs: want 0, got %d", len(ids))
	}
}
