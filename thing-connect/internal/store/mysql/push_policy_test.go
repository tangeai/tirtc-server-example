package mysql_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"thing-connect/internal/push"
	"thing-connect/internal/recognition"
	m "thing-connect/internal/store/mysql"
)

func TestSourceRefundFencesBatchAndProbe(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	first, peer := m.NewRecognitionStore(db), m.NewRecognitionStore(db)
	policy := push.DefaultDeliveryPolicy()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("DELETE FROM recognition_push_target")
	t.Cleanup(func() { db.Exec("DELETE FROM recognition_push_target") })
	for _, store := range []*m.RecognitionStore{first, peer} {
		if err := store.ConfigureSource(ctx, policy, 0, true); err != nil {
			t.Fatal(err)
		}
	}
	dev := uniqueDevID()
	t.Cleanup(func() { db.Exec("DELETE FROM recognition_user_event_outbox WHERE device_id=?", dev) })
	for _, id := range []string{"a:alarm", "b:alarm"} {
		exec(`INSERT INTO recognition_user_event_outbox(device_id,bind_id,source_event_id,event_type,occurred_at,registered_at,next_at,expires_at) VALUES(?,'test',?,'alarm',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)-INTERVAL 1 DAY,UTC_TIMESTAMP(6)+INTERVAL 120 SECOND)`, dev, id)
	}
	rows, err := first.ClaimUserEvents(ctx, 2)
	if err != nil || len(rows) != 2 || rows[0].Lease != rows[1].Lease {
		t.Fatal(rows, err)
	}
	for i := 0; i < 2; i++ {
		if err = first.RefundAuth(ctx, rows[0]); err != nil {
			t.Fatal(err)
		}
	}
	// Deliberately pass BOTH rows to the original batch acknowledgement.
	for i := range rows {
		rows[i].Outcome = "accepted"
	}
	if err = first.FinishUserEvents(ctx, rows); err != nil {
		t.Fatal(err)
	}
	var status, lease string
	var attempts int
	var reserved bool
	if err = db.QueryRow("SELECT status,lease,attempts,reserved FROM recognition_user_event_outbox WHERE id=?", rows[0].ID).Scan(&status, &lease, &attempts, &reserved); err != nil || status != "pending" || lease != "" || attempts != 0 || reserved {
		t.Fatal(status, lease, attempts, reserved, err)
	}
	if err = db.QueryRow("SELECT status,attempts FROM recognition_user_event_outbox WHERE id=?", rows[1].ID).Scan(&status, &attempts); err != nil || status != "accepted" || attempts != 1 {
		t.Fatal(status, attempts, err)
	}
	if batch, err := peer.ClaimUserEvents(ctx, 2); err != nil || len(batch) != 0 {
		t.Fatal(batch, err)
	}
	exec("UPDATE recognition_push_target SET probe_at=UTC_TIMESTAMP(6)")
	var wg sync.WaitGroup
	leases := make(chan string, 2)
	for _, store := range []*m.RecognitionStore{first, peer} {
		wg.Add(1)
		go func(s *m.RecognitionStore) {
			defer wg.Done()
			l, e := s.ClaimProbe(ctx)
			if e != nil {
				t.Error(e)
			}
			if l != "" {
				leases <- l
			}
		}(store)
	}
	wg.Wait()
	close(leases)
	if len(leases) != 1 {
		t.Fatal("concurrent probe leases", len(leases))
	}
	probe := <-leases
	if err = peer.FinishProbe(ctx, probe, true); err != nil {
		t.Fatal(err)
	}
	batch, err := peer.ClaimUserEvents(ctx, 1)
	if err != nil || len(batch) != 1 || batch[0].ID != rows[0].ID {
		t.Fatal(batch, err)
	}
	// Close while this POST holds the lease; reopen before maintenance.
	policy.Enabled = false
	if err = peer.ConfigureSource(ctx, policy, 1, true); err != nil {
		t.Fatal(err)
	}
	policy.Enabled = true
	if err = peer.ConfigureSource(ctx, policy, 2, true); err != nil {
		t.Fatal(err)
	}
	if ok, e := peer.MayPost(ctx, batch[0]); ok || e != nil {
		t.Fatal(ok, e)
	}
	if err = peer.CleanUserEvents(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&lease, "SELECT lease FROM recognition_user_event_outbox WHERE id=?", batch[0].ID); err != nil || lease != batch[0].Lease {
		t.Fatal(lease, err)
	}
	exec("UPDATE recognition_user_event_outbox SET next_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE id=?", batch[0].ID)
	if err = peer.CleanUserEvents(ctx); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE recognition_user_event_outbox SET next_at=UTC_TIMESTAMP(6) WHERE id=?", batch[0].ID)
	check, err := peer.ClaimUserEvents(ctx, 1)
	if err != nil || len(check) != 1 || !check[0].Checking {
		t.Fatal(check, err)
	}
	check[0].Outcome = "accepted"
	if err = peer.FinishUserEvents(ctx, []recognition.UserEvent{check[0]}); err != nil {
		t.Fatal(err)
	}
}
func TestAdmissionDeadlineAndLookup(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := m.NewPushStore(db)
	e := push.Event{Device: uniqueDevID(), Bind: "1:1", ID: "alarm:alarm", Kind: "alarm", Registered: time.Now().UTC(), Occurred: time.Now().UTC().Add(-3 * time.Minute)}
	e.Expires = e.Occurred.Add(120 * time.Second)
	if _, err := s.Admit(ctx, e, "wx", "template"); !errors.Is(err, push.ErrExpired) {
		t.Fatalf("expired event: %v", err)
	}
	if v, err := s.Lookup(ctx, e); v != "expired" || err != nil {
		t.Fatal(v, err)
	}
}

