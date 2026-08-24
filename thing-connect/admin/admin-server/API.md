# Admin API Reference

Admin API 默认前缀为 `/v1/admin`，响应使用统一结构：

```json
{"code":200,"msg":"ok","data":{}}
```

## 首次安装 API

首次安装接口使用独立前缀 `/v1/setup`，只在部署目录具有显式 `first-run.allowed` 或存在未完成安装状态时工作。除状态查询外，请求必须携带启动时生成的一次性令牌：

```http
X-Setup-Token: <one-time-setup-token>
Content-Type: application/json
```

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/setup/status` | 读取 `fresh/recovery/installed/normal` 模式和脱敏进度 |
| POST | `/v1/setup/preview` | 测试 MySQL 和 Redis，并只读生成数据库计划 |
| POST | `/v1/setup/execute` | 携带 `draft` 和 `plan_digest` 执行 Admin 安装或对账本机同一未完成任务 |

`preview` 不执行建库、建表、配置写入或进程控制。首次安装只接受不存在或完全无表的专用数据库。其他已有表的数据库经只读识别后返回 `409`，不会建表、迁移、补数据、清空或覆盖。

只有数据库安装标记、本地 journal 和目标库名中的操作标识全部一致时，才允许恢复本机同一未完成任务。

`draft.database` 必须同时提供迁移账号和独立的 DML 运行账号，两者用户名不能相同。迁移账号只用于初始化不存在或无表的专用库。`preview` 在不选择目标数据库、不执行写入的情况下验证运行账号能否登录；安装阶段等表结构就绪后，再用零行语句验证 DML 权限。

运行账号会写入生成的服务配置。安装器生成五个业务服务的基础配置，但不启动任何业务服务。`draft.optional_services` 和 `draft.mqtt` 只为旧客户端保留解码兼容，服务端会忽略其值。密码字段都是只写输入，不会出现在计划或状态响应中。

`GET /v1/setup/status` 的 `available_services[]` 来自服务清单，包含 `name`、`display_name`、`business`、`required` 和 `uses_mqtt`。页面将业务服务标记为“安装后配置”。

恢复状态中的 `can_resume=true` 表示 Admin 配置和数据库安装状态可以完成对账，不表示业务服务已就绪，也不会触发进程控制。

首次安装、创建管理员和修改管理员密码使用同一密码策略：按 Unicode 字符计数至少 8 位，并同时包含 ASCII 大写字母、小写字母和数字；中文和特殊字符可以作为其余字符使用。

Redis 或 MySQL 连接预检失败时返回 `503`，`msg` 只标识失败依赖和检查方向，不包含上游原始错误、内网地址、账号或密码；脱敏后的详细原因只写入 Admin 服务日志，日志不记录安装请求体。迁移账号连接失败使用诊断码 `MYSQL_UNAVAILABLE`；运行账号登录、来源授权或 DML 权限检查失败使用 `MYSQL_RUNTIME_ACCOUNT_INVALID`。MQTT 由安装完成后的服务器启动预检验证。

安装错误响应的 `data` 提供结构化诊断信息：`code` 是安装器诊断码，`message` 是页面主提示，`suggestions` 是可直接执行的处理建议。客户端仍按响应顶层的数值 `code` 判断 HTTP 业务结果，不按诊断文案分支。

例如：

```json
{
  "code": 503,
  "msg": "MySQL 连接检查失败",
  "data": {
    "code": "MYSQL_UNAVAILABLE",
    "message": "MySQL 连接检查失败",
    "suggestions": [
      "确认 MySQL 地址和端口可从安装服务器访问",
      "检查 TLS 模式、迁移账号密码，以及 MySQL 对安装服务器来源地址的授权"
    ]
  }
}
```

安装状态中的顶层 `problem` 使用相同结构。`services[]` 表示安装器为五个业务服务生成基础配置的进度，不表示它们已启动。原始进程输出和依赖错误不进入状态响应。

所有响应设置 `Cache-Control: no-store`。安装令牌、数据库/Redis 密码、首个管理员密码和生成密钥不出现在状态响应中。安装完成后写接口返回 `410`；重新授权只能在服务器本地执行部署流程，普通配置错误不会重新开放这些接口。

除登录、MFA 验证、刷新和退出外，请求使用 `Authorization: Bearer <access_token>`。刷新令牌保存在 HttpOnly Cookie `admin_refresh` 中；Admin Web 只在页面内存中保存短期访问令牌，页面重新加载时通过刷新 Cookie 恢复会话。

Admin Web 和二次开发客户端发送 `X-Admin-Request: 1`。使用 Cookie 的刷新与退出接口缺少该请求头时会被拒绝，以阻止跨站表单触发会话操作。列表接口通常接受 `page`、`page_size` 和页面对应的筛选参数。

## 认证

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/auth/login` | 邮箱和密码登录；返回完整会话、MFA challenge 或 MFA setup token |
| POST | `/auth/mfa/verify` | 使用 TOTP 或恢复码完成 challenge |
| POST | `/auth/refresh` | 使用刷新 Cookie 换取访问令牌并轮换刷新令牌 |
| POST | `/auth/logout` | 注销当前刷新会话 |
| POST | `/me/mfa/totp/enroll` | 使用一次性 setup token 获取 TOTP 绑定信息 |
| POST | `/me/mfa/totp/confirm` | 确认 TOTP 并生成恢复码 |
| GET | `/me` | 当前管理员信息 |
| PUT | `/me/password` | 修改当前管理员密码 |
| GET | `/me/navigation` | 当前管理员可见菜单 |
| POST | `/me/mfa/recovery-codes/regenerate` | 重新生成恢复码 |

