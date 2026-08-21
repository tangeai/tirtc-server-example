package admin

import "strings"

func defaultConfigFields(namespace, key string) []ConfigFieldDefinition {
	fields := map[string][]ConfigFieldDefinition{
		definitionID("device-server", "device.code_policy"): {
			textConfigField("code_ttl", "设备验证码有效期", "例如 190s 表示 190 秒"),
			textConfigField("rate_limit_window", "单设备限频周期", "例如 190s"),
			numberConfigField("rate_limit_max_hits", "单设备周期内最多申请次数"),
			textConfigField("ip_rate_limit_window", "单 IP 限频周期", "例如 60s"),
			numberConfigField("ip_rate_limit_max_fingerprints", "单 IP 周期内最多设备数"),
			numberConfigField("global_max_pending_codes", "全局待验证数量上限"),
		},
		definitionID("device-server", "device.token_policy"): {
			textConfigField("token_expiry", "设备登录有效期", "例如 168h 表示 7 天"),
		},
		definitionID("device-server", "mqtt.ack_policy"): mqttACKFields(),
		definitionID("user-server", "mqtt.ack_policy"):   mqttACKFields(),
		definitionID("user-server", "smtp"): {
			booleanConfigField("enabled", "启用邮件服务", ""),
			requiredWhenEnabled(textConfigField("host", "SMTP 服务器地址", "")),
			requiredWhenEnabled(numberConfigField("port", "端口")),
			requiredWhenEnabled(selectConfigField("tls_mode", "加密方式", []ConfigFieldOption{
				{Label: "自动选择", Value: "auto"},
				{Label: "直接使用 TLS", Value: "implicit_tls"},
				{Label: "连接后升级为 TLS（STARTTLS）", Value: "starttls"},
			})),
			optionalConfigField(textConfigField("username", "登录账号", "")),
			requiredWhenEnabled(textConfigField("from", "发件人地址", "例如 ThingConnect <noreply@example.com>")),
			requiredWhenEnabled(secretConfigField("password", "登录密码或授权码", "默认隐藏，点击眼睛查看原值")),
		},
		definitionID("user-server", "captcha"): captchaConfigFields(),
		definitionID("user-server", "email.code_ttl"): {
			textConfigField("duration", "验证码有效期", "默认 5m，即 5 分钟"),
		},
		definitionID("user-server", "email.send_rate_limit"): {
			textConfigField("window", "统计周期", "例如 15m"),
			numberConfigField("max_per_email", "单邮箱最多发送次数"),
			numberConfigField("max_per_ip", "单 IP 最多发送次数"),
		},
		definitionID("user-server", "user.default_bind_quota"): {
			numberConfigField("quota", "每位新用户可绑定设备数"),
		},
		definitionID("user-server", "user.token_policy"): {
			textConfigField("token_expiry", "用户登录有效期", "例如 168h 表示 7 天"),
		},
		definitionID("ai-server", "ai.role_policy"): {
			textConfigField("default_role_id", "默认 AI 角色 ID", ""),
			textConfigField("base_url", "AI 服务地址", ""),
			textConfigField("base_role_url", "AI 角色服务地址", ""),
		},
		definitionID("ai-server", "ai.resource_policy"): {
			numberConfigField("resource_quota.mcp", "每台设备 MCP 资源上限"),
			numberConfigField("resource_quota.device_plugin", "每台设备插件上限"),
			numberConfigField("resource_quota.kb", "每台设备知识库上限"),
			resourceRefsConfigField("default_resources.mcp", "默认 MCP 资源", "每项同时填写云端资源 ID 和后台展示名称"),
			resourceRefsConfigField("default_resources.device_plugin", "默认设备插件", "每项同时填写云端资源 ID 和后台展示名称"),
			resourceRefsConfigField("default_resources.kb", "默认知识库", "每项同时填写云端资源 ID 和后台展示名称"),
		},
		definitionID("call-server", "call.contact_policy"): {
			numberConfigField("max_contacts_per_device", "每台设备最多联系人数量"),
		},
		definitionID("call-server", "call.room_policy"): {
			numberConfigField("room_ttl_hours", "呼叫房间保留时间（小时）"),
		},
		definitionID("common", "tirtc"): {
			optionalConfigField(textConfigField("endpoint", "服务地址", "使用默认地址时可留空")),
			blockingConfigField(textConfigField("app_id", "应用 ID", "")),
			blockingConfigField(requiredSecretConfigField("access_key_id", "Access Key ID", "默认隐藏，点击眼睛查看原值")),
			blockingConfigField(requiredSecretConfigField("secret_key_id", "Secret Key ID", "默认隐藏，点击眼睛查看原值")),
		},
		definitionID("system", "mfa.policy"): {
			booleanConfigField("enabled", "启用管理员双重验证", "启用后，未绑定的管理员下次登录时需要绑定身份验证器"),
		},
		definitionID("system", "admin.session_policy"): {
			textConfigField("access_ttl", "访问凭证有效期", "例如 15m"),
			textConfigField("refresh_ttl", "免登录续期有效期", "例如 168h 表示 7 天"),
			numberConfigField("max_sessions", "每位管理员最多同时登录设备数"),
			textConfigField("login_window", "密码失败统计周期", "例如 15m"),
			numberConfigField("login_max_attempts", "周期内最多密码失败次数"),
			textConfigField("mfa_window", "双重验证失败统计周期", "例如 5m"),
			numberConfigField("mfa_max_attempts", "周期内最多验证失败次数"),
		},
	}
	return append([]ConfigFieldDefinition(nil), fields[definitionID(namespace, key)]...)
}

