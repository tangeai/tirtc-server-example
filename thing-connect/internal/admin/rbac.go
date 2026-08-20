package admin

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	casbin "github.com/casbin/casbin/v3"
	casbinmodel "github.com/casbin/casbin/v3/model"
	"github.com/jmoiron/sqlx"
)

const rbacModel = `[request_definition]
r = sub, perm

[policy_definition]
p = sub, perm

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub) && r.perm == p.perm
`

type AccessController struct {
	db *sqlx.DB
	mu sync.RWMutex
	e  *casbin.Enforcer
}

func NewAccessController(ctx context.Context, db *sqlx.DB) (*AccessController, error) {
	a := &AccessController{db: db}
	if err := a.Reload(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *AccessController) Reload(ctx context.Context) error {
	m, err := casbinmodel.NewModelFromString(rbacModel)
	if err != nil {
		return fmt.Errorf("parse RBAC model: %w", err)
	}
	e, err := casbin.NewEnforcer(m)
	if err != nil {
		return fmt.Errorf("create RBAC enforcer: %w", err)
	}
	type permissionRow struct {
		RoleCode   string `db:"role_code"`
		Permission string `db:"permission_code"`
	}
	var permissions []permissionRow
	if err := a.db.SelectContext(ctx, &permissions, `SELECT r.code AS role_code,p.permission_code FROM admin_roles r JOIN admin_role_permissions p ON p.role_id=r.id WHERE r.status=1`); err != nil {
		return fmt.Errorf("load role permissions: %w", err)
	}
	for _, row := range permissions {
		if _, err := e.AddPolicy(row.RoleCode, row.Permission); err != nil {
			return err
		}
	}
	type membershipRow struct {
		UserID   int64  `db:"admin_user_id"`
		RoleCode string `db:"role_code"`
	}
	var memberships []membershipRow
	if err := a.db.SelectContext(ctx, &memberships, `SELECT ur.admin_user_id,r.code AS role_code FROM admin_user_roles ur JOIN admin_roles r ON r.id=ur.role_id WHERE r.status=1`); err != nil {
		return fmt.Errorf("load admin roles: %w", err)
	}
	for _, row := range memberships {
		if _, err := e.AddGroupingPolicy(subjectForUser(row.UserID), row.RoleCode); err != nil {
			return err
		}
	}
	type inheritanceRow struct {
		Child  string `db:"child_code"`
		Parent string `db:"parent_code"`
	}
	var inheritance []inheritanceRow
	if err := a.db.SelectContext(ctx, &inheritance, `SELECT child.code AS child_code,parent.code AS parent_code FROM admin_roles child JOIN admin_roles parent ON parent.id=child.parent_id WHERE child.status=1 AND parent.status=1`); err != nil {
		return fmt.Errorf("load role inheritance: %w", err)
	}
	for _, row := range inheritance {
		if _, err := e.AddGroupingPolicy(row.Child, row.Parent); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.e = e
	a.mu.Unlock()
	return nil
}

func (a *AccessController) Enforce(userID int64, permission string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.e == nil {
		return false
	}
	ok, err := a.e.Enforce(subjectForUser(userID), permission)
	return err == nil && ok
}

func (a *AccessController) Permissions(userID int64) []string {
	permissions := make([]string, 0, len(AllPermissions))
	for _, permission := range AllPermissions {
		if a.Enforce(userID, permission) {
			permissions = append(permissions, permission)
		}
	}
	return permissions
}

func subjectForUser(userID int64) string { return "user:" + strconv.FormatInt(userID, 10) }
