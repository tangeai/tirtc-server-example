package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/model"
	"thing-connect/internal/store"
	"thing-connect/internal/tirtcapi"
)

// AgentHandler handles role CRUD (proxied to tange cloud) and device-role binding.
type AgentHandler struct {
	agentAPI          agentAPI
	roleStore         store.RoleBindingStore
	userRoleStore     store.UserRoleStore
	userResourceStore store.UserResourceStore
	defaultRoleID     string
	internalKey       string                         // X-Internal-Key for service-to-service calls
	resourceQuota     map[string]int                 // type -> per-user max (mcp/device_plugin/kb)
	defaultResources  map[string][]model.ResourceRef // type -> configured default {id,name} visible to all
}

func failAIUpstream(c *gin.Context, action string, err error) {
	slog.WarnContext(c.Request.Context(), "AI upstream request failed", "action", action, "err", err)
	reason := "AI 云服务暂不可用，请稍后重试"
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		reason = "AI 云服务请求超时，请稍后重试"
	}
	apiresp.Fail(c, http.StatusBadGateway, 50200, action+"失败："+reason)
}

// agentAPI is the subset of tirtcapi.AgentAPIClient used by AgentHandler.
type agentAPI interface {
	GetRole(ctx context.Context, roleID string) (*tirtcapi.Role, error)
	CreateRole(ctx context.Context, input tirtcapi.RoleInput) (*tirtcapi.Role, error)
	UpdateRole(ctx context.Context, roleID string, input tirtcapi.RoleInput) (*tirtcapi.Role, error)
	DeleteRole(ctx context.Context, roleID string) error
	ListVoices(ctx context.Context, language string) ([]tirtcapi.VoiceInfo, error)
	BatchCreateDeviceRoles(ctx context.Context, req tirtcapi.BatchDeviceRolesRequest) error
	BatchQueryDeviceRoles(ctx context.Context, req tirtcapi.BatchDeviceRolesQueryRequest) ([]tirtcapi.DeviceRoleBinding, error)
	BatchDeleteDeviceRoles(ctx context.Context, req tirtcapi.BatchDeviceRolesRequest) error
	ListGlobalMCPTools(ctx context.Context) ([]tirtcapi.MCPToolBrief, error)
	GetGlobalMCPTool(ctx context.Context, id string) (*tirtcapi.MCPToolBrief, error)
	ListAppMCPTools(ctx context.Context) ([]tirtcapi.AppMCPTool, error)
	CreateAppMCPTool(ctx context.Context, req tirtcapi.AppMCPToolCreateRequest) (*tirtcapi.AppMCPTool, error)
	GetAppMCPTool(ctx context.Context, id string) (*tirtcapi.AppMCPTool, error)
	UpdateAppMCPTool(ctx context.Context, id string, req tirtcapi.AppMCPToolUpdateRequest) (*tirtcapi.AppMCPTool, error)
	DeleteAppMCPTool(ctx context.Context, id string) error
	ListDevicePlugins(ctx context.Context) ([]tirtcapi.DevicePlugin, error)
	CreateDevicePlugin(ctx context.Context, req tirtcapi.DevicePluginCreateRequest) (*tirtcapi.DevicePlugin, error)
	GetDevicePlugin(ctx context.Context, id string) (*tirtcapi.DevicePlugin, error)
	UpdateDevicePlugin(ctx context.Context, id string, req tirtcapi.DevicePluginUpdateRequest) (*tirtcapi.DevicePlugin, error)
	DeleteDevicePlugin(ctx context.Context, id string) error
	ListKnowledgeIndexes(ctx context.Context, page, pageSize int) ([]tirtcapi.KnowledgeIndexInfo, int, error)
	CreateKnowledgeIndex(ctx context.Context, req tirtcapi.CreateKnowledgeIndexRequest) (*tirtcapi.KnowledgeIndexInfo, error)
	GetKnowledgeIndex(ctx context.Context, indexID string) (*tirtcapi.KnowledgeIndexInfo, error)
	UpdateKnowledgeIndex(ctx context.Context, indexID string, req tirtcapi.UpdateKnowledgeIndexRequest) (*tirtcapi.KnowledgeIndexInfo, error)
	DeleteKnowledgeIndex(ctx context.Context, indexID string) error
	ListKnowledgeDocuments(ctx context.Context, indexID string, page, pageSize int) ([]tirtcapi.KnowledgeDocument, int, error)
	ListKnowledgeFiles(ctx context.Context) ([]tirtcapi.KnowledgeFileInfo, error)
	UploadKnowledgeFile(ctx context.Context, fileName string, fileData []byte) (*tirtcapi.KnowledgeFileSimpleResponse, error)
	DeleteKnowledgeFile(ctx context.Context, fileID string) error
}

