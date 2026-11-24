package mysql_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"thing-connect/internal/ai"
	mysqlstore "thing-connect/internal/store/mysql"
)

func TestAICloudTargetsClaimOnceAndParkWhenSettled(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	stamp := time.Now().UnixNano() % 1_000_000_000
	user := stamp + 2_000_000_000
	var devices []string
	for i := 0; i < 12; i++ {
		device := fmt.Sprintf("C%09d%02d", stamp, i)
		devices = append(devices, device)
		if _, err := db.Exec(`INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,?,NOW())`, device, user); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO ai_cloud_role_target(device_id,user_id,binding_revision,remove_role,next_attempt_at) VALUES(?,?,0,1,DATE_SUB(NOW(),INTERVAL 1 SECOND))`, device, user); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, device := range devices {
			_, _ = db.Exec(`DELETE FROM ai_cloud_role_ref WHERE device_id=?`, device)
			_, _ = db.Exec(`DELETE FROM ai_cloud_role_target WHERE device_id=?`, device)
			_, _ = db.Exec(`DELETE FROM device_bind WHERE device_id=?`, device)
		}
	})
	store := mysqlstore.NewRoleLifecycleStore(db)
	ours := map[string]bool{}
	for _, device := range devices {
		ours[device] = true
	}

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				ids, err := store.ClaimCloud(ctx, 3, time.Minute)
				if err != nil {
					t.Errorf("ClaimCloud: %v", err)
					return
				}
				if len(ids) == 0 {
					return
				}
				mu.Lock()
				for _, id := range ids {
					seen[id]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	for _, device := range devices {
		if seen[device] != 1 {
			t.Fatalf("%s claimed %d times, want once", device, seen[device])
		}
	}

	var revision int64
	if err := db.Get(&revision, `SELECT binding_revision FROM device_bind WHERE device_id=?`, devices[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE ai_cloud_role_target SET binding_revision=?,next_attempt_at=DATE_SUB(NOW(),INTERVAL 1 SECOND) WHERE device_id=?`, revision, devices[0]); err != nil {
		t.Fatal(err)
	}
	target := ai.CloudTarget{DeviceID: devices[0], UserID: user, Revision: revision, Remove: true}
	if current, err := store.CompleteCloud(ctx, target, true); err != nil || !current {
		t.Fatalf("CompleteCloud settled current=%v err=%v", current, err)
	}
	if ids, err := store.ClaimCloud(ctx, 100, time.Minute); err != nil || containsAny(ids, ours) {
		t.Fatalf("settled target claimed again: %v err=%v", ids, err)
	}
	if _, err := store.PrepareCloud(ctx, user, []string{devices[0]}, "role-new", false); err != nil {
		t.Fatal(err)
	}
	if ids, err := store.ClaimCloud(ctx, 100, time.Minute); err != nil || len(ids) != 1 || ids[0] != devices[0] {
		t.Fatalf("new target after settling not claimable: %v err=%v", ids, err)
	}
}

