package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	"thing-connect/internal/apiresp"
)

var stableCode = regexp.MustCompile(`^[a-z][a-z0-9_.-]{1,63}$`)

type roleView struct {
	Role
	Permissions          []string `json:"permissions"`
	EffectivePermissions []string `json:"effective_permissions"`
}

func (s *HTTPServer) listRoles(c *gin.Context) {
	var roles []Role
	if err := s.store.db.SelectContext(c, &roles, `SELECT id,code,name,parent_id,sort_no,status,remark,created_at,updated_at FROM admin_roles ORDER BY sort_no,id`); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	result := make([]roleView, 0, len(roles))
	for _, role := range roles {
		var permissions []string
		_ = s.store.db.SelectContext(c, &permissions, `SELECT permission_code FROM admin_role_permissions WHERE role_id=? ORDER BY permission_code`, role.ID)
		effective := []string{}
		if role.Status == 1 {
			var err error
			effective, err = effectiveRolePermissions(c, s.store.db, role.ID)
			if err != nil {
				apiresp.Internal(c, err.Error())
				return
			}
		}
		result = append(result, roleView{Role: role, Permissions: permissions, EffectivePermissions: effective})
	}
	apiresp.OK(c, gin.H{"items": result, "registered_permissions": AllPermissions, "permission_definitions": PermissionDefinitions})
}

func effectiveRolePermissions(ctx *gin.Context, db *sqlx.DB, roleID int64) ([]string, error) {
	seenRoles := map[int64]bool{}
	seenPermissions := map[string]bool{}
	for roleID > 0 && !seenRoles[roleID] {
		seenRoles[roleID] = true
		var parentID int64
		if err := db.GetContext(ctx, &parentID, `SELECT parent_id FROM admin_roles WHERE id=? AND status=1`, roleID); errors.Is(err, sql.ErrNoRows) {
			break
		} else if err != nil {
			return nil, err
		}
		var permissions []string
		if err := db.SelectContext(ctx, &permissions, `SELECT permission_code FROM admin_role_permissions WHERE role_id=?`, roleID); err != nil {
			return nil, err
		}
		for _, permission := range permissions {
			seenPermissions[permission] = true
		}
		roleID = parentID
	}
	result := make([]string, 0, len(seenPermissions))
	for permission := range seenPermissions {
		result = append(result, permission)
	}
	sort.Strings(result)
	return result, nil
}

type roleRequest struct {
	Code            string `json:"code"`
	Name            string `json:"name" binding:"required"`
	ParentID        int64  `json:"parent_id"`
	SortNo          int    `json:"sort_no"`
	Status          *int8  `json:"status"`
	Remark          string `json:"remark"`
	Reason          string `json:"reason" binding:"required"`
	CurrentMFACode  string `json:"current_mfa_code"`
	CurrentRecovery string `json:"current_recovery_code"`
}

func (s *HTTPServer) createRole(c *gin.Context) {
	var request roleRequest
	if c.ShouldBindJSON(&request) != nil || !stableCode.MatchString(request.Code) || strings.TrimSpace(request.Name) == "" {
		apiresp.BadParam(c, "角色编码或名称无效")
		return
	}
	status := int8(1)
	if request.Status != nil {
		status = *request.Status
	}
	if status != 0 && status != 1 {
		apiresp.BadParam(c, "状态无效")
		return
	}
	identity, _ := identityFromContext(c)
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	if request.ParentID > 0 && !rowExists(c, tx, `SELECT COUNT(*) FROM admin_roles WHERE id=?`, request.ParentID) {
		apiresp.BadParam(c, "父角色不存在")
		return
	}
	result, err := tx.ExecContext(c, `INSERT INTO admin_roles (code,name,parent_id,sort_no,status,remark) VALUES (?,?,?,?,?,?)`, request.Code, strings.TrimSpace(request.Name), request.ParentID, request.SortNo, status, strings.TrimSpace(request.Remark))
	if err != nil {
		apiresp.BadParam(c, "角色编码已存在或数据无效")
		return
	}
	id, _ := result.LastInsertId()
	if err := s.auditTx(c, tx, identity, "role.create", "role", strconv.FormatInt(id, 10), request.Reason, "", marshalSafe(request)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit role")
		return
	}
	_ = s.access.Reload(c)
	apiresp.OK(c, gin.H{"id": id})
}

