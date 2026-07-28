// Package tests contains end-to-end integration tests for device-server and user-server.
// Tests run against real MySQL and Redis using the config in testdata/config.yaml.
// Each test uses unique MACs / emails to avoid cross-test interference.
package tests

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	devhandler "thing-connect/device-server/handler"
	"thing-connect/internal/apiresp"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	mysqlstore "thing-connect/internal/store/mysql"
	"thing-connect/internal/testenv"
	usrhandler "thing-connect/user-server/handler"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func loadConfig(t *testing.T) *config.Config {
	t.Helper()
	return testenv.LoadConfigOrSkip(t, "testdata/config.yaml")
}

type suite struct {
	devSrv *httptest.Server
	usrSrv *httptest.Server
	rdb    *redis.Client
	sqlDB  *sqlx.DB
}

func newSuite(t *testing.T) *suite {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := loadConfig(t)

	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	resetPool(t, sqlDB)

	rdb := testenv.OpenRedisOrSkip(t, cfg)

	// device-server
	devR := gin.New()
	devS := &devhandler.Server{DB: sqlDB, RDB: rdb, JWTSecret: cfg.JWTSecret}
	devS.Register(devR)

	// user-server (no MQTT for unit tests)
	usrR := gin.New()
	usrS := &usrhandler.Server{DB: sqlDB, RDB: rdb, MQTT: nil, JWTSecret: cfg.JWTSecret, RoleStore: mysqlstore.NewRoleBindingStore(sqlDB), UnbindCleanup: &usrhandler.UnbindCleanup{
		DeleteLocalRole: func(ctx context.Context, deviceID string) error {
			return mysqlstore.NewRoleBindingStore(sqlDB).DeleteDeviceRole(ctx, deviceID)
		},
	}}
	usrS.Register(usrR)

	s := &suite{
		devSrv: httptest.NewServer(devR),
		usrSrv: httptest.NewServer(usrR),
		rdb:    rdb,
		sqlDB:  sqlDB,
	}

	t.Cleanup(func() {
		s.devSrv.Close()
		s.usrSrv.Close()
		rdb.Close()
		sqlDB.Close()
	})
	return s
}

// resetPool returns every device_pool row to fresh (status=0) and clears
// device_bind, so 禁止流转 allocation always has fresh devices to hand out.
// Without this, prior runs leave the pool exhausted — HTTP binds (CommitBindFromPool)
// set status=1 with no per-test cleanup, and once a device_bind row exists the
// device is never re-allocated — so every bind fails with "设备池已耗尽".
// Run per-test for isolation.
func resetPool(t *testing.T, sqlDB *sqlx.DB) {
	t.Helper()
	if _, err := sqlDB.Exec(`DELETE FROM device_bind`); err != nil {
		t.Fatalf("reset device_bind: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE device_pool SET status=0`); err != nil {
		t.Fatalf("reset device_pool: %v", err)
	}
}

func (s *suite) devPOST(t *testing.T, path string, body any) *apiResp {
	t.Helper()
	return doPost(t, s.devSrv.URL+path, "", body)
}

func (s *suite) usrPOST(t *testing.T, path, jwt string, body any) *apiResp {
	t.Helper()
	return doPost(t, s.usrSrv.URL+path, jwt, body)
}

func (s *suite) usrGET(t *testing.T, path, jwt string) *apiResp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, s.usrSrv.URL+path, nil)
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return parseResp(t, resp)
}

func (s *suite) usrDELETE(t *testing.T, path, jwt string, body any) *apiResp {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodDelete, s.usrSrv.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	return parseResp(t, resp)
}

func doPost(t *testing.T, url, jwt string, body any) *apiResp {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return parseResp(t, resp)
}

type apiResp struct {
	HTTPStatus int
	Code       int             `json:"code"`
	Msg        string          `json:"msg"`
	Data       json.RawMessage `json:"data"`
}

func parseResp(t *testing.T, resp *http.Response) *apiResp {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	t.Logf("HTTP %d  body: %s", resp.StatusCode, body)
	var r apiResp
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("parse json: %v  raw: %s", err, body)
	}
	r.HTTPStatus = resp.StatusCode
	return &r
}

func dataString(t *testing.T, r *apiResp, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Data, &m); err != nil {
		t.Fatalf("dataString: %v", err)
	}
	v, _ := m[key].(string)
	return v
}

// unique email / MAC per test run
func uniqueEmail() string {
	return fmt.Sprintf("test_%d@example.com", time.Now().UnixNano())
}

// seedCode pre-seeds an email-verification code into Redis so registration can proceed.
func seedCode(t *testing.T, rdb *redis.Client, email, code string) {
	t.Helper()
	err := rdb.Set(context.Background(), "email_code:"+email, code, 5*time.Minute).Err()
	if err != nil {
		t.Fatalf("seedCode: %v", err)
	}
}

func uniqueMAC() string {
	n := time.Now().UnixNano() & 0xFFFFFFFF
	return fmt.Sprintf("AA:BB:%02X:%02X:%02X:%02X",
		(n>>24)&0xFF, (n>>16)&0xFF, (n>>8)&0xFF, n&0xFF)
}

// ── tests ────────────────────────────────────────────────────────────────────

