package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"thing-connect/internal/apiresp"
	"thing-connect/internal/model"
	"thing-connect/internal/tirtcapi"
)

// ── Mock implementations ──

type mockAgentAPI struct {
	roles            map[string]*tirtcapi.Role
	voices           []tirtcapi.VoiceInfo
	getRoleErr       error
	createRoleErr    error
	updateRoleErr    error
	deleteRoleErr    error
	listVoicesErr    error
	createRoleResult *tirtcapi.Role
	// App MCP tool mocking
	createAppMCPResult  *tirtcapi.AppMCPTool
	getAppMCPToolResult *tirtcapi.AppMCPTool
	createAppMCPErr     error
	createAppMCPCalls   int
	updateAppMCPCalls   int
	deleteAppMCPCalls   int
	// Device plugin mocking
	createPluginResult *tirtcapi.DevicePlugin
	createPluginErr    error
	createPluginCalls  int
	// Knowledge index mocking
	createKBResult *tirtcapi.KnowledgeIndexInfo
	createKBErr    error
	createKBCalls  int
	// Knowledge document/file mocking
	knowledgeDocuments         []tirtcapi.KnowledgeDocument
	knowledgeFiles             []tirtcapi.KnowledgeFileInfo
	uploadKnowledgeFileResult  *tirtcapi.KnowledgeFileSimpleResponse
	listKnowledgeDocumentCalls int
	listKnowledgeFileCalls     int
	uploadKnowledgeFileCalls   int
	deleteKnowledgeFileCalls   int
}

func (m *mockAgentAPI) GetRole(ctx context.Context, roleID string) (*tirtcapi.Role, error) {
	if m.getRoleErr != nil {
		return nil, m.getRoleErr
	}
	return m.roles[roleID], nil
}

func (m *mockAgentAPI) CreateRole(ctx context.Context, input tirtcapi.RoleInput) (*tirtcapi.Role, error) {
	if m.createRoleErr != nil {
		return nil, m.createRoleErr
	}
	if m.createRoleResult != nil {
		return m.createRoleResult, nil
	}
	r := &tirtcapi.Role{ID: "new-role-id", Name: input.Name}
	m.roles["new-role-id"] = r
	return r, nil
}

func (m *mockAgentAPI) UpdateRole(ctx context.Context, roleID string, input tirtcapi.RoleInput) (*tirtcapi.Role, error) {
	if m.updateRoleErr != nil {
		return nil, m.updateRoleErr
	}
	r := m.roles[roleID]
	if r == nil {
		return nil, nil //nolint:nilnil // The mock mirrors the upstream not-found contract.
	}
	r.Name = input.Name
	return r, nil
}

func (m *mockAgentAPI) DeleteRole(ctx context.Context, roleID string) error {
	if m.deleteRoleErr != nil {
		return m.deleteRoleErr
	}
	delete(m.roles, roleID)
	return nil
}

func (m *mockAgentAPI) ListVoices(ctx context.Context, language string) ([]tirtcapi.VoiceInfo, error) {
	if m.listVoicesErr != nil {
		return nil, m.listVoicesErr
	}
	return m.voices, nil
}

// Stub implementations for interface satisfaction
func (m *mockAgentAPI) BatchCreateDeviceRoles(ctx context.Context, req tirtcapi.BatchDeviceRolesRequest) error {
	return nil
}
func (m *mockAgentAPI) BatchQueryDeviceRoles(ctx context.Context, req tirtcapi.BatchDeviceRolesQueryRequest) ([]tirtcapi.DeviceRoleBinding, error) {
	return nil, nil
}
func (m *mockAgentAPI) BatchDeleteDeviceRoles(ctx context.Context, req tirtcapi.BatchDeviceRolesRequest) error {
	return nil
}
func (m *mockAgentAPI) ListGlobalMCPTools(ctx context.Context) ([]tirtcapi.MCPToolBrief, error) {
	return nil, nil
}
func (m *mockAgentAPI) GetGlobalMCPTool(ctx context.Context, id string) (*tirtcapi.MCPToolBrief, error) {
	return nil, nil //nolint:nilnil // The mock mirrors the upstream not-found contract.
}
func (m *mockAgentAPI) ListAppMCPTools(ctx context.Context) ([]tirtcapi.AppMCPTool, error) {
	return nil, nil
}
func (m *mockAgentAPI) CreateAppMCPTool(ctx context.Context, req tirtcapi.AppMCPToolCreateRequest) (*tirtcapi.AppMCPTool, error) {
	m.createAppMCPCalls++
	if m.createAppMCPErr != nil {
		return nil, m.createAppMCPErr
	}
	if m.createAppMCPResult != nil {
		return m.createAppMCPResult, nil
	}
	return &tirtcapi.AppMCPTool{ID: "new-mcp"}, nil
}
func (m *mockAgentAPI) GetAppMCPTool(ctx context.Context, id string) (*tirtcapi.AppMCPTool, error) {
	if m.getAppMCPToolResult != nil {
		return m.getAppMCPToolResult, nil
	}
	return nil, nil //nolint:nilnil // The mock mirrors the upstream not-found contract.
}
func (m *mockAgentAPI) UpdateAppMCPTool(ctx context.Context, id string, req tirtcapi.AppMCPToolUpdateRequest) (*tirtcapi.AppMCPTool, error) {
	m.updateAppMCPCalls++
	return nil, nil //nolint:nilnil // Tests that need a result configure the mock explicitly.
}
func (m *mockAgentAPI) DeleteAppMCPTool(ctx context.Context, id string) error {
	m.deleteAppMCPCalls++
	return nil
}
func (m *mockAgentAPI) ListDevicePlugins(ctx context.Context) ([]tirtcapi.DevicePlugin, error) {
	return nil, nil
}
func (m *mockAgentAPI) CreateDevicePlugin(ctx context.Context, req tirtcapi.DevicePluginCreateRequest) (*tirtcapi.DevicePlugin, error) {
	m.createPluginCalls++
	if m.createPluginErr != nil {
		return nil, m.createPluginErr
	}
	if m.createPluginResult != nil {
		return m.createPluginResult, nil
	}
	return &tirtcapi.DevicePlugin{ID: "new-plg"}, nil
}
func (m *mockAgentAPI) GetDevicePlugin(ctx context.Context, id string) (*tirtcapi.DevicePlugin, error) {
	return nil, nil //nolint:nilnil // The mock mirrors the upstream not-found contract.
}
func (m *mockAgentAPI) UpdateDevicePlugin(ctx context.Context, id string, req tirtcapi.DevicePluginUpdateRequest) (*tirtcapi.DevicePlugin, error) {
	return nil, nil //nolint:nilnil // Tests that need a result configure the mock explicitly.
}
func (m *mockAgentAPI) DeleteDevicePlugin(ctx context.Context, id string) error { return nil }
func (m *mockAgentAPI) ListKnowledgeIndexes(ctx context.Context, page, pageSize int) ([]tirtcapi.KnowledgeIndexInfo, int, error) {
	return nil, 0, nil
}
func (m *mockAgentAPI) CreateKnowledgeIndex(ctx context.Context, req tirtcapi.CreateKnowledgeIndexRequest) (*tirtcapi.KnowledgeIndexInfo, error) {
	m.createKBCalls++
	if m.createKBErr != nil {
		return nil, m.createKBErr
	}
	if m.createKBResult != nil {
		return m.createKBResult, nil
	}
	return &tirtcapi.KnowledgeIndexInfo{IndexID: "new-kb"}, nil
}
func (m *mockAgentAPI) GetKnowledgeIndex(ctx context.Context, indexID string) (*tirtcapi.KnowledgeIndexInfo, error) {
	return nil, nil //nolint:nilnil // The mock mirrors the upstream not-found contract.
}
func (m *mockAgentAPI) UpdateKnowledgeIndex(ctx context.Context, indexID string, req tirtcapi.UpdateKnowledgeIndexRequest) (*tirtcapi.KnowledgeIndexInfo, error) {
	return nil, nil //nolint:nilnil // Tests that need a result configure the mock explicitly.
}
func (m *mockAgentAPI) DeleteKnowledgeIndex(ctx context.Context, indexID string) error { return nil }
func (m *mockAgentAPI) ListKnowledgeDocuments(ctx context.Context, indexID string, page, pageSize int) ([]tirtcapi.KnowledgeDocument, int, error) {
	m.listKnowledgeDocumentCalls++
	return m.knowledgeDocuments, len(m.knowledgeDocuments), nil
}
func (m *mockAgentAPI) ListKnowledgeFiles(ctx context.Context) ([]tirtcapi.KnowledgeFileInfo, error) {
	m.listKnowledgeFileCalls++
	return m.knowledgeFiles, nil
}
func (m *mockAgentAPI) UploadKnowledgeFile(ctx context.Context, fileName string, fileData []byte) (*tirtcapi.KnowledgeFileSimpleResponse, error) {
	m.uploadKnowledgeFileCalls++
	if m.uploadKnowledgeFileResult != nil {
		return m.uploadKnowledgeFileResult, nil
	}
	return &tirtcapi.KnowledgeFileSimpleResponse{FileID: "file-1"}, nil
}
func (m *mockAgentAPI) DeleteKnowledgeFile(ctx context.Context, fileID string) error {
	m.deleteKnowledgeFileCalls++
	return nil
}

