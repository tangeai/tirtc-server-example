package mysql_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"thing-connect/internal/push"
	"thing-connect/internal/pushwechat"
	"thing-connect/internal/store/mysql"
	"thing-connect/push-server/handler"
)

type wechatTransport func(*http.Request) (*http.Response, error)

func (f wechatTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise the app's actual HTTP sequence and durable delivery. Only the
// external WeChat login, access token and message responses are fixtures.
func TestAlarmSubscriptionHTTPThroughDelivery(t *testing.T) {
	db := openTestDB(t)
	user := seedUser(t, db)
	device, _ := seedPool(t, db)
	b, err := db.Exec("INSERT INTO device_bind(device_id,user_id,bind_time) VALUES(?,?,NOW())", device, user)
	if err != nil {
		t.Fatal(err)
	}
	bindID, err := b.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanBind(t, db, device)
	t.Cleanup(func() {
		for _, table := range []string{"push_delivery", "push_event", "push_window"} {
			db.Exec("DELETE FROM "+table+" WHERE device_id=?", device)
		}
		db.Exec("DELETE s FROM push_subscription s JOIN push_recipient r ON s.generation=r.generation WHERE r.user_id=?", user)
		db.Exec("DELETE FROM push_recipient WHERE user_id=?", user)
		db.Exec("DELETE FROM push_session WHERE user_id=?", user)
	})
	store := mysql.NewPushStore(db)
	p := push.DefaultPolicy()
	p.AppID, p.Alarm.Enabled = "test-app-"+device, true
	channel := pushwechat.Channel{AppID: p.AppID, Secret: "test-secret", Config: p, Reserve: store.ReserveSend}
	service := push.New(store, channel, p)
	router := gin.New()
	handler.Register(router, func() *push.Service { return service }, "test-internal", "test-jwt")
	jwtToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": time.Now().Add(time.Minute).Unix(), "user_id": user}).SignedString([]byte("test-jwt"))
	if err != nil {
		t.Fatal(err)
	}
	var sent int
	var reference string
	prior := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = prior })
	http.DefaultTransport = wechatTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.weixin.qq.com" {
			return nil, fmt.Errorf("unexpected external call")
		}
		body := ""
		switch r.URL.Path {
		case "/sns/jscode2session":
			body = `{"openid":"test-receiver"}`
		case "/cgi-bin/stable_token":
			body = `{"access_token":"test-token","expires_in":7200}`
		case "/cgi-bin/message/subscribe/send":
			var msg struct {
				To       string `json:"touser"`
				Template string `json:"template_id"`
				Page     string
				Data     map[string]struct{ Value string }
			}
			if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
				return nil, err
			}
			if msg.To != "test-receiver" || msg.Template != p.Alarm.ID || msg.Data["character_string4"].Value != device || !strings.HasPrefix(msg.Page, p.Page+"?ref=") {
				return nil, fmt.Errorf("wrong notification target or payload")
			}
			reference = strings.TrimPrefix(msg.Page, p.Page+"?ref=")
			sent++
			body = `{"errcode":0}`
		default:
			return nil, fmt.Errorf("unexpected WeChat endpoint")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	call := func(method, path string, payload any) map[string]any {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, "/v1/push/"+path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+jwtToken)
		req.Header.Set("X-Internal-Key", "test-internal")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var envelope struct {
			Code int
			Data map[string]any
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return envelope.Data
	}
	if got := call("GET", "user/capabilities", nil); got["alarm_subscribed"] != false || got["alarm_template_id"] != p.Alarm.ID {
		t.Fatal(got)
	}
	session := call("POST", "user/subscriptions", map[string]string{"app_id": p.AppID, "code": "test-code", "event_type": "alarm"})
	path := "user/subscriptions/" + session["session_id"].(string)
	call("POST", path+"/confirm", map[string]bool{"replace": false})
	call("POST", path+"/result", map[string]string{"decision": "accept"})
	if call("GET", "user/capabilities", nil)["alarm_subscribed"] != true {
		t.Fatal("grant not visible")
	}
	event := push.Event{ID: "http-test:alarm", Device: device, Bind: fmt.Sprintf("%d:1", bindID), Kind: "alarm", Occurred: time.Now().UTC(), Registered: time.Now().UTC(), LabelText: "哭声"}
	if got := call("POST", "internal/events", event)["outcome"]; got != "queued" {
		t.Fatal(got)
	}
	ctx := context.Background()
	d, err := store.ClaimPush(ctx)
	if err != nil || d == nil {
		t.Fatal(d, err)
	}
	result := channel.Send(ctx, *d)
	if result != push.Accepted || sent != 1 {
		t.Fatal(result, sent)
	}
	if err := store.FinishPush(ctx, *d, result); err != nil {
		t.Fatal(err)
	}
	if got := call("GET", "user/references/"+reference, nil); got["device_id"] != device || got["label_text"] != event.LabelText {
		t.Fatal(got)
	}
	var attempts int
	if err := db.Get(&attempts, "SELECT attempts FROM push_delivery WHERE id=? AND status='accepted'", d.ID); err != nil || attempts != 1 {
		t.Fatal(attempts, err)
	}
}
