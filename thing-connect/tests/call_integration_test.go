// Integration tests for call-server (device-to-device calling). Runs against
// the same real MySQL + Redis as integration_test.go, reusing its helpers
// (loadConfig/doPost/parseResp/dataString) since this file is in the same
// package. MQTT is faked (fakeBroker) so tests don't need a live broker and
// can assert on exactly what was published.
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	"thing-connect/call-server/apiresp"
	callhandler "thing-connect/call-server/handler"
	"thing-connect/internal/cache"
	"thing-connect/internal/config"
	"thing-connect/internal/db"
	mysqlstore "thing-connect/internal/store/mysql"
)

// ── fake MQTT broker ─────────────────────────────────────────────────────────

type publishedMsg struct {
	Topic string
	QoS   byte
	Type  string
	From  string
	Msg   map[string]any
}

type fakeBroker struct {
	mu        sync.Mutex
	online    map[string]bool
	published []publishedMsg
}

func newFakeBroker() *fakeBroker {
	return &fakeBroker{online: map[string]bool{}}
}

func (b *fakeBroker) SetOnline(deviceID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.online["sn_"+deviceID] = true
}

func (b *fakeBroker) IsOnline(_ context.Context, clientID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.online[clientID]
}

func (b *fakeBroker) Publish(topic string, qos byte, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var env struct {
		Type string         `json:"type"`
		From string         `json:"from"`
		Msg  map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, publishedMsg{Topic: topic, QoS: qos, Type: env.Type, From: env.From, Msg: env.Msg})
	return nil
}

// findPublished returns the most recent message of msgType sent on a topic
// containing clientIDSubstr (e.g. "sn_TIRZ...").
func (b *fakeBroker) findPublished(msgType, clientIDSubstr string) *publishedMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.published) - 1; i >= 0; i-- {
		m := b.published[i]
		if m.Type == msgType && strings.Contains(m.Topic, clientIDSubstr) {
			return &m
		}
	}
	return nil
}

// ── suite ────────────────────────────────────────────────────────────────────

type callSuite struct {
	callSrv *httptest.Server
	sqlDB   *sqlx.DB
	rdb     *redis.Client
	cfg     *config.Config
	broker  *fakeBroker
	devices []string
}

func newCallSuite(t *testing.T) *callSuite {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := loadConfig(t)
	sqlDB, err := db.Open(cfg.Database)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(sqlDB); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	rdb, err := cache.New(cfg.Redis)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}

	broker := newFakeBroker()
	devStore := mysqlstore.NewDeviceStore(sqlDB)

	r := gin.New()
	callhandler.NewServer(cfg, sqlDB, rdb, broker, devStore).Register(r)

	srv := httptest.NewServer(r)
	cs := &callSuite{callSrv: srv, sqlDB: sqlDB, rdb: rdb, cfg: cfg, broker: broker}
	t.Cleanup(func() {
		for _, deviceID := range cs.devices {
			_, _ = sqlDB.Exec(`DELETE FROM call_contact WHERE device_id_a=? OR device_id_b=?`, deviceID, deviceID)
			_, _ = sqlDB.Exec(`DELETE FROM voip_device_auth WHERE device_id=?`, deviceID)
			_, _ = sqlDB.Exec(`DELETE FROM device_bind WHERE device_id=?`, deviceID)
			_, _ = sqlDB.Exec(`DELETE FROM device_pool WHERE device_id=?`, deviceID)
		}
		srv.Close()
		rdb.Close()
		sqlDB.Close()
	})
	return cs
}

func (cs *callSuite) POST(t *testing.T, path, jwtTok string, body any) *apiResp {
	t.Helper()
	return doPost(t, cs.callSrv.URL+path, jwtTok, body)
}

func (cs *callSuite) POSTInternal(t *testing.T, path string, body any) *apiResp {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, cs.callSrv.URL+path, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", cs.cfg.Call.InternalKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return parseResp(t, resp)
}

func (cs *callSuite) GET(t *testing.T, path, jwtTok string) *apiResp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, cs.callSrv.URL+path, nil)
	if jwtTok != "" {
		req.Header.Set("Authorization", "Bearer "+jwtTok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return parseResp(t, resp)
}

func (cs *callSuite) PUT(t *testing.T, path, jwtTok string, body any) *apiResp {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, cs.callSrv.URL+path, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	if jwtTok != "" {
		req.Header.Set("Authorization", "Bearer "+jwtTok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	return parseResp(t, resp)
}

func (cs *callSuite) DELETE(t *testing.T, path, jwtTok string) *apiResp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, cs.callSrv.URL+path, nil)
	if jwtTok != "" {
		req.Header.Set("Authorization", "Bearer "+jwtTok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	return parseResp(t, resp)
}

// deviceJWT mints a JWT with the device_id claim, matching what device-server's
// /v1/device/token issues (internal/service/device.go issueMQTTToken) — call-server
// validates it the same way voip-server does, so tests don't need a real device-server.
func deviceJWT(t *testing.T, secret, deviceID string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"device_id": deviceID,
		"iat":       time.Now().Unix(),
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("deviceJWT: %v", err)
	}
	return signed
}

func userJWT(t *testing.T, secret string, userID int64) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID,
		"iat":     time.Now().Unix(),
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("userJWT: %v", err)
	}
	return signed
}

var deviceSeq int64

func uniqueDeviceID(t *testing.T) string {
	deviceSeq++
	return fmt.Sprintf("TESTDEV%d%d", time.Now().UnixNano(), deviceSeq)
}

func minTestStr(a, b string) string {
	if a < b {
		return a
	}
	return b
}

func maxTestStr(a, b string) string {
	if a > b {
		return a
	}
	return b
}

// seedDevice inserts device_pool + device_bind rows so GetDeviceKey/GetBindByDeviceID
// resolve, mirroring what a real bind flow would have produced.
func (cs *callSuite) seedDevice(t *testing.T, deviceID string, userID int64) {
	t.Helper()
	cs.devices = append(cs.devices, deviceID)
	if _, err := cs.sqlDB.Exec(
		`INSERT INTO device_pool (device_id, device_key, status) VALUES (?, ?, 1)
		 ON DUPLICATE KEY UPDATE device_key=device_key`,
		deviceID, "key-"+deviceID); err != nil {
		t.Fatalf("seedDevice pool: %v", err)
	}
	if _, err := cs.sqlDB.Exec(
		`INSERT INTO device_bind (device_id, user_id, assign) VALUES (?, ?, 'dynamic')
		 ON DUPLICATE KEY UPDATE user_id=?`,
		deviceID, userID, userID); err != nil {
		t.Fatalf("seedDevice bind: %v", err)
	}
}

// seedContact inserts an accepted (or given-status) call_contact row for (a,b).
func (cs *callSuite) seedContact(t *testing.T, a, b string, userA, userB int64, status int) {
	t.Helper()
	idA, idB, uA, uB := a, b, userA, userB
	if idA > idB {
		idA, idB, uA, uB = b, a, userB, userA
	}
	if _, err := cs.sqlDB.Exec(
		`INSERT INTO call_contact (device_id_a, device_id_b, source, initiator, user_id_a, user_id_b, status)
		 VALUES (?, ?, 'manual', 'a', ?, ?, ?)
		 ON DUPLICATE KEY UPDATE status=?`,
		idA, idB, uA, uB, status, status); err != nil {
		t.Fatalf("seedContact: %v", err)
	}
}