func (s *HTTPServer) updateRole(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request roleRequest
	if err != nil || c.ShouldBindJSON(&request) != nil || strings.TrimSpace(request.Name) == "" || id == request.ParentID {
		apiresp.BadParam(c, "角色数据无效")
		return
	}
	status := int8(1)
	if request.Status != nil {
		status = *request.Status
	}
	if status != 0 && status != 1 {
		apiresp.BadParam(c, "状态无效")
		return
	}
	if roleCreatesCycle(c, s.store.db, id, request.ParentID) {
		apiresp.BadParam(c, "父角色会形成继承环")
		return
	}
	identity, _ := identityFromContext(c)
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	var before Role
	if err := tx.GetContext(c, &before, `SELECT id,code,name,parent_id,sort_no,status,remark,created_at,updated_at FROM admin_roles WHERE id=? FOR UPDATE`, id); err != nil {
		c.JSON(http.StatusNotFound, apiresp.JSON{Code: 404, Msg: "角色不存在"})
		return
	}
	if before.Code == "super_admin" && status == 0 {
		apiresp.BadParam(c, "超级管理员角色不能停用")
		return
	}
	if !s.requireStepUp(c, identity.UserID, request.CurrentMFACode, request.CurrentRecovery) {
		return
	}
	if _, err := tx.ExecContext(c, `UPDATE admin_roles SET name=?,parent_id=?,sort_no=?,status=?,remark=? WHERE id=?`, strings.TrimSpace(request.Name), request.ParentID, request.SortNo, status, strings.TrimSpace(request.Remark), id); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	securityChanged := before.ParentID != request.ParentID || before.Status != status
	if securityChanged {
		if err := invalidateRoleMembers(c, tx, id, "role authorization updated"); err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
	}
	if err := s.auditTx(c, tx, identity, "role.update", "role", strconv.FormatInt(id, 10), request.Reason, marshalSafe(before), marshalSafe(request)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit role")
		return
	}
	_ = s.access.Reload(c)
	apiresp.OK(c, gin.H{"id": id})
}

type permissionRequest struct {
	Permissions     []string `json:"permissions"`
	Reason          string   `json:"reason" binding:"required"`
	CurrentMFACode  string   `json:"current_mfa_code"`
	CurrentRecovery string   `json:"current_recovery_code"`
}

func (s *HTTPServer) requireStepUp(c *gin.Context, userID int64, code, recoveryCode string) bool {
	if err := s.auth.VerifyStepUp(c, userID, code, recoveryCode); err == nil {
		return true
	}
	if strings.TrimSpace(code) == "" && strings.TrimSpace(recoveryCode) == "" {
		apiresp.BadParam(c, "此操作需要当前管理员的身份验证器验证码或恢复码")
	} else {
		apiresp.BadParam(c, "身份验证器验证码或恢复码无效，请重新输入")
	}
	return false
}

func (s *HTTPServer) updateRolePermissions(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request permissionRequest
	identity, _ := identityFromContext(c)
	if err != nil || c.ShouldBindJSON(&request) != nil {
		apiresp.BadParam(c, "权限请求无效")
		return
	}
	if !s.requireStepUp(c, identity.UserID, request.CurrentMFACode, request.CurrentRecovery) {
		return
	}
	permissions, valid := validatePermissions(request.Permissions)
	if !valid {
		apiresp.BadParam(c, "包含未注册权限码")
		return
	}
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	var roleCode string
	if err := tx.GetContext(c, &roleCode, `SELECT code FROM admin_roles WHERE id=? FOR UPDATE`, id); err != nil {
		apiresp.BadParam(c, "角色不存在")
		return
	}
	if roleCode == "super_admin" && !containsAllPermissions(permissions) {
		apiresp.BadParam(c, "超级管理员必须保留全部已注册权限")
		return
	}
	var before []string
	if err := tx.SelectContext(c, &before, `SELECT permission_code FROM admin_role_permissions WHERE role_id=? FOR UPDATE`, id); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	permissionsChanged := !sameStrings(before, permissions)
	if permissionsChanged {
		if _, err := tx.ExecContext(c, `DELETE FROM admin_role_permissions WHERE role_id=?`, id); err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
		for _, permission := range permissions {
			if _, err := tx.ExecContext(c, `INSERT INTO admin_role_permissions (role_id,permission_code) VALUES (?,?)`, id, permission); err != nil {
				apiresp.Internal(c, err.Error())
				return
			}
		}
		if err := invalidateRoleMembers(c, tx, id, "role permissions updated"); err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
	}
	if err := s.auditTx(c, tx, identity, "role.permissions.update", "role", strconv.FormatInt(id, 10), request.Reason, marshalSafe(before), marshalSafe(permissions)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit permissions")
		return
	}
	_ = s.access.Reload(c)
	apiresp.OK(c, gin.H{"id": id, "permissions": permissions})
}

