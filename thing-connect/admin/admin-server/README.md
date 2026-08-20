# ThingConnect 完整部署指南

`admin-server` 是 ThingConnect 的管理入口，同时提供 Admin Web、管理员认证与 MFA、RBAC、用户和设备管理、动态配置、数据字典、服务状态、任务中心及审计日志。本指南说明六个服务和两个 Web 前端的完整自托管方式。

## 系统组成

| 组件 | 默认端口 | 作用 |
|---|---:|---|
| device-server | 9001 | 设备上报、分配和设备令牌 |
| user-server | 9002 | 用户、绑定、邮件验证码和 H5 页面 |
| voip-server | 9003 | 微信 VoIP 应用、授权和回调 |
| ai-server | 9004 | AI 对讲和智能体资源 |
| call-server | 9005 | 设备间联系人和通话房间 |
| admin-server | 9010 | 管理 API、Admin Web 和动态配置中心 |

六个进程共用 MySQL 和 Redis。需要 MQTT 的服务连接同一个 Broker。五个业务服务使用相同的 `jwt_secret`；六个服务使用相同的 `internal.key`。管理员令牌使用独立的 `admin.jwt_secret`。

## 运行环境

- Linux x86-64 或 arm64
- Go 1.21 或更高版本
- Node.js 20.19+ 或 22.12+，npm 10+
- MySQL 8.0+
- Redis 7+
- 支持 MQTT 3.1.1 的 Broker；公网部署使用 TLS
- Nginx 和 Supervisor，或提供等价能力的反向代理与进程管理器

生产环境应为数据库、Redis、MQTT 和 HTTPS 配置访问控制、备份与监控。应用进程不需要以 root 用户运行。

## 获取与构建

```bash
git clone https://github.com/tangeai/tirtc-server-example.git
cd tirtc-server-example/thing-connect
chmod +x build.sh
./build.sh
```

构建产物位于 `bin/`。脚本同时构建 user-server CSS 和 `admin/admin-web/dist/`，并把 Git 版本及提交号写入服务状态信息。只构建部分服务时可传入名称，例如：

```bash
./build.sh user-server admin-server
```

## 初始化数据库

使用具有建库和 DDL 权限的账号执行：

```sql
CREATE DATABASE thing_connect CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'thing_connect'@'%' IDENTIFIED BY 'replace-with-database-password';
GRANT SELECT, INSERT, UPDATE, DELETE ON thing_connect.* TO 'thing_connect'@'%';
```

随后用迁移账号导入完整结构：

```bash
mysql -u root -p thing_connect < scripts/schema.sql
```

`schema_migrations` 记录业务结构和 Admin 结构的版本。全新安装导入 `schema.sql` 后，六个服务可使用仅具备 DML 权限的应用账号。升级时可准备一份临时配置，把 `database.dsn` 改为迁移账号后执行 `admin-server -c migration-config.yaml -migrate-only`，再恢复应用配置并滚动重启；服务不会忽略 DDL 权限错误。

## 安装目录

以下示例使用 `/opt/thing-connect`：

```text
/opt/thing-connect/
├── device-server/{device-server,config.yaml}
├── user-server/{user-server,config.yaml,static/}
├── voip-server/{voip-server,config.yaml}
├── ai-server/{ai-server,config.yaml,static/}
├── call-server/{call-server,config.yaml}
├── admin-server/{admin-server,config.yaml,static/,var/}
└── logs/
```

创建目录并安装二进制及静态文件：

```bash
sudo install -d /opt/thing-connect/{device-server,user-server,voip-server,ai-server,call-server,admin-server,logs}
for service in device-server user-server voip-server ai-server call-server admin-server; do
  sudo install -m 0755 "bin/$service" "/opt/thing-connect/$service/$service"
done
sudo cp -a user-server/static /opt/thing-connect/user-server/
sudo cp -a ai-server/static /opt/thing-connect/ai-server/
sudo cp -a admin/admin-web/dist /opt/thing-connect/admin-server/static
```

将六份示例配置复制为 `config.yaml`：

```bash
for service in device-server user-server voip-server ai-server call-server; do
  sudo cp "$service/config.yaml.example" "/opt/thing-connect/$service/config.yaml"
done
sudo cp admin/admin-server/config.yaml.example /opt/thing-connect/admin-server/config.yaml
sudo chmod 600 /opt/thing-connect/*/config.yaml
```

## 密钥与配置

生成三个独立的随机密钥和一个配置加密密钥：

```bash
openssl rand -hex 32     # 五个业务服务共用的 jwt_secret
openssl rand -hex 32     # 六个服务共用的 internal.key
openssl rand -hex 32     # admin.jwt_secret，仅 admin-server 使用
openssl rand -base64 32  # security.config_encryption_key，仅 admin-server 使用
```

公开示例中的 `replace-with-*` 和 `your-*` 是不可运行的占位值，服务会拒绝使用这些值启动。各配置至少需要确认：

