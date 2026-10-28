package mysql_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	r "thing-connect/internal/recognition"
	m "thing-connect/internal/store/mysql"
	"time"
)

func TestRecognitionDurableOrderQuotaAndOwnership(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id, _ := seedPool(t, db)
	uid := seedUser(t, db)
	if _, e := db.Exec(`INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,?,NOW())`, id, uid); e != nil {
		t.Fatal(e)
	}
	defer cleanBind(t, db, id)
	store := m.NewRecognitionStore(db)
	actor := r.Actor{UserID: uid}
	state, e := store.State(ctx, actor, id)
	if e != nil || state.Policy.Enabled {
		t.Fatal("default", e)
	}
	ids := []string{fmt.Sprintf("rec_%d_a", time.Now().UnixNano()), fmt.Sprintf("rec_%d_b", time.Now().UnixNano())}
	for i, typ := range ids {
		mode := "text"
		if i == 1 {
			mode = "audio"
		}
		s := r.Scheme{ID: typ, Version: 1, Revision: 1, Name: "声音分析", MediaType: "audio", Model: "qwen3-omni-flash", Prompt: "分析声音", OutputMode: mode, Voice: "Cherry", Enabled: true, Limits: r.Limits{MaxFileBytes: 5 << 20, MaxAudioSeconds: 15}}
		raw, _ := json.Marshal(s)
		if _, e = db.Exec(`INSERT INTO recognition_scheme(id,revision,version,enabled,draft) VALUES(?,1,1,TRUE,?)`, typ, string(raw)); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO recognition_scheme_version(scheme_id,version,snapshot) VALUES(?,1,?)`, typ, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	t.Cleanup(func() {
		db.Exec(`DELETE a FROM recognition_audio a JOIN recognition_task t ON t.id=a.task_id WHERE t.device_id=?`, id)
		db.Exec(`DELETE c FROM recognition_call c JOIN recognition_task t ON t.id=c.task_id WHERE t.device_id=?`, id)
		db.Exec(`DELETE FROM recognition_task WHERE device_id=?`, id)
		db.Exec(`DELETE FROM recognition_outbox WHERE device_id=?`, id)
		db.Exec(`DELETE FROM recognition_device WHERE device_id=?`, id)
		for _, typ := range ids {
			db.Exec(`DELETE FROM recognition_scheme_version WHERE scheme_id=?`, typ)
			db.Exec(`DELETE FROM recognition_scheme WHERE id=?`, typ)
		}
	})
	state, e = store.SetSelection(ctx, actor, id, true, []r.Binding{{ID: ids[0], Version: 1}, {ID: ids[1], Version: 1}}, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE recognition_device SET policy=JSON_SET(policy,'$.monthly_limit',2) WHERE device_id=?`, id); e != nil {
		t.Fatal(e)
	}
	audio := make([]byte, 16044)
	copy(audio, "RIFF")
	binary.LittleEndian.PutUint32(audio[4:], 16036)
	copy(audio[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(audio[16:], 16)
	binary.LittleEndian.PutUint16(audio[20:], 1)
	binary.LittleEndian.PutUint16(audio[22:], 1)
	binary.LittleEndian.PutUint32(audio[24:], 8000)
	binary.LittleEndian.PutUint32(audio[28:], 16000)
	binary.LittleEndian.PutUint16(audio[32:], 2)
	binary.LittleEndian.PutUint16(audio[34:], 16)
	copy(audio[36:], "data")
	binary.LittleEndian.PutUint32(audio[40:], 16000)
	req := r.Request{ID: "same-request", Revision: state.Policy.Revision, MediaType: "audio", Data: audio}
	t.Run("concurrent retries create and charge once", func(t *testing.T) {
		var wg sync.WaitGroup
		type submission struct {
			fresh bool
			err   error
		}
		results := make(chan submission, 4)
		start := make(chan struct{})
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, fresh, err := store.Submit(ctx, r.Actor{DeviceID: id}, id, req, "hash")
				results <- submission{fresh, err}
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		created := 0
		for result := range results {
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.fresh {
				created++
			}
		}
		if created != 1 {
			t.Fatalf("created %d tasks for one request", created)
		}
		var units, count int
		if err := db.Get(&units, `SELECT COALESCE(SUM(units),0) FROM recognition_task WHERE device_id=?`, id); err != nil {
			t.Fatal(err)
		}
		if err := db.Get(&count, `SELECT COUNT(*) FROM recognition_task WHERE device_id=?`, id); err != nil {
			t.Fatal(err)
		}
		if count != 1 || units != 2 {
			t.Fatalf("count=%d units=%d, want one task with two units", count, units)
		}
	})
	if t.Failed() {
		return
	}
	req.ID = "over-quota"
	if _, _, e = store.Submit(ctx, r.Actor{DeviceID: id}, id, req, "hash"); !errors.Is(e, r.ErrQuota) {
		t.Fatal("quota", e)
	}
	var w *r.Work
	t.Run("concurrent workers claim a task once", func(t *testing.T) {
		type claim struct {
			work *r.Work
			err  error
		}
		claims := make(chan claim, 4)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				work, err := store.Claim(ctx)
				claims <- claim{work, err}
			}()
		}
		close(start)
		wg.Wait()
		close(claims)
		claimed := 0
		for result := range claims {
			if errors.Is(result.err, r.ErrNotFound) {
				continue
			}
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.work == nil || result.work.Scheme.ID != ids[0] {
				t.Fatalf("unexpected claim: %+v", result.work)
			}
			claimed++
			w = result.work
		}
		if claimed != 1 {
			t.Fatalf("claimed %d times, want one", claimed)
		}
		var count int
		if err := db.Get(&count, `SELECT COUNT(*) FROM recognition_call WHERE task_id=?`, w.TaskKey); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("claimed task recorded %d upstream calls", count)
		}
	})
	if t.Failed() {
		return
	}
	result := &r.Result{Summary: "疑似哭声", Alarm: true, Labels: []string{"crying"}, Details: map[string]any{}}
	if e = store.Complete(ctx, r.Completion{Work: *w, Result: result}); e != nil {
		t.Fatal(e)
	}
	w, e = store.Claim(ctx)
	if e != nil || w == nil || w.Scheme.ID != ids[1] || w.Stage != "analysis" {
		t.Fatal("order", e)
	}
	if e = store.Complete(ctx, r.Completion{Work: *w, Result: result}); e != nil {
		t.Fatal(e)
	}
	task, e := store.Get(ctx, r.Actor{DeviceID: id}, id, "same-request")
	if e != nil || task.Results[1].Result != nil || task.Results[0].Result == nil {
		t.Fatal("output policy", e)
	}
	w, e = store.Claim(ctx)
	if e != nil || w == nil || w.Stage != "tts" {
		t.Fatal("tts", e)
	}
	if _, e = db.Exec(`UPDATE recognition_task SET lease_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE id=?`, w.TaskKey); e != nil {
		t.Fatal(e)
	}
	if e = store.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	task, e = store.Get(ctx, r.Actor{DeviceID: id}, id, "same-request")
	if e != nil || task.Status != "unknown" {
		t.Fatal("lease recovery", e)
	}
	t.Run("late completion cannot replace recovered lease", func(t *testing.T) {
		before := task
		if err := store.Complete(ctx, r.Completion{Work: *w, Audio: audio}); !errors.Is(err, r.ErrConflict) {
			t.Fatalf("late completion error=%v, want conflict", err)
		}
		after, err := store.Get(ctx, r.Actor{DeviceID: id}, id, "same-request")
		if err != nil {
			t.Fatal(err)
		}
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(after)
		if string(beforeJSON) != string(afterJSON) {
			t.Fatalf("late completion changed recovered task: %s", afterJSON)
		}
		var audioCount int
		if err = db.Get(&audioCount, `SELECT COUNT(*) FROM recognition_audio WHERE task_id=?`, w.TaskKey); err != nil {
			t.Fatal(err)
		}
		if audioCount != 0 {
			t.Fatal("late completion persisted audio")
		}
	})
	if _, e = db.Exec(`UPDATE device_bind SET binding_revision=binding_revision+1 WHERE device_id=?`, id); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Get(ctx, r.Actor{DeviceID: id}, id, "same-request"); !errors.Is(e, r.ErrNotFound) {
		t.Fatal("old ownership leaked", e)
	}
	state, e = store.State(ctx, actor, id)
	if e != nil || state.Policy.Enabled {
		t.Fatal("new binding not reset", e)
	}
	t.Run("different requests compete for final quota atomically", func(t *testing.T) {
		state, err := store.SetSelection(ctx, actor, id, true, []r.Binding{{ID: ids[0], Version: 1}, {ID: ids[1], Version: 1}}, state.Policy.Revision)
		if err != nil {
			t.Fatal(err)
		}
		// The earlier task used two units; exactly one two-type task remains.
		if _, err = db.Exec(`UPDATE recognition_device SET policy=JSON_SET(policy,'$.monthly_limit',4) WHERE device_id=?`, id); err != nil {
			t.Fatal(err)
		}
		type submission struct {
			fresh bool
			err   error
		}
		results := make(chan submission, 4)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			request := r.Request{ID: fmt.Sprintf("last-quota-%d", i), Revision: state.Policy.Revision, MediaType: "audio", Data: audio}
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, fresh, err := store.Submit(ctx, r.Actor{DeviceID: id}, id, request, "hash")
				results <- submission{fresh, err}
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		created, rejected := 0, 0
		for result := range results {
			if errors.Is(result.err, r.ErrQuota) {
				rejected++
				continue
			}
			if result.err != nil {
				t.Fatal(result.err)
			}
			if !result.fresh {
				t.Fatal("distinct request treated as retry")
			}
			created++
		}
		if created != 1 || rejected != 3 {
			t.Fatalf("created=%d quota rejected=%d", created, rejected)
		}
		var units int
		if err = db.Get(&units, `SELECT COALESCE(SUM(units),0) FROM recognition_task WHERE device_id=?`, id); err != nil {
			t.Fatal(err)
		}
		if units != 4 {
			t.Fatalf("quota consumed=%d, want4", units)
		}
	})
}

