package webnav

import (
	"strings"
	"testing"
)

func TestNavigationValidationAndPublication(t *testing.T) {
	for _, raw := range []string{`null`, `{"unknown":1}`, `{"links":[{"name":"官网","url":"javascript:alert(1)","enabled":true}]}`, `{"links":[{"name":"官网","url":"https://u:p@example.com","enabled":true}]}`, `{"links":[{"name":"","url":"https://example.com"}]}`, `{} {}`, `{"links":[{"name":"官网","url":"https://example.com","extra":true}]}`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("accepted invalid configuration: %s", raw)
		}
	}
	links := []Link{{Name: "官网", URL: "https://example.com", Enabled: true}, {Name: "文档", URL: "https://docs.example.com", Enabled: false}}
	state, err := New(Config{links})
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Current(); len(got.Links) != 1 || got.Links[0].Name != "官网" {
		t.Fatal(got)
	}
	if err := state.Apply([]byte(`{"links":[]}`), 0); err != nil {
		t.Fatal(err)
	}
	if len(state.Current().Links) != 1 {
		t.Fatal("unpublished default overwrote YAML")
	}
	if err := state.Apply([]byte(`{"links":[]}`), 3); err != nil {
		t.Fatal(err)
	}
	if got := state.Current(); got.Revision != 3 || got.Links == nil || len(got.Links) != 0 {
		t.Fatal(got)
	}
	if err := state.Apply([]byte(`{"links":[{"name":"bad","url":"data:text/plain,hello"}]}`), 4); err == nil {
		t.Fatal("invalid update applied")
	}
	if state.Current().Revision != 3 {
		t.Fatal("invalid update mutated revision")
	}
	if err := (Config{Links: append(links, links...)}).Validate(); err == nil {
		t.Fatal("accepted four links")
	}
	if err := (Config{Links: []Link{{Name: strings.Repeat("文", 21), URL: "https://example.com"}}}).Validate(); err == nil {
		t.Fatal("accepted long name")
	}
}