// NewAgentHandler creates an AgentHandler.
func NewAgentHandler(
	agentAPI agentAPI,
	roleStore store.RoleBindingStore,
	userRoleStore store.UserRoleStore,
	userResourceStore store.UserResourceStore,
	defaultRoleID, internalKey string,
	resourceQuota map[string]int,
	defaultResources map[string][]model.ResourceRef,
) *AgentHandler {
	return &AgentHandler{
		agentAPI:          agentAPI,
		roleStore:         roleStore,
		userRoleStore:     userRoleStore,
		userResourceStore: userResourceStore,
		defaultRoleID:     defaultRoleID,
		internalKey:       internalKey,
		resourceQuota:     resourceQuota,
		defaultResources:  defaultResources,
	}
}

// Register mounts agent routes under the gin engine (authenticated group).
func (h *AgentHandler) Register(r *gin.Engine, jwtSecret string) {
	auth := r.Group("/v1/ai", JWTAuth(jwtSecret))

	// Role CRUD (proxy to tange cloud)
	auth.GET("/roles", h.listRoles)
	auth.GET("/roles/default", h.getDefaultRole)
	auth.POST("/roles", h.createRole)
	auth.GET("/roles/:id", h.getRole)
	auth.PUT("/roles/:id", h.updateRole)
	auth.DELETE("/roles/:id", h.deleteRole)

	// Device-role binding (local MySQL)
	auth.GET("/device/:device_id/role", h.getDeviceRole)
	auth.PUT("/device/:device_id/role", h.setDeviceRole)
	auth.DELETE("/device/:device_id/role", h.deleteDeviceRole)

	// Device-role batch binding (proxy to tange cloud)
	auth.POST("/device-roles", h.batchCreateDeviceRoles)
	auth.POST("/device-roles/query", h.batchQueryDeviceRoles)
	auth.DELETE("/device-roles", h.batchDeleteDeviceRoles)

	// Voices
	auth.GET("/voices", h.listVoices)

	// MCP tools — global
	auth.GET("/mcp/tools", h.listGlobalMCPTools)
	auth.GET("/mcp/tools/:id", h.getGlobalMCPTool)

	// MCP tools — app
	auth.GET("/mcp/app-tools", h.listAppMCPTools)
	auth.POST("/mcp/app-tools", h.createAppMCPTool)
	auth.GET("/mcp/app-tools/:id", h.getAppMCPTool)
	auth.PUT("/mcp/app-tools/:id", h.updateAppMCPTool)
	auth.DELETE("/mcp/app-tools/:id", h.deleteAppMCPTool)

	// Device plugins
	auth.GET("/plugins", h.listPlugins)
	auth.POST("/plugins", h.createPlugin)
	auth.GET("/plugins/:id", h.getPlugin)
	auth.PUT("/plugins/:id", h.updatePlugin)
	auth.DELETE("/plugins/:id", h.deletePlugin)

	// Knowledge indexes
	auth.GET("/knowledge/indexes", h.listKnowledgeIndexes)
	auth.POST("/knowledge/indexes", h.createKnowledgeIndex)
	auth.GET("/knowledge/indexes/:id", h.getKnowledgeIndex)
	auth.PUT("/knowledge/indexes/:id", h.updateKnowledgeIndex)
	auth.DELETE("/knowledge/indexes/:id", h.deleteKnowledgeIndex)

	// Knowledge documents
	auth.GET("/knowledge/indexes/:id/documents", h.listKnowledgeDocuments)

	// Knowledge files
	auth.GET("/knowledge/files", h.listKnowledgeFiles)
	auth.POST("/knowledge/files", h.uploadKnowledgeFile)
	auth.DELETE("/knowledge/files/:id", h.deleteKnowledgeFile)

	// Internal — service-to-service (user-server calls on unbind)
	r.POST("/v1/ai/internal/unbind", h.internalUnbind)
}

// ── Internal handlers ──

