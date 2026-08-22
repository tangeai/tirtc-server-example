package admin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistryRejectsUnknownAndBadValues(t *testing.T) {
	registry := DefaultConfigRegistry()
	if err := registry.Validate("user-server", "unknown", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown config key accepted")
	}
	if err := registry.Validate("user-server", "email.code_ttl", json.RawMessage(`{"duration":"0s"}`)); err == nil {
		t.Fatal("zero duration accepted")
	}
	if err := registry.Validate("user-server", "email.code_ttl", json.RawMessage(`{"duration":"5m"}`)); err != nil {
		t.Fatalf("valid duration rejected: %v", err)
	}
}

func TestRegistryDefinitionsAndUsableInitialValues(t *testing.T) {
	registry := DefaultConfigRegistry()
	definitions := registry.List("")
	if len(definitions) != 23 {
		t.Fatalf("registered configuration count = %d, want 23", len(definitions))
	}
	for _, definition := range definitions {
		value := definition.Default
		// TiRTC has no runnable default because its credentials are
		// environment-specific and must be published through Admin.
		if definition.Namespace == "common" && definition.Key == "tirtc" {
			value = json.RawMessage(`{"endpoint":"https://api.example.com","app_id":"test-app"}`)
		}
		if definition.Key == "mqtt.connection" {
			value = json.RawMessage(`{"broker":"mqtt://broker.example.com:1883","auth_mode":"username","username":"service","client_id":""}`)
		}
		if err := registry.Validate(definition.Namespace, definition.Key, value); err != nil {
			t.Errorf("registered value %s/%s rejected: %v", definition.Namespace, definition.Key, err)
		}
		customEditor := definition.Group == "email_template" || definition.Key == "wechat.apps"
		if !customEditor && len(definition.Fields) == 0 {
			t.Errorf("registered value %s/%s has no form schema", definition.Namespace, definition.Key)
		}
		for _, field := range definition.Fields {
			if len(field.Path) == 0 || field.Label == "" || field.Kind == "" {
				t.Errorf("invalid form field in %s/%s: %+v", definition.Namespace, definition.Key, field)
			}
			if field.Secret && len(definition.SecretPaths) == 0 {
				t.Errorf("secret field in %s/%s is missing registry secret paths", definition.Namespace, definition.Key)
			}
		}
	}
	for _, namespace := range []string{"device-server", "user-server", "voip-server", "ai-server", "call-server", "common", "system"} {
		if len(registry.List(namespace)) == 0 {
			t.Errorf("namespace %s has no registered configuration", namespace)
		}
	}
	tirtc, ok := registry.Lookup("common", "tirtc")
	if !ok || !tirtc.Required || !tirtc.Blocking || tirtc.Description == "" {
		t.Fatalf("TiRTC blocking metadata is incomplete: %+v", tirtc)
	}
	blockingFields := 0
	for _, field := range tirtc.Fields {
		if field.Blocking {
			blockingFields++
		}
	}
	if blockingFields != 3 {
		t.Fatalf("TiRTC blocking fields = %d, want 3", blockingFields)
	}
}

func TestMQTTConnectionIsBlockingAndRequiresUnambiguousAuthentication(t *testing.T) {
	registry := DefaultConfigRegistry()
	for _, namespace := range []string{"user-server", "voip-server", "call-server"} {
		definition, ok := registry.Lookup(namespace, "mqtt.connection")
		if !ok || !definition.Required || !definition.Blocking || definition.Reload != "restart" {
			t.Fatalf("%s MQTT definition = %+v", namespace, definition)
		}
		valid := json.RawMessage(`{"broker":"mqtts://broker.example.com:8883","auth_mode":"username","username":"service","client_id":""}`)
		if err := registry.Validate(namespace, "mqtt.connection", valid); err != nil {
			t.Fatalf("%s valid MQTT connection rejected: %v", namespace, err)
		}
		if err := validateRequiredSecrets(namespace, "mqtt.connection", valid, json.RawMessage(`{"password":"secret"}`)); err != nil {
			t.Fatalf("%s valid MQTT secret rejected: %v", namespace, err)
		}
	}
	invalid := json.RawMessage(`{"broker":"mqtt://broker.example.com:1883/path","auth_mode":"username","username":"service","client_id":"duplicate"}`)
	if err := registry.Validate("user-server", "mqtt.connection", invalid); err == nil {
		t.Fatal("ambiguous or path-bearing MQTT connection accepted")
	}
}

