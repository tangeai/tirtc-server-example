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

全新 Supervisor 部署可跳过本节的手工建库和配置复制，直接使用下文“首次 Web 安装”。手工部署、受管数据库和独立迁移流水线按本节准备权限。

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

`schema_migrations` 记录业务结构和 Admin 结构的版本。全新安装导入 `schema.sql` 后，所有已安装服务使用仅具备 DML 权限的应用账号。升级使用 `deploy-prod.sh migrate/update`；脚本让迁移程序确认迁移 DSN 与所有已安装服务的运行配置指向相同主机、端口和库名，并在执行 DDL 前拒绝陌生非空库、未来版本及结构漂移。常驻服务只读校验迁移账本，缺少迁移或数据库版本高于当前程序时拒绝就绪，不会使用运行账号尝试 DDL。

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
├── config-releases/<operation-id>/<service>/config.yaml
├── config-current -> config-releases/<operation-id>
├── var/installer/{first-run.allowed,token.sha256,state.json,installed.json}
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

手工配置部署将六份示例配置复制为 `config.yaml`：

```bash
for service in device-server user-server voip-server ai-server call-server; do
  sudo cp "$service/config.yaml.example" "/opt/thing-connect/$service/config.yaml"
done
sudo cp admin/admin-server/config.yaml.example /opt/thing-connect/admin-server/config.yaml
sudo chmod 600 /opt/thing-connect/*/config.yaml
```

首次 Web 安装不复制示例配置。它在一个私有 revision 中生成 Admin、两个必需业务服务及所选可选服务的配置并原子切换 `config-current`；服务目录中没有 `config.yaml` 时读取该 bundle。已有服务目录配置始终优先，解析错误会使服务失败关闭，不会重新开放安装入口或静默改用其他 revision。

## 首次 Web 安装

先按实际目录和组名调整 Supervisor 模板中的 `command`、`-deploy-root` 与 `-supervisor-group`，执行 `reread` 和 `update`。在空部署目录运行：

```bash
DEPLOY_ROOT=/opt/thing-connect \
SUPERVISOR_GROUP=thing-connect \
./scripts/deploy-prod.sh first-install
```

不带命令运行脚本时，也可以选择菜单第 1 项“首次 Web 安装（仅空部署）”。终端显示一次性安装令牌并启动 `admin-server` 的安装模式。安装模式默认只监听 `127.0.0.1:9010`，通过同机 Nginx 的 HTTPS 入口打开 `https://your-domain.example.com/admin/`；没有反向代理时使用 SSH 端口转发，不应把安装端口直接暴露到公网。令牌丢失且配置尚未提交时，重新执行 `first-install` 会轮换令牌；检测到配置 bundle 或安装完成标记后始终拒绝重新开放入口。

向导填写：

- 业务服务：`device-server` 和 `user-server` 固定启用；`voip-server`、`ai-server`、`call-server` 按需选择。未选择的服务不生成配置、不启动，也不参与安装就绪检查。
- MySQL 地址、数据库名、具有建库/DDL 权限的安装账号，以及用户名不同、仅长期使用 DML 权限的运行账号。安装器验证运行账号对 `schema_migrations` 的 SELECT，以及对其余受管表的 SELECT、INSERT、UPDATE、DELETE。
- Redis 地址、密码和 DB。
- MQTT Broker、用户名和密码。安装器使用临时随机 ClientID 预检，六个服务使用固定服务实例后缀。
- 对外访问地址、实际可信代理和 HTTPS Cookie 选项。
- 首个管理员邮箱、昵称和至少 12 位密码。

业务 JWT、六服务 `internal.key`、Admin JWT 和 MFA 加密密钥由密码学安全随机源生成一次，不需要手工复制。TiRTC、SMTP、人机验证、微信应用和 AI 默认资源不是进程启动引导项，在安装完成后进入配置中心填写；后台继续标记无默认值且阻塞相关业务能力的字段。

数据库预检只读取 `INFORMATION_SCHEMA`、ThingConnect 安装标记和迁移账本，不扫描用户或设备数据。处理矩阵如下：

| 数据库状态 | 自动行为 |
|---|---|
| 不存在 | 在 MySQL 安装锁内创建并初始化 |
| 已存在且零表 | 初始化 |
| 未完成的同一 ThingConnect 安装 | 按持久步骤继续 |
| 可信旧版 ThingConnect | 只执行缺失迁移；已有管理员不修改 |
| 已安装实例 | 要求导入该实例的原配置和共享密钥，不生成替代密钥 |
| 陌生非空、结构漂移、未来版本 | 零写入拒绝，转人工诊断 |

