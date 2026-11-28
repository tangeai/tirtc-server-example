package mysql_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"thing-connect/internal/push"
	m "thing-connect/internal/store/mysql"
)

func TestPushLongSubscriptionLifecycle(t *testing.T) {
	db := openTestDB(t)
	s := m.NewPushStore(db)
	user := seedUser(t, db)
	device, _ := seedPool(t, db)
	if _, err := db.Exec("INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,?,NOW())", device, user); err != nil {
		t.Fatal(err)
	}
	var bind string
	if err := db.Get(&bind, "SELECT CONCAT(id,':',binding_revision) FROM device_bind WHERE device_id=?", device); err != nil {
		t.Fatal(err)
	}
	defer cleanBind(t, db, device)
	t.Cleanup(func() {
		for _, table := range []string{"push_delivery", "push_event", "push_window"} {
			db.Exec("DELETE FROM "+table+" WHERE device_id=?", device)
		}
		db.Exec("DELETE s FROM push_subscription s JOIN push_recipient r ON r.generation=s.generation WHERE r.user_id=?", user)
		db.Exec("DELETE FROM push_recipient WHERE user_id=?", user)
		db.Exec("DELETE FROM push_session WHERE user_id=?", user)
	})
	ctx := context.Background()
	e := push.Event{ID: "one:alarm", Device: device, Bind: bind, Kind: "alarm", LabelText: "哭泣、陌生词", Registered: time.Now().UTC().Truncate(time.Microsecond), Occurred: time.Now().UTC()}
	admit := func(v push.Event, want string) {
		t.Helper()
		got, err := s.Admit(ctx, v, "app", push.AlarmTemplate)
		if err != nil || got != want {
			t.Fatalf("admit %s: %s %v", v.ID, got, err)
		}
	}
	n := 60
	if _, err := s.Preference(ctx, user, device, "alarm", true, &n); err != nil {
		t.Fatal(err)
	}
	admit(e, "unsubscribed")
	id, replace, err := s.PrepareDevice(ctx, user, "app", "openid-"+device, push.AlarmTemplate, device, "model")
	if err != nil || replace {
		t.Fatal(replace, err)
	}
	if err = s.Confirm(ctx, user, id, false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Report(ctx, user, id, "accept"); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		app, template string
		want          bool
	}{
		{"app", push.AlarmTemplate, true}, {"other", push.AlarmTemplate, false}, {"app", "other", false},
	} {
		got, err := s.HasDeviceGrant(ctx, user, tc.app, tc.template, device)
		if err != nil || got != tc.want {
			t.Fatalf("grant %q %q: %v %v", tc.app, tc.template, got, err)
		}
	}
	admit(e, "unsubscribed")
	e.ID = "two:alarm"
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := s.Admit(ctx, e, "app", push.AlarmTemplate)
			if err != nil || v != "queued" {
				t.Errorf("duplicate %s %v", v, err)
			}
		}()
	}
	wg.Wait()
	e.ID = "three:alarm"
	admit(e, "interval")
	legacy, _, err := s.Prepare(ctx, user, "app", "openid-"+device, push.AlarmTemplate)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Confirm(ctx, user, legacy, false); err != nil {
		t.Fatal(err)
	}
	if err = s.Report(ctx, user, legacy, "accept"); err != nil {
		t.Fatal(err)
	}
	e.Kind = "quarrel"
	e.ID = "two:quarrel"
	admit(e, "queued")
	d, err := s.ClaimPush(ctx)
	if err != nil || d == nil {
		t.Fatalf("claim: %v", err)
	}
	detail, err := s.Reference(ctx, user, d.Reference)
	if err != nil || detail.LabelText != e.LabelText || d.LabelText != e.LabelText || detail.Device != device {
		t.Fatal(detail, d, err)
	}
	if err = s.FinishPush(ctx, *d, push.Unknown); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE device_bind SET binding_revision=binding_revision+1 WHERE device_id=?", device); err != nil {
		t.Fatal(err)
	}
	if d, err := s.ClaimPush(ctx); err != nil || d != nil {
		t.Fatalf("stale claim: %v %v", d, err)
	}
	e.ID = "late:quarrel"
	if _, err = s.Admit(ctx, e, "app", push.AlarmTemplate); !errors.Is(err, push.ErrBinding) {
		t.Fatal(err)
	}
	e.ID = "two:quarrel"
	admit(e, "queued")
	if err = s.MaintainPush(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPushReplaceIdentityBeforeSubscription(t *testing.T) {
	db := openTestDB(t)
	s := m.NewPushStore(db)
	ctx := context.Background()
	a, b := seedUser(t, db), seedUser(t, db)
	identity := "wx-" + uniqueDevID()
	t.Cleanup(func() {
		db.Exec("DELETE s FROM push_subscription s JOIN push_recipient r ON r.generation=s.generation WHERE r.user_id IN (?,?)", a, b)
		db.Exec("DELETE FROM push_recipient WHERE user_id IN (?,?)", a, b)
		db.Exec("DELETE FROM push_session WHERE user_id IN (?,?)", a, b)
	})
	first, replace, err := s.Prepare(ctx, a, "app", identity, push.AlarmTemplate)
	if err != nil || replace {
		t.Fatal(replace, err)
	}
	if err = s.Confirm(ctx, a, first, false); err != nil {
		t.Fatal(err)
	}
	if err = s.Report(ctx, a, first, "accept"); err != nil {
		t.Fatal(err)
	}
	transfer, replace, err := s.Prepare(ctx, b, "app", identity, push.AlarmTemplate)
	if err != nil || !replace {
		t.Fatal(replace, err)
	}
	if err = s.Confirm(ctx, b, transfer, false); !errors.Is(err, push.ErrConflict) {
		t.Fatal(err)
	}
	if err = s.Report(ctx, b, transfer, "accept"); !errors.Is(err, push.ErrConflict) {
		t.Fatal(err)
	}
	var owner int64
	if err = db.Get(&owner, "SELECT user_id FROM push_recipient WHERE app_id=? AND open_id=?", "app", identity); err != nil || owner != a {
		t.Fatal(owner, err)
	}
	if err = s.Confirm(ctx, b, transfer, true); err != nil {
		t.Fatal(err)
	}
	if err = s.Report(ctx, b, transfer, "accept"); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&owner, "SELECT user_id FROM push_recipient WHERE app_id=? AND open_id=?", "app", identity); err != nil || owner != b {
		t.Fatal(owner, err)
	}
}