func TestBlockingDefinitionsAreTargetedPerService(t *testing.T) {
	registry := DefaultConfigRegistry()
	for service, want := range map[string][]string{
		"device-server": {},
		"user-server":   {"common/tirtc", "user-server/mqtt.connection"},
		"voip-server":   {"common/tirtc", "voip-server/mqtt.connection"},
		"ai-server":     {"common/tirtc"},
		"call-server":   {"call-server/mqtt.connection", "common/tirtc"},
	} {
		definitions := registry.BlockingForTarget(service)
		got := make([]string, 0, len(definitions))
		for _, definition := range definitions {
			got = append(got, definitionID(definition.Namespace, definition.Key))
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s blocking definitions = %v, want %v", service, got, want)
		}
	}
}

func TestAIResourcePolicyRequiresIDAndNamePairs(t *testing.T) {
	registry := DefaultConfigRegistry()
	valid := json.RawMessage(`{"resource_quota":{"mcp":4,"device_plugin":20,"kb":5},"default_resources":{"mcp":[{"id":"fo4ho7e618n4","name":"高德地图1"},{"id":"fo4hpeez2qyo","name":"百度地图"}],"device_plugin":[],"kb":[]}}`)
	if err := registry.Validate("ai-server", "ai.resource_policy", valid); err != nil {
		t.Fatalf("valid resource references rejected: %v", err)
	}
	for _, invalid := range []string{
		`{"resource_quota":{"mcp":4,"device_plugin":20,"kb":5},"default_resources":{"mcp":["fo4ho7e618n4"],"device_plugin":[],"kb":[]}}`,
		`{"resource_quota":{"mcp":4,"device_plugin":20,"kb":5},"default_resources":{"mcp":[{"id":"fo4ho7e618n4","name":""}],"device_plugin":[],"kb":[]}}`,
		`{"resource_quota":{"mcp":4,"device_plugin":20,"kb":5},"default_resources":{"mcp":[{"id":"same","name":"甲"},{"id":"same","name":"乙"}],"device_plugin":[],"kb":[]}}`,
	} {
		if err := registry.Validate("ai-server", "ai.resource_policy", json.RawMessage(invalid)); err == nil {
			t.Fatalf("invalid resource references accepted: %s", invalid)
		}
	}
	definition, _ := registry.Lookup("ai-server", "ai.resource_policy")
	resourceFields := 0
	for _, field := range definition.Fields {
		if field.Kind == "resource_refs" {
			resourceFields++
		}
	}
	if resourceFields != 3 {
		t.Fatalf("resource reference editors = %d, want 3", resourceFields)
	}
}

func TestTemplateVariableWhitelist(t *testing.T) {
	registry := DefaultConfigRegistry()
	bad := json.RawMessage(`{"enabled":true,"subject":"Hello {{product_name}}","html_body":"{{code}} {{exec}}","text_body":""}`)
	if err := registry.Validate("user-server", "email.template.registration_code", bad); err == nil {
		t.Fatal("unknown template variable accepted")
	}
	valid := json.RawMessage(`{"enabled":true,"subject":"{{ product_name }} code","html_body":"{{ code }} expires in {{ expires_in_minutes }} minutes","text_body":"{{support_email}}"}`)
	if err := registry.Validate("user-server", "email.template.registration_code", valid); err != nil {
		t.Fatalf("whitelisted template variables rejected: %v", err)
	}
}

func TestMFAPolicyOnlyRequiresEnabled(t *testing.T) {
	registry := DefaultConfigRegistry()
	if err := registry.Validate("system", "mfa.policy", json.RawMessage(`{"enabled":true}`)); err != nil {
		t.Fatalf("default MFA policy rejected: %v", err)
	}
	if err := registry.Validate("system", "mfa.policy", json.RawMessage(`{"enabled":"true"}`)); err == nil {
		t.Fatal("non-boolean MFA switch accepted")
	}
}

