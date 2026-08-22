package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrConfigConnectionTestFailed = errors.New("configuration connection test failed")

// MQTTConnection is the transport-neutral candidate passed to the MQTT
// adapter. ConfigTester owns parsing, validation, secret resolution, timeout
// and safe errors; the adapter only proves reachability and authentication.
type MQTTConnection struct {
	Broker   string
	Username string
	ClientID string
	Password string
}

type MQTTConnectionProbe interface {
	Probe(context.Context, MQTTConnection) error
}

type ConfigTestInput struct {
	Namespace       string
	ConfigKey       string
	Value           json.RawMessage
	Secrets         json.RawMessage
	SecretsProvided bool
}

type ConfigTestResult struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type ConfigTester struct {
	configs   *ConfigService
	mqttProbe MQTTConnectionProbe
}

func NewConfigTester(configs *ConfigService, mqttProbe MQTTConnectionProbe) *ConfigTester {
	return &ConfigTester{configs: configs, mqttProbe: mqttProbe}
}

// Test validates and probes a candidate without publishing it. supported is
// false for registry entries that do not declare an online test.
func (t *ConfigTester) Test(ctx context.Context, input ConfigTestInput) (result ConfigTestResult, supported bool, err error) {
	definition, ok := t.configs.registry.Lookup(strings.TrimSpace(input.Namespace), strings.TrimSpace(input.ConfigKey))
	if !ok || definition.TestKind == "" {
		return ConfigTestResult{}, false, nil
	}
	supported = true
	if definition.TestKind != "mqtt" {
		return ConfigTestResult{}, true, errors.New("该配置项的在线测试类型尚未实现")
	}
	if err := t.configs.Validate(input.Namespace, input.ConfigKey, input.Value, input.Secrets, input.SecretsProvided); err != nil {
		return ConfigTestResult{}, true, err
	}
	effectiveSecrets := input.Secrets
	if !input.SecretsProvided {
		effectiveSecrets, err = t.configs.EffectiveSecrets(ctx, input.Namespace, input.ConfigKey, nil, false)
		if err != nil {
			return ConfigTestResult{}, true, &configConnectionTestError{cause: fmt.Errorf("load effective MQTT secret: %w", err)}
		}
	}
	if err := validateRequiredSecrets(input.Namespace, input.ConfigKey, input.Value, effectiveSecrets); err != nil {
		return ConfigTestResult{}, true, err
	}
	connection, err := decodeMQTTTestConnection(input.Value, effectiveSecrets)
	if err != nil {
		return ConfigTestResult{}, true, err
	}
	if t.mqttProbe == nil {
		return ConfigTestResult{}, true, &configConnectionTestError{cause: errors.New("MQTT probe is unavailable")}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := t.mqttProbe.Probe(probeCtx, connection); err != nil {
		return ConfigTestResult{}, true, &configConnectionTestError{cause: err}
	}
	return ConfigTestResult{Kind: definition.TestKind, Message: "MQTT 网络、TLS 和账号认证测试通过"}, true, nil
}

type configConnectionTestError struct{ cause error }

func (e *configConnectionTestError) Error() string { return ErrConfigConnectionTestFailed.Error() }
func (e *configConnectionTestError) Unwrap() error { return ErrConfigConnectionTestFailed }
func (e *configConnectionTestError) Cause() error  { return e.cause }

func decodeMQTTTestConnection(value, secrets json.RawMessage) (MQTTConnection, error) {
	var public struct {
		Broker   string `json:"broker"`
		AuthMode string `json:"auth_mode"`
		Username string `json:"username"`
		ClientID string `json:"client_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&public); err != nil {
		return MQTTConnection{}, errors.New("MQTT 连接配置格式无效")
	}
	var private struct {
		Password string `json:"password"`
	}
	decoder = json.NewDecoder(bytes.NewReader(secrets))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&private); err != nil {
		return MQTTConnection{}, errors.New("MQTT 密码格式无效")
	}
	connection := MQTTConnection{Broker: strings.TrimSpace(public.Broker), Password: private.Password}
	switch public.AuthMode {
	case "username":
		connection.Username = strings.TrimSpace(public.Username)
	case "clientid":
		connection.ClientID = strings.TrimSpace(public.ClientID)
	default:
		return MQTTConnection{}, fmt.Errorf("MQTT 认证方式 %q 不受支持", public.AuthMode)
	}
	return connection, nil
}
