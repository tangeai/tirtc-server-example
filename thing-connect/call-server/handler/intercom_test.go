package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	roomapp "thing-connect/internal/room"
)

type intercomFixture struct {
	operation roomapp.Operation
	device    string
	user      int64
	calls     int
	err       error
}

func (f *intercomFixture) Change(_ context.Context, o roomapp.Operation) (roomapp.Assignment, error) {
	f.operation = o
	f.calls++
	return roomapp.Assignment{DeviceID: o.DeviceID, Owner: 99, SessionID: "private-session", RoomCode: "001234"}, f.err
}
func (f *intercomFixture) Current(_ context.Context, d string, u int64) (roomapp.Assignment, error) {
	f.device = d
	f.user = u
	f.calls++
	return roomapp.Assignment{DeviceID: d, Owner: 99, SessionID: "private-session", RoomCode: "001234"}, nil
}
func (f *intercomFixture) Connect(_ context.Context, d string, _ roomapp.Presence) (roomapp.Credential, error) {
	f.device = d
	f.calls++
	return roomapp.Credential{PeerID: "peer", Token: "private-token"}, nil
}
func (f *intercomFixture) Report(context.Context, string, roomapp.Presence) error {
	f.calls++
	return nil
}

func TestIntercomHTTPIdentityAndCredentialBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	f := &intercomFixture{}
	RegisterIntercom(r, "test-secret", f)
	token := func(claims jwt.MapClaims) string {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
		s, e := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-secret"))
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	user := token(jwt.MapClaims{"user_id": 17})
	device := token(jwt.MapClaims{"device_id": "jwt-device"})
	request := func(method, path, bearer, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	w := request("POST", "/v1/call/group/device/create", device, `{"device_id":"forged","user_id":99,"password":"0573"}`)
	if w.Code != 200 || f.operation.DeviceID != "jwt-device" || f.operation.UserID != 0 {
		t.Fatalf("device identity: %+v %s", f.operation, w.Body.String())
	}
	w = request("POST", "/v1/call/group/web/device/path-device/join", user, `{"device_id":"forged","user_id":99,"room_code":"001234"}`)
	if w.Code != 200 || f.operation.DeviceID != "path-device" || f.operation.UserID != 17 || f.operation.Code != "001234" {
		t.Fatalf("web identity: %+v %s", f.operation, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-session") || strings.Contains(w.Body.String(), "owner") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("public response exposed private fields or was cacheable")
	}
	prior := f.calls
	w = request("POST", "/v1/call/group/device/connect-token", user, `{}`)
	if w.Code != http.StatusUnauthorized || f.calls != prior {
		t.Fatal("user token reached device credential service")
	}
	w = request("GET", "/v1/call/group/web/device/target", device, ``)
	if w.Code != http.StatusUnauthorized || f.calls != prior {
		t.Fatal("device token reached user controls")
	}
	w = request("POST", "/v1/call/group/device/connect-token", device, `{}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "private-token") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("device credential response incorrect")
	}
	prior = f.calls
	w = request("POST", "/v1/call/group/device/create", device, `{"password":"`+strings.Repeat("1", 5000)+`"}`)
	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != 40000 || f.calls != prior {
		t.Fatal("oversized request reached service")
	}
	f.err = roomapp.ErrAssigned
	w = request("POST", "/v1/call/group/device/create", device, `{}`)
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != 40923 || !strings.Contains(w.Body.String(), "先退出") {
		t.Fatalf("assigned room response=%s", w.Body.String())
	}
}

func TestIntercomRoutesUseCallNamespace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterIntercom(r, "test-secret", &intercomFixture{})
	if len(r.Routes()) != 11 {
		t.Fatalf("unexpected room route count: %d", len(r.Routes()))
	}
	for _, route := range r.Routes() {
		if !strings.HasPrefix(route.Path, "/v1/call/group/") {
			t.Errorf("room route outside call namespace: %s", route.Path)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/call/group/page?device_id=test", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `id="home" href="/devices"`) {
		t.Fatal("room page or home navigation missing")
	}
}