func mqttACKFields() []ConfigFieldDefinition {
	return []ConfigFieldDefinition{textConfigField("timeout", "消息确认超时时间", "例如 5s")}
}

func captchaConfigFields() []ConfigFieldDefinition {
	return []ConfigFieldDefinition{
		booleanConfigField("enabled", "启用人机验证", ""),
		requiredWhenEnabled(selectConfigField("provider", "验证服务商", captchaProviderOptions())),
		providerConfigField(requiredWhenEnabled(textConfigField("captcha_id", "CaptchaID", "网易易盾控制台中的验证码 ID")), "yidun"),
		providerConfigField(requiredWhenEnabled(secretConfigField("secret_id", "Secret ID", "默认隐藏，点击眼睛查看原值")), "yidun"),
		providerConfigField(requiredWhenEnabled(secretConfigField("secret_key", "Secret Key", "默认隐藏，点击眼睛查看原值")), "yidun"),
		providerConfigField(requiredWhenEnabled(textConfigField("captcha_id", "Web 验证码 ID（Captcha ID）", "")), "geetest"),
		providerConfigField(requiredWhenEnabled(secretConfigField("secret_key", "Web 验证码密钥（Captcha Key）", "默认隐藏，点击眼睛查看原值")), "geetest"),
		providerConfigField(optionalConfigField(textConfigField("public_config.mini_program_captcha_id", "小程序验证码 ID", "不使用极验小程序验证时留空")), "geetest"),
		providerConfigField(optionalConfigField(secretConfigField("mini_program_secret_key", "小程序验证码密钥", "填写小程序验证码 ID 时必填")), "geetest"),
		providerConfigField(requiredWhenEnabled(textConfigField("captcha_id", "场景 ID（SceneId）", "")), "aliyun"),
		providerConfigField(requiredWhenEnabled(textConfigField("public_config.prefix", "身份标（Prefix）", "")), "aliyun"),
		providerConfigField(requiredWhenEnabled(selectConfigField("public_config.region", "服务地域", []ConfigFieldOption{{Label: "中国站", Value: "cn"}, {Label: "国际站", Value: "sgp"}})), "aliyun"),
		providerConfigField(requiredWhenEnabled(secretConfigField("secret_id", "AccessKey ID", "默认隐藏，点击眼睛查看原值")), "aliyun"),
		providerConfigField(requiredWhenEnabled(secretConfigField("secret_key", "AccessKey Secret", "默认隐藏，点击眼睛查看原值")), "aliyun"),
		providerConfigField(requiredWhenEnabled(textConfigField("captcha_id", "验证码应用 ID（CaptchaAppId）", "")), "tencent"),
		providerConfigField(requiredWhenEnabled(secretConfigField("secret_id", "腾讯云 SecretId", "默认隐藏，点击眼睛查看原值")), "tencent"),
		providerConfigField(requiredWhenEnabled(secretConfigField("secret_key", "腾讯云 SecretKey", "默认隐藏，点击眼睛查看原值")), "tencent"),
		providerConfigField(requiredWhenEnabled(secretConfigField("app_secret_key", "验证码应用密钥（AppSecretKey）", "默认隐藏，点击眼睛查看原值")), "tencent"),
		providerConfigField(optionalConfigField(textConfigField("public_config.mini_program_captcha_id", "小程序验证码 AppID", "不使用腾讯云小程序验证时留空")), "tencent"),
		providerConfigField(optionalConfigField(secretConfigField("mini_program_secret_key", "小程序验证码 AppSecretKey", "填写小程序验证码 AppID 时必填")), "tencent"),
	}
}

func textConfigField(path, label, description string) ConfigFieldDefinition {
	return ConfigFieldDefinition{Path: strings.Split(path, "."), Label: label, Description: description, Kind: "text", Required: true}
}

func booleanConfigField(path, label, description string) ConfigFieldDefinition {
	field := textConfigField(path, label, description)
	field.Kind = "boolean"
	return field
}

func numberConfigField(path, label string) ConfigFieldDefinition {
	field := textConfigField(path, label, "")
	field.Kind = "number"
	minimum := float64(1)
	field.Min = &minimum
	return field
}

func selectConfigField(path, label string, options []ConfigFieldOption) ConfigFieldDefinition {
	field := textConfigField(path, label, "")
	field.Kind = "select"
	field.Options = options
	return field
}

func resourceRefsConfigField(path, label, description string) ConfigFieldDefinition {
	field := textConfigField(path, label, description)
	field.Kind = "resource_refs"
	field.Required = false
	return field
}

func secretConfigField(path, label, description string) ConfigFieldDefinition {
	field := textConfigField(path, label, description)
	field.Kind = "password"
	field.Secret = true
	field.Required = false
	return field
}

func requiredSecretConfigField(path, label, description string) ConfigFieldDefinition {
	field := secretConfigField(path, label, description)
	field.Required = true
	return field
}

func optionalConfigField(field ConfigFieldDefinition) ConfigFieldDefinition {
	field.Required = false
	return field
}

func requiredWhenEnabled(field ConfigFieldDefinition) ConfigFieldDefinition {
	field.Required = false
	field.RequiredWhenEnabled = true
	return field
}

func blockingConfigField(field ConfigFieldDefinition) ConfigFieldDefinition {
	field.Blocking = true
	return field
}

func providerConfigField(field ConfigFieldDefinition, providers ...string) ConfigFieldDefinition {
	field.Providers = providers
	return field
}
