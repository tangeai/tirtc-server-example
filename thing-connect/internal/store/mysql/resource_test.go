package mysql_test

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/model"
	mysqlstore "thing-connect/internal/store/mysql"
)

func seedUserResource(t *testing.T, sqlDB *sqlx.DB, userID int64, typ, resourceID, name string) {
	t.Helper()
	_, err := sqlDB.Exec(
		`INSERT INTO ai_user_resource (user_id, type, resource_id, name) VALUES (?,?,?,?)`,
		userID, typ, resourceID, name)
	if err != nil {
		t.Fatalf("seedUserResource: %v", err)
	}
}

// ── Add + List ──

func TestAddAndListUserResources(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserResourceStore(sqlDB)
	userID := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_resource WHERE user_id=?`, userID) })

	if err := store.Add(ctx, userID, model.ResourceTypeMCP, "mcp-1", "My MCP"); err != nil {
		t.Fatalf("Add mcp-1: %v", err)
	}
	if err := store.Add(ctx, userID, model.ResourceTypeMCP, "mcp-2", "Other MCP"); err != nil {
		t.Fatalf("Add mcp-2: %v", err)
	}

	got, err := store.List(ctx, userID, model.ResourceTypeMCP)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List: want 2, got %d: %+v", len(got), got)
	}
	byID := map[string]model.UserResource{}
	for _, r := range got {
		byID[r.ResourceID] = r
	}
	if byID["mcp-1"].Name != "My MCP" {
		t.Errorf("mcp-1 name: want 'My MCP', got %q", byID["mcp-1"].Name)
	}
	if byID["mcp-1"].Type != model.ResourceTypeMCP {
		t.Errorf("mcp-1 type: want %q, got %q", model.ResourceTypeMCP, byID["mcp-1"].Type)
	}
}

func TestListUserResources_Empty(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserResourceStore(sqlDB)
	userID := seedUser(t, sqlDB)

	got, err := store.List(context.Background(), userID, model.ResourceTypeMCP)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil {
		t.Fatal("List: should return empty slice, not nil")
	}
	if len(got) != 0 {
		t.Errorf("List: want 0, got %d", len(got))
	}
}

// ── Add duplicate (INSERT IGNORE no-op) ──

func TestAddUserResource_Duplicate(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserResourceStore(sqlDB)
	userID := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_resource WHERE user_id=?`, userID) })

	if err := store.Add(ctx, userID, model.ResourceTypeMCP, "mcp-x", "First"); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	// Same (user,type,resource_id) again — silently ignored, name NOT overwritten.
	if err := store.Add(ctx, userID, model.ResourceTypeMCP, "mcp-x", "Second"); err != nil {
		t.Fatalf("duplicate Add: %v", err)
	}

	got, _ := store.List(ctx, userID, model.ResourceTypeMCP)
	if len(got) != 1 {
		t.Fatalf("after duplicate: want 1 row, got %d", len(got))
	}
	if got[0].Name != "First" {
		t.Errorf("duplicate Add overwrote name: want 'First', got %q", got[0].Name)
	}
}

// ── Remove ──

func TestRemoveUserResource(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserResourceStore(sqlDB)
	userID := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_resource WHERE user_id=?`, userID) })

	store.Add(ctx, userID, model.ResourceTypeMCP, "mcp-a", "A")
	store.Add(ctx, userID, model.ResourceTypeMCP, "mcp-b", "B")

	if err := store.Remove(ctx, userID, model.ResourceTypeMCP, "mcp-a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	got, _ := store.List(ctx, userID, model.ResourceTypeMCP)
	if len(got) != 1 || got[0].ResourceID != "mcp-b" {
		t.Fatalf("after remove: want [mcp-b], got %+v", got)
	}
}

func TestRemoveUserResource_NoOp(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserResourceStore(sqlDB)

	// Removing non-existent row must not error.
	if err := store.Remove(context.Background(), 99999, model.ResourceTypeMCP, "nope"); err != nil {
		t.Fatalf("Remove on nonexistent: %v", err)
	}
}

// ── Count (quota check) ──

func TestCountUserResources(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserResourceStore(sqlDB)
	userID := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_resource WHERE user_id=?`, userID) })

	n, err := store.Count(ctx, userID, model.ResourceTypeMCP)
	if err != nil {
		t.Fatalf("Count empty: %v", err)
	}
	if n != 0 {
		t.Fatalf("Count empty: want 0, got %d", n)
	}

	store.Add(ctx, userID, model.ResourceTypeMCP, "mcp-1", "1")
	store.Add(ctx, userID, model.ResourceTypeMCP, "mcp-2", "2")
	store.Add(ctx, userID, model.ResourceTypeMCP, "mcp-3", "3")

	n, err = store.Count(ctx, userID, model.ResourceTypeMCP)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 3 {
		t.Errorf("Count: want 3, got %d", n)
	}
}

// ── UpdateName (sync from cloud rename) ──

