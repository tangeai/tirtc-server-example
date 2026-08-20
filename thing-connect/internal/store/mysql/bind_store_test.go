package mysql_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"thing-connect/internal/db"
	"thing-connect/internal/model"
	"thing-connect/internal/store"
	mysqlstore "thing-connect/internal/store/mysql"
	"thing-connect/internal/testenv"
)

func openTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	cfg := testenv.LoadConfigOrSkip(t, "../../../tests/testdata/config.yaml")
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	// Reset device pool so each test starts with all devices fresh/unbound.
	// 禁止流转 means a device with any device_bind row is never re-allocated, and
	// the shared test DB accumulates state across runs/packages without this.
	if _, err := sqlDB.Exec(`DELETE FROM device_bind`); err != nil {
		t.Fatalf("reset device_bind: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE device_pool SET status=0`); err != nil {
		t.Fatalf("reset device_pool: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

func uniqueDevID() string {
	return fmt.Sprintf("test_dev_%d", time.Now().UnixNano())
}

// seedPool inserts a device into device_pool and returns its device_id and key.
func seedPool(t *testing.T, sqlDB *sqlx.DB) (id, key string) {
	t.Helper()
	id = uniqueDevID()
	key = "key_" + id
	if _, err := sqlDB.Exec(`INSERT INTO device_pool (device_id, device_key, status) VALUES (?,?,0)`, id, key); err != nil {
		t.Fatalf("seedPool: %v", err)
	}
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM device_pool WHERE device_id=?`, id) })
	return id, key
}

// seedUser inserts a user with quota=2 and returns its id.
func seedUser(t *testing.T, sqlDB *sqlx.DB) int64 {
	t.Helper()
	email := fmt.Sprintf("test_%d@example.com", time.Now().UnixNano())
	res, err := sqlDB.Exec(`INSERT INTO users (email, password, bind_quota) VALUES (?,?,2)`, email, "hash")
	if err != nil {
		t.Fatalf("seedUser: %v", err)
	}
	uid, _ := res.LastInsertId()
	t.Cleanup(func() { sqlDB.Exec(`DELETE FROM users WHERE id=?`, uid) })
	return uid
}

// cleanBind removes test rows from device_bind and device_bind_log.
func cleanBind(t *testing.T, sqlDB *sqlx.DB, deviceID string) {
	t.Helper()
	sqlDB.Exec(`DELETE FROM device_bind WHERE device_id=?`, deviceID)
	sqlDB.Exec(`DELETE FROM device_bind_log WHERE device_id=?`, deviceID)
}

func TestCommitBindFromPool(t *testing.T) {
	sqlDB := openTestDB(t)
	devID, _ := seedPool(t, sqlDB)
	userID := seedUser(t, sqlDB)
	defer cleanBind(t, sqlDB, devID)

	store := mysqlstore.NewBindStore(sqlDB)
	fp := model.Fingerprint{MAC: "AA:BB:CC:DD:EE:01"}
	got, err := store.CommitBindFromPool(context.Background(), fp, userID)
	if err != nil {
		t.Fatalf("CommitBindFromPool: %v", err)
	}
	if got == "" {
		t.Fatal("CommitBindFromPool: empty deviceID returned")
	}

	// Verify: device_bind has user_id=userID
	var uid int64
	sqlDB.QueryRow(`SELECT user_id FROM device_bind WHERE device_id=?`, got).Scan(&uid)
	if uid != userID {
		t.Errorf("device_bind.user_id: want %d, got %d", userID, uid)
	}

	// Verify: quota decremented
	var quota int
	sqlDB.QueryRow(`SELECT bind_quota FROM users WHERE id=?`, userID).Scan(&quota)
	if quota != 1 {
		t.Errorf("bind_quota: want 1, got %d", quota)
	}
}

// TestGetBindByFingerprint_CollisionPriority covers a fingerprint (e.g. a
// duplicate/blank MAC) shared by multiple device_bind rows. The caller's own
// row must win regardless of insertion order, so a collision with another
// user's device never hides the caller's own device.
func TestGetBindByFingerprint_CollisionPriority(t *testing.T) {
	sqlDB := openTestDB(t)
	mac := fmt.Sprintf("COLLIDE:%d", time.Now().UnixNano())

	otherDevID := uniqueDevID()
	selfDevID := uniqueDevID()
	defer cleanBind(t, sqlDB, otherDevID)
	defer cleanBind(t, sqlDB, selfDevID)

	selfUser := seedUser(t, sqlDB)
	otherUser := seedUser(t, sqlDB)

	// Row owned by another user, inserted first so it would win under a plain
	// "LIMIT 1" query with no ordering.
	if _, err := sqlDB.Exec(
		`INSERT INTO device_bind (device_id, mac, user_id, last_user_id, bind_time) VALUES (?,?,?,?,NOW())`,
		otherDevID, mac, otherUser, otherUser); err != nil {
		t.Fatalf("seed other row: %v", err)
	}
	// Caller's own row, inserted second.
	if _, err := sqlDB.Exec(
		`INSERT INTO device_bind (device_id, mac, user_id, last_user_id, bind_time) VALUES (?,?,?,?,NOW())`,
		selfDevID, mac, selfUser, selfUser); err != nil {
		t.Fatalf("seed self row: %v", err)
	}

	bindStore := mysqlstore.NewBindStore(sqlDB)
	row, err := bindStore.GetBindByFingerprint(context.Background(), mac, selfUser)
	if err != nil {
		t.Fatalf("GetBindByFingerprint: %v", err)
	}
	if row == nil || row.DeviceID != selfDevID {
		t.Fatalf("GetBindByFingerprint: want caller's own row %s, got %+v", selfDevID, row)
	}
}

func TestCommitUnbind(t *testing.T) {
	sqlDB := openTestDB(t)
	devID, _ := seedPool(t, sqlDB)
	userID := seedUser(t, sqlDB)
	defer cleanBind(t, sqlDB, devID)

	bindStore := mysqlstore.NewBindStore(sqlDB)
	fp := model.Fingerprint{MAC: "AA:BB:CC:DD:EE:02"}
	gotID, _ := bindStore.CommitBindFromPool(context.Background(), fp, userID)
	if _, err := sqlDB.Exec(
		`UPDATE device_bind SET device_name='客厅学习机' WHERE device_id=?`,
		gotID); err != nil {
		t.Fatal(err)
	}

	if _, err := sqlDB.Exec(`
		INSERT INTO call_contact (device_id_a, device_id_b, user_id_a, user_id_b, status)
		VALUES (?, ?, ?, ?, 1)`, gotID, "peer-"+gotID, userID, userID); err != nil {
		t.Fatalf("seed call contact: %v", err)
	}
	defer sqlDB.Exec(`DELETE FROM call_contact WHERE device_id_a=? OR device_id_b=?`, gotID, gotID)
	defer sqlDB.Exec(`DELETE FROM cleanup_outbox WHERE device_id=?`, gotID)

	cleanupStore, ok := bindStore.(store.UnbindCleanupStore)
	if !ok {
		t.Fatal("bind store does not support transactional cleanup")
	}
	if err := cleanupStore.CommitUnbindWithCleanup(context.Background(), gotID, userID, []string{"ai", "voip", "call"}); err != nil {
		t.Fatalf("CommitUnbindWithCleanup: %v", err)
	}

	var uid int64
	var deviceName string
	sqlDB.QueryRow(
		`SELECT user_id, device_name FROM device_bind WHERE device_id=?`,
		gotID).Scan(&uid, &deviceName)
	if uid != 0 {
		t.Errorf("after unbind user_id: want 0, got %d", uid)
	}
	if deviceName != "" {
		t.Errorf("after unbind device_name: want empty, got %q", deviceName)
	}

	var quota int
	sqlDB.QueryRow(`SELECT bind_quota FROM users WHERE id=?`, userID).Scan(&quota)
	if quota != 2 {
		t.Errorf("after unbind bind_quota: want 2, got %d", quota)
	}

	// Verify: device_pool.status resets to the released state. The retained
	// device_bind row prevents allocation to a different user.
	var poolStatus int
	sqlDB.QueryRow(`SELECT status FROM device_pool WHERE device_id=?`, gotID).Scan(&poolStatus)
	if poolStatus != 0 {
		t.Errorf("after unbind device_pool.status: want 0, got %d", poolStatus)
	}

	var queued int
	if err := sqlDB.Get(&queued, `SELECT COUNT(*) FROM cleanup_outbox WHERE device_id=?`, gotID); err != nil {
		t.Fatal(err)
	}
	if queued != 3 {
		t.Errorf("cleanup tasks = %d, want 3", queued)
	}
}

// TestCommitBindFromPool_AfterUnbind_NoReuse verifies the 禁止流转 rule: once a
// device_id is assigned to a user, unbinding does NOT return it to the pool for
// someone else — a later bind by a different user must allocate a different,
// never-bound device_id, never the one user A used. (Same user reclaiming their
// own unbound device goes through CommitClaim/Case D, not pool allocation.)
//
// Does not assume which device_id the pool hands out (it picks the lowest-id
// never-bound row, which may be a pre-seeded TG* device, not our seedPool one);
// only asserts B's device differs from A's.
func TestCommitBindFromPool_AfterUnbind_NoReuse(t *testing.T) {
	sqlDB := openTestDB(t)
	seedPool(t, sqlDB) // ensure fresh devices exist for A and B
	seedPool(t, sqlDB)
	userA := seedUser(t, sqlDB)
	userB := seedUser(t, sqlDB)

	s := mysqlstore.NewBindStore(sqlDB)
	fpA := model.Fingerprint{MAC: fmt.Sprintf("AA:BB:CC:DD:EE:%02X", time.Now().UnixNano()%256)}
	fpB := model.Fingerprint{MAC: fmt.Sprintf("AA:BB:CC:DD:EE:%02X", (time.Now().UnixNano()+1)%256)}

	// 1. User A binds some fresh device.
	gotID, err := s.CommitBindFromPool(context.Background(), fpA, userA)
	if err != nil {
		t.Fatalf("A CommitBindFromPool: %v", err)
	}
	defer cleanBind(t, sqlDB, gotID)
	// 2. User A unbinds (device_bind row kept user_id=0).
	if err := s.CommitUnbind(context.Background(), gotID, userA); err != nil {
		t.Fatalf("CommitUnbind: %v", err)
	}
	// 3. User B binds — must NOT reuse A's device_id.
	gotID2, err := s.CommitBindFromPool(context.Background(), fpB, userB)
	if err != nil {
		t.Fatalf("B CommitBindFromPool: %v", err)
	}
	defer cleanBind(t, sqlDB, gotID2)
	if gotID2 == gotID {
		t.Fatalf("禁止流转 violated: user B reused user A's device_id %s", gotID)
	}

	var uid int64
	sqlDB.QueryRow(`SELECT user_id FROM device_bind WHERE device_id=?`, gotID2).Scan(&uid)
	if uid != userB {
		t.Errorf("device_bind.user_id for B's device: want %d, got %d", userB, uid)
	}
}

func TestCommitClaim_ConcurrentConflict(t *testing.T) {
	// Seed an unowned device_bind row manually
	sqlDB := openTestDB(t)
	userA := seedUser(t, sqlDB)
	userB := seedUser(t, sqlDB)

	devID := uniqueDevID()
	defer cleanBind(t, sqlDB, devID)
	sqlDB.Exec(`INSERT INTO device_bind (device_id, mac, user_id, last_user_id, bind_time) VALUES (?,?,0,?,NOW())`,
		devID, "AA:BB:CC:DD:EE:03", userA)

	s := mysqlstore.NewBindStore(sqlDB)
	fp := model.Fingerprint{MAC: "AA:BB:CC:DD:EE:03"}

	// User B claims it
	if err := s.CommitClaim(context.Background(), devID, fp, userB); err != nil {
		t.Fatalf("CommitClaim: %v", err)
	}

	// Verify user_id=B
	var uid int64
	sqlDB.QueryRow(`SELECT user_id FROM device_bind WHERE device_id=?`, devID).Scan(&uid)
	if uid != userB {
		t.Errorf("CommitClaim: user_id want %d, got %d", userB, uid)
	}
}

func TestGetQuotaReadsBindQuota(t *testing.T) {
	sqlDB := openTestDB(t)
	userID := seedUser(t, sqlDB) // seeded with bind_quota=2

	us := mysqlstore.NewUserStore(sqlDB)
	q, err := us.GetQuota(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if q != 2 {
		t.Errorf("GetQuota: want 2, got %d", q)
	}
}

func TestCommitClaim_RealConcurrency(t *testing.T) {
	sqlDB := openTestDB(t)
	userA := seedUser(t, sqlDB)
	userB := seedUser(t, sqlDB)
	devID := uniqueDevID()
	defer cleanBind(t, sqlDB, devID)
	// Insert unowned device_bind row (user_id=0, last_user_id=userA)
	sqlDB.Exec(`INSERT INTO device_bind (device_id, mac, user_id, last_user_id) VALUES (?,?,0,?)`,
		devID, "AA:BB:CC:DD:EE:03", userA)

	s := mysqlstore.NewBindStore(sqlDB)
	fp := model.Fingerprint{MAC: "AA:BB:CC:DD:EE:03"}

	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = s.CommitClaim(context.Background(), devID, fp, userB)
		}(i)
	}
	wg.Wait()

	successCount := 0
	conflictCount := 0
	for _, e := range errs {
		if e == nil {
			successCount++
		} else if errors.Is(e, store.ErrSlotConflict) {
			conflictCount++
		} else {
			t.Errorf("unexpected error: %v", e)
		}
	}
	if successCount != 1 || conflictCount != 1 {
		t.Errorf("concurrent claim: want 1 success + 1 conflict, got %d success + %d conflict\nerrs: %v", successCount, conflictCount, errs)
	}
}

// TestCommitBindFromPool_ConcurrentSameMACSameUser asserts the per-user MAC
// uniqueness invariant: two concurrent binds of the same MAC by the same user
// must NOT allocate two device_ids. One wins (allocates), the other receives
// the SAME device_id back via ErrMACAlreadyBound — and device_bind holds exactly
// one row for (mac, user_id). Without the DB UNIQUE + in-tx check, a double-submit
// hands one user two device_ids for one MAC (the Case A hole).
func TestCommitBindFromPool_ConcurrentSameMACSameUser(t *testing.T) {
	sqlDB := openTestDB(t)
	seedPool(t, sqlDB) // two free devices — both could allocate if not guarded
	seedPool(t, sqlDB)
	userID := seedUser(t, sqlDB)
	mac := fmt.Sprintf("CC:CC:CC:CC:CC:%02X", time.Now().UnixNano()%256)
	fp := model.Fingerprint{MAC: mac}
	t.Cleanup(func() {
		sqlDB.Exec(`DELETE FROM device_bind WHERE mac=? AND user_id=?`, mac, userID)
		sqlDB.Exec(`DELETE FROM device_bind_log WHERE mac=? AND user_id=?`, mac, userID)
	})

	bs := mysqlstore.NewBindStore(sqlDB)
	type res struct {
		id  string
		err error
	}
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		rs []res
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := bs.CommitBindFromPool(context.Background(), fp, userID)
			mu.Lock()
			rs = append(rs, res{id, err})
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Invariant 1: exactly one device_bind row for (mac, user_id).
	var n int
	if err := sqlDB.Get(&n, `SELECT COUNT(*) FROM device_bind WHERE mac=? AND user_id=?`, mac, userID); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("device_bind rows for (mac,user): want 1, got %d — concurrent double-allocate", n)
	}

	// Invariant 2: both callers received the same device_id.
	ids := map[string]bool{}
	for _, r := range rs {
		if r.id != "" {
			ids[r.id] = true
		}
	}
	if len(ids) != 1 {
		t.Fatalf("want both callers to receive the same device_id, got distinct ids: %v", ids)
	}

	// Invariant 3: one success, one ErrMACAlreadyBound.
	var ok, already int
	for _, r := range rs {
		switch {
		case r.err == nil:
			ok++
		case errors.Is(r.err, store.ErrMACAlreadyBound):
			already++
		default:
			t.Fatalf("unexpected err: %v", r.err)
		}
	}
	if ok != 1 || already != 1 {
		t.Fatalf("want 1 success + 1 ErrMACAlreadyBound, got success=%d alreadybound=%d", ok, already)
	}
}
