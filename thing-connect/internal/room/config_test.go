package room

import (
	"strings"
	"testing"
)

func TestPolicyAndASCIIInputs(t *testing.T) {
	if _, e := ParseConfig([]byte(DefaultConfigJSON)); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`{}`, strings.Replace(DefaultConfigJSON, `"15s"`, `"0s"`, 1), strings.Replace(DefaultConfigJSON, `100`, `101`, 1), strings.Replace(DefaultConfigJSON, `"24h"`, `"oops"`, 1), strings.TrimSuffix(DefaultConfigJSON, "}") + `,"extra":1}`} {
		if _, e := ParseConfig([]byte(bad)); e == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	for _, s := range []string{"１２３４５６", "12345", "1234567", "12 345", "1e0000"} {
		if Digits(s, 6) {
			t.Errorf("accepted %q", s)
		}
	}
	if !Digits("000001", 6) {
		t.Fatal("leading zero rejected")
	}
}
