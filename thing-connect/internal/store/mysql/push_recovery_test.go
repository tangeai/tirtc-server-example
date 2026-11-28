package mysql_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"thing-connect/internal/push"
	m "thing-connect/internal/store/mysql"
)

func TestSubscriptionReportsRespectSessionOrder(t *testing.T) {
	db := openTestDB(t)
	s := m.NewPushStore(db)
	ctx := context.Background()
	user := seedUser(t, db)
	open := uniqueDevID()
	t.Cleanup(func() {
		db.Exec("DELETE s FROM push_subscription s JOIN push_recipient r ON r.generation=s.generation WHERE r.user_id=?", user)
		db.Exec("DELETE FROM push_recipient WHERE user_id=?", user)
		db.Exec("DELETE FROM push_session WHERE user_id=?", user)
	})
	newSession := func() string {
		t.Helper()
		id, _, err := s.Prepare(ctx, user, "wx", open, "template")
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Confirm(ctx, user, id, false); err != nil {
			t.Fatal(err)
		}
		return id
	}
	cancelled := newSession()
	if err := s.Report(ctx, user, cancelled, "cancel"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM push_subscription s JOIN push_recipient r ON r.generation=s.generation WHERE r.user_id=?", user); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	for _, decision := range []string{"reject", "accept"} {
		old, newer := newSession(), newSession()
		for i := 0; i < 2; i++ {
			if err := s.Report(ctx, user, newer, decision); err != nil {
				t.Fatal(err)
			}
		}
		stale := "accept"
		if decision == "accept" {
			stale = "reject"
		}
		if err := s.Report(ctx, user, old, stale); !errors.Is(err, push.ErrConflict) {
			t.Fatal(err)
		}
		if err := s.Report(ctx, user, old, "cancel"); !errors.Is(err, push.ErrConflict) {
			t.Fatal("stale cancel", err)
		}
		if err := s.Report(ctx, user, newSession(), "cancel"); err != nil {
			t.Fatal("fresh cancel", err)
		}
		if err := s.Report(ctx, user, cancelled, "cancel"); err != nil {
			t.Fatal("cancel replay", err)
		}
		var granted bool
		if err := db.Get(&granted, "SELECT granted FROM push_subscription WHERE grant_id=? AND decision_seq=(SELECT session_seq FROM push_session WHERE id=?)", newer, newer); err != nil || granted != (decision == "accept") {
			t.Fatal(granted, err)
		}
	}
}

