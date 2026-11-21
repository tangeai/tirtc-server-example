package mysql_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	m "thing-connect/internal/store/mysql"
)

func TestCloudBacklogPreservesPendingAgeAndRestartsSettledCycle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	device := fmt.Sprintf("Q%d", time.Now().UnixNano())
	user := seedUser(t, db)
	if _, err := db.Exec(`INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,?,NOW())`, device, user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM ai_cloud_role_target WHERE device_id=?`, device)
		db.Exec(`DELETE FROM ai_cloud_role_ref WHERE device_id=?`, device)
		db.Exec(`DELETE FROM device_bind WHERE device_id=?`, device)
	})
	store := m.NewRoleLifecycleStore(db)
	source := m.NewAICloudBacklog(db)
	base, err := source.Backlog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := store.PrepareCloud(ctx, user, []string{device}, "role-a", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE ai_cloud_role_target SET pending_since=DATE_SUB(NOW(6),INTERVAL 30 MINUTE) WHERE device_id=?`, device); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.PrepareCloud(ctx, user, []string{device}, "role-b", false)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.CompleteCloud(ctx, targets[0], true); err != nil || ok {
		t.Fatalf("stale completion: %v %v", ok, err)
	}
	b, err := source.Backlog(ctx)
	if err != nil || b.Pending != base.Pending+1 || b.OldestAge < 30*time.Minute {
		t.Fatalf("backlog=%+v err=%v", b, err)
	}
	if ok, err := store.CompleteCloud(ctx, replacement[0], true); err != nil || !ok {
		t.Fatalf("completion: %v %v", ok, err)
	}
	b, err = source.Backlog(ctx)
	if err != nil || b.Pending != base.Pending {
		t.Fatalf("settled counted: %+v %v", b, err)
	}
	if _, err = store.PrepareCloud(ctx, user, []string{device}, "role-c", false); err != nil {
		t.Fatal(err)
	}
	var age int64
	if err = db.Get(&age, `SELECT TIMESTAMPDIFF(SECOND,pending_since,NOW(6)) FROM ai_cloud_role_target WHERE device_id=?`, device); err != nil || age > 5 {
		t.Fatalf("new cycle age=%d err=%v", age, err)
	}
}

func TestRecognitionBacklogIncludesExhaustedButNotObsoleteOrDelivered(t *testing.T) {
	db := openTestDB(t)
	device := fmt.Sprintf("Q%d", time.Now().UnixNano())
	if _, err := db.Exec(`INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,1,NOW())`, device); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM recognition_outbox WHERE device_id=?`, device)
		db.Exec(`DELETE FROM device_bind WHERE device_id=?`, device)
	})
	var bind string
	if err := db.Get(&bind, `SELECT CONCAT(id,':',binding_revision) FROM device_bind WHERE device_id=?`, device); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		bind      string
		delivered bool
		attempts  int
	}{{bind, false, 8}, {bind, false, 1}, {bind, true, 0}, {"obsolete", false, 8}} {
		if _, err := db.Exec(`INSERT INTO recognition_outbox(device_id,bind_id,payload,delivered,attempts,next_at,pending_since) VALUES(?,?, '{}',?,?,UTC_TIMESTAMP(6),DATE_SUB(NOW(6),INTERVAL 20 MINUTE))`, device, v.bind, v.delivered, v.attempts); err != nil {
			t.Fatal(err)
		}
	}
	b, err := m.NewRecognitionBacklog(db).Backlog(context.Background())
	if err != nil || b.Pending != 2 || b.Exhausted != 1 || b.OldestAge < 20*time.Minute {
		t.Fatalf("backlog=%+v err=%v", b, err)
	}
}
