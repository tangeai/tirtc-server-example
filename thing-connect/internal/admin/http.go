package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/logging"
	smtpmailer "thing-connect/internal/mailer/smtp"
	"thing-connect/internal/servicestatus"
)

const identityKey = "admin_identity"

type HTTPServer struct {
	store            *Store
	auth             *AuthService
	access           *AccessController
	configs          *ConfigService
	statuses         *servicestatus.Aggregator
	redis            *redis.Client
	jobs             *JobService
	devices          *DeviceService
	cookieSecure     bool
	policyMu         sync.RWMutex
	loginWindow      time.Duration
	loginMaxAttempts int64
	mfaWindow        time.Duration
	mfaMaxAttempts   int64
}

func NewHTTPServer(store *Store, auth *AuthService, access *AccessController, configs *ConfigService, statuses *servicestatus.Aggregator, redisClient *redis.Client, jobs *JobService, devices *DeviceService, cookieSecure bool) *HTTPServer {
	return &HTTPServer{store: store, auth: auth, access: access, configs: configs, statuses: statuses, redis: redisClient, jobs: jobs, devices: devices, cookieSecure: cookieSecure, loginWindow: 15 * time.Minute, loginMaxAttempts: 5, mfaWindow: 5 * time.Minute, mfaMaxAttempts: 5}
}

func (s *HTTPServer) SetAuthRatePolicy(loginWindow time.Duration, loginMax int64, mfaWindow time.Duration, mfaMax int64) {
	if loginWindow <= 0 || loginMax <= 0 || mfaWindow <= 0 || mfaMax <= 0 {
		return
	}
	s.policyMu.Lock()
	s.loginWindow, s.loginMaxAttempts, s.mfaWindow, s.mfaMaxAttempts = loginWindow, loginMax, mfaWindow, mfaMax
	s.policyMu.Unlock()
}

