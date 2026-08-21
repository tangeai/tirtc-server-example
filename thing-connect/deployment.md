# ThingConnect 部署指南

本文说明如何把 ThingConnect 源码部署到 Linux 服务器，以及如何执行首次安装、验收、更新和数据库迁移。生产环境使用 `scripts/deploy-prod.sh`、Supervisor 和 Nginx。

## 1. 服务清单

| 服务 | 端口 | 安装要求 |
|---|---:|---|
| `admin-server` | 9000 | 必装，管理后台和动态配置中心 |
| `device-server` | 9001 | 必装 |
| `user-server` | 9002 | 必装，同时提供用户 H5 |
| `voip-server` | 9003 | 可选 |
| `ai-server` | 9004 | 可选 |
| `call-server` | 9005 | 可选 |

以上是新安装默认端口。已有部署以当前服务配置和反向代理配置为准；调整 Admin 端口时，必须同时更新 Admin 监听端口、五个业务服务的 `admin.server_url`、Supervisor 安装参数和 Nginx upstream。

安装器固定启用 Device 和 User，并按选择启用 VoIP、AI、Call。未选择的可选服务不生成配置、不启动、不参与健康检查。业务服务依赖 Admin 首次加载动态配置，启动顺序为 Admin、Device、User、已选可选服务。

## 2. 部署前准备

### 2.1 服务器软件

- Linux x86-64 或 arm64。
- Git、Bash、`curl`、`flock`、CA 证书、MySQL 客户端。
- Go 1.21+。
- Node.js 20.19+ 或 22.12+，npm 10+。
- MySQL 8.0+、Redis 7+、支持 MQTT 3.1.1 的 Broker。
- Supervisor 和 Nginx。

部署脚本会在服务器上构建 Go 服务、用户 H5 和 Admin Web。执行账号必须能运行 `git`、`go`、`npm`、`supervisorctl`，并能写入部署目录。

### 2.2 网络信息

提前准备：

- HTTPS 域名和证书。
- 应用服务器可访问的 MySQL、Redis、MQTT 地址。
- MQTT 服务端用户名和密码。
- Nginx 或负载均衡器的实际 IP/CIDR，用于 `trusted_proxies`。

公网只开放 80、443 和设备需要访问的 MQTTS 端口。`9000`～`9005`、MySQL、Redis 只允许本机或受信内网访问。`/v1/internal/*` 及各业务服务的 `/internal/*` 不得对公网代理。

### 2.3 MySQL 账号

安装页面要求两个不同账号：

- 迁移账号：创建数据库、建表和执行版本化迁移。
- 运行账号：所有常驻服务使用，只授予 DML 权限。

两个账号都必须提前创建。安装器不创建 MySQL 用户、不执行 `GRANT`，因此迁移账号不需要 `GRANT OPTION`。

自管 MySQL 可由 DBA 执行以下模板；生产环境应把 `%` 限制为应用服务器来源地址，并替换密码：

```sql
CREATE USER 'thingconnect_migrator'@'%' IDENTIFIED BY 'replace-with-migration-password';
CREATE USER 'thingconnect_runtime'@'%' IDENTIFIED BY 'replace-with-runtime-password';

GRANT CREATE, ALTER, INDEX, SELECT, INSERT, UPDATE, DELETE
ON thing_connect.* TO 'thingconnect_migrator'@'%';

GRANT SELECT, INSERT, UPDATE, DELETE
ON thing_connect.* TO 'thingconnect_runtime'@'%';
```

数据库可以不存在，也可以预先创建为空库：

```sql
CREATE DATABASE IF NOT EXISTS thing_connect
  CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
```

运行账号必须能连接目标库。安装器会用事务验证其对迁移账本的读取权限，以及对所有受管表的 `SELECT`、`INSERT`、`UPDATE`、`DELETE` 权限，不会产生业务数据。

### 2.4 Redis、MQTT 和业务凭据

- Redis：准备地址、密码和 DB 编号。
- MQTT：准备 `mqtt://` 或 `mqtts://` 地址、用户名和密码；生产使用 TLS，并配置 Topic ACL。
- TiRTC、SMTP、人机验证、微信和 AI 资源可在安装完成后从 Admin Web 配置。

TiRTC App ID、Access Key ID、Secret Key ID 没有可用默认值。未填写不会阻止进程启动，但会阻塞登录令牌、实时音视频、呼叫和 AI 等相关功能。

## 3. 首次部署

以下示例使用部署目录 `/opt/thing-connect` 和 Supervisor 组 `thing-connect`。

### 3.1 获取代码

```bash
git clone https://github.com/tangeai/tirtc-server-example.git
cd tirtc-server-example/thing-connect
```

私有仓库需要给实际执行脚本的账号配置只读 Deploy Key，并通过 `REPO_URL` 传入仓库地址；本文使用 `sudo` 执行，因此对应账号是 root。

