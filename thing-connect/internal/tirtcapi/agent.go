// Package tirtcapi implements TGV1-HMAC-SHA256 signed clients for tange.ai APIs.
package tirtcapi

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// AgentAPIConfig holds credentials and endpoint for the tange cloud Role API.
type AgentAPIConfig struct {
	BaseURL     string
	AppID       string
	AccessKeyID string
	SecretKeyID string
}

// AgentAPIClient calls the tange cloud Role CRUD API.
type AgentAPIClient struct {
	cfg    AgentAPIConfig
	client *http.Client
}

// NewAgentAPIClient creates a client. If client is nil, http.DefaultClient is used.
func NewAgentAPIClient(cfg AgentAPIConfig, client *http.Client) *AgentAPIClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &AgentAPIClient{cfg: cfg, client: client}
}

// ── API DTOs ──

// Role is the role-config response from GET /ai/aigcrtc/roles/{id}.
// Matches the actual RoleConfig API structure (agent_config + service_config).
type Role struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name,omitempty"`
	Avatar        string                 `json:"avatar,omitempty"`
	AppID         string                 `json:"app_id,omitempty"`
	UserID        string                 `json:"user_id,omitempty"`
	ParentRoleID  string                 `json:"parent_role_id,omitempty"`
	AgentConfig   *AgentConfig           `json:"agent_config,omitempty"`
	ServiceConfig *ServiceConfig         `json:"service_config,omitempty"`
	UserParams    map[string]interface{} `json:"user_params,omitempty"`
	CreatedAt     string                 `json:"created_at,omitempty"`
	UpdatedAt     string                 `json:"updated_at,omitempty"`
}

// AgentConfig holds the AI agent behaviour configuration.
// AliRAG intentionally lacks omitempty: the frontend sends an explicit
// null to clear a previously bound knowledge base, and omitempty would
// silently drop that signal (indistinguishable from "field not provided").
type AgentConfig struct {
	Prompt        string            `json:"prompt,omitempty"`
	WelcomeText   string            `json:"welcome_text,omitempty"`
	AliRAG        *RoleAliRAGConfig `json:"ali_rag"`
	AliMemory     *AliMemoryCfg     `json:"ali_memory,omitempty"`
	MCPTools      []string          `json:"mcp_tools,omitempty"`
	DevicePlugins []string          `json:"device_plugins,omitempty"`
}

// RoleAliRAGConfig is the knowledge base (RAG) configuration.
type RoleAliRAGConfig struct {
	IndexID string `json:"index_id"`
}

// AliMemoryCfg is the Alibaba memory configuration.
type AliMemoryCfg struct {
	Enable bool `json:"enable"`
}