func (s *HTTPServer) Register(r *gin.Engine) {
	v1 := r.Group("/v1/admin")
	auth := v1.Group("/auth")
	auth.POST("/login", s.login)
	auth.POST("/mfa/verify", s.verifyMFA)
	auth.POST("/refresh", s.refresh)
	auth.POST("/logout", s.logout)

	// Enrollment only accepts the one-purpose setup token issued during login.
	v1.POST("/me/mfa/totp/enroll", s.enrollTOTP)
	v1.POST("/me/mfa/totp/confirm", s.confirmTOTP)

	protected := v1.Group("")
	protected.Use(s.AuthMiddleware())
	protected.GET("/me", s.me)
	protected.PUT("/me/password", s.changeOwnPassword)
	protected.GET("/permissions", s.permissions)
	protected.GET("/me/navigation", s.navigation)
	protected.GET("/services/status", s.Require("dashboard.read"), s.serviceStatus)
	protected.GET("/services/:service/status", s.Require("service.status.read"), s.singleServiceStatus)
	protected.GET("/config-definitions", s.RequireConfigRead(), s.configDefinitions)
	protected.GET("/configs", s.RequireConfigRead(), s.listConfigs)
	protected.GET("/configs/:namespace/:config_key", s.RequireConfigRead(), s.getConfig)
	protected.POST("/configs/:namespace/:config_key/validate", s.RequireConfigWrite(), s.validateConfig)
	protected.POST("/configs/:namespace/:config_key/test", s.RequireConfigWrite(), s.testConfig)
	protected.PUT("/configs/:namespace/:config_key", s.RequireConfigWrite(), s.putConfig)
	protected.GET("/users", s.Require("user.read"), s.listUsers)
	protected.GET("/users/:id", s.Require("user.read"), s.getUser)
	protected.PUT("/users/:id/status", s.Require("user.status.write"), s.updateUserStatus)
	protected.PUT("/users/:id/bind-quota", s.Require("user.quota.write"), s.updateUserQuota)
	protected.POST("/users/:id/password-reset-email", s.Require("user.password_reset"), s.sendPasswordResetEmail)
	protected.GET("/devices", s.Require("device.read"), s.listDevices)
	protected.GET("/device-pool", s.Require("device.read"), s.listDevicePool)
	protected.GET("/devices/:device_id", s.Require("device.read"), s.getDevice)
	protected.GET("/devices/:device_id/bind-logs", s.Require("device.read"), s.deviceBindLogs)
	protected.POST("/devices/:device_id/force-unbind", s.Require("device.unbind"), s.forceUnbind)
	protected.POST("/me/mfa/recovery-codes/regenerate", s.regenerateRecoveryCodes)
	protected.GET("/roles", s.Require("role.read"), s.listRoles)
	protected.POST("/roles", s.Require("role.manage"), s.createRole)
	protected.PUT("/roles/:id", s.Require("role.manage"), s.updateRole)
	protected.PUT("/roles/:id/permissions", s.Require("role.manage"), s.updateRolePermissions)
	protected.GET("/roles/:id/menus", s.Require("role.read"), s.roleMenus)
	protected.PUT("/roles/:id/menus", s.Require("role.manage"), s.updateRoleMenus)
	protected.GET("/admin-users", s.Require("admin.read"), s.listAdminUsers)
	protected.POST("/admin-users", s.Require("admin.write"), s.createAdminUser)
	protected.PUT("/admin-users/:id", s.Require("admin.write"), s.updateAdminUser)
	protected.PUT("/admin-users/:id/roles", s.Require("admin.write"), s.updateAdminRoles)
	protected.GET("/admin-users/:id/sessions", s.Require("admin.read"), s.listAdminSessions)
	protected.POST("/admin-users/:id/sessions/revoke", s.Require("admin.session.revoke"), s.revokeAdminSessions)
	protected.POST("/admin-users/:id/mfa/reset", s.Require("security.mfa.write"), s.resetAdminMFA)
	protected.GET("/menus", s.Require("role.read"), s.listMenus)
	protected.POST("/menus", s.Require("menu.manage"), s.createMenu)
	protected.PUT("/menus/:id", s.Require("menu.manage"), s.updateMenu)
	protected.GET("/dict-types", s.Require("dictionary.read"), s.listDictTypes)
	protected.POST("/dict-types", s.Require("dictionary.write"), s.createDictType)
	protected.PUT("/dict-types/:id", s.Require("dictionary.write"), s.updateDictType)
	protected.GET("/dict-types/:code/items", s.Require("dictionary.read"), s.listDictItems)
	protected.POST("/dict-types/:code/items", s.Require("dictionary.write"), s.createDictItem)
	protected.PUT("/dict-items/:id", s.Require("dictionary.write"), s.updateDictItem)
	protected.GET("/dictionaries/:code", s.Require("dictionary.read"), s.activeDictionary)
	protected.GET("/login-logs", s.Require("login_log.read"), s.loginLogs)
	protected.GET("/audit-logs", s.Require("audit.read"), s.auditLogs)
	protected.GET("/voip/apps", s.Require("voip.app.read"), s.voipApps)
	protected.GET("/voip/apps/:app_id", s.Require("voip.app.read"), s.voipApp)
	protected.GET("/voip/apps/:app_id/devices", s.Require("voip.app.read"), s.voipAppDevices)
	protected.GET("/voip/devices/:device_id/profile", s.Require("voip.profile.read"), s.voipDeviceProfile)
	protected.POST("/device-pool/imports", s.Require("device.import"), s.createDeviceImport)
	protected.GET("/jobs", s.Require("job.read"), s.listJobs)
	protected.GET("/jobs/:id", s.Require("job.read"), s.getJob)
	protected.GET("/jobs/:id/result", s.Require("job.read"), s.jobResult)
	protected.POST("/jobs/:id/retry", s.Require("job.retry"), s.retryJob)
}

func (s *HTTPServer) RegisterInternal(r *gin.Engine, internalKey string) {
	r.GET("/v1/internal/configs/:namespace/:config_key", func(c *gin.Context) {
		provided := c.GetHeader("X-Internal-Key")
		if internalKey == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(internalKey)) != 1 {
			apiresp.Unauthorized(c)
			return
		}
		value, secrets, revision, err := s.configs.Resolved(c, c.Param("namespace"), c.Param("config_key"), c.Query("scope_type"), c.Query("scope_id"))
		if err != nil {
			s.writeConfigError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		apiresp.OK(c, gin.H{"value": value, "secrets": secrets, "revision": revision})
	})
}

