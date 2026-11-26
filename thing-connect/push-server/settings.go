package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"thing-connect/internal/dynamicconfig"
	"thing-connect/internal/push"
	"thing-connect/internal/pushwechat"
	"thing-connect/internal/wechat"
)

type settingsSnapshot struct {
	service *push.Service
	channel pushwechat.Channel
}

type settings struct {
	loaded   chan struct{}
	first    sync.Once
	current  atomic.Pointer[settingsSnapshot]
	repo     push.Repository
	gate     wechat.RefreshGate
	fallback push.Policy
	secret   string
}

func (s *settings) apply(snapshot dynamicconfig.Snapshot) error {
	policy, secret := s.fallback, s.secret
	if snapshot.Revision != 0 {
		var err error
		policy, err = push.ParsePolicy(snapshot.Value)
		if err != nil {
			return err
		}
		var credentials struct {
			Secret string `json:"app_secret"`
		}
		if len(snapshot.Secrets) > 0 {
			if err = json.Unmarshal(snapshot.Secrets, &credentials); err != nil {
				return errors.New("微信推送密钥格式无效")
			}
		}
		secret = credentials.Secret
		if (policy.Crying.Enabled || policy.Quarrel.Enabled) && strings.TrimSpace(secret) == "" {
			return errors.New("启用微信推送需要 AppSecret")
		}
	}
	ch := pushwechat.Channel{Config: policy, AppID: policy.AppID, Secret: secret, Refresh: s.gate}
	s.current.Store(&settingsSnapshot{push.New(s.repo, ch, policy), ch})
	s.first.Do(func() { close(s.loaded) })
	return nil
}

func (s *settings) service() *push.Service { return s.current.Load().service }

func (s *settings) Send(ctx context.Context, d push.Delivery) push.Result {
	ch := s.current.Load().channel
	if d.Attempts > ch.Config.MaxAttempts {
		return push.Failed
	}
	result := ch.Send(ctx, d)
	if result == push.Retry && d.Attempts >= ch.Config.MaxAttempts {
		return push.Failed
	}
	return result
}
