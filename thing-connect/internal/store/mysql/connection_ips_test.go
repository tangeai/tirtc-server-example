package mysql_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"thing-connect/internal/presence"
	mysqlstore "thing-connect/internal/store/mysql"
	"thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestConnectionIPsPersistAndRejectOlderObservations(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate.Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	users := mysqlstore.NewUserStore(db)
	const email = "connection-ip-test@example.invalid"
	_, _ = db.Exec(`DELETE c FROM user_connection c JOIN users u ON u.id=c.user_id WHERE u.email=?`, email)
	_, _ = db.Exec(`DELETE FROM users WHERE email=?`, email)
	id, err := users.CreateUser(ctx, email, "test-hash", 10, "192.0.2.5")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM user_connection WHERE user_id=?`, id)
		_, _ = db.Exec(`DELETE FROM users WHERE id=?`, id)
	})
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := users.RecordLogin(ctx, id, "2001:db8::1", now); err != nil {
		t.Fatal(err)
	}
	if err := users.RecordLogin(ctx, id, "192.0.2.1", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	u, err := users.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	if u.RegistrationIP != "192.0.2.5" || u.LastLoginIP != "2001:db8::1" || u.LastLoginAt == nil || !u.LastLoginAt.Equal(now) {
		t.Fatalf("unexpected user IP metadata: %+v", u)
	}
	connections := mysqlstore.NewDeviceConnectionStore(db)
	const device = "connection-ip-test"
	_, _ = db.Exec(`DELETE FROM device_connection WHERE device_id=?`, device)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM device_connection WHERE device_id=?`, device) })
	observation := presence.Observation{ClientID: "sn_" + device, IP: "2001:db8::2", ConnectedAt: now, At: now}
	if err := connections.SaveConnections(ctx, []presence.Observation{observation, observation}); err != nil {
		t.Fatal(err)
	}
	observation.IP = "192.0.2.9"
	observation.ConnectedAt = now.Add(-time.Minute)
	if err := connections.SaveConnections(ctx, []presence.Observation{observation}); err != nil {
		t.Fatal(err)
	}
	var ip string
	if err := db.Get(&ip, `SELECT last_online_ip FROM device_connection WHERE device_id=?`, device); err != nil {
		t.Fatal(err)
	}
	if ip != "2001:db8::2" {
		t.Fatal("late connection overwrote latest IP")
	}
	if err := migrate.Migrate(db); err != nil {
		t.Fatalf("migration is not repeatable: %v", err)
	}
}

func TestConnectionIPsLegacyUserAndConcurrentUpdates(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate.Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	email := fmt.Sprintf("ip-concurrent-%d@example.invalid", time.Now().UnixNano())
	result, err := db.Exec(`INSERT INTO users(email,password) VALUES (?, 'test-hash')`, email)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	device := fmt.Sprintf("ip-concurrent-%d", id)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM device_connection WHERE device_id=?`, device)
		_, _ = db.Exec(`DELETE FROM user_connection WHERE user_id=?`, id)
		_, _ = db.Exec(`DELETE FROM users WHERE id=?`, id)
	})
	users := mysqlstore.NewUserStore(db)
	legacy, err := users.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.RegistrationIP != "" || legacy.LastLoginIP != "" || legacy.LastLoginAt != nil {
		t.Fatal("legacy user has fabricated metadata")
	}
	connections := mysqlstore.NewDeviceConnectionStore(db)
	at := time.Now().UTC().Truncate(time.Millisecond)
	const n = 16
	errs := make(chan error, n*2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < n; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			ip := fmt.Sprintf("192.0.2.%d", i+1)
			timestamp := at.Add(time.Duration(i) * time.Millisecond)
			errs <- users.RecordLogin(ctx, id, ip, timestamp)
			errs <- connections.SaveConnections(ctx, []presence.Observation{{ClientID: "sn_" + device, IP: ip, ConnectedAt: timestamp}})
		}(i)
	}
	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	latest, err := users.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	expectedAt := at.Add((n - 1) * time.Millisecond)
	if latest.RegistrationIP != "" || latest.LastLoginIP != "192.0.2.16" || latest.LastLoginAt == nil || !latest.LastLoginAt.Equal(expectedAt) {
		t.Fatalf("incorrect latest user metadata: %+v", latest)
	}
	var row struct {
		IP string    `db:"last_online_ip"`
		At time.Time `db:"last_online_at"`
	}
	if err := db.Get(&row, `SELECT last_online_ip,last_online_at FROM device_connection WHERE device_id=?`, device); err != nil {
		t.Fatal(err)
	}
	if row.IP != "192.0.2.16" || !row.At.Equal(expectedAt) {
		t.Fatalf("incorrect latest device metadata: %+v", row)
	}
	if err := connections.SaveConnections(ctx, []presence.Observation{{ClientID: "tmp_ignored", IP: "192.0.2.9", ConnectedAt: at}, {ClientID: "sn_" + device, IP: "", ConnectedAt: expectedAt.Add(time.Second)}}); err != nil {
		t.Fatal(err)
	}
	var ip string
	if err := db.Get(&ip, `SELECT last_online_ip FROM device_connection WHERE device_id=?`, device); err != nil {
		t.Fatal(err)
	}
	if ip != "192.0.2.16" {
		t.Fatal("empty IP overwrote known IP")
	}
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM device_connection WHERE device_id IN ('tmp_ignored','ignored')`); err != nil || count != 0 {
		t.Fatalf("temporary connection persisted: count=%d err=%v", count, err)
	}
}