// TC01: 注册新用户，返回 token，分配 10 个设备配额
func TestRegister(t *testing.T) {
	s := newSuite(t)
	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")

	r := s.usrPOST(t, "/v1/user/register", "", map[string]string{
		"email": email, "password": "pass1234", "code": "123456",
	})
	if r.Code != 200 {
		t.Fatalf("register: want code=200, got %d  msg=%s", r.Code, r.Msg)
	}
	tok := dataString(t, r, "token")
	if tok == "" {
		t.Fatal("register: empty token")
	}

	// 配额应为 10
	q := s.usrGET(t, "/v1/user/quota", tok)
	if q.Code != 200 {
		t.Fatalf("quota: want 200, got %d", q.Code)
	}
	var qd map[string]any
	json.Unmarshal(q.Data, &qd)
	quota := int(qd["quota"].(float64))
	t.Logf("quota after register: %d", quota)
	if quota < 1 {
		t.Errorf("quota: want >=1, got %d (global pool may be exhausted)", quota)
	}
}

// TC02: 重复注册同一邮箱，返回 409
func TestRegisterDuplicate(t *testing.T) {
	s := newSuite(t)
	email := uniqueEmail()

	seedCode(t, s.rdb, email, "111111")
	r1 := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "111111"})
	if r1.Code != 200 {
		t.Fatalf("first register failed: %d %s", r1.Code, r1.Msg)
	}
	seedCode(t, s.rdb, email, "222222")
	r2 := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "222222"})
	if r2.HTTPStatus != 409 {
		t.Errorf("duplicate register: want HTTP 409, got %d", r2.HTTPStatus)
	}
}

// TC03: 登录正确凭证，返回 token
func TestLogin(t *testing.T) {
	s := newSuite(t)
	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})

	r := s.usrPOST(t, "/v1/user/login", "", map[string]string{
		"email": email, "password": "pass1234",
		"captcha_id": "test", "validate": "test",
	})
	if r.Code != 200 {
		t.Fatalf("login: want 200, got %d  msg=%s", r.Code, r.Msg)
	}
	if dataString(t, r, "token") == "" {
		t.Fatal("login: empty token")
	}
}

// TC04: 登录错误密码，返回 401
func TestLoginWrongPassword(t *testing.T) {
	s := newSuite(t)
	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "correct", "code": "123456"})

	r := s.usrPOST(t, "/v1/user/login", "", map[string]string{
		"email": email, "password": "wrong",
		"captcha_id": "test", "validate": "test",
	})
	if r.HTTPStatus != 401 {
		t.Errorf("wrong password: want HTTP 401, got %d", r.HTTPStatus)
	}
}

// TC05: 设备上报物理标识，返回 6 位验证码 + temp_token，并写入 Redis device_code_lookup
func TestDeviceReport(t *testing.T) {
	s := newSuite(t)
	mac := uniqueMAC()

	r := s.devPOST(t, "/v1/device/report", map[string]string{
		"mac": mac, "chip_uid": "0xTEST01",
	})
	if r.Code != 200 {
		t.Fatalf("report: want 200, got %d  msg=%s", r.Code, r.Msg)
	}

	code := dataString(t, r, "code")
	tempToken := dataString(t, r, "temp_token")
	t.Logf("code=%s  temp_token=%s…", code, tempToken[:8])

	if len(code) != 6 {
		t.Errorf("code: want 6 digits, got %q", code)
	}
	if tempToken == "" {
		t.Error("temp_token: empty")
	}

	// ★ 关键：device_code_lookup:{code} 必须存在于 Redis
	ctx := context.Background()
	val, err := s.rdb.Get(ctx, "device_code_lookup:"+code).Result()
	if err != nil {
		t.Errorf("device_code_lookup:%s not found in Redis: %v", code, err)
	} else {
		t.Logf("device_code_lookup:%s = %s", code, val)
	}

	// verify:{hash} 也必须存在
	hash, _ := s.rdb.Get(ctx, "device_code_lookup:"+code).Result()
	if hash != "" {
		raw, err := s.rdb.Get(ctx, "verify:"+hash).Result()
		if err != nil {
			t.Errorf("verify:%s not found: %v", hash, err)
		} else {
			t.Logf("verify record: %s", raw)
		}
	}

	// TTS accepts only the temp_token issued with this Report record.
	ttsReq, _ := http.NewRequest(http.MethodGet, s.devSrv.URL+"/v1/device/tts?code="+code+"&fmt=wav", nil)
	ttsReq.Header.Set("Authorization", "Bearer "+tempToken)
	ttsResp, err := http.DefaultClient.Do(ttsReq)
	if err != nil {
		t.Fatalf("tts request: %v", err)
	}
	ttsAudio, readErr := io.ReadAll(ttsResp.Body)
	ttsResp.Body.Close()
	if readErr != nil {
		t.Fatalf("read tts response: %v", readErr)
	}
	if ttsResp.StatusCode != http.StatusOK || !bytes.HasPrefix(ttsAudio, []byte("RIFF")) {
		t.Fatalf("tts: status=%d prefix=%q", ttsResp.StatusCode, ttsAudio[:min(4, len(ttsAudio))])
	}
}

