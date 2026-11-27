package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"thing-connect/internal/push"
	"thing-connect/internal/recognition"
	"thing-connect/internal/requestguard"
	"thing-connect/internal/store/mysql"
	"thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
	"thing-connect/push-server/handler"
)

// Exclusively for a fresh capacity_test database. Use a separate database for
// each run so historical measurements and retention batches remain comparable.
type capacityChannel struct {
	sent  atomic.Int64
	store *mysql.PushStore
}

func (c *capacityChannel) Send(ctx context.Context, d push.Delivery) push.Result {
	ok, err := c.store.ReserveSend(ctx, d)
	if !ok || err != nil {
		return push.Retry
	}
	c.sent.Add(1)
	return push.Accepted
}
func (c *capacityChannel) Ready(string) bool { return true }
func (c *capacityChannel) Login(context.Context, string) (string, error) {
	return "", fmt.Errorf("capacity test has no WeChat identity")
}

func TestFirstTierCapacity(t *testing.T) {
	if os.Getenv("PUSH_CAPACITY_TEST") != "1" {
		t.Skip("opt-in 10 minute MySQL capacity run")
	}
	cfg := testenv.LoadConfigOrSkip(t, "../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	var name string
	if err := db.Get(&name, "SELECT DATABASE()"); err != nil || !strings.HasSuffix(name, "_capacity_test") {
		t.Fatal("requires dedicated _capacity_test schema")
	}
	var tables int
	if err := db.Get(&tables, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE()"); err != nil || tables != 0 {
		t.Fatal("requires empty capacity schema")
	}
	if err := migrate.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE TABLE capacity_seq(n INT NOT NULL PRIMARY KEY)")
	exec("INSERT INTO capacity_seq VALUES(0)")
	for step := 1; step < 100000; step *= 2 {
		exec("INSERT INTO capacity_seq SELECT n+? FROM capacity_seq WHERE n+?<100000", step, step)
	}
	exec("INSERT INTO device_bind(device_id,user_id) SELECT CONCAT('CAP',n),n+1 FROM capacity_seq")
	exec("INSERT INTO push_recipient(user_id,app_id,open_id,generation) SELECT n+1,'app',CONCAT('wx',n),CONCAT('g',n) FROM capacity_seq WHERE MOD(n,10)=0")
	exec("INSERT INTO push_subscription(generation,template_id,grant_id) SELECT generation,?,'grant' FROM push_recipient", push.AlarmTemplate)
	for batch := 0; batch < 30; batch++ {
		exec(`INSERT INTO push_event(device_id,bind_id,source_event_id,registered_at,received_at,outcome)
 SELECT CONCAT('CAP',n),'history',CONCAT('hist',?,':alarm'),UTC_TIMESTAMP(6),
 TIMESTAMPADD(SECOND,-(?*8640+MOD(n,8640)),UTC_TIMESTAMP(6)),'interval' FROM capacity_seq`, batch, batch)
	}
	t.Log("seeded 3M events and 100k device bindings")
	store := mysql.NewPushStore(db)
	source := mysql.NewRecognitionStore(db)
	if err := source.ConfigureSource(context.Background(), push.DefaultDeliveryPolicy(), 0, true); err != nil {
		t.Fatal(err)
	}
	ch := &capacityChannel{store: store}
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	policy := push.DefaultPolicy()
	policy.AppID = "app"
	service := push.New(store, ch, policy)
	handler.Register(engine, func() *push.Service { return service }, "capacity-key", "capacity-secret")
	guard, err := requestguard.New(cfg.Server.RequestLimits)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(guard.Wrap(engine))
	defer server.Close()
	publisher := recognition.Publisher{Queue: source, BaseURL: server.URL, InternalKey: "capacity-key"}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	start := func(run func(context.Context)) { wg.Add(1); go func() { defer wg.Done(); run(ctx) }() }
	defer func() { cancel(); wg.Wait() }()
	for i := 0; i < 2; i++ {
		start(publisher.Run)
	}
	metrics := new(push.DeliveryMetrics)
	for i := 0; i < 8; i++ {
		start(func(c context.Context) { push.RunDelivery(c, store, ch, metrics) })
	}
	start(func(c context.Context) { push.RunMaintenance(c, store) })
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	total := 0
	startTime := time.Now()
	for second := 0; second < 600; second++ {
		<-tick.C
		count := 167
		if second >= 300 && second < 310 {
			count += 1000
		}
		// Rotate device identities through the full population while preserving
		// source id uniqueness when the stream wraps.
		offset := total % 100000
		for left := count; left > 0; {
			n := min(left, 100000-offset)
			exec(`INSERT INTO recognition_user_event_outbox(device_id,bind_id,source_event_id,event_type,occurred_at,registered_at,next_at,expires_at)
 SELECT b.device_id,CONCAT(b.id,':',b.binding_revision),CONCAT('cap',?+s.n,':alarm'),'alarm',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)+INTERVAL 120 SECOND
 FROM capacity_seq s JOIN device_bind b ON b.device_id=CONCAT('CAP',s.n) WHERE s.n>=? AND s.n<?`, total-offset, offset, offset+n)
			total += n
			left -= n
			offset = 0
		}
		if (second+1)%60 == 0 {
			t.Logf("second=%d injected=%d accepted_by_simulator=%d", second+1, total, ch.sent.Load())
		}
	}
	injectedAt := time.Now()
	var accepted, queued, skipped, expired, delivered int
	for {
		if err := db.Get(&accepted, "SELECT COUNT(*) FROM recognition_user_event_outbox WHERE status='accepted'"); err != nil {
			t.Fatal(err)
		}
		if err := db.Get(&queued, "SELECT COUNT(*) FROM push_event WHERE outcome='queued'"); err != nil {
			t.Fatal(err)
		}
		if err := db.Get(&delivered, "SELECT COUNT(*) FROM push_delivery WHERE status='accepted'"); err != nil {
			t.Fatal(err)
		}
		if accepted == total && delivered == queued && int64(queued) == ch.sent.Load() {
			break
		}
		if time.Since(injectedAt) > 10*time.Minute {
			t.Fatalf("drain timeout accepted=%d/%d sent=%d/%d", accepted, total, ch.sent.Load(), queued)
		}
		time.Sleep(time.Second)
	}
	if err := db.Get(&skipped, "SELECT COUNT(*) FROM push_event WHERE outcome='unsubscribed'"); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&expired, "SELECT COUNT(*) FROM push_delivery WHERE status='expired'"); err != nil {
		t.Fatal(err)
	}
	t.Logf("input=%d admitted=%d skipped=%d sent=%d expired=%d injection=%s drain_after_last_input=%s", total, queued, skipped, ch.sent.Load(), expired, injectedAt.Sub(startTime), time.Since(injectedAt))
	if queued+skipped != total || expired != 0 {
		t.Fatal("event accounting or delivery deadline failed")
	}
}
