package admin

type menuSeed struct {
	Code       string
	Name       string
	Path       string
	Permission string
	Sort       int
	ParentCode string
	MenuType   int8
}

type PermissionDefinition struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Group       string `json:"group"`
	Description string `json:"description"`
}

type roleSeed struct {
	Code        string
	Name        string
	Sort        int
	Remark      string
	Permissions []string
	Menus       []string
}

var PermissionDefinitions = []PermissionDefinition{
	{Code: "dashboard.read", Name: "查看数据概览", Group: "数据概览", Description: "查看业务统计和最近管理操作"},
	{Code: "service.status.read", Name: "查看服务状态", Group: "数据概览", Description: "查看五个业务服务及其实例状态"},
	{Code: "user.read", Name: "查看用户", Group: "用户管理", Description: "查询用户列表和详情"},
	{Code: "user.status.write", Name: "修改用户状态", Group: "用户管理", Description: "启用或禁用用户账号"},
	{Code: "user.quota.write", Name: "修改绑定额度", Group: "用户管理", Description: "调整用户可绑定设备数量"},
	{Code: "user.password_reset", Name: "发送重置密码邮件", Group: "用户管理", Description: "为指定用户触发找回密码邮件"},
	{Code: "device.read", Name: "查看设备", Group: "设备管理", Description: "查看设备、绑定信息和上报属性"},
	{Code: "device.unbind", Name: "强制解绑设备", Group: "设备管理", Description: "管理员强制解除用户与设备的绑定"},
	{Code: "device.import", Name: "导入设备池", Group: "设备管理", Description: "批量导入设备 ID 和设备密钥"},
	{Code: "config.read", Name: "查看业务配置", Group: "配置管理", Description: "查看通用配置、系统配置和各服务配置"},
	{Code: "config.write", Name: "修改业务配置", Group: "配置管理", Description: "校验、测试和发布非密钥配置"},
	{Code: "config.secret.write", Name: "修改配置密钥", Group: "配置管理", Description: "写入 SMTP、验证码、TiRTC 等敏感配置"},
	{Code: "voip.app.read", Name: "查看微信 VoIP 应用", Group: "微信 VoIP", Description: "查看小程序、设备和上报属性"},
	{Code: "voip.app.write", Name: "修改微信 VoIP 应用", Group: "微信 VoIP", Description: "维护小程序及其密钥配置"},
	{Code: "voip.profile.read", Name: "查看 VoIP 设备属性", Group: "微信 VoIP", Description: "查看设备上报的音视频能力"},
	{Code: "admin.read", Name: "查看管理员", Group: "管理员与安全", Description: "查看管理员账号、角色和会话"},
	{Code: "admin.write", Name: "修改管理员", Group: "管理员与安全", Description: "新增或修改管理员及其角色"},
	{Code: "admin.session.revoke", Name: "撤销管理员会话", Group: "管理员与安全", Description: "使指定管理员的现有会话失效"},
	{Code: "security.mfa.write", Name: "重置管理员双重验证", Group: "管理员与安全", Description: "清除管理员的双重验证信息并要求重新绑定身份验证器"},
	{Code: "role.read", Name: "查看角色与菜单", Group: "权限与菜单", Description: "查看角色、权限和菜单配置"},
	{Code: "role.manage", Name: "管理角色权限", Group: "权限与菜单", Description: "新增角色并配置继承和权限"},
	{Code: "menu.manage", Name: "管理后台菜单", Group: "权限与菜单", Description: "维护菜单注册表和角色菜单授权"},
	{Code: "dictionary.read", Name: "查看数据字典", Group: "数据字典", Description: "查看字典类型和启用的字典项"},
	{Code: "dictionary.write", Name: "修改数据字典", Group: "数据字典", Description: "新增或修改字典类型和字典项"},
	{Code: "job.read", Name: "查看任务", Group: "任务与审计", Description: "查看导入任务、结果和相关队列"},
	{Code: "job.retry", Name: "重试失败任务", Group: "任务与审计", Description: "重新执行失败或部分成功的任务"},
	{Code: "audit.read", Name: "查看操作日志", Group: "任务与审计", Description: "查询管理员写操作审计记录"},
	{Code: "login_log.read", Name: "查看登录日志", Group: "任务与审计", Description: "查询管理员登录和双重验证结果"},
}

var AllPermissions = permissionCodes(PermissionDefinitions)