func (cs *callSuite) roomIDForLock(t *testing.T, deviceID string) string {
	t.Helper()
	v, err := cs.rdb.Get(context.Background(), "room:lock:"+deviceID).Result()
	if err == redis.Nil {
		return ""
	}
	if err != nil {
		t.Fatalf("roomIDForLock: %v", err)
	}
	return v
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestCall_SingleCallEndToEnd(t *testing.T) {
	cs := newCallSuite(t)
	a, b := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, a, 1001)
	cs.seedDevice(t, b, 1002)
	cs.seedContact(t, a, b, 1001, 1002, 1)
	cs.broker.SetOnline(b)

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, a)
	jwtB := deviceJWT(t, cs.cfg.JWTSecret, b)

	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{b}, "call_type": "video"})
	if r.Code != 200 {
		t.Fatalf("call/request: want code=200, got %d msg=%s", r.Code, r.Msg)
	}
	roomID := dataString(t, r, "room_id")
	if roomID == "" {
		t.Fatal("call/request: empty room_id")
	}
	if msg := cs.broker.findPublished("call_incoming", "sn_"+b); msg == nil {
		t.Fatal("expected call_incoming published to b")
	}

	r2 := cs.POST(t, "/v1/call/device/info", jwtB, map[string]any{"device_id": a, "room_id": roomID, "purpose": "call"})
	if r2.Code != 200 {
		t.Fatalf("call/device/info: want code=200, got %d msg=%s", r2.Code, r2.Msg)
	}
	if dataString(t, r2, "token") == "" {
		t.Fatal("call/device/info: empty token")
	}
	if got := dataString(t, r2, "device_id"); got != a {
		t.Fatalf("call/device/info: device_id = %q, want %q", got, a)
	}
	if cs.roomIDForLock(t, a) != roomID || cs.roomIDForLock(t, b) != roomID {
		t.Fatal("expected both caller and answerer locked to room")
	}

	r3 := cs.POST(t, "/v1/call/hangup", jwtA, map[string]any{"room_id": roomID})
	if r3.Code != 200 {
		t.Fatalf("call/hangup: want code=200, got %d msg=%s", r3.Code, r3.Msg)
	}
	if cs.roomIDForLock(t, a) != "" || cs.roomIDForLock(t, b) != "" {
		t.Fatal("expected both locks released after leave")
	}
	if msg := cs.broker.findPublished("room_cancel", "sn_"+b); msg == nil || msg.Msg["reason"] != "hangup" {
		t.Fatalf("expected room_cancel{reason:hangup} to b, got %+v", msg)
	}
}

func TestCall_GroupCallOnlyOneAnswers(t *testing.T) {
	cs := newCallSuite(t)
	a, b1, b2 := uniqueDeviceID(t), uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, a, 2001)
	cs.seedDevice(t, b1, 2002)
	cs.seedDevice(t, b2, 2003)
	cs.seedContact(t, a, b1, 2001, 2002, 1)
	cs.seedContact(t, a, b2, 2001, 2003, 1)
	cs.broker.SetOnline(b1)
	cs.broker.SetOnline(b2)

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, a)
	jwtB1 := deviceJWT(t, cs.cfg.JWTSecret, b1)
	jwtB2 := deviceJWT(t, cs.cfg.JWTSecret, b2)

	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{b1, b2}, "call_type": "video"})
	if r.Code != 200 {
		t.Fatalf("call/request: %d %s", r.Code, r.Msg)
	}
	roomID := dataString(t, r, "room_id")

	r1 := cs.POST(t, "/v1/call/device/info", jwtB1, map[string]any{"device_id": a, "room_id": roomID, "purpose": "call"})
	if r1.Code != 200 {
		t.Fatalf("b1 answer: want 0, got %d %s", r1.Code, r1.Msg)
	}
	r2 := cs.POST(t, "/v1/call/device/info", jwtB2, map[string]any{"device_id": a, "room_id": roomID, "purpose": "call"})
	if r2.Code != 40210 {
		t.Fatalf("b2 answer: want 40210 already answered, got %d %s", r2.Code, r2.Msg)
	}
	if msg := cs.broker.findPublished("room_cancel", "sn_"+b2); msg == nil || msg.Msg["reason"] != "accepted_by_other" {
		t.Fatalf("expected room_cancel{accepted_by_other} to b2, got %+v", msg)
	}
}

func TestCall_AllOffline(t *testing.T) {
	cs := newCallSuite(t)
	a, b := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, a, 3001)
	cs.seedDevice(t, b, 3002)
	cs.seedContact(t, a, b, 3001, 3002, 1)
	// b left offline on purpose

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, a)
	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{b}, "call_type": "video"})
	if r.Code != 40201 {
		t.Fatalf("want 40201 all offline, got %d %s", r.Code, r.Msg)
	}
	if cs.roomIDForLock(t, a) != "" {
		t.Fatal("expected no room lock created when all targets offline")
	}
}

func TestCall_OfflineTargetCountsAsRejected(t *testing.T) {
	cs := newCallSuite(t)
	a, b, c := uniqueDeviceID(t), uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, a, 4001)
	cs.seedDevice(t, b, 4002)
	cs.seedDevice(t, c, 4003)
	cs.seedContact(t, a, b, 4001, 4002, 1)
	cs.seedContact(t, a, c, 4001, 4003, 1)
	cs.broker.SetOnline(b)
	// c left offline — should be pre-counted as rejected

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, a)
	jwtB := deviceJWT(t, cs.cfg.JWTSecret, b)

	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{b, c}, "call_type": "audio"})
	if r.Code != 200 {
		t.Fatalf("call/request: %d %s", r.Code, r.Msg)
	}
	roomID := dataString(t, r, "room_id")

	r2 := cs.POST(t, "/v1/call/reject", jwtB, map[string]any{"room_id": roomID, "reason": "busy"})
	if r2.Code != 200 {
		t.Fatalf("call/reject: %d %s", r2.Code, r2.Msg)
	}
	if msg := cs.broker.findPublished("room_cancel", "sn_"+a); msg == nil || msg.Msg["reason"] != "all_rejected" {
		t.Fatalf("expected room_cancel{all_rejected} to a (since c was pre-rejected offline), got %+v", msg)
	}
	if cs.roomIDForLock(t, a) != "" {
		t.Fatal("expected room fully released (caller lock gone) after all_rejected")
	}
}

func TestCall_DuplicateRequestReturnsBusyWithExistingRoom(t *testing.T) {
	cs := newCallSuite(t)
	a, b, c := uniqueDeviceID(t), uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, a, 5001)
	cs.seedDevice(t, b, 5002)
	cs.seedDevice(t, c, 5003)
	cs.seedContact(t, a, b, 5001, 5002, 1)
	cs.seedContact(t, a, c, 5001, 5003, 1)
	cs.broker.SetOnline(b)
	cs.broker.SetOnline(c)

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, a)
	r1 := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{b}, "call_type": "video"})
	if r1.Code != 200 {
		t.Fatalf("first request: %d %s", r1.Code, r1.Msg)
	}
	roomID := dataString(t, r1, "room_id")

	r2 := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{c}, "call_type": "video"})
	if r2.Code != 40202 {
		t.Fatalf("second request: want 40202 busy, got %d %s", r2.Code, r2.Msg)
	}
	if got := dataString(t, r2, "room_id"); got != roomID {
		t.Fatalf("second request room_id = %q, want existing %q", got, roomID)
	}
}

func TestCall_AnsweringNewCallReleasesOldRoom(t *testing.T) {
	cs := newCallSuite(t)
	a, b, c := uniqueDeviceID(t), uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, a, 6001)
	cs.seedDevice(t, b, 6002)
	cs.seedDevice(t, c, 6003)
	cs.seedContact(t, a, b, 6001, 6002, 1)
	cs.seedContact(t, a, c, 6001, 6003, 1)
	cs.broker.SetOnline(a)
	cs.broker.SetOnline(b)

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, a)
	jwtB := deviceJWT(t, cs.cfg.JWTSecret, b)
	jwtC := deviceJWT(t, cs.cfg.JWTSecret, c)

	// A calls B, B answers -> room1 (A=caller, B=answered_by)
	r1 := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{b}, "call_type": "video"})
	room1 := dataString(t, r1, "room_id")
	ans := cs.POST(t, "/v1/call/device/info", jwtB, map[string]any{"device_id": a, "room_id": room1, "purpose": "call"})
	if ans.Code != 200 {
		t.Fatalf("b answers room1: %d %s", ans.Code, ans.Msg)
	}

	// C calls A -> room2 (A is a target)
	r2 := cs.POST(t, "/v1/call/request", jwtC, map[string]any{"targets": []string{a}, "call_type": "video"})
	if r2.Code != 200 {
		t.Fatalf("c calls a: %d %s", r2.Code, r2.Msg)
	}
	room2 := dataString(t, r2, "room_id")

	// A answers C's call while still locked to room1 -> room1 must auto-release.
	ans2 := cs.POST(t, "/v1/call/device/info", jwtA, map[string]any{"device_id": c, "room_id": room2, "purpose": "call"})
	if ans2.Code != 200 {
		t.Fatalf("a answers room2: %d %s", ans2.Code, ans2.Msg)
	}

	if msg := cs.broker.findPublished("room_cancel", "sn_"+b); msg == nil || msg.Msg["reason"] != "caller_left" {
		t.Fatalf("expected room_cancel{caller_left} to b, got %+v", msg)
	}
	if cs.roomIDForLock(t, b) != "" {
		t.Fatal("expected b's lock released when room1 auto-released")
	}
	if cs.roomIDForLock(t, a) != room2 {
		t.Fatalf("expected a locked to room2, got %q", cs.roomIDForLock(t, a))
	}
}