### 3.2 配置 Supervisor

```bash
sudo cp deploy/supervisor/thing-connect.supervisor.conf \
  /etc/supervisor/conf.d/thing-connect.conf
sudo editor /etc/supervisor/conf.d/thing-connect.conf
```

模板默认使用 `/opt/thing-connect` 和 `thing-connect`，可直接配合本文命令使用。需要自定义时必须同步修改：

- 所有 program 的 `directory`、`command` 和日志路径。
- Supervisor 组名，以及 Admin 命令的 `-deploy-root`、`-supervisor-group`。
- 确认 `user`。模板默认使用 `root`，可直接配合本文命令完成安装；如改为专用账号，部署脚本也必须以该账号执行，并提前配置部署目录写权限和 `supervisorctl` 权限，否则安装器生成的 `0600` 配置可能无法被服务读取。

保留六个 program。三个可选服务保持 `autostart=false`。

```bash
sudo supervisorctl reread
sudo supervisorctl update
sudo supervisorctl status 'thing-connect:*'
```

首次发布前 program 可能因二进制尚不存在而退出；六个 program 能被 Supervisor 识别即可。

### 3.3 启动安装器

```bash
sudo env \
  DEPLOY_ROOT=/opt/thing-connect \
  SUPERVISOR_GROUP=thing-connect \
  REPO_URL=https://github.com/tangeai/tirtc-server-example.git \
  ./scripts/deploy-prod.sh first-install
```

脚本会拉取代码、完整构建、发布文件、停止未配置的业务服务，并启动 Admin 安装模式。终端只显示一次安装令牌，不要把令牌写入聊天、工单或日志。

脚本同时把当前版本原子发布为 `/opt/thing-connect/deploy-prod.sh`。首次安装后的更新、迁移和服务管理统一使用这个入口；只有文件发布成功或完整更新完成后才刷新，拉取、构建或发布失败不会覆盖上一版。

安装模式默认只监听 `127.0.0.1:9000`。Nginx 尚未配置时，在本地电脑建立隧道：

```bash
ssh -L 9000:127.0.0.1:9000 deployer@thing.example.com
```

打开 `http://127.0.0.1:9000/admin/`。不要把安装端口直接开放到公网。

令牌丢失且配置尚未提交时，可重新运行 `first-install` 轮换令牌。已有正式配置或安装完成标记时，脚本拒绝再次开放安装入口。

### 3.4 填写安装页面

依次填写：

1. 可选服务：VoIP、AI、Call。
2. MySQL 地址、数据库名、迁移账号和运行账号。
3. Redis 地址、密码和 DB。
4. MQTT Broker、用户名和密码。
5. 最终公网 HTTPS 地址、可信代理和安全 Cookie。
6. 首个管理员邮箱、昵称和至少 12 位密码。

先执行预检并核对页面展示的数据库动作。安装器按数据库状态处理：

| 数据库状态 | 行为 |
|---|---|
| 不存在 | 创建数据库并初始化表 |
| 已存在且没有表 | 初始化表 |
| 可信且版本已是当前版本，但尚未锁定为已安装实例 | 不重建、不清空已有表和数据 |
| 可信旧版本 | 只执行缺失迁移，保留已有数据 |
| 同一实例的中断安装 | 从持久化步骤继续 |
| 已锁定的已安装实例 | 拒绝生成新共享密钥；必须恢复该实例原配置 |
| 陌生非空库、结构漂移、未来版本 | 写入前拒绝，转人工处理 |

安装器会创建管理员和默认权限，生成共享业务 JWT、六服务 `internal.key`、Admin JWT 和 MFA 加密密钥，并把配置以 `0600` 权限写入不可变 revision。配置激活后先启动 Admin，再启动必需服务和已选可选服务。

安装中断时使用同一部署目录和数据库继续，不要删除表、切换数据库或手工改写 `config-current`。

### 3.5 准备日常迁移配置

安装器不长期保存迁移账号密码。安装成功后创建：

```text
/opt/thing-connect/admin-server/migration-config.yaml
```

```bash
sudo cp /opt/thing-connect/config-current/admin-server/config.yaml \
  /opt/thing-connect/admin-server/migration-config.yaml
sudo chmod 600 /opt/thing-connect/admin-server/migration-config.yaml
sudo editor /opt/thing-connect/admin-server/migration-config.yaml
```

只把 `database.dsn` 改成迁移账号 DSN。该 DSN 必须与所有运行配置指向同一 MySQL 主机、端口和数据库。文件包含高权限凭据，不得提交到 Git。

### 3.6 配置 Nginx

```bash
sudo cp deploy/nginx/thing-connect.nginx.conf \
  /etc/nginx/conf.d/thing-connect.conf
sudo editor /etc/nginx/conf.d/thing-connect.conf
sudo nginx -t
sudo systemctl reload nginx
```

