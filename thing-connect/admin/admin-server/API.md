# Admin API Reference

Admin API 默认前缀为 `/v1/admin`，响应使用统一结构：

```json
{"code":200,"msg":"ok","data":{}}
```

除登录、MFA 验证、刷新和退出外，请求使用 `Authorization: Bearer <access_token>`。刷新令牌保存在 HttpOnly Cookie `admin_refresh` 中，Admin Web 只在页面内存中保存短期访问令牌，页面重新加载时通过刷新 Cookie 恢复会话。Admin Web 和二次开发客户端发送 `X-Admin-Request: 1`；使用 Cookie 的刷新与退出接口缺少该请求头时拒绝请求，以阻止跨站表单触发会话操作。列表接口通常接受 `page`、`page_size` 和页面对应的筛选参数。

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

写操作由权限码控制。超级管理员默认拥有 [permissions.go](../../internal/admin/permissions.go) 中列出的全部权限，其他角色按后台配置授权。

`GET /roles` 同时返回 `registered_permissions` 兼容权限码列表和 `permission_definitions` 元数据。元数据包含稳定的 `code`、面向管理员的中文 `name`、`group` 与 `description`；自定义后台可以直接按该元数据分组展示，不需要自行维护权限码文案。

`GET /devices` 和 `GET /devices/:device_id` 的设备对象包含在线状态字段：

```json
{"online":true,"presence_known":true,"last_seen_at":"2026-08-20T12:30:45Z"}
```

`online` 依据 Redis 中 150 秒有效期的 MQTT 在线记录判定；设备心跳或 Broker 连接事件会刷新记录。`presence_known=false` 表示 Admin Server 当时无法读取 Redis，不能按离线处理。兼容升级前值时设备仍可显示在线，但 `last_seen_at` 可能为空；设备产生下一次心跳后会写入最近观测时间。

`GET /users` 默认按 `created_at DESC, id DESC` 返回，并接受 `sort_by=created_at` 与 `sort_order=asc|desc`。`GET /devices` 接受 `sort_by=active_time|bind_time` 与 `sort_order=asc|desc`；未指定时按设备记录 ID 倒序。所有排序都追加同方向的 ID 作为稳定次序，注册时间、账号状态与注册时间组合、首次活跃时间和绑定时间均有对应索引。页码分页仍使用 `OFFSET`，大数据量下应限制深翻页；面向千万级数据的连续导出或无限滚动客户端应使用后续提供的游标接口，而不是遍历高页码。

`GET /device-pool` 只返回设备池元数据，不返回 `device_key`。支持 `keyword` 和 `state=available|allocated|released`：`available` 表示从未绑定且可分配，`allocated` 表示已分配，`released` 表示已解绑但按设备身份保留、不会重新分配。返回对象包含 `ever_bound`、当前归属、导入任务编号和导入文件名；初始化或外部写入的数据没有导入任务编号。

登录日志和操作日志保留稳定的英文动作码用于筛选与二次开发。`GET /login-logs` 的每条记录包含登录时提交的 `email`；`GET /audit-logs` 的每条记录包含 `admin_user_id` 和当前可查询到的 `email`，账号不存在或已离线清理时 `email` 为空。

## 动态配置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/config-definitions` | 获取配置定义、类型、范围和密钥字段元数据 |
| GET | `/configs` | 按命名空间和范围查询有效配置；未发布项返回注册表默认值 |
| GET | `/configs/:namespace/:config_key` | 读取配置及修订号 |
| POST | `/configs/:namespace/:config_key/validate` | 只验证，不发布 |
| POST | `/configs/:namespace/:config_key/test` | 对 SMTP、人机验证等支持测试的配置执行连通性测试 |
| PUT | `/configs/:namespace/:config_key` | 发布配置；需要提交当前修订号以避免并发覆盖 |

配置定义中的 `name`、`group`、`description`、`default`、`required`、`blocking`、`secret_paths` 和 `fields` 用于生成管理表单。`required` 表示必须发布可运行值，`blocking` 表示缺失会阻塞定义中说明的业务能力。`fields` 是后端注册表提供的单一字段事实源，每项包含字段路径 `path`、中文名称 `label`、控件类型 `kind`，并可包含 `description`、`options`、`secret`、`providers`、`required`、`required_when_enabled`、`blocking` 和 `min`。`resource_refs` 控件编辑 `[{"id":"...","name":"..."}]` 结构，用于 AI 默认 MCP、设备插件和知识库资源。Admin Web 根据这些元数据生成输入控件和基础校验；只有明确使用自定义编辑器的复杂配置才维护独立页面。

配置响应中的 `using_default=true` 表示数据库尚无发布记录，`value` 是当前有效的注册表默认值，`revision` 为 `0`。配置密钥以明文 JSON 存储。具有 `config.secret.write` 的管理员读取普通配置、具有 `voip.app.write` 的管理员读取微信应用配置时，响应额外包含 `secrets` 原值；其他管理员只看到 `secret_configured`。Admin Web 用默认隐藏且可点击眼睛切换的密码控件展示这些值。

当前仅定义 `scope_type=global`。管理接口和内部读取接口都会拒绝 `instance` 等未注册范围；增加新的配置范围时，需要先在配置注册表定义其语义和生效目标。

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