func TestPushExpiryAndLateAck(t *testing.T) {
	db := openTestDB(t)
	s := m.NewPushStore(db)
	ctx := context.Background()
	dev, _ := seedPool(t, db)
	user := seedUser(t, db)
	if _, err := db.Exec("INSERT INTO device_bind(device_id,user_id) VALUES(?,?)", dev, user); err != nil {
		t.Fatal(err)
	}
	var bind string
	if err := db.Get(&bind, "SELECT CONCAT(id,':',binding_revision) FROM device_bind WHERE device_id=?", dev); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM push_delivery WHERE device_id=?", dev)
		db.Exec("DELETE FROM push_event WHERE device_id=?", dev)
		db.Exec("DELETE FROM push_window WHERE device_id=?", dev)
		cleanBind(t, db, dev)
	})
	e := push.Event{Device: dev, Bind: bind, ID: "old:crying", Kind: "crying", Registered: time.Now().UTC().Add(-25 * time.Hour)}
	if _, err := s.Admit(ctx, e, "app", push.AlarmTemplate); !errors.Is(err, push.ErrExpired) {
		t.Fatal(err)
	}
	e.Registered = time.Now().UTC().Add(-time.Hour)
	e.Occurred = time.Now().UTC()
	e.ID = "one:crying"
	if v, err := s.Admit(ctx, e, "app", push.AlarmTemplate); err != nil || v != "unsubscribed" {
		t.Fatal(v, err)
	}
	// Simulate a lost response followed by final confirmation after 24h.
	if _, err := db.Exec("UPDATE push_event SET registered_at=registered_at-INTERVAL 24 HOUR WHERE device_id=?", dev); err != nil {
		t.Fatal(err)
	}
	e.Registered = e.Registered.Add(-24 * time.Hour)
	if v, err := s.Admit(ctx, e, "app", push.AlarmTemplate); err != nil || v != "unsubscribed" {
		t.Fatal(v, err)
	}
	for i, status := range []string{"pending", "sending"} {
		res, err := db.Exec(`INSERT INTO push_delivery(event_id,user_id,device_id,device_name,bind_id,event_type,generation,grant_id,template_id,occurred_at,reference_id,started_at,status,next_at,lease) VALUES(?,?,?,?,?,'crying','generation','grant','template',UTC_TIMESTAMP()-INTERVAL 11 MINUTE,?,UTC_TIMESTAMP(),?,UTC_TIMESTAMP()-INTERVAL 1 MINUTE,'lease')`, time.Now().UnixMicro()+int64(i), user, dev, "test", bind, uniqueDevID(), status)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if err = s.MaintainPush(ctx); err != nil {
			t.Fatal(err)
		}
		if err = s.FinishPush(ctx, push.Delivery{ID: id, Lease: "lease"}, push.Accepted); err != nil {
			t.Fatal(err)
		}
		want := "expired"
		if status == "sending" {
			want = "unknown"
		}
		var got string
		if err = db.Get(&got, "SELECT status FROM push_delivery WHERE id=?", id); err != nil || got != want {
			t.Fatal(got, err)
		}
	}
}