func TestContacts_RequestRespondAndList(t *testing.T) {
	cs := newCallSuite(t)
	d1, d2 := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, d1, 7001)
	cs.seedDevice(t, d2, 7002)

	jwt1 := deviceJWT(t, cs.cfg.JWTSecret, d1)
	jwt2 := deviceJWT(t, cs.cfg.JWTSecret, d2)

	r := cs.POST(t, "/v1/call/device/contacts/request", jwt1, map[string]any{"target_device_id": d2})
	if r.Code != 200 {
		t.Fatalf("request: %d %s", r.Code, r.Msg)
	}
	if got := dataString(t, r, "status"); got != "pending" {
		t.Fatalf("request status = %q, want pending", got)
	}
	if msg := cs.broker.findPublished("callers_update", "sn_"+d2); msg == nil {
		t.Fatal("request target did not receive callers_update")
	} else if msg.Msg["action"] != "request" || msg.Msg["contact_type"] != "device" || msg.Msg["peer_id"] != d1 {
		t.Fatalf("request callers_update payload=%+v", msg.Msg)
	}
	pending := cs.GET(t, "/v1/call/device/contacts/pending", jwt2)
	if pending.Code != 200 {
		t.Fatalf("pending: %d %s", pending.Code, pending.Msg)
	}
	var pendingData struct {
		Pending []struct {
			PeerDeviceID string `json:"peer_device_id"`
			Type         string `json:"type"`
		} `json:"pending"`
	}
	if err := json.Unmarshal(pending.Data, &pendingData); err != nil || len(pendingData.Pending) != 1 || pendingData.Pending[0].PeerDeviceID != d1 || pendingData.Pending[0].Type != "device" {
		t.Fatalf("pending payload=%s err=%v", string(pending.Data), err)
	}

	r2 := cs.POST(t, "/v1/call/device/contacts/respond", jwt2, map[string]any{"peer_device_id": d1, "action": "accept"})
	if r2.Code != 200 {
		t.Fatalf("respond: %d %s", r2.Code, r2.Msg)
	}
	if msg := cs.broker.findPublished("callers_update", "sn_"+d1); msg == nil {
		t.Fatal("request initiator did not receive callers_update after response")
	} else if msg.Msg["action"] != "accept" || msg.Msg["contact_type"] != "device" || msg.Msg["peer_id"] != d2 {
		t.Fatalf("accept callers_update payload=%+v", msg.Msg)
	}

	list := cs.GET(t, "/v1/call/device/contacts", jwt1)
	if list.Code != 200 {
		t.Fatalf("list: %d %s", list.Code, list.Msg)
	}
	var parsed struct {
		Contacts []struct {
			DeviceID string `json:"device_id"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(list.Data, &parsed); err != nil {
		t.Fatalf("unmarshal contacts: %v", err)
	}
	found := false
	for _, c := range parsed.Contacts {
		if c.DeviceID == d2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected %s in d1's contact list, got %+v", d2, parsed.Contacts)
	}
}

func TestContacts_AcceptEnforcesLimitForBothDevices(t *testing.T) {
	for _, fullSide := range []string{"initiator", "responder"} {
		t.Run(fullSide, func(t *testing.T) {
			cs := newCallSuite(t)
			cs.cfg.Service.MaxContactsPerDevice = 1
			initiator, responder, existingPeer := uniqueDeviceID(t), uniqueDeviceID(t), uniqueDeviceID(t)
			cs.seedDevice(t, initiator, 7051)
			cs.seedDevice(t, responder, 7052)
			cs.seedDevice(t, existingPeer, 7053)

			request := cs.POST(t, "/v1/call/device/contacts/request",
				deviceJWT(t, cs.cfg.JWTSecret, initiator),
				map[string]any{"target_device_id": responder})
			if request.Code != 200 {
				t.Fatalf("request: %d %s", request.Code, request.Msg)
			}
			if fullSide == "initiator" {
				cs.seedContact(t, initiator, existingPeer, 7051, 7053, 1)
			} else {
				cs.seedContact(t, responder, existingPeer, 7052, 7053, 1)
			}

			accept := cs.POST(t, "/v1/call/device/contacts/respond",
				deviceJWT(t, cs.cfg.JWTSecret, responder),
				map[string]any{"peer_device_id": initiator, "action": "accept"})
			if accept.Code != apiresp.ErrContactMax {
				t.Fatalf("accept with full %s: code=%d, want %d", fullSide, accept.Code, apiresp.ErrContactMax)
			}
			var status int
			if err := cs.sqlDB.Get(&status,
				`SELECT status FROM call_contact WHERE device_id_a=? AND device_id_b=?`,
				minTestStr(initiator, responder), maxTestStr(initiator, responder)); err != nil {
				t.Fatal(err)
			}
			if status != 0 {
				t.Fatalf("failed accept changed pending status to %d", status)
			}
		})
	}
}

func TestContacts_ConcurrentAcceptCannotExceedLimit(t *testing.T) {
	cs := newCallSuite(t)
	cs.cfg.Service.MaxContactsPerDevice = 1
	responder, initiatorA, initiatorB := uniqueDeviceID(t), uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, responder, 7061)
	cs.seedDevice(t, initiatorA, 7062)
	cs.seedDevice(t, initiatorB, 7063)

	for _, initiator := range []string{initiatorA, initiatorB} {
		request := cs.POST(t, "/v1/call/device/contacts/request",
			deviceJWT(t, cs.cfg.JWTSecret, initiator),
			map[string]any{"target_device_id": responder})
		if request.Code != 200 {
			t.Fatalf("request from %s: %d %s", initiator, request.Code, request.Msg)
		}
	}

	type result struct {
		code int
		msg  string
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, initiator := range []string{initiatorA, initiatorB} {
		wg.Add(1)
		go func(peer string) {
			defer wg.Done()
			response := cs.POST(t, "/v1/call/device/contacts/respond",
				deviceJWT(t, cs.cfg.JWTSecret, responder),
				map[string]any{"peer_device_id": peer, "action": "accept"})
			results <- result{code: response.Code, msg: response.Msg}
		}(initiator)
	}
	wg.Wait()
	close(results)

	successes, limited := 0, 0
	for result := range results {
		switch result.code {
		case 200:
			successes++
		case apiresp.ErrContactMax:
			limited++
		default:
			t.Fatalf("unexpected accept result: %d %s", result.code, result.msg)
		}
	}
	if successes != 1 || limited != 1 {
		t.Fatalf("accept results: successes=%d limited=%d", successes, limited)
	}
	var accepted int
	if err := cs.sqlDB.Get(&accepted,
		`SELECT COUNT(*) FROM call_contact
		 WHERE (device_id_a=? OR device_id_b=?) AND status=1`,
		responder, responder); err != nil {
		t.Fatal(err)
	}
	if accepted != 1 {
		t.Fatalf("responder accepted contacts=%d, want 1", accepted)
	}
}

func TestContacts_DeviceDeleteAcceptedAndNotifyPeer(t *testing.T) {
	cs := newCallSuite(t)
	d1, d2 := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, d1, 7101)
	cs.seedDevice(t, d2, 7102)
	cs.seedContact(t, d1, d2, 7101, 7102, 1)

	r := cs.DELETE(t, "/v1/call/device/contacts?peer_id="+d2, deviceJWT(t, cs.cfg.JWTSecret, d1))
	if r.Code != 200 {
		t.Fatalf("delete: %d %s", r.Code, r.Msg)
	}
	var status int
	if err := cs.sqlDB.Get(&status,
		`SELECT status FROM call_contact WHERE device_id_a=? AND device_id_b=?`, minTestStr(d1, d2), maxTestStr(d1, d2)); err != nil {
		t.Fatal(err)
	}
	if status != 3 {
		t.Fatalf("contact status=%d, want deleted(3)", status)
	}
	if msg := cs.broker.findPublished("callers_update", "sn_"+d2); msg == nil {
		t.Fatal("peer did not receive callers_update")
	}

	again := cs.DELETE(t, "/v1/call/device/contacts?peer_id="+d2, deviceJWT(t, cs.cfg.JWTSecret, d1))
	if again.Code != apiresp.ErrContactNotExist {
		t.Fatalf("second delete code=%d, want %d", again.Code, apiresp.ErrContactNotExist)
	}
}

func TestContacts_DeviceDeleteRejectsNonAcceptedStates(t *testing.T) {
	for _, status := range []int{0, 2, 3} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			cs := newCallSuite(t)
			d1, d2 := uniqueDeviceID(t), uniqueDeviceID(t)
			cs.seedDevice(t, d1, 7201+int64(status)*10)
			cs.seedDevice(t, d2, 7202+int64(status)*10)
			cs.seedContact(t, d1, d2, 7201, 7202, status)

			r := cs.DELETE(t, "/v1/call/device/contacts?peer_id="+d2, deviceJWT(t, cs.cfg.JWTSecret, d1))
			if r.Code != apiresp.ErrContactNotExist {
				t.Fatalf("delete status=%d returned code=%d, want %d", status, r.Code, apiresp.ErrContactNotExist)
			}
			var got int
			if err := cs.sqlDB.Get(&got,
				`SELECT status FROM call_contact WHERE device_id_a=? AND device_id_b=?`, minTestStr(d1, d2), maxTestStr(d1, d2)); err != nil {
				t.Fatal(err)
			}
			if got != status {
				t.Fatalf("status changed from %d to %d", status, got)
			}
		})
	}
}

func TestContacts_SameAccountAutoContactCannotBeDeleted(t *testing.T) {
	cs := newCallSuite(t)
	d1, d2 := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, d1, 7301)
	cs.seedDevice(t, d2, 7301)
	jwt1 := deviceJWT(t, cs.cfg.JWTSecret, d1)

	// The first list lazily materializes the same-account auto contact.
	if r := cs.GET(t, "/v1/call/device/contacts", jwt1); r.Code != 200 {
		t.Fatalf("initial list: %d %s", r.Code, r.Msg)
	}
	if r := cs.DELETE(t, "/v1/call/device/contacts?peer_id="+d2, jwt1); r.Code != apiresp.ErrContactProtected {
		t.Fatalf("delete auto contact: code=%d, want %d", r.Code, apiresp.ErrContactProtected)
	}
	var status int
	if err := cs.sqlDB.Get(&status,
		`SELECT status FROM call_contact WHERE device_id_a=? AND device_id_b=?`, minTestStr(d1, d2), maxTestStr(d1, d2)); err != nil {
		t.Fatal(err)
	}
	if status != 1 {
		t.Fatalf("protected auto contact status=%d, want accepted(1)", status)
	}
	if msg := cs.broker.findPublished("callers_update", "sn_"+d2); msg != nil {
		t.Fatal("rejected deletion should not publish callers_update")
	}
}

func TestContacts_SameAccountRequestRepairsLegacyDeletedAuto(t *testing.T) {
	cs := newCallSuite(t)
	d1, d2 := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, d1, 7311)
	cs.seedDevice(t, d2, 7311)
	cs.seedContact(t, d1, d2, 7311, 7311, 3) // row left by a pre-protection server

	readd := cs.POST(t, "/v1/call/device/contacts/request", deviceJWT(t, cs.cfg.JWTSecret, d1), map[string]any{"target_device_id": d2})
	if readd.Code != 200 || dataString(t, readd, "status") != "accepted" {
		t.Fatalf("repair: %d %s data=%s", readd.Code, readd.Msg, string(readd.Data))
	}
	var row struct {
		Status int    `db:"status"`
		Source string `db:"source"`
	}
	if err := cs.sqlDB.Get(&row,
		`SELECT status, source FROM call_contact WHERE device_id_a=? AND device_id_b=?`, minTestStr(d1, d2), maxTestStr(d1, d2)); err != nil {
		t.Fatal(err)
	}
	if row.Status != 1 || row.Source != "auto" {
		t.Fatalf("repaired contact=%+v, want status=1 source=auto", row)
	}
}

func TestContacts_SameAccountAutoLinked(t *testing.T) {
	cs := newCallSuite(t)
	d1, d2 := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, d1, 8001)
	cs.seedDevice(t, d2, 8001) // same account

	jwt1 := deviceJWT(t, cs.cfg.JWTSecret, d1)
	list := cs.GET(t, "/v1/call/device/contacts", jwt1)
	if list.Code != 200 {
		t.Fatalf("list: %d %s", list.Code, list.Msg)
	}
	var parsed struct {
		Contacts []struct {
			DeviceID string `json:"device_id"`
			Source   string `json:"source"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(list.Data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, c := range parsed.Contacts {
		if c.DeviceID == d2 && c.Source == "auto" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected %s auto-linked in d1's contacts, got %+v", d2, parsed.Contacts)
	}
}

func TestContacts_InternalUnbindHardDeletesAndRebindRestoresAuto(t *testing.T) {
	cs := newCallSuite(t)
	unbound, sibling := uniqueDeviceID(t), uniqueDeviceID(t)
	pendingPeer, rejectedPeer, deletedPeer := uniqueDeviceID(t), uniqueDeviceID(t), uniqueDeviceID(t)
	unrelatedA, unrelatedB := uniqueDeviceID(t), uniqueDeviceID(t)

	cs.seedDevice(t, unbound, 8101)
	cs.seedDevice(t, sibling, 8101)
	cs.seedDevice(t, pendingPeer, 8102)
	cs.seedDevice(t, rejectedPeer, 8103)
	cs.seedDevice(t, deletedPeer, 8104)
	cs.seedDevice(t, unrelatedA, 8105)
	cs.seedDevice(t, unrelatedB, 8106)

	// Materialize the accepted auto contact, then add every other contact state.
	if r := cs.GET(t, "/v1/call/device/contacts", deviceJWT(t, cs.cfg.JWTSecret, unbound)); r.Code != 200 {
		t.Fatalf("materialize auto contact: %d %s", r.Code, r.Msg)
	}
	cs.seedContact(t, unbound, pendingPeer, 8101, 8102, 0)
	cs.seedContact(t, unbound, rejectedPeer, 8101, 8103, 2)
	cs.seedContact(t, unbound, deletedPeer, 8101, 8104, 3)
	cs.seedContact(t, unrelatedA, unrelatedB, 8105, 8106, 1)

	// user-server clears the binding before its outbox calls this endpoint.
	if _, err := cs.sqlDB.Exec(`UPDATE device_bind SET user_id=0 WHERE device_id=?`, unbound); err != nil {
		t.Fatal(err)
	}
	r := cs.POSTInternal(t, "/v1/call/internal/unbind", map[string]any{"device_id": unbound})
	if r.Code != 200 {
		t.Fatalf("internal unbind: %d %s", r.Code, r.Msg)
	}

	var touching int
	if err := cs.sqlDB.Get(&touching,
		`SELECT COUNT(*) FROM call_contact WHERE device_id_a=? OR device_id_b=?`, unbound, unbound); err != nil {
		t.Fatal(err)
	}
	if touching != 0 {
		t.Fatalf("contacts touching unbound device=%d, want 0", touching)
	}
	var unrelated int
	if err := cs.sqlDB.Get(&unrelated,
		`SELECT COUNT(*) FROM call_contact WHERE device_id_a=? AND device_id_b=?`,
		minTestStr(unrelatedA, unrelatedB), maxTestStr(unrelatedA, unrelatedB)); err != nil {
		t.Fatal(err)
	}
	if unrelated != 1 {
		t.Fatalf("unrelated contact count=%d, want 1", unrelated)
	}
	for _, peer := range []string{sibling, pendingPeer, rejectedPeer} {
		if msg := cs.broker.findPublished("callers_update", "sn_"+peer); msg == nil {
			t.Fatalf("expected callers_update for affected peer %s", peer)
		}
	}
	if msg := cs.broker.findPublished("callers_update", "sn_"+deletedPeer); msg != nil {
		t.Fatal("already-deleted peer should not receive callers_update")
	}

	// Rebinding to the original account can now recreate the same-account auto contact.
	if _, err := cs.sqlDB.Exec(`UPDATE device_bind SET user_id=? WHERE device_id=?`, 8101, unbound); err != nil {
		t.Fatal(err)
	}
	if r := cs.GET(t, "/v1/call/device/contacts", deviceJWT(t, cs.cfg.JWTSecret, unbound)); r.Code != 200 {
		t.Fatalf("list after rebind: %d %s", r.Code, r.Msg)
	}
	var restored struct {
		Status int    `db:"status"`
		Source string `db:"source"`
	}
	if err := cs.sqlDB.Get(&restored,
		`SELECT status, source FROM call_contact WHERE device_id_a=? AND device_id_b=?`,
		minTestStr(unbound, sibling), maxTestStr(unbound, sibling)); err != nil {
		t.Fatal(err)
	}
	if restored.Status != 1 || restored.Source != "auto" {
		t.Fatalf("restored contact=%+v, want accepted auto", restored)
	}
}

func TestCallHangupRouteRenamed(t *testing.T) {
	// /v1/call/leave should return 404, /v1/call/hangup should return 200 or 4xx (not 404)
	cs := newCallSuite(t)
	a := uniqueDeviceID(t)
	cs.seedDevice(t, a, 10001)
	token := deviceJWT(t, cs.cfg.JWTSecret, a)
	client := &http.Client{}

	req, _ := http.NewRequest("POST", cs.callSrv.URL+"/v1/call/leave",
		strings.NewReader(`{"room_id":"nonexistent"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/call/leave: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("/v1/call/leave should be gone (want 404), got %d", resp.StatusCode)
	}
	resp.Body.Close()

	req2, _ := http.NewRequest("POST", cs.callSrv.URL+"/v1/call/hangup",
		strings.NewReader(`{"room_id":"nonexistent"}`))
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("POST /v1/call/hangup: %v", err)
	}
	if resp2.StatusCode == http.StatusNotFound {
		t.Errorf("/v1/call/hangup must exist (got 404)")
	}
	resp2.Body.Close()
}

func TestGetDeviceRoomNotInAny(t *testing.T) {
	cs := newCallSuite(t)
	a := uniqueDeviceID(t)
	cs.seedDevice(t, a, 10002)
	token := deviceJWT(t, cs.cfg.JWTSecret, a)
	r := cs.GET(t, "/v1/call/room", token)
	if r.Code != 200 {
		t.Fatalf("GET /v1/call/room: want code=200, got %d msg=%s", r.Code, r.Msg)
	}
	if len(r.Data) != 0 && string(r.Data) != "null" {
		t.Errorf("GET /v1/call/room: want null data, got %s", r.Data)
	}
}

func TestContacts_UserListByDevice(t *testing.T) {
	cs := newCallSuite(t)
	d1, d2 := uniqueDeviceID(t), uniqueDeviceID(t)
	const userID = int64(9001)
	cs.seedDevice(t, d1, userID)
	cs.seedDevice(t, d2, 9002)
	cs.seedContact(t, d1, d2, userID, 9002, 1)

	uJWT := userJWT(t, cs.cfg.JWTSecret, userID)

	// Wrong device (not owned by this user) → business forbidden.
	other := uniqueDeviceID(t)
	cs.seedDevice(t, other, 9999)
	denied := cs.GET(t, "/v1/call/user/contacts?device_id="+other, uJWT)
	if denied.Code != apiresp.ErrForbidden {
		t.Fatalf("want ErrForbidden for non-owned device, got %d %s", denied.Code, denied.Msg)
	}

	list := cs.GET(t, "/v1/call/user/contacts?device_id="+d1, uJWT)
	if list.Code != 200 {
		t.Fatalf("list: %d %s", list.Code, list.Msg)
	}
	var parsed struct {
		Contacts []struct {
			ID       int64  `json:"id"`
			DeviceID string `json:"device_id"`
			Source   string `json:"source"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(list.Data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, c := range parsed.Contacts {
		if c.DeviceID == d2 && c.ID > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected %s in d1's contact list via user endpoint, got %+v", d2, parsed.Contacts)
	}
}

func TestContacts_UserRequestRejectsSelf(t *testing.T) {
	cs := newCallSuite(t)
	d := uniqueDeviceID(t)
	const userID = int64(9101)
	cs.seedDevice(t, d, userID)

	r := cs.POST(t, "/v1/call/user/contacts/request", userJWT(t, cs.cfg.JWTSecret, userID), map[string]any{
		"device_id": d, "target_device_id": d,
	})
	if r.Code != apiresp.ErrBadParam {
		t.Fatalf("self request code=%d, want %d", r.Code, apiresp.ErrBadParam)
	}
	var count int
	if err := cs.sqlDB.Get(&count, `SELECT COUNT(*) FROM call_contact WHERE device_id_a=? AND device_id_b=?`, d, d); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("self request created %d rows", count)
	}
}

func TestContacts_UserRespondRequiresResponderOwnership(t *testing.T) {
	cs := newCallSuite(t)
	d1, d2 := uniqueDeviceID(t), uniqueDeviceID(t)
	const user1, user2 = int64(9201), int64(9202)
	cs.seedDevice(t, d1, user1)
	cs.seedDevice(t, d2, user2)
	jwt1 := userJWT(t, cs.cfg.JWTSecret, user1)
	jwt2 := userJWT(t, cs.cfg.JWTSecret, user2)

	request := cs.POST(t, "/v1/call/user/contacts/request", jwt1, map[string]any{
		"device_id": d1, "target_device_id": d2,
	})
	if request.Code != 200 {
		t.Fatalf("request: %d %s", request.Code, request.Msg)
	}
	if msg := cs.broker.findPublished("callers_update", "sn_"+d2); msg == nil {
		t.Fatal("H5 request target did not receive callers_update")
	} else if msg.Msg["action"] != "request" || msg.Msg["contact_type"] != "device" || msg.Msg["peer_id"] != d1 {
		t.Fatalf("H5 request callers_update payload=%+v", msg.Msg)
	}

	pending := cs.GET(t, "/v1/call/user/contacts/pending", jwt2)
	if pending.Code != 200 {
		t.Fatalf("pending: %d %s", pending.Code, pending.Msg)
	}
	var payload struct {
		Pending []struct {
			ID              int64  `json:"id"`
			Type            string `json:"type"`
			InitiatorDevice string `json:"initiator_device"`
			TargetDevice    string `json:"target_device"`
		} `json:"pending"`
	}
	if err := json.Unmarshal(pending.Data, &payload); err != nil || len(payload.Pending) != 1 || payload.Pending[0].ID == 0 || payload.Pending[0].Type != "device" || payload.Pending[0].InitiatorDevice != d1 || payload.Pending[0].TargetDevice != d2 {
		t.Fatalf("pending payload=%s err=%v", string(pending.Data), err)
	}
	contactID := payload.Pending[0].ID

	forged := cs.POST(t, "/v1/call/user/contacts/respond", jwt1, map[string]any{
		"id": contactID, "action": "accept",
	})
	if forged.Code != apiresp.ErrForbidden {
		t.Fatalf("initiator approved own request: code=%d", forged.Code)
	}

	accepted := cs.POST(t, "/v1/call/user/contacts/respond", jwt2, map[string]any{
		"id": contactID, "action": "accept",
	})
	if accepted.Code != 200 {
		t.Fatalf("responder accept: %d %s", accepted.Code, accepted.Msg)
	}
	if cs.broker.findPublished("callers_update", "sn_"+d1) == nil || cs.broker.findPublished("callers_update", "sn_"+d2) == nil {
		t.Fatal("H5 response did not notify both devices")
	}
}

func TestContacts_UserSameAccountRequestRepairsLegacyDeletedAuto(t *testing.T) {
	cs := newCallSuite(t)
	d1, d2 := uniqueDeviceID(t), uniqueDeviceID(t)
	const userID = int64(9301)
	cs.seedDevice(t, d1, userID)
	cs.seedDevice(t, d2, userID)
	cs.seedContact(t, d1, d2, userID, userID, 3)

	r := cs.POST(t, "/v1/call/user/contacts/request", userJWT(t, cs.cfg.JWTSecret, userID), map[string]any{
		"device_id": d1, "target_device_id": d2,
	})
	if r.Code != 200 || dataString(t, r, "status") != "accepted" {
		t.Fatalf("repair: %d %s data=%s", r.Code, r.Msg, string(r.Data))
	}
	var row struct {
		Status int    `db:"status"`
		Source string `db:"source"`
	}
	if err := cs.sqlDB.Get(&row,
		`SELECT status, source FROM call_contact WHERE device_id_a=? AND device_id_b=?`, minTestStr(d1, d2), maxTestStr(d1, d2)); err != nil {
		t.Fatal(err)
	}
	if row.Status != 1 || row.Source != "auto" {
		t.Fatalf("repaired row=%+v", row)
	}
}

func TestContacts_UserRemarkUsesDeviceAndPeerID(t *testing.T) {
	cs := newCallSuite(t)
	d1 := fmt.Sprintf("V%018d", time.Now().UnixNano()%1_000_000_000_000_000_000)
	d2 := uniqueDeviceID(t)
	d3 := fmt.Sprintf("W%018d", time.Now().UnixNano()%1_000_000_000_000_000_000)
	const user1, user2 = int64(9401), int64(9402)
	cs.seedDevice(t, d1, user1)
	cs.seedDevice(t, d2, user2)
	cs.seedDevice(t, d3, user1)
	cs.seedContact(t, d1, d2, user1, user2, 1)
	openID := "wx-open-" + d1
	wxAppID := "wx-test-" + d1
	t.Cleanup(func() {
		_, _ = cs.sqlDB.Exec(`DELETE FROM voip_user_profile WHERE wx_open_id=? AND wx_app_id=?`, openID, wxAppID)
	})
	if _, err := cs.sqlDB.Exec(`INSERT INTO voip_device_auth (device_id, wx_open_id, wx_app_id) VALUES (?, ?, ?)`, d1, openID, wxAppID); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.sqlDB.Exec(`INSERT INTO voip_device_auth (device_id, wx_open_id, wx_app_id) VALUES (?, ?, ?)`, d3, openID, wxAppID); err != nil {
		t.Fatal(err)
	}
	var deviceContactID int64
	if err := cs.sqlDB.Get(&deviceContactID,
		`SELECT id FROM call_contact WHERE device_id_a=? AND device_id_b=?`, minTestStr(d1, d2), maxTestStr(d1, d2)); err != nil {
		t.Fatal(err)
	}
	jwt := userJWT(t, cs.cfg.JWTSecret, user1)
	denied := cs.PUT(t, "/v1/call/user/contacts/remark", jwt, map[string]any{
		"device_id": d2, "peer_id": d1, "remark": "越权备注",
	})
	if denied.Code != apiresp.ErrForbidden {
		t.Fatalf("remark for non-owned device: code=%d, want %d", denied.Code, apiresp.ErrForbidden)
	}

	voip := cs.PUT(t, "/v1/call/user/contacts/remark", jwt, map[string]any{
		"device_id": d1, "peer_id": openID, "remark": "微信联系人",
	})
	if voip.Code != 200 {
		t.Fatalf("voip remark: %d %s", voip.Code, voip.Msg)
	}
	voipAgain := cs.PUT(t, "/v1/call/user/contacts/remark", jwt, map[string]any{
		"device_id": d1, "peer_id": openID, "remark": "微信联系人",
	})
	if voipAgain.Code != 200 {
		t.Fatalf("idempotent voip remark: %d %s", voipAgain.Code, voipAgain.Msg)
	}
	var voipRemarks []string
	if err := cs.sqlDB.Select(&voipRemarks,
		`SELECT remark FROM voip_device_auth WHERE device_id IN (?, ?) AND wx_open_id=? ORDER BY device_id`,
		d1, d3, openID); err != nil || len(voipRemarks) != 2 ||
		voipRemarks[0] != "微信联系人" || voipRemarks[1] != "微信联系人" {
		t.Fatalf("global voip remarks=%v err=%v", voipRemarks, err)
	}
	var profileRemark string
	if err := cs.sqlDB.Get(&profileRemark,
		`SELECT remark FROM voip_user_profile WHERE wx_open_id=? AND wx_app_id=?`,
		openID, wxAppID); err != nil || profileRemark != "微信联系人" {
		t.Fatalf("profile remark=%q err=%v", profileRemark, err)
	}
	if cs.broker.findPublished("callers_update", "sn_"+d1) == nil ||
		cs.broker.findPublished("callers_update", "sn_"+d3) == nil {
		t.Fatal("global H5 VoIP remark did not notify all authorized devices")
	}

	deviceVoip := cs.PUT(t, "/v1/call/device/contacts/remark",
		deviceJWT(t, cs.cfg.JWTSecret, d1), map[string]any{
			"peer_id": openID, "remark": "设备端最后写入",
		})
	if deviceVoip.Code != 200 {
		t.Fatalf("device VoIP remark: %d %s", deviceVoip.Code, deviceVoip.Msg)
	}
	voipRemarks = nil
	if err := cs.sqlDB.Select(&voipRemarks,
		`SELECT remark FROM voip_device_auth WHERE device_id IN (?, ?) AND wx_open_id=? ORDER BY device_id`,
		d1, d3, openID); err != nil || len(voipRemarks) != 2 ||
		voipRemarks[0] != "设备端最后写入" || voipRemarks[1] != "设备端最后写入" {
		t.Fatalf("device global voip remarks=%v err=%v", voipRemarks, err)
	}

	device := cs.PUT(t, "/v1/call/user/contacts/remark", jwt, map[string]any{
		"device_id": d1, "peer_id": d2, "remark": "设备联系人",
	})
	if device.Code != 200 {
		t.Fatalf("device remark: %d %s", device.Code, device.Msg)
	}
	deviceAgain := cs.PUT(t, "/v1/call/user/contacts/remark", jwt, map[string]any{
		"device_id": d1, "peer_id": d2, "remark": "设备联系人",
	})
	if deviceAgain.Code != 200 {
		t.Fatalf("idempotent device remark: %d %s", deviceAgain.Code, deviceAgain.Msg)
	}
	tooLong := cs.PUT(t, "/v1/call/user/contacts/remark", jwt, map[string]any{
		"device_id": d1, "peer_id": d2, "remark": strings.Repeat("名", 65),
	})
	if tooLong.Code != apiresp.ErrBadParam {
		t.Fatalf("long remark: code=%d, want %d", tooLong.Code, apiresp.ErrBadParam)
	}
	var remarkA, remarkB string
	if err := cs.sqlDB.QueryRowx(`SELECT remark_a, remark_b FROM call_contact WHERE device_id_a=? AND device_id_b=?`, minTestStr(d1, d2), maxTestStr(d1, d2)).Scan(&remarkA, &remarkB); err != nil {
		t.Fatal(err)
	}
	got := remarkA
	if d1 != minTestStr(d1, d2) {
		got = remarkB
	}
	if got != "设备联系人" {
		t.Fatalf("device remark=%q", got)
	}

	deleted := cs.DELETE(t, fmt.Sprintf("/v1/call/user/contacts/%d", deviceContactID), jwt)
	if deleted.Code != 200 {
		t.Fatalf("H5 delete: %d %s", deleted.Code, deleted.Msg)
	}
	var status int
	if err := cs.sqlDB.Get(&status,
		`SELECT status FROM call_contact WHERE device_id_a=? AND device_id_b=?`, minTestStr(d1, d2), maxTestStr(d1, d2)); err != nil {
		t.Fatal(err)
	}
	if status != 3 {
		t.Fatalf("H5 deleted status=%d, want 3", status)
	}
	if cs.broker.findPublished("callers_update", "sn_"+d1) == nil || cs.broker.findPublished("callers_update", "sn_"+d2) == nil {
		t.Fatal("H5 deletion did not notify both devices")
	}
}

func TestDeviceInfoPublishesCalleeAnswered(t *testing.T) {
	cs := newCallSuite(t)
	caller, callee := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, caller, 11001)
	cs.seedDevice(t, callee, 11002)
	cs.seedContact(t, caller, callee, 11001, 11002, 1)
	cs.broker.SetOnline(callee)

	jwtCaller := deviceJWT(t, cs.cfg.JWTSecret, caller)
	jwtCallee := deviceJWT(t, cs.cfg.JWTSecret, callee)

	// 发起呼叫
	r := cs.POST(t, "/v1/call/request", jwtCaller, map[string]any{"targets": []string{callee}, "call_type": "audio"})
	if r.Code != 200 {
		t.Fatalf("call/request: %d %s", r.Code, r.Msg)
	}
	roomID := dataString(t, r, "room_id")

	// 被叫查到连接信息
	r2 := cs.POST(t, "/v1/call/device/info", jwtCallee, map[string]any{
		"device_id": caller,
		"room_id":   roomID,
		"purpose":   "call",
	})
	if r2.Code != 200 {
		t.Fatalf("call/device/info: want code=200, got %d msg=%s", r2.Code, r2.Msg)
	}
	if dataString(t, r2, "token") == "" {
		t.Fatal("call/device/info: token must be returned")
	}

	// 主叫应收到 callee_answered
	msg := cs.broker.findPublished("callee_answered", "sn_"+caller)
	if msg == nil {
		t.Fatal("expected callee_answered published to caller")
	}
	if msg.Msg["room_id"] != roomID {
		t.Errorf("callee_answered room_id = %v, want %s", msg.Msg["room_id"], roomID)
	}
	if msg.Msg["callee_id"] != callee {
		t.Errorf("callee_answered callee_id = %v, want %s", msg.Msg["callee_id"], callee)
	}
	if msg.QoS != 1 {
		t.Errorf("callee_answered QoS = %d, want 1", msg.QoS)
	}

	// 清理
	cs.POST(t, "/v1/call/hangup", jwtCaller, map[string]any{"room_id": roomID})
}

// TestRtcToken_FlagsInCall verifies user-server's live-preview token endpoint
// still issues a token while a device is mid device-to-device call, but flags
// it via in_call so H5 can warn the user (see project decision: don't block).
func TestRtcToken_FlagsInCall(t *testing.T) {
	s := newSuite(t)
	email := uniqueEmail()
	seedCode(t, s.rdb, email, "123456")
	r := s.usrPOST(t, "/v1/user/register", "", map[string]string{
		"email": email, "password": "pass1234", "code": "123456",
	})
	if r.Code != 200 {
		t.Fatalf("register: %d %s", r.Code, r.Msg)
	}
	tok := dataString(t, r, "token")
	var reg struct {
		UserID int64 `json:"user_id"`
	}
	if err := json.Unmarshal(r.Data, &reg); err != nil {
		t.Fatalf("unmarshal register data: %v", err)
	}

	deviceID := uniqueDeviceID(t)
	if _, err := s.sqlDB.Exec(
		`INSERT INTO device_pool (device_id, device_key, status) VALUES (?, ?, 1)`,
		deviceID, "key-"+deviceID); err != nil {
		t.Fatalf("seed device_pool: %v", err)
	}
	if _, err := s.sqlDB.Exec(
		`INSERT INTO device_bind (device_id, user_id, assign) VALUES (?, ?, 'dynamic')`,
		deviceID, reg.UserID); err != nil {
		t.Fatalf("seed device_bind: %v", err)
	}

	before := s.usrGET(t, "/v1/user/device/rtc-token?device_id="+deviceID, tok)
	if before.Code != 200 {
		t.Fatalf("rtc-token (not in call): %d %s", before.Code, before.Msg)
	}
	var beforeData struct {
		InCall bool `json:"in_call"`
	}
	if err := json.Unmarshal(before.Data, &beforeData); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if beforeData.InCall {
		t.Fatal("expected in_call=false before any room lock exists")
	}

	if err := s.rdb.Set(context.Background(), "room:lock:"+deviceID, "some-room-id", time.Hour).Err(); err != nil {
		t.Fatalf("set room lock: %v", err)
	}

	after := s.usrGET(t, "/v1/user/device/rtc-token?device_id="+deviceID, tok)
	if after.Code != 200 {
		t.Fatalf("rtc-token (in call): %d %s", after.Code, after.Msg)
	}
	var afterData struct {
		InCall bool   `json:"in_call"`
		Token  string `json:"token"`
	}
	if err := json.Unmarshal(after.Data, &afterData); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !afterData.InCall {
		t.Fatal("expected in_call=true once room:lock exists")
	}
	if afterData.Token == "" {
		t.Fatal("expected token to still be issued despite in_call=true")
	}
}

// TestGetDeviceRoomWhileInRoom verifies that GET /v1/call/room returns the
// correct room_id, role, status, and call_type while the caller is locked to a room.
func TestGetDeviceRoomWhileInRoom(t *testing.T) {
	cs := newCallSuite(t)
	caller, callee := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, caller, 12001)
	cs.seedDevice(t, callee, 12002)
	cs.seedContact(t, caller, callee, 12001, 12002, 1)
	cs.broker.SetOnline(callee)

	jwtCaller := deviceJWT(t, cs.cfg.JWTSecret, caller)

	// 发起呼叫，创建 room
	r := cs.POST(t, "/v1/call/request", jwtCaller, map[string]any{"targets": []string{callee}, "call_type": "video"})
	if r.Code != 200 {
		t.Fatalf("call/request: want code=200, got %d msg=%s", r.Code, r.Msg)
	}
	roomID := dataString(t, r, "room_id")
	if roomID == "" {
		t.Fatal("call/request: empty room_id")
	}
	defer cs.POST(t, "/v1/call/hangup", jwtCaller, map[string]any{"room_id": roomID})

	// 查询当前 room
	gr := cs.GET(t, "/v1/call/room", jwtCaller)
	if gr.Code != 200 {
		t.Fatalf("GET /v1/call/room: want code=200, got %d msg=%s", gr.Code, gr.Msg)
	}

	var body struct {
		RoomID   string `json:"room_id"`
		Role     string `json:"role"`
		Status   string `json:"status"`
		CallType string `json:"call_type"`
	}
	if err := json.Unmarshal(gr.Data, &body); err != nil {
		t.Fatalf("unmarshal room data: %v", err)
	}
	if body.RoomID != roomID {
		t.Errorf("room_id = %q, want %q", body.RoomID, roomID)
	}
	if body.Role != "caller" {
		t.Errorf("role = %q, want caller", body.Role)
	}
	if body.Status != "active" {
		t.Errorf("status = %q, want active", body.Status)
	}
	if body.CallType == "" {
		t.Errorf("call_type should not be empty")
	}
}

// TestHangupAnsweredRoomSendsQoS0Cancel verifies that when a room has status=answered
// (callee has called /v1/call/device/info), calling /v1/call/hangup sends a
// room_cancel MQTT message with QoS=0 (in-call reliability mode).
func TestHangupAnsweredRoomSendsQoS0Cancel(t *testing.T) {
	cs := newCallSuite(t)
	caller, callee := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, caller, 13001)
	cs.seedDevice(t, callee, 13002)
	cs.seedContact(t, caller, callee, 13001, 13002, 1)
	cs.broker.SetOnline(callee)

	jwtCaller := deviceJWT(t, cs.cfg.JWTSecret, caller)
	jwtCallee := deviceJWT(t, cs.cfg.JWTSecret, callee)

	// 发起呼叫
	r := cs.POST(t, "/v1/call/request", jwtCaller, map[string]any{"targets": []string{callee}, "call_type": "audio"})
	if r.Code != 200 {
		t.Fatalf("call/request: want code=200, got %d msg=%s", r.Code, r.Msg)
	}
	roomID := dataString(t, r, "room_id")

	// 被叫接听 → room status 变为 answered
	r2 := cs.POST(t, "/v1/call/device/info", jwtCallee, map[string]any{
		"device_id": caller,
		"room_id":   roomID,
		"purpose":   "call",
	})
	if r2.Code != 200 {
		t.Fatalf("call/device/info: want code=200, got %d msg=%s", r2.Code, r2.Msg)
	}

	// 主叫挂断 → releaseRoom 应以 QoS=0 发送 room_cancel（room status=answered）
	r3 := cs.POST(t, "/v1/call/hangup", jwtCaller, map[string]any{"room_id": roomID})
	if r3.Code != 200 {
		t.Fatalf("call/hangup: want code=200, got %d msg=%s", r3.Code, r3.Msg)
	}

	msg := cs.broker.findPublished("room_cancel", "sn_"+callee)
	if msg == nil {
		t.Fatal("expected room_cancel published to callee after hangup")
	}
	if msg.Msg["reason"] != "hangup" {
		t.Errorf("room_cancel reason = %v, want hangup", msg.Msg["reason"])
	}
	if msg.QoS != 0 {
		t.Errorf("room_cancel QoS = %d, want 0 (room was answered)", msg.QoS)
	}
}

// ── 新增测试 ──────────────────────────────────────────────────────────────────

// TestCall_CallerCancel verifies POST /v1/call/cancel before answer.
func TestCall_CallerCancel(t *testing.T) {
	cs := newCallSuite(t)
	caller, callee := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, caller, 14001)
	cs.seedDevice(t, callee, 14002)
	cs.seedContact(t, caller, callee, 14001, 14002, 1)
	cs.broker.SetOnline(callee)

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, caller)

	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{callee}, "call_type": "audio"})
	if r.Code != 200 {
		t.Fatalf("call/request: %d %s", r.Code, r.Msg)
	}
	roomID := dataString(t, r, "room_id")

	// callee does NOT answer — caller cancels
	r2 := cs.POST(t, "/v1/call/cancel", jwtA, map[string]any{"room_id": roomID})
	if r2.Code != 200 {
		t.Fatalf("call/cancel: want code=200, got %d msg=%s", r2.Code, r2.Msg)
	}
	if cs.roomIDForLock(t, caller) != "" {
		t.Fatal("expected caller lock released after cancel")
	}
	msg := cs.broker.findPublished("room_cancel", "sn_"+callee)
	if msg == nil || msg.Msg["reason"] != "cancel" {
		t.Fatalf("expected room_cancel{cancel} to callee, got %+v", msg)
	}
}

