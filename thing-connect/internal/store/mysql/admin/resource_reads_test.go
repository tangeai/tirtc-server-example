package adminmysql

import (
	"context"
	"errors"
	"testing"
	"thing-connect/internal/admin"
	"thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestResourceReadStoreQueriesIPMetadata(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO users(email,password) VALUES ('admin-resource-read@example.invalid','test-hash')`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM user_connection WHERE user_id=?`, id)
		_, _ = db.Exec(`DELETE FROM users WHERE id=?`, id)
	})
	serviceBefore := admin.NewResourceService(NewResourceReadStore(db), nil)
	oldUser, err := serviceBefore.User(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if oldUser.User.RegistrationIP != "" || oldUser.User.LastLoginIP != "" || oldUser.User.LastLoginAt != nil {
		t.Fatal("legacy user metadata should be unknown")
	}
	if _, err := db.Exec(`INSERT INTO user_connection(user_id,registration_ip,last_login_ip) VALUES (?,'192.0.2.1','2001:db8::1')`, id); err != nil {
		t.Fatal(err)
	}
	store := NewResourceReadStore(db)
	service := admin.NewResourceService(store, nil)
	page, err := service.Users(context.Background(), admin.ResourceFilter{Keyword: "admin-resource-read@example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].RegistrationIP != "192.0.2.1" {
		t.Fatalf("page=%+v", page)
	}
	detail, err := service.User(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.User.LastLoginIP != "2001:db8::1" {
		t.Fatal("missing login IP")
	}
	const device = "admin-resource-device"
	if _, err := db.Exec(`INSERT INTO device_bind(device_id,device_name,user_id) VALUES (?,'device',?)`, device, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM device_connection WHERE device_id=?`, device)
		_, _ = db.Exec(`DELETE FROM device_bind WHERE device_id=?`, device)
	})
	oldDevice, err := service.Device(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	if oldDevice.LastOnlineIP != "" || oldDevice.LastOnlineAt != nil {
		t.Fatal("legacy device metadata should be unknown")
	}
	if _, err := db.Exec(`INSERT INTO device_connection(device_id,last_online_ip,last_online_at) VALUES (?,'192.0.2.2',NOW(3))`, device); err != nil {
		t.Fatal(err)
	}
	devices, err := service.Devices(context.Background(), admin.ResourceFilter{Keyword: device})
	if err != nil {
		t.Fatal(err)
	}
	row, err := service.Device(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	if devices.Total != 1 || len(devices.Items) != 1 || devices.Items[0].LastOnlineIP != "192.0.2.2" || devices.Items[0].LastOnlineAt == nil || row.LastOnlineAt == nil || row.LastOnlineIP != "192.0.2.2" {
		t.Fatal("missing device IP")
	}
	if _, err := service.Device(context.Background(), "missing-resource-device"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatal(err)
	}
}
