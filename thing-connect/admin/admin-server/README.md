# ThingConnect Admin Server

`admin-server` 提供 Admin Web、管理员认证与 MFA、RBAC、用户和设备管理、动态配置、数据字典、服务状态、任务中心和审计日志，默认监听 `9000`。

整套系统的环境准备、首次安装、Supervisor、Nginx、数据库迁移、验收、更新和恢复步骤统一见 [ThingConnect 部署指南](../../deployment.md)。本文只说明 Admin Server 自身的使用约束。

## 启动配置

启动配置示例位于 [config.yaml.example](config.yaml.example)，只包含进程启动必需项：

- HTTP 端口和可信代理。
- MySQL 运行账号 DSN。
- Redis 地址、密码和 DB。
- 六个服务共享的 `internal.key`。
- 独立的 `admin.jwt_secret`。
- MFA 数据使用的 `security.config_encryption_key`。
- Admin 任务文件目录。

公开示例中的 `replace-with-*` 和 `your-*` 是不可运行的占位值。生产首次安装应由 Web 安装器生成正式配置和共享密钥，不要手工复制示例密钥。

`security.config_encryption_key` 是 Base64 编码的 32 字节随机值，用于管理员 MFA 因子和旧版配置密钥迁移。不能直接覆盖已有实例的密钥，否则已有加密数据无法读取。

## 管理员初始化

首次 Web 安装会创建首个管理员、默认菜单、权限以及以下角色：

- 超级管理员。
- 运营管理员。
- 技术支持。
- 审计员。

手工配置部署使用 `deploy-prod.sh init-admin`。数据库已有管理员时，该命令拒绝重复初始化。管理员密码至少 8 位，且必须包含英文大写字母、英文小写字母和数字；允许同时使用中文和特殊字符。首次登录后按安全策略修改密码并绑定 TOTP。

新增或修改管理员、调整角色权限、撤销会话和重置他人 MFA 属于高风险操作。启用 MFA 时，提交此类操作需要当前登录管理员自己的 TOTP 验证码或恢复码，不是被编辑管理员的验证码。恢复码验证成功后立即失效。

## 动态配置

Admin 配置中心管理 `device-server`、`user-server`、`voip-server`、`ai-server`、`call-server`、`common` 和 `system` 命名空间。

- 数据库没有发布值时使用后端注册表默认值。
- 不读取业务服务 YAML 中的同名旧业务值。
- TiRTC App ID、Access Key ID 和 Secret Key ID 没有可运行默认值，后台将其标记为必填阻塞项。
- 五个业务服务首次启动必须从 Admin 取得有效配置；Admin 不可达或响应无效时拒绝监听。
- 运行期间 Admin 短暂不可达时，业务服务继续使用内存中的最后有效值并重试。

普通字段和密钥字段都以明文 JSON 存储在 MySQL。Admin Web 对密钥使用密码控件，默认显示 `*`；具备相应权限的管理员可点击眼睛查看原值。数据库账号、备份、网络和审计必须覆盖这些明文凭据。

内置配置使用中文表单。扩展配置时，应同时在后端注册表登记中文名称、说明、默认值、必填/阻塞属性、密钥路径、字段定义和校验规则；否则页面只提供“高级配置”JSON 编辑入口。

## 设备池任务

“业务管理 → 设备管理 → 设备池”支持 CSV 导入。文件要求：

- UTF-8 或带 BOM 的 UTF-8。
- 固定表头和顺序：`device_id,device_key`。
- `device_id`、`device_key` 必填且不超过 64 个字符。
- 单个文件不超过 10 MB、10 万行。
- 每个设备 ID 只能导入一次。

```csv
device_id,device_key
TC-DEVICE-000001,replace-with-device-secret
```

导入后在“运维审计 → 任务中心”查看逐行结果。失败或部分成功的任务可以重试并下载结果 CSV。设备密钥不在设备池列表中展示。

## 健康检查与排障

- `/health/live`：只表示 Admin 进程存活。
- `/health/ready`：检查 MySQL、Redis 和 Admin 数据库迁移版本。

首次安装页面持续显示结构化失败原因和处理建议。依赖预检失败时按页面检查地址、认证、TLS 和来源授权；业务服务启动失败时按页面列出的服务日志、端口或进程管理器建议处理。客户可见响应不包含 SQL、Redis、MQTT 客户端原始错误、内网连接串或凭据，详细原因只进入受保护的服务日志。

- Admin 首页按实例展示五个业务服务的版本、提交号、依赖状态和配置 revision。

常见检查：

- 页面刷新后无法登录：确认 HTTPS 环境使用安全 Cookie，本机 HTTP 开发关闭安全 Cookie。
- 登录后没有导航：确认首个管理员初始化成功，并重启 Admin 重新加载权限。
- 页面显示的客户端 IP 是代理地址：只配置真实代理 IP/CIDR，并确认代理传递 `X-Forwarded-For`。
- 业务服务离线：检查各服务到 Redis、Admin 的网络，以及六服务 `internal.key` 是否一致。
- 动态配置未生效：检查配置是否已经发布、目标实例是否在线及其配置 revision。

接口契约见 [Admin API](API.md)，业务接口见 [API Reference](../../api-reference.md)。
