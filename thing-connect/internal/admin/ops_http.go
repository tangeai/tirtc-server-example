package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/apiresp"
)

type dictType struct {
	ID        int64     `db:"id" json:"id"`
	Code      string    `db:"code" json:"code"`
	Name      string    `db:"name" json:"name"`
	Status    int8      `db:"status" json:"status"`
	Remark    string    `db:"remark" json:"remark"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}
type dictItem struct {
	ID           int64     `db:"id" json:"id"`
	DictTypeCode string    `db:"dict_type_code" json:"dict_type_code"`
	Label        string    `db:"label" json:"label"`
	Value        string    `db:"value" json:"value"`
	SortNo       int       `db:"sort_no" json:"sort_no"`
	IsDefault    int8      `db:"is_default" json:"is_default"`
	Status       int8      `db:"status" json:"status"`
	Extra        string    `db:"extra" json:"extra"`
	Remark       string    `db:"remark" json:"remark"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

func (s *HTTPServer) listDictTypes(c *gin.Context) {
	var items []dictType
	if err := s.store.db.SelectContext(c, &items, `SELECT id,code,name,status,remark,created_at,updated_at FROM admin_dict_types ORDER BY id DESC`); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"items": items})
}

type dictTypeRequest struct {
	Code   string `json:"code"`
	Name   string `json:"name" binding:"required"`
	Status int8   `json:"status"`
	Remark string `json:"remark"`
	Reason string `json:"reason" binding:"required"`
}

func (s *HTTPServer) createDictType(c *gin.Context) {
	var request dictTypeRequest
	if c.ShouldBindJSON(&request) != nil || !stableCode.MatchString(request.Code) || (request.Status != 0 && request.Status != 1) {
		apiresp.BadParam(c, "字典类型数据无效")
		return
	}
	identity, _ := identityFromContext(c)
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(c, `INSERT INTO admin_dict_types (code,name,status,remark) VALUES (?,?,?,?)`, request.Code, strings.TrimSpace(request.Name), request.Status, strings.TrimSpace(request.Remark))
	if err != nil {
		apiresp.BadParam(c, "字典编码已存在")
		return
	}
	id, _ := result.LastInsertId()
	if err := s.auditTx(c, tx, identity, "dictionary.type.create", "dict_type", request.Code, request.Reason, "", marshalSafe(request)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit dictionary")
		return
	}
	apiresp.OK(c, gin.H{"id": id})
}

func (s *HTTPServer) updateDictType(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	var request dictTypeRequest
	if err != nil || c.ShouldBindJSON(&request) != nil || (request.Status != 0 && request.Status != 1) {
		apiresp.BadParam(c, "字典类型数据无效")
		return
	}
	identity, _ := identityFromContext(c)
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	var before dictType
	if err := tx.GetContext(c, &before, `SELECT id,code,name,status,remark,created_at,updated_at FROM admin_dict_types WHERE id=? FOR UPDATE`, id); err != nil {
		c.JSON(404, apiresp.JSON{Code: 404, Msg: "字典类型不存在"})
		return
	}
	// Code is immutable after publication.
	if request.Code != "" && request.Code != before.Code {
		apiresp.BadParam(c, "字典编码发布后不可修改")
		return
	}
	if _, err := tx.ExecContext(c, `UPDATE admin_dict_types SET name=?,status=?,remark=? WHERE id=?`, strings.TrimSpace(request.Name), request.Status, strings.TrimSpace(request.Remark), id); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	if request.Status == 0 {
		_, _ = tx.ExecContext(c, `UPDATE admin_dict_items SET status=0 WHERE dict_type_code=?`, before.Code)
	}
	if err := s.auditTx(c, tx, identity, "dictionary.type.update", "dict_type", before.Code, request.Reason, marshalSafe(before), marshalSafe(request)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit dictionary")
		return
	}
	_ = s.redis.Del(c, "thingconnect:dictionary:"+before.Code).Err()
	apiresp.OK(c, gin.H{"id": id})
}

func (s *HTTPServer) listDictItems(c *gin.Context) {
	var items []dictItem
	if err := s.store.db.SelectContext(c, &items, `SELECT id,dict_type_code,label,value,sort_no,is_default,status,extra,remark,created_at,updated_at FROM admin_dict_items WHERE dict_type_code=? ORDER BY sort_no,id`, c.Param("code")); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"items": items})
}