func permissionCodes(definitions []PermissionDefinition) []string {
	codes := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		codes = append(codes, definition.Code)
	}
	return codes
}

var defaultMenus = []menuSeed{
	{Code: "overview", Name: "数据概览", Path: "/overview", Permission: "dashboard.read", Sort: 10, MenuType: 2},
	{Code: "business", Name: "业务管理", Sort: 20, MenuType: 1},
	{Code: "service-config", Name: "服务配置", Sort: 30, MenuType: 1},
	{Code: "system-management", Name: "系统管理", Sort: 40, MenuType: 1},
	{Code: "operations", Name: "运维审计", Sort: 50, MenuType: 1},
	{Code: "users", Name: "用户管理", Path: "/users", Permission: "user.read", Sort: 10, ParentCode: "business", MenuType: 2},
	{Code: "devices", Name: "设备管理", Path: "/devices", Permission: "device.read", Sort: 20, ParentCode: "business", MenuType: 2},
	{Code: "device-config", Name: "设备服务", Path: "/configs/device-server", Permission: "config.read", Sort: 10, ParentCode: "service-config", MenuType: 2},
	{Code: "user-config", Name: "用户服务", Path: "/configs/user-server", Permission: "config.read", Sort: 20, ParentCode: "service-config", MenuType: 2},
	{Code: "voip-config", Name: "VoIP 服务", Path: "/configs/voip-server", Permission: "voip.app.read", Sort: 30, ParentCode: "service-config", MenuType: 2},
	{Code: "ai-config", Name: "AI 服务", Path: "/configs/ai-server", Permission: "config.read", Sort: 40, ParentCode: "service-config", MenuType: 2},
	{Code: "call-config", Name: "呼叫服务", Path: "/configs/call-server", Permission: "config.read", Sort: 50, ParentCode: "service-config", MenuType: 2},
	{Code: "common-config", Name: "通用配置", Path: "/configs/common", Permission: "config.read", Sort: 60, ParentCode: "service-config", MenuType: 2},
	{Code: "admin-users", Name: "管理员", Path: "/admin-users", Permission: "admin.read", Sort: 10, ParentCode: "system-management", MenuType: 2},
	{Code: "access", Name: "权限与菜单", Path: "/access", Permission: "role.read", Sort: 20, ParentCode: "system-management", MenuType: 2},
	{Code: "dictionaries", Name: "数据字典", Path: "/dictionaries", Permission: "dictionary.read", Sort: 30, ParentCode: "system-management", MenuType: 2},
	{Code: "system-config", Name: "系统配置", Path: "/configs/system", Permission: "config.read", Sort: 40, ParentCode: "system-management", MenuType: 2},
	{Code: "jobs", Name: "任务中心", Path: "/jobs", Permission: "job.read", Sort: 10, ParentCode: "operations", MenuType: 2},
	{Code: "login-logs", Name: "登录日志", Path: "/login-logs", Permission: "login_log.read", Sort: 20, ParentCode: "operations", MenuType: 2},
	{Code: "audit-logs", Name: "操作日志", Path: "/audit-logs", Permission: "audit.read", Sort: 30, ParentCode: "operations", MenuType: 2},
}

var defaultRoles = []roleSeed{
	{Code: "super_admin", Name: "超级管理员", Sort: 1, Remark: "拥有全部管理权限；仅授予系统负责人", Permissions: AllPermissions},
	{
		Code: "operations_admin", Name: "运营管理员", Sort: 10, Remark: "管理用户、设备、导入任务并查看审计记录",
		Permissions: []string{"dashboard.read", "service.status.read", "user.read", "user.status.write", "user.quota.write", "user.password_reset", "device.read", "device.unbind", "device.import", "job.read", "job.retry", "audit.read", "login_log.read"},
		Menus:       []string{"overview", "users", "devices", "jobs", "login-logs", "audit-logs"},
	},
	{
		Code: "technical_support", Name: "技术支持", Sort: 20, Remark: "只读查询用户、设备、绑定历史和任务状态",
		Permissions: []string{"dashboard.read", "service.status.read", "user.read", "device.read", "job.read"},
		Menus:       []string{"overview", "users", "devices", "jobs"},
	},
	{
		Code: "auditor", Name: "审计员", Sort: 30, Remark: "只读查看业务统计、登录日志和操作日志",
		Permissions: []string{"dashboard.read", "service.status.read", "audit.read", "login_log.read"},
		Menus:       []string{"overview", "login-logs", "audit-logs"},
	},
}