// TestCall_CalleeReject verifies that a reject doesn't release the room
// while other targets remain pending.
func TestCall_CalleeReject(t *testing.T) {
	cs := newCallSuite(t)
	caller, c1, c2 := uniqueDeviceID(t), uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, caller, 15001)
	cs.seedDevice(t, c1, 15002)
	cs.seedDevice(t, c2, 15003)
	cs.seedContact(t, caller, c1, 15001, 15002, 1)
	cs.seedContact(t, caller, c2, 15001, 15003, 1)
	cs.broker.SetOnline(c1)
	cs.broker.SetOnline(c2)

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, caller)
	jwtC1 := deviceJWT(t, cs.cfg.JWTSecret, c1)

	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{c1, c2}, "call_type": "video"})
	if r.Code != 200 {
		t.Fatalf("call/request: %d %s", r.Code, r.Msg)
	}
	roomID := dataString(t, r, "room_id")

	// c1 rejects — room should NOT be released (c2 still pending)
	r2 := cs.POST(t, "/v1/call/reject", jwtC1, map[string]any{"room_id": roomID, "reason": "busy"})
	if r2.Code != 200 {
		t.Fatalf("call/reject: %d %s", r2.Code, r2.Msg)
	}
	if msg := cs.broker.findPublished("call_reject", "sn_"+caller); msg == nil {
		t.Fatal("expected call_reject published to caller")
	}
	if cs.roomIDForLock(t, caller) != roomID {
		t.Fatal("room should still be active after partial reject")
	}
}

