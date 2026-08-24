package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"thing-connect/internal/config"
	"thing-connect/internal/testenv"
	"thing-connect/voip-server/apiresp"
)

func init() { gin.SetMode(gin.TestMode) }

const testSecret = "test-jwt-secret"

type onlineCheckingBroker struct {
	clientID string
	online   bool
}

func (b *onlineCheckingBroker) Publish(_ string, _ byte, _ any) error {
	return nil
}

func (b *onlineCheckingBroker) IsOnline(_ context.Context, clientID string) bool {
	b.clientID = clientID
	return b.online
}

func TestIsDeviceOnlineUsesFormalMQTTClientID(t *testing.T) {
	broker := &onlineCheckingBroker{online: true}
	server := NewServer(nil, nil, nil, broker)

	if !server.IsDeviceOnline(context.Background(), "TIR123") {
		t.Fatal("expected configured online checker result")
	}
	if broker.clientID != "sn_TIR123" {
		t.Fatalf("online checker client id = %q, want %q", broker.clientID, "sn_TIR123")
	}
}

func makeToken(secret string, deviceID string, expiry time.Time) string {
	claims := jwt.MapClaims{"device_id": deviceID}
	if !expiry.IsZero() {
		claims["exp"] = expiry.Unix()
	}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	return tok
}

func jwtTestRouter() *gin.Engine {
	r := gin.New()
	r.GET("/protected", JWTAuth(testSecret), func(c *gin.Context) {
		c.JSON(200, gin.H{"device_id": currentDeviceID(c)})
	})
	return r
}

func TestJWTAuth_ValidToken(t *testing.T) {
	tok := makeToken(testSecret, "device-abc", time.Time{})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	jwtTestRouter().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), "device-abc") {
		t.Errorf("want device_id in response, got %s", w.Body.String())
	}
}

func TestJWTAuth_MissingAuthHeader(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/protected", nil)

	jwtTestRouter().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestJWTAuth_WrongPrefix(t *testing.T) {
	tok := makeToken(testSecret, "device-abc", time.Time{})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Token "+tok) // wrong prefix

	jwtTestRouter().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestJWTAuth_ExpiredToken(t *testing.T) {
	tok := makeToken(testSecret, "device-abc", time.Now().Add(-time.Hour))
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	jwtTestRouter().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 for expired token, got %d", w.Code)
	}
}

func TestJWTAuth_WrongSecret(t *testing.T) {
	tok := makeToken("other-secret", "device-abc", time.Time{})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	jwtTestRouter().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 for wrong secret, got %d", w.Code)
	}
}

func TestJWTAuth_MissingDeviceID(t *testing.T) {
	// token without device_id claim
	claims := jwt.MapClaims{"sub": "noid"}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	jwtTestRouter().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 for missing device_id, got %d", w.Code)
	}
}

func TestInternalUnbindInvalidCredentialUsesBusinessCode(t *testing.T) {
	server := NewServer(
		&config.Config{Internal: config.InternalCfg{Key: "expected-key"}},
		nil,
		nil,
		nil,
	)
	router := gin.New()
	router.POST("/", server.postInternalUnbind)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP=%d, want 200", recorder.Code)
	}
	var response struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != apiresp.ErrInternalCredential {
		t.Fatalf("code=%d, want %d", response.Code, apiresp.ErrInternalCredential)
	}
}

func TestNormalizeAuthRemark(t *testing.T) {
	remark, ok := normalizeAuthRemark("  小雨  ")
	if !ok || remark != "小雨" {
		t.Fatalf("normalizeAuthRemark = %q, %v; want 小雨, true", remark, ok)
	}

	remark, ok = normalizeAuthRemark("")
	if !ok || remark != "" {
		t.Fatalf("empty normalizeAuthRemark = %q, %v; want empty, true", remark, ok)
	}

	_, ok = normalizeAuthRemark(string(make([]rune, maxAuthRemarkChars+1)))
	if ok {
		t.Fatal("remark longer than 64 characters should be rejected")
	}
}