type dictItemRequest struct {
	Label     string          `json:"label" binding:"required"`
	Value     string          `json:"value" binding:"required"`
	SortNo    int             `json:"sort_no"`
	IsDefault int8            `json:"is_default"`
	Status    int8            `json:"status"`
	Extra     json.RawMessage `json:"extra"`
	Remark    string          `json:"remark"`
	Reason    string          `json:"reason" binding:"required"`
}

func (s *HTTPServer) createDictItem(c *gin.Context) { s.saveDictItem(c, 0, c.Param("code")) }
func (s *HTTPServer) updateDictItem(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		apiresp.BadParam(c, "字典项 ID 无效")
		return
	}
	s.saveDictItem(c, id, "")
}
func (s *HTTPServer) saveDictItem(c *gin.Context, id int64, code string) {
	var request dictItemRequest
	if c.ShouldBindJSON(&request) != nil || (request.Status != 0 && request.Status != 1) || (request.IsDefault != 0 && request.IsDefault != 1) || !validExtra(request.Extra) {
		apiresp.BadParam(c, "字典项数据无效")
		return
	}
	identity, _ := identityFromContext(c)
	tx, err := s.store.db.BeginTxx(c, nil)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	defer tx.Rollback()
	before := ""
	if id > 0 {
		var item dictItem
		if err := tx.GetContext(c, &item, `SELECT id,dict_type_code,label,value,sort_no,is_default,status,extra,remark,created_at,updated_at FROM admin_dict_items WHERE id=? FOR UPDATE`, id); err != nil {
			c.JSON(404, apiresp.JSON{Code: 404, Msg: "字典项不存在"})
			return
		}
		code = item.DictTypeCode
		before = marshalSafe(item)
		if request.Value != item.Value {
			apiresp.BadParam(c, "字典项值发布后不可修改")
			return
		}
	}
	var typeStatus int8
	if err := tx.GetContext(c, &typeStatus, `SELECT status FROM admin_dict_types WHERE code=? FOR UPDATE`, code); err != nil || typeStatus != 1 {
		apiresp.BadParam(c, "字典类型不存在或已停用")
		return
	}
	if request.IsDefault == 1 && request.Status == 1 {
		if _, err := tx.ExecContext(c, `UPDATE admin_dict_items SET is_default=0 WHERE dict_type_code=? AND id<>?`, code, id); err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
	}
	extra := string(request.Extra)
	if len(request.Extra) == 0 {
		extra = "{}"
	}
	if id == 0 {
		result, err := tx.ExecContext(c, `INSERT INTO admin_dict_items (dict_type_code,label,value,sort_no,is_default,status,extra,remark) VALUES (?,?,?,?,?,?,?,?)`, code, strings.TrimSpace(request.Label), request.Value, request.SortNo, request.IsDefault, request.Status, extra, strings.TrimSpace(request.Remark))
		if err != nil {
			apiresp.BadParam(c, "同一字典内的值必须唯一")
			return
		}
		id, _ = result.LastInsertId()
	} else {
		if _, err := tx.ExecContext(c, `UPDATE admin_dict_items SET label=?,sort_no=?,is_default=?,status=?,extra=?,remark=? WHERE id=?`, strings.TrimSpace(request.Label), request.SortNo, request.IsDefault, request.Status, extra, strings.TrimSpace(request.Remark), id); err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
	}
	if err := s.auditTx(c, tx, identity, "dictionary.item.write", "dict_item", strconv.FormatInt(id, 10), request.Reason, before, marshalSafe(request)); err != nil || tx.Commit() != nil {
		apiresp.Internal(c, "commit dictionary item")
		return
	}
	_ = s.redis.Del(c, "thingconnect:dictionary:"+code).Err()
	apiresp.OK(c, gin.H{"id": id})
}