type adminUserListItem struct {
	AdminUser
	Roles      string   `db:"roles" json:"-"`
	MFAEnabled int8     `db:"mfa_enabled" json:"mfa_enabled"`
	RoleCodes  []string `db:"-" json:"roles"`
}

func (s *HTTPServer) listAdminUsers(c *gin.Context) {
	page, size := pageParams(c)
	var total int
	_ = s.store.db.GetContext(c, &total, `SELECT COUNT(*) FROM admin_users`)
	var items []adminUserListItem
	err := s.store.db.SelectContext(c, &items, `SELECT a.id,a.email,a.password,a.nick_name,a.status,a.auth_revision,a.must_change_password,a.password_updated_at,a.last_login_ip,a.last_login_at,a.remark,a.created_at,a.updated_at,COALESCE(GROUP_CONCAT(DISTINCT r.code ORDER BY r.code),'') roles,IF(MAX(f.status)=1,1,0) mfa_enabled FROM admin_users a LEFT JOIN admin_user_roles ur ON ur.admin_user_id=a.id LEFT JOIN admin_roles r ON r.id=ur.role_id LEFT JOIN admin_mfa_factors f ON f.admin_user_id=a.id GROUP BY a.id ORDER BY a.id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	for index := range items {
		if items[index].Roles != "" {
			items[index].RoleCodes = strings.Split(items[index].Roles, ",")
		}
	}
	apiresp.OK(c, pageResult{Items: items, Page: page, PageSize: size, Total: total})
}

type adminUserRequest struct {
	Email              string  `json:"email" binding:"required,email"`
	NickName           string  `json:"nick_name" binding:"required"`
	Password           string  `json:"password"`
	Status             *int8   `json:"status"`
	MustChangePassword bool    `json:"must_change_password"`
	Remark             string  `json:"remark"`
	RoleIDs            []int64 `json:"role_ids"`
	Reason             string  `json:"reason" binding:"required"`
	CurrentMFACode     string  `json:"current_mfa_code"`
	CurrentRecovery    string  `json:"current_recovery_code"`
}

func (s *HTTPServer) createAdminUser(c *gin.Context) {
	var request adminUserRequest
	identity, _ := identityFromContext(c)
	if c.ShouldBindJSON(&request) != nil || strings.TrimSpace(request.NickName) == "" {
		apiresp.BadParam(c, "请填写有效的管理员邮箱、昵称和操作原因")
		return
	}
	if !s.requireStepUp(c, identity.UserID, request.CurrentMFACode, request.CurrentRecovery) {
		return
	}
	hash, err := HashAdminPassword(request.Password)
	if err != nil {
		apiresp.BadParam(c, AdminPasswordPolicyMessage)
		return
	}
	status := int8(1)
	if request.Status != nil {
		status = *request.Status
	}
	if status != 0 && status != 1 {
		apiresp.BadParam(c, "管理员状态无效")
		return
	}
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(c, `INSERT INTO admin_users (email,password,nick_name,status,must_change_password,password_updated_at,remark) VALUES (?,?,?,?,?,NOW(),?)`, strings.ToLower(strings.TrimSpace(request.Email)), hash, strings.TrimSpace(request.NickName), status, boolInt(request.MustChangePassword), strings.TrimSpace(request.Remark))
	if err != nil {
		apiresp.BadParam(c, "管理员邮箱已存在或数据无效")
		return
	}
	id, _ := result.LastInsertId()
	if err := replaceAdminRoles(c, tx, id, request.RoleIDs); err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	if err := s.auditTx(c, tx, identity, "admin.create", "admin_user", strconv.FormatInt(id, 10), request.Reason, "", marshalSafe(map[string]any{"email": request.Email, "nick_name": request.NickName, "roles": request.RoleIDs})); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit administrator")
		return
	}
	_ = s.access.Reload(c)
	apiresp.OK(c, gin.H{"id": id})
}

func (s *HTTPServer) updateAdminUser(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request adminUserRequest
	identity, _ := identityFromContext(c)
	if err != nil {
		apiresp.BadParam(c, "管理员 ID 无效")
		return
	}
	if c.ShouldBindJSON(&request) != nil || strings.TrimSpace(request.NickName) == "" {
		apiresp.BadParam(c, "请填写有效的管理员邮箱、昵称和操作原因")
		return
	}
	if id == identity.UserID && request.Status != nil && *request.Status == 0 {
		apiresp.BadParam(c, "不能禁用当前登录的管理员账号")
		return
	}
	if !s.requireStepUp(c, identity.UserID, request.CurrentMFACode, request.CurrentRecovery) {
		return
	}
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	var before AdminUser
	if err := tx.GetContext(c, &before, `SELECT `+adminUserColumns+` FROM admin_users WHERE id=? FOR UPDATE`, id); err != nil {
		c.JSON(404, apiresp.JSON{Code: 404, Msg: "管理员不存在"})
		return
	}
	var beforeRoleIDs []int64
	_ = tx.SelectContext(c, &beforeRoleIDs, `SELECT role_id FROM admin_user_roles WHERE admin_user_id=? FOR UPDATE`, id)
	status := before.Status
	if request.Status != nil {
		status = *request.Status
	}
	if status != 0 && status != 1 {
		apiresp.BadParam(c, "管理员状态无效")
		return
	}
	newRoleIDs := beforeRoleIDs
	if request.RoleIDs != nil {
		newRoleIDs = request.RoleIDs
	}
	if err := ensureSuperAdminContinuity(c, tx, id, before.Status, status, beforeRoleIDs, newRoleIDs); err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	rolesChanged := request.RoleIDs != nil && !sameIDs(beforeRoleIDs, request.RoleIDs)
	if rolesChanged {
		if err := replaceAdminRoles(c, tx, id, request.RoleIDs); err != nil {
			apiresp.BadParam(c, err.Error())
			return
		}
	}
	password, changed := before.Password, false
	if request.Password != "" {
		password, err = HashAdminPassword(request.Password)
		if err != nil {
			apiresp.BadParam(c, AdminPasswordPolicyMessage)
			return
		}
		changed = true
	}
	email := strings.ToLower(strings.TrimSpace(request.Email))
	securityChanged := email != before.Email || status != before.Status || changed || int8(boolInt(request.MustChangePassword)) != before.MustChangePassword || rolesChanged
	_, err = tx.ExecContext(c, `UPDATE admin_users SET email=?,nick_name=?,status=?,password=?,must_change_password=?,password_updated_at=IF(?,NOW(),password_updated_at),remark=?,auth_revision=auth_revision+? WHERE id=?`, email, strings.TrimSpace(request.NickName), status, password, boolInt(request.MustChangePassword), boolInt(changed), strings.TrimSpace(request.Remark), boolInt(securityChanged), id)
	if err != nil {
		apiresp.BadParam(c, "邮箱已存在或数据无效")
		return
	}
	if securityChanged {
		if _, err := tx.ExecContext(c, `UPDATE admin_sessions SET revoked_at=COALESCE(revoked_at,NOW()),revoked_reason='administrator authorization updated' WHERE admin_user_id=?`, id); err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
	}
	after := map[string]any{"email": request.Email, "nick_name": request.NickName, "status": status, "remark": request.Remark}
	if rolesChanged {
		after["role_ids"] = request.RoleIDs
	}
	beforeAudit := map[string]any{"user": before}
	if rolesChanged {
		beforeAudit["role_ids"] = beforeRoleIDs
	}
	if err := s.auditTx(c, tx, identity, "admin.update", "admin_user", strconv.FormatInt(id, 10), request.Reason, marshalSafe(beforeAudit), marshalSafe(after)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit administrator")
		return
	}
	if rolesChanged {
		_ = s.access.Reload(c)
	}
	apiresp.OK(c, gin.H{"id": id})
}

type adminRolesRequest struct {
	RoleIDs         []int64 `json:"role_ids"`
	Reason          string  `json:"reason" binding:"required"`
	CurrentMFACode  string  `json:"current_mfa_code"`
	CurrentRecovery string  `json:"current_recovery_code"`
}

func (s *HTTPServer) updateAdminRoles(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request adminRolesRequest
	identity, _ := identityFromContext(c)
	if err != nil || c.ShouldBindJSON(&request) != nil {
		apiresp.BadParam(c, "角色授权请求无效")
		return
	}
	if !s.requireStepUp(c, identity.UserID, request.CurrentMFACode, request.CurrentRecovery) {
		return
	}
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	var before []int64
	_ = tx.SelectContext(c, &before, `SELECT role_id FROM admin_user_roles WHERE admin_user_id=? FOR UPDATE`, id)
	var targetStatus int8
	if err := tx.GetContext(c, &targetStatus, `SELECT status FROM admin_users WHERE id=? FOR UPDATE`, id); err != nil {
		apiresp.BadParam(c, "管理员不存在")
		return
	}
	if err := ensureSuperAdminContinuity(c, tx, id, targetStatus, targetStatus, before, request.RoleIDs); err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	rolesChanged := !sameIDs(before, request.RoleIDs)
	if rolesChanged {
		if err := replaceAdminRoles(c, tx, id, request.RoleIDs); err != nil {
			apiresp.BadParam(c, err.Error())
			return
		}
		_, _ = tx.ExecContext(c, `UPDATE admin_users SET auth_revision=auth_revision+1 WHERE id=?`, id)
		_, _ = tx.ExecContext(c, `UPDATE admin_sessions SET revoked_at=COALESCE(revoked_at,NOW()),revoked_reason='roles updated' WHERE admin_user_id=?`, id)
	}
	if err := s.auditTx(c, tx, identity, "admin.roles.update", "admin_user", strconv.FormatInt(id, 10), request.Reason, marshalSafe(before), marshalSafe(request.RoleIDs)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit roles")
		return
	}
	if rolesChanged {
		_ = s.access.Reload(c)
	}
	apiresp.OK(c, gin.H{"id": id, "role_ids": request.RoleIDs})
}

func (s *HTTPServer) revokeAdminSessions(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request struct {
		Reason          string `json:"reason" binding:"required"`
		CurrentMFACode  string `json:"current_mfa_code"`
		CurrentRecovery string `json:"current_recovery_code"`
	}
	identity, _ := identityFromContext(c)
	if err != nil || c.ShouldBindJSON(&request) != nil {
		apiresp.BadParam(c, "撤销请求无效")
		return
	}
	if !s.requireStepUp(c, identity.UserID, request.CurrentMFACode, request.CurrentRecovery) {
		return
	}
	if err := s.store.RevokeSessions(c, id, "revoked by administrator"); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	_, _ = s.store.db.ExecContext(c, `UPDATE admin_users SET auth_revision=auth_revision+1 WHERE id=?`, id)
	_ = s.store.Audit(c, requestAudit(c, identity, "admin.sessions.revoke", "admin_user", strconv.FormatInt(id, 10), request.Reason, "", `{"revoked":true}`))
	apiresp.OK(c, gin.H{"revoked": true})
}

func (s *HTTPServer) listAdminSessions(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		apiresp.BadParam(c, "管理员 ID 无效")
		return
	}
	var sessions []Session
	if err := s.store.db.SelectContext(c, &sessions, `SELECT id,admin_user_id,family_id,token_hash,replaced_by_id,expires_at,revoked_at,revoked_reason,created_at,updated_at FROM admin_sessions WHERE admin_user_id=? ORDER BY id DESC LIMIT 100`, id); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"items": sessions})
}

func (s *HTTPServer) resetAdminMFA(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request struct {
		Reason          string `json:"reason" binding:"required"`
		CurrentMFACode  string `json:"current_mfa_code"`
		CurrentRecovery string `json:"current_recovery_code"`
	}
	identity, _ := identityFromContext(c)
	if err != nil || c.ShouldBindJSON(&request) != nil {
		apiresp.BadParam(c, "双重验证重置请求无效")
		return
	}
	if id == identity.UserID {
		apiresp.BadParam(c, "不能在管理员列表中重置当前登录账号的双重验证")
		return
	}
	if !s.requireStepUp(c, identity.UserID, request.CurrentMFACode, request.CurrentRecovery) {
		return
	}
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	_, _ = tx.ExecContext(c, `DELETE FROM admin_mfa_recovery_codes WHERE admin_user_id=?`, id)
	_, _ = tx.ExecContext(c, `DELETE FROM admin_mfa_factors WHERE admin_user_id=?`, id)
	_, _ = tx.ExecContext(c, `UPDATE admin_users SET auth_revision=auth_revision+1 WHERE id=?`, id)
	_, _ = tx.ExecContext(c, `UPDATE admin_sessions SET revoked_at=COALESCE(revoked_at,NOW()),revoked_reason='MFA reset' WHERE admin_user_id=?`, id)
	if err := s.auditTx(c, tx, identity, "admin.mfa.reset", "admin_user", strconv.FormatInt(id, 10), request.Reason, `{"configured":true}`, `{"configured":false}`); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit MFA reset")
		return
	}
	apiresp.OK(c, gin.H{"reset": true})
}

func (s *HTTPServer) regenerateRecoveryCodes(c *gin.Context) {
	identity, _ := identityFromContext(c)
	var request struct {
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
		Reason       string `json:"reason" binding:"required"`
	}
	if c.ShouldBindJSON(&request) != nil {
		apiresp.BadParam(c, "恢复码生成请求无效")
		return
	}
	if !s.requireStepUp(c, identity.UserID, request.Code, request.RecoveryCode) {
		return
	}
	codes, err := s.auth.RegenerateRecoveryCodes(c, identity.UserID)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	_ = s.store.Audit(c, requestAudit(c, identity, "admin.mfa.recovery.regenerate", "admin_user", strconv.FormatInt(identity.UserID, 10), request.Reason, `{"configured":true}`, `{"configured":true}`))
	apiresp.OK(c, gin.H{"recovery_codes": codes})
}

func (s *HTTPServer) listMenus(c *gin.Context) {
	var items []Menu
	if err := s.store.db.SelectContext(c, &items, `SELECT id,parent_id,menu_code,name,icon,path,permission_code,menu_type,sort_no,visible,status,created_at,updated_at FROM admin_menus ORDER BY parent_id,sort_no,id`); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"items": items})
}

type menuRequest struct {
	ParentID       int64  `json:"parent_id"`
	MenuCode       string `json:"menu_code"`
	Name           string `json:"name" binding:"required"`
	Icon           string `json:"icon"`
	Path           string `json:"path"`
	PermissionCode string `json:"permission_code"`
	MenuType       int8   `json:"menu_type"`
	SortNo         int    `json:"sort_no"`
	Visible        int8   `json:"visible"`
	Status         int8   `json:"status"`
	Reason         string `json:"reason" binding:"required"`
}

func (s *HTTPServer) createMenu(c *gin.Context) { s.saveMenu(c, 0) }
func (s *HTTPServer) updateMenu(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		apiresp.BadParam(c, "菜单 ID 无效")
		return
	}
	s.saveMenu(c, id)
}
func (s *HTTPServer) saveMenu(c *gin.Context, id int64) {
	var request menuRequest
	if c.ShouldBindJSON(&request) != nil || !stableCode.MatchString(request.MenuCode) || !validMenuRequest(request) || id == request.ParentID {
		apiresp.BadParam(c, "菜单数据无效或路径未在前端注册")
		return
	}
	identity, _ := identityFromContext(c)
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	if id == 0 {
		result, err := tx.ExecContext(c, `INSERT INTO admin_menus (parent_id,menu_code,name,icon,path,permission_code,menu_type,sort_no,visible,status) VALUES (?,?,?,?,?,?,?,?,?,?)`, request.ParentID, request.MenuCode, request.Name, request.Icon, request.Path, request.PermissionCode, request.MenuType, request.SortNo, request.Visible, request.Status)
		if err != nil {
			apiresp.BadParam(c, "菜单编码已存在或数据无效")
			return
		}
		id, _ = result.LastInsertId()
	} else {
		if menuCreatesCycle(c, s.store.db, id, request.ParentID) {
			apiresp.BadParam(c, "父菜单会形成层级环")
			return
		}
		if _, err := tx.ExecContext(c, `UPDATE admin_menus SET parent_id=?,name=?,icon=?,path=?,permission_code=?,menu_type=?,sort_no=?,visible=?,status=? WHERE id=?`, request.ParentID, request.Name, request.Icon, request.Path, request.PermissionCode, request.MenuType, request.SortNo, request.Visible, request.Status, id); err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
	}
	if err := s.auditTx(c, tx, identity, "menu.write", "menu", strconv.FormatInt(id, 10), request.Reason, "", marshalSafe(request)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit menu")
		return
	}
	apiresp.OK(c, gin.H{"id": id})
}

func (s *HTTPServer) roleMenus(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		apiresp.BadParam(c, "角色 ID 无效")
		return
	}
	var ids []int64
	if err := s.store.db.SelectContext(c, &ids, `SELECT menu_id FROM admin_role_menus WHERE role_id=? ORDER BY menu_id`, id); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"menu_ids": ids})
}

func (s *HTTPServer) updateRoleMenus(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request struct {
		MenuIDs []int64 `json:"menu_ids"`
		Reason  string  `json:"reason" binding:"required"`
	}
	if err != nil || c.ShouldBindJSON(&request) != nil {
		apiresp.BadParam(c, "菜单授权请求无效")
		return
	}
	identity, _ := identityFromContext(c)
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	var roleCode string
	if err := tx.GetContext(c, &roleCode, `SELECT code FROM admin_roles WHERE id=? FOR UPDATE`, id); err != nil {
		apiresp.BadParam(c, "角色不存在")
		return
	}
	if roleCode == "super_admin" {
		apiresp.BadParam(c, "超级管理员菜单授权固定为全部菜单")
		return
	}
	var before []int64
	_ = tx.SelectContext(c, &before, `SELECT menu_id FROM admin_role_menus WHERE role_id=? FOR UPDATE`, id)
	_, _ = tx.ExecContext(c, `DELETE FROM admin_role_menus WHERE role_id=?`, id)
	for _, menuID := range uniquePositiveIDs(request.MenuIDs) {
		result, err := tx.ExecContext(c, `INSERT INTO admin_role_menus (role_id,menu_id) SELECT ?,id FROM admin_menus WHERE id=?`, id, menuID)
		if err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
		if n, _ := result.RowsAffected(); n != 1 {
			apiresp.BadParam(c, "包含不存在的菜单")
			return
		}
	}
	if err := s.auditTx(c, tx, identity, "role.menus.update", "role", strconv.FormatInt(id, 10), request.Reason, marshalSafe(before), marshalSafe(request.MenuIDs)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit role menus")
		return
	}
	apiresp.OK(c, gin.H{"menu_ids": request.MenuIDs})
}

func validatePermissions(values []string) ([]string, bool) {
	allowed := map[string]bool{}
	for _, v := range AllPermissions {
		allowed[v] = true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if !allowed[v] {
			return nil, false
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, true
}

func containsAllPermissions(values []string) bool {
	if len(values) != len(AllPermissions) {
		return false
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		seen[value] = true
	}
	for _, permission := range AllPermissions {
		if !seen[permission] {
			return false
		}
	}
	return true
}
func replaceAdminRoles(c *gin.Context, tx *sqlx.Tx, userID int64, roleIDs []int64) error {
	roleIDs, err := requiredAdminRoleIDs(roleIDs)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(c, `DELETE FROM admin_user_roles WHERE admin_user_id=?`, userID); err != nil {
		return err
	}
	for _, id := range roleIDs {
		result, err := tx.ExecContext(c, `INSERT INTO admin_user_roles (admin_user_id,role_id) SELECT ?,id FROM admin_roles WHERE id=? AND status=1`, userID, id)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errors.New("包含不存在或停用的角色")
		}
	}
	return nil
}

func requiredAdminRoleIDs(values []int64) ([]int64, error) {
	roleIDs := uniquePositiveIDs(values)
	if len(roleIDs) == 0 {
		return nil, errors.New("至少选择一个启用的角色")
	}
	return roleIDs, nil
}

func uniquePositiveIDs(values []int64) []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	for _, v := range values {
		if v > 0 && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func ensureSuperAdminContinuity(c *gin.Context, tx *sqlx.Tx, userID int64, oldStatus, newStatus int8, oldRoles, newRoles []int64) error {
	var superRoleID int64
	if err := tx.GetContext(c, &superRoleID, `SELECT id FROM admin_roles WHERE code='super_admin'`); err != nil {
		return errors.New("超级管理员角色不存在")
	}
	if oldStatus != 1 || !containsID(oldRoles, superRoleID) || newStatus == 1 && containsID(newRoles, superRoleID) {
		return nil
	}
	var others int
	if err := tx.GetContext(c, &others, `SELECT COUNT(DISTINCT a.id) FROM admin_users a JOIN admin_user_roles ur ON ur.admin_user_id=a.id WHERE a.status=1 AND ur.role_id=? AND a.id<>?`, superRoleID, userID); err != nil {
		return err
	}
	if others == 0 {
		return errors.New("至少保留一个启用的超级管理员")
	}
	return nil
}

func containsID(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func invalidateRoleMembers(c *gin.Context, tx *sqlx.Tx, roleID int64, reason string) error {
	type roleParent struct {
		ID       int64 `db:"id"`
		ParentID int64 `db:"parent_id"`
	}
	var roles []roleParent
	if err := tx.SelectContext(c, &roles, `SELECT id,parent_id FROM admin_roles`); err != nil {
		return err
	}
	affected := map[int64]bool{roleID: true}
	for changed := true; changed; {
		changed = false
		for _, role := range roles {
			if affected[role.ParentID] && !affected[role.ID] {
				affected[role.ID] = true
				changed = true
			}
		}
	}
	roleIDs := make([]int64, 0, len(affected))
	for id := range affected {
		roleIDs = append(roleIDs, id)
	}
	sort.Slice(roleIDs, func(i, j int) bool { return roleIDs[i] < roleIDs[j] })
	userQuery, args, err := sqlx.In(`UPDATE admin_users a JOIN admin_user_roles ur ON ur.admin_user_id=a.id SET a.auth_revision=a.auth_revision+1 WHERE ur.role_id IN (?)`, roleIDs)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(c, tx.Rebind(userQuery), args...); err != nil {
		return err
	}
	sessionQuery, args, err := sqlx.In(`UPDATE admin_sessions s JOIN admin_user_roles ur ON ur.admin_user_id=s.admin_user_id SET s.revoked_at=COALESCE(s.revoked_at,NOW()),s.revoked_reason=? WHERE ur.role_id IN (?)`, reason, roleIDs)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(c, tx.Rebind(sessionQuery), args...)
	return err
}

func sameIDs(left, right []int64) bool {
	left = uniquePositiveIDs(left)
	right = uniquePositiveIDs(right)
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })
	sort.Slice(right, func(i, j int) bool { return right[i] < right[j] })
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameStrings(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
func rowExists(c *gin.Context, tx *sqlx.Tx, query string, args ...any) bool {
	var count int
	return tx.GetContext(c, &count, query, args...) == nil && count > 0
}
func roleCreatesCycle(c *gin.Context, db *sqlx.DB, id, parent int64) bool {
	for parent > 0 {
		if parent == id {
			return true
		}
		var next int64
		if db.GetContext(c, &next, `SELECT parent_id FROM admin_roles WHERE id=?`, parent) != nil {
			return true
		}
		parent = next
	}
	return false
}
func menuCreatesCycle(c *gin.Context, db *sqlx.DB, id, parent int64) bool {
	for parent > 0 {
		if parent == id {
			return true
		}
		var next int64
		if db.GetContext(c, &next, `SELECT parent_id FROM admin_menus WHERE id=?`, parent) != nil {
			return true
		}
		parent = next
	}
	return false
}
func validMenuRequest(r menuRequest) bool {
	if (r.MenuType != 1 && r.MenuType != 2) || (r.Visible != 0 && r.Visible != 1) || (r.Status != 0 && r.Status != 1) {
		return false
	}
	if r.PermissionCode != "" {
		if _, ok := validatePermissions([]string{r.PermissionCode}); !ok {
			return false
		}
	}
	if r.MenuType == 1 {
		return r.Path == ""
	}
	for _, menu := range defaultMenus {
		if menu.Path == r.Path {
			return true
		}
	}
	return false
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
