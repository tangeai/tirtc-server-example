package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type recordingMQTTProbe struct {
	connection MQTTConnection
	err        error
}

func (p *recordingMQTTProbe) Probe(_ context.Context, connection MQTTConnection) error {
	p.connection = connection
	return p.err
}

func TestConfigTesterChecksCandidateMQTTWithoutWriting(t *testing.T) {
	probe := &recordingMQTTProbe{}
	tester := NewConfigTester(NewConfigService(nil, DefaultConfigRegistry(), nil), probe)
	result, supported, err := tester.Test(context.Background(), ConfigTestInput{
		Namespace: "voip-server", ConfigKey: "mqtt.connection",
		Value:           json.RawMessage(`{"broker":"mqtts://broker.example.com:8883","auth_mode":"username","username":"voipsrv","client_id":""}`),
		Secrets:         json.RawMessage(`{"password":"secret"}`),
		SecretsProvided: true,
	})
	if err != nil || !supported {
		t.Fatalf("Test() = result %#v, supported %v, err %v", result, supported, err)
	}
	if result.Message != "MQTT 网络、TLS 和账号认证测试通过" {
		t.Fatalf("message = %q", result.Message)
	}
	if probe.connection.Broker != "mqtts://broker.example.com:8883" || probe.connection.Username != "voipsrv" || probe.connection.Password != "secret" {
		t.Fatalf("connection = %#v", probe.connection)
	}
}

func TestConfigTesterSanitizesMQTTProbeFailure(t *testing.T) {
	probe := &recordingMQTTProbe{err: errors.New("dial tcp 10.0.0.8:1883: connection refused")}
	tester := NewConfigTester(NewConfigService(nil, DefaultConfigRegistry(), nil), probe)
	_, supported, err := tester.Test(context.Background(), ConfigTestInput{
		Namespace: "call-server", ConfigKey: "mqtt.connection",
		Value:           json.RawMessage(`{"broker":"mqtt://broker.internal:1883","auth_mode":"clientid","username":"","client_id":"callsrv"}`),
		Secrets:         json.RawMessage(`{"password":"secret"}`),
		SecretsProvided: true,
	})
	if !supported || !errors.Is(err, ErrConfigConnectionTestFailed) {
		t.Fatalf("supported = %v, err = %v", supported, err)
	}
	if err == nil || strings.Contains(err.Error(), "10.0.0.8") || strings.Contains(err.Error(), "broker.internal") {
		t.Fatalf("probe error leaked infrastructure details: %v", err)
	}
}

func TestConfigTesterSkipsNonTestableConfiguration(t *testing.T) {
	tester := NewConfigTester(NewConfigService(nil, DefaultConfigRegistry(), nil), &recordingMQTTProbe{})
	_, supported, err := tester.Test(context.Background(), ConfigTestInput{
		Namespace: "common", ConfigKey: "tirtc", Value: json.RawMessage(`{}`),
	})
	if err != nil || supported {
		t.Fatalf("supported = %v, err = %v", supported, err)
	}
}