// ServiceConfig holds the LLM/TTS service configuration.
// Config is a json.RawMessage whose shape depends on Type:
//
//	"custom-compose" → CustomComposeConfig
//	"coze"           → CozeConfig
//	"qwen-agent"     → QwenAgentConfig
type ServiceConfig struct {
	Name   string          `json:"name,omitempty"`
	Type   string          `json:"type,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
}

// CustomComposeConfig is the config for type="custom-compose".
type CustomComposeConfig struct {
	TTS *AliTtsComponent `json:"tts,omitempty"`
}

// AliTtsComponent is a TTS provider using Alibaba CosyVoice.
type AliTtsComponent struct {
	Provider       string              `json:"provider,omitempty"`
	ProviderParams AliTtsProviderParams `json:"provider_params,omitempty"`
}

// AliTtsProviderParams are the Alibaba TTS provider parameters.
type AliTtsProviderParams struct {
	Voice         string   `json:"voice,omitempty"`
	Volume        int      `json:"volume,omitempty"`
	Rate          float64  `json:"rate,omitempty"`
	Pitch         float64  `json:"pitch,omitempty"`
	LanguageHints []string `json:"language_hints,omitempty"`
}

// CozeConfig is the config for type="coze".
type CozeConfig struct {
	AppID       string `json:"app_id"`
	BotID       string `json:"bot_id"`
	PrivateKey  string `json:"private_key"`
	WorkflowID  string `json:"workflow_id,omitempty"`
	PublicKeyID string `json:"public_key_id"`
}

// QwenAgentConfig is the config for type="qwen-agent".
type QwenAgentConfig struct {
	AppID       string `json:"app_id"`
	APIKey      string `json:"api_key,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// ── V2 Device-Role DTOs ──

// BatchDeviceRolesRequest is the body for batch create/delete device-role bindings.
type BatchDeviceRolesRequest struct {
	DeviceIDs []string `json:"device_ids"`
	RoleID    string   `json:"role_id,omitempty"`
}

// BatchDeviceRolesQueryRequest is the body for batch query device-role bindings.
type BatchDeviceRolesQueryRequest struct {
	DeviceIDs []string `json:"device_ids"`
}

// DeviceRoleBinding is one device-role mapping returned by the V2 cloud API.
type DeviceRoleBinding struct {
	DeviceID  string `json:"device_id"`
	RoleID    string `json:"role_id"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// ── Voice DTOs ──

// VoiceInfo is a TTS voice entry.
type VoiceInfo struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Languages  []string `json:"languages,omitempty"`
	Model      string   `json:"model,omitempty"`
	Scene      string   `json:"scene,omitempty"`
	SampleURL  string   `json:"sample_url,omitempty"`
}

// ── MCP Tool DTOs ──

// MCPToolBrief is a global MCP tool summary.
type MCPToolBrief struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// MCPAuthenticationConfig holds auth for an MCP server.
type MCPAuthenticationConfig struct {
	Type        string `json:"type,omitempty"`
	BearerToken string `json:"bearer_token,omitempty"`
}

// MCPToolExternalRuntimeConfig is the MCP tool runtime config (used in create/update/response).
type MCPToolExternalRuntimeConfig struct {
	Enable         bool                      `json:"enable,omitempty"`
	Type           string                    `json:"type,omitempty"`
	Name           string                    `json:"name"`
	Description    string                    `json:"description,omitempty"`
	URL            string                    `json:"url"`
	Authentication *MCPAuthenticationConfig  `json:"authentication,omitempty"`
}

// AppMCPToolCreateRequest is the body for creating an app MCP tool.
type AppMCPToolCreateRequest struct {
	Config  *MCPToolExternalRuntimeConfig `json:"config"`
	Enabled *bool                         `json:"enabled,omitempty"`
}

// AppMCPToolUpdateRequest is the body for updating an app MCP tool.
type AppMCPToolUpdateRequest struct {
	Config  *MCPToolExternalRuntimeConfig `json:"config,omitempty"`
	Enabled *bool                         `json:"enabled,omitempty"`
}

// AppMCPTool is the full app MCP tool resource.
type AppMCPTool struct {
	ID      string                        `json:"id"`
	AppID   string                        `json:"app_id,omitempty"`
	Config  *MCPToolExternalRuntimeConfig `json:"config,omitempty"`
	Enabled bool                          `json:"enabled"`
}

// ── Device Plugin DTOs ──

// DevicePluginParam is a single input or output parameter definition.
type DevicePluginParam struct {
	Name         string   `json:"name,omitempty"`
	Type         string   `json:"type,omitempty"`
	Description  string   `json:"description,omitempty"`
	DefaultValue string   `json:"default_value,omitempty"`
	Required     bool     `json:"required"`
	Enum         []string `json:"enum,omitempty"`
}

// DevicePluginCreateRequest is the body for creating a device plugin.
type DevicePluginCreateRequest struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Action      string              `json:"action"`
	InputParams []DevicePluginParam `json:"input_params,omitempty"`
	ReturnParams []DevicePluginParam `json:"return_params,omitempty"`
}

// DevicePluginUpdateRequest is the body for updating a device plugin.
type DevicePluginUpdateRequest struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Action      string              `json:"action"`
	InputParams []DevicePluginParam `json:"input_params,omitempty"`
	ReturnParams []DevicePluginParam `json:"return_params,omitempty"`
}

// DevicePlugin is the full device plugin resource.
type DevicePlugin struct {
	ID          string              `json:"id"`
	AppID       string              `json:"app_id,omitempty"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Action      string              `json:"action"`
	InputParams []DevicePluginParam `json:"input_params,omitempty"`
	ReturnParams []DevicePluginParam `json:"return_params,omitempty"`
	CreatedAt   string              `json:"created_at,omitempty"`
	UpdatedAt   string              `json:"updated_at,omitempty"`
}

// ── Knowledge DTOs ──

// CreateKnowledgeIndexRequest is the body for creating a knowledge index.
type CreateKnowledgeIndexRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// UpdateKnowledgeIndexRequest is the body for updating a knowledge index.
type UpdateKnowledgeIndexRequest struct {
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	DocumentIDs []string `json:"document_ids,omitempty"`
}

// KnowledgeIndexInfo is a knowledge index detail.
type KnowledgeIndexInfo struct {
	IndexID     string   `json:"index_id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	DocumentIDs []string `json:"document_ids,omitempty"`
}

// KnowledgeDocument is a document within a knowledge index.
type KnowledgeDocument struct {
	DocumentID string `json:"document_id"`
	Name       string `json:"name"`
	Status     string `json:"status,omitempty"`
	Size       int    `json:"size,omitempty"`
	ModifiedAt int64  `json:"modified_at,omitempty"`
}

// KnowledgeFileInfo is an uploaded raw file.
type KnowledgeFileInfo struct {
	FileID      string `json:"file_id"`
	FileName    string `json:"file_name"`
	FileType    string `json:"file_type,omitempty"`
	Status      string `json:"status,omitempty"`
	SizeInBytes int64  `json:"size_in_bytes,omitempty"`
	CreateTime  string `json:"create_time,omitempty"`
}

// KnowledgeFileSimpleResponse is the upload/create response that only contains file_id.
type KnowledgeFileSimpleResponse struct {
	FileID string `json:"file_id"`
}

// RoleInput is the request body for create/update role (POST/PUT).
// Matches the RoleConfig API input structure.
type RoleInput struct {
	Name          string                 `json:"name,omitempty"`
	Avatar        string                 `json:"avatar,omitempty"`
	ParentRoleID  string                 `json:"parent_role_id,omitempty"`
	AgentConfig   *AgentConfig           `json:"agent_config,omitempty"`
	ServiceConfig *ServiceConfig         `json:"service_config,omitempty"`
	UserParams    map[string]interface{} `json:"user_params,omitempty"`
}

// ── API response wrappers ──

type roleResp struct {
	Code int32           `json:"code"`
	Msg  string          `json:"msg,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

type roleListData struct {
	Items []Role `json:"items"`
	Total int    `json:"total"`
}

// ── API methods ──

const rolesPath = "/ai/aigcrtc/roles"

// ListRoles fetches the role list with pagination.
func (c *AgentAPIClient) ListRoles(ctx context.Context, page, pageSize int) ([]Role, int, error) {
	path := rolesPath
	query := fmt.Sprintf("page=%d&page_size=%d", page, pageSize)

	start := time.Now()
	items, total, err := c.listRoles(ctx, path, query)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent ListRoles failed", "page", page, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent ListRoles ok", "page", page, "total", total, "dur_ms", durMs)
	}
	return items, total, err
}

