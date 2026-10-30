package weblanguage

import (
	"sync"
	"testing"
)

func TestPolicyValidationAndAtomicPublication(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"default":"fr","allow_switch":true}`, `{"default":"en"}`, `{"default":"en","allow_switch":"yes"}`, `{"default":"en","allow_switch":null}`, `{"default":"en","allow_switch":true,"unknown":1}`, DefaultJSON + ` {}`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("accepted invalid config %s", raw)
		}
	}
	state, err := New(Config{Default: "en", AllowSwitch: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Apply([]byte(DefaultJSON), 0); err != nil {
		t.Fatal(err)
	}
	if got := state.Current(); got.Default != "en" || got.AllowSwitch || got.Revision != 0 {
		t.Fatal(got)
	}
	if err := state.Apply([]byte(`{"default":"zh-CN","allow_switch":true}`), 7); err != nil {
		t.Fatal(err)
	}
	if err := state.Apply([]byte(`{"default":"fr","allow_switch":true}`), 8); err == nil {
		t.Fatal("invalid publication applied")
	}
	if got := state.Current(); got.Default != "zh-CN" || !got.AllowSwitch || got.Revision != 7 {
		t.Fatal(got)
	}
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 100; j++ {
				_ = state.Current()
				if err := state.Apply([]byte(DefaultJSON), 9); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	group.Wait()
}

func TestServicesHaveIndependentPolicies(t *testing.T) {
	user, _ := New(DefaultConfig())
	ai, _ := New(DefaultConfig())
	if err := user.Apply([]byte(`{"default":"en","allow_switch":false}`), 2); err != nil {
		t.Fatal(err)
	}
	if ai.Current().Default != "auto" || user.Current().Default != "en" {
		t.Fatal("service policies share state")
	}
}
