package mysql_test

import (
	"context"
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

func containsAny(ids []string, set map[string]bool) bool {
	for _, id := range ids {
		if set[id] {
			return true
		}
	}
	return false
}