func (c *AgentAPIClient) listRoles(ctx context.Context, path, query string) ([]Role, int, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr+"?"+query, nil)
	if err != nil {
		return nil, 0, err
	}

	c.signRequest(req, http.MethodGet, path, query, nil)

	res, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("agent: list roles: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, 0, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}

	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, 0, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, 0, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}

	var data roleListData
	if err := json.Unmarshal(wrap.Data, &data); err != nil {
		return nil, 0, fmt.Errorf("agent: parse data: %w", err)
	}
	return data.Items, data.Total, nil
}

// GetRole fetches a single role by ID.
func (c *AgentAPIClient) GetRole(ctx context.Context, roleID string) (*Role, error) {
	path := rolesPath + "/" + roleID
	start := time.Now()

	role, err := c.doGet(ctx, path)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent GetRole failed", "id", roleID, "dur_ms", durMs, "err", err)
	} else if role != nil {
		respJSON, _ := json.Marshal(role)
		slog.InfoContext(ctx, "agent GetRole ok", "id", roleID, "resp", string(respJSON), "dur_ms", durMs)
	} else {
		slog.InfoContext(ctx, "agent GetRole ok", "id", roleID, "resp", "null", "dur_ms", durMs)
	}
	return role, err
}

func (c *AgentAPIClient) doGet(ctx context.Context, path string) (*Role, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}

	c.signRequest(req, http.MethodGet, path, "", nil)

	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: get role: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}

	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}

	// Data may be null when the role is not found.
	if len(wrap.Data) == 0 || string(wrap.Data) == "null" {
		return nil, nil
	}

	var role Role
	if err := json.Unmarshal(wrap.Data, &role); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return &role, nil
}

// CreateRole creates a new role on the tange cloud.
func (c *AgentAPIClient) CreateRole(ctx context.Context, input RoleInput) (*Role, error) {
	start := time.Now()
	role, err := c.doCreate(ctx, rolesPath, input)
	durMs := time.Since(start).Milliseconds()
	reqJSON, _ := json.Marshal(input)
	if err != nil {
		slog.WarnContext(ctx, "agent CreateRole failed", "req", string(reqJSON), "dur_ms", durMs, "err", err)
	} else {
		respJSON, _ := json.Marshal(role)
		slog.InfoContext(ctx, "agent CreateRole ok", "req", string(reqJSON), "resp", string(respJSON), "dur_ms", durMs)
	}
	return role, err
}

func (c *AgentAPIClient) doCreate(ctx context.Context, path string, input RoleInput) (*Role, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("agent: marshal input: %w", err)
	}

	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	c.signRequest(req, http.MethodPost, path, "", body)

	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: create role: %w", err)
	}
	defer res.Body.Close()

	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(respBody))
	}

	var wrap roleResp
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}

	// Create API returns IDEnvelope: {"data": {"id": "xxx"}}
	var idResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(wrap.Data, &idResp); err != nil {
		return nil, fmt.Errorf("agent: parse create response: %w", err)
	}
	return &Role{ID: idResp.ID}, nil
}

// UpdateRole updates an existing role.
func (c *AgentAPIClient) UpdateRole(ctx context.Context, roleID string, input RoleInput) (*Role, error) {
	path := rolesPath + "/" + roleID
	start := time.Now()
	role, err := c.doUpdate(ctx, path, input)
	durMs := time.Since(start).Milliseconds()
	reqJSON, _ := json.Marshal(input)
	if err != nil {
		slog.WarnContext(ctx, "agent UpdateRole failed", "id", roleID, "req", string(reqJSON), "dur_ms", durMs, "err", err)
	} else {
		respJSON, _ := json.Marshal(role)
		slog.InfoContext(ctx, "agent UpdateRole ok", "id", roleID, "req", string(reqJSON), "resp", string(respJSON), "dur_ms", durMs)
	}
	return role, err
}

func (c *AgentAPIClient) doUpdate(ctx context.Context, path string, input RoleInput) (*Role, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("agent: marshal input: %w", err)
	}

	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, urlStr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	c.signRequest(req, http.MethodPut, path, "", body)

	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: update role: %w", err)
	}
	defer res.Body.Close()

	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(respBody))
	}

	var wrap roleResp
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}

	var role Role
	if len(wrap.Data) == 0 || string(wrap.Data) == "null" {
		// Upstream returned success without data; synthesise a minimal role.
		role = Role{
			ID:            path[strings.LastIndex(path, "/")+1:],
			Name:          input.Name,
			AgentConfig:   input.AgentConfig,
			ServiceConfig: input.ServiceConfig,
			UserParams:    input.UserParams,
		}
	} else if err := json.Unmarshal(wrap.Data, &role); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return &role, nil
}