type mockRoleBindingStore struct {
	bindings  map[string]string // deviceID -> roleID
	owners    map[string]int64
	allowAll  bool
	getErr    error
	setErr    error
	deleteErr error
}

func (m *mockRoleBindingStore) UserOwnsDevice(_ context.Context, deviceID string, userID int64) (bool, error) {
	if m.allowAll || m.owners == nil {
		return true, nil
	}
	return m.owners[deviceID] == userID, nil
}

func (m *mockRoleBindingStore) GetDeviceRole(ctx context.Context, deviceID string) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	return m.bindings[deviceID], nil
}

func (m *mockRoleBindingStore) SetDeviceRole(ctx context.Context, deviceID, roleID string, userID int64) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.bindings[deviceID] = roleID
	return nil
}

func (m *mockRoleBindingStore) DeleteDeviceRole(ctx context.Context, deviceID string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.bindings, deviceID)
	return nil
}

type mockUserRoleStore struct {
	roles     map[int64][]string // userID -> []roleID
	listErr   error
	addErr    error
	removeErr error
	allowAll  bool
}

func (m *mockUserRoleStore) ListUserRoleIDs(ctx context.Context, userID int64) ([]string, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.roles[userID], nil
}

func (m *mockUserRoleStore) AddUserRole(ctx context.Context, userID int64, roleID string) error {
	if m.addErr != nil {
		return m.addErr
	}
	m.roles[userID] = append(m.roles[userID], roleID)
	return nil
}

