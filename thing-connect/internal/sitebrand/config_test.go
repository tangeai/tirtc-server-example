package sitebrand

import (
	"strings"
	"sync"
	"testing"
)

func TestPublicationAndFallback(t *testing.T) {
	fallback := Config{Name: "我的站点", NameEN: "My site", PublicURL: "https://example.test"}
	state, err := New(fallback)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Apply([]byte(DefaultJSON), 0); err != nil {
		t.Fatal(err)
	}
	if state.Current().Config != fallback {
		t.Fatal("revision zero must retain YAML fallback")
	}
	if err := state.Apply([]byte(`{"name":"新名称","name_en":"","public_url":"https://new.example/"}`), 7); err != nil {
		t.Fatal(err)
	}
	before := state.Current()
	if before.Revision != 7 || before.PublicURL != "https://new.example" {
		t.Fatalf("publication = %+v", before)
	}
	if err := state.Apply([]byte(`{"name":"","name_en":""}`), 8); err == nil {
		t.Fatal("invalid update accepted")
	}
	if state.Current() != before {
		t.Fatal("invalid update changed current value")
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				_ = state.Apply([]byte(DefaultJSON), 9)
				_ = state.Current()
			}
		}()
	}
	wg.Wait()
}
func TestStrictConfiguration(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"name":"x","name_en":null}`, `{"name":"x","name_en":"","unknown":1}`, `{"name":"x","name_en":""} {}`, `{"name":" x","name_en":""}`, `{"name":"x\n","name_en":""}`, `{"name":"` + strings.Repeat("钛", 41) + `","name_en":""}`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, address := range []string{"http://example.com", "https://example.com/path", "https://example.com//", "https://u:p@example.com", "https://example.com?q=1", "https://example.com/#x", "javascript:alert(1)"} {
		if err := (Config{Name: "test", PublicURL: address}).Validate(); err == nil {
			t.Errorf("accepted URL %q", address)
		}
	}
}
