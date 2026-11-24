package mysql_test

import (
	"context"
	"testing"
	"time"

	"thing-connect/internal/adminmail"
	mysqlstore "thing-connect/internal/store/mysql"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestAdminResetMailLeaseRecoveryAndTerminalAge(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	defer db.Close()
	if err := mysqlmigrate.Migrate(db); err != nil {
		t.Fatal(err)
	}
	queue := mysqlstore.NewResetMailStore(db)
	ctx := context.Background()
	res, err := db.Exec(`INSERT INTO admin_reset_mail_outbox(user_id,email,created_at) VALUES(0,'lease@example.test',NOW()-INTERVAL 20 MINUTE)`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM admin_reset_mail_outbox WHERE id=?`, id)
	first, err := queue.Claim(ctx)
	if err != nil || first == nil || first.ID != id {
		t.Fatalf("claim=%+v err=%v", first, err)
	}
	if other, err := queue.Claim(ctx); err != nil || other != nil {
		t.Fatalf("overlap=%+v err=%v", other, err)
	}
	for attempt := 2; attempt <= adminmail.MaxAttempts; attempt++ {
		if _, err := db.Exec(`UPDATE admin_reset_mail_outbox SET next_at=NOW(6) WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		current, err := queue.Claim(ctx)
		if err != nil || current == nil || current.Attempts != attempt {
			t.Fatalf("takeover=%+v err=%v", current, err)
		}
		if err := queue.Finish(ctx, *first, true); err != nil {
			t.Fatal(err)
		}
		sample, err := queue.Backlog(ctx)
		if err != nil || sample.Pending != 1 || sample.OldestAge < 20*time.Minute || sample.Exhausted != 0 {
			t.Fatalf("inflight backlog=%+v err=%v", sample, err)
		}
		if err := queue.Finish(ctx, *current, false); err != nil {
			t.Fatal(err)
		}
	}
	if current, err := queue.Claim(ctx); err != nil || current != nil {
		t.Fatalf("exhausted claim=%+v err=%v", current, err)
	}
	sample, err := queue.Backlog(ctx)
	if err != nil || sample.Pending != 0 || sample.Exhausted != 1 {
		t.Fatalf("exhausted backlog=%+v err=%v", sample, err)
	}
	if _, err := db.Exec(`UPDATE admin_reset_mail_outbox SET next_at=NOW(6)-INTERVAL 61 MINUTE WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	sample, err = queue.Backlog(ctx)
	if err != nil || sample.Exhausted != 0 || sample.OldestAge != 0 {
		t.Fatalf("recovered backlog=%+v err=%v", sample, err)
	}
}

func TestAdminResetMailWorkerAcknowledgesAfterStop(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	defer db.Close()
	if err := mysqlmigrate.Migrate(db); err != nil {
		t.Fatal(err)
	}
	const email = "deliver@example.test"
	res, err := db.Exec(`INSERT INTO admin_reset_mail_outbox(user_id,email) VALUES(0,?)`, email)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	defer db.Exec(`DELETE FROM admin_reset_mail_outbox WHERE id=?`, id)
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	delivered := 0
	worker := adminmail.NewWorker(mysqlstore.NewResetMailStore(db), func(delivery context.Context, address string) error {
		stop()
		if delivery.Err() != nil || address != email {
			t.Fatalf("delivery err=%v address=%q", delivery.Err(), address)
		}
		delivered++
		return nil
	})
	worker.Run(ctx)
	var left int
	if err := db.Get(&left, `SELECT COUNT(*) FROM admin_reset_mail_outbox WHERE id=?`, id); err != nil || delivered != 1 || left != 0 {
		t.Fatalf("delivered=%d left=%d err=%v", delivered, left, err)
	}
}
