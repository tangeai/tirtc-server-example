package pushwechat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"thing-connect/internal/push"
)

func TestAlarmTemplateFieldsAndDeliveryResults(t *testing.T) {
	p := push.DefaultPolicy()
	p.Crying.Hint, p.Crying.Location = "测试提示", "测试位置"
	ch := Channel{Config: p}
	d := push.Delivery{Device: "D123", Open: "openid", Template: push.AlarmTemplate, Reference: "test-ref", Time: "2026-11-26 09:00:00", Kind: "crying"}
	for _, tc := range []struct {
		reply string
		want  push.Result
	}{{"{\"errcode\":0}", push.Accepted}, {"{}", push.Unknown}, {"{\"errcode\":43101}", push.Unsubscribed}, {"{\"errcode\":47003}", push.Failed}, {"{\"errcode\":45009}", push.Limited}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var v struct {
				Data     map[string]struct{ Value string }
				Page     string
				Template string `json:"template_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
				t.Error(err)
			}
			if v.Template != d.Template || v.Page != "pages/notifications/index?ref=test-ref" || len(v.Data) != 4 || v.Data["time1"].Value != d.Time || v.Data["enum_string3"].Value != "测试提示" || v.Data["enum_string2"].Value != "测试位置" || v.Data["character_string4"].Value != d.Device {
				t.Errorf("template mismatch: %+v", v)
			}
			fmt.Fprint(w, tc.reply)
		}))
		got, err := ch.send(context.Background(), srv.Client(), srv.URL, "test-token", d)
		srv.Close()
		if err != nil || got != tc.want {
			t.Fatalf("reply=%s: %s %v", tc.reply, got, err)
		}
	}
}

func TestConfiguredFieldNamesAndOptionalPage(t *testing.T) {
	policy := push.DefaultPolicy()
	policy.Page = ""
	policy.Alarm = push.Template{ID: "other-id", Enabled: true, LabelKey: "thing7", DeviceKey: "character_string8"}
	ch := Channel{Config: policy}
	d := push.Delivery{Device: "device", LabelText: "哭泣、未知标签", Kind: "alarm", Template: "other-id"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			Page     *string
			Template string `json:"template_id"`
			Data     map[string]struct{ Value string }
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Error(err)
		}
		if msg.Page != nil || msg.Template != d.Template || len(msg.Data) != 2 || msg.Data["thing7"].Value != "哭泣、未知标签" || msg.Data["character_string8"].Value != "device" {
			t.Errorf("unexpected configured message %+v", msg)
		}
		fmt.Fprint(w, `{"errcode":0}`)
	}))
	defer server.Close()
	if result, err := ch.send(context.Background(), server.Client(), server.URL, "test", d); err != nil || result != push.Accepted {
		t.Fatal(result, err)
	}
}