func TestUpdateUserResourceName(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserResourceStore(sqlDB)
	userID := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_resource WHERE user_id=?`, userID) })

	store.Add(ctx, userID, model.ResourceTypeKB, "kb-1", "Old Name")

	if err := store.UpdateName(ctx, userID, model.ResourceTypeKB, "kb-1", "New Name"); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}

	got, _ := store.List(ctx, userID, model.ResourceTypeKB)
	if len(got) != 1 || got[0].Name != "New Name" {
		t.Fatalf("after UpdateName: want 'New Name', got %+v", got)
	}
}

func TestUpdateUserResourceName_OwnerScoped(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserResourceStore(sqlDB)
	userID := seedUser(t, sqlDB)
	other := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_resource WHERE user_id IN (?,?)`, userID, other) })

	store.Add(ctx, userID, model.ResourceTypeKB, "kb-shared", "Owner")

	// Another user cannot rename a resource they don't own — must be a no-op.
	if err := store.UpdateName(ctx, other, model.ResourceTypeKB, "kb-shared", "Hijacked"); err != nil {
		t.Fatalf("UpdateName by non-owner: %v", err)
	}

	got, _ := store.List(ctx, userID, model.ResourceTypeKB)
	if len(got) != 1 || got[0].Name != "Owner" {
		t.Fatalf("non-owner rename should not apply: want 'Owner', got %+v", got)
	}
}

// ── Strict privacy: a user only sees their own resources ──

func TestListUserResources_UserIsolation(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserResourceStore(sqlDB)
	alice := seedUser(t, sqlDB)
	bob := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_resource WHERE user_id IN (?,?)`, alice, bob) })

	store.Add(ctx, alice, model.ResourceTypeMCP, "alice-mcp", "Alice's MCP")

	// Bob must not see Alice's resource.
	got, err := store.List(ctx, bob, model.ResourceTypeMCP)
	if err != nil {
		t.Fatalf("List as Bob: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("privacy leak: Bob saw Alice's resource: %+v", got)
	}
}

// ── Type isolation: same user's resource types don't mix ──

func TestListUserResources_TypeIsolation(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserResourceStore(sqlDB)
	userID := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_resource WHERE user_id=?`, userID) })

	store.Add(ctx, userID, model.ResourceTypeMCP, "res-1", "an mcp")
	store.Add(ctx, userID, model.ResourceTypeKB, "res-1", "a kb") // same resource_id, different type
	store.Add(ctx, userID, model.ResourceTypeDevicePlugin, "res-1", "a dev plugin")
	store.Add(ctx, userID, model.ResourceTypeKBFile, "res-1", "a knowledge file")

	mcp, _ := store.List(ctx, userID, model.ResourceTypeMCP)
	kb, _ := store.List(ctx, userID, model.ResourceTypeKB)
	dp, _ := store.List(ctx, userID, model.ResourceTypeDevicePlugin)
	kbFile, _ := store.List(ctx, userID, model.ResourceTypeKBFile)

	if len(mcp) != 1 || mcp[0].Name != "an mcp" {
		t.Errorf("mcp list: want 1 'an mcp', got %+v", mcp)
	}
	if len(kb) != 1 || kb[0].Name != "a kb" {
		t.Errorf("kb list: want 1 'a kb', got %+v", kb)
	}
	if len(dp) != 1 || dp[0].Name != "a dev plugin" {
		t.Errorf("device_plugin list: want 1 'a dev plugin', got %+v", dp)
	}
	if len(kbFile) != 1 || kbFile[0].Name != "a knowledge file" {
		t.Errorf("kb_file list: want 1 'a knowledge file', got %+v", kbFile)
	}

	n, _ := store.Count(ctx, userID, model.ResourceTypeMCP)
	if n != 1 {
		t.Errorf("Count mcp: want 1, got %d", n)
	}
}

// ── Exists (ownership check) ──

func TestExistsUserResource(t *testing.T) {
	sqlDB := openTestDB(t)
	store := mysqlstore.NewUserResourceStore(sqlDB)
	userID := seedUser(t, sqlDB)
	other := seedUser(t, sqlDB)
	ctx := context.Background()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM ai_user_resource WHERE user_id IN (?,?)`, userID, other) })

	store.Add(ctx, userID, model.ResourceTypeMCP, "m1", "M1")

	// owner → true
	ok, err := store.Exists(ctx, userID, model.ResourceTypeMCP, "m1")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !ok {
		t.Error("owner: want true, got false")
	}
	// wrong id → false
	if ok, _ := store.Exists(ctx, userID, model.ResourceTypeMCP, "nope"); ok {
		t.Error("nonexistent id: want false, got true")
	}
	// other user → false (privacy: B must not "own" A's resource)
	if ok, _ := store.Exists(ctx, other, model.ResourceTypeMCP, "m1"); ok {
		t.Error("other user: want false, got true (privacy leak)")
	}
	// wrong type → false
	if ok, _ := store.Exists(ctx, userID, model.ResourceTypeKB, "m1"); ok {
		t.Error("wrong type: want false, got true")
	}
}
