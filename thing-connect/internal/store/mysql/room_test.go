package mysql_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"thing-connect/internal/room"
	mysqlstore "thing-connect/internal/store/mysql"
)

type roomTokenStub struct{ before func() }

func (s roomTokenStub) Issue(context.Context, string, string) (room.Credential, error) {
	if s.before != nil {
		s.before()
	}
	return room.Credential{PeerID: "peer", Token: "test-token"}, nil
}

type roomSignalsStub struct{}

func (roomSignalsStub) Allow(context.Context, room.Operation) (bool, error)     { return true, nil }
func (roomSignalsStub) Online(context.Context, string) bool                     { return true }
func (roomSignalsStub) Notify(context.Context, room.Event, time.Duration) error { return nil }

func TestIntercomTransactions(t *testing.T) {
	db := openTestDB(t)
	db.SetMaxOpenConns(12)
	ctx := context.Background()
	for _, table := range []string{"call_outbox", "call_requests", "call_leases", "call_assignments", "call_room_codes", "call_rooms"} {
		if _, e := db.Exec("DELETE FROM " + table); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 102; i++ {
		if _, e := db.Exec("INSERT INTO device_bind(device_id,user_id) VALUES(?,1)", fmt.Sprintf("intercom-%d", i)); e != nil {
			t.Fatal(e)
		}
	}
	repo := mysqlstore.NewRoomStore(db)
	service, e := room.New(repo, roomTokenStub{}, roomSignalsStub{}, []byte(strings.Repeat("p", 32)), room.DefaultPolicy())
	if e != nil {
		t.Fatal(e)
	}
	op := room.Operation{DeviceID: "intercom-0", UserID: 1, Key: "create-request-1", Kind: "create", Password: "0573"}
	a, e := service.Change(ctx, op)
	if e != nil {
		t.Fatal(e)
	}
	if !room.Digits(a.RoomCode, 6) || !strings.HasPrefix(a.RoomID, "group_room_") {
		t.Fatalf("code=%q", a.RoomCode)
	}
	replay, e := service.Change(ctx, op)
	if e != nil || replay.RoomID != a.RoomID || replay.Version != a.Version {
		t.Fatalf("duplicate create: %+v %v", replay, e)
	}
	op.Password = "1234"
	if _, e = service.Change(ctx, op); !errors.Is(e, room.ErrConflict) {
		t.Fatalf("idempotency conflict=%v", e)
	}
	if _, e = service.Current(ctx, "intercom-0", 2); !errors.Is(e, room.ErrForbidden) {
		t.Fatalf("ownership=%v", e)
	}
	for i := 0; i < 5; i++ {
		_, e = service.Change(ctx, room.Operation{DeviceID: "intercom-101", UserID: 1, Key: fmt.Sprintf("password-wrong-%d", i), Kind: "join", Code: a.RoomCode, Password: "9999"})
		want := room.ErrPassword
		if i == 4 {
			want = room.ErrLocked
		}
		if !errors.Is(e, want) {
			t.Fatalf("failure %d=%v", i, e)
		}
	}
	if _, e = service.Change(ctx, room.Operation{DeviceID: "intercom-101", UserID: 1, Key: "locked-join", Kind: "join", Code: a.RoomCode, Password: "0573"}); !errors.Is(e, room.ErrLocked) {
		t.Fatalf("locked correct password=%v", e)
	}
	if _, e = service.Connect(ctx, "intercom-0", room.Presence{RoomID: a.RoomID, Version: 1, SessionID: "session-0"}); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 1; i < 101; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			device := fmt.Sprintf("intercom-%d", i)
			if i != 0 {
				_, e := service.Change(ctx, room.Operation{DeviceID: device, UserID: 1, Key: fmt.Sprintf("join-request-%d", i), Kind: "join", Code: a.RoomCode, Password: "0573"})
				if e != nil {
					errs <- e
					return
				}
			}
			_, e := service.Connect(ctx, device, room.Presence{RoomID: a.RoomID, Version: 1, SessionID: fmt.Sprintf("session-%d", i)})
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	success, full := 1, 0
	for e := range errs {
		if e == nil {
			success++
		} else if errors.Is(e, room.ErrFull) {
			full++
		} else {
			t.Fatal(e)
		}
	}
	if success != 100 || full != 1 {
		t.Fatalf("capacity: success=%d full=%d", success, full)
	}
	p := room.Presence{RoomID: a.RoomID, Version: 1, SessionID: "session-0", State: "joined"}
	if e = service.Report(ctx, "intercom-0", p); e != nil {
		t.Fatal(e)
	}
	p.SessionID = "late-session"
	if e = service.Report(ctx, "intercom-0", p); !errors.Is(e, room.ErrStale) {
		t.Fatalf("late callback=%v", e)
	}
	// Lease expiry, not worker restart time, establishes the empty deadline.
	if _, e = db.Exec("UPDATE call_leases SET state='ended',expires_at=DATE_SUB(NOW(6), INTERVAL 25 HOUR) WHERE room_id=?", a.RoomID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("UPDATE call_rooms SET created_at=DATE_SUB(NOW(6), INTERVAL 26 HOUR),empty_deadline=NULL WHERE room_id=?", a.RoomID); e != nil {
		t.Fatal(e)
	}
	restarted, e := room.New(repo, roomTokenStub{}, roomSignalsStub{}, []byte(strings.Repeat("p", 32)), room.DefaultPolicy())
	if e != nil {
		t.Fatal(e)
	}
	if e = restarted.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	status, e := restarted.Current(ctx, "intercom-0", 1)
	if e != nil || status.Desired != "left" || status.State != "room_closed" {
		t.Fatalf("closed=%+v %v", status, e)
	}
	var cooldown int
	if e = db.Get(&cooldown, "SELECT COUNT(*) FROM call_room_codes WHERE room_id=? AND reusable_at>NOW()", a.RoomID); e != nil || cooldown != 1 {
		t.Fatalf("cooldown=%d %v", cooldown, e)
	}
}

func TestIntercomLeaveDuringTokenRequest(t *testing.T) {
	db := openTestDB(t)
	db.SetMaxOpenConns(12)
	ctx := context.Background()
	device := uniqueDevID()
	if _, e := db.Exec("INSERT INTO device_bind(device_id,user_id) VALUES(?,9)", device); e != nil {
		t.Fatal(e)
	}
	repo := mysqlstore.NewRoomStore(db)
	var svc *room.Service
	var a room.Assignment
	issuer := roomTokenStub{before: func() {
		_, e := svc.Change(ctx, room.Operation{DeviceID: device, UserID: 9, Kind: "leave", Key: "leave-during-token", RoomID: a.RoomID, Version: a.Version})
		if e != nil {
			t.Error(e)
		}
	}}
	var e error
	svc, e = room.New(repo, issuer, roomSignalsStub{}, []byte(strings.Repeat("s", 32)), room.DefaultPolicy())
	if e != nil {
		t.Fatal(e)
	}
	a, e = svc.Change(ctx, room.Operation{DeviceID: device, UserID: 9, Kind: "create", Key: "create-token-race"})
	if e != nil {
		t.Fatal(e)
	}
	credentials, e := svc.Connect(ctx, device, room.Presence{RoomID: a.RoomID, Version: a.Version, SessionID: "connect-race-session"})
	if !errors.Is(e, room.ErrStale) || credentials.Token != "" {
		t.Fatalf("credentials escaped leave: %+v %v", credentials, e)
	}
}

func TestIntercomSessionFencingAndUnbind(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	device := uniqueDevID()
	if _, err := db.Exec("INSERT INTO device_bind(device_id,user_id) VALUES(?,1)", device); err != nil {
		t.Fatal(err)
	}
	svc, err := room.New(mysqlstore.NewRoomStore(db), roomTokenStub{}, roomSignalsStub{}, []byte(strings.Repeat("p", 32)), room.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	a, err := svc.Change(ctx, room.Operation{DeviceID: device, UserID: 1, Kind: "create", Key: "create-room-request"})
	if err != nil {
		t.Fatal(err)
	}
	p := room.Presence{RoomID: a.RoomID, Version: a.Version, SessionID: "first-session", State: "joined"}
	if _, err = svc.Connect(ctx, device, p); err != nil {
		t.Fatal(err)
	}
	if err = svc.Report(ctx, device, p); err != nil {
		t.Fatal(err)
	}
	p.State = "suspended"
	for i := 0; i < 2; i++ {
		if err = svc.Report(ctx, device, p); err != nil {
			t.Fatal(err)
		}
	}
	current, err := svc.Current(ctx, device, 1)
	if err != nil || current.Desired != "joined" || current.State != "suspended" || current.OnlineCount != 0 {
		t.Fatalf("suspension=%+v %v", current, err)
	}
	next := p
	next.SessionID = "second-session"
	next.State = "joined"
	if _, err = svc.Connect(ctx, device, next); err != nil {
		t.Fatal(err)
	}
	if err = svc.Report(ctx, device, p); !errors.Is(err, room.ErrStale) {
		t.Fatalf("stale suspension=%v", err)
	}
	if err = svc.Report(ctx, device, next); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DELETE FROM device_bind WHERE device_id=?", device); err != nil {
		t.Fatal(err)
	}
	if err = svc.Unbind(ctx, device); err != nil {
		t.Fatal(err)
	}
	if err = svc.Unbind(ctx, device); err != nil {
		t.Fatal(err)
	}
	var desired string
	var version int64
	if err = db.QueryRow("SELECT desired_state,assignment_version FROM call_assignments WHERE device_id=?", device).Scan(&desired, &version); err != nil {
		t.Fatal(err)
	}
	if desired != "left" || version != a.Version+1 {
		t.Fatalf("unbind desired=%s version=%d", desired, version)
	}
	if err = svc.Report(ctx, device, next); !errors.Is(err, room.ErrForbidden) {
		t.Fatalf("unbound report=%v", err)
	}
	for i := 0; i < 10; i++ {
		if err = svc.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var pending int
	if err = db.Get(&pending, "SELECT COUNT(*) FROM call_outbox WHERE device_id=?", device); err != nil || pending != 0 {
		t.Fatalf("outbox=%d %v", pending, err)
	}
}