func TestVideoUIProfile(t *testing.T) {
	for _, rotation := range []int{0, 90, 180, 270} {
		profile := json.RawMessage(fmt.Sprintf(`{"camera_rotation":%d}`, rotation))
		if err := validateVideoUIProfile(profile); err != nil {
			t.Fatalf("rotation %d rejected: %v", rotation, err)
		}
		config := videoUIConfigFromProfile(string(profile))
		if config.CameraRotation == nil || *config.CameraRotation != rotation {
			t.Fatalf("rotation %d parsed as %#v", rotation, config.CameraRotation)
		}
	}
	for _, profile := range []string{
		`{"camera_rotation":45}`,
		`{"camera_rotation":"90"}`,
		`{"camera_rotation":null}`,
		`{"aspect_ratio":0}`,
		`{"aspect_ratio":"1.777"}`,
		`{"aspect_ratio":null}`,
		`{"hor_mirror":1}`,
		`{"vert_mirror":"false"}`,
		`{"vert_mirror":null}`,
		`{"object_fit":"cover"}`,
		`{"object_fit":1}`,
		`{"object_fit":null}`,
	} {
		if err := validateVideoUIProfile(json.RawMessage(profile)); err == nil {
			t.Fatalf("invalid profile accepted: %s", profile)
		}
	}
	valid := json.RawMessage(
		`{"aspect_ratio":1.7777777778,"hor_mirror":true,"vert_mirror":false,"object_fit":"contain"}`,
	)
	if err := validateVideoUIProfile(valid); err != nil {
		t.Fatalf("valid video UI profile rejected: %v", err)
	}
	config := videoUIConfigFromProfile(string(valid))
	if config.AspectRatio == nil || *config.AspectRatio != 1.7777777778 ||
		config.HorMirror == nil || !*config.HorMirror ||
		config.VertMirror == nil || *config.VertMirror ||
		config.ObjectFit == nil || *config.ObjectFit != "contain" {
		t.Fatalf("unexpected parsed video UI config: %+v", config)
	}
	if err := validateVideoUIProfile(json.RawMessage(`{"up_video_mt":"h264"}`)); err != nil {
		t.Fatalf("profile without video UI fields rejected: %v", err)
	}
	if err := validateVideoUIProfile(json.RawMessage(`{"video_res_mode":"developer-defined"}`)); err != nil {
		t.Fatalf("TiRTC profile fields must not be validated by ThingConnect: %v", err)
	}
}

func TestPostDeviceProfileRejectsNonObject(t *testing.T) {
	server := NewServer(nil, nil, nil, nil)
	router := gin.New()
	router.POST("/", func(c *gin.Context) {
		c.Set("device_id", "device-1")
		server.postDeviceProfile(c)
	})

	for _, profile := range []string{"null", `[]`, `"profile"`, `1`} {
		t.Run(profile, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(profile))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("HTTP=%d, want 200", recorder.Code)
			}
			var response apiresp.JSON
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Code != apiresp.ErrBadParam || response.Msg != "profile 必须是 JSON 对象" {
				t.Fatalf("response=%+v", response)
			}
		})
	}
}

func TestQueryWithVideoUIConfig(t *testing.T) {
	rotation := 90
	ratio := 16.0 / 9.0
	horMirror := true
	vertMirror := false
	objectFit := "contain"
	config := videoUIConfig{
		CameraRotation: &rotation,
		AspectRatio:    &ratio,
		HorMirror:      &horMirror,
		VertMirror:     &vertMirror,
		ObjectFit:      &objectFit,
	}
	if got := queryWithVideoUIConfig("", config); got !=
		"aspect_ratio=1.7777777777777777&camera_rotation=90&hor_mirror=true&object_fit=contain&vert_mirror=false" {
		t.Fatalf("empty query = %q", got)
	}
	if got := queryWithVideoUIConfig("foo=bar&camera_rotation=0", config); got !=
		"aspect_ratio=1.7777777777777777&camera_rotation=90&foo=bar&hor_mirror=true&object_fit=contain&vert_mirror=false" {
		t.Fatalf("merged query = %q", got)
	}
	if got := queryWithVideoUIConfig("foo=bar", videoUIConfig{}); got != "foo=bar" {
		t.Fatalf("query with empty config = %q", got)
	}
}

func TestWeChatLoginAndDeviceOwnershipChecks(t *testing.T) {
	cfg := testenv.LoadConfigOrSkip(t, "../config.yaml", "../../user-server/config.yaml")
	rdb := testenv.OpenRedisOrSkip(t, cfg)
	defer rdb.Close()
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	defer sqlDB.Close()

	ctx := context.Background()
	userID := time.Now().UnixNano()
	appID := fmt.Sprintf("wx-test-%d", userID)
	openID := fmt.Sprintf("openid-%d", userID)
	deviceID := fmt.Sprintf("WX%018d", userID%1_000_000_000_000_000_000)
	key := wxLoginBindingKey(userID, appID)
	defer rdb.Del(ctx, key)
	defer sqlDB.Exec(`DELETE FROM device_bind WHERE device_id=?`, deviceID)
	defer sqlDB.Exec(`DELETE FROM device_pool WHERE device_id=?`, deviceID)

	s := &Server{db: sqlDB, rdb: rdb}
	if err := rdb.Set(ctx, key, openID, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.verifyWeChatLogin(ctx, userID, appID, openID); err != nil || !ok {
		t.Fatalf("matching wechat login: ok=%v err=%v", ok, err)
	}
	if ok, err := s.verifyWeChatLogin(ctx, userID, appID, "forged"); err != nil || ok {
		t.Fatalf("forged openid: ok=%v err=%v", ok, err)
	}

	if _, err := sqlDB.Exec(
		`INSERT INTO device_pool (device_id, device_key, status) VALUES (?, 'test-key', 1)`,
		deviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO device_bind (device_id, user_id, assign) VALUES (?, ?, 'dynamic')`,
		deviceID, userID); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.userOwnsDevice(ctx, userID, deviceID); err != nil || !ok {
		t.Fatalf("owned device: ok=%v err=%v", ok, err)
	}
	if ok, err := s.userOwnsDevice(ctx, userID+1, deviceID); err != nil || ok {
		t.Fatalf("foreign device: ok=%v err=%v", ok, err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