func (h *AgentHandler) internalUnbind(c *gin.Context) {
	if h.internalKey == "" || c.GetHeader("X-Internal-Key") != h.internalKey {
		apiresp.Fail(c, http.StatusForbidden, 40301, "内部服务凭证无效")
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.DeviceID == "" {
		apiresp.BadParam(c, "缺少 device_id")
		return
	}
	// Clean up local ai_device_role
	if h.roleStore != nil {
		if err := h.roleStore.DeleteDeviceRole(c.Request.Context(), body.DeviceID); err != nil {
			apiresp.Internal(c, err.Error())
			return
		}
	}
	// Clean up cloud device-role bindings
	if h.agentAPI != nil {
		if err := h.agentAPI.BatchDeleteDeviceRoles(c.Request.Context(), tirtcapi.BatchDeviceRolesRequest{
			DeviceIDs: []string{body.DeviceID},
		}); err != nil {
			failAIUpstream(c, "解绑设备角色", err)
			return
		}
	}
	apiresp.OK(c, nil)
}

// ── Role CRUD ──

func (h *AgentHandler) listRoles(c *gin.Context) {
	userID := currentUserID(c)

	// Query local index for user's role IDs
	ids, err := h.userRoleStore.ListUserRoleIDs(c.Request.Context(), userID)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}

	// Fetch details from cloud for each role
	items := make([]*tirtcapi.Role, 0, len(ids))
	for _, id := range ids {
		role, err := h.agentAPI.GetRole(c.Request.Context(), id)
		if err != nil {
			// Skip roles that fail to fetch (e.g. deleted on cloud)
			continue
		}
		if role != nil {
			items = append(items, role)
		}
	}

	apiresp.OK(c, gin.H{"items": items, "total": len(items)})
}

func (h *AgentHandler) createRole(c *gin.Context) {
	userID := currentUserID(c)

	var input tirtcapi.RoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	if input.Name == "" {
		apiresp.BadParam(c, "缺少 name")
		return
	}

	created, err := h.agentAPI.CreateRole(c.Request.Context(), input)
	if err != nil {
		failAIUpstream(c, "创建角色", err)
		return
	}

	// Save local user→role mapping for listing
	if err := h.userRoleStore.AddUserRole(c.Request.Context(), userID, created.ID); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}

	// Fetch full details for the response
	role, err := h.agentAPI.GetRole(c.Request.Context(), created.ID)
	if err != nil || role == nil {
		// Return at least the ID
		apiresp.OK(c, created)
		return
	}
	apiresp.OK(c, role)
}

func (h *AgentHandler) getRole(c *gin.Context) {
	roleID := c.Param("id")
	if roleID == "" {
		apiresp.BadParam(c, "缺少角色 ID")
		return
	}
	if !h.requireOwnedRole(c, roleID) {
		return
	}

	role, err := h.agentAPI.GetRole(c.Request.Context(), roleID)
	if err != nil {
		failAIUpstream(c, "查询角色", err)
		return
	}
	apiresp.OK(c, role)
}

func (h *AgentHandler) updateRole(c *gin.Context) {
	roleID := c.Param("id")
	if roleID == "" {
		apiresp.BadParam(c, "缺少角色 ID")
		return
	}
	if !h.requireOwnedRole(c, roleID) {
		return
	}

	var input tirtcapi.RoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		apiresp.BadParamError(c, err)
		return
	}

	role, err := h.agentAPI.UpdateRole(c.Request.Context(), roleID, input)
	if err != nil {
		failAIUpstream(c, "更新角色", err)
		return
	}
	apiresp.OK(c, role)
}

func (h *AgentHandler) deleteRole(c *gin.Context) {
	roleID := c.Param("id")
	userID := currentUserID(c)
	if roleID == "" {
		apiresp.BadParam(c, "缺少角色 ID")
		return
	}
	if !h.requireOwnedRole(c, roleID) {
		return
	}

	if err := h.agentAPI.DeleteRole(c.Request.Context(), roleID); err != nil {
		failAIUpstream(c, "删除角色", err)
		return
	}
	// Remove local mapping (ignore error if it didn't exist)
	_ = h.userRoleStore.RemoveUserRole(c.Request.Context(), userID, roleID)
	apiresp.OK(c, nil)
}

func (h *AgentHandler) getDefaultRole(c *gin.Context) {
	if h.defaultRoleID == "" {
		apiresp.Fail(c, http.StatusNotFound, 40400, "未配置默认角色")
		return
	}

	role, err := h.agentAPI.GetRole(c.Request.Context(), h.defaultRoleID)
	if err != nil {
		failAIUpstream(c, "查询默认角色", err)
		return
	}
	if role == nil {
		apiresp.Fail(c, http.StatusNotFound, 40400, "AI 云服务中不存在默认角色")
		return
	}
	apiresp.OK(c, role)
}

// ── Device-role binding ──

func (h *AgentHandler) getDeviceRole(c *gin.Context) {
	deviceID := c.Param("device_id")
	if !h.requireOwnedDevice(c, deviceID) {
		return
	}

	resp := gin.H{"default_role_id": h.defaultRoleID}

	roleID, err := h.roleStore.GetDeviceRole(c.Request.Context(), deviceID)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	resp["role_id"] = roleID
	apiresp.OK(c, resp)
}

