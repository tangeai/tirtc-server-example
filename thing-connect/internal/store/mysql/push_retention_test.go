package mysql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"thing-connect/internal/push"
	r "thing-connect/internal/recognition"
	m "thing-connect/internal/store/mysql"
	"thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

// Uses the historical push_event fixture from TestFirstTierCapacity. All
// generated source history belongs to one synthetic device.
func TestRetentionWithRecognitionCompletion(t *testing.T) {
	if os.Getenv("PUSH_RETENTION_TEST") != "1" {
		t.Skip("opt-in 3M + 3M history test")
	}
	cfg := testenv.LoadConfigOrSkip(t, "../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	defer db.Close()
	var name string
	if err := db.Get(&name, "SELECT DATABASE()"); err != nil || !strings.HasSuffix(name, "_capacity_test") {
		t.Fatal("dedicated capacity database required")
	}
	if err := migrate.RequireSchemaCurrent(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	var existing int
	if err := db.Get(&existing, "SELECT COUNT(*) FROM recognition_user_event_outbox WHERE device_id='retbench'"); err != nil {
		t.Fatal(err)
	}
	for batch := 0; existing < 3000000 && batch < 30; batch++ {
		exec(`INSERT IGNORE INTO recognition_user_event_outbox(device_id,bind_id,source_event_id,event_type,occurred_at,registered_at,next_at,status,expires_at)
 SELECT 'retbench','r',CONCAT(?,'-',n,':alarm'),'alarm',UTC_TIMESTAMP(6),TIMESTAMPADD(HOUR,-IF(MOD(n,100)=0,74,MOD(n,70)),UTC_TIMESTAMP(6)),UTC_TIMESTAMP(6),'accepted',UTC_TIMESTAMP(6) FROM capacity_seq`, batch)
	}
	for _, table := range []string{"recognition_user_event_outbox", "push_event"} {
		var n int
		if err := db.Get(&n, "SELECT COUNT(*) FROM "+table); err != nil || n < 3000000 {
			t.Fatal(table, n, err)
		}
		t.Log(table, n)
	}
	ctx := context.Background()
	s := m.NewRecognitionStore(db)
	if err := s.ConfigureSource(ctx, push.DefaultDeliveryPolicy(), 0, true); err != nil {
		t.Fatal(err)
	}
	dev, _ := seedPool(t, db)
	user := seedUser(t, db)
	exec("INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,?,NOW())", dev, user)
	scheme := r.Scheme{ID: dev, Version: 1, Revision: 1, Name: "test", MediaType: "audio", Model: "qwen3-omni-flash", Prompt: "test", OutputMode: "text", Enabled: true, Limits: r.Limits{MaxFileBytes: 5 << 20, MaxAudioSeconds: 15}}
	raw, _ := json.Marshal(scheme)
	exec("INSERT INTO recognition_scheme(id,revision,version,enabled,draft) VALUES(?,1,1,TRUE,?)", dev, raw)
	exec("INSERT INTO recognition_scheme_version(scheme_id,version,snapshot) VALUES(?,1,?)", dev, raw)
	defer func() {
		for _, q := range []string{"DELETE c FROM recognition_call c JOIN recognition_task t ON t.id=c.task_id WHERE t.device_id=?", "DELETE FROM recognition_task WHERE device_id=?", "DELETE FROM recognition_outbox WHERE device_id=?", "DELETE FROM recognition_user_event_outbox WHERE device_id=?", "DELETE FROM recognition_device WHERE device_id=?", "DELETE FROM recognition_scheme_version WHERE scheme_id=?", "DELETE FROM recognition_scheme WHERE id=?", "DELETE FROM device_bind WHERE device_id=?"} {
			exec(q, dev)
		}
	}()
	state, err := s.SetSelection(ctx, r.Actor{UserID: user}, dev, true, []r.Binding{{ID: dev, Version: 1}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	exec("UPDATE recognition_device SET policy=JSON_SET(policy,'$.monthly_limit',1000) WHERE device_id=?", dev)
	audio := recognitionTestAudio()
	for phase := 0; phase < 4; phase++ {
		stop := make(chan struct{})
		cleaned := make(chan error, 1)
		if phase%2 == 1 {
			go func() {
				for {
					if err := s.CleanUserEvents(ctx); err != nil {
						cleaned <- err
						return
					}
					select {
					case <-stop:
						cleaned <- nil
						return
					case <-time.After(100 * time.Millisecond):
					}
				}
			}()
		}
		var lat []time.Duration
		func() {
			if phase%2 == 1 {
				defer func() {
					close(stop)
					if err := <-cleaned; err != nil {
						t.Fatal(err)
					}
				}()
			}
			for i := 0; i < 100; i++ {
				req := r.Request{ID: fmt.Sprintf("bench%d-%d", phase, i), Revision: state.Policy.Revision, MediaType: "audio", Data: audio}
				if _, _, err := s.Submit(ctx, r.Actor{DeviceID: dev}, dev, req, "hash"); err != nil {
					t.Fatal(err)
				}
				w, err := s.Claim(ctx)
				if err != nil {
					t.Fatal(err)
				}
				start := time.Now()
				if err = s.Complete(ctx, r.Completion{Work: *w, Result: &r.Result{Summary: "alarm", Alarm: true, Details: map[string]any{}}}); err != nil {
					t.Fatal(err)
				}
				lat = append(lat, time.Since(start))
			}
		}()
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		t.Logf("phase=%d cleanup=%v samples=%d p50=%s p95=%s p99=%s", phase, phase%2 == 1, len(lat), lat[49], lat[94], lat[98])
	}
	var generated int
	if err := db.Get(&generated, "SELECT COUNT(*) FROM recognition_user_event_outbox WHERE device_id=?", dev); err != nil || generated != 400 {
		t.Fatal("registered", generated, err)
	}
}
