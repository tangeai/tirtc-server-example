package dynamicconfig

import (
	"testing"

	"thing-connect/internal/config"
)

func TestResolveTiRTCPublishedValueIsRequiredAndAuthoritative(t *testing.T) {
	fallback := config.TirtcCfg{AppID: "legacy", AccessKeyID: "legacy-id", SecretKeyID: "legacy-secret"}
	if _, err := ResolveTiRTC(Snapshot{Value: []byte(`{"endpoint":"","app_id":""}`), Revision: 1}, fallback); err == nil {
		t.Fatal("incomplete published TiRTC config fell back to YAML")
	}
	resolved, err := ResolveTiRTC(Snapshot{
		Value:   []byte(`{"endpoint":"https://api.example.com","app_id":"app"}`),
		Secrets: []byte(`{"access_key_id":"access","secret_key_id":"secret"}`), Revision: 2,
	}, fallback)
	if err != nil || resolved.AppID != "app" || resolved.AccessKeyID != "access" {
		t.Fatalf("ResolveTiRTC = %+v, %v", resolved, err)
	}
}