// DeleteRole deletes a role.
func (c *AgentAPIClient) DeleteRole(ctx context.Context, roleID string) error {
	path := rolesPath + "/" + roleID
	start := time.Now()
	err := c.doDelete(ctx, path)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent DeleteRole failed", "id", roleID, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent DeleteRole ok", "id", roleID, "dur_ms", durMs)
	}
	return err
}

func (c *AgentAPIClient) doDelete(ctx context.Context, path string) error {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, urlStr, nil)
	if err != nil {
		return err
	}

	c.signRequest(req, http.MethodDelete, path, "", nil)

	res, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("agent: delete role: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}

	// DELETE may not return JSON body; read but don't require parse.
	var wrap roleResp
	body, _ := io.ReadAll(res.Body)
	if len(body) > 0 {
		if err := json.Unmarshal(body, &wrap); err == nil && wrap.Code != 200 {
			return fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
		}
	}
	return nil
}

// ── V2 Device-Role binding ──

const deviceRolesPath = "/v2/ai/device-roles"

// BatchCreateDeviceRoles creates device-role bindings for multiple devices.
func (c *AgentAPIClient) BatchCreateDeviceRoles(ctx context.Context, req BatchDeviceRolesRequest) error {
	start := time.Now()
	err := c.doDeviceRoleWrite(ctx, http.MethodPost, deviceRolesPath, req)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent BatchCreateDeviceRoles failed", "n", len(req.DeviceIDs), "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent BatchCreateDeviceRoles ok", "n", len(req.DeviceIDs), "dur_ms", durMs)
	}
	return err
}

// BatchQueryDeviceRoles queries device-role bindings for multiple devices.
func (c *AgentAPIClient) BatchQueryDeviceRoles(ctx context.Context, req BatchDeviceRolesQueryRequest) ([]DeviceRoleBinding, error) {
	start := time.Now()
	items, err := c.doDeviceRoleQuery(ctx, req)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent BatchQueryDeviceRoles failed", "n", len(req.DeviceIDs), "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent BatchQueryDeviceRoles ok", "n", len(req.DeviceIDs), "result", len(items), "dur_ms", durMs)
	}
	return items, err
}

// BatchDeleteDeviceRoles deletes device-role bindings for multiple devices.
func (c *AgentAPIClient) BatchDeleteDeviceRoles(ctx context.Context, req BatchDeviceRolesRequest) error {
	start := time.Now()
	err := c.doDeviceRoleWrite(ctx, http.MethodDelete, deviceRolesPath, req)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent BatchDeleteDeviceRoles failed", "n", len(req.DeviceIDs), "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent BatchDeleteDeviceRoles ok", "n", len(req.DeviceIDs), "dur_ms", durMs)
	}
	return err
}

func (c *AgentAPIClient) doDeviceRoleWrite(ctx context.Context, method, path string, req interface{}) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("agent: marshal device-role request: %w", err)
	}
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	httpReq, err := http.NewRequestWithContext(ctx, method, urlStr, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.signRequest(httpReq, method, path, "", body)
	res, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("agent: device-role %s: %w", method, err)
	}
	defer res.Body.Close()
	respBody, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(respBody))
	}
	var wrap roleResp
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &wrap); err == nil && wrap.Code != 0 && wrap.Code != 200 {
			return fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
		}
	}
	return nil
}

func (c *AgentAPIClient) doDeviceRoleQuery(ctx context.Context, req BatchDeviceRolesQueryRequest) ([]DeviceRoleBinding, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("agent: marshal query request: %w", err)
	}
	path := deviceRolesPath + "/query"
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.signRequest(httpReq, http.MethodPost, path, "", body)
	res, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("agent: device-role query: %w", err)
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(respBody))
	}
	var wrap roleResp
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var items []DeviceRoleBinding
	if err := json.Unmarshal(wrap.Data, &items); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return items, nil
}

// ── Voices ──

const voicesPath = "/ai/aigcrtc/voices"

// ListVoices returns available TTS voices, optionally filtered by language.
func (c *AgentAPIClient) ListVoices(ctx context.Context, language string) ([]VoiceInfo, error) {
	start := time.Now()
	voices, err := c.doListVoices(ctx, language)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent ListVoices failed", "lang", language, "dur_ms", durMs, "err", err)
	} else {
		respJSON, _ := json.Marshal(voices)
		slog.InfoContext(ctx, "agent ListVoices ok", "lang", language, "n", len(voices), "resp", string(respJSON), "dur_ms", durMs)
	}
	return voices, err
}

