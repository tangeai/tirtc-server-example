package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/logging"
)

type pageResult struct {
	Items    any `json:"items"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Total    int `json:"total"`
}

func pageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

func appendDateRange(c *gin.Context, column string, where *string, args *[]any) error {
	if value := strings.TrimSpace(c.Query("created_from")); value != "" {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return errors.New("created_from must use YYYY-MM-DD")
		}
		*where += " AND " + column + ">=?"
		*args = append(*args, parsed)
	}
	if value := strings.TrimSpace(c.Query("created_to")); value != "" {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return errors.New("created_to must use YYYY-MM-DD")
		}
		*where += " AND " + column + "<?"
		*args = append(*args, parsed.AddDate(0, 0, 1))
	}
	return nil
}

func listOrder(c *gin.Context, allowed map[string]string, defaultField, tieColumn string) (string, error) {
	field := strings.TrimSpace(c.Query("sort_by"))
	if field == "" {
		field = defaultField
	}
	column, ok := allowed[field]
	if !ok {
		return "", errors.New("sort_by is not supported")
	}
	direction := strings.ToLower(strings.TrimSpace(c.Query("sort_order")))
	if direction == "" {
		direction = "desc"
	}
	if direction != "asc" && direction != "desc" {
		return "", errors.New("sort_order must be asc or desc")
	}
	direction = strings.ToUpper(direction)
	if column == tieColumn {
		return " ORDER BY " + column + " " + direction, nil
	}
	return " ORDER BY " + column + " " + direction + "," + tieColumn + " " + direction, nil
}

type managedUser struct {
	ID           int64      `db:"id" json:"id"`
	Email        string     `db:"email" json:"email"`
	BindQuota    int        `db:"bind_quota" json:"bind_quota"`
	Status       int8       `db:"status" json:"status"`
	DisabledAt   *time.Time `db:"disabled_at" json:"disabled_at,omitempty"`
	AuthRevision int64      `db:"auth_revision" json:"auth_revision"`
	DeviceCount  int        `db:"device_count" json:"device_count"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at" json:"updated_at"`
}