// TestCall_AllRejectReleasesRoom verifies when all targets reject, room is released.
func TestCall_AllRejectReleasesRoom(t *testing.T) {
	cs := newCallSuite(t)
	caller, c1, c2 := uniqueDeviceID(t), uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, caller, 16001)
	cs.seedDevice(t, c1, 16002)
	cs.seedDevice(t, c2, 16003)
	cs.seedContact(t, caller, c1, 16001, 16002, 1)
	cs.seedContact(t, caller, c2, 16001, 16003, 1)
	cs.broker.SetOnline(c1)
	cs.broker.SetOnline(c2)

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, caller)
	jwtC1 := deviceJWT(t, cs.cfg.JWTSecret, c1)
	jwtC2 := deviceJWT(t, cs.cfg.JWTSecret, c2)

	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{c1, c2}, "call_type": "audio"})
	if r.Code != 200 {
		t.Fatalf("call/request: %d %s", r.Code, r.Msg)
	}
	roomID := dataString(t, r, "room_id")

	cs.POST(t, "/v1/call/reject", jwtC1, map[string]any{"room_id": roomID, "reason": "busy"})
	cs.POST(t, "/v1/call/reject", jwtC2, map[string]any{"room_id": roomID, "reason": "decline"})

	msg := cs.broker.findPublished("room_cancel", "sn_"+caller)
	if msg == nil || msg.Msg["reason"] != "all_rejected" {
		t.Fatalf("expected room_cancel{all_rejected} to caller, got %+v", msg)
	}
	if cs.roomIDForLock(t, caller) != "" {
		t.Fatal("expected room fully released after all rejected")
	}
}