func TestAICloudCleanupRejectsDeviceOutsidePool(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := mysqlstore.NewRoleLifecycleStore(db)
	count := func(device string) (n int) {
		t.Helper()
		if err := db.Get(&n, `SELECT COUNT(*) FROM ai_cloud_role_target WHERE device_id=?`, device); err != nil {
			t.Fatal(err)
		}
		return n
	}

	unknown := uniqueDevID()
	if _, err := store.CloudTarget(ctx, unknown, true); !errors.Is(err, ai.ErrDeviceUnknown) {
		t.Fatalf("unknown device error = %v, want ErrDeviceUnknown", err)
	}
	if n := count(unknown); n != 0 {
		t.Fatalf("unknown device persisted %d cloud targets", n)
	}

	// An unbound device that was provisioned still needs its cloud role cleared.
	known, _ := seedPool(t, db)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM ai_cloud_role_target WHERE device_id=?`, known) })
	target, err := store.CloudTarget(ctx, known, true)
	if err != nil || !target.Remove || target.DeviceID != known {
		t.Fatalf("provisioned device target=%+v err=%v", target, err)
	}
	if n := count(known); n != 1 {
		t.Fatalf("provisioned device has %d cloud targets, want 1", n)
	}
}

func containsAny(ids []string, set map[string]bool) bool {
	for _, id := range ids {
		if set[id] {
			return true
		}
	}
	return false
}

func TestAICloudStaleCompletionPreservesReplacement(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := mysqlstore.NewRoleLifecycleStore(db)
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprintf("remove=%v", remove), func(t *testing.T) {
			device := uniqueDevID()
			const user = 101
			if _, err := db.Exec(`INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,?,NOW())`, device, user); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = db.Exec(`DELETE FROM ai_cloud_role_target WHERE device_id=?`, device)
				_, _ = db.Exec(`DELETE FROM ai_cloud_role_ref WHERE device_id=?`, device)
				_, _ = db.Exec(`DELETE FROM device_bind WHERE device_id=?`, device)
			})
			old, err := store.PrepareCloud(ctx, user, []string{device}, "old-role", remove)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.PrepareCloud(ctx, user, []string{device}, "new-role", false); err != nil {
				t.Fatal(err)
			}
			if err = store.DelayCloud(ctx, old[0]); err != nil {
				t.Fatal(err)
			}
			var attempts int
			if err = db.Get(&attempts, `SELECT attempts FROM ai_cloud_role_target WHERE device_id=?`, device); err != nil {
				t.Fatal(err)
			}
			if attempts != 0 {
				t.Fatalf("old failure incremented replacement attempts: %d", attempts)
			}
			current, err := store.CompleteCloud(ctx, old[0], true)
			if err != nil {
				t.Fatal(err)
			}
			if current {
				t.Error("old target was accepted after its replacement")
			}
			var due, protected bool
			if err = db.Get(&due, `SELECT next_attempt_at<=NOW() FROM ai_cloud_role_target WHERE device_id=?`, device); err != nil {
				t.Fatal(err)
			}
			if err = db.Get(&protected, `SELECT EXISTS(SELECT 1 FROM ai_cloud_role_ref WHERE device_id=? AND role_id='new-role')`, device); err != nil {
				t.Fatal(err)
			}
			if !due || !protected {
				t.Fatalf("replacement due=%v deletion-protected=%v, want both true", due, protected)
			}
		})
	}
}

func TestAICloudRetryFencesOwnershipAndResumesAfterLease(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := mysqlstore.NewRoleLifecycleStore(db)
	device := uniqueDevID()
	const user = 102
	if _, err := db.Exec(`INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,?,NOW())`, device, user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM ai_cloud_role_target WHERE device_id=?`, device)
		_, _ = db.Exec(`DELETE FROM ai_cloud_role_ref WHERE device_id=?`, device)
		_, _ = db.Exec(`DELETE FROM device_bind WHERE device_id=?`, device)
	})
	targets, err := store.PrepareCloud(ctx, user, []string{device}, "role", false)
	if err != nil {
		t.Fatal(err)
	}
	old := targets[0]
	if ids, e := store.ClaimCloud(ctx, 100, time.Minute); e != nil || !containsAny(ids, map[string]bool{device: true}) {
		t.Fatalf("claim: ids=%v err=%v", ids, e)
	}
	if err = store.DelayCloud(ctx, old); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err = db.Get(&attempts, `SELECT attempts FROM ai_cloud_role_target WHERE device_id=?`, device); err != nil || attempts != 1 {
		t.Fatalf("current failure: attempts=%d err=%v", attempts, err)
	}
	// A device ownership change does not acquire the AI named device lock.
	if _, err = db.Exec(`UPDATE device_bind SET user_id=0,binding_revision=binding_revision+1 WHERE device_id=?`, device); err != nil {
		t.Fatal(err)
	}
	if err = store.DelayCloud(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&attempts, `SELECT attempts FROM ai_cloud_role_target WHERE device_id=?`, device); err != nil || attempts != 1 {
		t.Fatalf("superseded failure: attempts=%d err=%v", attempts, err)
	}
	if current, e := store.CompleteCloud(ctx, old, true); e != nil || current {
		t.Fatalf("superseded completion: current=%v err=%v", current, e)
	}
	// A restarted worker reclaims the expired lease and derives a cleanup target
	// from current ownership instead of replaying the old role assignment.
	if _, err = db.Exec(`UPDATE ai_cloud_role_target SET next_attempt_at=DATE_SUB(NOW(),INTERVAL 1 SECOND) WHERE device_id=?`, device); err != nil {
		t.Fatal(err)
	}
	restarted := mysqlstore.NewRoleLifecycleStore(db)
	if ids, e := restarted.ClaimCloud(ctx, 100, time.Minute); e != nil || !containsAny(ids, map[string]bool{device: true}) {
		t.Fatalf("recovery claim: ids=%v err=%v", ids, e)
	}
	current, err := restarted.CloudTarget(ctx, device, false)
	if err != nil || !current.Remove || current.UserID != 0 || current.Revision != old.Revision+1 {
		t.Fatalf("recovery target=%+v err=%v", current, err)
	}
	if applied, e := restarted.CompleteCloud(ctx, current, true); e != nil || !applied {
		t.Fatalf("cleanup completion: current=%v err=%v", applied, e)
	}
	if ids, e := restarted.ClaimCloud(ctx, 100, time.Minute); e != nil || containsAny(ids, map[string]bool{device: true}) {
		t.Fatalf("settled cleanup reclaimed: ids=%v err=%v", ids, e)
	}
}
