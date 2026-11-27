package push

import "testing"

func TestDeliveryPolicyValidation(t *testing.T) {
	p, err := ParseDeliveryPolicy([]byte(`{"source_max_attempts":1,"event_ttl_seconds":30}`))
	if err != nil || p.SourceAttempts != 1 || p.TTLSeconds != 30 || !p.Enabled || p.SourceCapacity < 32 {
		t.Fatal(p, err)
	}
	for _, input := range []string{`{"event_ttl_seconds":601}`, `{"event_ttl_seconds":29}`, `{"source_max_attempts":11}`, `{"source_capacity":31}`, `{"enabled":null}`, `{"extra":true}`, `{"wechat_requests_per_second":0}`} {
		if _, err := ParseDeliveryPolicy([]byte(input)); err == nil {
			t.Fatal(input)
		}
	}
}
