package dynamicconfig

import (
	"context"
	"errors"
	"testing"

	"thing-connect/internal/config"
)

type mqttSnapshotLoader struct {
	snapshot Snapshot
	err      error
}

func (l mqttSnapshotLoader) Load(context.Context, string, string) (Snapshot, error) {
	return l.snapshot, l.err
}

func TestResolveMQTTRequiresPublishedConfigWithoutLegacyFallback(t *testing.T) {
	_, _, err := ResolveMQTT(context.Background(), mqttSnapshotLoader{}, "call-server", config.MQTTCfg{})
	if !errors.Is(err, ErrMQTTNotConfigured) {
		t.Fatalf("ResolveMQTT error = %v, want ErrMQTTNotConfigured", err)
	}
}

func TestResolveMQTTPublishedConfigIsAuthoritative(t *testing.T) {
	loader := mqttSnapshotLoader{snapshot: Snapshot{
		Value:   []byte(`{"broker":"mqtts://broker.example.com:8883","auth_mode":"username","username":"callsrv","client_id":""}`),
		Secrets: []byte(`{"password":"secret"}`), Revision: 3,
	}}
	resolved, revision, err := ResolveMQTT(context.Background(), loader, "call-server", config.MQTTCfg{Broker: "mqtt://legacy:1883", ClientID: "legacy", Password: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if revision != 3 || resolved.Broker != "mqtts://broker.example.com:8883" || resolved.Username != "callsrv" || resolved.ClientID != "" || resolved.Password != "secret" {
		t.Fatalf("resolved MQTT = %+v revision=%d", resolved, revision)
	}
}

func TestResolveMQTTRejectsAmbiguousAuthentication(t *testing.T) {
	loader := mqttSnapshotLoader{snapshot: Snapshot{
		Value:   []byte(`{"broker":"mqtt://broker.example.com:1883","auth_mode":"username","username":"callsrv","client_id":"fixed"}`),
		Secrets: []byte(`{"password":"secret"}`), Revision: 1,
	}}
	if _, _, err := ResolveMQTT(context.Background(), loader, "call-server", config.MQTTCfg{}); err == nil {
		t.Fatal("ambiguous MQTT authentication accepted")
	}
}
