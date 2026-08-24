package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type memoryMFAChallengeStore struct {
	claimed map[string]bool
}

func newMemoryMFAChallengeStore() *memoryMFAChallengeStore {
	return &memoryMFAChallengeStore{claimed: map[string]bool{}}
}

func (s *memoryMFAChallengeStore) Claim(_ context.Context, challengeID string, _ time.Duration) (bool, error) {
	if s.claimed[challengeID] {
		return false, nil
	}
	s.claimed[challengeID] = true
	return true, nil
}

func (s *memoryMFAChallengeStore) Release(_ context.Context, challengeID string) error {
	delete(s.claimed, challengeID)
	return nil
}

func TestLoadAppConfigDefaultsAndMFAOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `
database:
  dsn: test-dsn
internal:
  key: 01234567890123456789012345678901
admin:
  jwt_secret: 12345678901234567890123456789012
  mfa_enabled: false
security:
  config_encryption_key_id: test-v1
  config_encryption_key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAppConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.HTTPPort != 9000 || cfg.Server.StaticDir != "admin/admin-web/dist" {
		t.Fatalf("unexpected server defaults: %+v", cfg.Server)
	}
	if cfg.Redis.Addr != "127.0.0.1:6379" || cfg.Job.MaxBytes != 10*1024*1024 {
		t.Fatalf("unexpected dependency/job defaults: redis=%q job=%+v", cfg.Redis.Addr, cfg.Job)
	}
	if cfg.Admin.AccessTTL != 15*time.Minute || cfg.Admin.RefreshTTL != 7*24*time.Hour || cfg.Admin.ChallengeTTL != 5*time.Minute {
		t.Fatalf("unexpected auth TTL defaults: %+v", cfg.Admin)
	}
	if cfg.Admin.MFAIsEnabled() {
		t.Fatal("explicit mfa_enabled=false was ignored")
	}
	if !(AdminAuthConfig{}).MFAIsEnabled() {
		t.Fatal("MFA must default to enabled when omitted")
	}
}

func TestLoadAppConfigRejectsMissingOrShortCredentials(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "missing dsn", raw: "internal:\n  key: 01234567890123456789012345678901\nadmin:\n  jwt_secret: 12345678901234567890123456789012\n", wantErr: "database.dsn"},
		{name: "short internal key", raw: "database:\n  dsn: test\ninternal:\n  key: short\nadmin:\n  jwt_secret: 12345678901234567890123456789012\n", wantErr: "internal.key"},
		{name: "short jwt", raw: "database:\n  dsn: test\ninternal:\n  key: 01234567890123456789012345678901\nadmin:\n  jwt_secret: short\n", wantErr: "admin.jwt_secret"},
		{name: "invalid yaml", raw: "database: [", wantErr: "parse admin config"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(test.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadAppConfig(path)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
			}
		})
	}
}

func TestAuthAndTokenPrimitives(t *testing.T) {
	if _, err := NewAuthService(nil, nil, nil, AuthConfig{JWTSecret: "short"}); err == nil {
		t.Fatal("short JWT secret accepted")
	}
	if _, err := NewAuthService(nil, nil, nil, AuthConfig{JWTSecret: strings.Repeat("j", 32)}); err == nil {
		t.Fatal("missing MFA challenge store accepted")
	}
	service, err := NewAuthService(nil, nil, newMemoryMFAChallengeStore(), AuthConfig{JWTSecret: strings.Repeat("j", 32)})
	if err != nil {
		t.Fatal(err)
	}
	access, refresh, challenge := service.TTLs()
	if access != 15*time.Minute || refresh != 7*24*time.Hour || challenge != 5*time.Minute {
		t.Fatalf("unexpected default TTLs: %v %v %v", access, refresh, challenge)
	}
	service.SetMFAEnabled(true)
	if !service.MFAEnabled() {
		t.Fatal("MFA runtime switch did not update")
	}
	service.SetSessionPolicy(time.Minute, time.Hour, 3)
	access, refresh, _ = service.TTLs()
	if access != time.Minute || refresh != time.Hour || service.cfg.MaxSessions != 3 {
		t.Fatalf("session policy did not update: %+v", service.cfg)
	}

	invalidPasswords := []string{
		"Abcdef1",
		"abcdefg1",
		"ABCDEFG1",
		"Abcdefgh",
		"中文大写A123",
	}
	for _, password := range invalidPasswords {
		if _, err := HashAdminPassword(password); !errors.Is(err, ErrInvalidAdminPassword) {
			t.Fatalf("invalid administrator password %q was accepted: %v", password, err)
		}
	}
	for _, password := range []string{"Abcdefg1", "中文Abcde1"} {
		if err := ValidateAdminPassword(password); err != nil {
			t.Fatalf("valid administrator password %q was rejected: %v", password, err)
		}
	}
	hash, err := HashAdminPassword("Abcdefg1")
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte("Abcdefg1")) != nil {
		t.Fatalf("valid administrator password was not hashed correctly: %v", err)
	}
	if _, err := RandomToken(15); err == nil {
		t.Fatal("short random token accepted")
	}
	token, err := RandomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("bad random token encoding: len=%d err=%v", len(decoded), err)
	}
	if TokenHash(token) == TokenHash(token+"x") || len(TokenHash(token)) != 64 {
		t.Fatal("token hash is not a stable SHA-256 hex digest")
	}
}

func TestConfigScopeAndSecretHelpers(t *testing.T) {
	tests := []struct {
		scopeType string
		scopeID   string
		wantType  string
		wantID    string
		wantErr   bool
	}{
		{wantType: "global"},
		{scopeType: "global", scopeID: "bad", wantErr: true},
		{scopeType: "instance", scopeID: "node-1", wantErr: true},
		{scopeType: "instance", wantErr: true},
		{scopeType: "tenant", wantErr: true},
	}
	for _, test := range tests {
		scopeType, scopeID, err := normalizeScope(test.scopeType, test.scopeID)
		if (err != nil) != test.wantErr || (!test.wantErr && (scopeType != test.wantType || scopeID != test.wantID)) {
			t.Fatalf("normalizeScope(%q,%q) = %q,%q,%v", test.scopeType, test.scopeID, scopeType, scopeID, err)
		}
	}

	if !containsMaskedSecret(map[string]any{"nested": []any{"******"}}) || !containsMaskedSecret("••••") {
		t.Fatal("masked secret marker was not detected")
	}
	if containsMaskedSecret(map[string]any{"password": "real-secret"}) {
		t.Fatal("ordinary secret was treated as a mask")
	}
	if err := validateSecretShape("user-server", "smtp", map[string]any{"password": "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := validateSecretShape("user-server", "smtp", map[string]any{"unknown": "secret"}); err == nil {
		t.Fatal("unknown SMTP secret field accepted")
	}
	if err := validateSecretShape("voip-server", "wechat.apps", map[string]any{"apps": map[string]any{"wx": map[string]any{"secret": "secret", "bad": "x"}}}); err == nil {
		t.Fatal("unknown WeChat secret field accepted")
	}
	if err := validateExistingSecretRequirement("common", "tirtc", json.RawMessage(`{"app_id":"app"}`), false); err == nil {
		t.Fatal("TiRTC config without credentials was accepted")
	}

	merged, err := mergeSecretJSON(
		json.RawMessage(`{"apps":{"wx":{"secret":"old","token":"keep"}}}`),
		json.RawMessage(`{"apps":{"wx":{"secret":"new"},"wx2":{"secret":"second"}}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatal(err)
	}
	wx := got["apps"].(map[string]any)["wx"].(map[string]any)
	if wx["secret"] != "new" || wx["token"] != "keep" {
		t.Fatalf("nested secret merge lost values: %s", merged)
	}
	if _, err := mergeSecretJSON(json.RawMessage(`[]`), json.RawMessage(`{}`)); err == nil {
		t.Fatal("non-object stored secret accepted")
	}
	if truncateString("abcdef", 3) != "abc" || truncateString("ab", 3) != "ab" {
		t.Fatal("truncateString returned an unexpected result")
	}
	if enabled, ok := mfaPolicyEnabled(json.RawMessage(`{"enabled":true}`)); !ok || !enabled {
		t.Fatal("valid MFA policy was not parsed")
	}
	if _, ok := mfaPolicyEnabled(json.RawMessage(`{"enabled":"true"}`)); ok {
		t.Fatal("invalid MFA policy was parsed")
	}
}

