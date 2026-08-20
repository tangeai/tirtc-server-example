package admin

import "time"

type AdminUser struct {
	ID                 int64      `db:"id" json:"id"`
	Email              string     `db:"email" json:"email"`
	Password           string     `db:"password" json:"-"`
	NickName           string     `db:"nick_name" json:"nick_name"`
	Status             int8       `db:"status" json:"status"`
	AuthRevision       int64      `db:"auth_revision" json:"auth_revision"`
	MustChangePassword int8       `db:"must_change_password" json:"must_change_password"`
	PasswordUpdatedAt  *time.Time `db:"password_updated_at" json:"password_updated_at,omitempty"`
	LastLoginIP        string     `db:"last_login_ip" json:"last_login_ip"`
	LastLoginAt        *time.Time `db:"last_login_at" json:"last_login_at,omitempty"`
	Remark             string     `db:"remark" json:"remark"`
	CreatedAt          time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time  `db:"updated_at" json:"updated_at"`
}

type Role struct {
	ID        int64     `db:"id" json:"id"`
	Code      string    `db:"code" json:"code"`
	Name      string    `db:"name" json:"name"`
	ParentID  int64     `db:"parent_id" json:"parent_id"`
	SortNo    int       `db:"sort_no" json:"sort_no"`
	Status    int8      `db:"status" json:"status"`
	Remark    string    `db:"remark" json:"remark"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

type Menu struct {
	ID             int64     `db:"id" json:"id"`
	ParentID       int64     `db:"parent_id" json:"parent_id"`
	MenuCode       string    `db:"menu_code" json:"menu_code"`
	Name           string    `db:"name" json:"name"`
	Icon           string    `db:"icon" json:"icon"`
	Path           string    `db:"path" json:"path"`
	PermissionCode string    `db:"permission_code" json:"permission_code"`
	MenuType       int8      `db:"menu_type" json:"menu_type"`
	SortNo         int       `db:"sort_no" json:"sort_no"`
	Visible        int8      `db:"visible" json:"visible"`
	Status         int8      `db:"status" json:"status"`
	CreatedAt      time.Time `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time `db:"updated_at" json:"updated_at"`
}

type MFAFactor struct {
	ID           int64      `db:"id"`
	AdminUserID  int64      `db:"admin_user_id"`
	FactorType   string     `db:"factor_type"`
	SecretEnc    string     `db:"secret_enc"`
	Status       int8       `db:"status"`
	LastUsedStep int64      `db:"last_used_step"`
	ConfirmedAt  *time.Time `db:"confirmed_at"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
}

type Session struct {
	ID            int64      `db:"id" json:"id"`
	AdminUserID   int64      `db:"admin_user_id" json:"admin_user_id"`
	FamilyID      string     `db:"family_id" json:"family_id"`
	TokenHash     string     `db:"token_hash" json:"-"`
	ReplacedByID  int64      `db:"replaced_by_id" json:"-"`
	ExpiresAt     time.Time  `db:"expires_at" json:"expires_at"`
	RevokedAt     *time.Time `db:"revoked_at" json:"revoked_at,omitempty"`
	RevokedReason string     `db:"revoked_reason" json:"revoked_reason"`
	CreatedAt     time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at" json:"updated_at"`
}

type AuditEvent struct {
	AdminUserID int64
	RoleCodes   string
	RequestID   string
	Method      string
	Path        string
	HTTPStatus  int
	LatencyMS   int64
	Action      string
	Resource    string
	ResourceID  string
	Reason      string
	Before      string
	After       string
	ClientIP    string
	UserAgent   string
	Success     bool
	Error       string
}