func (s *HTTPServer) listUsers(c *gin.Context) {
	page, size := pageParams(c)
	keyword := strings.TrimSpace(c.Query("keyword"))
	status := strings.TrimSpace(c.Query("status"))
	where, args := ` WHERE 1=1`, []any{}
	if keyword != "" {
		where += ` AND (u.email LIKE ? OR CAST(u.id AS CHAR)=?)`
		args = append(args, "%"+keyword+"%", keyword)
	}
	if status == "0" || status == "1" {
		where += ` AND u.status=?`
		args = append(args, status)
	}
	if err := appendDateRange(c, "u.created_at", &where, &args); err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	order, err := listOrder(c, map[string]string{"created_at": "u.created_at"}, "created_at", "u.id")
	if err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	var total int
	if err := s.store.db.GetContext(c, &total, `SELECT COUNT(*) FROM users u`+where, args...); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	var users []managedUser
	err = s.store.db.SelectContext(c, &users, `SELECT u.id,u.email,u.bind_quota,u.status,u.disabled_at,u.auth_revision,u.created_at,u.updated_at,(SELECT COUNT(*) FROM device_bind d WHERE d.user_id=u.id) device_count FROM users u`+where+order+` LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, pageResult{Items: users, Page: page, PageSize: size, Total: total})
}

func (s *HTTPServer) getUser(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		apiresp.BadParam(c, "用户 ID 无效")
		return
	}
	var user managedUser
	err = s.store.db.GetContext(c, &user, `SELECT u.id,u.email,u.bind_quota,u.status,u.disabled_at,u.auth_revision,u.created_at,u.updated_at,COUNT(d.id) device_count FROM users u LEFT JOIN device_bind d ON d.user_id=u.id WHERE u.id=? GROUP BY u.id`, id)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, apiresp.JSON{Code: 404, Msg: "用户不存在"})
		return
	}
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	var devices []managedDevice
	_ = s.store.db.SelectContext(c, &devices, managedDeviceSelect+` WHERE d.user_id=? ORDER BY d.id DESC`, id)
	var bindLogs []struct {
		ID        int64     `db:"id" json:"id"`
		DeviceID  string    `db:"device_id" json:"device_id"`
		Action    int8      `db:"action" json:"action"`
		MAC       string    `db:"mac" json:"mac"`
		Assign    string    `db:"assign" json:"assign"`
		CreatedAt time.Time `db:"created_at" json:"created_at"`
	}
	_ = s.store.db.SelectContext(c, &bindLogs, `SELECT id,device_id,action,mac,assign,created_at FROM device_bind_log WHERE user_id=? ORDER BY id DESC LIMIT 100`, id)
	type aiSummary struct {
		RoleCount     int `db:"role_count" json:"role_count"`
		ResourceCount int `db:"resource_count" json:"resource_count"`
	}
	var ai aiSummary
	_ = s.store.db.GetContext(c, &ai, `SELECT (SELECT COUNT(*) FROM ai_user_role WHERE user_id=?) role_count,(SELECT COUNT(*) FROM ai_user_resource WHERE user_id=?) resource_count`, id, id)
	apiresp.OK(c, gin.H{"user": user, "devices": devices, "bind_logs": bindLogs, "ai": ai})
}

type statusRequest struct {
	Status         int8   `json:"status"`
	ExpectedStatus *int8  `json:"expected_status" binding:"required"`
	Reason         string `json:"reason" binding:"required"`
}

func (s *HTTPServer) updateUserStatus(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request statusRequest
	if err != nil || c.ShouldBindJSON(&request) != nil || (request.Status != 0 && request.Status != 1) {
		apiresp.BadParam(c, "状态请求无效")
		return
	}
	identity, _ := identityFromContext(c)
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(c, `UPDATE users SET status=?,disabled_at=IF(?=0,NOW(),NULL),auth_revision=auth_revision+1 WHERE id=? AND status=?`, request.Status, request.Status, id, *request.ExpectedStatus)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		c.JSON(http.StatusConflict, apiresp.JSON{Code: 409, Msg: "用户状态已变化，请刷新后重试"})
		return
	}
	if err := s.auditTx(c, tx, identity, "user.status.write", "user", strconv.FormatInt(id, 10), request.Reason, fmt.Sprintf(`{"status":%d}`, *request.ExpectedStatus), fmt.Sprintf(`{"status":%d}`, request.Status)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit user status")
		return
	}
	key := fmt.Sprintf("thingconnect:user:disabled:%d", id)
	_ = s.redis.Set(c, fmt.Sprintf("thingconnect:user:revoked_after:%d", id), strconv.FormatInt(time.Now().Unix(), 10), 0).Err()
	if request.Status == 0 {
		_ = s.redis.Set(c, key, "1", 0).Err()
	} else {
		_ = s.redis.Del(c, key).Err()
	}
	apiresp.OK(c, gin.H{"id": id, "status": request.Status})
}

type quotaRequest struct {
	BindQuota     int    `json:"bind_quota"`
	ExpectedQuota int    `json:"expected_quota"`
	Reason        string `json:"reason" binding:"required"`
}

func (s *HTTPServer) updateUserQuota(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request quotaRequest
	if err != nil || c.ShouldBindJSON(&request) != nil || request.BindQuota < 0 || request.ExpectedQuota < 0 {
		apiresp.BadParam(c, "额度请求无效")
		return
	}
	identity, _ := identityFromContext(c)
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(c, `UPDATE users SET bind_quota=? WHERE id=? AND bind_quota=?`, request.BindQuota, id, request.ExpectedQuota)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		c.JSON(http.StatusConflict, apiresp.JSON{Code: 409, Msg: "用户额度已变化，请刷新后重试"})
		return
	}
	if err := s.auditTx(c, tx, identity, "user.quota.write", "user", strconv.FormatInt(id, 10), request.Reason, fmt.Sprintf(`{"bind_quota":%d}`, request.ExpectedQuota), fmt.Sprintf(`{"bind_quota":%d}`, request.BindQuota)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit user quota")
		return
	}
	apiresp.OK(c, gin.H{"id": id, "bind_quota": request.BindQuota})
}

func (s *HTTPServer) sendPasswordResetEmail(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err != nil || c.ShouldBindJSON(&request) != nil {
		apiresp.BadParam(c, "请求无效")
		return
	}
	var email string
	if err := s.store.db.GetContext(c, &email, `SELECT email FROM users WHERE id=? AND status=1`, id); err != nil {
		c.JSON(http.StatusNotFound, apiresp.JSON{Code: 404, Msg: "可用用户不存在"})
		return
	}
	if err := s.redis.XAdd(c, &redis.XAddArgs{Stream: "thingconnect:admin:user-commands", MaxLen: 10000, Approx: true, Values: map[string]any{"type": "password_reset_email", "user_id": id, "email": email, "created_at": time.Now().Unix()}}).Err(); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	identity, _ := identityFromContext(c)
	_ = s.store.Audit(c, requestAudit(c, identity, "user.password_reset", "user", strconv.FormatInt(id, 10), request.Reason, "", `{"queued":true}`))
	c.JSON(http.StatusAccepted, apiresp.JSON{Code: 200, Msg: "ok", Data: gin.H{"queued": true}})
}

type managedDevice struct {
	ID            int64      `db:"id" json:"id"`
	DeviceID      string     `db:"device_id" json:"device_id"`
	MAC           string     `db:"mac" json:"mac"`
	Assign        string     `db:"assign" json:"assign"`
	DeviceName    string     `db:"device_name" json:"device_name"`
	UserID        int64      `db:"user_id" json:"user_id"`
	UserEmail     string     `db:"user_email" json:"user_email"`
	LastUserID    int64      `db:"last_user_id" json:"last_user_id"`
	ActiveTime    *time.Time `db:"active_time" json:"active_time,omitempty"`
	BindTime      *time.Time `db:"bind_time" json:"bind_time,omitempty"`
	UnbindTime    *time.Time `db:"unbind_time" json:"unbind_time,omitempty"`
	CreatedAt     time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at" json:"updated_at"`
	Online        bool       `db:"-" json:"online"`
	PresenceKnown bool       `db:"-" json:"presence_known"`
	LastSeenAt    *time.Time `db:"-" json:"last_seen_at,omitempty"`
}

const managedDeviceSelect = `SELECT d.id,d.device_id,d.mac,d.assign,d.device_name,d.user_id,COALESCE(u.email,'') user_email,d.last_user_id,d.active_time,d.bind_time,d.unbind_time,d.created_at,d.updated_at FROM device_bind d LEFT JOIN users u ON u.id=d.user_id`

func (s *HTTPServer) listDevices(c *gin.Context) {
	page, size := pageParams(c)
	keyword := strings.TrimSpace(c.Query("keyword"))
	bound := strings.TrimSpace(c.Query("bound"))
	where, args := ` WHERE 1=1`, []any{}
	if keyword != "" {
		where += ` AND (d.device_id LIKE ? OR d.mac LIKE ? OR u.email LIKE ?)`
		args = append(args, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}
	switch bound {
	case "true":
		where += ` AND d.user_id>0`
	case "false":
		where += ` AND d.user_id=0`
	}
	order, err := listOrder(c, map[string]string{
		"id": "d.id", "active_time": "d.active_time", "bind_time": "d.bind_time",
	}, "id", "d.id")
	if err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	var total int
	countFrom := ` FROM device_bind d`
	if keyword != "" {
		countFrom += ` LEFT JOIN users u ON u.id=d.user_id`
	}
	if err := s.store.db.GetContext(c, &total, `SELECT COUNT(*)`+countFrom+where, args...); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	var devices []managedDevice
	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	if err := s.store.db.SelectContext(c, &devices, managedDeviceSelect+where+order+` LIMIT ? OFFSET ?`, queryArgs...); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	s.attachDevicePresence(c, devices)
	apiresp.OK(c, pageResult{Items: devices, Page: page, PageSize: size, Total: total})
}

type managedPoolDevice struct {
	ID               int64     `db:"id" json:"id"`
	DeviceID         string    `db:"device_id" json:"device_id"`
	Status           int8      `db:"status" json:"status"`
	EverBound        bool      `db:"ever_bound" json:"ever_bound"`
	CurrentUserID    int64     `db:"current_user_id" json:"current_user_id"`
	CurrentUserEmail string    `db:"current_user_email" json:"current_user_email"`
	Assign           string    `db:"assign" json:"assign"`
	ImportJobID      int64     `db:"import_job_id" json:"import_job_id"`
	ImportSourceName string    `db:"import_source_name" json:"import_source_name"`
	CreatedAt        time.Time `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time `db:"updated_at" json:"updated_at"`
}

func (s *HTTPServer) listDevicePool(c *gin.Context) {
	page, size := pageParams(c)
	keyword := strings.TrimSpace(c.Query("keyword"))
	state := strings.TrimSpace(c.Query("state"))
	where, args := ` WHERE 1=1`, []any{}
	if keyword != "" {
		where += ` AND p.device_id LIKE ?`
		args = append(args, "%"+keyword+"%")
	}
	switch state {
	case "":
	case "available":
		where += ` AND p.status=0 AND b.id IS NULL`
	case "allocated":
		where += ` AND p.status=1`
	case "released":
		where += ` AND p.status=0 AND b.id IS NOT NULL`
	default:
		apiresp.BadParam(c, "设备池状态无效")
		return
	}
	joins := ` FROM device_pool p
		LEFT JOIN device_bind b ON b.device_id=p.device_id
		LEFT JOIN users u ON u.id=b.user_id
		LEFT JOIN (
			SELECT ji.resource_id,MAX(ji.job_id) job_id
			FROM admin_job_items ji
			JOIN admin_jobs aj ON aj.id=ji.job_id AND aj.job_type='device_pool_import'
			WHERE ji.status=1
			GROUP BY ji.resource_id
		) imported ON imported.resource_id=p.device_id
		LEFT JOIN admin_jobs j ON j.id=imported.job_id`
	var total int
	if err := s.store.db.GetContext(c, &total, `SELECT COUNT(*)`+joins+where, args...); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	var items []managedPoolDevice
	query := `SELECT p.id,p.device_id,p.status,IF(b.id IS NULL,0,1) ever_bound,
		COALESCE(b.user_id,0) current_user_id,COALESCE(u.email,'') current_user_email,
		COALESCE(b.assign,'') assign,COALESCE(j.id,0) import_job_id,
		COALESCE(j.source_name,'') import_source_name,p.created_at,p.updated_at` + joins + where + ` ORDER BY p.id DESC LIMIT ? OFFSET ?`
	if err := s.store.db.SelectContext(c, &items, query, queryArgs...); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, pageResult{Items: items, Page: page, PageSize: size, Total: total})
}

func (s *HTTPServer) getDevice(c *gin.Context) {
	var device managedDevice
	err := s.store.db.GetContext(c, &device, managedDeviceSelect+` WHERE d.device_id=?`, c.Param("device_id"))
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, apiresp.JSON{Code: 404, Msg: "设备不存在"})
		return
	}
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	devices := []managedDevice{device}
	s.attachDevicePresence(c, devices)
	device = devices[0]
	apiresp.OK(c, device)
}

// devicePresenceKey maps a permanent device ID to the Redis presence key
// written from its formal MQTT ClientID (sn_{device_id}).
func devicePresenceKey(deviceID string) string { return "online:sn_" + deviceID }

func (s *HTTPServer) attachDevicePresence(ctx context.Context, devices []managedDevice) {
	if len(devices) == 0 || s.redis == nil {
		return
	}
	keys := make([]string, len(devices))
	for i := range devices {
		keys[i] = devicePresenceKey(devices[i].DeviceID)
	}
	values, err := s.redis.MGet(ctx, keys...).Result()
	if err != nil {
		return
	}
	for i, value := range values {
		devices[i].PresenceKnown = true
		if value == nil {
			continue
		}
		devices[i].Online = true
		raw, ok := value.(string)
		if !ok || raw == "1" {
			continue
		}
		seen, err := time.Parse(time.RFC3339Nano, raw)
		if err == nil {
			devices[i].LastSeenAt = &seen
		}
	}
}

func (s *HTTPServer) deviceBindLogs(c *gin.Context) {
	page, size := pageParams(c)
	var total int
	_ = s.store.db.GetContext(c, &total, `SELECT COUNT(*) FROM device_bind_log WHERE device_id=?`, c.Param("device_id"))
	var items []struct {
		ID        int64     `db:"id" json:"id"`
		DeviceID  string    `db:"device_id" json:"device_id"`
		UserID    int64     `db:"user_id" json:"user_id"`
		Action    int8      `db:"action" json:"action"`
		MAC       string    `db:"mac" json:"mac"`
		Assign    string    `db:"assign" json:"assign"`
		CreatedAt time.Time `db:"created_at" json:"created_at"`
	}
	if err := s.store.db.SelectContext(c, &items, `SELECT id,device_id,user_id,action,mac,assign,created_at FROM device_bind_log WHERE device_id=? ORDER BY id DESC LIMIT ? OFFSET ?`, c.Param("device_id"), size, (page-1)*size); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, pageResult{Items: items, Page: page, PageSize: size, Total: total})
}

func (s *HTTPServer) forceUnbind(c *gin.Context) {
	var request struct {
		ExpectedUserID int64  `json:"expected_user_id" binding:"required"`
		Reason         string `json:"reason" binding:"required"`
		Confirm        bool   `json:"confirm" binding:"required"`
	}
	if c.ShouldBindJSON(&request) != nil || request.ExpectedUserID <= 0 || !request.Confirm {
		apiresp.BadParam(c, "解绑请求无效或未二次确认")
		return
	}
	identity, _ := identityFromContext(c)
	result, err := s.devices.ForceUnbind(c.Request.Context(), ForceUnbindInput{
		DeviceID:       c.Param("device_id"),
		ExpectedUserID: request.ExpectedUserID,
		Reason:         request.Reason,
		Actor:          identity,
		RequestID:      logging.RequestIDFrom(c.Request.Context()),
		Method:         c.Request.Method,
		Path:           c.Request.URL.Path,
		ClientIP:       c.ClientIP(),
		UserAgent:      c.Request.UserAgent(),
	})
	switch {
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, apiresp.JSON{Code: 404, Msg: "设备不存在"})
		return
	case errors.Is(err, ErrConflict):
		c.JSON(http.StatusConflict, apiresp.JSON{Code: 409, Msg: "设备归属已变化，请刷新后重试"})
		return
	case errors.Is(err, ErrInvalidDeviceCommand):
		apiresp.BadParam(c, "解绑请求无效")
		return
	case err != nil:
		apiresp.Internal(c, "强制解绑失败")
		return
	}
	apiresp.OK(c, result)
}

func positiveID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func requestAudit(c *gin.Context, identity AccessIdentity, action, resource, resourceID, reason, before, after string) AuditEvent {
	return AuditEvent{AdminUserID: identity.UserID, RoleCodes: strings.Join(identity.Roles, ","), RequestID: logging.RequestIDFrom(c.Request.Context()), Method: c.Request.Method, Path: c.Request.URL.Path, HTTPStatus: 200, Action: action, Resource: resource, ResourceID: resourceID, Reason: strings.TrimSpace(reason), Before: before, After: after, ClientIP: c.ClientIP(), UserAgent: c.Request.UserAgent(), Success: true}
}

func (s *HTTPServer) auditTx(c *gin.Context, tx *sqlx.Tx, identity AccessIdentity, action, resource, resourceID, reason, before, after string) error {
	return insertAuditTx(c, tx, requestAudit(c, identity, action, resource, resourceID, reason, before, after))
}

func marshalSafe(value any) string {
	raw, _ := json.Marshal(value)
	return logging.RedactJSON(string(raw))
}