func TestCaptchaProviderValidation(t *testing.T) {
	registry := DefaultConfigRegistry()
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "disabled", value: `{"enabled":false,"provider":"yidun","captcha_id":"","public_config":{}}`},
		{name: "yidun", value: `{"enabled":true,"provider":"yidun","captcha_id":"captcha","public_config":{}}`},
		{name: "geetest", value: `{"enabled":true,"provider":"geetest","captcha_id":"captcha","public_config":{"mini_program_captcha_id":"mini"}}`},
		{name: "aliyun", value: `{"enabled":true,"provider":"aliyun","captcha_id":"scene","public_config":{"prefix":"abc","region":"cn"}}`},
		{name: "tencent", value: `{"enabled":true,"provider":"tencent","captcha_id":"123","public_config":{}}`},
		{name: "unknown", value: `{"enabled":true,"provider":"other","captcha_id":"captcha","public_config":{}}`, wantErr: "网易易盾"},
		{name: "aliyun missing prefix", value: `{"enabled":true,"provider":"aliyun","captcha_id":"scene","public_config":{"region":"cn"}}`, wantErr: "prefix"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := registry.Validate("user-server", "captcha", json.RawMessage(test.value))
			if test.wantErr == "" && err != nil {
				t.Fatalf("valid provider configuration rejected: %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
			}
		})
	}
}

func TestCaptchaAndWechatRequiredSecrets(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		key       string
		value     string
		secrets   string
		wantErr   string
	}{
		{name: "yidun", namespace: "user-server", key: "captcha", value: `{"enabled":true,"provider":"yidun","captcha_id":"captcha","public_config":{}}`, secrets: `{"secret_id":"id","secret_key":"key"}`},
		{name: "geetest web", namespace: "user-server", key: "captcha", value: `{"enabled":true,"provider":"geetest","captcha_id":"captcha","public_config":{}}`, secrets: `{"secret_key":"key"}`},
		{name: "geetest mini missing secret", namespace: "user-server", key: "captcha", value: `{"enabled":true,"provider":"geetest","captcha_id":"captcha","public_config":{"mini_program_captcha_id":"mini"}}`, secrets: `{"secret_key":"key"}`, wantErr: "mini_program_secret_key"},
		{name: "aliyun", namespace: "user-server", key: "captcha", value: `{"enabled":true,"provider":"aliyun","captcha_id":"scene","public_config":{"prefix":"abc","region":"cn"}}`, secrets: `{"secret_id":"id","secret_key":"key"}`},
		{name: "tencent missing app key", namespace: "user-server", key: "captcha", value: `{"enabled":true,"provider":"tencent","captcha_id":"123","public_config":{}}`, secrets: `{"secret_id":"id","secret_key":"key"}`, wantErr: "app_secret_key"},
		{name: "wechat app", namespace: "voip-server", key: "wechat.apps", value: `{"default_app_id":"wx1","apps":{"wx1":{"enabled":true,"model_id":"model"}}}`, secrets: `{"apps":{"wx1":{"secret":"secret"}}}`},
		{name: "wechat app missing secret", namespace: "voip-server", key: "wechat.apps", value: `{"default_app_id":"wx1","apps":{"wx1":{"enabled":true,"model_id":"model"}}}`, secrets: `{"apps":{"wx1":{"token":"token"}}}`, wantErr: "AppSecret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRequiredSecrets(test.namespace, test.key, json.RawMessage(test.value), json.RawMessage(test.secrets))
			if test.wantErr == "" && err != nil {
				t.Fatalf("valid secret set rejected: %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
			}
		})
	}
}

func TestWechatAppsMayAllBeDisabled(t *testing.T) {
	registry := DefaultConfigRegistry()
	value := json.RawMessage(`{"default_app_id":"","apps":{"wx1":{"enabled":false,"model_id":"model"}}}`)
	if err := registry.Validate("voip-server", "wechat.apps", value); err != nil {
		t.Fatalf("all-disabled WeChat apps rejected: %v", err)
	}
	invalid := json.RawMessage(`{"default_app_id":"","apps":{"wx1":{"enabled":true,"model_id":"model"}}}`)
	if err := registry.Validate("voip-server", "wechat.apps", invalid); err == nil {
		t.Fatal("enabled WeChat app without a default was accepted")
	}
}
