package admin

import (
	"context"
	"fmt"
	"testing"
	"time"

	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestVoIPMenuSplitPreservesGrantsOnlyOnCreation(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := mysqlmigrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	ctx := context.Background()
	if err := store.SeedDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO admin_roles(code,name) VALUES (?,?)`, fmt.Sprintf("voip_split_%d", time.Now().UnixNano()), "VoIP split test")
	if err != nil {
		t.Fatal(err)
	}
	roleID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(`DELETE FROM admin_role_menus WHERE role_id=?`, roleID)
		_, _ = db.Exec(`DELETE FROM admin_roles WHERE id=?`, roleID)
	}()
	// Reproduce an installation with only the original combined page.
	if _, err = db.Exec(`DELETE rm FROM admin_role_menus rm JOIN admin_menus m ON m.id=rm.menu_id WHERE m.menu_code='voip-devices'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM admin_menus WHERE menu_code='voip-devices'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO admin_role_menus(role_id,menu_id) SELECT ?,id FROM admin_menus WHERE menu_code='voip-config'`, roleID); err != nil {
		t.Fatal(err)
	}
	if err = store.SeedDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount := func(want int) {
		t.Helper()
		var count int
		if err := db.Get(&count, `SELECT COUNT(*) FROM admin_role_menus rm JOIN admin_menus m ON m.id=rm.menu_id JOIN admin_menus parent ON parent.id=m.parent_id WHERE rm.role_id=? AND m.menu_code='voip-devices' AND m.path='/voip-devices' AND m.permission_code='voip.app.read' AND parent.menu_code='business'`, roleID); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("business menu grants=%d, want %d", count, want)
		}
	}
	assertCount(1)
	if _, err = db.Exec(`DELETE rm FROM admin_role_menus rm JOIN admin_menus m ON m.id=rm.menu_id WHERE rm.role_id=? AND m.menu_code='voip-devices'`, roleID); err != nil {
		t.Fatal(err)
	}
	if err = store.SeedDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount(0)
}

func TestDefaultRoleRevocationsSurviveRestart(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := mysqlmigrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	ctx := context.Background()
	if err := store.SeedDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	var roleID, menuID int64
	if err := db.Get(&roleID, `SELECT id FROM admin_roles WHERE code='operations_admin'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&menuID, `SELECT id FROM admin_menus WHERE menu_code='users'`); err != nil {
		t.Fatal(err)
	}
	// This fixture runs only against a dedicated _test database.
	t.Cleanup(func() {
		_, _ = db.Exec(`INSERT IGNORE INTO admin_role_permissions(role_id,permission_code) VALUES (?,'user.read')`, roleID)
		_, _ = db.Exec(`INSERT IGNORE INTO admin_role_menus(role_id,menu_id) VALUES (?,?)`, roleID, menuID)
	})
	if _, err := db.Exec(`DELETE FROM admin_role_permissions WHERE role_id=? AND permission_code='user.read'`, roleID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM admin_role_menus WHERE role_id=? AND menu_id=?`, roleID, menuID); err != nil {
		t.Fatal(err)
	}
	if err := store.SeedDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	var permissions, menus int
	if err := db.Get(&permissions, `SELECT COUNT(*) FROM admin_role_permissions WHERE role_id=? AND permission_code='user.read'`, roleID); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&menus, `SELECT COUNT(*) FROM admin_role_menus WHERE role_id=? AND menu_id=?`, roleID, menuID); err != nil {
		t.Fatal(err)
	}
	if permissions != 0 || menus != 0 {
		t.Fatalf("revoked grants restored after restart: permissions=%d menus=%d", permissions, menus)
	}
}

func TestMenuSettingsSurviveRestart(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := mysqlmigrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := NewStore(db)
	if err := store.SeedDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	var before Menu
	query := `SELECT id,name,parent_id,sort_no,visible,status FROM admin_menus WHERE menu_code='users'`
	if err := db.Get(&before, query); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`UPDATE admin_menus SET name=?,parent_id=?,sort_no=?,visible=?,status=? WHERE id=?`, before.Name, before.ParentID, before.SortNo, before.Visible, before.Status, before.ID)
	})
	if _, err := db.Exec(`UPDATE admin_menus SET name='自定义用户入口',parent_id=0,sort_no=987,visible=0,status=0 WHERE id=?`, before.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := NewStore(db).SeedDefaults(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var after Menu
	if err := db.Get(&after, query); err != nil {
		t.Fatal(err)
	}
	if after.Name != "自定义用户入口" || after.ParentID != 0 || after.SortNo != 987 || after.Visible != 0 || after.Status != 0 {
		t.Fatalf("menu settings overwritten: %+v", after)
	}
}