func TestRecognitionOutboxUsesUTCAndDrainsLegacyFirstAttempts(t *testing.T) {
	db := openTestDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`SET time_zone='+08:00'`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, _ := seedPool(t, db)
	uid := seedUser(t, db)
	if _, err := db.Exec(`INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,?,NOW())`, id, uid); err != nil {
		t.Fatal(err)
	}
	defer cleanBind(t, db, id)
	defer db.Exec(`DELETE FROM recognition_device WHERE device_id=?`, id)
	defer db.Exec(`DELETE FROM recognition_outbox WHERE device_id=?`, id)
	store := m.NewRecognitionStore(db)
	if _, err := store.SetSelection(ctx, r.Actor{UserID: uid}, id, false, nil, 0); err != nil {
		t.Fatal(err)
	}
	var delta int
	if err := db.Get(&delta, `SELECT ABS(TIMESTAMPDIFF(SECOND,next_at,UTC_TIMESTAMP(6))) FROM recognition_outbox WHERE device_id=? LIMIT 1`, id); err != nil {
		t.Fatal(err)
	}
	if delta > 5 {
		t.Fatalf("new event uses non-UTC clock: delta=%d", delta)
	}
	// Reproduce old rows inserted with CURRENT_TIMESTAMP in a UTC+8 session.
	if _, err := db.Exec(`UPDATE recognition_outbox SET next_at=NOW(6) WHERE device_id=?`, id); err != nil {
		t.Fatal(err)
	}
	events, err := store.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var event r.Event
	for _, v := range events {
		if v.DeviceID == id {
			event = v
		}
	}
	if event.ID == 0 {
		t.Fatal("legacy first attempt delayed by eight hours")
	}
	if err = store.Deliver(ctx, event, false); err != nil {
		t.Fatal(err)
	}
	events, err = store.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range events {
		if v.ID == event.ID {
			t.Fatal("retry backoff ignored")
		}
	}
	if _, err = db.Exec(`UPDATE recognition_outbox SET next_at=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE id=?`, event.ID); err != nil {
		t.Fatal(err)
	}
	events, err = store.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range events {
		if v.ID == event.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("due retry not returned")
	}
	if err = store.Deliver(ctx, event, true); err != nil {
		t.Fatal(err)
	}
	// A second publisher can report failure after the first publisher succeeds.
	if err = store.Deliver(ctx, event, false); err != nil {
		t.Fatal(err)
	}
	var delivered bool
	if err = db.Get(&delivered, `SELECT delivered FROM recognition_outbox WHERE id=?`, event.ID); err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("late failed delivery overwrote an acknowledged event")
	}
	events, err = store.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range events {
		if v.ID == event.ID {
			t.Fatal("delivered event returned again")
		}
	}
}