func TestRequiredAdminRoleIDs(t *testing.T) {
	if _, err := requiredAdminRoleIDs(nil); err == nil {
		t.Fatal("administrator without a role was accepted")
	}
	roles, err := requiredAdminRoleIDs([]int64{0, 9, 9, -1, 3})
	if err != nil || !reflect.DeepEqual(roles, []int64{9, 3}) {
		t.Fatalf("requiredAdminRoleIDs = %v, %v", roles, err)
	}
}

func TestRBACAndMenuValidationHelpers(t *testing.T) {
	permissions, ok := validatePermissions([]string{"user.read", "dashboard.read", "user.read"})
	if !ok || !reflect.DeepEqual(permissions, []string{"dashboard.read", "user.read"}) {
		t.Fatalf("permission normalization failed: %v %v", permissions, ok)
	}
	if _, ok := validatePermissions([]string{"unknown.permission"}); ok {
		t.Fatal("unknown permission accepted")
	}
	if !containsAllPermissions(append([]string(nil), AllPermissions...)) {
		t.Fatal("complete permission set was not recognized")
	}
	if containsAllPermissions(AllPermissions[:len(AllPermissions)-1]) {
		t.Fatal("incomplete permission set was treated as complete")
	}
	if got := uniquePositiveIDs([]int64{3, 0, -1, 2, 3}); !reflect.DeepEqual(got, []int64{3, 2}) {
		t.Fatalf("unexpected unique IDs: %v", got)
	}
	if !sameIDs([]int64{3, 2, 2}, []int64{2, 3}) || sameIDs([]int64{1}, []int64{2}) {
		t.Fatal("role ID comparison returned an unexpected result")
	}
	if !sameStrings([]string{"b", "a"}, []string{"a", "b"}) || sameStrings([]string{"a"}, []string{"b"}) {
		t.Fatal("permission comparison returned an unexpected result")
	}
	if !containsID([]int64{1, 2}, 2) || containsID([]int64{1, 2}, 3) || boolInt(true) != 1 || boolInt(false) != 0 {
		t.Fatal("basic RBAC helper returned an unexpected result")
	}
	if !validMenuRequest(menuRequest{MenuType: 1, Visible: 1, Status: 1}) {
		t.Fatal("valid directory menu rejected")
	}
	if !validMenuRequest(menuRequest{MenuType: 2, Path: "/users", PermissionCode: "user.read", Visible: 1, Status: 1}) {
		t.Fatal("registered page menu rejected")
	}
	if validMenuRequest(menuRequest{MenuType: 2, Path: "/unregistered", Visible: 1, Status: 1}) {
		t.Fatal("unregistered dynamic component path accepted")
	}
}

