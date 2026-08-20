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
			textConfigField("host", "SMTP 服务器地址", ""),
			numberConfigField("port", "端口"),
			selectConfigField("tls_mode", "加密方式", []ConfigFieldOption{
				{Label: "自动选择", Value: "auto"},
				{Label: "直接使用 TLS", Value: "implicit_tls"},
				{Label: "连接后升级为 TLS（STARTTLS）", Value: "starttls"},
			}),
			textConfigField("username", "登录账号", ""),
			textConfigField("from", "发件人地址", "例如 ThingConnect <noreply@example.com>"),
			secretConfigField("password", "登录密码或授权码", "留空表示保留当前密码"),
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
			tagsConfigField("default_resources.mcp", "默认 MCP 资源 ID", "输入一个 ID 后按回车，可添加多个"),
			tagsConfigField("default_resources.device_plugin", "默认设备插件 ID", "输入一个 ID 后按回车，可添加多个"),
			tagsConfigField("default_resources.kb", "默认知识库 ID", "输入一个 ID 后按回车，可添加多个"),
		},
		definitionID("call-server", "call.contact_policy"): {
			numberConfigField("max_contacts_per_device", "每台设备最多联系人数量"),
		},
		definitionID("call-server", "call.room_policy"): {
			numberConfigField("room_ttl_hours", "呼叫房间保留时间（小时）"),
		},
		definitionID("common", "tirtc"): {
			textConfigField("endpoint", "服务地址", "使用默认地址时可留空"),
			textConfigField("app_id", "应用 ID", ""),
			secretConfigField("access_key_id", "Access Key ID", "留空保留原值"),
			secretConfigField("secret_key_id", "Secret Key ID", "留空保留原值"),
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
		selectConfigField("provider", "验证服务商", captchaProviderOptions()),
		providerConfigField(textConfigField("captcha_id", "CaptchaID", "网易易盾控制台中的验证码 ID"), "yidun"),
		providerConfigField(secretConfigField("secret_id", "Secret ID", "留空保留当前值"), "yidun"),
		providerConfigField(secretConfigField("secret_key", "Secret Key", "留空保留当前值"), "yidun"),
		providerConfigField(textConfigField("captcha_id", "Web 验证码 ID（Captcha ID）", ""), "geetest"),
		providerConfigField(secretConfigField("secret_key", "Web 验证码密钥（Captcha Key）", "留空保留当前值"), "geetest"),
		providerConfigField(textConfigField("public_config.mini_program_captcha_id", "小程序验证码 ID", "不使用极验小程序验证时留空"), "geetest"),
		providerConfigField(secretConfigField("mini_program_secret_key", "小程序验证码密钥", "填写小程序验证码 ID 时必填；留空保留当前值"), "geetest"),
		providerConfigField(textConfigField("captcha_id", "场景 ID（SceneId）", ""), "aliyun"),
		providerConfigField(textConfigField("public_config.prefix", "身份标（Prefix）", ""), "aliyun"),
		providerConfigField(selectConfigField("public_config.region", "服务地域", []ConfigFieldOption{{Label: "中国站", Value: "cn"}, {Label: "国际站", Value: "sgp"}}), "aliyun"),
		providerConfigField(secretConfigField("secret_id", "AccessKey ID", "留空保留当前值"), "aliyun"),
		providerConfigField(secretConfigField("secret_key", "AccessKey Secret", "留空保留当前值"), "aliyun"),
		providerConfigField(textConfigField("captcha_id", "验证码应用 ID（CaptchaAppId）", ""), "tencent"),
		providerConfigField(secretConfigField("secret_id", "腾讯云 SecretId", "留空保留当前值"), "tencent"),
		providerConfigField(secretConfigField("secret_key", "腾讯云 SecretKey", "留空保留当前值"), "tencent"),
		providerConfigField(secretConfigField("app_secret_key", "验证码应用密钥（AppSecretKey）", "留空保留当前值"), "tencent"),
		providerConfigField(textConfigField("public_config.mini_program_captcha_id", "小程序验证码 AppID", "不使用腾讯云小程序验证时留空"), "tencent"),
		providerConfigField(secretConfigField("mini_program_secret_key", "小程序验证码 AppSecretKey", "填写小程序验证码 AppID 时必填；留空保留当前值"), "tencent"),
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

func tagsConfigField(path, label, description string) ConfigFieldDefinition {
	field := textConfigField(path, label, description)
	field.Kind = "tags"
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

func providerConfigField(field ConfigFieldDefinition, providers ...string) ConfigFieldDefinition {
	field.Providers = providers
	field.Required = false
	return field
}
