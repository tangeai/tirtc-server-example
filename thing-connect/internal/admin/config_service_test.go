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
