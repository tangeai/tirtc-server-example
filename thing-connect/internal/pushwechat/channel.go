// Package pushwechat adapts device alarm notifications to the WeChat long-term subscription template.
package pushwechat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"thing-connect/internal/push"
	"thing-connect/internal/wechat"
)

type Channel struct {
	AppID, Secret string
	Config        push.Config
	Refresh       wechat.RefreshGate
}

func (c Channel) Ready(kind string) bool {
	return c.AppID != "" && c.Secret != "" && c.Config.TemplateID != "" && c.Config.Location != "" && c.Config.Hint(kind) != ""
}
func (c Channel) Login(ctx context.Context, code string) (string, error) {
	return wechat.Jscode2session(ctx, c.AppID, c.Secret, code)
}

func (c Channel) Send(ctx context.Context, d push.Delivery) push.Result {
	if !c.Ready(d.Kind) || d.Template != c.Config.TemplateID {
		return push.Failed
	}
	result := push.Retry
	err := wechat.CallWithAccessToken(ctx, c.AppID, c.Secret, c.Refresh, func(token string) error {
		var channelErr error
		result, channelErr = c.send(ctx, http.DefaultClient, "https://api.weixin.qq.com/cgi-bin/message/subscribe/send", token, d)
		return channelErr
	})
	if err != nil {
		return push.Retry
	}
	return result
}
func (c Channel) send(ctx context.Context, client *http.Client, endpoint, token string, d push.Delivery) (push.Result, error) {
	type field struct {
		Value string `json:"value"`
	}
	data := map[string]field{
		"time1":             {d.Time},
		"enum_string3":      {c.Config.Hint(d.Kind)},
		"enum_string2":      {c.Config.Location},
		"character_string4": {d.Device},
	}
	body, err := json.Marshal(map[string]any{"touser": d.Open, "template_id": d.Template, "page": "pages/notifications/index?ref=" + url.QueryEscape(d.Reference), "data": data})
	if err != nil {
		return push.Failed, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?access_token="+url.QueryEscape(token), bytes.NewReader(body))
	if err != nil {
		return push.Failed, nil
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return push.Unknown, nil
	}
	defer resp.Body.Close()
	var reply struct {
		Code *int `json:"errcode"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&reply) != nil || reply.Code == nil {
		return push.Unknown, nil
	}
	switch *reply.Code {
	case 0:
		return push.Accepted, nil
	case 43101:
		return push.Unsubscribed, nil
	case -1, 45009, 45011:
		return push.Retry, nil
	case 40001, 40014, 42001:
		return push.Retry, &wechat.APIError{Errcode: *reply.Code}
	default:
		return push.Failed, nil
	}
}
