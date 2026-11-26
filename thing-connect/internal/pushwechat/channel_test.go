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
	ch := Channel{Config: push.Config{Crying: "测试提示", Location: "测试位置"}}
	d := push.Delivery{Device: "D123", Open: "openid", Template: push.AlarmTemplate, Reference: "test-ref", Time: "2026-11-26 09:00:00", Kind: "crying"}
	for _, tc := range []struct {
		reply string
		want  push.Result
	}{{"{\"errcode\":0}", push.Accepted}, {"{}", push.Unknown}, {"{\"errcode\":43101}", push.Unsubscribed}, {"{\"errcode\":47003}", push.Failed}, {"{\"errcode\":45009}", push.Retry}} {
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
