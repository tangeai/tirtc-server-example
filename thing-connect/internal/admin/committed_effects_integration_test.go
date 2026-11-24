package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestReloadAccessAppliesCommittedGrantAfterRequestCancellation(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := mysqlmigrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	access, err := NewAccessController(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	role, err := db.Exec(`INSERT INTO admin_roles(code,name) VALUES (?, 'reload probe')`, "reload_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	roleID, _ := role.LastInsertId()
	user, err := db.Exec(`INSERT INTO admin_users(email,password,nick_name) VALUES (?, 'unused', 'reload')`, "reload_"+suffix+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := user.LastInsertId()
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM admin_user_roles WHERE admin_user_id=?`, userID)
		_, _ = db.Exec(`DELETE FROM admin_users WHERE id=?`, userID)
		_, _ = db.Exec(`DELETE FROM admin_role_permissions WHERE role_id=?`, roleID)
		_, _ = db.Exec(`DELETE FROM admin_roles WHERE id=?`, roleID)
	})
	if _, err := db.Exec(`INSERT INTO admin_role_permissions(role_id,permission_code) VALUES (?, 'user.read')`, roleID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO admin_user_roles(admin_user_id,role_id) VALUES (?,?)`, userID, roleID); err != nil {
		t.Fatal(err)
	}
	if access.Enforce(userID, "user.read") {
		t.Fatal("grant visible before reload")
	}

	disconnected, cancel := context.WithCancel(ctx)
	cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPut, "/", nil).WithContext(disconnected)
	if !(&HTTPServer{access: access}).publishCommittedAccess(c) {
		t.Fatal("committed policy reload failed")
	}
	if !access.Enforce(userID, "user.read") {
		t.Fatal("committed RBAC change was not applied after the client disconnected")
	}
	if _, err := db.Exec(`DELETE FROM admin_role_permissions WHERE role_id=?`, roleID); err != nil {
		t.Fatal(err)
	}
	if err := access.Reload(disconnected); err == nil {
		t.Fatal("cancelled reload succeeded")
	}
	if access.Enforce(userID, "user.read") {
		t.Fatal("previous policy remained usable after a failed reload")
	}
	if err := access.EnsureCurrent(ctx); err != nil {
		t.Fatal(err)
	}
	if access.Enforce(userID, "user.read") {
		t.Fatal("recovery did not load committed policy")
	}
}

func TestPasswordResetCommandCommitsWithAudit(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := mysqlmigrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}

	result, err := db.Exec(`INSERT INTO users (email,password,status) VALUES (?,?,1)`, fmt.Sprintf("reset-audit-%d@example.test", time.Now().UnixNano()), "unused")
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := result.LastInsertId()
	resourceID := strconv.FormatInt(userID, 10)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM admin_audit_log WHERE action='user.password_reset' AND resource_id=?`, resourceID)
		_, _ = db.Exec(`DELETE FROM admin_reset_mail_outbox WHERE user_id=?`, userID)
		_, _ = db.Exec(`DELETE FROM users WHERE id=?`, userID)
	})

	s := &HTTPServer{store: NewStore(db)}
	router := gin.New()
	router.POST("/users/:id/password-reset", s.sendPasswordResetEmail)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/users/"+resourceID+"/password-reset", strings.NewReader(`{"reason":"integration test"}`)))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var after string
	if err := db.Get(&after, `SELECT after_value FROM admin_audit_log WHERE action='user.password_reset' AND resource_id=? ORDER BY id DESC LIMIT 1`, resourceID); err != nil {
		t.Fatalf("queued command has no audit row: %v", err)
	}
	var audit struct {
		CommandID string `json:"command_id"`
	}
	var queued string
	if err := db.Get(&queued, `SELECT CONCAT('mysql-',id) FROM admin_reset_mail_outbox WHERE user_id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(after), &audit); err != nil || audit.CommandID == "" || audit.CommandID != queued {
		t.Fatalf("audit after_value=%s queued=%s err=%v", after, queued, err)
	}
	disabled, err := db.Exec(`UPDATE users SET status=0 WHERE id=?`, userID)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := disabled.RowsAffected(); n != 1 {
		t.Fatal("test user was not disabled")
	}
	forbidden := httptest.NewRecorder()
	router.ServeHTTP(forbidden, httptest.NewRequest(http.MethodPost, "/users/"+resourceID+"/password-reset", strings.NewReader(`{"reason":"disabled user"}`)))
	if forbidden.Code != http.StatusNotFound {
		t.Fatalf("disabled user status=%d", forbidden.Code)
	}
	if _, err = db.Exec(`UPDATE users SET status=1 WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	// An audit write that cannot fit the schema must roll back the queued command.
	if err := s.store.QueuePasswordReset(context.Background(), userID, AuditEvent{Action: strings.Repeat("a", 1000)}); err == nil {
		t.Fatal("invalid audit accepted")
	}
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM admin_reset_mail_outbox WHERE user_id=?`, userID); err != nil || count != 1 {
		t.Fatalf("after audit rollback count=%d err=%v", count, err)
	}
}