type setRoleReq struct {
	RoleID string `json:"role_id" binding:"required"`
}

func (h *AgentHandler) setDeviceRole(c *gin.Context) {
	deviceID := c.Param("device_id")
	userID := currentUserID(c)
	if !h.requireOwnedDevice(c, deviceID) {
		return
	}

	var req setRoleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	if !h.requireOwnedRole(c, req.RoleID) {
		return
	}

	if err := h.roleStore.SetDeviceRole(c.Request.Context(), deviceID, req.RoleID, userID); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, nil)
}

func (h *AgentHandler) deleteDeviceRole(c *gin.Context) {
	deviceID := c.Param("device_id")
	if !h.requireOwnedDevice(c, deviceID) {
		return
	}

	if err := h.roleStore.DeleteDeviceRole(c.Request.Context(), deviceID); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, nil)
}

// ── V2 Batch Device-Role Handlers ──

func (h *AgentHandler) batchCreateDeviceRoles(c *gin.Context) {
	var req tirtcapi.BatchDeviceRolesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	if len(req.DeviceIDs) == 0 {
		apiresp.BadParam(c, "缺少 device_ids")
		return
	}
	if req.RoleID == "" {
		apiresp.BadParam(c, "缺少 role_id")
		return
	}
	if !h.requireOwnedRole(c, req.RoleID) || !h.requireOwnedDevices(c, req.DeviceIDs) {
		return
	}
	if err := h.agentAPI.BatchCreateDeviceRoles(c.Request.Context(), req); err != nil {
		failAIUpstream(c, "批量绑定设备角色", err)
		return
	}
	apiresp.OK(c, nil)
}

func (h *AgentHandler) batchQueryDeviceRoles(c *gin.Context) {
	var req tirtcapi.BatchDeviceRolesQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	if len(req.DeviceIDs) == 0 {
		apiresp.BadParam(c, "缺少 device_ids")
		return
	}
	if !h.requireOwnedDevices(c, req.DeviceIDs) {
		return
	}
	items, err := h.agentAPI.BatchQueryDeviceRoles(c.Request.Context(), req)
	if err != nil {
		failAIUpstream(c, "批量查询设备角色", err)
		return
	}
	apiresp.OK(c, gin.H{"items": items})
}

func (h *AgentHandler) batchDeleteDeviceRoles(c *gin.Context) {
	var req tirtcapi.BatchDeviceRolesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	if len(req.DeviceIDs) == 0 {
		apiresp.BadParam(c, "缺少 device_ids")
		return
	}
	if !h.requireOwnedDevices(c, req.DeviceIDs) {
		return
	}
	if err := h.agentAPI.BatchDeleteDeviceRoles(c.Request.Context(), req); err != nil {
		failAIUpstream(c, "批量解绑设备角色", err)
		return
	}
	apiresp.OK(c, nil)
}

func (h *AgentHandler) requireOwnedRole(c *gin.Context, roleID string) bool {
	ok, err := h.userRoleStore.ExistsUserRole(c.Request.Context(), currentUserID(c), roleID)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return false
	}
	if !ok {
		apiresp.Fail(c, http.StatusForbidden, 40300, "角色不属于当前用户")
		return false
	}
	return true
}

func (h *AgentHandler) requireOwnedDevice(c *gin.Context, deviceID string) bool {
	ok, err := h.roleStore.UserOwnsDevice(c.Request.Context(), deviceID, currentUserID(c))
	if err != nil {
		apiresp.Internal(c, err.Error())
		return false
	}
	if !ok {
		apiresp.Fail(c, http.StatusForbidden, 40300, "设备不属于当前用户")
		return false
	}
	return true
}

func (h *AgentHandler) requireOwnedDevices(c *gin.Context, deviceIDs []string) bool {
	for _, deviceID := range deviceIDs {
		if deviceID == "" || !h.requireOwnedDevice(c, deviceID) {
			return false
		}
	}
	return true
}

// ── Voice Handler ──

func (h *AgentHandler) listVoices(c *gin.Context) {
	language := c.Query("language")
	voices, err := h.agentAPI.ListVoices(c.Request.Context(), language)
	if err != nil {
		failAIUpstream(c, "查询音色列表", err)
		return
	}
	apiresp.OK(c, gin.H{"items": voices})
}

// ── Global MCP Tool Handlers ──