// TestCall_CannotCallSelf verifies calling yourself is rejected.
func TestCall_CannotCallSelf(t *testing.T) {
	cs := newCallSuite(t)
	d := uniqueDeviceID(t)
	cs.seedDevice(t, d, 17001)
	cs.broker.SetOnline(d)

	jwtD := deviceJWT(t, cs.cfg.JWTSecret, d)
	r := cs.POST(t, "/v1/call/request", jwtD, map[string]any{"targets": []string{d}, "call_type": "video"})
	if r.Code != 40000 {
		t.Fatalf("want 40000 cannot call self, got %d msg=%s", r.Code, r.Msg)
	}
}

// TestCall_CannotCallNonContact verifies calling a non-contact is rejected.
func TestCall_CannotCallNonContact(t *testing.T) {
	cs := newCallSuite(t)
	a, b := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, a, 18001)
	cs.seedDevice(t, b, 18002)
	cs.broker.SetOnline(b)

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, a)
	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{b}, "call_type": "video"})
	if r.Code != 40205 {
		t.Fatalf("want 40205 contact not exist, got %d msg=%s", r.Code, r.Msg)
	}
}

// TestCall_HangupByCallee verifies the answered callee can hang up.
func TestCall_HangupByCallee(t *testing.T) {
	cs := newCallSuite(t)
	caller, callee := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, caller, 19001)
	cs.seedDevice(t, callee, 19002)
	cs.seedContact(t, caller, callee, 19001, 19002, 1)
	cs.broker.SetOnline(callee)

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, caller)
	jwtB := deviceJWT(t, cs.cfg.JWTSecret, callee)

	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{callee}, "call_type": "video"})
	if r.Code != 200 {
		t.Fatalf("call/request: %d %s", r.Code, r.Msg)
	}
	roomID := dataString(t, r, "room_id")

	r2 := cs.POST(t, "/v1/call/device/info", jwtB, map[string]any{"device_id": caller, "room_id": roomID, "purpose": "call"})
	if r2.Code != 200 {
		t.Fatalf("call/device/info: %d %s", r2.Code, r2.Msg)
	}

	r3 := cs.POST(t, "/v1/call/hangup", jwtB, map[string]any{"room_id": roomID})
	if r3.Code != 200 {
		t.Fatalf("call/hangup by callee: %d %s", r3.Code, r3.Msg)
	}
	if cs.roomIDForLock(t, caller) != "" || cs.roomIDForLock(t, callee) != "" {
		t.Fatal("expected both locks released after callee hangup")
	}
	msg := cs.broker.findPublished("room_cancel", "sn_"+caller)
	if msg == nil || msg.Msg["reason"] != "hangup" {
		t.Fatalf("expected room_cancel{hangup} to caller, got %+v", msg)
	}
}

