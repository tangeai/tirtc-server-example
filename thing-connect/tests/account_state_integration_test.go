package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	callhandler "thing-connect/call-server/handler"
	"thing-connect/internal/cache"
	callservice "thing-connect/internal/call"
	"thing-connect/internal/deviceprofile"
	"thing-connect/internal/mqttc"
	mysqlstore "thing-connect/internal/store/mysql"
	"thing-connect/internal/userauth"
	voiphandler "thing-connect/voip-server/handler"
)

// Assemble the same global account-state guard and route authentication used by
// the running services, against real SQL bindings and actual contacts handlers.
func TestAccountStateAcrossCallAndVoipHTTP(t *testing.T) {
	cs := newCallSuite(t)
	result, err := cs.sqlDB.Exec(`INSERT INTO users(email,password) VALUES (?,?)`, fmt.Sprintf("auth-chain-%d@example.test", time.Now().UnixNano()), "unused-test-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	uid, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.sqlDB.Exec(`DELETE FROM users WHERE id=?`, uid) })
	device := uniqueDeviceID(t)
	cs.seedDevice(t, device, uid)
	r := gin.New()
	r.Use(userauth.EnforceState(cs.sqlDB, cs.cfg.JWTSecret))
	calls := callservice.NewService(cache.NewCallRooms(cs.rdb), mqttc.NewCallNotifier(cs.broker))
	callhandler.NewServer(cs.cfg, cs.sqlDB, calls, cs.broker, mysqlstore.NewDeviceStore(cs.sqlDB)).Register(r)
	voiphandler.NewServer(cs.cfg, cs.sqlDB, cs.rdb, cs.broker, deviceprofile.NewService(mysqlstore.NewDeviceProfileStore(cs.sqlDB)), nil, cache.WeChatRefreshGate(cs.rdb)).Register(r)
	server := httptest.NewServer(r)
	defer server.Close()
	token := func(revision int) string {
		claims := jwt.MapClaims{"user_id": uid, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
		if revision > 0 {
			claims["auth_revision"] = revision
		}
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(cs.cfg.JWTSecret))
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	legacy, old, fresh := token(0), token(1), token(2)
	check := func(t *testing.T, signed string, want int) {
		t.Helper()
		for _, service := range []string{"call", "voip"} {
			req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/"+service+"/user/contacts?device_id="+device, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+signed)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Code int `json:"code"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&body)
			response.Body.Close()
			wantCode := want
			if service == "voip" && want == http.StatusOK {
				wantCode = 0
			}
			if decodeErr != nil || response.StatusCode != want || body.Code != wantCode {
				t.Fatalf("%s HTTP=%d code=%d decode=%v want=%d", service, response.StatusCode, body.Code, decodeErr, want)
			}
		}
	}
	t.Run("initial-and-legacy", func(t *testing.T) { check(t, legacy, 200); check(t, old, 200) })
	if _, err := cs.sqlDB.Exec(`UPDATE users SET auth_revision=2 WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}
	t.Run("revision-invalidates-both-old-forms", func(t *testing.T) { check(t, legacy, 401); check(t, old, 401); check(t, fresh, 200) })
	if _, err := cs.sqlDB.Exec(`UPDATE users SET status=0 WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}
	t.Run("disabled-account", func(t *testing.T) { check(t, fresh, 401) })
	if _, err := cs.sqlDB.Exec(`UPDATE users SET status=1,auth_revision=3 WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}
	t.Run("reenabling-does-not-revive-old-token", func(t *testing.T) { check(t, fresh, 401); check(t, token(3), 200) })
}
