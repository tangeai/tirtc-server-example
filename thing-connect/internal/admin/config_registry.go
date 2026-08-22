package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var validNamespaces = map[string]bool{
	"device-server": true, "user-server": true, "voip-server": true,
	"ai-server": true, "call-server": true, "common": true, "system": true,
}

type ConfigDefinition struct {
	Namespace   string                  `json:"namespace"`
	Key         string                  `json:"config_key"`
	Group       string                  `json:"group"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Default     json.RawMessage         `json:"default"`
	SecretPaths []string                `json:"secret_paths,omitempty"`
	Fields      []ConfigFieldDefinition `json:"fields,omitempty"`
	Required    bool                    `json:"required"`
	Blocking    bool                    `json:"blocking"`
	TestKind    string                  `json:"test_kind,omitempty"`
	Targets     []string                `json:"targets"`
	Reload      string                  `json:"reload"`
	validator   func(json.RawMessage) error
}

type ConfigFieldOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ConfigFieldDefinition is the transport-neutral form schema for one config
// value or secret. Validation remains authoritative on the server; this
// metadata lets Admin Web and downstream extensions render the same registry
// without maintaining a second config-key switch.
type ConfigFieldDefinition struct {
	Path                []string            `json:"path"`
	Label               string              `json:"label"`
	Description         string              `json:"description,omitempty"`
	Kind                string              `json:"kind"`
	Options             []ConfigFieldOption `json:"options,omitempty"`
	Secret              bool                `json:"secret,omitempty"`
	Providers           []string            `json:"providers,omitempty"`
	Required            bool                `json:"required,omitempty"`
	RequiredWhenEnabled bool                `json:"required_when_enabled,omitempty"`
	Blocking            bool                `json:"blocking,omitempty"`
	Min                 *float64            `json:"min,omitempty"`
}

type ConfigRegistry struct{ definitions map[string]ConfigDefinition }

func DefaultConfigRegistry() *ConfigRegistry {
	definitions := []ConfigDefinition{
		def("device-server", "device.code_policy", "device", "设备验证码策略", `{"code_ttl":"190s","rate_limit_window":"190s","rate_limit_max_hits":10,"ip_rate_limit_window":"60s","ip_rate_limit_max_fingerprints":50,"global_max_pending_codes":10000}`, nil, []string{"device-server"}, validateDeviceCodePolicy),
		def("device-server", "device.token_policy", "device", "设备 Token 策略", `{"token_expiry":"168h"}`, nil, []string{"device-server"}, validateDurationFields("token_expiry")),
		def("device-server", "mqtt.ack_policy", "mqtt", "MQTT ACK 策略", `{"timeout":"5s"}`, nil, []string{"device-server"}, validateDurationFields("timeout")),
		def("user-server", "smtp", "email", "SMTP 邮件服务", `{"enabled":false,"host":"","port":465,"tls_mode":"implicit_tls","username":"","from":""}`, []string{"password"}, []string{"user-server"}, validateSMTP),
		def("user-server", "captcha", "captcha", "人机验证", `{"enabled":false,"provider":"yidun","captcha_id":"","public_config":{"mini_program_captcha_id":""}}`, []string{"secret_id", "secret_key", "app_secret_key", "mini_program_secret_key"}, []string{"user-server"}, validateCaptcha),
		def("user-server", "email.template.registration_code", "email_template", "注册验证码邮件", `{"enabled":true,"subject":"{{product_name}} 注册验证码","html_body":"<p>验证码：{{code}}</p><p>{{expires_in_minutes}} 分钟内有效</p>","text_body":"验证码：{{code}}"}`, nil, []string{"user-server"}, validateEmailTemplate),
		def("user-server", "email.template.password_reset_code", "email_template", "找回密码验证码邮件", `{"enabled":true,"subject":"{{product_name}} 找回密码验证码","html_body":"<p>验证码：{{code}}</p><p>{{expires_in_minutes}} 分钟内有效</p>","text_body":"验证码：{{code}}"}`, nil, []string{"user-server"}, validateEmailTemplate),
		def("user-server", "email.code_ttl", "email_policy", "邮件验证码有效期", `{"duration":"5m"}`, nil, []string{"user-server"}, validateDurationFields("duration")),
		def("user-server", "email.send_rate_limit", "email_policy", "邮件发送限频", `{"window":"15m","max_per_email":5,"max_per_ip":20}`, nil, []string{"user-server"}, validateRateLimit),
		def("user-server", "user.default_bind_quota", "user", "新用户默认绑定额度", `{"quota":10}`, nil, []string{"user-server"}, validatePositiveFields("quota")),
		def("user-server", "user.token_policy", "user", "用户 Token 策略", `{"token_expiry":"168h"}`, nil, []string{"user-server"}, validateDurationFields("token_expiry")),
		def("user-server", "mqtt.ack_policy", "mqtt", "绑定 MQTT ACK 策略", `{"timeout":"5s"}`, nil, []string{"user-server"}, validateDurationFields("timeout")),
		testDef(restartDef(blockingDef(def("user-server", "mqtt.connection", "mqtt", "MQTT 连接", `{"broker":"","auth_mode":"username","username":"","client_id":""}`, []string{"password"}, []string{"user-server"}, validateMQTTConnection), "用户服务需要 MQTT 才能完成设备绑定消息交互")), "mqtt"),
		def("voip-server", "wechat.apps", "wechat", "微信小程序", `{"default_app_id":"","apps":{}}`, []string{"apps.*.secret", "apps.*.token", "apps.*.encoding_aes_key"}, []string{"voip-server"}, validateWechatApps),
		testDef(restartDef(blockingDef(def("voip-server", "mqtt.connection", "mqtt", "MQTT 连接", `{"broker":"","auth_mode":"username","username":"","client_id":""}`, []string{"password"}, []string{"voip-server"}, validateMQTTConnection), "VoIP 服务启动前必须配置可用的 MQTT 连接")), "mqtt"),
		def("ai-server", "ai.role_policy", "ai", "AI 角色策略", `{"default_role_id":"fin63bby1og0","base_url":"https://api-tirtc.tange365.com","base_role_url":"https://openapi-cn01.tange365.com"}`, nil, []string{"ai-server"}, validateAIRolePolicy),
		def("ai-server", "ai.resource_policy", "ai", "AI 资源策略", `{"resource_quota":{"mcp":4,"device_plugin":20,"kb":5},"default_resources":{"mcp":[],"device_plugin":[],"kb":[]}}`, nil, []string{"ai-server"}, validateAIResourcePolicy),
		def("call-server", "call.contact_policy", "call", "联系人策略", `{"max_contacts_per_device":200}`, nil, []string{"call-server"}, validatePositiveFields("max_contacts_per_device")),
		def("call-server", "call.room_policy", "call", "房间策略", `{"room_ttl_hours":12}`, nil, []string{"call-server"}, validatePositiveFields("room_ttl_hours")),
		testDef(restartDef(blockingDef(def("call-server", "mqtt.connection", "mqtt", "MQTT 连接", `{"broker":"","auth_mode":"username","username":"","client_id":""}`, []string{"password"}, []string{"call-server"}, validateMQTTConnection), "设备通话服务启动前必须配置可用的 MQTT 连接")), "mqtt"),
		restartDef(blockingDef(def("common", "tirtc", "tirtc", "TiRTC", `{"endpoint":"","app_id":""}`, []string{"access_key_id", "secret_key_id"}, []string{"user-server", "voip-server", "ai-server", "call-server"}, validateTiRTC), "TiRTC 凭证缺失时，登录令牌、呼叫和 AI 能力不可用")),
		def("system", "mfa.policy", "security", "MFA 策略", `{"enabled":true}`, nil, []string{"admin-server"}, validateMFAPolicy),
		def("system", "admin.session_policy", "security", "管理员会话策略", `{"access_ttl":"15m","refresh_ttl":"168h","max_sessions":10,"login_window":"15m","login_max_attempts":5,"mfa_window":"5m","mfa_max_attempts":5}`, nil, []string{"admin-server"}, validateSessionPolicy),
	}
	registry := &ConfigRegistry{definitions: make(map[string]ConfigDefinition, len(definitions))}
	for _, definition := range definitions {
		definition.Fields = defaultConfigFields(definition.Namespace, definition.Key)
		registry.definitions[definitionID(definition.Namespace, definition.Key)] = definition
	}
	return registry
}

func def(namespace, key, group, name, defaultValue string, secrets, targets []string, validator func(json.RawMessage) error) ConfigDefinition {
	return ConfigDefinition{Namespace: namespace, Key: key, Group: group, Name: name, Default: json.RawMessage(defaultValue), SecretPaths: secrets, Targets: targets, Reload: "runtime", validator: validator}
}

func blockingDef(definition ConfigDefinition, description string) ConfigDefinition {
	definition.Required = true
	definition.Blocking = true
	definition.Description = description
	return definition
}

func restartDef(definition ConfigDefinition) ConfigDefinition {
	definition.Reload = "restart"
	return definition
}

func testDef(definition ConfigDefinition, kind string) ConfigDefinition {
	definition.TestKind = kind
	return definition
}

func (r *ConfigRegistry) Lookup(namespace, key string) (ConfigDefinition, bool) {
	definition, ok := r.definitions[definitionID(namespace, key)]
	return definition, ok
}

func (r *ConfigRegistry) List(namespace string) []ConfigDefinition {
	definitions := make([]ConfigDefinition, 0)
	for _, definition := range r.definitions {
		if namespace == "" || definition.Namespace == namespace {
			definitions = append(definitions, definition)
		}
	}
	sort.Slice(definitions, func(i, j int) bool {
		if definitions[i].Namespace != definitions[j].Namespace {
			return definitions[i].Namespace < definitions[j].Namespace
		}
		return definitions[i].Key < definitions[j].Key
	})
	return definitions
}

func (r *ConfigRegistry) BlockingForTarget(target string) []ConfigDefinition {
	definitions := make([]ConfigDefinition, 0)
	for _, definition := range r.definitions {
		if !definition.Blocking {
			continue
		}
		for _, candidate := range definition.Targets {
			if candidate == target {
				definitions = append(definitions, definition)
				break
			}
		}
	}
	sort.Slice(definitions, func(i, j int) bool {
		if definitions[i].Namespace != definitions[j].Namespace {
			return definitions[i].Namespace < definitions[j].Namespace
		}
		return definitions[i].Key < definitions[j].Key
	})
	return definitions
}

func (r *ConfigRegistry) Validate(namespace, key string, value json.RawMessage) error {
	if !validNamespaces[namespace] {
		return fmt.Errorf("未知配置命名空间 %q", namespace)
	}
	definition, ok := r.Lookup(namespace, key)
	if !ok {
		return fmt.Errorf("配置项 %s/%s 未注册", namespace, key)
	}
	if len(value) == 0 || !json.Valid(value) {
		return errors.New("配置值必须是有效的 JSON")
	}
	if definition.validator != nil {
		return definition.validator(value)
	}
	return nil
}

func definitionID(namespace, key string) string { return namespace + "/" + key }

func decodeObject(value json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, errors.New("配置值必须是 JSON 对象")
	}
	return object, nil
}

func stringField(object map[string]any, key string) (string, bool) {
	value, ok := object[key].(string)
	return value, ok
}

func positiveIntField(object map[string]any, key string) (int64, error) {
	number, ok := object[key].(json.Number)
	if !ok {
		return 0, fmt.Errorf("字段 %s 必须是整数", key)
	}
	value, err := number.Int64()
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("字段 %s 必须是大于 0 的整数", key)
	}
	return value, nil
}

func validateDurationFields(fields ...string) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		object, err := decodeObject(raw)
		if err != nil {
			return err
		}
		for _, field := range fields {
			value, ok := stringField(object, field)
			if !ok {
				return fmt.Errorf("字段 %s 必须是时长字符串，例如 5m 或 30s", field)
			}
			duration, err := time.ParseDuration(value)
			if err != nil || duration <= 0 {
				return fmt.Errorf("字段 %s 必须是大于 0 的时长", field)
			}
		}
		return nil
	}
}

func validatePositiveFields(fields ...string) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		object, err := decodeObject(raw)
		if err != nil {
			return err
		}
		for _, field := range fields {
			if _, err := positiveIntField(object, field); err != nil {
				return err
			}
		}
		return nil
	}
}

func validateDeviceCodePolicy(raw json.RawMessage) error {
	if err := validateDurationFields("code_ttl", "rate_limit_window", "ip_rate_limit_window")(raw); err != nil {
		return err
	}
	return validatePositiveFields("rate_limit_max_hits", "ip_rate_limit_max_fingerprints", "global_max_pending_codes")(raw)
}

func validateSMTP(raw json.RawMessage) error {
	object, err := decodeObject(raw)
	if err != nil {
		return err
	}
	enabled, ok := object["enabled"].(bool)
	if !ok {
		return errors.New("启用开关 enabled 必须是 true 或 false")
	}
	if !enabled {
		return nil
	}
	host, _ := stringField(object, "host")
	from, _ := stringField(object, "from")
	if host == "" || strings.ContainsAny(host, "\r\n") || from == "" || strings.ContainsAny(from, "\r\n") {
		return errors.New("启用邮件服务时必须填写有效的 SMTP 服务器地址和发件人地址")
	}
	port, err := positiveIntField(object, "port")
	if err != nil || port > 65535 {
		return errors.New("SMTP 端口必须在 1 到 65535 之间")
	}
	tlsMode, _ := stringField(object, "tls_mode")
	if tlsMode != "auto" && tlsMode != "implicit_tls" && tlsMode != "starttls" {
		return errors.New("SMTP 加密方式必须选择自动、直接 TLS 或 STARTTLS")
	}
	return nil
}

func validateMQTTConnection(raw json.RawMessage) error {
	object, err := decodeObject(raw)
	if err != nil {
		return err
	}
	broker, _ := stringField(object, "broker")
	parsed, err := url.Parse(strings.TrimSpace(broker))
	if err != nil || (parsed.Scheme != "mqtt" && parsed.Scheme != "mqtts") || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("Broker 必须是 mqtt:// 或 mqtts:// 地址，且不能包含账号、路径、查询参数或片段")
	}
	authMode, _ := stringField(object, "auth_mode")
	username, _ := stringField(object, "username")
	clientID, _ := stringField(object, "client_id")
	switch authMode {
	case "username":
		if strings.TrimSpace(username) == "" {
			return errors.New("Username 认证方式必须填写 MQTT 用户名")
		}
		if strings.TrimSpace(clientID) != "" {
			return errors.New("Username 认证方式不能同时填写固定 ClientID")
		}
	case "clientid":
		if strings.TrimSpace(clientID) == "" {
			return errors.New("ClientID 认证方式必须填写固定 ClientID")
		}
		if strings.TrimSpace(username) != "" {
			return errors.New("ClientID 认证方式不能同时填写 Username")
		}
	default:
		return errors.New("MQTT 认证方式必须选择 Username 或 ClientID")
	}
	return nil
}

func validateCaptcha(raw json.RawMessage) error {
	object, err := decodeObject(raw)
	if err != nil {
		return err
	}
	enabled, ok := object["enabled"].(bool)
	if !ok {
		return errors.New("启用开关 enabled 必须是 true 或 false")
	}
	if !enabled {
		return nil
	}
	provider, _ := stringField(object, "provider")
	if _, ok := captchaProviderSpecFor(provider); !ok {
		return fmt.Errorf("启用人机验证时，服务商必须选择%s", captchaProviderLabels())
	}
	captchaID, _ := stringField(object, "captcha_id")
	if captchaID == "" {
		return errors.New("启用人机验证时必须填写验证码场景 ID")
	}
	publicConfig, ok := object["public_config"].(map[string]any)
	if !ok {
		return errors.New("人机验证公开参数 public_config 必须是对象")
	}
	if provider == "aliyun" {
		prefix, _ := publicConfig["prefix"].(string)
		region, _ := publicConfig["region"].(string)
		if strings.TrimSpace(prefix) == "" {
			return errors.New("阿里云人机验证必须填写身份标（public_config.prefix）")
		}
		if region != "cn" && region != "sgp" {
			return errors.New("阿里云人机验证地域必须选择中国站（cn）或国际站（sgp）")
		}
	}
	return nil
}

var templateVariable = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

func validateEmailTemplate(raw json.RawMessage) error {
	object, err := decodeObject(raw)
	if err != nil {
		return err
	}
	if _, ok := object["enabled"].(bool); !ok {
		return errors.New("模板启用开关 enabled 必须是 true 或 false")
	}
	subject, ok := stringField(object, "subject")
	if !ok || subject == "" || len(subject) > 200 || strings.ContainsAny(subject, "\r\n") {
		return errors.New("邮件主题为空、过长或包含非法换行")
	}
	html, _ := stringField(object, "html_body")
	text, _ := stringField(object, "text_body")
	if html == "" || len(html) > 64*1024 || len(text) > 64*1024 {
		return errors.New("邮件正文为空或超过 64 KB")
	}
	allowed := map[string]bool{"code": true, "expires_in_minutes": true, "product_name": true, "support_email": true}
	hasCode := false
	for _, body := range []string{subject, html, text} {
		for _, match := range templateVariable.FindAllStringSubmatch(body, -1) {
			if !allowed[match[1]] {
				return fmt.Errorf("邮件模板变量 %q 不受支持", match[1])
			}
			if match[1] == "code" {
				hasCode = true
			}
		}
	}
	if !hasCode {
		return errors.New("验证码邮件模板必须包含 {{code}} 变量")
	}
	return nil
}

func validateRateLimit(raw json.RawMessage) error {
	if err := validateDurationFields("window")(raw); err != nil {
		return err
	}
	return validatePositiveFields("max_per_email", "max_per_ip")(raw)
}

func validateWechatApps(raw json.RawMessage) error {
	object, err := decodeObject(raw)
	if err != nil {
		return err
	}
	defaultID, _ := stringField(object, "default_app_id")
	apps, ok := object["apps"].(map[string]any)
	if !ok {
		return errors.New("微信应用列表 apps 必须是对象")
	}
	anyEnabled := false
	for appID, value := range apps {
		app, ok := value.(map[string]any)
		if !ok || strings.TrimSpace(appID) == "" {
			return errors.New("每个微信应用必须填写 AppID 和完整配置")
		}
		modelID, _ := app["model_id"].(string)
		if modelID == "" {
			return fmt.Errorf("微信应用 %s 必须填写设备型号 ModelID", appID)
		}
		if enabled, ok := app["enabled"].(bool); !ok {
			return fmt.Errorf("微信应用 %s 的启用开关必须是 true 或 false", appID)
		} else if enabled {
			anyEnabled = true
		}
	}
	if defaultID == "" {
		if anyEnabled {
			return errors.New("存在启用的微信应用时，必须设置一个默认应用")
		}
		return nil
	}
	app, ok := apps[defaultID].(map[string]any)
	if !ok {
		return errors.New("默认微信应用必须指向列表中已有的应用")
	}
	if enabled, _ := app["enabled"].(bool); !enabled {
		return errors.New("默认微信应用必须处于启用状态")
	}
	return nil
}

func validateAIRolePolicy(raw json.RawMessage) error {
	object, err := decodeObject(raw)
	if err != nil {
		return err
	}
	if role, _ := stringField(object, "default_role_id"); role == "" {
		return errors.New("必须填写默认 AI 角色 ID")
	}
	for _, field := range []string{"base_url", "base_role_url"} {
		value, _ := stringField(object, field)
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return fmt.Errorf("字段 %s 必须是不含账号信息的 HTTPS 地址", field)
		}
	}
	return nil
}

func validateAIResourcePolicy(raw json.RawMessage) error {
	object, err := decodeObject(raw)
	if err != nil {
		return err
	}
	quota, ok := object["resource_quota"].(map[string]any)
	if !ok {
		return errors.New("AI 资源上限 resource_quota 必须是对象")
	}
	for _, key := range []string{"mcp", "device_plugin", "kb"} {
		if _, err := positiveIntField(quota, key); err != nil {
			return err
		}
	}
	resources, ok := object["default_resources"].(map[string]any)
	if !ok {
		return errors.New("AI 默认资源 default_resources 必须是对象")
	}
	if len(resources) != 3 {
		return errors.New("AI 默认资源只能包含 mcp、device_plugin 和 kb")
	}
	for _, resourceType := range []string{"mcp", "device_plugin", "kb"} {
		items, ok := resources[resourceType].([]any)
		if !ok {
			return fmt.Errorf("默认资源 %s 必须是数组", resourceType)
		}
		seen := make(map[string]bool, len(items))
		for index, rawItem := range items {
			item, ok := rawItem.(map[string]any)
			if !ok || len(item) != 2 {
				return fmt.Errorf("默认资源 %s 第 %d 项必须只包含 id 和 name", resourceType, index+1)
			}
			id, idOK := item["id"].(string)
			name, nameOK := item["name"].(string)
			id, name = strings.TrimSpace(id), strings.TrimSpace(name)
			if !idOK || !nameOK || id == "" || name == "" {
				return fmt.Errorf("默认资源 %s 第 %d 项必须填写 id 和 name", resourceType, index+1)
			}
			if seen[id] {
				return fmt.Errorf("默认资源 %s 的 id %q 重复", resourceType, id)
			}
			seen[id] = true
		}
	}
	return nil
}

func validateTiRTC(raw json.RawMessage) error {
	object, err := decodeObject(raw)
	if err != nil {
		return err
	}
	if appID, _ := stringField(object, "app_id"); appID == "" {
		return errors.New("必须填写 TiRTC 应用 ID")
	}
	if endpoint, _ := stringField(object, "endpoint"); endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return errors.New("TiRTC 服务地址必须使用 HTTPS")
		}
	}
	return nil
}

func validateMFAPolicy(raw json.RawMessage) error {
	object, err := decodeObject(raw)
	if err != nil {
		return err
	}
	if _, ok := object["enabled"].(bool); !ok {
		return errors.New("双重验证启用开关 enabled 必须是 true 或 false")
	}
	return nil
}

func validateSessionPolicy(raw json.RawMessage) error {
	if err := validateDurationFields("access_ttl", "refresh_ttl", "login_window", "mfa_window")(raw); err != nil {
		return err
	}
	return validatePositiveFields("max_sessions", "login_max_attempts", "mfa_max_attempts")(raw)
}