func (c *AgentAPIClient) doListVoices(ctx context.Context, language string) ([]VoiceInfo, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + voicesPath
	query := ""
	if language != "" {
		query = "language=" + language
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	if query != "" {
		req.URL.RawQuery = query
	}
	c.signRequest(req, http.MethodGet, voicesPath, query, nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: list voices: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var listData struct {
		Items []VoiceInfo `json:"items"`
	}
	if err := json.Unmarshal(wrap.Data, &listData); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return listData.Items, nil
}

// ── Global MCP Tools ──

const mcpToolsPath = "/ai/aigcrtc/mcp/tools"

// ListGlobalMCPTools returns the list of built-in MCP tools.
func (c *AgentAPIClient) ListGlobalMCPTools(ctx context.Context) ([]MCPToolBrief, error) {
	start := time.Now()
	items, err := c.doListMCPTools(ctx, mcpToolsPath)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent ListGlobalMCPTools failed", "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent ListGlobalMCPTools ok", "n", len(items), "dur_ms", durMs)
	}
	return items, err
}

// GetGlobalMCPTool returns a single global MCP tool detail.
func (c *AgentAPIClient) GetGlobalMCPTool(ctx context.Context, id string) (*MCPToolBrief, error) {
	path := mcpToolsPath + "/" + id
	start := time.Now()
	item, err := c.doGetMCPTool(ctx, path)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent GetGlobalMCPTool failed", "id", id, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent GetGlobalMCPTool ok", "id", id, "dur_ms", durMs)
	}
	return item, err
}

func (c *AgentAPIClient) doListMCPTools(ctx context.Context, path string) ([]MCPToolBrief, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	c.signRequest(req, http.MethodGet, path, "", nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: list mcp tools: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var listData struct {
		Items []MCPToolBrief `json:"items"`
	}
	if err := json.Unmarshal(wrap.Data, &listData); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return listData.Items, nil
}

func (c *AgentAPIClient) doGetMCPTool(ctx context.Context, path string) (*MCPToolBrief, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	c.signRequest(req, http.MethodGet, path, "", nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: get mcp tool: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	if len(wrap.Data) == 0 || string(wrap.Data) == "null" {
		return nil, nil
	}
	var item MCPToolBrief
	if err := json.Unmarshal(wrap.Data, &item); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return &item, nil
}

// ── App MCP Tools ──

const appMCPToolsPath = "/ai/aigcrtc/app-mcp/tools"

// ListAppMCPTools returns all app-level MCP tools.
func (c *AgentAPIClient) ListAppMCPTools(ctx context.Context) ([]AppMCPTool, error) {
	start := time.Now()
	items, err := c.doListAppMCPTools(ctx)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent ListAppMCPTools failed", "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent ListAppMCPTools ok", "n", len(items), "dur_ms", durMs)
	}
	return items, err
}

func (c *AgentAPIClient) doListAppMCPTools(ctx context.Context) ([]AppMCPTool, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + appMCPToolsPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	c.signRequest(req, http.MethodGet, appMCPToolsPath, "", nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: list app mcp tools: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var listData struct {
		Items []AppMCPTool `json:"items"`
	}
	if err := json.Unmarshal(wrap.Data, &listData); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return listData.Items, nil
}

// CreateAppMCPTool creates an app MCP tool.
func (c *AgentAPIClient) CreateAppMCPTool(ctx context.Context, req AppMCPToolCreateRequest) (*AppMCPTool, error) {
	start := time.Now()
	item, err := c.doAppMCPWrite(ctx, http.MethodPost, appMCPToolsPath, req)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent CreateAppMCPTool failed", "name", req.Config.Name, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent CreateAppMCPTool ok", "name", req.Config.Name, "id", item.ID, "dur_ms", durMs)
	}
	return item, err
}

// GetAppMCPTool returns a single app MCP tool.
func (c *AgentAPIClient) GetAppMCPTool(ctx context.Context, id string) (*AppMCPTool, error) {
	path := appMCPToolsPath + "/" + id
	start := time.Now()
	item, err := c.doGetAppMCPTool(ctx, path)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent GetAppMCPTool failed", "id", id, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent GetAppMCPTool ok", "id", id, "dur_ms", durMs)
	}
	return item, err
}

// UpdateAppMCPTool updates an app MCP tool.
func (c *AgentAPIClient) UpdateAppMCPTool(ctx context.Context, id string, req AppMCPToolUpdateRequest) (*AppMCPTool, error) {
	path := appMCPToolsPath + "/" + id
	start := time.Now()
	item, err := c.doAppMCPWrite(ctx, http.MethodPut, path, req)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent UpdateAppMCPTool failed", "id", id, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent UpdateAppMCPTool ok", "id", id, "dur_ms", durMs)
	}
	return item, err
}

// DeleteAppMCPTool deletes an app MCP tool.
func (c *AgentAPIClient) DeleteAppMCPTool(ctx context.Context, id string) error {
	path := appMCPToolsPath + "/" + id
	start := time.Now()
	err := c.doAppMCPDelete(ctx, path)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent DeleteAppMCPTool failed", "id", id, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent DeleteAppMCPTool ok", "id", id, "dur_ms", durMs)
	}
	return err
}

func (c *AgentAPIClient) doAppMCPWrite(ctx context.Context, method, path string, req interface{}) (*AppMCPTool, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("agent: marshal app mcp request: %w", err)
	}
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	httpReq, err := http.NewRequestWithContext(ctx, method, urlStr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.signRequest(httpReq, method, path, "", body)
	res, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("agent: app mcp %s: %w", method, err)
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(respBody))
	}
	var wrap roleResp
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var item AppMCPTool
	if err := json.Unmarshal(wrap.Data, &item); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return &item, nil
}

