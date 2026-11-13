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
	defer db.Close()
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
