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
	"github.com/redis/go-redis/v9"

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
	(&HTTPServer{access: access}).reloadAccess(disconnected)
	if !access.Enforce(userID, "user.read") {
		t.Fatal("committed RBAC change was not applied after the client disconnected")
	}
}

type cancelAfterXAdd struct {
	cancel context.CancelFunc
	queued *string
}

func (h cancelAfterXAdd) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h cancelAfterXAdd) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h cancelAfterXAdd) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if cmd.Name() == "xadd" {
			*h.queued = cmd.(*redis.StringCmd).Val()
			h.cancel()
		}
		return err
	}
}

func TestPasswordResetCommandKeepsAuditWhenClientDisconnectsAfterEnqueue(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := mysqlmigrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	client := testenv.OpenRedisOrSkip(t, cfg)
	t.Cleanup(func() { _ = client.Close() })
	result, err := db.Exec(`INSERT INTO users (email,password,status) VALUES (?,?,1)`, fmt.Sprintf("reset-audit-%d@example.test", time.Now().UnixNano()), "unused")
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := result.LastInsertId()
	resourceID := strconv.FormatInt(userID, 10)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM admin_audit_log WHERE action='user.password_reset' AND resource_id=?`, resourceID)
		_, _ = db.Exec(`DELETE FROM users WHERE id=?`, userID)
	})

	var cancelRequest context.CancelFunc
	var queued string
	client.AddHook(cancelAfterXAdd{cancel: func() { cancelRequest() }, queued: &queued})
	t.Cleanup(func() {
		if queued != "" {
			_ = client.XDel(context.Background(), "thingconnect:admin:user-commands", queued).Err()
		}
	})
	s := &HTTPServer{store: NewStore(db), redis: client}
	router := gin.New()
	router.POST("/users/:id/password-reset", func(c *gin.Context) {
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		cancelRequest = cancel
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}, s.sendPasswordResetEmail)
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
	if err := json.Unmarshal([]byte(after), &audit); err != nil || audit.CommandID == "" || audit.CommandID != queued {
		t.Fatalf("audit after_value=%s queued=%s err=%v", after, queued, err)
	}
}
