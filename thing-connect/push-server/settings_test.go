package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"thing-connect/internal/dynamicconfig"
	"thing-connect/internal/push"
)

type settingsRepo struct{ push.Repository }

func TestPublishedNotificationSettings(t *testing.T) {
	local := push.Config{TemplateID: "local", Location: "位置", Crying: "哭声"}.Policy("local-app")
	s := settings{loaded: make(chan struct{}), repo: settingsRepo{}, fallback: local, secret: "local-secret"}
	if err := s.apply(dynamicconfig.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	old := s.service()
	p := push.DefaultPolicy()
	p.AppID = "new-app"
	p.Crying.Enabled = true
	p.Crying.HintKey, p.Crying.LocationKey = "", ""
	p.Crying.ID = "new-template"
	raw, _ := json.Marshal(p)
	if err := s.apply(dynamicconfig.Snapshot{Revision: 1, Value: raw}); err == nil || old != s.service() {
		t.Fatal("missing secret must preserve last snapshot")
	}
	if err := s.apply(dynamicconfig.Snapshot{Revision: 1, Value: raw, Secrets: json.RawMessage(`{"app_secret":"new-secret"}`)}); err != nil {
		t.Fatal(err)
	}
	if !old.Enabled("crying") || old.TemplateID("crying") != "local" || s.service().TemplateID("crying") != "new-template" || s.service().Enabled("quarrel") {
		t.Fatal("mixed or incorrect policy")
	}
	for _, d := range []push.Delivery{{AppID: "local-app", Template: "new-template", Kind: "crying"}, {AppID: "new-app", Template: "local", Kind: "crying"}, {AppID: "new-app", Template: "new-template", Kind: "crying", Attempts: 6}} {
		if got := s.Send(context.Background(), d); got != push.Failed {
			t.Fatal(got)
		}
	}
}

func TestRejectInvalidTemplateMapping(t *testing.T) {
	p := push.DefaultPolicy()
	p.Crying.DeviceKey = p.Crying.TimeKey
	if err := p.Validate(); err == nil {
		t.Fatal("duplicate template field")
	}
	for _, raw := range []string{`{"unexpected":true}`, `{"max_attempts":0}`, `{"page":"https://example.org/"}`, `{"crying":{"enabled":true}}`} {
		if _, err := push.ParsePolicy([]byte(raw)); err == nil {
			t.Fatal(raw)
		}
	}
}

func TestWaitingForAdminDefersEvent(t *testing.T) {
	waiting := push.AwaitingPolicy(settingsRepo{})
	if waiting.Enabled("crying") {
		t.Fatal("published template used before fetch")
	}
	if _, err := waiting.Receive(context.Background(), push.Event{}); !errors.Is(err, push.ErrUnavailable) {
		t.Fatal(err)
	}
}
