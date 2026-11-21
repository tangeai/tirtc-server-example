package mysql_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	mysqlstore "thing-connect/internal/store/mysql"
	"thing-connect/internal/voip"
)

func openRemarkOutbox(t *testing.T) (*sqlx.DB, voip.RemarkStore) {
	t.Helper()
	db := openTestDB(t)
	if _, err := db.Exec(`DELETE FROM voip_remark_outbox`); err != nil {
		t.Fatalf("reset voip_remark_outbox: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM voip_remark_outbox`) })
	return db, mysqlstore.NewVoipRemarkStore(db)
}

func TestVoipRemarkSaveLeasesEventToWriter(t *testing.T) {
	db, store := openRemarkOutbox(t)
	device := fmt.Sprintf("sn_remark%d", time.Now().UnixNano()%1_000_000_000)
	if _, err := db.Exec(`INSERT INTO device_bind(device_id,user_id) VALUES(?,7)`, device); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO voip_device_auth(device_id,wx_open_id,auth_status) VALUES(?,?,'active')`, device, "openid-remark"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM voip_device_auth WHERE device_id=?`, device)
		_, _ = db.Exec(`DELETE FROM device_bind WHERE device_id=?`, device)
	})

	event, err := store.Save(context.Background(), device, "openid-remark", "外婆", 7, time.Minute)
	if err != nil || event.ID == 0 || event.Lease == "" {
		t.Fatalf("Save event=%+v err=%v", event, err)
	}
	if others, err := store.Claim(context.Background(), 10, time.Minute); err != nil || len(others) != 0 {
		t.Fatalf("writer-leased event claimed by another instance: %v err=%v", others, err)
	}
	if applied, err := store.Complete(context.Background(), event); err != nil || !applied {
		t.Fatalf("writer Complete applied=%v err=%v", applied, err)
	}
}

func TestVoipRemarkClaimsDoNotOverlapAndStaleAcksAreIgnored(t *testing.T) {
	db, store := openRemarkOutbox(t)
	for i := 0; i < 25; i++ {
		if _, err := db.Exec(`INSERT INTO voip_remark_outbox(device_id,peer_id) VALUES(?,?)`, fmt.Sprintf("sn_r%d", i), "peer"); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				events, err := store.Claim(context.Background(), 4, time.Minute)
				if err != nil {
					t.Errorf("Claim: %v", err)
					return
				}
				if len(events) == 0 {
					return
				}
				mu.Lock()
				for _, event := range events {
					seen[event.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 25 {
		t.Fatalf("claimed %d distinct events, want 25", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("event %d claimed %d times", id, n)
		}
	}

	if _, err := db.Exec(`DELETE FROM voip_remark_outbox`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO voip_remark_outbox(device_id,peer_id) VALUES('sn_stale','peer')`); err != nil {
		t.Fatal(err)
	}
	first, err := store.Claim(context.Background(), 1, time.Second)
	if err != nil || len(first) != 1 {
		t.Fatalf("Claim: %v err=%v", first, err)
	}
	time.Sleep(2100 * time.Millisecond)
	second, err := store.Claim(context.Background(), 1, time.Minute)
	if err != nil || len(second) != 1 {
		t.Fatalf("takeover Claim: %v err=%v", second, err)
	}
	if applied, err := store.Retry(context.Background(), first[0], time.Hour); err != nil || applied {
		t.Fatalf("stale Retry applied=%v err=%v", applied, err)
	}
	if applied, err := store.Complete(context.Background(), first[0]); err != nil || applied {
		t.Fatalf("stale Complete applied=%v err=%v", applied, err)
	}
	if applied, err := store.Retry(context.Background(), second[0], time.Hour); err != nil || !applied {
		t.Fatalf("current Retry applied=%v err=%v", applied, err)
	}
}