func (m *mockUserRoleStore) RemoveUserRole(ctx context.Context, userID int64, roleID string) error {
	if m.removeErr != nil {
		return m.removeErr
	}
	ids := m.roles[userID]
	for i, id := range ids {
		if id == roleID {
			m.roles[userID] = append(ids[:i], ids[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *mockUserRoleStore) ExistsUserRole(_ context.Context, userID int64, roleID string) (bool, error) {
	if m.allowAll {
		return true, nil
	}
	for _, id := range m.roles[userID] {
		if id == roleID {
			return true, nil
		}
	}
	return false, nil
}

// resourceKey is the mock's composite key: "userID:type".
func resourceKey(userID int64, typ string) string { return fmt.Sprintf("%d:%s", userID, typ) }

type mockUserResourceStore struct {
	rows      map[string][]model.UserResource // key: resourceKey(userID, type)
	addErr    error
	removeErr error
	listErr   error
	countErr  error
	updateErr error
}

func (m *mockUserResourceStore) Add(ctx context.Context, userID int64, typ, resourceID, name string) error {
	if m.addErr != nil {
		return m.addErr
	}
	k := resourceKey(userID, typ)
	for _, r := range m.rows[k] {
		if r.ResourceID == resourceID {
			return nil // INSERT IGNORE semantics
		}
	}
	m.rows[k] = append(m.rows[k], model.UserResource{
		UserID: userID, Type: typ, ResourceID: resourceID, Name: name,
	})
	return nil
}

func (m *mockUserResourceStore) Remove(ctx context.Context, userID int64, typ, resourceID string) error {
	if m.removeErr != nil {
		return m.removeErr
	}
	k := resourceKey(userID, typ)
	rows := m.rows[k]
	for i, r := range rows {
		if r.ResourceID == resourceID {
			m.rows[k] = append(rows[:i], rows[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *mockUserResourceStore) List(ctx context.Context, userID int64, typ string) ([]model.UserResource, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	if rows := m.rows[resourceKey(userID, typ)]; rows != nil {
		return rows, nil
	}
	return []model.UserResource{}, nil
}

func (m *mockUserResourceStore) Count(ctx context.Context, userID int64, typ string) (int, error) {
	if m.countErr != nil {
		return 0, m.countErr
	}
	return len(m.rows[resourceKey(userID, typ)]), nil
}

func (m *mockUserResourceStore) UpdateName(ctx context.Context, userID int64, typ, resourceID, name string) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	k := resourceKey(userID, typ)
	for i := range m.rows[k] {
		if m.rows[k][i].ResourceID == resourceID {
			m.rows[k][i].Name = name
			return nil
		}
	}
	return nil
}

func (m *mockUserResourceStore) Exists(ctx context.Context, userID int64, typ, resourceID string) (bool, error) {
	for _, r := range m.rows[resourceKey(userID, typ)] {
		if r.ResourceID == resourceID {
			return true, nil
		}
	}
	return false, nil
}

// ── Test helpers ──

func setupTestRouter(h *AgentHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.RedirectTrailingSlash = false
	if h.userRoleStore == nil {
		h.userRoleStore = &mockUserRoleStore{allowAll: true}
	}
	if h.roleStore == nil {
		h.roleStore = &mockRoleBindingStore{allowAll: true}
	}
	jwtSecret := "test-secret"
	h.Register(r, jwtSecret)
	return r
}

// makeToken creates a valid JWT for testing with the given userID.
func makeToken(userID int64) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": float64(userID),
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	s, err := tok.SignedString([]byte("test-secret"))
	if err != nil {
		panic(fmt.Sprintf("failed to sign test token: %v", err))
	}
	return s
}

func doRequest(r *gin.Engine, method, path string, body string, token string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func doMultipartFileRequest(
	t *testing.T,
	r *gin.Engine,
	path, token, fieldName, fileName string,
	fileData []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(fieldName, fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(fileData); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func parseJSON(t *testing.T, w *httptest.ResponseRecorder) apiresp.JSON {
	t.Helper()
	var resp apiresp.JSON
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v, body=%s", err, w.Body.String())
	}
	return resp
}

// ── Tests ──

func TestInternalUnbind_InvalidKeyUsesInternalCredentialCode(t *testing.T) {
	h := &AgentHandler{internalKey: "expected-key"}
	router := setupTestRouter(h)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/ai/internal/unbind",
		strings.NewReader(`{"device_id":"dev-1"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", "wrong-key")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("HTTP status = %d, want %d", w.Code, http.StatusForbidden)
	}
	resp := parseJSON(t, w)
	if resp.Code != 40301 {
		t.Fatalf("code = %d, want 40301", resp.Code)
	}
}

func TestListRoles_Success(t *testing.T) {
	api := &mockAgentAPI{
		roles: map[string]*tirtcapi.Role{
			"r1": {ID: "r1", Name: "Role1"},
			"r2": {ID: "r2", Name: "Role2"},
		},
	}
	userRoleStore := &mockUserRoleStore{
		roles: map[int64][]string{1: {"r1", "r2"}},
	}
	h := &AgentHandler{
		agentAPI:      api,
		userRoleStore: userRoleStore,
	}
	router := setupTestRouter(h)
	tok := makeToken(1)

	w := doRequest(router, "GET", "/v1/ai/roles", "", tok)
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d: %s", resp.Code, resp.Msg)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be an object, got %T", resp.Data)
	}
	items := data["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(items))
	}
}

func TestListRoles_Empty(t *testing.T) {
	api := &mockAgentAPI{roles: map[string]*tirtcapi.Role{}}
	userRoleStore := &mockUserRoleStore{
		roles: map[int64][]string{1: {}},
	}
	h := &AgentHandler{agentAPI: api, userRoleStore: userRoleStore}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/roles", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d", resp.Code)
	}
	data := resp.Data.(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 0 {
		t.Fatalf("expected 0 roles, got %d", len(items))
	}
}

func TestListRoles_UserRoleStoreError(t *testing.T) {
	userRoleStore := &mockUserRoleStore{
		listErr: errors.New("db down"),
		roles:   map[int64][]string{},
	}
	h := &AgentHandler{agentAPI: &mockAgentAPI{}, userRoleStore: userRoleStore}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/roles", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 50000 {
		t.Fatalf("expected internal error code 50000, got %d", resp.Code)
	}
}

func TestListRoles_SkipsAPIErrors(t *testing.T) {
	// One role fetches fine, the other errors — should still return the good one.
	api := &mockAgentAPI{
		roles: map[string]*tirtcapi.Role{
			"r1": {ID: "r1", Name: "Good"},
		},
		// r2 is missing from the map → GetRole returns nil (not found)
	}
	userRoleStore := &mockUserRoleStore{
		roles: map[int64][]string{1: {"r1", "r2"}},
	}
	h := &AgentHandler{agentAPI: api, userRoleStore: userRoleStore}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/roles", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d", resp.Code)
	}
	data := resp.Data.(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 role (r2 skipped), got %d", len(items))
	}
}

func TestListRoles_Unauthorized(t *testing.T) {
	h := &AgentHandler{agentAPI: &mockAgentAPI{}}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/roles", "", "") // no token
	resp := parseJSON(t, w)
	if resp.Code != 401 {
		t.Fatalf("expected unauthorized code 401, got %d", resp.Code)
	}
}

// ── CreateRole ──

func TestCreateRole_Success(t *testing.T) {
	api := &mockAgentAPI{
		roles:            map[string]*tirtcapi.Role{},
		createRoleResult: &tirtcapi.Role{ID: "created-id", Name: "TestAgent"},
	}
	userRoleStore := &mockUserRoleStore{
		roles: map[int64][]string{1: {}},
	}
	h := &AgentHandler{agentAPI: api, userRoleStore: userRoleStore}
	router := setupTestRouter(h)

	body := `{"name":"TestAgent","agent_config":{"prompt":"Hello"}}`
	w := doRequest(router, "POST", "/v1/ai/roles", body, makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d: %s", resp.Code, resp.Msg)
	}
	// role mapping should be saved
	if ids := userRoleStore.roles[1]; len(ids) != 1 || ids[0] != "created-id" {
		t.Fatalf("expected user role mapping [created-id], got %v", ids)
	}
}

func TestCreateRole_MissingName(t *testing.T) {
	h := &AgentHandler{agentAPI: &mockAgentAPI{}}
	router := setupTestRouter(h)

	w := doRequest(router, "POST", "/v1/ai/roles", `{"name":""}`, makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 40000 {
		t.Fatalf("expected bad param code 40000, got %d", resp.Code)
	}
}

func TestCreateRole_APIError(t *testing.T) {
	api := &mockAgentAPI{createRoleErr: errors.New("cloud unreachable")}
	h := &AgentHandler{agentAPI: api}
	router := setupTestRouter(h)

	w := doRequest(router, "POST", "/v1/ai/roles", `{"name":"X"}`, makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 50200 {
		t.Fatalf("expected bad gateway code 50200, got %d", resp.Code)
	}
	if resp.Msg != "创建角色失败：AI 云服务暂不可用，请稍后重试" {
		t.Fatalf("unexpected actionable message: %q", resp.Msg)
	}
}

func TestCreateRole_APITimeout(t *testing.T) {
	api := &mockAgentAPI{createRoleErr: context.DeadlineExceeded}
	h := &AgentHandler{agentAPI: api}
	router := setupTestRouter(h)

	w := doRequest(router, "POST", "/v1/ai/roles", `{"name":"X"}`, makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 50200 || resp.Msg != "创建角色失败：AI 云服务请求超时，请稍后重试" {
		t.Fatalf("expected unchanged 50200 with timeout detail, got code=%d msg=%q", resp.Code, resp.Msg)
	}
}

func TestCreateRole_UserRoleStoreError(t *testing.T) {
	api := &mockAgentAPI{
		roles:            map[string]*tirtcapi.Role{},
		createRoleResult: &tirtcapi.Role{ID: "rid", Name: "X"},
	}
	userRoleStore := &mockUserRoleStore{
		addErr: errors.New("db error"),
		roles:  map[int64][]string{1: {}},
	}
	h := &AgentHandler{agentAPI: api, userRoleStore: userRoleStore}
	router := setupTestRouter(h)

	w := doRequest(router, "POST", "/v1/ai/roles", `{"name":"X"}`, makeToken(1))
	resp := parseJSON(t, w)

	// userRoleStore.AddUserRole fails → 500
	if resp.Code != 50000 {
		t.Fatalf("expected internal error code 50000, got %d", resp.Code)
	}
}

// ── GetRole ──

func TestGetRole_Success(t *testing.T) {
	api := &mockAgentAPI{
		roles: map[string]*tirtcapi.Role{"r1": {ID: "r1", Name: "Role1"}},
	}
	h := &AgentHandler{agentAPI: api}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/roles/r1", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d", resp.Code)
	}
}

func TestGetRole_RejectsOtherUsersRole(t *testing.T) {
	api := &mockAgentAPI{roles: map[string]*tirtcapi.Role{"r2": {ID: "r2"}}}
	h := &AgentHandler{
		agentAPI: api,
		userRoleStore: &mockUserRoleStore{roles: map[int64][]string{
			2: {"r2"},
		}},
	}
	w := doRequest(setupTestRouter(h), http.MethodGet, "/v1/ai/roles/r2", "", makeToken(1))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetDeviceRole_RejectsOtherUsersDevice(t *testing.T) {
	h := &AgentHandler{
		agentAPI:      &mockAgentAPI{},
		userRoleStore: &mockUserRoleStore{allowAll: true},
		roleStore: &mockRoleBindingStore{
			bindings: map[string]string{"dev-2": "r2"},
			owners:   map[string]int64{"dev-2": 2},
		},
	}
	w := doRequest(setupTestRouter(h), http.MethodGet, "/v1/ai/device/dev-2/role", "", makeToken(1))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetRole_NotFound(t *testing.T) {
	api := &mockAgentAPI{roles: map[string]*tirtcapi.Role{}}
	h := &AgentHandler{agentAPI: api}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/roles/nonexistent", "", makeToken(1))
	resp := parseJSON(t, w)

	// nil role from API still returns 200 with null data
	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d", resp.Code)
	}
}

func TestGetRole_APIError(t *testing.T) {
	api := &mockAgentAPI{getRoleErr: errors.New("timeout")}
	h := &AgentHandler{agentAPI: api}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/roles/any", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 50200 {
		t.Fatalf("expected bad gateway code 50200, got %d", resp.Code)
	}
}

// ── UpdateRole ──

func TestUpdateRole_Success(t *testing.T) {
	api := &mockAgentAPI{
		roles: map[string]*tirtcapi.Role{"r1": {ID: "r1", Name: "Old"}},
	}
	h := &AgentHandler{agentAPI: api}
	router := setupTestRouter(h)

	w := doRequest(router, "PUT", "/v1/ai/roles/r1", `{"name":"New"}`, makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d", resp.Code)
	}
	if api.roles["r1"].Name != "New" {
		t.Fatalf("expected name 'New', got %q", api.roles["r1"].Name)
	}
}

func TestUpdateRole_APIError(t *testing.T) {
	api := &mockAgentAPI{updateRoleErr: errors.New("cloud error")}
	h := &AgentHandler{agentAPI: api}
	router := setupTestRouter(h)

	w := doRequest(router, "PUT", "/v1/ai/roles/r1", `{"name":"X"}`, makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 50200 {
		t.Fatalf("expected bad gateway code 50200, got %d", resp.Code)
	}
}

// ── DeleteRole ──

func TestDeleteRole_Success(t *testing.T) {
	api := &mockAgentAPI{
		roles: map[string]*tirtcapi.Role{"r1": {ID: "r1"}},
	}
	userRoleStore := &mockUserRoleStore{
		roles: map[int64][]string{1: {"r1"}},
	}
	h := &AgentHandler{agentAPI: api, userRoleStore: userRoleStore}
	router := setupTestRouter(h)

	w := doRequest(router, "DELETE", "/v1/ai/roles/r1", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d", resp.Code)
	}
	if _, exists := api.roles["r1"]; exists {
		t.Fatal("expected role r1 to be deleted")
	}
	if len(userRoleStore.roles[1]) != 0 {
		t.Fatalf("expected user role mapping to be empty, got %v", userRoleStore.roles[1])
	}
}

func TestDeleteRole_APIError(t *testing.T) {
	api := &mockAgentAPI{
		deleteRoleErr: errors.New("cloud error"),
		roles:         map[string]*tirtcapi.Role{"r1": {ID: "r1"}},
	}
	h := &AgentHandler{agentAPI: api}
	router := setupTestRouter(h)

	w := doRequest(router, "DELETE", "/v1/ai/roles/r1", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 50200 {
		t.Fatalf("expected bad gateway code 50200, got %d", resp.Code)
	}
}

// ── GetDefaultRole ──

func TestGetDefaultRole_Success(t *testing.T) {
	api := &mockAgentAPI{
		roles: map[string]*tirtcapi.Role{
			"default-1": {ID: "default-1", Name: "DefaultAgent"},
		},
	}
	h := &AgentHandler{agentAPI: api, defaultRoleID: "default-1"}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/roles/default", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d: %s", resp.Code, resp.Msg)
	}
}

func TestGetDefaultRole_NotConfigured(t *testing.T) {
	h := &AgentHandler{agentAPI: &mockAgentAPI{}, defaultRoleID: ""}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/roles/default", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 40400 {
		t.Fatalf("expected not found code 40400, got %d", resp.Code)
	}
}

func TestGetDefaultRole_APIError(t *testing.T) {
	api := &mockAgentAPI{getRoleErr: errors.New("timeout")}
	h := &AgentHandler{agentAPI: api, defaultRoleID: "default-1"}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/roles/default", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 50200 {
		t.Fatalf("expected bad gateway code 50200, got %d", resp.Code)
	}
}

func TestGetDefaultRole_NotFoundOnCloud(t *testing.T) {
	api := &mockAgentAPI{roles: map[string]*tirtcapi.Role{}}
	h := &AgentHandler{agentAPI: api, defaultRoleID: "default-1"}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/roles/default", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 40400 {
		t.Fatalf("expected not found code 40400, got %d", resp.Code)
	}
}

// ── Device-Role Binding ──

func TestGetDeviceRole_Bound(t *testing.T) {
	roleStore := &mockRoleBindingStore{
		bindings: map[string]string{"dev1": "role-abc"},
	}
	h := &AgentHandler{
		agentAPI:      &mockAgentAPI{},
		roleStore:     roleStore,
		defaultRoleID: "default-1",
	}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/device/dev1/role", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d", resp.Code)
	}
	data := resp.Data.(map[string]interface{})
	if data["role_id"] != "role-abc" {
		t.Fatalf("expected role_id 'role-abc', got %v", data["role_id"])
	}
	if data["default_role_id"] != "default-1" {
		t.Fatalf("expected default_role_id 'default-1', got %v", data["default_role_id"])
	}
}

func TestGetDeviceRole_NotBound(t *testing.T) {
	roleStore := &mockRoleBindingStore{bindings: map[string]string{}}
	h := &AgentHandler{
		agentAPI:      &mockAgentAPI{},
		roleStore:     roleStore,
		defaultRoleID: "default-1",
	}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/device/dev1/role", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d", resp.Code)
	}
	data := resp.Data.(map[string]interface{})
	if data["role_id"] != "" {
		t.Fatalf("expected empty role_id, got %v", data["role_id"])
	}
}

func TestSetDeviceRole_Success(t *testing.T) {
	roleStore := &mockRoleBindingStore{bindings: map[string]string{}}
	h := &AgentHandler{agentAPI: &mockAgentAPI{}, roleStore: roleStore}
	router := setupTestRouter(h)

	w := doRequest(router, "PUT", "/v1/ai/device/dev1/role", `{"role_id":"role-xyz"}`, makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d: %s", resp.Code, resp.Msg)
	}
	if roleStore.bindings["dev1"] != "role-xyz" {
		t.Fatalf("expected role-xyz, got %q", roleStore.bindings["dev1"])
	}
}

func TestSetDeviceRole_MissingRoleID(t *testing.T) {
	h := &AgentHandler{agentAPI: &mockAgentAPI{}}
	router := setupTestRouter(h)

	w := doRequest(router, "PUT", "/v1/ai/device/dev1/role", `{}`, makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 40000 {
		t.Fatalf("expected bad param code 40000, got %d", resp.Code)
	}
}

func TestDeleteDeviceRole_Success(t *testing.T) {
	roleStore := &mockRoleBindingStore{
		bindings: map[string]string{"dev1": "role-abc"},
	}
	h := &AgentHandler{agentAPI: &mockAgentAPI{}, roleStore: roleStore}
	router := setupTestRouter(h)

	w := doRequest(router, "DELETE", "/v1/ai/device/dev1/role", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d", resp.Code)
	}
	if _, exists := roleStore.bindings["dev1"]; exists {
		t.Fatal("expected dev1 binding to be deleted")
	}
}

// ── ListVoices ──

func TestListVoices_Success(t *testing.T) {
	api := &mockAgentAPI{
		voices: []tirtcapi.VoiceInfo{
			{ID: "v1", Name: "Voice1"},
			{ID: "v2", Name: "Voice2"},
		},
	}
	h := &AgentHandler{agentAPI: api}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/voices", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected code 200, got %d", resp.Code)
	}
	data := resp.Data.(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 voices, got %d", len(items))
	}
}

func TestListVoices_APIError(t *testing.T) {
	api := &mockAgentAPI{listVoicesErr: errors.New("timeout")}
	h := &AgentHandler{agentAPI: api}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/voices", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 50200 {
		t.Fatalf("expected bad gateway code 50200, got %d", resp.Code)
	}
}

// ── App MCP Tools (user-private: list returns local id+name, no cloud call) ──

func TestListAppMCPTools_ReturnsLocalBriefs(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows: map[string][]model.UserResource{
			"1:mcp": {
				{ResourceID: "m1", Name: "My MCP 1"},
				{ResourceID: "m2", Name: "My MCP 2"},
			},
		},
	}
	h := &AgentHandler{
		agentAPI:          &mockAgentAPI{},
		userResourceStore: resourceStore,
		defaultResources: map[string][]model.ResourceRef{
			"mcp": {{ID: "d1", Name: "Default MCP"}},
		},
	}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/mcp/app-tools", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Msg)
	}
	data := resp.Data.(map[string]interface{})
	items := data["items"].([]interface{})
	// 2 user-created + 1 default, served purely from local store — zero cloud calls.
	if len(items) != 3 {
		t.Fatalf("expected 3 items (2 user + 1 default), got %d: %v", len(items), items)
	}
}

func TestCreateAppMCPTool_Success(t *testing.T) {
	resourceStore := &mockUserResourceStore{rows: map[string][]model.UserResource{}}
	api := &mockAgentAPI{
		createAppMCPResult: &tirtcapi.AppMCPTool{
			ID:     "new-mcp",
			Config: &tirtcapi.MCPToolExternalRuntimeConfig{Name: "New MCP"},
		},
	}
	h := &AgentHandler{
		agentAPI:          api,
		userResourceStore: resourceStore,
		resourceQuota:     map[string]int{"mcp": 4},
	}
	router := setupTestRouter(h)

	body := `{"config":{"name":"New MCP","url":"http://x"}}`
	w := doRequest(router, "POST", "/v1/ai/mcp/app-tools", body, makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Msg)
	}
	rows := resourceStore.rows["1:mcp"]
	if len(rows) != 1 || rows[0].ResourceID != "new-mcp" || rows[0].Name != "New MCP" {
		t.Fatalf("expected local row {new-mcp,'New MCP'}, got %v", rows)
	}
}

func TestCreateAppMCPTool_QuotaExceeded(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows: map[string][]model.UserResource{
			"1:mcp": {
				{ResourceID: "m1", Name: "A"},
				{ResourceID: "m2", Name: "B"},
			},
		},
	}
	api := &mockAgentAPI{}
	h := &AgentHandler{
		agentAPI:          api,
		userResourceStore: resourceStore,
		resourceQuota:     map[string]int{"mcp": 2},
	}
	router := setupTestRouter(h)

	body := `{"config":{"name":"C","url":"http://x"}}`
	w := doRequest(router, "POST", "/v1/ai/mcp/app-tools", body, makeToken(1))
	resp := parseJSON(t, w)

	// quota=2 and user already owns 2 → rejected before touching the cloud.
	if resp.Code != 42900 {
		t.Fatalf("expected quota-exceeded code 42900, got %d: %s", resp.Code, resp.Msg)
	}
	if api.createAppMCPCalls != 0 {
		t.Fatalf("cloud CreateAppMCPTool must not be called when over quota, got %d calls", api.createAppMCPCalls)
	}
}

func TestUpdateAppMCPTool_SyncsLocalName(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows: map[string][]model.UserResource{
			"1:mcp": {{UserID: 1, Type: "mcp", ResourceID: "m1", Name: "Old"}},
		},
	}
	h := &AgentHandler{agentAPI: &mockAgentAPI{}, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	body := `{"config":{"name":"Fresh","url":"http://x"}}`
	w := doRequest(router, "PUT", "/v1/ai/mcp/app-tools/m1", body, makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Msg)
	}
	rows := resourceStore.rows["1:mcp"]
	if len(rows) != 1 || rows[0].Name != "Fresh" {
		t.Fatalf("expected local name synced to 'Fresh', got %v", rows)
	}
}

func TestDeleteAppMCPTool_RemovesLocalRow(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows: map[string][]model.UserResource{
			"1:mcp": {{UserID: 1, Type: "mcp", ResourceID: "m1", Name: "A"}},
		},
	}
	h := &AgentHandler{agentAPI: &mockAgentAPI{}, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	w := doRequest(router, "DELETE", "/v1/ai/mcp/app-tools/m1", "", makeToken(1))
	resp := parseJSON(t, w)

	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Msg)
	}
	if rows := resourceStore.rows["1:mcp"]; len(rows) != 0 {
		t.Fatalf("expected local row removed, got %v", rows)
	}
}

// ── Device plugins (same pattern; name = req.Name, type = device_plugin) ──

func TestCreatePlugin_Success(t *testing.T) {
	resourceStore := &mockUserResourceStore{rows: map[string][]model.UserResource{}}
	api := &mockAgentAPI{createPluginResult: &tirtcapi.DevicePlugin{ID: "new-plg", Name: "New Plugin"}}
	h := &AgentHandler{
		agentAPI:          api,
		userResourceStore: resourceStore,
		resourceQuota:     map[string]int{"device_plugin": 20},
	}
	router := setupTestRouter(h)

	body := `{"name":"New Plugin","action":"turn_on"}`
	w := doRequest(router, "POST", "/v1/ai/plugins", body, makeToken(1))
	resp := parseJSON(t, w)
	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Msg)
	}
	rows := resourceStore.rows["1:device_plugin"]
	if len(rows) != 1 || rows[0].ResourceID != "new-plg" || rows[0].Name != "New Plugin" {
		t.Fatalf("expected local row {new-plg,'New Plugin'} under device_plugin, got %v", rows)
	}
}

func TestCreatePlugin_QuotaExceeded(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows: map[string][]model.UserResource{
			"1:device_plugin": {{ResourceID: "p1"}, {ResourceID: "p2"}},
		},
	}
	api := &mockAgentAPI{}
	h := &AgentHandler{
		agentAPI:          api,
		userResourceStore: resourceStore,
		resourceQuota:     map[string]int{"device_plugin": 2},
	}
	router := setupTestRouter(h)

	body := `{"name":"C","action":"turn_on"}`
	w := doRequest(router, "POST", "/v1/ai/plugins", body, makeToken(1))
	resp := parseJSON(t, w)
	if resp.Code != 42900 {
		t.Fatalf("expected quota-exceeded code 42900, got %d: %s", resp.Code, resp.Msg)
	}
	if api.createPluginCalls != 0 {
		t.Fatalf("cloud CreateDevicePlugin must not be called over quota, got %d", api.createPluginCalls)
	}
}

// ── Knowledge indexes (name = req.Name, type = kb, resource_id = IndexID) ──

func TestCreateKnowledgeIndex_Success(t *testing.T) {
	resourceStore := &mockUserResourceStore{rows: map[string][]model.UserResource{}}
	api := &mockAgentAPI{createKBResult: &tirtcapi.KnowledgeIndexInfo{IndexID: "new-kb", Name: "New KB"}}
	h := &AgentHandler{
		agentAPI:          api,
		userResourceStore: resourceStore,
		resourceQuota:     map[string]int{"kb": 5},
	}
	router := setupTestRouter(h)

	body := `{"name":"New KB","description":"d"}`
	w := doRequest(router, "POST", "/v1/ai/knowledge/indexes", body, makeToken(1))
	resp := parseJSON(t, w)
	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Msg)
	}
	rows := resourceStore.rows["1:kb"]
	if len(rows) != 1 || rows[0].ResourceID != "new-kb" || rows[0].Name != "New KB" {
		t.Fatalf("expected local row {new-kb,'New KB'} under kb, got %v", rows)
	}
}

func TestCreateKnowledgeIndex_QuotaExceeded(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows: map[string][]model.UserResource{
			"1:kb": {{ResourceID: "k1"}, {ResourceID: "k2"}, {ResourceID: "k3"}, {ResourceID: "k4"}, {ResourceID: "k5"}},
		},
	}
	api := &mockAgentAPI{}
	h := &AgentHandler{
		agentAPI:          api,
		userResourceStore: resourceStore,
		resourceQuota:     map[string]int{"kb": 5},
	}
	router := setupTestRouter(h)

	body := `{"name":"X","description":"d"}`
	w := doRequest(router, "POST", "/v1/ai/knowledge/indexes", body, makeToken(1))
	resp := parseJSON(t, w)
	if resp.Code != 42900 {
		t.Fatalf("expected quota-exceeded code 42900, got %d: %s", resp.Code, resp.Msg)
	}
	if api.createKBCalls != 0 {
		t.Fatalf("cloud CreateKnowledgeIndex must not be called over quota, got %d", api.createKBCalls)
	}
}

func TestListKnowledgeDocuments_ForbiddenIfNotOwner(t *testing.T) {
	resourceStore := &mockUserResourceStore{rows: map[string][]model.UserResource{}}
	api := &mockAgentAPI{
		knowledgeDocuments: []tirtcapi.KnowledgeDocument{
			{DocumentID: "doc-foreign", Name: "foreign.txt"},
		},
	}
	h := &AgentHandler{agentAPI: api, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	w := doRequest(
		router,
		http.MethodGet,
		"/v1/ai/knowledge/indexes/foreign/documents",
		"",
		makeToken(1),
	)
	resp := parseJSON(t, w)
	if w.Code != http.StatusForbidden || resp.Code != 40300 {
		t.Fatalf("expected HTTP 403/code 40300, got HTTP %d/code %d: %s", w.Code, resp.Code, resp.Msg)
	}
	if api.listKnowledgeDocumentCalls != 0 {
		t.Fatalf("cloud document list must not be called for foreign index, got %d calls", api.listKnowledgeDocumentCalls)
	}
}

func TestListKnowledgeDocuments_AllowedForOwner(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows: map[string][]model.UserResource{
			"1:kb": {{UserID: 1, Type: "kb", ResourceID: "own-kb", Name: "Mine"}},
		},
	}
	api := &mockAgentAPI{
		knowledgeDocuments: []tirtcapi.KnowledgeDocument{
			{DocumentID: "doc-1", Name: "mine.txt"},
		},
	}
	h := &AgentHandler{agentAPI: api, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	w := doRequest(
		router,
		http.MethodGet,
		"/v1/ai/knowledge/indexes/own-kb/documents",
		"",
		makeToken(1),
	)
	resp := parseJSON(t, w)
	if resp.Code != 200 {
		t.Fatalf("expected owner access, got HTTP %d/code %d: %s", w.Code, resp.Code, resp.Msg)
	}
	if api.listKnowledgeDocumentCalls != 1 {
		t.Fatalf("cloud document list should be called once, got %d", api.listKnowledgeDocumentCalls)
	}
}

func TestListKnowledgeDocuments_AllowedForDefaultIndex(t *testing.T) {
	resourceStore := &mockUserResourceStore{rows: map[string][]model.UserResource{}}
	api := &mockAgentAPI{
		knowledgeDocuments: []tirtcapi.KnowledgeDocument{
			{DocumentID: "doc-default", Name: "default.txt"},
		},
	}
	h := &AgentHandler{
		agentAPI:          api,
		userResourceStore: resourceStore,
		defaultResources: map[string][]model.ResourceRef{
			"kb": {{ID: "default-kb", Name: "Default KB"}},
		},
	}
	router := setupTestRouter(h)

	w := doRequest(
		router,
		http.MethodGet,
		"/v1/ai/knowledge/indexes/default-kb/documents",
		"",
		makeToken(1),
	)
	resp := parseJSON(t, w)
	if resp.Code != 200 {
		t.Fatalf("expected default index access, got HTTP %d/code %d: %s", w.Code, resp.Code, resp.Msg)
	}
	if api.listKnowledgeDocumentCalls != 1 {
		t.Fatalf("cloud document list should be called once, got %d", api.listKnowledgeDocumentCalls)
	}
}

func TestListKnowledgeFiles_FiltersToCurrentUser(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows: map[string][]model.UserResource{
			"1:kb_file": {
				{UserID: 1, Type: "kb_file", ResourceID: "mine", Name: "mine.pdf"},
			},
			"2:kb_file": {
				{UserID: 2, Type: "kb_file", ResourceID: "foreign", Name: "foreign.pdf"},
			},
		},
	}
	api := &mockAgentAPI{
		knowledgeFiles: []tirtcapi.KnowledgeFileInfo{
			{FileID: "mine", FileName: "mine.pdf"},
			{FileID: "foreign", FileName: "foreign.pdf"},
		},
	}
	h := &AgentHandler{agentAPI: api, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	w := doRequest(
		router,
		http.MethodGet,
		"/v1/ai/knowledge/files",
		"",
		makeToken(1),
	)
	resp := parseJSON(t, w)
	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Msg)
	}
	data := resp.Data.(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected only one owned file, got %d: %v", len(items), items)
	}
	item := items[0].(map[string]interface{})
	if item["file_id"] != "mine" {
		t.Fatalf("foreign file leaked through list: %v", item)
	}
}

func TestListKnowledgeFiles_EmptyOwnershipSkipsCloud(t *testing.T) {
	resourceStore := &mockUserResourceStore{rows: map[string][]model.UserResource{}}
	api := &mockAgentAPI{
		knowledgeFiles: []tirtcapi.KnowledgeFileInfo{
			{FileID: "foreign", FileName: "foreign.pdf"},
		},
	}
	h := &AgentHandler{agentAPI: api, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	w := doRequest(
		router,
		http.MethodGet,
		"/v1/ai/knowledge/files",
		"",
		makeToken(1),
	)
	resp := parseJSON(t, w)
	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Msg)
	}
	data := resp.Data.(map[string]interface{})
	if items := data["items"].([]interface{}); len(items) != 0 {
		t.Fatalf("expected empty file list, got %v", items)
	}
	if api.listKnowledgeFileCalls != 0 {
		t.Fatalf("cloud list must be skipped without owned files, got %d calls", api.listKnowledgeFileCalls)
	}
}

func TestUploadKnowledgeFile_RecordsOwnership(t *testing.T) {
	resourceStore := &mockUserResourceStore{rows: map[string][]model.UserResource{}}
	api := &mockAgentAPI{
		uploadKnowledgeFileResult: &tirtcapi.KnowledgeFileSimpleResponse{FileID: "uploaded-file"},
	}
	h := &AgentHandler{agentAPI: api, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	w := doMultipartFileRequest(
		t,
		router,
		"/v1/ai/knowledge/files",
		makeToken(1),
		"file",
		"manual.pdf",
		[]byte("test-content"),
	)
	resp := parseJSON(t, w)
	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Msg)
	}
	rows := resourceStore.rows["1:kb_file"]
	if len(rows) != 1 ||
		rows[0].ResourceID != "uploaded-file" ||
		rows[0].Name != "manual.pdf" {
		t.Fatalf("expected uploaded file ownership record, got %v", rows)
	}
}

func TestUploadKnowledgeFile_OwnershipFailureCleansUpCloudFile(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows:   map[string][]model.UserResource{},
		addErr: errors.New("local ownership write failed"),
	}
	api := &mockAgentAPI{
		uploadKnowledgeFileResult: &tirtcapi.KnowledgeFileSimpleResponse{FileID: "orphan"},
	}
	h := &AgentHandler{agentAPI: api, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	w := doMultipartFileRequest(
		t,
		router,
		"/v1/ai/knowledge/files",
		makeToken(1),
		"file",
		"manual.pdf",
		[]byte("test-content"),
	)
	resp := parseJSON(t, w)
	if w.Code != http.StatusInternalServerError || resp.Code != 50000 {
		t.Fatalf("expected HTTP 500/code 50000, got HTTP %d/code %d: %s", w.Code, resp.Code, resp.Msg)
	}
	if api.deleteKnowledgeFileCalls != 1 {
		t.Fatalf("unowned cloud file should be cleaned up once, got %d deletes", api.deleteKnowledgeFileCalls)
	}
}

func TestDeleteKnowledgeFile_ForbiddenIfNotOwner(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows: map[string][]model.UserResource{
			"2:kb_file": {
				{UserID: 2, Type: "kb_file", ResourceID: "foreign", Name: "foreign.pdf"},
			},
		},
	}
	api := &mockAgentAPI{}
	h := &AgentHandler{agentAPI: api, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	w := doRequest(
		router,
		http.MethodDelete,
		"/v1/ai/knowledge/files/foreign",
		"",
		makeToken(1),
	)
	resp := parseJSON(t, w)
	if w.Code != http.StatusForbidden || resp.Code != 40300 {
		t.Fatalf("expected HTTP 403/code 40300, got HTTP %d/code %d: %s", w.Code, resp.Code, resp.Msg)
	}
	if api.deleteKnowledgeFileCalls != 0 {
		t.Fatalf("cloud delete must not be called for foreign file, got %d calls", api.deleteKnowledgeFileCalls)
	}
}

func TestDeleteKnowledgeFile_RemovesOwnedFile(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows: map[string][]model.UserResource{
			"1:kb_file": {
				{UserID: 1, Type: "kb_file", ResourceID: "mine", Name: "mine.pdf"},
			},
		},
	}
	api := &mockAgentAPI{}
	h := &AgentHandler{agentAPI: api, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	w := doRequest(
		router,
		http.MethodDelete,
		"/v1/ai/knowledge/files/mine",
		"",
		makeToken(1),
	)
	resp := parseJSON(t, w)
	if resp.Code != 200 {
		t.Fatalf("expected 200, got HTTP %d/code %d: %s", w.Code, resp.Code, resp.Msg)
	}
	if api.deleteKnowledgeFileCalls != 1 {
		t.Fatalf("cloud delete should be called once, got %d", api.deleteKnowledgeFileCalls)
	}
	if rows := resourceStore.rows["1:kb_file"]; len(rows) != 0 {
		t.Fatalf("ownership row was not removed: %v", rows)
	}
}

// ── Ownership checks (update/delete/get must be private) ──

func TestUpdateAppMCPTool_ForbiddenIfNotOwner(t *testing.T) {
	resourceStore := &mockUserResourceStore{rows: map[string][]model.UserResource{}} // user 1 owns nothing
	api := &mockAgentAPI{}
	h := &AgentHandler{agentAPI: api, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	body := `{"config":{"name":"X","url":"http://x"}}`
	w := doRequest(router, "PUT", "/v1/ai/mcp/app-tools/foreign-id", body, makeToken(1))
	resp := parseJSON(t, w)
	if resp.Code != 40300 {
		t.Fatalf("expected 403 not-owner, got %d: %s", resp.Code, resp.Msg)
	}
	if api.updateAppMCPCalls != 0 {
		t.Fatalf("cloud Update must not be called when not owner, got %d calls", api.updateAppMCPCalls)
	}
}

func TestUpdateAppMCPTool_AllowedForOwner(t *testing.T) {
	resourceStore := &mockUserResourceStore{
		rows: map[string][]model.UserResource{
			"1:mcp": {{UserID: 1, Type: "mcp", ResourceID: "own-id", Name: "old"}},
		},
	}
	api := &mockAgentAPI{}
	h := &AgentHandler{agentAPI: api, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	body := `{"config":{"name":"new","url":"http://x"}}`
	w := doRequest(router, "PUT", "/v1/ai/mcp/app-tools/own-id", body, makeToken(1))
	resp := parseJSON(t, w)
	if resp.Code != 200 {
		t.Fatalf("expected 200 for owner, got %d: %s", resp.Code, resp.Msg)
	}
	if api.updateAppMCPCalls != 1 {
		t.Fatalf("cloud Update should be called once for owner, got %d", api.updateAppMCPCalls)
	}
}

func TestGetAppMCPTool_AllowsDefaultResource(t *testing.T) {
	// default id: user does NOT own it, but it's in defaultResources → accessible.
	resourceStore := &mockUserResourceStore{rows: map[string][]model.UserResource{}}
	h := &AgentHandler{
		agentAPI:          &mockAgentAPI{getAppMCPToolResult: &tirtcapi.AppMCPTool{ID: "default-mcp"}},
		userResourceStore: resourceStore,
		defaultResources: map[string][]model.ResourceRef{
			"mcp": {{ID: "default-mcp", Name: "Default"}},
		},
	}
	router := setupTestRouter(h)

	w := doRequest(router, "GET", "/v1/ai/mcp/app-tools/default-mcp", "", makeToken(1))
	resp := parseJSON(t, w)
	if resp.Code != 200 {
		t.Fatalf("default resource should be accessible, got %d: %s", resp.Code, resp.Msg)
	}
}

func TestUpdatePlugin_ForbiddenIfNotOwner(t *testing.T) {
	resourceStore := &mockUserResourceStore{rows: map[string][]model.UserResource{}}
	h := &AgentHandler{agentAPI: &mockAgentAPI{}, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	body := `{"name":"X","action":"turn_on"}`
	w := doRequest(router, "PUT", "/v1/ai/plugins/foreign", body, makeToken(1))
	resp := parseJSON(t, w)
	if resp.Code != 40300 {
		t.Fatalf("expected 403 not-owner (device_plugin), got %d: %s", resp.Code, resp.Msg)
	}
}

func TestUpdateKnowledgeIndex_ForbiddenIfNotOwner(t *testing.T) {
	resourceStore := &mockUserResourceStore{rows: map[string][]model.UserResource{}}
	h := &AgentHandler{agentAPI: &mockAgentAPI{}, userResourceStore: resourceStore}
	router := setupTestRouter(h)

	body := `{"name":"X","description":"d"}`
	w := doRequest(router, "PUT", "/v1/ai/knowledge/indexes/foreign", body, makeToken(1))
	resp := parseJSON(t, w)
	if resp.Code != 40300 {
		t.Fatalf("expected 403 not-owner (kb), got %d: %s", resp.Code, resp.Msg)
	}
}

// ── parseInt / queryPagination ──

func TestParseInt(t *testing.T) {
	tests := []struct {
		input string
		want  int
		err   bool
	}{
		{"123", 123, false},
		{"0", 0, false},
		{"", 0, false}, // empty string: loop body never runs, returns 0, nil
		{"abc", 0, true},
		{"12a", 0, true},
		{"-1", 0, true}, // negative not supported
	}
	for _, tt := range tests {
		n, err := parseInt(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("parseInt(%q) err=%v, want err=%v", tt.input, err, tt.err)
		}
		if !tt.err && n != tt.want {
			t.Errorf("parseInt(%q) = %d, want %d", tt.input, n, tt.want)
		}
	}
}

func TestQueryPagination_Defaults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/test", nil)

	page, pageSize := queryPagination(c)
	if page != 1 {
		t.Errorf("default page = %d, want 1", page)
	}
	if pageSize != 20 {
		t.Errorf("default pageSize = %d, want 20", pageSize)
	}
}

func TestQueryPagination_Custom(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/test?page=3&page_size=50", nil)

	page, pageSize := queryPagination(c)
	if page != 3 {
		t.Errorf("page = %d, want 3", page)
	}
	if pageSize != 50 {
		t.Errorf("pageSize = %d, want 50", pageSize)
	}
}

func TestQueryPagination_ClampPageSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/test?page=1&page_size=200", nil) // >100 max

	_, pageSize := queryPagination(c)
	if pageSize != 20 { // should stay default, not use 200
		t.Errorf("pageSize = %d, want 20 (default, because 200 > 100)", pageSize)
	}
}

func TestQueryPagination_InvalidValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/test?page=abc&page_size=xyz", nil)

	page, pageSize := queryPagination(c)
	if page != 1 {
		t.Errorf("page = %d, want 1 (default for invalid)", page)
	}
	if pageSize != 20 {
		t.Errorf("pageSize = %d, want 20 (default for invalid)", pageSize)
	}
}

func TestQueryPagination_NegativePage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/test?page=-1&page_size=10", nil)

	page, _ := queryPagination(c)
	if page != 1 { // parseInt rejects "-1" because '-' is not a digit
		t.Errorf("page = %d, want 1 (default for negative)", page)
	}
}