// TestCall_InvalidCallType verifies invalid call_type is rejected.
func TestCall_InvalidCallType(t *testing.T) {
	cs := newCallSuite(t)
	a, b := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, a, 20001)
	cs.seedDevice(t, b, 20002)
	cs.seedContact(t, a, b, 20001, 20002, 1)
	cs.broker.SetOnline(b)

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, a)
	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{b}, "call_type": "text"})
	if r.Code != 40000 {
		t.Fatalf("want 40000 invalid call_type, got %d msg=%s", r.Code, r.Msg)
	}
}

// TestCall_EmptyTargets verifies empty targets is rejected.
func TestCall_EmptyTargets(t *testing.T) {
	cs := newCallSuite(t)
	a := uniqueDeviceID(t)
	cs.seedDevice(t, a, 21001)
	jwtA := deviceJWT(t, cs.cfg.JWTSecret, a)

	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{}, "call_type": "video"})
	if r.Code != 40000 {
		t.Fatalf("want 40000 empty targets, got %d msg=%s", r.Code, r.Msg)
	}
}

// TestCall_OnlyCallerCanCancel verifies only the caller can cancel.
func TestCall_OnlyCallerCanCancel(t *testing.T) {
	cs := newCallSuite(t)
	caller, callee := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, caller, 22001)
	cs.seedDevice(t, callee, 22002)
	cs.seedContact(t, caller, callee, 22001, 22002, 1)
	cs.broker.SetOnline(callee)

	jwtA := deviceJWT(t, cs.cfg.JWTSecret, caller)
	jwtB := deviceJWT(t, cs.cfg.JWTSecret, callee)

	r := cs.POST(t, "/v1/call/request", jwtA, map[string]any{"targets": []string{callee}, "call_type": "audio"})
	roomID := dataString(t, r, "room_id")

	r2 := cs.POST(t, "/v1/call/cancel", jwtB, map[string]any{"room_id": roomID})
	if r2.Code != apiresp.ErrForbidden {
		t.Fatalf("want %d forbidden, got %d msg=%s", apiresp.ErrForbidden, r2.Code, r2.Msg)
	}
}