func (c *AgentAPIClient) doGetAppMCPTool(ctx context.Context, path string) (*AppMCPTool, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	c.signRequest(req, http.MethodGet, path, "", nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: get app mcp: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	if len(wrap.Data) == 0 || string(wrap.Data) == "null" {
		return nil, nil
	}
	var item AppMCPTool
	if err := json.Unmarshal(wrap.Data, &item); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return &item, nil
}

func (c *AgentAPIClient) doAppMCPDelete(ctx context.Context, path string) error {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, urlStr, nil)
	if err != nil {
		return err
	}
	c.signRequest(req, http.MethodDelete, path, "", nil)
	res, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("agent: delete app mcp: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	body, _ := io.ReadAll(res.Body)
	if len(body) > 0 {
		if err := json.Unmarshal(body, &wrap); err == nil && wrap.Code != 0 && wrap.Code != 200 {
			return fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
		}
	}
	return nil
}

// ── Device Plugins ──

const devicePluginsPath = "/ai/aigcrtc/device/plugins"

// ListDevicePlugins returns all device plugins.
func (c *AgentAPIClient) ListDevicePlugins(ctx context.Context) ([]DevicePlugin, error) {
	start := time.Now()
	items, err := c.doListPlugins(ctx)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent ListDevicePlugins failed", "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent ListDevicePlugins ok", "n", len(items), "dur_ms", durMs)
	}
	return items, err
}

func (c *AgentAPIClient) doListPlugins(ctx context.Context) ([]DevicePlugin, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + devicePluginsPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	c.signRequest(req, http.MethodGet, devicePluginsPath, "", nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: list plugins: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var listData struct {
		Items []DevicePlugin `json:"items"`
	}
	if err := json.Unmarshal(wrap.Data, &listData); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return listData.Items, nil
}

// CreateDevicePlugin creates a device plugin.
func (c *AgentAPIClient) CreateDevicePlugin(ctx context.Context, req DevicePluginCreateRequest) (*DevicePlugin, error) {
	start := time.Now()
	item, err := c.doPluginWrite(ctx, http.MethodPost, devicePluginsPath, req)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent CreateDevicePlugin failed", "name", req.Name, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent CreateDevicePlugin ok", "name", req.Name, "id", item.ID, "dur_ms", durMs)
	}
	return item, err
}

// GetDevicePlugin returns a single device plugin.
func (c *AgentAPIClient) GetDevicePlugin(ctx context.Context, id string) (*DevicePlugin, error) {
	path := devicePluginsPath + "/" + id
	start := time.Now()
	item, err := c.doGetPlugin(ctx, path)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent GetDevicePlugin failed", "id", id, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent GetDevicePlugin ok", "id", id, "dur_ms", durMs)
	}
	return item, err
}

// UpdateDevicePlugin updates a device plugin.
func (c *AgentAPIClient) UpdateDevicePlugin(ctx context.Context, id string, req DevicePluginUpdateRequest) (*DevicePlugin, error) {
	path := devicePluginsPath + "/" + id
	start := time.Now()
	item, err := c.doPluginWrite(ctx, http.MethodPut, path, req)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent UpdateDevicePlugin failed", "id", id, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent UpdateDevicePlugin ok", "id", id, "dur_ms", durMs)
	}
	return item, err
}

// DeleteDevicePlugin deletes a device plugin.
func (c *AgentAPIClient) DeleteDevicePlugin(ctx context.Context, id string) error {
	path := devicePluginsPath + "/" + id
	start := time.Now()
	err := c.doPluginDelete(ctx, path)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent DeleteDevicePlugin failed", "id", id, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent DeleteDevicePlugin ok", "id", id, "dur_ms", durMs)
	}
	return err
}

func (c *AgentAPIClient) doPluginWrite(ctx context.Context, method, path string, req interface{}) (*DevicePlugin, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("agent: marshal plugin request: %w", err)
	}
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	httpReq, err := http.NewRequestWithContext(ctx, method, urlStr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.signRequest(httpReq, method, path, "", body)
	res, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("agent: plugin %s: %w", method, err)
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(respBody))
	}
	var wrap roleResp
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var item DevicePlugin
	if err := json.Unmarshal(wrap.Data, &item); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return &item, nil
}

func (c *AgentAPIClient) doGetPlugin(ctx context.Context, path string) (*DevicePlugin, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	c.signRequest(req, http.MethodGet, path, "", nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: get plugin: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	if len(wrap.Data) == 0 || string(wrap.Data) == "null" {
		return nil, nil
	}
	var item DevicePlugin
	if err := json.Unmarshal(wrap.Data, &item); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return &item, nil
}

func (c *AgentAPIClient) doPluginDelete(ctx context.Context, path string) error {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, urlStr, nil)
	if err != nil {
		return err
	}
	c.signRequest(req, http.MethodDelete, path, "", nil)
	res, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("agent: delete plugin: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	body, _ := io.ReadAll(res.Body)
	if len(body) > 0 {
		if err := json.Unmarshal(body, &wrap); err == nil && wrap.Code != 0 && wrap.Code != 200 {
			return fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
		}
	}
	return nil
}

// ── Knowledge Indexes ──

const knowledgeIndexesPath = "/ai/aigcrtc/knowledge/indexes"

// ListKnowledgeIndexes returns paginated knowledge indexes.
func (c *AgentAPIClient) ListKnowledgeIndexes(ctx context.Context, page, pageSize int) ([]KnowledgeIndexInfo, int, error) {
	start := time.Now()
	items, total, err := c.doListKnowledgeIndexes(ctx, page, pageSize)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent ListKnowledgeIndexes failed", "page", page, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent ListKnowledgeIndexes ok", "page", page, "total", total, "dur_ms", durMs)
	}
	return items, total, err
}