func (s *HTTPServer) activeDictionary(c *gin.Context) {
	code := c.Param("code")
	key := "thingconnect:dictionary:" + code
	if cached, err := s.redis.Get(c, key).Bytes(); err == nil {
		var items []dictItem
		if json.Unmarshal(cached, &items) == nil {
			apiresp.OK(c, gin.H{"code": code, "items": items})
			return
		}
	}
	var items []dictItem
	err := s.store.db.SelectContext(c, &items, `SELECT i.id,i.dict_type_code,i.label,i.value,i.sort_no,i.is_default,i.status,i.extra,i.remark,i.created_at,i.updated_at FROM admin_dict_items i JOIN admin_dict_types t ON t.code=i.dict_type_code WHERE i.dict_type_code=? AND i.status=1 AND t.status=1 ORDER BY i.sort_no,i.id`, code)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	raw, _ := json.Marshal(items)
	_ = s.redis.Set(c, key, raw, 5*time.Minute).Err()
	apiresp.OK(c, gin.H{"code": code, "items": items})
}

func (s *HTTPServer) loginLogs(c *gin.Context) {
	page, size := pageParams(c)
	where, args := ` WHERE 1=1`, []any{}
	if q := strings.TrimSpace(c.Query("email")); q != "" {
		where += ` AND COALESCE(NULLIF(l.email,''),a.email,'') LIKE ?`
		args = append(args, "%"+q+"%")
	}
	if status := c.Query("status"); status == "0" || status == "1" {
		where += ` AND l.status=?`
		args = append(args, status)
	}
	if err := appendDateRange(c, "l.created_at", &where, &args); err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	from := ` FROM admin_login_log l LEFT JOIN admin_users a ON a.id=l.admin_user_id`
	var total int
	_ = s.store.db.GetContext(c, &total, `SELECT COUNT(*)`+from+where, args...)
	type row struct {
		ID          int64     `db:"id" json:"id"`
		AdminUserID int64     `db:"admin_user_id" json:"admin_user_id"`
		Email       string    `db:"email" json:"email"`
		ClientIP    string    `db:"client_ip" json:"client_ip"`
		UserAgent   string    `db:"user_agent" json:"user_agent"`
		Status      int8      `db:"status" json:"status"`
		Message     string    `db:"message" json:"message"`
		CreatedAt   time.Time `db:"created_at" json:"created_at"`
	}
	var items []row
	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	query := `SELECT l.id,l.admin_user_id,COALESCE(NULLIF(l.email,''),a.email,'') email,l.client_ip,l.user_agent,l.status,l.message,l.created_at` + from + where + ` ORDER BY l.id DESC LIMIT ? OFFSET ?`
	if err := s.store.db.SelectContext(c, &items, query, queryArgs...); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, pageResult{Items: items, Page: page, PageSize: size, Total: total})
}

func (s *HTTPServer) auditLogs(c *gin.Context) {
	page, size := pageParams(c)
	where, args := ` WHERE 1=1`, []any{}
	if action := strings.TrimSpace(c.Query("action")); action != "" {
		where += ` AND l.action=?`
		args = append(args, action)
	}
	if resource := strings.TrimSpace(c.Query("resource_type")); resource != "" {
		where += ` AND l.resource_type=?`
		args = append(args, resource)
	}
	if resourceID := strings.TrimSpace(c.Query("resource_id")); resourceID != "" {
		where += ` AND l.resource_id=?`
		args = append(args, resourceID)
	}
	if adminUserID := strings.TrimSpace(c.Query("admin_user_id")); adminUserID != "" {
		parsed, err := strconv.ParseInt(adminUserID, 10, 64)
		if err != nil || parsed <= 0 {
			apiresp.BadParam(c, "管理员 ID 无效")
			return
		}
		where += ` AND l.admin_user_id=?`
		args = append(args, parsed)
	}
	if err := appendDateRange(c, "l.created_at", &where, &args); err != nil {
		apiresp.BadParam(c, err.Error())
		return
	}
	from := ` FROM admin_audit_log l LEFT JOIN admin_users a ON a.id=l.admin_user_id`
	var total int
	_ = s.store.db.GetContext(c, &total, `SELECT COUNT(*)`+from+where, args...)
	type auditRow struct {
		ID           int64     `db:"id" json:"id"`
		AdminUserID  int64     `db:"admin_user_id" json:"admin_user_id"`
		Email        string    `db:"email" json:"email"`
		RoleCodes    string    `db:"role_codes" json:"role_codes"`
		RequestID    string    `db:"request_id" json:"request_id"`
		Method       string    `db:"method" json:"method"`
		Path         string    `db:"path" json:"path"`
		HTTPStatus   int       `db:"http_status" json:"http_status"`
		LatencyMS    int64     `db:"latency_ms" json:"latency_ms"`
		Action       string    `db:"action" json:"action"`
		ResourceType string    `db:"resource_type" json:"resource_type"`
		ResourceID   string    `db:"resource_id" json:"resource_id"`
		Reason       string    `db:"reason" json:"reason"`
		BeforeValue  *string   `db:"before_value" json:"before_value,omitempty"`
		AfterValue   *string   `db:"after_value" json:"after_value,omitempty"`
		ClientIP     string    `db:"client_ip" json:"client_ip"`
		Success      int8      `db:"success" json:"success"`
		ErrorMessage string    `db:"error_message" json:"error_message"`
		CreatedAt    time.Time `db:"created_at" json:"created_at"`
	}
	var items []auditRow
	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	if err := s.store.db.SelectContext(c, &items, `SELECT l.id,l.admin_user_id,COALESCE(a.email,'') email,l.role_codes,l.request_id,l.method,l.path,l.http_status,l.latency_ms,l.action,l.resource_type,l.resource_id,l.reason,l.before_value,l.after_value,l.client_ip,l.success,l.error_message,l.created_at`+from+where+` ORDER BY l.id DESC LIMIT ? OFFSET ?`, queryArgs...); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, pageResult{Items: items, Page: page, PageSize: size, Total: total})
}

