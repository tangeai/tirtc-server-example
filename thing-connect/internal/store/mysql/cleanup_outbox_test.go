package mysql_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/cleanup"
	"thing-connect/internal/store"
	mysqlstore "thing-connect/internal/store/mysql"
)

var voipTarget = []string{"voip"}

func openCleanupOutbox(t *testing.T) (*sqlx.DB, cleanup.Queue) {
	t.Helper()
	db := openTestDB(t)
	if _, err := db.Exec(`DELETE FROM cleanup_outbox`); err != nil {
		t.Fatalf("reset cleanup_outbox: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM cleanup_outbox`) })
	return db, mysqlstore.NewCleanupOutboxStore(db)
}

func seedCleanupTasks(t *testing.T, db *sqlx.DB, n int) {
	t.Helper()
	prefix := fmt.Sprintf("cleanup_%d", time.Now().UnixNano())
	for i := 0; i < n; i++ {
		if _, err := db.Exec(`INSERT INTO cleanup_outbox(device_id,target) VALUES(?,?)`, fmt.Sprintf("%s_%d", prefix, i), "voip"); err != nil {
			t.Fatalf("seed cleanup task: %v", err)
		}
	}
}

func TestCleanupOutboxConcurrentClaimsDoNotOverlap(t *testing.T) {
	db, queue := openCleanupOutbox(t)
	seedCleanupTasks(t, db, 30)

	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				tasks, err := queue.Claim(context.Background(), voipTarget, 5, time.Minute)
				if err != nil {
					t.Errorf("Claim: %v", err)
					return
				}
				if len(tasks) == 0 {
					return
				}
				mu.Lock()
				for _, task := range tasks {
					seen[task.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 30 {
		t.Fatalf("claimed %d distinct tasks, want 30", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("task %d claimed %d times", id, count)
		}
	}
}

// unbindAgain goes through the shared User/Admin transaction on a device
// that was rebound while its first cleanup was pending.
func unbindAgain(t *testing.T, db *sqlx.DB, device, target string) {
	t.Helper()
	user := seedUser(t, db)
	if _, err := db.Exec(`INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,?,NOW())`, device, user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanBind(t, db, device) })
	bindings := mysqlstore.NewBindStore(db).(store.UnbindCleanupStore)
	if err := bindings.CommitUnbindWithCleanup(context.Background(), device, user, []string{target}); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupOutboxCompleteKeepsTaskReenqueuedDuringDelivery(t *testing.T) {
	db, queue := openCleanupOutbox(t)
	seedCleanupTasks(t, db, 1)
	tasks, err := queue.Claim(context.Background(), voipTarget, 10, time.Minute)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("Claim: tasks=%v err=%v", tasks, err)
	}
	if again, err := queue.Claim(context.Background(), voipTarget, 10, time.Minute); err != nil || len(again) != 0 {
		t.Fatalf("leased task claimed again: %v err=%v", again, err)
	}

	unbindAgain(t, db, tasks[0].DeviceID, tasks[0].Target)
	redo, err := queue.Claim(context.Background(), voipTarget, 10, time.Minute)
	if err != nil || len(redo) != 1 || redo[0].ID == tasks[0].ID {
		t.Fatalf("new unbind generation did not receive a distinct ID: old=%v new=%v err=%v", tasks, redo, err)
	}
	// Exercise the exact timestamp collision even if the test crossed a
	// wall-clock second between claims.
	tasks[0].Lease = redo[0].Lease
	applied, err := queue.Complete(context.Background(), tasks[0])
	if err != nil || applied {
		t.Fatalf("stale Complete applied=%v err=%v, want not applied", applied, err)
	}
	applied, err = queue.Retry(context.Background(), tasks[0], time.Hour, "late failure")
	if err != nil || applied {
		t.Fatalf("stale Retry applied=%v err=%v, want not applied", applied, err)
	}

	applied, err = queue.Complete(context.Background(), redo[0])
	if err != nil || !applied {
		t.Fatalf("current Complete applied=%v err=%v", applied, err)
	}
	var remaining int
	if err := db.Get(&remaining, `SELECT COUNT(*) FROM cleanup_outbox`); err != nil || remaining != 0 {
		t.Fatalf("remaining=%d err=%v, want 0", remaining, err)
	}
}

func TestCleanupOutboxRetrySchedulesBackoff(t *testing.T) {
	db, queue := openCleanupOutbox(t)
	seedCleanupTasks(t, db, 1)
	tasks, err := queue.Claim(context.Background(), voipTarget, 10, time.Minute)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("Claim: %v err=%v", tasks, err)
	}
	applied, err := queue.Retry(context.Background(), tasks[0], time.Hour, "voip returned HTTP 503")
	if err != nil || !applied {
		t.Fatalf("Retry applied=%v err=%v", applied, err)
	}
	var row struct {
		Attempts  int    `db:"attempts"`
		LastError string `db:"last_error"`
		Delay     int    `db:"delay"`
	}
	if err := db.Get(&row, `SELECT attempts,last_error,TIMESTAMPDIFF(SECOND,NOW(),next_attempt_at) AS delay FROM cleanup_outbox WHERE id=?`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if row.Attempts != 1 || row.LastError != "voip returned HTTP 503" || row.Delay < 3500 {
		t.Fatalf("row=%+v, want one attempt and about an hour of backoff", row)
	}
}

func TestCleanupOutboxExpiredLeaseIsTakenOver(t *testing.T) {
	db, queue := openCleanupOutbox(t)
	seedCleanupTasks(t, db, 1)
	first, err := queue.Claim(context.Background(), voipTarget, 10, time.Second)
	if err != nil || len(first) != 1 {
		t.Fatalf("Claim: %v err=%v", first, err)
	}
	time.Sleep(2100 * time.Millisecond)
	second, err := queue.Claim(context.Background(), voipTarget, 10, time.Minute)
	if err != nil || len(second) != 1 || second[0].ID != first[0].ID || second[0].Lease == first[0].Lease {
		t.Fatalf("takeover claim=%v err=%v", second, err)
	}
	if applied, err := queue.Complete(context.Background(), first[0]); err != nil || applied {
		t.Fatalf("expired claimer Complete applied=%v err=%v, want not applied", applied, err)
	}
	if applied, err := queue.Complete(context.Background(), second[0]); err != nil || !applied {
		t.Fatalf("current claimer Complete applied=%v err=%v", applied, err)
	}
}

func TestCleanupOutboxBacklogKeepsFirstEnqueueAge(t *testing.T) {
	db, queue := openCleanupOutbox(t)
	backlog, err := queue.Backlog(context.Background(), voipTarget)
	if err != nil || backlog.Pending != 0 || backlog.OldestAge != 0 {
		t.Fatalf("empty backlog=%+v err=%v", backlog, err)
	}
	seedCleanupTasks(t, db, 2)
	var deviceID string
	if err := db.Get(&deviceID, `SELECT device_id FROM cleanup_outbox ORDER BY id LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE cleanup_outbox SET created_at=DATE_SUB(NOW(), INTERVAL 20 MINUTE) WHERE device_id=?`, deviceID); err != nil {
		t.Fatal(err)
	}
	unbindAgain(t, db, deviceID, "voip")
	backlog, err = queue.Backlog(context.Background(), voipTarget)
	if err != nil {
		t.Fatal(err)
	}
	if backlog.Pending != 2 || backlog.OldestAge < 19*time.Minute || backlog.OldestAge > 21*time.Minute {
		t.Fatalf("backlog=%+v, want 2 pending and about 20 minutes old", backlog)
	}
}

func TestCleanupOutboxScopesClaimAndBacklogToConfiguredTargets(t *testing.T) {
	db, queue := openCleanupOutbox(t)
	seedCleanupTasks(t, db, 2)
	device := fmt.Sprintf("cleanup_removed_%d", time.Now().UnixNano())
	if _, err := db.Exec(`INSERT INTO cleanup_outbox(device_id,target,created_at) VALUES(?,?,DATE_SUB(NOW(), INTERVAL 20 MINUTE))`, device, "removed"); err != nil {
		t.Fatal(err)
	}
	backlog, err := queue.Backlog(context.Background(), voipTarget)
	if err != nil || backlog.Pending != 2 || backlog.Unknown != 1 || backlog.OldestAge > time.Minute {
		t.Fatalf("backlog=%+v err=%v, want 2 recent configured tasks and 1 unknown", backlog, err)
	}
	tasks, err := queue.Claim(context.Background(), voipTarget, 10, time.Minute)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("Claim: %v err=%v", tasks, err)
	}
	for _, task := range tasks {
		if task.Target != "voip" {
			t.Fatalf("claimed task for unconfigured target: %+v", task)
		}
	}
	if none, err := queue.Claim(context.Background(), nil, 10, time.Minute); err != nil || len(none) != 0 {
		t.Fatalf("Claim without targets: %v err=%v", none, err)
	}
	backlog, err = queue.Backlog(context.Background(), nil)
	if err != nil || backlog.Pending != 0 || backlog.Unknown != 3 || backlog.OldestAge != 0 {
		t.Fatalf("backlog without targets=%+v err=%v", backlog, err)
	}
	restored, err := queue.Backlog(context.Background(), []string{"removed", "voip"})
	if err != nil || restored.Pending != 3 || restored.Unknown != 0 || restored.OldestAge < 19*time.Minute {
		t.Fatalf("backlog after reconfiguring=%+v err=%v", restored, err)
	}
}