func (c *AgentAPIClient) doListKnowledgeIndexes(ctx context.Context, page, pageSize int) ([]KnowledgeIndexInfo, int, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + knowledgeIndexesPath
	query := fmt.Sprintf("page=%d&page_size=%d", page, pageSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr+"?"+query, nil)
	if err != nil {
		return nil, 0, err
	}
	c.signRequest(req, http.MethodGet, knowledgeIndexesPath, query, nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("agent: list knowledge indexes: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, 0, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, 0, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, 0, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var pageData struct {
		Items      []KnowledgeIndexInfo `json:"items"`
		TotalCount int                  `json:"total_count"`
	}
	if err := json.Unmarshal(wrap.Data, &pageData); err != nil {
		return nil, 0, fmt.Errorf("agent: parse data: %w", err)
	}
	return pageData.Items, pageData.TotalCount, nil
}

// CreateKnowledgeIndex creates a knowledge index.
func (c *AgentAPIClient) CreateKnowledgeIndex(ctx context.Context, req CreateKnowledgeIndexRequest) (*KnowledgeIndexInfo, error) {
	start := time.Now()
	item, err := c.doKnowledgeWrite(ctx, http.MethodPost, knowledgeIndexesPath, req)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent CreateKnowledgeIndex failed", "name", req.Name, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent CreateKnowledgeIndex ok", "name", req.Name, "id", item.IndexID, "dur_ms", durMs)
	}
	return item, err
}

// GetKnowledgeIndex returns a single knowledge index.
func (c *AgentAPIClient) GetKnowledgeIndex(ctx context.Context, indexID string) (*KnowledgeIndexInfo, error) {
	path := knowledgeIndexesPath + "/" + indexID
	start := time.Now()
	item, err := c.doGetKnowledge(ctx, path)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent GetKnowledgeIndex failed", "id", indexID, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent GetKnowledgeIndex ok", "id", indexID, "dur_ms", durMs)
	}
	return item, err
}

// UpdateKnowledgeIndex updates a knowledge index.
func (c *AgentAPIClient) UpdateKnowledgeIndex(ctx context.Context, indexID string, req UpdateKnowledgeIndexRequest) (*KnowledgeIndexInfo, error) {
	path := knowledgeIndexesPath + "/" + indexID
	start := time.Now()
	item, err := c.doKnowledgeWrite(ctx, http.MethodPut, path, req)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent UpdateKnowledgeIndex failed", "id", indexID, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent UpdateKnowledgeIndex ok", "id", indexID, "dur_ms", durMs)
	}
	return item, err
}

// DeleteKnowledgeIndex deletes a knowledge index.
func (c *AgentAPIClient) DeleteKnowledgeIndex(ctx context.Context, indexID string) error {
	path := knowledgeIndexesPath + "/" + indexID
	start := time.Now()
	err := c.doDelete(ctx, path)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent DeleteKnowledgeIndex failed", "id", indexID, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent DeleteKnowledgeIndex ok", "id", indexID, "dur_ms", durMs)
	}
	return err
}

func (c *AgentAPIClient) doKnowledgeWrite(ctx context.Context, method, path string, req interface{}) (*KnowledgeIndexInfo, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("agent: marshal knowledge request: %w", err)
	}
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	httpReq, err := http.NewRequestWithContext(ctx, method, urlStr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.signRequest(httpReq, method, path, "", body)
	res, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("agent: knowledge %s: %w", method, err)
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(respBody))
	}
	var wrap roleResp
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var item KnowledgeIndexInfo
	if err := json.Unmarshal(wrap.Data, &item); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return &item, nil
}

func (c *AgentAPIClient) doGetKnowledge(ctx context.Context, path string) (*KnowledgeIndexInfo, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	c.signRequest(req, http.MethodGet, path, "", nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: get knowledge: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	if len(wrap.Data) == 0 || string(wrap.Data) == "null" {
		return nil, nil
	}
	var item KnowledgeIndexInfo
	if err := json.Unmarshal(wrap.Data, &item); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return &item, nil
}

// ── Knowledge Documents ──

// ListKnowledgeDocuments returns paginated documents under a knowledge index.
func (c *AgentAPIClient) ListKnowledgeDocuments(ctx context.Context, indexID string, page, pageSize int) ([]KnowledgeDocument, int, error) {
	path := knowledgeIndexesPath + "/" + indexID + "/documents"
	start := time.Now()
	items, total, err := c.doListKnowledgeDocuments(ctx, path, page, pageSize)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent ListKnowledgeDocuments failed", "index", indexID, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent ListKnowledgeDocuments ok", "index", indexID, "total", total, "dur_ms", durMs)
	}
	return items, total, err
}