func TestSourceLookupMissingRespectsFences(t *testing.T) {
	db := openTestDB(t)
	s := m.NewRecognitionStore(db)
	ctx := context.Background()
	dev, _ := seedPool(t, db)
	user := seedUser(t, db)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("DELETE FROM recognition_push_target")
	policy := push.DefaultDeliveryPolicy()
	policy.SourceCapacity = 32
	if err := s.ConfigureSource(ctx, policy, 0, true); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO device_bind(device_id,user_id) VALUES(?,?)", dev, user)
	t.Cleanup(func() {
		db.Exec("DELETE FROM recognition_user_event_outbox WHERE device_id=?", dev)
		db.Exec("DELETE FROM recognition_push_target")
		cleanBind(t, db, dev)
	})
	var bind string
	if err := db.Get(&bind, "SELECT CONCAT(id,':',binding_revision) FROM device_bind WHERE device_id=?", dev); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id, bind           string
		attempts, age, gen int
		want               string
	}{
		{"eligible", bind, 1, 120, 0, "pending"}, {"spent", bind, 3, 120, 0, "attempts_exhausted"}, {"expired", bind, 1, -1, 0, "expired"}, {"closed", bind, 1, 120, -1, "cancelled"}, {"stale", "old", 1, 120, 0, "stale_binding"},
		{"invalid!", bind, 1, 120, 0, "invalid"}, {"full", bind, 1, 120, 0, "skipped_overload"},
	} {
		if c.want == "skipped_overload" {
			exec("INSERT INTO recognition_user_event_outbox(device_id,bind_id,source_event_id,event_type,occurred_at,registered_at,next_at,expires_at) VALUES(?,?,'occupant:alarm','alarm',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)+INTERVAL 1 HOUR,UTC_TIMESTAMP(6)+INTERVAL 2 HOUR)", dev, bind)
		}
		id := c.id + ":alarm"
		exec("INSERT INTO recognition_user_event_outbox(device_id,bind_id,source_event_id,event_type,occurred_at,registered_at,next_at,status,attempts,generation,expires_at) VALUES(?,?,?,'alarm',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),'checking',?,?,TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)))", dev, c.bind, id, c.attempts, c.gen, c.age)
		rows, err := s.ClaimUserEvents(ctx, 1)
		if err != nil || len(rows) != 1 || !rows[0].Checking {
			t.Fatal(rows, err)
		}
		rows[0].Outcome = "not_found"
		if err = s.FinishUserEvents(ctx, rows); err != nil {
			t.Fatal(err)
		}
		var state string
		var attempts int
		if err = db.QueryRow("SELECT status,attempts FROM recognition_user_event_outbox WHERE id=?", rows[0].ID).Scan(&state, &attempts); err != nil || state != c.want || attempts != c.attempts {
			t.Fatal(id, state, attempts, err)
		}
		exec("UPDATE recognition_user_event_outbox SET next_at=UTC_TIMESTAMP(6) WHERE id=?", rows[0].ID)
		if state != "pending" {
			if next, err := s.ClaimUserEvents(ctx, 1); err != nil || len(next) != 0 {
				t.Fatal("terminal row reclaimed", state, next, err)
			}
		} else {
			rows, err = s.ClaimUserEvents(ctx, 1)
			if err != nil || len(rows) != 1 || rows[0].Checking {
				t.Fatal(rows, err)
			}
			rows[0].Outcome = "accepted"
			if err = s.FinishUserEvents(ctx, rows); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestWechatCooldownUsesOneProbeAndRamps(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := m.NewPushStore(db)
	peer := m.NewPushStore(db)
	user := seedUser(t, db)
	dev, _ := seedPool(t, db)
	app := uniqueDevID()
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
	exec("INSERT INTO push_recipient(user_id,app_id,open_id,generation) VALUES(?,?,?,?)", user, app, app, app)
	exec("INSERT INTO push_subscription(generation,grant_id,template_id,bind_id,model_id) VALUES(?,'grant','template',?,'model')", app, bind)
	t.Cleanup(func() {
		for _, q := range []string{"DELETE FROM push_delivery WHERE device_id=?", "DELETE FROM push_event WHERE device_id=?", "DELETE FROM push_window WHERE device_id=?"} {
			db.Exec(q, dev)
		}
		db.Exec("DELETE FROM push_channel_limit WHERE app_id=?", app)
		db.Exec("DELETE FROM push_subscription WHERE generation=?", app)
		db.Exec("DELETE FROM push_recipient WHERE generation=?", app)
		cleanBind(t, db, dev)
	})
	var tasks []push.Delivery
	for _, id := range []string{"first", "probe", "peer", "last"} {
		now := time.Now().UTC()
		e := push.Event{ID: id + ":alarm", Kind: "alarm", Device: dev, Bind: bind, Registered: now, Occurred: now, Expires: now.Add(time.Minute)}
		if got, err := s.Admit(ctx, e, app, "template"); err != nil || got != "queued" {
			t.Fatal(got, err)
		}
		task, err := s.ClaimPush(ctx)
		if err != nil || task == nil {
			t.Fatal(task, err)
		}
		tasks = append(tasks, *task)
	}
	send := func(store *m.PushStore, i int, want bool) {
		t.Helper()
		got, err := store.ReserveSend(ctx, tasks[i])
		if got != want || err != nil {
			t.Fatal(i, got, err)
		}
	}
	finish := func(i int, result push.Result) {
		t.Helper()
		if err := s.FinishPush(ctx, tasks[i], result); err != nil {
			t.Fatal(err)
		}
	}
	due := func() { exec("UPDATE push_channel_limit SET next_at=UTC_TIMESTAMP(6) WHERE app_id=?", app) }
	send(s, 0, true)
	finish(0, push.Limited)
	send(peer, 1, false)
	due()
	send(s, 1, true)
	send(peer, 2, false)
	finish(1, push.Limited)
	var cooldown int
	if err := db.Get(&cooldown, "SELECT cooldown_seconds FROM push_channel_limit WHERE app_id=?", app); err != nil || cooldown != 4 {
		t.Fatal(cooldown, err)
	}
	due()
	send(peer, 2, true)
	finish(2, push.Accepted)
	send(s, 3, true)
	send(peer, 3, false)
	var calls int
	if err := db.Get(&calls, "SELECT SUM(attempts) FROM push_delivery WHERE device_id=?", dev); err != nil || calls != 4 {
		t.Fatal(calls, err)
	}
}