// Simulate a half-hour outage by advancing queue timestamps in MySQL. Keep an
// old sending request to verify that its unknown result never becomes eligible.
func TestDeliveryRecoveryDropsStaleAndBoundsRealCalls(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := m.NewPushStore(db)
	dev, _ := seedPool(t, db)
	user := seedUser(t, db)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO device_bind(device_id,user_id) VALUES(?,?)", dev, user)
	var bind string
	if err := db.Get(&bind, "SELECT CONCAT(id,':',binding_revision) FROM device_bind WHERE device_id=?", dev); err != nil {
		t.Fatal(err)
	}
	app := "wx" + dev
	gen := uniqueDevID()
	exec("INSERT INTO push_recipient(user_id,app_id,open_id,generation) VALUES(?,?,?,?)", user, app, "open", gen)
	exec("INSERT INTO push_subscription(generation,template_id,grant_id) VALUES(?,?,?)", gen, "template", "grant")
	t.Cleanup(func() {
		for _, table := range []string{"push_event", "push_delivery", "push_window"} {
			db.Exec("DELETE FROM "+table+" WHERE device_id=?", dev)
		}
		db.Exec("DELETE FROM push_channel_limit WHERE app_id=?", app)
		db.Exec("DELETE FROM push_subscription WHERE generation=?", gen)
		db.Exec("DELETE FROM push_recipient WHERE generation=?", gen)
		cleanBind(t, db, dev)
	})
	now := time.Now().UTC().Truncate(time.Microsecond)
	event := push.Event{Device: dev, Bind: bind, Kind: "alarm", Occurred: now, Registered: now, Expires: now.Add(120 * time.Second), MaxAttempts: 2}
	for _, id := range []string{"stale:alarm", "unknown:alarm", "fresh:alarm"} {
		event.ID = id
		if got, err := store.Admit(ctx, event, app, "template"); err != nil || got != "queued" {
			t.Fatal(got, err)
		}
	}
	exec(`UPDATE push_delivery d JOIN push_event e ON e.id=d.event_id SET d.status=IF(e.source_event_id='stale:alarm','pending','sending'),d.expires_at=UTC_TIMESTAMP()-INTERVAL 30 MINUTE,d.next_at=UTC_TIMESTAMP()-INTERVAL 1 MINUTE WHERE d.device_id=? AND e.source_event_id<>'fresh:alarm'`, dev)
	if err := store.MaintainPush(ctx); err != nil {
		t.Fatal(err)
	}
	task, err := store.ClaimPush(ctx)
	if err != nil || task == nil {
		t.Fatal(task, err)
	}
	for i, want := range []bool{true, true, false} {
		allowed, err := store.ReserveSend(ctx, *task)
		if err != nil || allowed != want {
			t.Fatalf("external call %d allowed=%v, err=%v", i+1, allowed, err)
		}
	}
	if err := store.FinishPush(ctx, *task, push.Retry); err != nil {
		t.Fatal(err)
	}
	metrics := store.NotificationMetrics()
	for _, key := range []string{"push_delivery_failed_total", "push_delivery_expired_total", "push_delivery_unknown_total"} {
		if metrics[key] != 1 {
			t.Fatal(key, metrics)
		}
	}
	var states []string
	err = db.Select(&states, `SELECT CONCAT(status,':',attempts) FROM push_delivery WHERE device_id=? ORDER BY id`, dev)
	if err != nil || len(states) != 3 || states[0] != "expired:0" || states[1] != "unknown:0" || states[2] != "failed:2" {
		t.Fatal(states, err)
	}
}

