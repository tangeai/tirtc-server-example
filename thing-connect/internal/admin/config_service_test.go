package admin

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jmoiron/sqlx/reflectx"
)

func TestConfigEventDatabaseAndJSONMappings(t *testing.T) {
	mapping := reflectx.NewMapper("db").TypeMap(reflect.TypeOf(configEvent{}))
	for _, column := range []string{"id", "entry_id", "namespace", "config_key", "scope_type", "scope_id", "revision"} {
		if mapping.Names[column] == nil {
			t.Errorf("configEvent has no sqlx destination for %q", column)
		}
	}

	raw, err := json.Marshal(configEvent{OutboxID: 9, EntryID: 11, Namespace: "user-server", ConfigKey: "smtp", ScopeType: "global", Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if _, exposed := payload["OutboxID"]; exposed {
		t.Fatal("outbox database id must not be published")
	}
	if payload["entry_id"] != float64(11) {
		t.Fatalf("entry_id = %v, want 11", payload["entry_id"])
	}
}

func TestCaptchaProvider(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value string
		want  string
	}{
		{value: `{"provider":"yidun"}`, want: "yidun"},
		{value: `{"provider":" Tencent "}`, want: "tencent"},
		{value: `{}`, want: ""},
		{value: `not-json`, want: ""},
	} {
		if got := captchaProvider(test.value); got != test.want {
			t.Errorf("captchaProvider(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestRecognitionModelSecretValidation(t *testing.T) {
	service := &ConfigService{registry: DefaultConfigRegistry()}
	definition, ok := service.registry.Lookup("recognition-server", "recognition.models")
	if !ok {
		t.Fatal("recognition model definition missing")
	}
	for _, tc := range []struct {
		name, secret string
		valid        bool
	}{
		{"api key", `{"api_key":"test-key"}`, true},
		{"unknown field", `{"password":"test-key"}`, false},
		{"null key", `{"api_key":null}`, false},
		{"numeric key", `{"api_key":123}`, false},
		{"masked key", `{"api_key":"******"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := service.Validate(definition.Namespace, definition.Key, definition.Default, json.RawMessage(tc.secret), true)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
		})
	}
	if err := service.Validate(definition.Namespace, definition.Key, definition.Default, nil, false); err != nil {
		t.Fatalf("keeping existing secret rejected: %v", err)
	}
}