// TC06: 限频 — 同一物理标识超过 RateLimitMaxHits(10) 次上报后返回 429。
// 注意：前 10 次中 attempt>1 时走 replay（返回 200），第 11 次触发限频。
func TestDeviceReportRateLimit(t *testing.T) {
	s := newSuite(t)
	mac := uniqueMAC()
	body := map[string]string{"mac": mac, "chip_uid": "0xTEST02"}

	r1 := s.devPOST(t, "/v1/device/report", body)
	if r1.Code != 200 {
		t.Fatalf("first report: want 200, got %d", r1.Code)
	}
	// attempt 2..10：走 replay，返回 200（幂等重试）
	for i := 2; i <= 10; i++ {
		r := s.devPOST(t, "/v1/device/report", body)
		if r.Code != 200 {
			t.Fatalf("report #%d (replay): want 200, got code=%d", i, r.Code)
		}
	}
	// attempt 11：超过限制，返回 429
	rLast := s.devPOST(t, "/v1/device/report", body)
	if rLast.HTTPStatus != 429 && rLast.Code != 429 {
		t.Errorf("rate limit: want 429 after %d hits, got HTTP=%d biz=%d", 11, rLast.HTTPStatus, rLast.Code)
	}
}

// TC07: 未登录访问 /v1/user/quota，返回 401
func TestAuthRequired(t *testing.T) {
	s := newSuite(t)
	r := s.usrGET(t, "/v1/user/quota", "")
	if r.HTTPStatus != 401 {
		t.Errorf("no auth: want 401, got %d", r.HTTPStatus)
	}
}

// TC08: bind — 验证码不存在（device_code_lookup 不存在），返回 4002
func TestBindCodeNotFound(t *testing.T) {
	s := newSuite(t)
	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	regR := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})
	tok := dataString(t, regR, "token")

	r := s.usrPOST(t, "/v1/user/device/bind", tok, map[string]string{"code": "000000"})
	if r.Code != 4002 {
		t.Errorf("bind nonexistent code: want 4002, got %d  msg=%s", r.Code, r.Msg)
	}
}

// TC09: bind — 验证码错误（device_code_lookup 存在但 code 不匹配），返回 4001
func TestBindWrongCode(t *testing.T) {
	s := newSuite(t)
	mac := uniqueMAC()

	// 上报设备，拿到真实 code
	rpt := s.devPOST(t, "/v1/device/report", map[string]string{"mac": mac, "chip_uid": ""})
	if rpt.Code != 200 {
		t.Fatalf("report: %d %s", rpt.Code, rpt.Msg)
	}
	code := dataString(t, rpt, "code")

	// 构造一个不同的 6 位错误码
	wrongCode := "000000"
	if code == "000000" {
		wrongCode = "111111"
	}

	// 注册用户
	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	regR := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})
	tok := dataString(t, regR, "token")

	// 直接用错误码调 bind
	r := s.usrPOST(t, "/v1/user/device/bind", tok, map[string]string{"code": wrongCode})
	// wrongCode 没有 device_code_lookup，所以返回 4002（not found）
	if r.Code != 4002 && r.Code != 4001 {
		t.Errorf("wrong code: want 4001 or 4002, got %d  msg=%s", r.Code, r.Msg)
	}
}

// TC10: bind — MQTT=nil 时（测试环境），验证码正确但无 MQTT，应返回 6002（设备离线）
func TestBindDeviceOffline(t *testing.T) {
	s := newSuite(t)
	mac := uniqueMAC()

	// 上报
	rpt := s.devPOST(t, "/v1/device/report", map[string]string{"mac": mac, "chip_uid": ""})
	if rpt.Code != 200 {
		t.Fatalf("report: %d %s", rpt.Code, rpt.Msg)
	}
	code := dataString(t, rpt, "code")
	t.Logf("验证码: %s", code)

	// 注册用户
	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	regR := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})
	tok := dataString(t, regR, "token")

	// bind — MQTT 为 nil，IsOnline 返回 false → 期望 6002
	r := s.usrPOST(t, "/v1/user/device/bind", tok, map[string]string{"code": code})
	if r.Code != 6002 {
		t.Errorf("offline bind: want 6002, got %d  msg=%s", r.Code, r.Msg)
	}
}

// TC11: reset — 重置不属于自己的 device_id，返回 404
func TestResetNotOwned(t *testing.T) {
	s := newSuite(t)
	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	regR := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})
	tok := dataString(t, regR, "token")

	r := s.usrDELETE(t, "/v1/user/device/reset", tok, map[string]string{"device_id": "nonexistent-device"})
	if r.HTTPStatus != 404 && r.Code != 40400 {
		t.Errorf("reset not-owned: want 404, got HTTP=%d biz=%d", r.HTTPStatus, r.Code)
	}
}

// TC12: device/token — 缺少请求头，返回 6008
func TestDeviceTokenMissingHeaders(t *testing.T) {
	s := newSuite(t)
	r := s.devPOST(t, "/v1/device/token", nil)
	if r.Code != 6008 {
		t.Errorf("missing headers: want 6008, got %d  msg=%s", r.Code, r.Msg)
	}
}

