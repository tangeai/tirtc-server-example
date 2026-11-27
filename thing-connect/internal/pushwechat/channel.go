// Package pushwechat adapts device alarm notifications to the WeChat long-term subscription template.
package pushwechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"thing-connect/internal/push"
	"thing-connect/internal/wechat"
)

var messageClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type Channel struct {
	AppID, Secret string
	Config        push.Policy
	Refresh       wechat.RefreshGate
	Reserve       func(context.Context, push.Delivery) (bool, error)
}

func (c Channel) Ready(kind string) bool {
	return c.AppID != "" && c.Secret != "" && c.Config.Template(kind).Enabled
}
func (c Channel) Login(ctx context.Context, code string) (string, error) {
	return wechat.Jscode2session(ctx, c.AppID, c.Secret, code)
}

func (c Channel) Send(ctx context.Context, d push.Delivery) push.Result {
	if !c.Ready(d.Kind) || d.AppID != c.AppID || d.Template != c.Config.Template(d.Kind).ID {
		return push.Failed
	}
	result := push.Retry
	err := wechat.CallWithAccessToken(ctx, c.AppID, c.Secret, c.Refresh, func(token string) error {
		if c.Reserve != nil {
			ok, err := c.Reserve(ctx, d)
			if err != nil || !ok {
				result = push.Deferred
				return nil
			}
		}
		if !d.Expires.IsZero() && !time.Now().Before(d.Expires) {
			result = push.Expired
			return nil
		}
		var channelErr error
		result, channelErr = c.send(ctx, messageClient, "https://api.weixin.qq.com/cgi-bin/message/subscribe/send", token, d)
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
	t := c.Config.Template(d.Kind)
	data := map[string]field{}
	for _, mapping := range []struct{ key, value string }{{t.TimeKey, d.Time}, {t.HintKey, t.Hint}, {t.LocationKey, t.Location}, {t.DeviceKey, d.Device}} {
		if mapping.key != "" {
			data[mapping.key] = field{mapping.value}
		}
	}
	if t.LabelKey != "" {
		text := []rune(d.LabelText)
		value := "异常提醒"
		if len(text) > 0 {
			value = string(text[:min(len(text), 20)])
		}
		data[t.LabelKey] = field{value}
	}
	message := map[string]any{"touser": d.Open, "template_id": d.Template, "data": data}
	if c.Config.Page != "" {
		message["page"] = c.Config.Page + "?ref=" + url.QueryEscape(d.Reference)
	}
	body, err := json.Marshal(message)
	if err != nil {
		return push.Failed, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?access_token="+url.QueryEscape(token), bytes.NewReader(body))
	if err != nil {
		return push.Failed, nil
	}
	req.Header.Set("Content-Type", "application/json")
	if !d.Expires.IsZero() && !time.Now().Before(d.Expires) {
		return push.Expired, nil
	}
	resp, err := client.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return push.Retry, nil
		}
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
	case 45009, 45011:
		return push.Limited, nil
	case -1:
		return push.Retry, nil
	case 40001, 40014, 42001:
		return push.Retry, &wechat.APIError{Errcode: *reply.Code}
	default:
		return push.Failed, nil
	}
}
