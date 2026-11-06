package mysql_test

import (
	"context"
	"testing"

	mysqlstore "thing-connect/internal/store/mysql"
)

func TestDeviceNameSaveIsIdempotentAndOwnerScoped(t *testing.T) {
	db := openTestDB(t)
	user := seedUser(t, db)
	other := seedUser(t, db)
	device := "name-idempotence-test"
	_, err := db.Exec(`INSERT INTO device_bind(device_id,mac,user_id,last_user_id,device_name,bind_time) VALUES(?,?,?,?,?,NOW())`, device, "name-idempotence-mac", user, user, "书房")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM device_bind WHERE device_id=?`, device) })
	s := mysqlstore.NewUserStore(db)
	for _, name := range []string{"书房", "书房", "客厅", "客厅", "", ""} {
		ok, err := s.UpdateDeviceName(context.Background(), user, device, name)
		if err != nil || !ok {
			t.Fatalf("save %q: ok=%v err=%v", name, ok, err)
		}
		var actual string
		if err := db.Get(&actual, `SELECT device_name FROM device_bind WHERE device_id=?`, device); err != nil || actual != name {
			t.Fatalf("persisted=%q err=%v", actual, err)
		}
	}
	for _, id := range []string{device, "missing-name-device"} {
		ok, err := s.UpdateDeviceName(context.Background(), other, id, "")
		if err != nil || ok {
			t.Fatalf("foreign/missing save: ok=%v err=%v", ok, err)
		}
	}
	// Ownership changes before the save must not be masked as a successful no-op.
	if _, err := db.Exec(`UPDATE device_bind SET user_id=? WHERE device_id=?`, other, device); err != nil {
		t.Fatal(err)
	}
	ok, err := s.UpdateDeviceName(context.Background(), user, device, "")
	if err != nil || ok {
		t.Fatalf("previous owner: ok=%v err=%v", ok, err)
	}
}
