package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	mysqlmigrate "thing-connect/internal/store/mysql/migrate"
	"thing-connect/internal/testenv"
)

func TestRBACSavedGrantsNavigationAndRevocation(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../../tests/testdata/config.yaml")
	db := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { _ = db.Close() })
	if err := mysqlmigrate.MigrateAdmin(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := NewStore(db)
	if err := store.SeedDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	execID := func(query string, args ...any) int64 {
		t.Helper()
		result, err := db.Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	parentID := execID(`INSERT INTO admin_roles(code,name) VALUES (?, 'RBAC parent')`, "parent_"+suffix)
	childID := execID(`INSERT INTO admin_roles(code,name,parent_id) VALUES (?, 'RBAC child',?)`, "child_"+suffix, parentID)
	password := "RbacTest123!"
	hash, err := HashAdminPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	operatorEmail, memberEmail := "operator_"+suffix+"@example.test", "member_"+suffix+"@example.test"
	operatorID := execID(`INSERT INTO admin_users(email,password,nick_name,must_change_password) VALUES (?,?,'operator',0)`, operatorEmail, hash)
	memberID := execID(`INSERT INTO admin_users(email,password,nick_name,must_change_password) VALUES (?,?,'member',0)`, memberEmail, hash)
	t.Cleanup(func() {
		for _, id := range []int64{operatorID, memberID} {
			for _, table := range []string{"admin_sessions", "admin_login_log", "admin_user_roles", "admin_audit_log", "admin_mfa_factors", "admin_mfa_recovery_codes"} {
				_, _ = db.Exec("DELETE FROM "+table+" WHERE admin_user_id=?", id)
			}
			_, _ = db.Exec(`DELETE FROM admin_users WHERE id=?`, id)
		}
		for _, id := range []int64{childID, parentID} {
			_, _ = db.Exec(`DELETE FROM admin_role_permissions WHERE role_id=?`, id)
			_, _ = db.Exec(`DELETE FROM admin_role_menus WHERE role_id=?`, id)
			_, _ = db.Exec(`DELETE FROM admin_roles WHERE id=?`, id)
		}
	})
	execID(`INSERT INTO admin_user_roles(admin_user_id,role_id) SELECT ?,id FROM admin_roles WHERE code='super_admin'`, operatorID)
	execID(`INSERT INTO admin_user_roles(admin_user_id,role_id) VALUES (?,?)`, memberID, childID)
	var menuID, directoryID int64
	if err := db.Get(&menuID, `SELECT id FROM admin_menus WHERE menu_code='users'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&directoryID, `SELECT parent_id FROM admin_menus WHERE id=?`, menuID); err != nil {
		t.Fatal(err)
	}
	access, err := NewAccessController(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	// Login requires MFA; authenticated management requests do not ask for it again.
	auth, err := NewAuthService(store, nil, newMemoryMFAChallengeStore(), AuthConfig{JWTSecret: strings.Repeat("t", 32), MFAEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s := &HTTPServer{store: store, auth: auth, access: access}
	router := gin.New()
	protected := router.Group("/v1/admin", s.AuthMiddleware())
	protected.GET("/me/navigation", s.navigation)
	protected.PUT("/me/password", s.changeOwnPassword)
	protected.PUT("/roles/:id", s.Require("role.manage"), s.updateRole)
	protected.POST("/menus", s.Require("menu.manage"), s.createMenu)
	protected.PUT("/menus/:id", s.Require("menu.manage"), s.updateMenu)
	protected.PUT("/admin-users/:id/roles", s.Require("admin.write"), s.updateAdminRoles)
	protected.PUT("/roles/:id/menus", s.Require("role.manage"), s.updateRoleMenus)
	protected.PUT("/roles/:id/permissions", s.Require("role.manage"), s.updateRolePermissions)
	// Exercise the same permission middleware used by the users endpoint.
	protected.GET("/read-probe", s.Require("user.read"), func(c *gin.Context) { c.Status(200) })

	for _, id := range []int64{operatorID, memberID} {
		execID(`INSERT INTO admin_mfa_factors(admin_user_id,factor_type,secret_enc,status) VALUES (?,'totp','unused-recovery-test',1)`, id)
	}
	login := func(email string) AuthResult {
		t.Helper()
		result, err := auth.Login(ctx, email, password, LoginMeta{})
		if err != nil || result.Stage != "mfa_required" || result.AccessToken != "" {
			t.Fatalf("login did not require MFA: stage=%s err=%v", result.Stage, err)
		}
		id := memberID
		if email == operatorEmail {
			id = operatorID
		}
		recovery := fmt.Sprintf("recovery-%d", time.Now().UnixNano())
		execID(`INSERT INTO admin_mfa_recovery_codes(admin_user_id,code_hash) VALUES (?,?)`, id, TokenHash(normalizeRecoveryCode(recovery)))
		verified, err := auth.VerifyMFA(ctx, result.Challenge, "", recovery, LoginMeta{})
		if err != nil || verified.AccessToken == "" {
			t.Fatalf("MFA login failed: %v", err)
		}
		return verified
	}

	operator := login(operatorEmail)
	member := login(memberEmail)
	request := func(method, path, body, token string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/v1/admin"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	navigation := func(want bool) {
		t.Helper()
		response := request("GET", "/me/navigation", "", member.AccessToken, 200)
		var envelope struct {
			Data struct {
				Menus []Menu `json:"menus"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		found, parentFound := false, false
		for _, m := range envelope.Data.Menus {
			if m.ID == menuID {
				found = true
			}
			if m.ID == directoryID {
				parentFound = true
			}
		}
		if found != want || (want && !parentFound) {
			t.Fatalf("navigation page=%v parent=%v want page=%v", found, parentFound, want)
		}
	}
	setMenus := func(ids string) {
		t.Helper()
		request("PUT", fmt.Sprintf("/roles/%d/menus", parentID), `{"menu_ids":`+ids+`,"reason":"integration test"}`, operator.AccessToken, 200)
	}
	setPermission := func(grant bool) {
		t.Helper()
		permissions := `[]`
		if grant {
			permissions = `["user.read"]`
		}
		request("PUT", fmt.Sprintf("/roles/%d/permissions", parentID), `{"permissions":`+permissions+`,"reason":"integration test"}`, operator.AccessToken, 200)
	}

	request("PUT", fmt.Sprintf("/roles/%d", parentID), `{"name":"编辑后的角色","parent_id":0,"status":1,"reason":"integration test"}`, operator.AccessToken, 200)
	var roleName string
	if err := db.Get(&roleName, `SELECT name FROM admin_roles WHERE id=?`, parentID); err != nil {
		t.Fatal(err)
	}
	if roleName != "编辑后的角色" {
		t.Fatal("role edit not persisted")
	}
	request("PUT", fmt.Sprintf("/roles/%d", parentID), fmt.Sprintf(`{"name":"非法循环","parent_id":%d,"status":1,"reason":"integration test"}`, childID), operator.AccessToken, 400)
	// New top-level directories must be editable without a fictitious parent.
	created := request("POST", "/menus", `{"menu_code":"test_`+suffix+`","name":"测试目录","menu_type":1,"parent_id":0,"visible":1,"status":1,"reason":"integration test"}`, operator.AccessToken, 200)
	var menuResult struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &menuResult); err != nil {
		t.Fatal(err)
	}
	createdID := menuResult.Data.ID
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM admin_role_menus WHERE menu_id=?`, createdID)
		_, _ = db.Exec(`DELETE FROM admin_menus WHERE id=?`, createdID)
	})
	request("PUT", fmt.Sprintf("/menus/%d", createdID), `{"menu_code":"test_`+suffix+`","name":"编辑后的目录","menu_type":1,"parent_id":0,"sort_no":17,"visible":0,"status":1,"reason":"integration test"}`, operator.AccessToken, 200)

	request("PUT", fmt.Sprintf("/menus/%d", createdID), fmt.Sprintf(`{"menu_code":"test_%s","name":"非法循环","menu_type":1,"parent_id":%d,"visible":1,"status":1,"reason":"integration test"}`, suffix, createdID), operator.AccessToken, 400)
	var saved Menu
	if err := db.Get(&saved, `SELECT name,parent_id,sort_no,visible FROM admin_menus WHERE id=?`, createdID); err != nil {
		t.Fatal(err)
	}
	if saved.Name != "编辑后的目录" || saved.ParentID != 0 || saved.SortNo != 17 || saved.Visible != 0 {
		t.Fatal("menu edit not persisted")
	}
	// An administrator can hold multiple independent role assignments.
	request("PUT", fmt.Sprintf("/admin-users/%d/roles", memberID), fmt.Sprintf(`{"role_ids":[%d,%d],"reason":"integration test"}`, parentID, childID), operator.AccessToken, 200)
	request("GET", "/read-probe", "", member.AccessToken, http.StatusUnauthorized)
	var assigned []int64
	if err := db.Select(&assigned, `SELECT role_id FROM admin_user_roles WHERE admin_user_id=?`, memberID); err != nil {
		t.Fatal(err)
	}
	if len(assigned) != 2 {
		t.Fatalf("assigned roles=%v", assigned)
	}
	request("PUT", fmt.Sprintf("/admin-users/%d/roles", memberID), fmt.Sprintf(`{"role_ids":[%d],"reason":"integration test"}`, childID), operator.AccessToken, 200)
	member = login(memberEmail)
	navigation(false)
	request("GET", "/read-probe", "", member.AccessToken, http.StatusForbidden)
	request("PUT", fmt.Sprintf("/roles/%d/menus", parentID), `{"menu_ids":[],"reason":"unauthorized"}`, member.AccessToken, http.StatusForbidden)
	setMenus(fmt.Sprintf("[%d]", menuID))
	navigation(false) // A menu grant alone must never grant API access.
	request("GET", "/read-probe", "", member.AccessToken, http.StatusForbidden)
	setPermission(true)
	request("GET", "/read-probe", "", member.AccessToken, http.StatusUnauthorized)
	if _, err := auth.Refresh(ctx, member.RefreshToken); err == nil {
		t.Fatal("old refresh session survived role permission change")
	}
	member = login(memberEmail)
	navigation(true) // Inherited page grant includes its parent directory.
	request("GET", "/read-probe", "", member.AccessToken, 200)
	if err := store.SeedDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	s.access, err = NewAccessController(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	navigation(true)
	request("GET", "/read-probe", "", member.AccessToken, 200)
	setMenus("[]")
	navigation(false)
	request("GET", "/read-probe", "", member.AccessToken, 200) // Menu visibility is not an API permission.
	setMenus(fmt.Sprintf("[%d]", menuID))
	setPermission(false)
	request("GET", "/read-probe", "", member.AccessToken, http.StatusUnauthorized)
	member = login(memberEmail)
	navigation(false)
	request("GET", "/read-probe", "", member.AccessToken, http.StatusForbidden)
	if err := store.SeedDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	s.access, err = NewAccessController(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	navigation(false)
	request("GET", "/read-probe", "", member.AccessToken, http.StatusForbidden)
	// A disabled ancestor contributes neither permissions nor menu grants, even
	// when the child independently has the permission required by that page.
	execID(`INSERT INTO admin_role_permissions(role_id,permission_code) VALUES (?,'user.read')`, childID)
	execID(`UPDATE admin_roles SET status=0 WHERE id=?`, parentID)
	s.access, err = NewAccessController(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	request("GET", "/read-probe", "", member.AccessToken, 200)
	navigation(false)

	request("PUT", "/me/password", `{"current_password":"wrong","new_password":"ChangedTest456!"}`, member.AccessToken, 400)
	request("GET", "/read-probe", "", member.AccessToken, 200)
	request("PUT", "/me/password", `{"current_password":"`+password+`","new_password":"ChangedTest456!"}`, member.AccessToken, 200)
	request("GET", "/read-probe", "", member.AccessToken, http.StatusUnauthorized)
	if _, err := auth.Refresh(ctx, member.RefreshToken); err == nil {
		t.Fatal("password change preserved old refresh session")
	}
	if err := auth.VerifyPassword(ctx, memberID, password); err == nil {
		t.Fatal("old password still valid")
	}
	changedLogin, err := auth.Login(ctx, memberEmail, "ChangedTest456!", LoginMeta{})
	if err != nil || changedLogin.Stage != "mfa_required" {
		t.Fatalf("new password login must still require MFA: %v", err)
	}

}