修改域名、证书路径和 upstream。未安装的可选服务应移除对应公网 location。保留模板中对所有内部接口的拒绝规则。

对外入口：

- 用户端：`https://thing.example.com/`
- 管理后台：`https://thing.example.com/admin/`
- 管理 API：`https://thing.example.com/v1/admin/`

Nginx 与服务同机时，`trusted_proxies` 通常只填 `127.0.0.1`，不要填写 `0.0.0.0/0`。

## 4. 安装后配置与验收

登录 Admin Web，先填写并发布 `common / tirtc`。再按已安装服务配置 SMTP、人机验证、微信应用和 AI 资源。

动态配置没有数据库记录时使用后端注册表默认值，不读取服务 YAML 中的同名业务值。普通配置和密钥均以明文 JSON 存储在 MySQL；Admin Web 对密钥默认显示 `*`，具备权限的管理员可点击眼睛查看原值。必须限制数据库和备份访问并启用审计。

检查必需服务：

```bash
sudo supervisorctl status 'thing-connect:*'
curl -fsS http://127.0.0.1:9000/health/ready
curl -fsS http://127.0.0.1:9001/health/ready
curl -fsS http://127.0.0.1:9002/health/ready
```

只检查已安装的可选服务：

```bash
curl -fsS http://127.0.0.1:9003/health/ready  # VoIP
curl -fsS http://127.0.0.1:9004/health/ready  # AI
curl -fsS http://127.0.0.1:9005/health/ready  # Call
```

`/health/live` 只表示进程存活；`/health/ready` 才表示必需依赖和数据库版本满足要求。Supervisor 显示 `RUNNING` 不能替代 readiness。

最后确认 HTTPS、Admin 登录、用户页面、设备接口正常，并确认公网无法访问 `/v1/internal/*`。服务日志位于 `${DEPLOY_ROOT}/logs/`。

## 5. 日常更新与迁移

更新前备份 MySQL，并确认备份可恢复。

```bash
sudo /opt/thing-connect/deploy-prod.sh update
```

`update` 会依次执行：快进拉取、完整构建、文件备份、数据库迁移、发布、按顺序重启和逐服务 readiness 检查。没有配置的可选服务不会发布或启动。

只执行迁移：

```bash
sudo /opt/thing-connect/deploy-prod.sh migrate
```

只有外部迁移系统已经完成相同版本迁移时，才可设置 `SKIP_MIGRATIONS=1`。不要用它绕过迁移错误。

迁移不能随二进制自动回滚：

- schema 未变化时，发布失败会尝试恢复上一份文件。
- schema 已变化或可能变化时，脚本停止整个服务组，避免新旧二进制混跑；此时恢复数据库备份或确认版本兼容后再人工启动。

脚本文件备份位于 `${DEPLOY_ROOT}/releases/`，默认保留 10 份。它不等于数据库备份。升级前还应备份 `config-releases/`、`config-current`、`admin-server/var/`、迁移配置、Nginx、Supervisor 和证书。

## 6. 常用命令

```bash
/opt/thing-connect/deploy-prod.sh help
/opt/thing-connect/deploy-prod.sh status
/opt/thing-connect/deploy-prod.sh validate
/opt/thing-connect/deploy-prod.sh start
/opt/thing-connect/deploy-prod.sh stop
/opt/thing-connect/deploy-prod.sh restart
```

| 环境变量 | 作用 | 默认值 |
|---|---|---|
| `DEPLOY_ROOT` | 绝对部署目录 | `/opt/thing-connect` |
| `REPO_URL` | Git 仓库地址 | 脚本内的 SSH 地址 |
| `SUPERVISOR_GROUP` | Supervisor 组名 | `thing-connect` |
| `MIGRATION_CONFIG` | 迁移配置路径 | `${DEPLOY_ROOT}/admin-server/migration-config.yaml` |
| `HEALTH_WAIT_SECONDS` | readiness 等待秒数 | `30` |
| `BACKUP_KEEP_COUNT` | 成功文件备份保留数 | `10` |

无参数运行脚本进入交互菜单；自动化发布使用明确的单一命令。

## 7. 手工部署说明

`scripts/schema.sql` 只用于全新空库。已有 ThingConnect 数据库必须使用 `deploy-prod.sh migrate`，不能重新导入该文件。

手工运行服务时，六份 `config.yaml.example` 仅是字段模板，必须替换所有占位密钥。先启动 Admin，再启动 Device、User 和所需可选服务。业务服务首次启动无法从 `admin.server_url` 取得有效动态配置时会拒绝监听。

更详细的 Admin 功能、RBAC、配置中心和任务说明见 [Admin Server README](admin/admin-server/README.md)。业务接口见 [API Reference](api-reference.md)，数据库迁移结构见 [迁移文件说明](internal/store/mysql/migrate/migrations/README.md)。