func (s *HTTPServer) serviceStatus(c *gin.Context) {
	services, err := s.statuses.List(c.Request.Context())
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	type metrics struct {
		UserCount       int `db:"user_count" json:"user_count"`
		BoundDevices    int `db:"bound_devices" json:"bound_devices"`
		AvailablePool   int `db:"available_pool" json:"available_pool"`
		Binds24H        int `db:"binds_24h" json:"binds_24h"`
		Unbinds24H      int `db:"unbinds_24h" json:"unbinds_24h"`
		CleanupPending  int `db:"cleanup_pending" json:"cleanup_pending"`
		FailedAdminJobs int `db:"failed_admin_jobs" json:"failed_admin_jobs"`
	}
	var summary metrics
	err = s.store.db.GetContext(c, &summary, `SELECT
		(SELECT COUNT(*) FROM users) user_count,
		(SELECT COUNT(*) FROM device_bind WHERE user_id>0) bound_devices,
		(SELECT COUNT(*) FROM device_pool p LEFT JOIN device_bind b ON b.device_id=p.device_id WHERE p.status=0 AND b.id IS NULL) available_pool,
		(SELECT COUNT(*) FROM device_bind_log WHERE action=1 AND created_at>=NOW()-INTERVAL 24 HOUR) binds_24h,
		(SELECT COUNT(*) FROM device_bind_log WHERE action=2 AND created_at>=NOW()-INTERVAL 24 HOUR) unbinds_24h,
		(SELECT COUNT(*) FROM cleanup_outbox) cleanup_pending,
		(SELECT COUNT(*) FROM admin_jobs WHERE status IN (3,4)) failed_admin_jobs`)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	type recentAudit struct {
		ID           int64     `db:"id" json:"id"`
		AdminUserID  int64     `db:"admin_user_id" json:"admin_user_id"`
		Email        string    `db:"email" json:"email"`
		Action       string    `db:"action" json:"action"`
		ResourceType string    `db:"resource_type" json:"resource_type"`
		ResourceID   string    `db:"resource_id" json:"resource_id"`
		CreatedAt    time.Time `db:"created_at" json:"created_at"`
	}
	var recent []recentAudit
	_ = s.store.db.SelectContext(c, &recent, `SELECT l.id,l.admin_user_id,COALESCE(a.email,'') email,l.action,l.resource_type,l.resource_id,l.created_at FROM admin_audit_log l LEFT JOIN admin_users a ON a.id=l.admin_user_id ORDER BY l.id DESC LIMIT 5`)
	apiresp.OK(c, gin.H{"services": services, "metrics": summary, "recent_audits": recent})
}

func (s *HTTPServer) singleServiceStatus(c *gin.Context) {
	services, err := s.statuses.List(c.Request.Context())
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	for _, service := range services {
		if service.Service == c.Param("service") {
			apiresp.OK(c, service)
			return
		}
	}
	c.JSON(http.StatusNotFound, apiresp.JSON{Code: 404, Msg: "服务不存在"})
}

func (s *HTTPServer) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearerToken(c.GetHeader("Authorization"))
		if raw == "" {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		identity, err := s.auth.ValidateAccess(c.Request.Context(), raw)
		if err != nil {
			apiresp.Unauthorized(c)
			c.Abort()
			return
		}
		if identity.MustChangePassword && c.Request.URL.Path != "/v1/admin/me" && c.Request.URL.Path != "/v1/admin/me/navigation" && c.Request.URL.Path != "/v1/admin/me/password" {
			c.JSON(http.StatusForbidden, apiresp.JSON{Code: 40302, Msg: "请先修改初始密码"})
			c.Abort()
			return
		}
		c.Set(identityKey, identity)
		c.Next()
	}
}