type voipApp struct {
	AppID            string    `json:"app_id"`
	Enabled          bool      `json:"enabled"`
	ModelID          string    `json:"model_id"`
	IsDefault        bool      `json:"is_default"`
	SecretConfigured bool      `json:"secret_configured"`
	DeviceCount      int       `json:"device_count"`
	ActiveAuthCount  int       `json:"active_auth_count"`
	InvalidAuthCount int       `json:"invalid_auth_count"`
	ConfigStatus     string    `json:"config_status"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (s *HTTPServer) voipApps(c *gin.Context) {
	entry, err := s.configs.Get(c, "voip-server", "wechat.apps", "global", "")
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	var value struct {
		DefaultAppID string `json:"default_app_id"`
		Apps         map[string]struct {
			Enabled bool   `json:"enabled"`
			ModelID string `json:"model_id"`
		} `json:"apps"`
	}
	if json.Unmarshal(entry.Value, &value) != nil {
		apiresp.Internal(c, "invalid stored wechat config")
		return
	}
	var secretValue struct {
		Apps map[string]struct {
			Secret string `json:"secret"`
		} `json:"apps"`
	}
	if _, secrets, _, resolveErr := s.configs.Resolved(c, "voip-server", "wechat.apps", "global", ""); resolveErr == nil && len(secrets) > 0 {
		_ = json.Unmarshal(secrets, &secretValue)
	}
	items := make([]voipApp, 0, len(value.Apps))
	for appID, app := range value.Apps {
		item := voipApp{AppID: appID, Enabled: app.Enabled, ModelID: app.ModelID, IsDefault: appID == value.DefaultAppID, SecretConfigured: strings.TrimSpace(secretValue.Apps[appID].Secret) != "", UpdatedAt: entry.UpdatedAt}
		_ = s.store.db.GetContext(c, &item.DeviceCount, `SELECT COUNT(DISTINCT device_id) FROM voip_device_auth WHERE wx_app_id=?`, appID)
		_ = s.store.db.GetContext(c, &item.ActiveAuthCount, `SELECT COUNT(*) FROM voip_device_auth WHERE wx_app_id=? AND auth_status='active'`, appID)
		_ = s.store.db.GetContext(c, &item.InvalidAuthCount, `SELECT COUNT(*) FROM voip_device_auth WHERE wx_app_id=? AND auth_status='invalid'`, appID)
		item.ConfigStatus = "disabled"
		if item.Enabled && item.SecretConfigured && strings.TrimSpace(item.ModelID) != "" {
			item.ConfigStatus = "healthy"
		} else if item.Enabled {
			item.ConfigStatus = "incomplete"
		}
		items = append(items, item)
	}
	apiresp.OK(c, gin.H{"items": items, "revision": entry.Revision})
}

func (s *HTTPServer) voipApp(c *gin.Context) {
	appID := c.Param("app_id")
	entry, err := s.configs.Get(c, "voip-server", "wechat.apps", "global", "")
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	var value struct {
		DefaultAppID string                     `json:"default_app_id"`
		Apps         map[string]json.RawMessage `json:"apps"`
	}
	if json.Unmarshal(entry.Value, &value) != nil {
		apiresp.Internal(c, "invalid stored config")
		return
	}
	raw, ok := value.Apps[appID]
	if !ok {
		c.JSON(404, apiresp.JSON{Code: 404, Msg: "微信应用不存在"})
		return
	}
	secretConfigured := false
	if _, secrets, _, resolveErr := s.configs.Resolved(c, "voip-server", "wechat.apps", "global", ""); resolveErr == nil && len(secrets) > 0 {
		var secretValue struct {
			Apps map[string]struct {
				Secret string `json:"secret"`
			} `json:"apps"`
		}
		if json.Unmarshal(secrets, &secretValue) == nil {
			secretConfigured = strings.TrimSpace(secretValue.Apps[appID].Secret) != ""
		}
	}
	apiresp.OK(c, gin.H{"app_id": appID, "is_default": appID == value.DefaultAppID, "value": raw, "secret_configured": secretConfigured, "revision": entry.Revision})
}

func (s *HTTPServer) voipAppDevices(c *gin.Context) {
	page, size := pageParams(c)
	appID := c.Param("app_id")
	var total int
	_ = s.store.db.GetContext(c, &total, `SELECT COUNT(*) FROM voip_device_auth WHERE wx_app_id=?`, appID)
	type row struct {
		ID               int64      `db:"id" json:"id"`
		DeviceID         string     `db:"device_id" json:"device_id"`
		WxOpenID         string     `db:"wx_open_id" json:"wx_open_id"`
		WxModelID        string     `db:"wx_model_id" json:"wx_model_id"`
		Remark           string     `db:"remark" json:"remark"`
		DeviceName       string     `db:"authorized_device_name" json:"authorized_device_name"`
		OwnerUserID      int64      `db:"owner_user_id" json:"owner_user_id"`
		OwnerEmail       string     `db:"owner_email" json:"owner_email"`
		AuthStatus       string     `db:"auth_status" json:"auth_status"`
		InvalidReason    string     `db:"invalid_reason" json:"invalid_reason"`
		InvalidAt        *time.Time `db:"invalid_at" json:"invalid_at,omitempty"`
		LastVerifiedAt   *time.Time `db:"last_verified_at" json:"last_verified_at,omitempty"`
		ProfileUpdatedAt *time.Time `db:"profile_updated_at" json:"profile_updated_at,omitempty"`
		CreatedAt        time.Time  `db:"created_at" json:"created_at"`
	}
	var items []row
	if err := s.store.db.SelectContext(c, &items, `SELECT a.id,a.device_id,a.wx_open_id,a.wx_model_id,a.remark,a.authorized_device_name,COALESCE(d.user_id,0) owner_user_id,COALESCE(u.email,'') owner_email,a.auth_status,a.invalid_reason,a.invalid_at,a.last_verified_at,p.updated_at profile_updated_at,a.created_at FROM voip_device_auth a LEFT JOIN device_bind d ON d.device_id=a.device_id LEFT JOIN users u ON u.id=d.user_id LEFT JOIN voip_device_profile p ON p.device_id=a.device_id WHERE a.wx_app_id=? ORDER BY a.id DESC LIMIT ? OFFSET ?`, appID, size, (page-1)*size); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, pageResult{Items: items, Page: page, PageSize: size, Total: total})
}

func (s *HTTPServer) voipDeviceProfile(c *gin.Context) {
	var row struct {
		DeviceID  string    `db:"device_id" json:"device_id"`
		Profile   string    `db:"profile" json:"profile"`
		UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
	}
	err := s.store.db.GetContext(c, &row, `SELECT device_id,profile,updated_at FROM voip_device_profile WHERE device_id=?`, c.Param("device_id"))
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, apiresp.JSON{Code: 404, Msg: "设备未上报 VoIP 属性"})
		return
	}
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	var attributes any
	if json.Unmarshal([]byte(row.Profile), &attributes) != nil {
		attributes = row.Profile
	}
	apiresp.OK(c, gin.H{"device_id": row.DeviceID, "profile": row.Profile, "attributes": attributes, "updated_at": row.UpdatedAt})
}

func validExtra(raw json.RawMessage) bool {
	return len(raw) == 0 || (json.Valid(raw) && strings.HasPrefix(strings.TrimSpace(string(raw)), "{"))
}