登录请求：

```json
{"email":"admin@example.com","password":"your-password"}
```

MFA 验证请求：

```json
{"mfa_challenge_token":"...","code":"123456"}
```

也可提交 `recovery_code` 替代 `code`。访问令牌默认有效期 15 分钟，刷新令牌默认有效期 7 天，具体值由系统会话策略控制。

## 管理资源

| 资源 | 接口 |
|---|---|
| 服务状态 | `GET /services/status`、`GET /services/:service/status` |
| 用户 | `GET /users`、`GET /users/:id`、`PUT /users/:id/status`、`PUT /users/:id/bind-quota`、`POST /users/:id/password-reset-email` |
| 设备 | `GET /devices`、`GET /device-pool`、`GET /devices/:device_id`、`GET /devices/:device_id/bind-logs`、`POST /devices/:device_id/force-unbind` |
| 管理员 | `GET/POST /admin-users`、`PUT /admin-users/:id`、`PUT /admin-users/:id/roles`、会话撤销和 MFA 重置 |
| 角色与菜单 | `GET/POST/PUT /roles`、角色权限和菜单分配、`GET/POST/PUT /menus` |
| 数据字典 | 字典类型、字典项增改查及 `GET /dictionaries/:code` |
| 微信 VoIP | 应用列表、应用详情、应用设备及设备上报属性 |
| 任务 | 设备池导入、任务列表、任务结果下载和失败任务重试 |
| 日志 | `GET /login-logs`、`GET /audit-logs` |

`GET /services/:service/status` 在实例状态外返回 `configuration_ready`、`required_configurations[]`、`start_command` 和 `restart_command`。`required_configurations[]` 列出未发布或无效的启动阻断项。命令仅用于管理员复制到部署服务器执行，Admin 不执行主机进程控制。

写操作由权限码控制。超级管理员默认拥有 [permissions.go](../../internal/admin/permissions.go) 中列出的全部权限，其他角色按后台配置授权。

`GET /roles` 同时返回 `registered_permissions` 兼容权限码列表和 `permission_definitions` 元数据。元数据包含稳定的 `code`、面向管理员的中文 `name`、`group` 与 `description`；自定义后台可以直接按该元数据分组展示，不需要自行维护权限码文案。

`GET /devices` 和 `GET /devices/:device_id` 的设备对象包含在线状态字段：

```json
{"online":true,"presence_known":true,"last_seen_at":"2026-08-20T12:30:45Z"}
```

`online` 依据 Redis 中 150 秒有效期的 MQTT 在线记录判定；设备心跳或 Broker 连接事件会刷新记录。`presence_known=false` 表示 Admin Server 当时无法读取 Redis，不能按离线处理。兼容升级前值时设备仍可显示在线，但 `last_seen_at` 可能为空；设备产生下一次心跳后会写入最近观测时间。

用户和设备列表的排序规则如下：

| 接口 | 默认排序 | 可选排序 |
|---|---|---|
| `GET /users` | `created_at DESC, id DESC` | `sort_by=created_at`，`sort_order=asc|desc` |
| `GET /devices` | 设备记录 ID 倒序 | `sort_by=active_time|bind_time`，`sort_order=asc|desc` |

所有排序都会追加同方向的 ID，保证结果稳定。注册时间、账号状态与注册时间组合、首次活跃时间和绑定时间均有对应索引。

页码分页使用 `OFFSET`。数据量较大时应限制深翻页，不要通过连续遍历高页码完成大批量导出或无限滚动。

`GET /device-pool` 只返回设备池元数据，不返回 `device_key`。支持 `keyword` 和 `state=available|allocated|released`：`available` 表示从未绑定且可分配，`allocated` 表示已分配，`released` 表示已解绑但按设备身份保留、不会重新分配。返回对象包含 `ever_bound`、当前归属、导入任务编号和导入文件名；初始化或外部写入的数据没有导入任务编号。

`GET /voip/apps/:app_id/devices` 分页返回指定微信小程序的授权设备，支持以下可选查询参数：

- `keyword`：匹配设备 ID、授权设备名称、微信 OpenID、设备型号或所属用户邮箱。
- `auth_status=active|invalid`：按有效或失效状态筛选。
- `profile_reported=true|false`：按设备是否上报 VoIP 属性筛选。