// TestContacts_RejectRequest verifies rejecting a contact request.
func TestContacts_RejectRequest(t *testing.T) {
	cs := newCallSuite(t)
	d1, d2 := uniqueDeviceID(t), uniqueDeviceID(t)
	cs.seedDevice(t, d1, 23001)
	cs.seedDevice(t, d2, 23002)

	jwt1 := deviceJWT(t, cs.cfg.JWTSecret, d1)
	jwt2 := deviceJWT(t, cs.cfg.JWTSecret, d2)

	cs.POST(t, "/v1/call/device/contacts/request", jwt1, map[string]any{"target_device_id": d2})

	r2 := cs.POST(t, "/v1/call/device/contacts/respond", jwt2, map[string]any{"peer_device_id": d1, "action": "reject"})
	if r2.Code != 200 {
		t.Fatalf("respond reject: %d %s", r2.Code, r2.Msg)
	}

	list := cs.GET(t, "/v1/call/device/contacts", jwt1)
	if list.Code != 200 {
		t.Fatalf("list: %d %s", list.Code, list.Msg)
	}
	var parsed struct {
		Contacts []struct {
			DeviceID string `json:"device_id"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(list.Data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, c := range parsed.Contacts {
		if c.DeviceID == d2 {
			t.Fatal("rejected contact should not appear in list")
		}
	}
}