func (h *AgentHandler) listGlobalMCPTools(c *gin.Context) {
	items, err := h.agentAPI.ListGlobalMCPTools(c.Request.Context())
	if err != nil {
		failAIUpstream(c, "查询全局 MCP 工具", err)
		return
	}
	apiresp.OK(c, gin.H{"items": items})
}

func (h *AgentHandler) getGlobalMCPTool(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		apiresp.BadParam(c, "缺少 id")
		return
	}
	item, err := h.agentAPI.GetGlobalMCPTool(c.Request.Context(), id)
	if err != nil {
		failAIUpstream(c, "查询全局 MCP 工具", err)
		return
	}
	if item == nil {
		apiresp.Fail(c, http.StatusNotFound, 40400, "MCP 工具不存在")
		return
	}
	apiresp.OK(c, item)
}

// ── App MCP Tool Handlers ──

func (h *AgentHandler) listAppMCPTools(c *gin.Context) {
	userID := currentUserID(c)
	items, err := h.listUserResources(c.Request.Context(), userID, model.ResourceTypeMCP)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"items": items})
}

// listUserResources returns a user's own resources of the given type plus the
// configured defaults, as lightweight {id,name} refs. Pure local — no cloud
// call; full details are fetched on demand via the single-resource GET endpoints.
// An id appearing as both user-owned and a configured default is merged once.
func (h *AgentHandler) listUserResources(ctx context.Context, userID int64, typ string) ([]model.ResourceRef, error) {
	rows, err := h.userResourceStore.List(ctx, userID, typ)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(rows))
	items := make([]model.ResourceRef, 0, len(rows))
	for _, r := range rows {
		if r.ResourceID == "" || seen[r.ResourceID] {
			continue
		}
		seen[r.ResourceID] = true
		items = append(items, model.ResourceRef{ID: r.ResourceID, Name: r.Name})
	}
	for _, d := range h.defaultResources[typ] {
		if d.ID == "" || seen[d.ID] {
			continue
		}
		seen[d.ID] = true
		items = append(items, d)
	}
	return items, nil
}

// checkQuota reports whether the user may create one more resource of typ.
// A quota of 0 or an unconfigured type is treated as unlimited.
func (h *AgentHandler) checkQuota(ctx context.Context, userID int64, typ string) (bool, error) {
	if max := h.resourceQuota[typ]; max > 0 {
		n, err := h.userResourceStore.Count(ctx, userID, typ)
		if err != nil {
			return false, err
		}
		if n >= max {
			return false, nil
		}
	}
	return true, nil
}

// requireOwnership checks that userID owns resource id of typ. Returns true if
// the caller should proceed; on non-ownership or error it writes the response
// and returns false. Used for update/delete (defaults are not user-mutable).
func (h *AgentHandler) requireOwnership(c *gin.Context, userID int64, typ, id string) bool {
	ok, err := h.userResourceStore.Exists(c.Request.Context(), userID, typ, id)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return false
	}
	if !ok {
		apiresp.Fail(c, http.StatusForbidden, 40300, "无权操作该资源")
		return false
	}
	return true
}

// canAccess reports whether userID may read resource id of typ: either they own
// it, or it is a configured default (visible to everyone). Used for get-detail.
func (h *AgentHandler) canAccess(ctx context.Context, userID int64, typ, id string) (bool, error) {
	ok, err := h.userResourceStore.Exists(ctx, userID, typ, id)
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}
	for _, d := range h.defaultResources[typ] {
		if d.ID == id {
			return true, nil
		}
	}
	return false, nil
}

func (h *AgentHandler) createAppMCPTool(c *gin.Context) {
	userID := currentUserID(c)
	var req tirtcapi.AppMCPToolCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	if req.Config == nil {
		apiresp.BadParam(c, "缺少 config")
		return
	}
	ok, err := h.checkQuota(c.Request.Context(), userID, model.ResourceTypeMCP)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	if !ok {
		apiresp.Fail(c, http.StatusTooManyRequests, 42900, "MCP 创建额度已用尽")
		return
	}
	item, err := h.agentAPI.CreateAppMCPTool(c.Request.Context(), req)
	if err != nil {
		failAIUpstream(c, "创建 MCP 工具", err)
		return
	}
	if err := h.userResourceStore.Add(c.Request.Context(), userID, model.ResourceTypeMCP, item.ID, req.Config.Name); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, item)
}