- 六份 `database.dsn` 指向同一数据库。
- 六份 `redis` 指向同一 Redis。
- 五个业务服务的 `jwt_secret` 完全相同。变更它会使现有用户令牌失效。
- 六份 `internal.key` 完全相同，且仅允许服务间网络访问。
- `admin.jwt_secret` 与业务 JWT 分离。变更它只影响管理员会话。
- `security.config_encryption_key` 是 Base64 编码的 32 字节随机值；数据库只保存配置密文。轮换前应使用未来的密钥轮换工具或 KMS，不可直接覆盖旧密钥。
- HTTPS 部署设置 `admin.cookie_secure: true`；仅本机 HTTP 开发时可设为 `false`。
- Nginx 与服务同机时设置 `trusted_proxies: ["127.0.0.1"]`。不要使用 `0.0.0.0/0`。
- 自托管设备引导在 user-server 中启用 `discovery.enabled`，并把各 URL 配成设备实际可访问的 HTTPS/MQTTS 地址；`GET /services` 由 user-server 提供。

`user-server` 的 `captcha.provider` 留空表示关闭人机验证，可切换为 `yidun`、`geetest`、`aliyun` 或 `tencent`。选择供应商后只填写对应 `providers` 节点的标识和密钥。`smtp.host` 留空时验证码邮件写入日志，仅适用于本地开发；生产环境必须配置真实 SMTP。

注册验证码和找回密码验证码分别由后台的邮件配置管理，缺省有效期均为 5 分钟。邮件模板、SMTP、人机验证、微信 VoIP 应用及各服务业务参数均可在 Admin Web 发布。

## 首次管理员

Admin Server 初始化会执行数据库版本检查，并写入默认菜单、权限以及“超级管理员、运营管理员、技术支持、审计员”四个角色。首个账号使用一次性命令创建并授予超级管理员角色：

```bash
sudo env ADMIN_INIT_PASSWORD='replace-with-one-time-strong-password' \
  /opt/thing-connect/admin-server/admin-server \
  -c /opt/thing-connect/admin-server/config.yaml \
  -init-admin \
  -init-email admin@example.com \
  -init-nick-name 管理员
```

数据库已存在管理员时该命令拒绝重复初始化。首次登录后修改初始密码并按系统策略绑定 TOTP。TOTP 使用标准认证器应用，不依赖外部 MFA 服务。

新增或修改管理员、调整角色权限、撤销会话及重置他人双重验证属于高风险操作。系统启用 MFA 时，保存表单需要填写当前登录管理员自己的 6 位身份验证器验证码，或使用一枚恢复码；不是填写被编辑管理员的验证码。恢复码验证成功后会被消费。

## Supervisor

复制 Supervisor 模板。模板默认对应 `/data/demo-open.tangeai.cn` 和 `demo-open` 服务组；部署到其他目录或服务组时，同步修改模板与 `deploy-prod.sh` 顶部配置：

```bash
sudo cp deploy/supervisor/demo-open.supervisor.conf /etc/supervisor/conf.d/demo-open.supervisor.conf
sudo supervisorctl reread
sudo supervisorctl update
sudo supervisorctl status demo-open:*
```

同一服务运行多个实例时，每个实例使用独立端口和唯一 `SERVICE_INSTANCE_ID`。MQTT 推荐 `username` 认证模式；同一主机上的同类实例必须使用不同的 `SERVICE_INSTANCE_ID`，避免 MQTT ClientID 冲突。MySQL、Redis、Admin 地址必须能从每台服务节点访问。

## Nginx 与访问地址

以 [demo-open.nginx.conf](../../deploy/nginx/demo-open.nginx.conf) 为基础设置域名、证书和各服务地址，然后检查并加载：

```bash
sudo nginx -t
sudo systemctl reload nginx
```

公网入口为：

- 用户端：`https://your-domain.example.com/`
- 管理后台：`https://your-domain.example.com/admin/`
- 管理 API：`https://your-domain.example.com/v1/admin/`

`/v1/internal/*` 不应通过公网 Nginx 暴露。五个业务服务通过各自配置中的 `admin.server_url` 直连 Admin Server 获取动态配置。

## 动态配置规则

服务启动时先读取本机 `config.yaml`。某个配置项在数据库中尚未发布时，YAML 值继续生效；Admin 页面中的定义默认值不是当前 YAML 值。管理员首次发布后，该项由数据库接管并通过内部接口及 Redis 事件同步到各实例。

配置命名空间包括 `device-server`、`user-server`、`voip-server`、`ai-server`、`call-server`、`common` 和 `system`。业务配置与系统配置分开授权。密钥字段在 API 中只返回是否已配置和掩码，不返回明文。

Admin Web 的内置配置使用中文业务表单，管理员无需手写 JSON。服务名、权限、角色、日志动作、任务类型、设备来源、VoIP 状态和设备上报属性均以中文名称为主；权限码、配置键和属性键作为二次开发标识保留在次要位置。新增代码级扩展配置时，应同时登记中文名称、说明和前端字段定义，否则页面只提供标注为“高级配置”的 JSON 编辑入口。