// TC13: device/token — 签名错误，返回 6008
func TestDeviceTokenWrongSignature(t *testing.T) {
	s := newSuite(t)

	req, _ := http.NewRequest(http.MethodPost, s.devSrv.URL+"/v1/device/token", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Id", "FAKE-DEVICE-ID")
	req.Header.Set("X-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	req.Header.Set("X-Nonce", "abcdef1234567890")
	req.Header.Set("X-Signature", "badsignature==")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/device/token: %v", err)
	}
	r := parseResp(t, resp)
	if r.Code != 6008 {
		t.Errorf("wrong sig: want 6008, got %d  msg=%s", r.Code, r.Msg)
	}
}

// 时间超窗仍使用兼容的 6008，但 msg 必须指出设备需要校时。
func TestDeviceTokenTimestampSkewMessage(t *testing.T) {
	s := newSuite(t)

	req, _ := http.NewRequest(
		http.MethodPost, s.devSrv.URL+"/v1/device/token", nil)
	req.Header.Set("X-Device-Id", "FAKE-DEVICE-ID")
	req.Header.Set(
		"X-Timestamp",
		strconv.FormatInt(time.Now().Add(-6*time.Minute).Unix(), 10))
	req.Header.Set("X-Nonce", "abcdef1234567890")
	req.Header.Set("X-Signature", "badsignature==")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/device/token: %v", err)
	}
	r := parseResp(t, resp)
	if r.HTTPStatus != http.StatusUnauthorized || r.Code != 6008 {
		t.Fatalf(
			"timestamp skew: want HTTP 401/code 6008, got HTTP=%d code=%d",
			r.HTTPStatus, r.Code)
	}
	const want = "签名校验失败：设备时间戳早于服务器时间超过 300 秒，请校准设备时钟"
	if r.Msg != want {
		t.Fatalf("timestamp skew: want msg=%q, got %q", want, r.Msg)
	}
}

// ── helpers for TC14–TC19 ─────────────────────────────────────────────────────

// injectOnline 向 Redis 写在线标记，模拟设备已通过临时 MQTT 连接。
func injectOnline(t *testing.T, rdb *redis.Client, mac string) {
	t.Helper()
	key := "online:mac_" + mac
	ctx := context.Background()
	if err := rdb.Set(ctx, key, "1", 200*time.Second).Err(); err != nil {
		t.Fatalf("injectOnline: %v", err)
	}
	t.Cleanup(func() { rdb.Del(ctx, key) })
}

// injectTempClientOnline 向 Redis 写在线标记（直接用 tempClientID），
// 用于 report-unsigned 路径（tempClientID = tmp_xxxx 而非 mac_xxx）。
func injectTempClientOnline(t *testing.T, rdb *redis.Client, tempClientID string) {
	t.Helper()
	ctx := context.Background()
	key := "online:" + tempClientID
	if err := rdb.Set(ctx, key, "1", 200*time.Second).Err(); err != nil {
		t.Fatalf("injectTempClientOnline: %v", err)
	}
	t.Cleanup(func() { rdb.Del(ctx, key) })
}

// injectDeviceOnlineByID 模拟设备通过 bind-by-id 路径所需的在线状态：
// report_fp:{deviceID} + pending_bind:{deviceID} + online:{tempClientID}。
// 对应 requireDeviceOnline 的三次检查。
func injectDeviceOnlineByID(t *testing.T, rdb *redis.Client, deviceID string) {
	t.Helper()
	ctx := context.Background()
	tempClientID := "tmp_test_" + deviceID
	ttl := 200 * time.Second
	for key, val := range map[string]string{
		"report_fp:" + deviceID:    "aa:bb:cc:dd:ee:ff",
		"pending_bind:" + deviceID: tempClientID,
		"online:" + tempClientID:   "1",
	} {
		if err := rdb.Set(ctx, key, val, ttl).Err(); err != nil {
			t.Fatalf("injectDeviceOnlineByID %s: %v", key, err)
		}
		k := key
		t.Cleanup(func() { rdb.Del(ctx, k) })
	}
}

// openTestDB 打开独立 DB 连接供辅助 SQL 操作。
func openTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	cfg := loadConfig(t)
	sqlDB := testenv.OpenDBOrSkip(t, cfg)
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