func (h *AgentHandler) getAppMCPTool(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		apiresp.BadParam(c, "缺少 id")
		return
	}
	userID := currentUserID(c)
	if ok, err := h.canAccess(c.Request.Context(), userID, model.ResourceTypeMCP, id); err != nil {
		apiresp.Internal(c, err.Error())
		return
	} else if !ok {
		apiresp.Fail(c, http.StatusForbidden, 40300, "无权访问该资源")
		return
	}
	item, err := h.agentAPI.GetAppMCPTool(c.Request.Context(), id)
	if err != nil {
		failAIUpstream(c, "查询 MCP 工具", err)
		return
	}
	if item == nil {
		apiresp.Fail(c, http.StatusNotFound, 40400, "MCP 工具不存在")
		return
	}
	apiresp.OK(c, item)
}

func (h *AgentHandler) updateAppMCPTool(c *gin.Context) {
	userID := currentUserID(c)
	id := c.Param("id")
	if id == "" {
		apiresp.BadParam(c, "缺少 id")
		return
	}
	if !h.requireOwnership(c, userID, model.ResourceTypeMCP, id) {
		return
	}
	var req tirtcapi.AppMCPToolUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	item, err := h.agentAPI.UpdateAppMCPTool(c.Request.Context(), id, req)
	if err != nil {
		failAIUpstream(c, "更新 MCP 工具", err)
		return
	}
	// Keep cached name in sync with cloud-side rename.
	if req.Config != nil {
		_ = h.userResourceStore.UpdateName(c.Request.Context(), userID, model.ResourceTypeMCP, id, req.Config.Name)
	}
	apiresp.OK(c, item)
}

func (h *AgentHandler) deleteAppMCPTool(c *gin.Context) {
	userID := currentUserID(c)
	id := c.Param("id")
	if id == "" {
		apiresp.BadParam(c, "缺少 id")
		return
	}
	if !h.requireOwnership(c, userID, model.ResourceTypeMCP, id) {
		return
	}
	if err := h.agentAPI.DeleteAppMCPTool(c.Request.Context(), id); err != nil {
		failAIUpstream(c, "删除 MCP 工具", err)
		return
	}
	_ = h.userResourceStore.Remove(c.Request.Context(), userID, model.ResourceTypeMCP, id)
	apiresp.OK(c, nil)
}

// ── Device Plugin Handlers ──

func (h *AgentHandler) listPlugins(c *gin.Context) {
	userID := currentUserID(c)
	items, err := h.listUserResources(c.Request.Context(), userID, model.ResourceTypeDevicePlugin)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"items": items})
}

func (h *AgentHandler) createPlugin(c *gin.Context) {
	userID := currentUserID(c)
	var req tirtcapi.DevicePluginCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	if req.Name == "" || req.Action == "" {
		apiresp.BadParam(c, "缺少 name 或 action")
		return
	}
	ok, err := h.checkQuota(c.Request.Context(), userID, model.ResourceTypeDevicePlugin)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	if !ok {
		apiresp.Fail(c, http.StatusTooManyRequests, 42900, "设备插件创建额度已用尽")
		return
	}
	item, err := h.agentAPI.CreateDevicePlugin(c.Request.Context(), req)
	if err != nil {
		failAIUpstream(c, "创建设备插件", err)
		return
	}
	if err := h.userResourceStore.Add(c.Request.Context(), userID, model.ResourceTypeDevicePlugin, item.ID, req.Name); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, item)
}

func (h *AgentHandler) getPlugin(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		apiresp.BadParam(c, "缺少 id")
		return
	}
	userID := currentUserID(c)
	if ok, err := h.canAccess(c.Request.Context(), userID, model.ResourceTypeDevicePlugin, id); err != nil {
		apiresp.Internal(c, err.Error())
		return
	} else if !ok {
		apiresp.Fail(c, http.StatusForbidden, 40300, "无权访问该资源")
		return
	}
	item, err := h.agentAPI.GetDevicePlugin(c.Request.Context(), id)
	if err != nil {
		failAIUpstream(c, "查询设备插件", err)
		return
	}
	if item == nil {
		apiresp.Fail(c, http.StatusNotFound, 40400, "设备插件不存在")
		return
	}
	apiresp.OK(c, item)
}

func (h *AgentHandler) updatePlugin(c *gin.Context) {
	userID := currentUserID(c)
	id := c.Param("id")
	if id == "" {
		apiresp.BadParam(c, "缺少 id")
		return
	}
	if !h.requireOwnership(c, userID, model.ResourceTypeDevicePlugin, id) {
		return
	}
	var req tirtcapi.DevicePluginUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	if req.Name == "" || req.Action == "" {
		apiresp.BadParam(c, "缺少 name 或 action")
		return
	}
	item, err := h.agentAPI.UpdateDevicePlugin(c.Request.Context(), id, req)
	if err != nil {
		failAIUpstream(c, "更新设备插件", err)
		return
	}
	_ = h.userResourceStore.UpdateName(c.Request.Context(), userID, model.ResourceTypeDevicePlugin, id, req.Name)
	apiresp.OK(c, item)
}