## 导入设备池

“业务管理 → 设备管理”包含“用户设备”和“设备池”两个页签。设备池页展示可分配、已分配和已解绑保留的设备、CSV 导入来源及任务编号，不展示设备密钥。点击“导入设备池”可下载 CSV 模板。文件使用 UTF-8 或带 BOM 的 UTF-8 编码，前两列名称和顺序固定：

```csv
device_id,device_key
TC-DEVICE-000001,replace-with-device-secret
```

`device_id` 是设备唯一标识，`device_key` 是该设备的独立密钥，两者均必填且不超过 64 个字符。每个设备 ID 只能导入一次，示例密钥不能用于实际设备。单个文件不超过 10 MB 和 10 万行。页面在提交前检查编码、表头、文件大小及行数；创建任务后在“运维审计 → 任务中心”查看逐行结果，失败或部分成功的任务可重试并下载结果 CSV。任务使用执行租约和心跳，多实例中只有持有租约的实例提交任务结果；实例异常退出后，过期任务自动重新排队。单行设备写入和成功标记在同一事务中完成。

## 健康检查与验收

```bash
for port in 9001 9002 9003 9004 9005 9010; do
  curl -fsS "http://127.0.0.1:$port/health/live"
  curl -fsS "http://127.0.0.1:$port/health/ready"
done
```

业务服务的就绪状态包含其必需的 MySQL、Redis 和 MQTT 连接；Admin Server 检查 MySQL 与 Redis。Admin 首页按实例显示五个业务服务的节点、版本、提交号、依赖状态和配置版本。

完整验收还包括：用户注册与找回密码邮件、设备上报与绑定、五类服务状态、Admin 登录与 MFA、RBAC 菜单、配置发布、微信 VoIP 应用、数据字典和审计日志。

## 升级与回滚

1. 备份 MySQL、配置文件、Admin 任务文件和当前二进制。
2. 使用迁移账号运行 `admin-server -c migration-config.yaml -migrate-only` 并核对 `schema_migrations`。
3. 运行 `./build.sh`，按服务逐个替换二进制和静态目录。
4. 滚动重启实例并检查 `/health/ready` 与 Admin 服务状态。
5. 不修改业务 `jwt_secret` 时，普通用户无需重新登录；只有密码、账号状态或用户认证版本变化时，对应用户令牌失效。

仓库内的 `scripts/deploy-prod.sh` 提供基于 Supervisor 的构建、发布、备份和失败回滚流程。通过 `DEPLOY_ROOT`、`REPO_URL`、`SUPERVISOR_GROUP` 等环境变量适配部署目录和代码来源。

全流程发布默认读取 `${DEPLOY_ROOT}/admin-server/migration-config.yaml`；当前生产脚本的 `DEPLOY_ROOT` 默认值为 `/data/demo-open.tangeai.cn`，可直接修改脚本顶部配置，也可通过同名环境变量临时覆盖。迁移配置也可通过 `MIGRATION_CONFIG` 指定其他绝对路径。该文件使用完整 Admin 配置格式，但 `database.dsn` 必须属于具备 DDL 权限的迁移账号，文件权限应为 `600`。迁移已由外部系统完成时才设置 `SKIP_MIGRATIONS=1`。脚本使用 `git pull --ff-only` 获取版本，并在每个服务重启后等待 `/health/ready`；等待时间可通过 `HEALTH_WAIT_SECONDS` 调整。

已有五服务部署首次接入 Admin Server 时，先准备六份配置，执行“编译”“仅执行数据库迁移”和“仅发布文件”，再把 Supervisor 配置中的 `admin-server` 加入服务组并执行 `reread`、`update`。Supervisor 能识别六个进程后才执行“全流程”。这样不会要求一个尚未安装的 Admin 进程先通过服务管理器校验。

## 故障排查

- 启动提示占位密钥：按“密钥与配置”生成并替换对应值。
- `schema_migrations` 或 DDL 权限失败：使用迁移账号导入或升级数据库，应用账号只保留 DML 权限。
- Admin 页面可打开但刷新登录失败：HTTPS 环境保持 `cookie_secure: true`；本机 HTTP 开发设为 `false`。
- 管理页面 IP 都是代理地址：只把实际 Nginx 地址加入 `trusted_proxies`，并确认转发 `X-Forwarded-For`。
- 服务状态离线：检查六个进程是否使用同一 Redis，以及节点到 Redis 的网络与认证。
- MQTT 状态异常：检查 Broker TLS、认证模式和 `SERVICE_INSTANCE_ID` 是否唯一。
- 动态配置未生效：确认业务服务的 `admin.server_url`、六份 `internal.key` 和 Redis 均一致。

管理接口契约见 [API.md](API.md)，业务接口见 [api-reference.md](../../api-reference.md)。