func TestDeliveryOverloadDoesNotSpendWindow(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := m.NewPushStore(db)
	policy := push.DefaultDeliveryPolicy()
	policy.DeliveryCapacity = 32
	s.SetDeliveryPolicy(policy)
	dev, _ := seedPool(t, db)
	user := seedUser(t, db)
	app := "wx" + dev
	gen := uniqueDevID()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO device_bind(device_id,user_id) VALUES(?,?)", dev, user)
	var bind string
	if err := db.Get(&bind, "SELECT CONCAT(id,':',binding_revision) FROM device_bind WHERE device_id=?", dev); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO push_recipient(user_id,app_id,open_id,generation) VALUES(?,?,?,?)", user, app, "open", gen)
	exec("INSERT INTO push_subscription(generation,template_id,grant_id) VALUES(?,?,?)", gen, "template", "grant")
	t.Cleanup(func() {
		for _, table := range []string{"push_event", "push_delivery", "push_window"} {
			db.Exec("DELETE FROM "+table+" WHERE device_id=?", dev)
		}
		db.Exec("DELETE FROM push_subscription WHERE generation=?", gen)
		db.Exec("DELETE FROM push_recipient WHERE generation=?", gen)
		cleanBind(t, db, dev)
	})
	now := time.Now().UTC().Truncate(time.Microsecond)
	ev := push.Event{Kind: "alarm", Device: dev, Bind: bind, Registered: now, Occurred: now, Expires: now.Add(120 * time.Second)}
	var admitted, rejected int
	var last string
	for i := 0; i < 96; i++ {
		ev.ID = fmt.Sprintf("peak%d:alarm", i)
		out, err := s.Admit(ctx, ev, app, "template")
		if err != nil {
			t.Fatal(err)
		}
		var stamp string
		if err = db.Get(&stamp, "SELECT DATE_FORMAT(last_admitted,'%Y-%m-%dT%H:%i:%s.%f') FROM push_window WHERE device_id=?", dev); err != nil {
			t.Fatal(err)
		}
		switch out {
		case "queued":
			admitted++
			last = stamp
		case "skipped_overload":
			rejected++
			if stamp != last {
				t.Fatal("overload moved window")
			}
		default:
			t.Fatal(out)
		}
	}
	if admitted > 32 || rejected == 0 || admitted+rejected != 96 {
		t.Fatal(admitted, rejected)
	}
}