func (h *AgentHandler) deletePlugin(c *gin.Context) {
	userID := currentUserID(c)
	id := c.Param("id")
	if id == "" {
		apiresp.BadParam(c, "缺少 id")
		return
	}
	if !h.requireOwnership(c, userID, model.ResourceTypeDevicePlugin, id) {
		return
	}
	if err := h.agentAPI.DeleteDevicePlugin(c.Request.Context(), id); err != nil {
		failAIUpstream(c, "删除设备插件", err)
		return
	}
	_ = h.userResourceStore.Remove(c.Request.Context(), userID, model.ResourceTypeDevicePlugin, id)
	apiresp.OK(c, nil)
}

// ── Knowledge Index Handlers ──

func (h *AgentHandler) listKnowledgeIndexes(c *gin.Context) {
	userID := currentUserID(c)
	items, err := h.listUserResources(c.Request.Context(), userID, model.ResourceTypeKB)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, gin.H{"items": items, "total": len(items)})
}

func (h *AgentHandler) createKnowledgeIndex(c *gin.Context) {
	userID := currentUserID(c)
	var req tirtcapi.CreateKnowledgeIndexRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	if req.Name == "" || req.Description == "" {
		apiresp.BadParam(c, "缺少 name 或 description")
		return
	}
	ok, err := h.checkQuota(c.Request.Context(), userID, model.ResourceTypeKB)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	if !ok {
		apiresp.Fail(c, http.StatusTooManyRequests, 42900, "知识库创建额度已用尽")
		return
	}
	item, err := h.agentAPI.CreateKnowledgeIndex(c.Request.Context(), req)
	if err != nil {
		failAIUpstream(c, "创建知识库", err)
		return
	}
	if err := h.userResourceStore.Add(c.Request.Context(), userID, model.ResourceTypeKB, item.IndexID, req.Name); err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, item)
}

func (h *AgentHandler) getKnowledgeIndex(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		apiresp.BadParam(c, "缺少 id")
		return
	}
	userID := currentUserID(c)
	if ok, err := h.canAccess(c.Request.Context(), userID, model.ResourceTypeKB, id); err != nil {
		apiresp.Internal(c, err.Error())
		return
	} else if !ok {
		apiresp.Fail(c, http.StatusForbidden, 40300, "无权访问该资源")
		return
	}
	item, err := h.agentAPI.GetKnowledgeIndex(c.Request.Context(), id)
	if err != nil {
		failAIUpstream(c, "查询知识库", err)
		return
	}
	if item == nil {
		apiresp.Fail(c, http.StatusNotFound, 40400, "知识库不存在")
		return
	}
	apiresp.OK(c, item)
}

func (h *AgentHandler) updateKnowledgeIndex(c *gin.Context) {
	userID := currentUserID(c)
	id := c.Param("id")
	if id == "" {
		apiresp.BadParam(c, "缺少 id")
		return
	}
	if !h.requireOwnership(c, userID, model.ResourceTypeKB, id) {
		return
	}
	var req tirtcapi.UpdateKnowledgeIndexRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiresp.BadParamError(c, err)
		return
	}
	item, err := h.agentAPI.UpdateKnowledgeIndex(c.Request.Context(), id, req)
	if err != nil {
		failAIUpstream(c, "更新知识库", err)
		return
	}
	if req.Name != "" {
		_ = h.userResourceStore.UpdateName(c.Request.Context(), userID, model.ResourceTypeKB, id, req.Name)
	}
	apiresp.OK(c, item)
}

func (h *AgentHandler) deleteKnowledgeIndex(c *gin.Context) {
	userID := currentUserID(c)
	id := c.Param("id")
	if id == "" {
		apiresp.BadParam(c, "缺少 id")
		return
	}
	if !h.requireOwnership(c, userID, model.ResourceTypeKB, id) {
		return
	}
	if err := h.agentAPI.DeleteKnowledgeIndex(c.Request.Context(), id); err != nil {
		failAIUpstream(c, "删除知识库", err)
		return
	}
	_ = h.userResourceStore.Remove(c.Request.Context(), userID, model.ResourceTypeKB, id)
	apiresp.OK(c, nil)
}

// ── Knowledge Document Handler ──