func TestMarshalSafeRedactsTransientCredentials(t *testing.T) {
	value := roleRequest{
		Code:            "operator",
		Name:            "运维",
		CurrentMFACode:  "123456",
		CurrentRecovery: "recovery-value",
	}
	got := marshalSafe(value)
	if strings.Contains(got, "123456") || strings.Contains(got, "recovery-value") {
		t.Fatalf("audit JSON leaked transient credentials: %s", got)
	}
	if !strings.Contains(got, "operator") {
		t.Fatalf("audit JSON lost non-sensitive fields: %s", got)
	}
}

func TestHTTPParsingHelpers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("GET", "/?page=0&page_size=999&created_from=2026-08-01&created_to=2026-08-20", nil)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	page, size := pageParams(context)
	if page != 1 || size != 100 {
		t.Fatalf("unexpected page params: %d %d", page, size)
	}
	where := " WHERE 1=1"
	args := []any{}
	if err := appendDateRange(context, "created_at", &where, &args); err != nil {
		t.Fatal(err)
	}
	if where != " WHERE 1=1 AND created_at>=? AND created_at<?" || len(args) != 2 {
		t.Fatalf("unexpected date filter: %q %#v", where, args)
	}
	if got := args[1].(time.Time); got.Format("2006-01-02") != "2026-08-21" {
		t.Fatalf("created_to must be inclusive, got upper bound %v", got)
	}

	bad := httptest.NewRequest("GET", "/?created_from=08-01-2026", nil)
	badContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	badContext.Request = bad
	if err := appendDateRange(badContext, "created_at", &where, &args); err == nil {
		t.Fatal("invalid date accepted")
	}
	if bearerToken("Bearer token") != "token" || bearerToken("bearer   token ") != "token" || bearerToken("Basic token") != "" {
		t.Fatal("bearer token parser returned an unexpected result")
	}
	if id, err := positiveID("42"); err != nil || id != 42 {
		t.Fatalf("positive ID rejected: %d %v", id, err)
	}
	if _, err := positiveID("0"); err == nil {
		t.Fatal("zero ID accepted")
	}
	if !validExtra(nil) || !validExtra(json.RawMessage(`{"a":1}`)) || validExtra(json.RawMessage(`[1]`)) {
		t.Fatal("dictionary extra JSON validation returned an unexpected result")
	}

	sortRequest := httptest.NewRequest("GET", "/?sort_by=active_time&sort_order=asc", nil)
	sortContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	sortContext.Request = sortRequest
	order, err := listOrder(sortContext, map[string]string{"id": "d.id", "active_time": "d.active_time"}, "id", "d.id")
	if err != nil || order != " ORDER BY d.active_time ASC,d.id ASC" {
		t.Fatalf("unexpected stable list order: %q %v", order, err)
	}
	invalidSort := httptest.NewRequest("GET", "/?sort_by=active_time&sort_order=sideways", nil)
	invalidSortContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	invalidSortContext.Request = invalidSort
	if _, err := listOrder(invalidSortContext, map[string]string{"active_time": "d.active_time"}, "active_time", "d.id"); err == nil {
		t.Fatal("invalid sort order accepted")
	}
}

func TestVoIPAppDeviceFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("GET", "/?keyword=device&auth_status=active&profile_reported=true", nil)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	where, args, err := voipAppDeviceFilter(context, "wx-app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, "a.device_id LIKE ?") ||
		!strings.Contains(where, "a.auth_status=?") ||
		!strings.Contains(where, "p.device_id IS NOT NULL") {
		t.Fatalf("unexpected VoIP device filter: %q", where)
	}
	if len(args) != 7 || args[0] != "wx-app" || args[1] != "%device%" || args[6] != "active" {
		t.Fatalf("unexpected VoIP device filter args: %#v", args)
	}

	for _, query := range []string{"auth_status=unknown", "profile_reported=maybe"} {
		badRequest := httptest.NewRequest("GET", "/?"+query, nil)
		badContext, _ := gin.CreateTestContext(httptest.NewRecorder())
		badContext.Request = badRequest
		if _, _, err := voipAppDeviceFilter(badContext, "wx-app"); err == nil {
			t.Fatalf("invalid filter %q was accepted", query)
		}
	}
}