配置提交前，安装器保持数据库锁和 `${DEPLOY_ROOT}/deploy.lock`，因此同机发布和并发浏览器提交不能同时执行。配置以 `0600` 写入同文件系统暂存目录，经项目真实配置加载器校验、文件和目录同步后，使用一个 `config-current` 指针激活。进程或主机中断后重新打开页面继续；安装器不提供清库、重建或覆盖已有配置的按钮。

配置提交后安装进程退出一次，由 Supervisor 拉起正常 Admin。Admin readiness 通过后依次启动两个必需业务服务和本次选择的可选服务；单个服务失败时 Admin 保持可用，恢复页显示脱敏状态并允许继续。数据库和本机安装状态都锁定后，一次性令牌及授权标记删除，配置缺失或损坏也不会重新开放安装页面。

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
- `security.config_encryption_key` 是 Base64 编码的 32 字节随机值，用于管理员 MFA 因子加密，并在升级时把旧版配置密钥解密迁移为明文。轮换前应使用未来的密钥轮换工具或 KMS，不可直接覆盖旧密钥。
- HTTPS 部署设置 `admin.cookie_secure: true`；仅本机 HTTP 开发时可设为 `false`。
- Nginx 与服务同机时设置 `trusted_proxies: ["127.0.0.1"]`。不要使用 `0.0.0.0/0`。
- 自托管设备引导在 user-server 中启用 `discovery.enabled`，并把各 URL 配成设备实际可访问的 HTTPS/MQTTS 地址；`GET /services` 由 user-server 提供。

人机验证和 SMTP 默认关闭。在 Admin Web 中启用人机验证时选择 `yidun`、`geetest`、`aliyun` 或 `tencent` 并填写该服务商的必填字段；启用邮件服务时填写 SMTP 地址、发件人和密码。未启用 SMTP 时邮件发送被禁用，不会把验证码写入日志。

注册验证码和找回密码验证码分别由后台的邮件配置管理，缺省有效期均为 5 分钟。邮件模板、SMTP、人机验证、微信 VoIP 应用及各服务业务参数均可在 Admin Web 发布。

## 首次管理员

Admin Server 初始化会执行数据库版本检查，并写入默认菜单、权限以及“超级管理员、运营管理员、技术支持、审计员”四个角色。首个账号使用一次性命令创建并授予超级管理员角色：

首次 Web 安装在向导中完成这一步。以下命令用于手工配置部署。

生产目录使用 `deploy-prod.sh init-admin`，或选择菜单中的“初始化首个管理员”。该流程先严格校验必需服务及已启用可选服务的配置并执行数据库迁移，交互输入时隐藏密码；如果 Admin Server 已在 Supervisor 中运行，初始化后会重启该进程并等待 readiness，使新账号、角色和菜单权限立即进入内存。

非交互发布可通过环境变量传入账号信息，密码只通过子进程环境传递：

```bash
sudo env \
  ADMIN_INIT_EMAIL='admin@example.com' \
  ADMIN_INIT_NICK_NAME='管理员' \
  ADMIN_INIT_PASSWORD='replace-with-one-time-strong-password' \
  /opt/thing-connect/deploy-prod.sh init-admin
```

直接调用二进制时使用：

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

复制 Supervisor 模板。模板默认对应 `/data/demo-open.tangeai.cn` 和 `demo-open` 服务组，安装模式监听 `127.0.0.1:9010`；部署到其他目录、安装监听地址、端口或服务组时，同步修改程序路径、Admin 命令中的 `-deploy-root`/`-setup-bind`/`-setup-port`/`-supervisor-group` 与 `deploy-prod.sh` 顶部配置：

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

本机 `config.yaml` 提供数据库、Redis、MQTT、Admin 地址和进程认证密钥等启动引导参数。注册配置项只使用数据库发布值；数据库中没有记录时，内部接口返回后端注册表默认值并同步到各实例，不读取各服务 YAML 中的同名业务值。五个业务服务启动时必须从 `admin.server_url` 完成首次加载，Admin 不可达或响应无效时不开始监听；运行中的短暂故障继续使用内存中的最后有效值并周期重试。后台把默认值、必填项和缺失后会阻塞相关能力的配置分开标注。TiRTC 应用 ID、Access Key ID 和 Secret Key ID 没有可运行的默认值，是当前的必填阻塞项。

配置命名空间包括 `device-server`、`user-server`、`voip-server`、`ai-server`、`call-server`、`common` 和 `system`。业务配置与系统配置分开授权。配置中心的普通字段和密钥字段均以明文 JSON 存储；Admin Web 使用密码控件隐藏密钥，具备对应密钥写权限的管理员可点击眼睛查看原值。应通过数据库账号、网络、备份和审计控制明文凭证的访问范围。

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
2. 使用 `deploy-prod.sh migrate`，或运行 `admin-server -c migration-config.yaml -deploy-root <部署根目录> -require-runtime-target -migrate-only`，并核对 `schema_migrations`。
3. 运行 `./build.sh`，按服务逐个替换二进制和静态目录。
4. 滚动重启实例并检查 `/health/ready` 与 Admin 服务状态。
5. 不修改业务 `jwt_secret` 时，普通用户无需重新登录；只有密码、账号状态或用户认证版本变化时，对应用户令牌失效。

