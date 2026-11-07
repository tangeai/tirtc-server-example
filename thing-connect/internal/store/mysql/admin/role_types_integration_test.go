package adminmysql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	adminapp "thing-connect/internal/admin"
	"thing-connect/internal/ai/roletypes"
	"thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
	"time"
)

func TestRoleTypesSeedReplayConcurrentEditAndAudit(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	defer db.Close()
	if err := migrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := NewRoleTypeStore(db)
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM ai_role_types WHERE id LIKE 'preset-%'"); err != nil || count != 49 {
		t.Fatalf("defaults=%d err=%v", count, err)
	}
	original, err := store.Get(ctx, "preset-001")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec("UPDATE ai_role_types SET name=?,prompt=?,revision=? WHERE id='preset-001'", original.Name, original.Prompt, original.Revision)
	}()
	if _, err = db.Exec("UPDATE ai_role_types SET prompt='管理员已编辑',revision=revision+1 WHERE id='preset-001'"); err != nil {
		t.Fatal(err)
	}
	// Simulate a migration interrupted after data writes but before its ledger entry.
	if _, err = db.Exec("DELETE FROM schema_migrations WHERE component='admin' AND version=2"); err != nil {
		t.Fatal(err)
	}
	if err = migrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	kept, err := store.Get(ctx, "preset-001")
	if err != nil || kept.Prompt != "管理员已编辑" || kept.Revision != original.Revision+1 {
		t.Fatalf("seed overwrote admin: %+v %v", kept, err)
	}
	id := fmt.Sprintf("test-%d", time.Now().UnixNano())
	defer func() {
		_, _ = db.Exec("DELETE FROM admin_audit_log WHERE resource_type='ai_role_type' AND resource_id=?", id)
		_, _ = db.Exec("DELETE FROM ai_role_types WHERE id=?", id)
	}()
	value := roletypes.Type{ID: id, Name: id, Prompt: "提示词", VoiceID: "voice", VoiceModel: roletypes.VoiceModel, Status: "draft"}
	audit := adminapp.AuditEvent{AdminUserID: 1, Action: "ai_role_type.create", Resource: "ai_role_type", ResourceID: id, Reason: "集成测试", Success: true, HTTPStatus: 200}
	created, err := store.Save(ctx, adminapp.RoleTypeMutation{Type: value, Action: "create", Audit: audit})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			next := created
			next.Prompt = fmt.Sprintf("并发编辑%d", n)
			_, e := store.Save(ctx, adminapp.RoleTypeMutation{Type: next, Action: "update", ExpectedRevision: 1, Audit: audit})
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for e := range results {
		if e == nil {
			successes++
		} else if errors.Is(e, adminapp.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflict=%d", successes, conflicts)
	}
	if err = db.Get(&count, "SELECT COUNT(*) FROM admin_audit_log WHERE resource_type='ai_role_type' AND resource_id=?", id); err != nil || count != 2 {
		t.Fatalf("audit=%d %v", count, err)
	}
	next, _ := store.Get(ctx, id)
	next.Prompt = "应回滚"
	badAudit := audit
	// An overlong request ID forces an audit insert failure without changing schema.
	badAudit.RequestID = strings.Repeat("x", 400)
	_, err = store.Save(ctx, adminapp.RoleTypeMutation{Type: next, Action: "update", ExpectedRevision: next.Revision, Audit: badAudit})
	if err == nil {
		t.Fatal("expected audit insert failure")
	}
	after, _ := store.Get(ctx, id)
	if after.Prompt == "应回滚" || after.Revision != next.Revision {
		t.Fatal("unaudited edit was committed")
	}
}