列表默认按授权记录 ID 倒序排列。筛选条件同时作用于 `items` 和 `total`，Admin Web 使用 `page_size=20` 分页查询，不会一次加载全部授权记录。

登录日志和操作日志保留稳定的英文动作码用于筛选与二次开发。`GET /login-logs` 的每条记录包含登录时提交的 `email`；`GET /audit-logs` 的每条记录包含 `admin_user_id` 和当前可查询到的 `email`，账号不存在或已离线清理时 `email` 为空。

## 动态配置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/config-definitions` | 获取配置定义、类型、范围和密钥字段元数据 |
| GET | `/configs` | 按命名空间和范围查询有效配置；未发布项返回注册表默认值 |
| GET | `/configs/:namespace/:config_key` | 读取配置及修订号 |
| POST | `/configs/:namespace/:config_key/validate` | 只验证，不发布 |
| POST | `/configs/:namespace/:config_key/test` | 使用待发布值测试 MQTT 连接认证或发送 SMTP/模板测试邮件，不发布配置 |
| PUT | `/configs/:namespace/:config_key` | 发布配置；需要提交当前修订号以避免并发覆盖，可测试配置会在写入前自动复检 |

配置定义中的 `name`、`group`、`description`、`default`、`required`、`blocking`、`test_kind`、`reload`、`secret_paths` 和 `fields` 用于生成管理表单。

- `required` 表示必须发布可运行值。
- `blocking` 表示缺失配置会阻塞定义中说明的业务能力。
- 非空 `test_kind` 表示表单提供对应类型的在线测试，发布接口也会自动执行同一测试；目前注册的类型为 `mqtt`。
- `reload=restart` 表示发布后需要重启目标服务，其他配置按服务实现热加载。

`fields` 是后端注册表提供的字段事实源。每项包含字段路径 `path`、中文名称 `label`、控件类型 `kind`，还可以包含 `description`、`options`、`secret`、`providers`、`required`、`required_when_enabled`、`blocking` 和 `min`。

`resource_refs` 控件编辑 `[{"id":"...","name":"..."}]` 结构，用于 AI 默认 MCP、设备插件和知识库资源。Admin Web 根据这些元数据生成输入控件和基础校验；只有使用自定义编辑器的复杂配置才维护独立页面。

User、VoIP、Call 的 `mqtt.connection` 支持连接测试：

- 测试使用请求中的 `value` 和可选 `secrets`；没有提交 `secrets` 时，沿用已发布密码。
- 测试只建立临时 MQTT 连接，不订阅、不发布消息、不写 Redis，也不发布配置项。
- 固定 ClientID 模式仍使用相同账号认证，但测试连接改用临时 ClientID，避免断开在线业务服务。
- 正式 ClientID 由服务器启动预检确认。

连接、TLS 或认证失败时返回 `503` 和可操作的中文提示。Broker 客户端原始错误及内网地址只写入受保护日志。

配置响应中的 `using_default=true` 表示数据库尚无发布记录，`value` 是当前有效的注册表默认值，`revision` 为 `0`。配置密钥以明文 JSON 存储。具有 `config.secret.write` 的管理员读取普通配置、具有 `voip.app.write` 的管理员读取微信应用配置时，响应额外包含 `secrets` 原值；其他管理员只看到 `secret_configured`。Admin Web 用默认隐藏且可点击眼睛切换的密码控件展示这些值。

系统只定义 `scope_type=global`。管理接口和内部读取接口都会拒绝 `instance` 等未注册范围。增加新的配置范围前，需要先在配置注册表定义其语义和生效目标。

`user-server/captcha` 的 `provider` 在 `yidun`、`geetest`、`aliyun` 和 `tencent` 之间切换。Provider 发生变化时，写请求必须同时提交新供应商要求的完整 `secrets`；服务端不会合并或复用旧供应商的密钥。Provider 不变时，留空的密钥字段继续保留原值。

内部读取接口为：

```http
GET /v1/internal/configs/:namespace/:config_key?scope_type=global&scope_id=
X-Internal-Key: <shared-internal-key>
```

该接口只允许服务间网络访问，响应包含有效 `value`、明文 `secrets` 和 `revision`，并设置 `Cache-Control: no-store`。数据库没有发布记录时返回注册表默认值和 `revision=0`。它不属于公网 Admin API。

## 兼容与安全约束

- 业务用户 JWT 与管理员 JWT 相互独立。
- 更新普通配置不改变现有用户会话。
- 修改管理员昵称、备注或角色显示名称不撤销登录会话；密码、邮箱、账号状态、角色成员、角色继承和权限变化会撤销受影响管理员会话。
- 更新 `jwt_secret` 会使所有使用该业务密钥的现有令牌失效。
- 密钥原值只向具有对应密钥修改权限的管理员和持有 `internal.key` 的内部服务返回；所有相关响应均禁止缓存。
- 管理员密码、MFA、角色、菜单、用户状态和设备解绑等操作写入审计日志。