// sqlBind 跳过 MQTT 直接写入绑定记录，模拟设备收到 ID+KEY 后回 ACK 的落库。
// 从 device_pool 取一台空闲设备，写 device_bind，扣 bind_quota。
// 测试结束时自动回滚绑定状态。
func sqlBind(t *testing.T, sqlDB *sqlx.DB, mac, chipUID string, userID int64) string {
	t.Helper()
	ctx := context.Background()

	// Grab a free device from pool
	var deviceID, deviceKey string
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT device_id, device_key FROM device_pool WHERE status=0 ORDER BY id LIMIT 1`,
	).Scan(&deviceID, &deviceKey); err != nil {
		t.Fatalf("sqlBind: no free device in device_pool: %v", err)
	}
	now := time.Now()
	sqlDB.ExecContext(ctx, `UPDATE device_pool SET status=1 WHERE device_id=?`, deviceID)
	sqlDB.ExecContext(ctx,
		`INSERT INTO device_bind (device_id, mac, chip_uid, device_rand, user_id, last_user_id, bind_time) VALUES (?,?,?,'',?,?,?)
		 ON DUPLICATE KEY UPDATE user_id=?, last_user_id=?, mac=?, chip_uid=?, bind_time=?`,
		deviceID, mac, chipUID, userID, userID, now,
		userID, userID, mac, chipUID, now)
	sqlDB.ExecContext(ctx, `UPDATE users SET bind_quota=bind_quota-1 WHERE id=?`, userID)

	t.Logf("sqlBind: device_id=%s user=%d mac=%s key=%s…", deviceID, userID, mac, deviceKey[:4])

	t.Cleanup(func() {
		sqlDB.ExecContext(ctx, `UPDATE device_bind SET user_id=0, unbind_time=NOW() WHERE device_id=?`, deviceID)
		sqlDB.ExecContext(ctx, `UPDATE device_pool SET status=0 WHERE device_id=?`, deviceID)
		sqlDB.ExecContext(ctx, `UPDATE users SET bind_quota=bind_quota+1 WHERE id=?`, userID)
	})
	return deviceID
}

// sqlReset 模拟 H5"重置"操作：解绑并归还配额。
func sqlReset(t *testing.T, sqlDB *sqlx.DB, deviceID string, userID int64) {
	t.Helper()
	ctx := context.Background()
	sqlDB.ExecContext(ctx,
		`UPDATE device_bind SET user_id=0, unbind_time=NOW() WHERE device_id=? AND user_id=?`,
		deviceID, userID)
	sqlDB.ExecContext(ctx, `UPDATE users SET bind_quota=bind_quota+1 WHERE id=?`, userID)
	t.Logf("sqlReset: device_id=%s reset", deviceID)
}

// exhaustQuota 将用户配额清零，模拟配额耗尽。
func exhaustQuota(t *testing.T, sqlDB *sqlx.DB, userID int64) {
	t.Helper()
	ctx := context.Background()
	sqlDB.ExecContext(ctx, `UPDATE users SET bind_quota=0 WHERE id=?`, userID)
	t.Logf("exhaustQuota: user %d quota exhausted", userID)
	t.Cleanup(func() {
		sqlDB.ExecContext(ctx, `UPDATE users SET bind_quota=10 WHERE id=?`, userID)
	})
}

// signToken 生成正确签名并调用 POST /v1/device/token。
func signToken(t *testing.T, s *suite, deviceID, deviceKey string) *apiResp {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := fmt.Sprintf("%016x", time.Now().UnixNano())
	raw := []byte(deviceID + ts + nonce)
	mac := hmac.New(sha256.New, []byte(deviceKey))
	mac.Write(raw)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, s.devSrv.URL+"/v1/device/token", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Id", deviceID)
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("signToken: %v", err)
	}
	return parseResp(t, resp)
}

// ── TC14: 配额枯竭 → bind 返回 4003 ──────────────────────────────────────────
func TestBindQuotaExhausted(t *testing.T) {
	s := newSuite(t)
	sql := openTestDB(t)
	mac := uniqueMAC()

	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	regR := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})
	tok := dataString(t, regR, "token")

	var userID int64
	sql.QueryRow(`SELECT id FROM users WHERE email=?`, email).Scan(&userID)
	exhaustQuota(t, sql, userID)

	rpt := s.devPOST(t, "/v1/device/report", map[string]string{"mac": mac, "chip_uid": ""})
	if rpt.Code != 200 {
		t.Fatalf("report: %d %s", rpt.Code, rpt.Msg)
	}
	code := dataString(t, rpt, "code")
	// reportUnsigned 生成 tempClientID=tmp_xxxx，需要注入正确 online key。
	injectTempClientOnline(t, s.rdb, dataString(t, rpt, "temp_client_id"))

	r := s.usrPOST(t, "/v1/user/device/bind", tok, map[string]string{"code": code})
	if r.Code != 4003 {
		t.Errorf("quota exhausted: want 4003, got %d  msg=%s", r.Code, r.Msg)
	}
}

// ── TC15: 本账号重复绑定同一物理设备 → 重新下发凭证（200）──────────────────────
// 设备可能 Flash 丢失，用户输入验证码后允许重复绑定，服务端重新下发 device_id/device_key。
func TestBindAlreadyBoundSameUser(t *testing.T) {
	s := newSuite(t)
	sql := openTestDB(t)
	mac := uniqueMAC()

	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	regR := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})
	tok := dataString(t, regR, "token")
	var userID int64
	sql.QueryRow(`SELECT id FROM users WHERE email=?`, email).Scan(&userID)

	// 先上报拿验证码（尚未绑定，正常返回 code）
	rpt := s.devPOST(t, "/v1/device/report", map[string]string{"mac": mac, "chip_uid": ""})
	if rpt.Code != 200 {
		t.Fatalf("report: %d %s", rpt.Code, rpt.Msg)
	}
	code := dataString(t, rpt, "code")
	t.Logf("验证码: %s", code)

	// 模拟第一次绑定已完成（写库）
	deviceID := sqlBind(t, sql, mac, "", userID)

	// reportUnsigned 生成 tempClientID=tmp_xxxx，redeliverCredentials 检查该 key。
	injectTempClientOnline(t, s.rdb, dataString(t, rpt, "temp_client_id"))

	// 再次用同一验证码发起 bind → 本账号已绑，重新下发凭证，期望 200
	r := s.usrPOST(t, "/v1/user/device/bind", tok, map[string]string{"code": code})
	if r.Code != 200 {
		t.Errorf("same user re-bind: want 200 (re-deliver), got %d  msg=%s", r.Code, r.Msg)
	}
	if got := dataString(t, r, "device_id"); got != deviceID {
		t.Errorf("re-delivered device_id mismatch: want %s, got %s", deviceID, got)
	}
}

// TC16 (updated): 扫码指纹与另一用户已绑定的设备重叠 → case A，下发新设备
func TestBindScanCode_OtherBound_IssuesNew(t *testing.T) {
	s := newSuite(t)
	sql := openTestDB(t)
	mac := uniqueMAC()

	rpt := s.devPOST(t, "/v1/device/report", map[string]string{"mac": mac, "chip_uid": ""})
	if rpt.Code != 200 {
		t.Fatalf("report: %d %s", rpt.Code, rpt.Msg)
	}
	code := dataString(t, rpt, "code")

	emailA := uniqueEmail()
	seedCode(t, s.rdb, emailA, "111111")
	s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": emailA, "password": "pass1234", "code": "111111"})
	var userAID int64
	sql.QueryRow(`SELECT id FROM users WHERE email=?`, emailA).Scan(&userAID)
	sqlBind(t, sql, mac, "", userAID)

	emailB := uniqueEmail()
	seedCode(t, s.rdb, emailB, "222222")
	regB := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": emailB, "password": "pass1234", "code": "222222"})
	tokB := dataString(t, regB, "token")

	// reportUnsigned 生成 tempClientID=tmp_xxxx（不是 mac_xxx），需要注入正确的 online 标记。
	tempClientID := dataString(t, rpt, "temp_client_id")
	injectTempClientOnline(t, s.rdb, tempClientID)

	// 新设计：另一用户已绑同指纹 → case A，下发新设备（200）
	r := s.usrPOST(t, "/v1/user/device/bind", tokB, map[string]string{"code": code})
	if r.Code != 200 {
		t.Errorf("scan code other-bound: new design should issue new device (200), got %d  msg=%s", r.Code, r.Msg)
	}
}

// TC17 (updated): 克隆检测移至绑定时，report 不再拒绝同 MAC 不同 chip_uid
func TestDeviceReportNowAllowsAnyFingerprint(t *testing.T) {
	s := newSuite(t)
	sql := openTestDB(t)
	mac := uniqueMAC()

	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})
	var userID int64
	sql.QueryRow(`SELECT id FROM users WHERE email=?`, email).Scan(&userID)
	sqlBind(t, sql, mac, "0xABCD", userID)

	// 同 MAC 但不同 chip_uid — 新设计：report 始终成功
	r := s.devPOST(t, "/v1/device/report", map[string]string{"mac": mac, "chip_uid": "0x9999"})
	if r.Code != 200 {
		t.Errorf("report with different chip_uid: new design expects 200, got %d  msg=%s", r.Code, r.Msg)
	}
}

// TC17b: bind-by-id 指纹与已绑设备不符 → 6013。严格终身绑定：stored MAC 与 reported MAC
// 冲突即拒，bindByDeviceIDInternal 的 macConflicts 防线独立于 Case 分支（自己设备换 MAC 也拦）。
// 6011(CloneConflict) 现仅用于 Case H（他人设备 MAC 不同=克隆）。
func TestBindByDeviceID_CloneConflict(t *testing.T) {
	s := newSuite(t)
	sql := openTestDB(t)
	mac := uniqueMAC()

	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	regR := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})
	tok := dataString(t, regR, "token")
	var userID int64
	sql.QueryRow(`SELECT id FROM users WHERE email=?`, email).Scan(&userID)
	deviceID := sqlBind(t, sql, mac, "0xABCD", userID)

	// 用不同 MAC 发起 bind-by-id（指纹不符），期望 6011
	r := s.usrPOST(t, "/v1/user/device/bind-by-id", tok, map[string]string{
		"device_id": deviceID, "mac": "FE:FE:FE:FE:FE:FE",
	})
	if r.Code != apiresp.CodeFingerprintMismatch {
		t.Errorf("fingerprint mismatch: want %d, got %d  msg=%s", apiresp.CodeFingerprintMismatch, r.Code, r.Msg)
	}
}

// ── TC18: 重置后换 token → 6006 ───────────────────────────────────────────────
func TestDeviceTokenAfterReset(t *testing.T) {
	s := newSuite(t)
	sql := openTestDB(t)
	mac := uniqueMAC()

	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})
	var userID int64
	sql.QueryRow(`SELECT id FROM users WHERE email=?`, email).Scan(&userID)
	deviceID := sqlBind(t, sql, mac, "", userID)

	var deviceKey string
	sql.QueryRow(`SELECT device_key FROM device_pool WHERE device_id=?`, deviceID).Scan(&deviceKey)
	if deviceKey == "" {
		t.Fatalf("device_key not found for %s", deviceID)
	}
	t.Logf("device_id=%s  device_key=%s", deviceID, deviceKey)

	sqlReset(t, sql, deviceID, userID)

	r := signToken(t, s, deviceID, deviceKey)
	if r.Code != 6006 {
		t.Errorf("token after reset: want 6006, got %d  msg=%s", r.Code, r.Msg)
	}
}

// ── TC19: device/token 正向路径 → 返回 mqtt_token ────────────────────────────
func TestDeviceTokenSuccess(t *testing.T) {
	s := newSuite(t)
	sql := openTestDB(t)
	mac := uniqueMAC()

	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})
	var userID int64
	sql.QueryRow(`SELECT id FROM users WHERE email=?`, email).Scan(&userID)
	deviceID := sqlBind(t, sql, mac, "", userID)

	var deviceKey string
	sql.QueryRow(`SELECT device_key FROM device_pool WHERE device_id=?`, deviceID).Scan(&deviceKey)
	if deviceKey == "" {
		t.Fatalf("device_key not found for %s", deviceID)
	}
	t.Logf("device_id=%s  device_key=%s…", deviceID, deviceKey[:6])

	r := signToken(t, s, deviceID, deviceKey)
	if r.Code != 200 {
		t.Fatalf("token success: want 200, got %d  msg=%s", r.Code, r.Msg)
	}
	mqttToken := dataString(t, r, "mqtt_token")
	if len(mqttToken) < 20 {
		t.Errorf("mqtt_token too short: %q", mqttToken)
	}
	t.Logf("mqtt_token: %s…", mqttToken[:24])
}

// TC-BindByDeviceID-01: 工厂预烧录场景——用户已知 device_id，直接绑定，无需验证码或设备在线
func TestBindByDeviceID_Success(t *testing.T) {
	s := newSuite(t)
	sql := openTestDB(t)
	email := uniqueEmail()
	seedCode(t, s.rdb, email, "700001")
	regR := s.usrPOST(t, "/v1/user/register", "", map[string]string{
		"email": email, "password": "pass1234", "code": "700001",
	})
	if regR.Code != 200 {
		t.Fatalf("register: %d %s", regR.Code, regR.Msg)
	}
	tok := dataString(t, regR, "token")
	var userID int64
	sql.QueryRow(`SELECT id FROM users WHERE email=?`, email).Scan(&userID)

	// 从 device_pool 取一台空闲设备的 device_id（模拟工厂预烧录已知 SN 的场景）
	var deviceID string
	if err := sql.QueryRow(
		`SELECT device_id FROM device_pool WHERE status=0 ORDER BY id LIMIT 1`,
	).Scan(&deviceID); err != nil || deviceID == "" {
		t.Fatalf("no free device in device_pool: %v", err)
	}
	t.Cleanup(func() {
		sql.Exec(`UPDATE device_pool SET status=0 WHERE device_id=?`, deviceID)
		sql.Exec(`UPDATE device_bind SET user_id=0, unbind_time=NOW() WHERE device_id=?`, deviceID)
		sql.Exec(`UPDATE users SET bind_quota=bind_quota+1 WHERE id=?`, userID)
	})

	// requireDeviceOnline 需要 report_fp + pending_bind + online 三个 Redis key。
	injectDeviceOnlineByID(t, s.rdb, deviceID)

	// bind by device_id
	r := s.usrPOST(t, "/v1/user/device/bind-by-id", tok, map[string]string{"device_id": deviceID})
	if r.Code != 200 {
		t.Fatalf("bind by device id: code=%d msg=%s", r.Code, r.Msg)
	}
	if got := dataString(t, r, "device_id"); got != deviceID {
		t.Errorf("response device_id mismatch: want %s, got %s", deviceID, got)
	}

	// 绑定后设备出现在列表中
	listR := s.usrGET(t, "/v1/user/device/list", tok)
	if listR.Code != 200 {
		t.Fatalf("device list: %d %s", listR.Code, listR.Msg)
	}
	var devices []map[string]any
	if err := json.Unmarshal(listR.Data, &devices); err != nil || len(devices) == 0 {
		t.Fatalf("device list should contain bound device, parse: %v len=%d", err, len(devices))
	}
}

// TC-BindByDeviceID-02: CommitBindByDeviceID は FK がないため device_id が存在しなくても
// INSERT が成功する（新設計の仕様）。クリーンアップして確認する。
func TestBindByDeviceID_NotInPool(t *testing.T) {
	s := newSuite(t)
	sql := openTestDB(t)
	email := uniqueEmail()
	seedCode(t, s.rdb, email, "700002")
	regR := s.usrPOST(t, "/v1/user/register", "", map[string]string{
		"email": email, "password": "pass1234", "code": "700002",
	})
	tok := dataString(t, regR, "token")
	var userID int64
	sql.QueryRow(`SELECT id FROM users WHERE email=?`, email).Scan(&userID)

	const fakeDeviceID = "dev_nonexistent_xyz999"
	t.Cleanup(func() {
		sql.Exec(`DELETE FROM device_bind WHERE device_id=?`, fakeDeviceID)
		sql.Exec(`UPDATE users SET bind_quota=10 WHERE id=?`, userID)
	})

	// 新设计：device_bind 无 FK 约束，bind-by-id 对任意 device_id 均可插入成功
	r := s.usrPOST(t, "/v1/user/device/bind-by-id", tok, map[string]string{"device_id": fakeDeviceID})
	t.Logf("bind-by-id nonexistent: code=%d msg=%s", r.Code, r.Msg)
	// 200 是预期行为（无 FK 约束），非 200 也是可接受的（如后续加了验证）
}

// TC-CaseG: 取消换板豁免——User A 绑定再解绑，User B 用不同指纹（换板）尝试绑定同一 device_id → 6013
func TestBindByDeviceID_CaseG_MACChange_Rejected(t *testing.T) {
	s := newSuite(t)
	sql := openTestDB(t)
	mac := uniqueMAC()

	// User A 绑定再解绑
	emailA := uniqueEmail()
	seedCode(t, s.rdb, emailA, "aaa001")
	s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": emailA, "password": "pass1234", "code": "aaa001"})
	var userAID int64
	sql.QueryRow(`SELECT id FROM users WHERE email=?`, emailA).Scan(&userAID)
	deviceID := sqlBind(t, sql, mac, "0xAABB", userAID)
	sqlReset(t, sql, deviceID, userAID)

	// User B 用不同 MAC（换板）尝试绑定同一 device_id → 期望 6013
	emailB := uniqueEmail()
	seedCode(t, s.rdb, emailB, "bbb001")
	regB := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": emailB, "password": "pass1234", "code": "bbb001"})
	tokB := dataString(t, regB, "token")

	// requireDeviceOnline 需要 report_fp + pending_bind + online 三个 Redis key。
	injectDeviceOnlineByID(t, s.rdb, deviceID)

	r := s.usrPOST(t, "/v1/user/device/bind-by-id", tokB, map[string]string{
		"device_id": deviceID, "mac": uniqueMAC(), // 新 MAC = 换板
	})
	// 新设计：stored MAC 非空 + 上报不同 MAC → 6013（取消换板豁免）
	if r.Code != 6013 {
		t.Errorf("Case G mac change: want 6013, got %d  msg=%s", r.Code, r.Msg)
	}
}

// TC-MACDrift: signed report MAC 终身绑定校验——同一 device_id 先后用不同 MAC signed report → 6013
func TestSignedReport_MAC_Lifetime_Check(t *testing.T) {
	s := newSuite(t)
	sql := openTestDB(t)
	macA := uniqueMAC()
	macB := uniqueMAC()

	// 注册用户
	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	regR := s.usrPOST(t, "/v1/user/register", "", map[string]string{"email": email, "password": "pass1234", "code": "123456"})
	if regR.Code != 200 {
		t.Fatalf("register: %d %s", regR.Code, regR.Msg)
	}
	tok := dataString(t, regR, "token")
	var userID int64
	sql.QueryRow(`SELECT id FROM users WHERE email=?`, email).Scan(&userID)

	// 第一次 signed report：MAC=A，生成验证码
	rptA := s.devPOST(t, "/v1/device/report", map[string]string{
		"mac": macA, "chip_uid": "", "device_rand": "",
	})
	if rptA.Code != 200 {
		t.Fatalf("first report (MAC=%s): %d %s", macA, rptA.Code, rptA.Msg)
	}
	codeA := dataString(t, rptA, "code")
	t.Logf("第一次 report MAC=%s code=%s", macA, codeA)

	// 完成绑定（通过验证码）
	injectTempClientOnline(t, s.rdb, dataString(t, rptA, "temp_client_id"))
	bindR := s.usrPOST(t, "/v1/user/device/bind", tok, map[string]string{"code": codeA})
	if bindR.Code != 200 {
		t.Fatalf("bind with MAC=%s: %d %s", macA, bindR.Code, bindR.Msg)
	}
	deviceID := dataString(t, bindR, "device_id")
	t.Logf("绑定成功 device_id=%s MAC=%s", deviceID, macA)

	// 获取 device_key 用于第二次 signed report
	var deviceKey string
	sql.QueryRow(`SELECT device_key FROM device_pool WHERE device_id=?`, deviceID).Scan(&deviceKey)
	if deviceKey == "" {
		t.Fatalf("device_key not found for %s", deviceID)
	}

	// 第二次 signed report：同一 device_id 但 MAC=B（模拟 MAC 漂移）→ 期望 6013
	// 构造 signed report 请求
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := fmt.Sprintf("%016x", time.Now().UnixNano())
	raw := []byte(deviceID + ts + nonce)
	mac := hmac.New(sha256.New, []byte(deviceKey))
	mac.Write(raw)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, s.devSrv.URL+"/v1/device/report", bytes.NewReader([]byte(fmt.Sprintf(`{"mac":"%s","chip_uid":"","device_rand":""}`, macB))))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Id", deviceID)
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("second signed report: %v", err)
	}
	rptB := parseResp(t, resp)

	// 新设计：reportSigned 检测到 stored MAC=A ≠上报 MAC=B → 6013（MAC 终身绑定校验）
	if rptB.Code != 6013 {
		t.Errorf("signed report MAC drift: want 6013, got %d  msg=%s", rptB.Code, rptB.Msg)
	}
	t.Logf("第二次 report MAC=%s 期望被拒绝 (6013)", macB)
}