仓库内的 `scripts/deploy-prod.sh` 提供基于 Supervisor 的首次安装、日常更新、显式迁移、备份和失败回滚流程。通过 `DEPLOY_ROOT`、`REPO_URL`、`SUPERVISOR_GROUP` 等环境变量适配部署目录和代码来源。无参数时显示按生命周期排列的交互菜单；自动化发布使用 `first-install`、`update`、`migrate`、`validate`、`deploy`、`start`、`stop`、`restart` 和 `status` 等单一命令。

`update` 默认读取 `${DEPLOY_ROOT}/admin-server/migration-config.yaml`；当前生产脚本的 `DEPLOY_ROOT` 默认值为 `/data/demo-open.tangeai.cn`，可直接修改脚本顶部配置，也可通过同名环境变量临时覆盖。迁移配置也可通过 `MIGRATION_CONFIG` 指定其他绝对路径。该文件使用完整 Admin 配置格式，但 `database.dsn` 必须属于具备 DDL 权限的迁移账号，文件权限应为 `600`。迁移已由外部系统完成时才设置 `SKIP_MIGRATIONS=1`。脚本使用 `git pull --ff-only` 获取版本；构建先写临时目录，全部目标成功后才替换 `bin/` 并生成当前提交的完整构建标记，发布会拒绝旧提交、缺失服务或构建后源码变脏的产物。更新在迁移前完成文件备份，然后先重启 Admin，再逐个重启业务服务，并在每次重启后等待 `/health/ready`；等待时间可通过 `HEALTH_WAIT_SECONDS` 调整。`validate` 同时执行脚本级跨服务约束和 Admin 程序的严格 YAML/未知字段校验，并兼容首次安装生成的 `config-current`；日常发布不会在 bundle 之上复制示例配置。数据库迁移不会随文件回滚；迁移实际改变 schema 后若文件发布失败，脚本停止整个服务组，避免新旧版本混跑。执行 `update` 或 `migrate` 前必须确认数据库备份可恢复。

`first-install` 只在六份服务配置、`config-current` 和安装完成标记均不存在时运行。升级和 `update` 不会创建 `first-run.allowed`，因此配置解析失败、数据库不可达或 Redis 故障只会导致失败关闭，不会切换为首次安装模式。所有修改代码、文件、数据库或进程的脚本命令使用同一部署锁，菜单等待输入和状态查询不持锁，安装器因此可以在脚本返回后取得该锁。

已有五服务部署首次接入 Admin Server 时，先准备六份配置并执行 `build`、`validate`、`migrate`、`deploy`，然后把 Supervisor 配置中的 `admin-server` 加入服务组并执行 `reread`、`update`。Supervisor 能识别六个进程后执行 `init-admin`；后续版本使用 `update`。这样不会要求一个尚未安装的 Admin 进程先通过服务管理器校验，也不会让常驻进程继续使用初始化前的权限快照。

## 故障排查

- 启动提示占位密钥：按“密钥与配置”生成并替换对应值。
- 首次安装页没有出现：确认使用 `first-install`，Supervisor 的 Admin 命令包含正确的 `-deploy-root` 和 `-setup-port`，且部署目录确实没有旧配置或安装标记。
- 安装提示陌生非空库或结构漂移：不要清表或点击强制接管；核对数据库名、备份、`schema_migrations` 和 `thingconnect_installation_state`，必要时换用空数据库或离线恢复原实例配置。
- `schema_migrations` 或 DDL 权限失败：使用迁移账号导入或升级数据库，应用账号只保留 DML 权限。
- Admin 页面可打开但刷新登录失败：HTTPS 环境保持 `cookie_secure: true`；本机 HTTP 开发设为 `false`。
- Admin 登录成功但页面持续转圈、导航为空：确认首个管理员通过发布脚本初始化，并重启 Admin Server 以重新加载 RBAC；随后检查 `/v1/admin/me` 与 `/v1/admin/me/navigation`。
- 管理页面 IP 都是代理地址：只把实际 Nginx 地址加入 `trusted_proxies`，并确认转发 `X-Forwarded-For`。
- 服务状态离线：检查六个进程是否使用同一 Redis，以及节点到 Redis 的网络与认证。
- MQTT 状态异常：检查 Broker TLS、认证模式和 `SERVICE_INSTANCE_ID` 是否唯一。
- 动态配置未生效：确认业务服务的 `admin.server_url`、六份 `internal.key` 和 Redis 均一致。

管理接口契约见 [API.md](API.md)，业务接口见 [api-reference.md](../../api-reference.md)。
