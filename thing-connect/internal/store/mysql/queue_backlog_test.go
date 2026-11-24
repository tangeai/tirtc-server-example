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

func TestRecognitionBacklogExhaustedRecoversAndSkipsObsoleteOrDelivered(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
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
	source := m.NewRecognitionBacklog(db)
	base, err := source.Backlog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		bind      string
		delivered bool
		attempts  int
		nextAt    string
		since     string
	}{
		{bind, false, 8, "TIMESTAMPADD(MINUTE,-1,UTC_TIMESTAMP(6))", "DATE_SUB(NOW(6),INTERVAL 3 HOUR)"},   // recently exhausted
		{bind, false, 8, "TIMESTAMPADD(HOUR,-2,UTC_TIMESTAMP(6))", "DATE_SUB(NOW(6),INTERVAL 5 HOUR)"},     // exhausted long ago
		{bind, false, 8, "TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6))", "DATE_SUB(NOW(6),INTERVAL 10 MINUTE)"}, // final attempt in flight
		{bind, false, 1, "UTC_TIMESTAMP(6)", "DATE_SUB(NOW(6),INTERVAL 20 MINUTE)"},
		{bind, true, 0, "UTC_TIMESTAMP(6)", "DATE_SUB(NOW(6),INTERVAL 4 HOUR)"},
		{"obsolete", false, 1, "UTC_TIMESTAMP(6)", "DATE_SUB(NOW(6),INTERVAL 4 HOUR)"},
	} {
		if _, err := db.Exec(`INSERT INTO recognition_outbox(device_id,bind_id,payload,delivered,attempts,next_at,pending_since) VALUES(?,?,'{}',?,?,`+v.nextAt+`,`+v.since+`)`, device, v.bind, v.delivered, v.attempts); err != nil {
			t.Fatal(err)
		}
	}
	b, err := source.Backlog(ctx)
	if err != nil || b.Pending != base.Pending+2 || b.Exhausted != base.Exhausted+1 || b.OldestAge < 20*time.Minute || b.OldestAge > time.Hour {
		t.Fatalf("backlog=%+v base=%+v err=%v", b, base, err)
	}
	// Once outside the alert window an exhausted row no longer degrades health.
	if _, err := db.Exec(`UPDATE recognition_outbox SET next_at=TIMESTAMPADD(HOUR,-2,UTC_TIMESTAMP(6)) WHERE device_id=? AND attempts=8`, device); err != nil {
		t.Fatal(err)
	}
	b, err = source.Backlog(ctx)
	if err != nil || b.Pending != base.Pending+1 || b.Exhausted != base.Exhausted {
		t.Fatalf("exhausted rows did not age out: %+v base=%+v err=%v", b, base, err)
	}
}

func TestLastRecognitionLeaseBecomesTerminalOnFailure(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	device := fmt.Sprintf("terminal%d", time.Now().UnixNano())
	user := seedUser(t, db)
	if _, err := db.Exec(`INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,?,NOW())`, device, user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM recognition_outbox WHERE device_id=?`, device)
		db.Exec(`DELETE FROM device_bind WHERE device_id=?`, device)
	})
	source := m.NewRecognitionBacklog(db)
	base, err := source.Backlog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO recognition_outbox(device_id,bind_id,payload,attempts,next_at)
		SELECT device_id,CONCAT(id,':',binding_revision),'{}',7,UTC_TIMESTAMP(6) FROM device_bind WHERE device_id=?`, device)
	if err != nil {
		t.Fatal(err)
	}
	store := m.NewRecognitionStore(db)
	events, err := store.ClaimEvents(ctx, 1, time.Minute)
	if err != nil || len(events) != 1 || events[0].DeviceID != device {
		t.Fatalf("last claim=%+v err=%v", events, err)
	}
	active, err := source.Backlog(ctx)
	if err != nil || active.Pending != base.Pending+1 || active.Exhausted != base.Exhausted {
		t.Fatalf("leased=%+v err=%v", active, err)
	}
	if ok, err := store.Deliver(ctx, events[0], false); err != nil || !ok {
		t.Fatalf("delivery failure applied=%v err=%v", ok, err)
	}
	terminal, err := source.Backlog(ctx)
	if err != nil || terminal.Pending != base.Pending || terminal.Exhausted != base.Exhausted+1 {
		t.Fatalf("terminal=%+v err=%v", terminal, err)
	}
	if ok, err := store.Deliver(ctx, events[0], true); err != nil || ok {
		t.Fatalf("stale completion applied=%v err=%v", ok, err)
	}
	if events, err := m.NewRecognitionStore(db).ClaimEvents(ctx, 1, time.Minute); err != nil || len(events) != 0 {
		t.Fatalf("exhausted event reclaimed=%v err=%v", events, err)
	}
}