func (c *AgentAPIClient) doListKnowledgeDocuments(ctx context.Context, path string, page, pageSize int) ([]KnowledgeDocument, int, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + path
	query := fmt.Sprintf("page=%d&page_size=%d", page, pageSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr+"?"+query, nil)
	if err != nil {
		return nil, 0, err
	}
	c.signRequest(req, http.MethodGet, path, query, nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("agent: list knowledge docs: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, 0, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, 0, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, 0, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var pageData struct {
		Items      []KnowledgeDocument `json:"items"`
		TotalCount int                 `json:"total_count"`
	}
	if err := json.Unmarshal(wrap.Data, &pageData); err != nil {
		return nil, 0, fmt.Errorf("agent: parse data: %w", err)
	}
	return pageData.Items, pageData.TotalCount, nil
}

// ── Knowledge Files ──

const knowledgeFilesPath = "/ai/aigcrtc/knowledge/files"

// ListKnowledgeFiles returns knowledge files with cursor pagination.
func (c *AgentAPIClient) ListKnowledgeFiles(ctx context.Context) ([]KnowledgeFileInfo, error) {
	start := time.Now()
	items, err := c.doListKnowledgeFiles(ctx)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent ListKnowledgeFiles failed", "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent ListKnowledgeFiles ok", "n", len(items), "dur_ms", durMs)
	}
	return items, err
}

func (c *AgentAPIClient) doListKnowledgeFiles(ctx context.Context) ([]KnowledgeFileInfo, error) {
	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + knowledgeFilesPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	c.signRequest(req, http.MethodGet, knowledgeFilesPath, "", nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: list knowledge files: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(body))
	}
	var wrap roleResp
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var listData struct {
		Items []KnowledgeFileInfo `json:"items"`
	}
	if err := json.Unmarshal(wrap.Data, &listData); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return listData.Items, nil
}

// UploadKnowledgeFile uploads a file and returns its file_id. The caller provides
// the file name and raw bytes; the method sends a multipart/form-data request.
func (c *AgentAPIClient) UploadKnowledgeFile(ctx context.Context, fileName string, fileData []byte) (*KnowledgeFileSimpleResponse, error) {
	start := time.Now()
	result, err := c.doUploadKnowledgeFile(ctx, fileName, fileData)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent UploadKnowledgeFile failed", "name", fileName, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent UploadKnowledgeFile ok", "name", fileName, "id", result.FileID, "dur_ms", durMs)
	}
	return result, err
}

func (c *AgentAPIClient) doUploadKnowledgeFile(ctx context.Context, fileName string, fileData []byte) (*KnowledgeFileSimpleResponse, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	sum := md5.Sum(fileData)
	if err := writer.WriteField("md5", hex.EncodeToString(sum[:])); err != nil {
		return nil, fmt.Errorf("agent: write md5 field: %w", err)
	}
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return nil, fmt.Errorf("agent: create form file: %w", err)
	}
	if _, err := part.Write(fileData); err != nil {
		return nil, fmt.Errorf("agent: write file data: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("agent: close writer: %w", err)
	}

	urlStr := strings.TrimRight(c.cfg.BaseURL, "/") + knowledgeFilesPath
	body := buf.Bytes()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	c.signRequest(req, http.MethodPost, knowledgeFilesPath, "", body)

	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: upload knowledge file: %w", err)
	}
	defer res.Body.Close()

	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: HTTP %d: %s", res.StatusCode, string(respBody))
	}
	var wrap roleResp
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return nil, fmt.Errorf("agent: parse response: %w", err)
	}
	if wrap.Code != 200 {
		return nil, fmt.Errorf("agent: upstream error code=%d msg=%s", wrap.Code, wrap.Msg)
	}
	var result KnowledgeFileSimpleResponse
	if err := json.Unmarshal(wrap.Data, &result); err != nil {
		return nil, fmt.Errorf("agent: parse data: %w", err)
	}
	return &result, nil
}

// DeleteKnowledgeFile deletes a knowledge file by ID.
func (c *AgentAPIClient) DeleteKnowledgeFile(ctx context.Context, fileID string) error {
	path := knowledgeFilesPath + "/" + fileID
	start := time.Now()
	err := c.doDelete(ctx, path)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.WarnContext(ctx, "agent DeleteKnowledgeFile failed", "id", fileID, "dur_ms", durMs, "err", err)
	} else {
		slog.InfoContext(ctx, "agent DeleteKnowledgeFile ok", "id", fileID, "dur_ms", durMs)
	}
	return err
}

// signRequest applies TGV1-HMAC-SHA256 signing headers to the request.
func (c *AgentAPIClient) signRequest(req *http.Request, method, uriPath, rawQuery string, body []byte) {
	hdr := SignTGV1Request(c.cfg.SecretKeyID, c.cfg.AccessKeyID, c.cfg.AppID, method, uriPath, rawQuery, body, req.Header.Get("Content-Type"), time.Now().UTC())
	for k, vs := range hdr {
		if len(vs) > 0 && req.Header.Get(k) == "" {
			req.Header.Set(k, vs[0])
		}
	}
	req.Header.Set("X-Tg-Platform", "web")
	req.Header.Set("Accept-Language", "zh-cn")
}