func (s *HTTPServer) Require(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := identityFromContext(c)
		if !ok || !s.access.Enforce(identity.UserID, permission) {
			c.JSON(http.StatusForbidden, apiresp.JSON{Code: 403, Msg: "无权执行此操作"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *HTTPServer) RequireConfigRead() gin.HandlerFunc {
	return func(c *gin.Context) {
		namespace := c.Param("namespace")
		if namespace == "" {
			namespace = c.Query("namespace")
		}
		permission := "config.read"
		if namespace == "voip-server" {
			permission = "voip.app.read"
		}
		identity, ok := identityFromContext(c)
		if !ok || !s.access.Enforce(identity.UserID, permission) {
			c.JSON(http.StatusForbidden, apiresp.JSON{Code: 403, Msg: "无权读取该配置"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *HTTPServer) RequireConfigWrite() gin.HandlerFunc {
	return func(c *gin.Context) {
		permission := "config.write"
		if c.Param("namespace") == "voip-server" && c.Param("config_key") == "wechat.apps" {
			permission = "voip.app.write"
		}
		identity, ok := identityFromContext(c)
		if !ok || !s.access.Enforce(identity.UserID, permission) {
			c.JSON(http.StatusForbidden, apiresp.JSON{Code: 403, Msg: "无权修改该配置"})
			c.Abort()
			return
		}
		c.Next()
	}
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (s *HTTPServer) login(c *gin.Context) {
	var request loginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	limitKeys := []string{authLimitKey("login:email", strings.ToLower(strings.TrimSpace(request.Email))), authLimitKey("login:ip", c.ClientIP())}
	s.policyMu.RLock()
	loginWindow, loginMax := s.loginWindow, s.loginMaxAttempts
	s.policyMu.RUnlock()
	if s.authAttemptsExceeded(c, limitKeys, loginMax) {
		c.JSON(http.StatusTooManyRequests, apiresp.JSON{Code: 429, Msg: "登录尝试过多，请 15 分钟后重试"})
		return
	}
	result, err := s.auth.Login(c.Request.Context(), request.Email, request.Password, requestMeta(c))
	if err != nil {
		s.recordAuthFailure(c, limitKeys, loginWindow)
		s.writeAuthError(c, err)
		return
	}
	s.clearAuthFailures(c, limitKeys)
	s.writeAuthResult(c, result)
}

type mfaVerifyRequest struct {
	ChallengeToken string `json:"mfa_challenge_token" binding:"required"`
	Code           string `json:"code"`
	RecoveryCode   string `json:"recovery_code"`
}

func (s *HTTPServer) verifyMFA(c *gin.Context) {
	var request mfaVerifyRequest
	if err := c.ShouldBindJSON(&request); err != nil || (request.Code == "" && request.RecoveryCode == "") {
		apiresp.BadParam(c, "请输入 TOTP 验证码或恢复码")
		return
	}
	limitKeys := []string{authLimitKey("mfa:challenge", TokenHash(request.ChallengeToken)), authLimitKey("mfa:ip", c.ClientIP())}
	s.policyMu.RLock()
	mfaWindow, mfaMax := s.mfaWindow, s.mfaMaxAttempts
	s.policyMu.RUnlock()
	if s.authAttemptsExceeded(c, limitKeys, mfaMax) {
		c.JSON(http.StatusTooManyRequests, apiresp.JSON{Code: 429, Msg: "MFA 尝试过多，请 5 分钟后重试"})
		return
	}
	result, err := s.auth.VerifyMFA(c.Request.Context(), request.ChallengeToken, request.Code, request.RecoveryCode, requestMeta(c))
	if err != nil {
		s.recordAuthFailure(c, limitKeys, mfaWindow)
		s.writeAuthError(c, err)
		return
	}
	s.clearAuthFailures(c, limitKeys)
	s.writeAuthResult(c, result)
}

func authLimitKey(kind, value string) string {
	return "thingconnect:admin:auth-limit:" + kind + ":" + TokenHash(strings.TrimSpace(value))
}

func (s *HTTPServer) authAttemptsExceeded(c *gin.Context, keys []string, limit int64) bool {
	if s.redis == nil {
		return false
	}
	values, err := s.redis.MGet(c.Request.Context(), keys...).Result()
	if err != nil {
		return false
	}
	for _, raw := range values {
		if value, ok := raw.(string); ok {
			var attempts int64
			if _, err := fmt.Sscan(value, &attempts); err == nil && attempts >= limit {
				return true
			}
		}
	}
	return false
}

func (s *HTTPServer) recordAuthFailure(c *gin.Context, keys []string, ttl time.Duration) {
	if s.redis == nil {
		return
	}
	pipe := s.redis.TxPipeline()
	for _, key := range keys {
		pipe.Incr(c.Request.Context(), key)
		pipe.Expire(c.Request.Context(), key, ttl)
	}
	_, _ = pipe.Exec(c.Request.Context())
}

func (s *HTTPServer) clearAuthFailures(c *gin.Context, keys []string) {
	if s.redis != nil {
		_ = s.redis.Del(c.Request.Context(), keys...).Err()
	}
}

func (s *HTTPServer) refresh(c *gin.Context) {
	if c.GetHeader("X-Admin-Request") != "1" {
		apiresp.BadParam(c, "缺少后台请求标识")
		return
	}
	refreshToken, err := c.Cookie("admin_refresh")
	if err != nil || refreshToken == "" {
		apiresp.Unauthorized(c)
		return
	}
	result, err := s.auth.Refresh(c.Request.Context(), refreshToken)
	if err != nil {
		s.clearRefreshCookie(c)
		s.writeAuthError(c, err)
		return
	}
	s.writeAuthResult(c, result)
}

func (s *HTTPServer) logout(c *gin.Context) {
	if c.GetHeader("X-Admin-Request") != "1" {
		apiresp.BadParam(c, "缺少后台请求标识")
		return
	}
	refreshToken, _ := c.Cookie("admin_refresh")
	_ = s.auth.Logout(c.Request.Context(), refreshToken)
	s.clearRefreshCookie(c)
	apiresp.OK(c, gin.H{"logged_out": true})
}

func (s *HTTPServer) enrollTOTP(c *gin.Context) {
	token := bearerToken(c.GetHeader("Authorization"))
	if token == "" {
		apiresp.Unauthorized(c)
		return
	}
	secret, uri, err := s.auth.EnrollTOTP(c.Request.Context(), token)
	if err != nil {
		s.writeAuthError(c, err)
		return
	}
	apiresp.OK(c, gin.H{"secret": secret, "otpauth_uri": uri, "expires_in": 600})
}

type confirmTOTPRequest struct {
	Code string `json:"code" binding:"required"`
}

func (s *HTTPServer) confirmTOTP(c *gin.Context) {
	token := bearerToken(c.GetHeader("Authorization"))
	var request confirmTOTPRequest
	if token == "" || c.ShouldBindJSON(&request) != nil {
		apiresp.BadParam(c, "缺少绑定令牌或 TOTP 验证码")
		return
	}
	limitKeys := []string{authLimitKey("mfa:setup", token), authLimitKey("mfa:ip", c.ClientIP())}
	s.policyMu.RLock()
	mfaWindow, mfaMax := s.mfaWindow, s.mfaMaxAttempts
	s.policyMu.RUnlock()
	if s.authAttemptsExceeded(c, limitKeys, mfaMax) {
		c.JSON(http.StatusTooManyRequests, apiresp.JSON{Code: 429, Msg: "MFA 绑定尝试过多，请稍后重试"})
		return
	}
	result, recoveryCodes, err := s.auth.ConfirmTOTP(c.Request.Context(), token, request.Code)
	if err != nil {
		s.recordAuthFailure(c, limitKeys, mfaWindow)
		s.writeAuthError(c, err)
		return
	}
	s.clearAuthFailures(c, limitKeys)
	s.setRefreshCookie(c, result.RefreshToken)
	result.RefreshToken = ""
	apiresp.OK(c, gin.H{"auth": result, "recovery_codes": recoveryCodes})
}

func (s *HTTPServer) me(c *gin.Context) {
	identity, _ := identityFromContext(c)
	user, err := s.store.AdminByID(c.Request.Context(), identity.UserID)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"user": user, "roles": identity.Roles})
}

func (s *HTTPServer) changeOwnPassword(c *gin.Context) {
	var request struct {
		CurrentPassword string `json:"current_password" binding:"required"`
		NewPassword     string `json:"new_password" binding:"required"`
		CurrentMFACode  string `json:"current_mfa_code"`
		CurrentRecovery string `json:"current_recovery_code"`
	}
	identity, _ := identityFromContext(c)
	if c.ShouldBindJSON(&request) != nil || request.CurrentPassword == request.NewPassword || s.auth.VerifyPassword(c, identity.UserID, request.CurrentPassword) != nil || s.auth.VerifyStepUp(c, identity.UserID, request.CurrentMFACode, request.CurrentRecovery) != nil {
		apiresp.BadParam(c, "当前密码或二次验证码错误")
		return
	}
	hash, err := HashAdminPassword(request.NewPassword)
	if err != nil {
		apiresp.BadParam(c, AdminPasswordPolicyMessage)
		return
	}
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(c, `UPDATE admin_users SET password=?,must_change_password=0,password_updated_at=NOW(),auth_revision=auth_revision+1 WHERE id=?`, hash, identity.UserID); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	if _, err := tx.ExecContext(c, `UPDATE admin_sessions SET revoked_at=COALESCE(revoked_at,NOW()),revoked_reason='password changed' WHERE admin_user_id=?`, identity.UserID); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	if err := s.auditTx(c, tx, identity, "admin.password.change", "admin_user", fmt.Sprint(identity.UserID), "管理员修改自己的密码", "", `{"changed":true}`); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit administrator password")
		return
	}
	s.clearRefreshCookie(c)
	apiresp.OK(c, gin.H{"changed": true, "reauthenticate": true})
}

func (s *HTTPServer) permissions(c *gin.Context) {
	identity, _ := identityFromContext(c)
	apiresp.OK(c, gin.H{"permissions": s.access.Permissions(identity.UserID)})
}

func (s *HTTPServer) navigation(c *gin.Context) {
	identity, _ := identityFromContext(c)
	menus, err := s.store.Navigation(c.Request.Context(), identity.UserID)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	allowed := make([]Menu, 0, len(menus))
	for _, menu := range menus {
		if menu.PermissionCode == "" || s.access.Enforce(identity.UserID, menu.PermissionCode) {
			allowed = append(allowed, menu)
		}
	}
	apiresp.OK(c, gin.H{"menus": allowed})
}

func (s *HTTPServer) configDefinitions(c *gin.Context) {
	definitions, err := s.configs.Definitions(c.Query("namespace"))
	if err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"items": definitions})
}

func (s *HTTPServer) listConfigs(c *gin.Context) {
	entries, err := s.configs.List(c.Request.Context(), c.Query("namespace"))
	if err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	identity, _ := identityFromContext(c)
	for index := range entries {
		if !s.canAccessConfigSecrets(identity, entries[index].Namespace, entries[index].ConfigKey) {
			continue
		}
		if err := s.configs.PopulateSecrets(c.Request.Context(), &entries[index]); err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
	}
	apiresp.OK(c, gin.H{"items": entries})
}

func (s *HTTPServer) getConfig(c *gin.Context) {
	entry, err := s.configs.Get(c.Request.Context(), c.Param("namespace"), c.Param("config_key"), c.Query("scope_type"), c.Query("scope_id"))
	if err != nil {
		s.writeConfigError(c, err)
		return
	}
	identity, _ := identityFromContext(c)
	if s.canAccessConfigSecrets(identity, entry.Namespace, entry.ConfigKey) {
		if err := s.configs.PopulateSecrets(c.Request.Context(), &entry); err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
	}
	apiresp.OK(c, entry)
}

func (s *HTTPServer) canAccessConfigSecrets(identity AccessIdentity, namespace, key string) bool {
	if namespace == "voip-server" && key == "wechat.apps" {
		return s.access.Enforce(identity.UserID, "voip.app.write")
	}
	return s.access.Enforce(identity.UserID, "config.secret.write")
}

type configWriteRequest struct {
	Value            json.RawMessage  `json:"value" binding:"required"`
	Secrets          *json.RawMessage `json:"secrets"`
	Status           *int8            `json:"status"`
	ScopeType        string           `json:"scope_type"`
	ScopeID          string           `json:"scope_id"`
	ExpectedRevision int64            `json:"expected_revision"`
	Reason           string           `json:"reason"`
	CurrentPassword  string           `json:"current_password"`
	CurrentMFACode   string           `json:"current_mfa_code"`
	CurrentRecovery  string           `json:"current_recovery_code"`
	Confirm          bool             `json:"confirm"`
	TestRecipient    string           `json:"test_recipient"`
}

func (s *HTTPServer) validateConfig(c *gin.Context) {
	var request configWriteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	secrets, provided := rawPointer(request.Secrets)
	if err := s.configs.Validate(c.Param("namespace"), c.Param("config_key"), request.Value, secrets, provided); err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"valid": true})
}

func (s *HTTPServer) testConfig(c *gin.Context) {
	var request configWriteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	recipient := strings.TrimSpace(request.TestRecipient)
	parsed, err := mail.ParseAddress(recipient)
	if err != nil || parsed.Address != recipient || strings.TrimSpace(request.Reason) == "" {
		apiresp.BadParam(c, "测试收件地址和操作原因不能为空")
		return
	}
	namespace, key := c.Param("namespace"), c.Param("config_key")
	secrets, provided := rawPointer(request.Secrets)
	if err := s.configs.Validate(namespace, key, request.Value, secrets, provided); err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	var smtpValue json.RawMessage
	var smtpSecrets json.RawMessage
	var subject, body, htmlBody string
	switch {
	case namespace == "user-server" && key == "smtp":
		smtpValue = request.Value
		smtpSecrets, err = s.configs.EffectiveSecrets(c, namespace, key, secrets, provided)
		subject = "ThingConnect SMTP 配置测试"
		body = "这是一封由 ThingConnect 管理后台发送的 SMTP 配置测试邮件。"
	case namespace == "user-server" && strings.HasPrefix(key, "email.template."):
		smtpValue, smtpSecrets, _, err = s.configs.Resolved(c, "user-server", "smtp", "global", "")
		var template struct {
			Subject  string `json:"subject"`
			HTMLBody string `json:"html_body"`
			TextBody string `json:"text_body"`
		}
		if json.Unmarshal(request.Value, &template) != nil {
			apiresp.BadParam(c, "邮件模板无效")
			return
		}
		replacements := map[string]string{"code": "123456", "expires_in_minutes": "5", "product_name": "ThingConnect", "support_email": "support@example.com"}
		render := func(value string) string {
			return templateVariable.ReplaceAllStringFunc(value, func(token string) string {
				parts := templateVariable.FindStringSubmatch(token)
				return replacements[parts[1]]
			})
		}
		subject = render(template.Subject)
		body = template.TextBody
		if strings.TrimSpace(body) == "" {
			body = template.HTMLBody
		}
		body = render(body)
		htmlBody = render(template.HTMLBody)
	default:
		apiresp.BadParam(c, "该配置项不支持在线测试")
		return
	}
	if err != nil {
		s.writeConfigError(c, err)
		return
	}
	var public struct {
		Enabled  bool   `json:"enabled"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		TLSMode  string `json:"tls_mode"`
		Username string `json:"username"`
		From     string `json:"from"`
	}
	var private struct {
		Password string `json:"password"`
	}
	if json.Unmarshal(smtpValue, &public) != nil || json.Unmarshal(smtpSecrets, &private) != nil || !public.Enabled || private.Password == "" {
		apiresp.BadParam(c, "请先配置并启用完整的 SMTP 参数")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	mailer := smtpmailer.New(smtpmailer.Config{Host: public.Host, Port: public.Port, TLSMode: public.TLSMode, Username: public.Username, Password: private.Password, From: public.From})
	var sendErr error
	if richMailer, ok := mailer.(interface {
		SendMessage(context.Context, string, string, string, string) error
	}); ok && strings.TrimSpace(htmlBody) != "" {
		sendErr = richMailer.SendMessage(ctx, recipient, subject, body, htmlBody)
	} else {
		sendErr = mailer.Send(ctx, recipient, subject, body)
	}
	if sendErr != nil {
		apiresp.BadParam(c, "测试邮件发送失败: "+sendErr.Error())
		return
	}
	identity, _ := identityFromContext(c)
	_ = s.store.Audit(c, requestAudit(c, identity, "config.test", "config", definitionID(namespace, key), request.Reason, "", `{"sent":true}`))
	apiresp.OK(c, gin.H{"sent": true})
}

func (s *HTTPServer) putConfig(c *gin.Context) {
	var request configWriteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	if strings.TrimSpace(request.Reason) == "" || !request.Confirm {
		apiresp.BadParam(c, "发布配置需要填写原因并明确确认")
		return
	}
	status := int8(1)
	if request.Status != nil {
		status = *request.Status
	}
	secrets, secretsProvided := rawPointer(request.Secrets)
	if secretsProvided {
		identity, _ := identityFromContext(c)
		if !s.canAccessConfigSecrets(identity, c.Param("namespace"), c.Param("config_key")) {
			c.JSON(http.StatusForbidden, apiresp.JSON{Code: 403, Msg: "无权修改密钥"})
			return
		}
	}
	identity, _ := identityFromContext(c)
	isMFAPolicy := c.Param("namespace") == "system" && c.Param("config_key") == "mfa.policy"
	if isMFAPolicy {
		if !s.access.Enforce(identity.UserID, "security.mfa.write") || s.auth.VerifyPassword(c, identity.UserID, request.CurrentPassword) != nil || s.auth.VerifyStepUp(c, identity.UserID, request.CurrentMFACode, request.CurrentRecovery) != nil {
			apiresp.BadParam(c, "修改 MFA 策略需要权限、当前密码、二次验证、原因和确认")
			return
		}
	}
	entry, err := s.configs.Put(c.Request.Context(), ConfigWrite{
		Namespace: c.Param("namespace"), ConfigKey: c.Param("config_key"),
		ScopeType: request.ScopeType, ScopeID: request.ScopeID, Value: request.Value,
		Secrets: secrets, SecretsProvided: secretsProvided, Status: status,
		ExpectedRevision: request.ExpectedRevision, Actor: identity,
		RequestID: logging.RequestIDFrom(c.Request.Context()), Method: c.Request.Method,
		Path: c.Request.URL.Path, ClientIP: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		Reason: strings.TrimSpace(request.Reason),
	})
	if err != nil {
		s.writeConfigError(c, err)
		return
	}
	if isMFAPolicy {
		var policy struct {
			Enabled bool `json:"enabled"`
		}
		if json.Unmarshal(request.Value, &policy) == nil {
			s.auth.SetMFAEnabled(policy.Enabled)
		}
	}
	if c.Param("namespace") == "system" && c.Param("config_key") == "admin.session_policy" {
		var policy struct {
			AccessTTL   string `json:"access_ttl"`
			RefreshTTL  string `json:"refresh_ttl"`
			MaxSessions int    `json:"max_sessions"`
			LoginWindow string `json:"login_window"`
			LoginMax    int64  `json:"login_max_attempts"`
			MFAWindow   string `json:"mfa_window"`
			MFAMax      int64  `json:"mfa_max_attempts"`
		}
		if json.Unmarshal(request.Value, &policy) == nil {
			accessTTL, accessErr := time.ParseDuration(policy.AccessTTL)
			refreshTTL, refreshErr := time.ParseDuration(policy.RefreshTTL)
			loginWindow, loginErr := time.ParseDuration(policy.LoginWindow)
			mfaWindow, mfaErr := time.ParseDuration(policy.MFAWindow)
			if accessErr == nil && refreshErr == nil && loginErr == nil && mfaErr == nil {
				s.auth.SetSessionPolicy(accessTTL, refreshTTL, policy.MaxSessions)
				s.SetAuthRatePolicy(loginWindow, policy.LoginMax, mfaWindow, policy.MFAMax)
			}
		}
	}
	apiresp.OK(c, entry)
}

func (s *HTTPServer) writeConfigError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, apiresp.JSON{Code: 404, Msg: "配置项不存在"})
	case errors.Is(err, ErrConflict):
		c.JSON(http.StatusConflict, apiresp.JSON{Code: 409, Msg: "配置已被其他管理员修改，请刷新后重试"})
	default:
		apiresp.BadParam(c, err.Error())
	}
}

func rawPointer(value *json.RawMessage) (json.RawMessage, bool) {
	if value == nil {
		return nil, false
	}
	return *value, true
}

func (s *HTTPServer) writeAuthResult(c *gin.Context, result AuthResult) {
	if result.RefreshToken != "" {
		s.setRefreshCookie(c, result.RefreshToken)
		result.RefreshToken = ""
	}
	apiresp.OK(c, result)
}

func (s *HTTPServer) writeAuthError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials), errors.Is(err, ErrInvalidMFA):
		c.JSON(http.StatusUnauthorized, apiresp.JSON{Code: 40101, Msg: "账号、密码或验证码错误"})
	case errors.Is(err, ErrAccountDisabled):
		c.JSON(http.StatusForbidden, apiresp.JSON{Code: 40301, Msg: "管理员账号已禁用"})
	case errors.Is(err, ErrInvalidToken), errors.Is(err, ErrSessionReuse), errors.Is(err, ErrSessionExpired), errors.Is(err, ErrNotFound):
		apiresp.Unauthorized(c)
	case errors.Is(err, ErrAuthUnavailable):
		c.JSON(http.StatusServiceUnavailable, apiresp.JSON{Code: 503, Msg: "认证服务暂不可用，请稍后重试"})
	default:
		apiresp.Internal(c, err.Error())
	}
}

func (s *HTTPServer) setRefreshCookie(c *gin.Context, value string) {
	_, refreshTTL, _ := s.auth.TTLs()
	if refreshTTL <= 0 {
		refreshTTL = 7 * 24 * time.Hour
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: "admin_refresh", Value: value, Path: "/v1/admin/auth",
		MaxAge: int(refreshTTL.Seconds()), HttpOnly: true,
		Secure: s.cookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

func (s *HTTPServer) clearRefreshCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: "admin_refresh", Value: "", Path: "/v1/admin/auth", MaxAge: -1, HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteStrictMode})
}

func bearerToken(value string) string {
	parts := strings.SplitN(value, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func identityFromContext(c *gin.Context) (AccessIdentity, bool) {
	value, ok := c.Get(identityKey)
	if !ok {
		return AccessIdentity{}, false
	}
	identity, ok := value.(AccessIdentity)
	return identity, ok
}

func requestMeta(c *gin.Context) LoginMeta {
	return LoginMeta{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()}
}
