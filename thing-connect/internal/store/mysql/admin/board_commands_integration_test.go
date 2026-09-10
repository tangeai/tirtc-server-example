package adminmysql

import (
	"context"
	"fmt"
	"testing"
	"time"

	adminapp "thing-connect/internal/admin"
	"thing-connect/internal/boardcatalog"
	mysqlstore "thing-connect/internal/store/mysql"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestBoardCommandsPersistRowsAndAudit(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../../../tests/testdata/config.yaml")
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := mysqlmigrate.Migrate(sqlDB); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := mysqlmigrate.MigrateAdmin(sqlDB); err != nil {
		t.Fatalf("MigrateAdmin: %v", err)
	}

	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	id := "board-" + suffix
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM admin_audit_log WHERE resource_type='board_resource' AND resource_id=?`, id)
		_, _ = sqlDB.Exec(`DELETE FROM board_resources WHERE id=?`, id)
	})
	store := NewBoardCommandStore(sqlDB)
	board := boardcatalog.Board{ID: id, Vendor: "厂商", Name: "音视频板", Model: "MODEL-" + suffix, Chip: "ESP32-S3", Summary: "用于测试实时音视频。", Capabilities: []string{"实时音视频"}, AdaptationStatus: "ready", ImageURL: "https://cdn.example.com/board.webp", DetailSlug: "board-" + suffix, SortOrder: 10, PublishStatus: "draft", Revision: 1, CreatedBy: 1, UpdatedBy: 1}
	audit := adminapp.AuditEvent{AdminUserID: 1, RequestID: "board-test", Method: "POST", Path: "/test", HTTPStatus: 200, Action: "board.create", Resource: "board_resource", ResourceID: id, Reason: "integration test", Success: true}
	created, err := store.Create(ctx, adminapp.BoardMutation{Board: board, Audit: audit})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.PublishStatus != "draft" {
		t.Fatalf("created board = %+v", created)
	}

	created.Name = "音视频开发板"
	created.UpdatedBy = 1
	updated, err := store.Update(ctx, adminapp.BoardMutation{Board: created, ExpectedRevision: 1, Audit: audit})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "音视频开发板" || updated.Revision != 2 {
		t.Fatalf("updated board = %+v", updated)
	}

	published, err := store.SetStatus(ctx, adminapp.BoardStatusMutation{ID: id, Status: "published", ExpectedRevision: 2, UpdatedBy: 1, Audit: audit})
	if err != nil {
		t.Fatal(err)
	}
	if published.PublishStatus != "published" || published.Revision != 3 || published.PublishedAt == nil {
		t.Fatalf("published board = %+v", published)
	}
	publicBoards, err := mysqlstore.NewBoardCatalogStore(sqlDB).ListPublished(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range publicBoards {
		found = found || item.ID == id
	}
	if !found {
		t.Fatal("published board not visible in public catalog")
	}

	if err := store.Delete(ctx, adminapp.BoardDeleteMutation{ID: id, ExpectedRevision: 3, Audit: audit}); err != nil {
		t.Fatal(err)
	}
	var auditCount int
	if err := sqlDB.Get(&auditCount, `SELECT COUNT(*) FROM admin_audit_log WHERE resource_type='board_resource' AND resource_id=?`, id); err != nil || auditCount != 4 {
		t.Fatalf("audit count=%d err=%v", auditCount, err)
	}
}