func (h *AgentHandler) listKnowledgeDocuments(c *gin.Context) {
	indexID := c.Param("id")
	if indexID == "" {
		apiresp.BadParam(c, "缺少知识库 ID")
		return
	}
	userID := currentUserID(c)
	if ok, err := h.canAccess(c.Request.Context(), userID, model.ResourceTypeKB, indexID); err != nil {
		apiresp.Internal(c, err.Error())
		return
	} else if !ok {
		apiresp.Fail(c, http.StatusForbidden, 40300, "无权访问该知识库")
		return
	}
	page, pageSize := queryPagination(c)
	items, total, err := h.agentAPI.ListKnowledgeDocuments(c.Request.Context(), indexID, page, pageSize)
	if err != nil {
		failAIUpstream(c, "查询知识库文档", err)
		return
	}
	apiresp.OK(c, gin.H{"items": items, "total": total})
}

// ── Knowledge File Handlers ──

func (h *AgentHandler) listKnowledgeFiles(c *gin.Context) {
	userID := currentUserID(c)
	ownedRows, err := h.userResourceStore.List(
		c.Request.Context(), userID, model.ResourceTypeKBFile)
	if err != nil {
		apiresp.Internal(c, err.Error())
		return
	}
	if len(ownedRows) == 0 {
		apiresp.OK(c, gin.H{"items": []tirtcapi.KnowledgeFileInfo{}})
		return
	}

	items, err := h.agentAPI.ListKnowledgeFiles(c.Request.Context())
	if err != nil {
		failAIUpstream(c, "查询知识文件", err)
		return
	}
	ownedIDs := make(map[string]struct{}, len(ownedRows))
	for _, row := range ownedRows {
		if row.ResourceID != "" {
			ownedIDs[row.ResourceID] = struct{}{}
		}
	}
	filtered := make([]tirtcapi.KnowledgeFileInfo, 0, len(ownedIDs))
	for _, item := range items {
		if _, ok := ownedIDs[item.FileID]; ok {
			filtered = append(filtered, item)
		}
	}
	apiresp.OK(c, gin.H{"items": filtered})
}

func (h *AgentHandler) uploadKnowledgeFile(c *gin.Context) {
	userID := currentUserID(c)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		apiresp.BadParam(c, "缺少上传文件")
		return
	}
	defer file.Close()

	fileData, err := io.ReadAll(file)
	if err != nil {
		apiresp.BadParam(c, "读取上传文件失败")
		return
	}

	result, err := h.agentAPI.UploadKnowledgeFile(c.Request.Context(), header.Filename, fileData)
	if err != nil {
		failAIUpstream(c, "上传知识文件", err)
		return
	}
	if result == nil || result.FileID == "" {
		apiresp.Internal(c, "AI 云服务上传成功响应缺少 file_id")
		return
	}
	if err := h.userResourceStore.Add(
		c.Request.Context(),
		userID,
		model.ResourceTypeKBFile,
		result.FileID,
		header.Filename,
	); err != nil {
		// Do not leave a cloud file without an owner record. The cleanup is
		// best-effort; both errors remain server-side only.
		if cleanupErr := h.agentAPI.DeleteKnowledgeFile(
			c.Request.Context(), result.FileID); cleanupErr != nil {
			slog.ErrorContext(
				c.Request.Context(),
				"cleanup unowned knowledge file failed",
				"file_id",
				result.FileID,
				"err",
				cleanupErr,
			)
		}
		apiresp.Internal(c, err.Error())
		return
	}
	apiresp.OK(c, result)
}

func (h *AgentHandler) deleteKnowledgeFile(c *gin.Context) {
	userID := currentUserID(c)
	id := c.Param("id")
	if id == "" {
		apiresp.BadParam(c, "缺少 id")
		return
	}
	if !h.requireOwnership(c, userID, model.ResourceTypeKBFile, id) {
		return
	}
	if err := h.agentAPI.DeleteKnowledgeFile(c.Request.Context(), id); err != nil {
		failAIUpstream(c, "删除知识文件", err)
		return
	}
	if err := h.userResourceStore.Remove(
		c.Request.Context(), userID, model.ResourceTypeKBFile, id); err != nil {
		slog.ErrorContext(
			c.Request.Context(),
			"remove deleted knowledge file ownership failed",
			"file_id",
			id,
			"err",
			err,
		)
	}
	apiresp.OK(c, nil)
}

// queryPagination extracts page and page_size from the query string.
func queryPagination(c *gin.Context) (int, int) {
	page := 1
	pageSize := 20
	if v := c.Query("page"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("page_size"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 && n <= 100 {
			pageSize = n
		}
	}
	return page, pageSize
}

func parseInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
