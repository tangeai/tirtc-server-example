# ThingConnect 部署指南

本文给出 Ubuntu 22.04/24.04 上可复制执行的首次安装、直接端口验收、可选 Nginx、Supervisor 日常发布和数据库迁移流程。默认部署目录为 `/opt/thing-connect`。

示例中的 `192.0.2.10`、`thing.example.com`、账号和密码都是占位值。执行前必须替换；任一步失败都应停止并处理错误。

## 1. 服务与端口

| 服务 | 默认 HTTP 端口 | 安装要求 |
|---|---:|---|
| `admin-server` | 9000 | 必装，管理后台和配置中心 |
| `device-server` | 9001 | 必装 |
| `user-server` | 9002 | 必装，同时提供用户 H5 |
| `voip-server` | 9003 | 可选 |
| `ai-server` | 9004 | 可选 |
| `call-server` | 9005 | 可选 |

首次安装会构建六个服务。安装器固定配置并启动 Admin、Device、User，只为安装页面勾选的 VoIP、AI、Call 生成配置并启动进程。未选择的可选服务不参与就绪检查。

应用端口使用上表默认值。安装页面填写的是 MySQL、Redis、MQTT 端口及部署访问地址，不在首次安装中任意重排应用端口。业务服务必须在 Admin 就绪后启动。

## 2. 安装前准备

### 2.1 安装构建工具

首次安装不要求 Nginx 或 Supervisor。安装基础工具：

```bash
(
  set -Eeuo pipefail
  sudo apt-get update
  sudo apt-get install -y \
    ca-certificates curl default-mysql-client git mosquitto-clients \
    redis-tools snapd util-linux
  sudo systemctl enable --now snapd.socket
  sudo snap install go --classic
  sudo snap install node --classic --channel=24/stable
)
```

使用企业软件源或已有工具链时可跳过 `snap install`，但必须满足 Go 1.21+、Node.js 20.19+ 或 22.12+、npm 10+。验证：

```bash
(
  set -Eeuo pipefail
  go version
  node --version
  npm --version
  GO_VERSION="$(go env GOVERSION)"
  awk -v version="${GO_VERSION#go}" '
    BEGIN {
      split(version, part, ".")
      exit !(part[1] > 1 || (part[1] == 1 && part[2] >= 21))
    }
  ' || {
    echo "Go 版本不满足 1.21+: $GO_VERSION" >&2
    exit 1
  }
  node -e '
    const [major, minor] = process.versions.node.split(".").map(Number);
    if (!((major === 20 && minor >= 19) ||
          (major === 22 && minor >= 12) || major > 22)) {
      console.error(`Node.js 版本不满足要求: ${process.versions.node}`);
      process.exit(1);
    }
  '
  NPM_MAJOR="$(npm --version | cut -d. -f1)"
  [ "$NPM_MAJOR" -ge 10 ] || {
    echo "npm 版本不满足 10+: $(npm --version)" >&2
    exit 1
  }
  command -v \
    curl flock git go mysql mysqldump node npm redis-cli setsid
)
```

安装服务器需要访问 Git 仓库、Go Module 源和 npm Registry。`install.sh` 会自行拉取源码并在服务器上构建二进制、用户 H5 和 Admin Web。

### 2.2 准备依赖和网络

准备以下信息：

- MySQL 8.0+ 地址、端口、目标数据库名和两个账号。
- Redis 7+ 地址、端口、密码和 DB 编号。
- 支持 MQTT 3.1.1 的 Broker 地址、用户名和密码。
- 应用服务器 IP；配置反向代理时再准备域名。
- 首个管理员邮箱和至少 12 位密码。

首次安装时只需允许管理员来源访问 TCP 9000。完成后按实际启用服务允许受信网络访问 9000～9005。MySQL、Redis 和内部 MQTT 不应对公网开放。

直接暴露业务端口会同时暴露该进程的路由面，只适合可信网络或验收。公网部署应使用第 4 节的 Nginx 单一入口，并禁止代理 `/v1/internal/*` 及各业务服务的 `/internal/*`。

### 2.3 准备 MySQL 账号

安装页面要求两个不同账号：

- 迁移账号：创建目标数据库、建表并执行版本化迁移。
- 运行账号：所有已启用服务共用，只授予 DML 权限。

安装器不创建 MySQL 用户、不执行 `GRANT`，因此迁移账号不需要 `GRANT OPTION`。先以 DBA 账号登录：

```bash
export MYSQL_HOST=127.0.0.1
export MYSQL_PORT=3306
mysql -h "$MYSQL_HOST" -P "$MYSQL_PORT" -u root -p
```

在 MySQL 客户端中执行。先替换两个密码；`thing_connect` 必须与安装页面填写的数据库名一致。生产环境应把 `%` 改成应用服务器来源地址：

```sql
CREATE USER IF NOT EXISTS 'thingconnect_migrator'@'%';
ALTER USER 'thingconnect_migrator'@'%'
  IDENTIFIED BY 'replace-with-migration-password';

CREATE USER IF NOT EXISTS 'thingconnect_runtime'@'%';
ALTER USER 'thingconnect_runtime'@'%'
  IDENTIFIED BY 'replace-with-runtime-password';

GRANT CREATE, ALTER, INDEX, SELECT, INSERT, UPDATE, DELETE
ON thing_connect.* TO 'thingconnect_migrator'@'%';

GRANT SELECT, INSERT, UPDATE, DELETE
ON thing_connect.* TO 'thingconnect_runtime'@'%';

EXIT;
```

目标数据库可以不存在。迁移账号的库级 `CREATE` 权限允许安装器创建该库；运行账号只有 DML 权限。如果数据库必须由 DBA 创建，在授权前执行：

```sql
CREATE DATABASE thing_connect
  CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
```

验证账号可以登录；此时不需要选择目标数据库：

```bash
(
  set -Eeuo pipefail
  export MYSQL_HOST=127.0.0.1
  export MYSQL_PORT=3306
  mysql -h "$MYSQL_HOST" -P "$MYSQL_PORT" \
    -u thingconnect_migrator -p -e "SELECT CURRENT_USER()"
  mysql -h "$MYSQL_HOST" -P "$MYSQL_PORT" \
    -u thingconnect_runtime -p -e "SELECT CURRENT_USER()"
)
```

不要手工导入 `scripts/schema.sql`。安装器会识别不存在的数据库、空库和可信旧版本，并按需初始化或迁移。运行账号权限会以不写入业务数据的语句逐表验证。

### 2.4 验证 Redis，并准备 MQTT 凭据

```bash
(
  set -Eeuo pipefail
  export REDIS_HOST=127.0.0.1
  export REDIS_PORT=6379
  export REDISCLI_AUTH='replace-with-redis-password'
  redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" PING
  unset REDISCLI_AUTH
)
```

无密码 Redis 应删除 `REDISCLI_AUTH` 的设置。MQTT 的通用 CLI 发布测试需要一个明确获准的 Topic，不能假设任意 Topic 都有权限；安装页面会使用所填账号执行不发布消息的连接认证。生产 MQTT 使用 TLS 时，应填写 `mqtts://` 地址并按 Broker 要求配置受信 CA。

TiRTC、SMTP、人机验证、微信和 AI 资源可在首次安装后从 Admin Web 配置。TiRTC App ID、Access Key ID、Secret Key ID 没有可用默认值；不填写不会阻止进程启动，但相关音视频、呼叫和 AI 功能不可用。

## 3. 首次安装

### 3.1 下载并运行安装脚本

以下命令只下载首次安装入口。脚本会把完整仓库克隆到 `/opt/thing-connect/tirtc-server-example`：

```bash
(
  set -Eeuo pipefail
  curl -fsSLo /tmp/thing-connect-install.sh \
    https://raw.githubusercontent.com/tangeai/tirtc-server-example/main/thing-connect/scripts/install.sh
  bash -n /tmp/thing-connect-install.sh
  chmod 0755 /tmp/thing-connect-install.sh
  sudo env "PATH=$PATH" /tmp/thing-connect-install.sh
)
```

私有镜像仓库或自定义目录使用环境变量，不要修改脚本：

```bash
sudo env "PATH=$PATH" \
  REPO_URL=git@example.com:team/tirtc-server-example.git \
  DEPLOY_ROOT=/opt/thing-connect \
  /tmp/thing-connect-install.sh
```

`install.sh` 依次执行：

1. 检查目标不是已安装实例，并取得部署锁。
2. 克隆仓库；已有干净工作树时只允许 `git pull --ff-only`。
3. 完整构建六个服务和 Web 静态资源。
4. 发布二进制、静态资源、`install.sh`、`service-local.sh` 和 `deploy-prod.sh`。
5. 生成只显示一次的安装令牌，通过本地启动脚本启动 Admin 安装模式。

脚本不安装、不修改 Nginx 或 Supervisor，也不复制示例配置。检测到正式配置、激活配置或安装完成标记时会拒绝重新安装。

### 3.2 完成 Web 安装

把 `192.0.2.10` 替换为服务器 IP，在浏览器打开：

```text
http://192.0.2.10:9000/admin/
```

安装端口默认监听所有网卡，但所有写操作都要求终端输出的一次性令牌。安装完成前仍应通过安全组或防火墙把 9000 限制到管理员来源，不要把令牌发送到聊天、工单或监控系统。

安装页面依次填写：

1. 可选服务：VoIP、AI、Call；Device 和 User 固定启用。
2. MySQL 主机、端口、数据库名、迁移账号和 DML 运行账号。
3. Redis 主机、端口、密码和 DB。
4. MQTT Broker 地址、端口、用户名和密码。
5. 首个管理员账号。
6. 统一对外访问地址、可信代理和 HTTPS Cookie。尚未配置反向代理时，统一对外访问地址可留空；这不影响各服务通过独立端口启动和访问。同机 Nginx 使用默认可信代理 `127.0.0.1`。

先执行“连接检查并生成安装计划”，核对数据库动作，再确认安装：

| 数据库状态 | 安装器行为 |
|---|---|
| 不存在 | 创建数据库并初始化表 |
| 已存在且无表 | 初始化表 |
| 可信且已是当前版本 | 保留表和数据，只补齐实例安装状态 |
| 可信旧版本 | 只执行缺失迁移，保留已有数据 |
| 同一实例中断 | 从持久化步骤恢复 |
| 已被其他已安装实例锁定 | 拒绝生成新共享密钥 |
| 陌生非空库、结构漂移、未来版本 | 写入前拒绝，转人工处理 |

安装器会生成共享业务 JWT、六服务 `internal.key`、Admin JWT 和 MFA 加密密钥，以 `0600` 权限写入不可变配置 revision。数据库提交和配置激活具有可恢复记录；中断后使用同一部署目录和数据库继续，不要删除表或手工改写 `config-current`。

令牌丢失且尚未提交配置时，可重新运行：

```bash
sudo env "PATH=$PATH" /opt/thing-connect/install.sh
```

配置已经激活但安装未结束时，重启 Admin 会自动恢复，不要重新安装。

### 3.3 验证直接端口和本地启动脚本

安装完成后检查进程和必需服务：

```bash
(
  set -Eeuo pipefail
  sudo /opt/thing-connect/service-local.sh status-all
  curl -fsS http://127.0.0.1:9000/health/ready
  curl -fsS http://127.0.0.1:9001/health/ready
  curl -fsS http://127.0.0.1:9002/health/ready
)
```

只检查已配置的可选服务：

```bash
(
  set -Eeuo pipefail
  for target in voip-server:9003 ai-server:9004 call-server:9005; do
    service="${target%%:*}"
    port="${target##*:}"
    if [ -f "/opt/thing-connect/config-current/$service/config.yaml" ] || \
       [ -f "/opt/thing-connect/$service/config.yaml" ]; then
      curl -fsS "http://127.0.0.1:$port/health/ready"
    fi
  done
)
```

可信网络中的直接入口：

- Admin Web：`http://192.0.2.10:9000/admin/`
- 用户 H5：`http://192.0.2.10:9002/`
- Device API：`http://192.0.2.10:9001/v1/device/`
- 已安装的 VoIP、AI、Call API：分别使用 9003、9004、9005。

`/health/live` 只表示进程存活；`/health/ready` 才表示必需依赖和数据库版本满足要求。

首次安装使用的本地启动脚本支持：

```bash
sudo /opt/thing-connect/service-local.sh status-all
sudo /opt/thing-connect/service-local.sh start-all
sudo /opt/thing-connect/service-local.sh stop-all
sudo /opt/thing-connect/service-local.sh restart thing-connect:admin-server
```

该脚本会拉起进程并在异常退出后重启，但不注册系统开机服务，也不轮转日志。服务器重启后需执行 `start-all`；长期生产运行和日常发布应按第 5 节切换到 Supervisor。

### 3.4 准备迁移配置

安装器不长期保存迁移账号密码。安装成功后创建独立迁移配置：

```bash
(
  set -Eeuo pipefail
  sudo cp /opt/thing-connect/config-current/admin-server/config.yaml \
    /opt/thing-connect/admin-server/migration-config.yaml
  sudo chmod 0600 /opt/thing-connect/admin-server/migration-config.yaml
  sudo nano /opt/thing-connect/admin-server/migration-config.yaml
)
```

只把 `database.dsn` 改成迁移账号 DSN。该 DSN 必须与所有运行配置指向同一 MySQL 主机、端口和数据库。文件包含高权限凭据，不得提交到 Git。

## 4. 可选：配置 Nginx 单一入口

直接端口已经可以使用，Nginx 不是首次安装条件。需要域名、统一 80 端口或公网隔离内部路由时再安装：

```bash
(
  set -Eeuo pipefail
  export THING_DOMAIN=thing.example.com
  [ "$THING_DOMAIN" != "thing.example.com" ] || {
    echo "请先替换 THING_DOMAIN" >&2
    exit 1
  }
  sudo apt-get update
  sudo apt-get install -y nginx
  cd /opt/thing-connect/tirtc-server-example/thing-connect
  sudo install -m 0644 deploy/nginx/thing-connect.nginx.conf \
    /etc/nginx/conf.d/thing-connect.conf
  sudo sed -i "s/thing\.example\.com/$THING_DOMAIN/g" \
    /etc/nginx/conf.d/thing-connect.conf
  sudo nginx -t
  sudo systemctl enable --now nginx
  sudo systemctl reload nginx
)
```

仓库模板使用以下推荐结构；它属于运维配置，不由 `install.sh` 写入系统：

```nginx
upstream device_server { server 127.0.0.1:9001; }
upstream user_server   { server 127.0.0.1:9002; }
upstream voip_server   { server 127.0.0.1:9003; }
upstream ai_server     { server 127.0.0.1:9004; }
upstream call_server   { server 127.0.0.1:9005; }
upstream admin_server  { server 127.0.0.1:9000; }

server {
    listen 80;
    server_name thing.example.com;
    server_tokens off;
    client_max_body_size 12m;

    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    location = /admin { return 301 /admin/; }
    location /admin/ { proxy_pass http://admin_server; }
    location /v1/admin/ { proxy_pass http://admin_server; }

    location = /v1/internal { return 404; }
    location ^~ /v1/internal/ { return 404; }
    location = /v1/device/internal { return 404; }
    location ^~ /v1/device/internal/ { return 404; }
    location = /v1/user/internal { return 404; }
    location ^~ /v1/user/internal/ { return 404; }
    location = /v1/voip/internal { return 404; }
    location ^~ /v1/voip/internal/ { return 404; }
    location = /v1/ai/internal { return 404; }
    location ^~ /v1/ai/internal/ { return 404; }
    location = /v1/call/internal { return 404; }
    location ^~ /v1/call/internal/ { return 404; }

    location /v1/device/ { proxy_pass http://device_server; }
    location /v1/voip/ { proxy_pass http://voip_server; }
    location /v1/ai/ { proxy_pass http://ai_server; }
    location /v1/call/ { proxy_pass http://call_server; }
    location / { proxy_pass http://user_server; }
}
```

未安装的可选服务应删除对应 upstream 和 location。Nginx 与服务同机时，各服务配置中的 `trusted_proxies` 使用安装页默认值 `127.0.0.1`，不要使用 `0.0.0.0/0`。

模板只监听 80，不申请或管理证书。已有证书时自行增加 443、证书路径和 HTTP 跳转，再执行 `sudo nginx -t && sudo systemctl reload nginx`。同时把 Admin `cookie_secure` 和用户服务发现地址改为实际 HTTPS 地址；不能只改 Nginx。

HTTP 会明文传输管理员登录信息和会话 Cookie。公网生产环境应在 Nginx 或上游负载均衡器启用 HTTPS。

## 5. 日常发布前切换到 Supervisor

只需快速直接运行时可以继续使用 `service-local.sh`。`deploy-prod.sh` 面向生产日常发布，要求 Supervisor 已接管所有六个程序定义；未配置的可选服务可以保持 `STOPPED`。

首次切换按以下顺序执行，先安装配置，再停止本地进程，最后让 Supervisor 接管，避免两个管理器抢占端口：

```bash
(
  set -Eeuo pipefail
  sudo apt-get update
  sudo apt-get install -y supervisor
  cd /opt/thing-connect/tirtc-server-example/thing-connect
  sudo install -m 0644 deploy/supervisor/thing-connect.supervisor.conf \
    /etc/supervisor/conf.d/thing-connect.conf
  sudo /opt/thing-connect/service-local.sh stop-all
  sudo supervisorctl reread
  sudo supervisorctl update
  sudo /opt/thing-connect/deploy-prod.sh start
  sudo /opt/thing-connect/deploy-prod.sh status
)
```

Supervisor 模板固定使用 `/opt/thing-connect` 和组名 `thing-connect`。使用其他目录或组名时，先复制模板到工作文件，替换所有路径和组名，执行 `supervisord -t -c /etc/supervisor/supervisord.conf` 验证后再安装。

如果 Supervisor 接管失败，先查看 `/opt/thing-connect/logs/*.err.log`。确认 Supervisor 进程已停止后，才可恢复本地脚本，避免端口冲突：

```bash
sudo supervisorctl stop 'thing-connect:*' || true
sudo /opt/thing-connect/service-local.sh start-all
```

## 6. 日常更新与数据库迁移

### 6.1 更新前备份

数据库地址、账号和数据库名必须与当前实例一致。命令会交互读取迁移账号密码：

```bash
(
  set -Eeuo pipefail
  export MYSQL_HOST=127.0.0.1
  export MYSQL_PORT=3306
  export MYSQL_USER=thingconnect_migrator
  export MYSQL_DATABASE=thing_connect
  export BACKUP_DIR="$HOME/thing-connect-backups"
  export BACKUP_FILE="$BACKUP_DIR/${MYSQL_DATABASE}-$(date +%Y%m%d-%H%M%S).sql.gz"
  install -d -m 0700 "$BACKUP_DIR"
  umask 077
  mysqldump -h "$MYSQL_HOST" -P "$MYSQL_PORT" \
    -u "$MYSQL_USER" -p --single-transaction --no-tablespaces \
    "$MYSQL_DATABASE" | gzip -9 >"$BACKUP_FILE"
  gzip -t "$BACKUP_FILE"
  test -s "$BACKUP_FILE"
  echo "数据库备份完成: $BACKUP_FILE"
)
```

备份配置和安装状态。文件包含明文密钥，只允许 root 读取：

```bash
(
  set -Eeuo pipefail
  export BACKUP_DIR="$HOME/thing-connect-backups"
  export CONFIG_BACKUP="$BACKUP_DIR/thing-connect-files-$(date +%Y%m%d-%H%M%S).tar.gz"
  install -d -m 0700 "$BACKUP_DIR"
  BACKUP_PATHS=()
  for path in \
    /opt/thing-connect/config-releases \
    /opt/thing-connect/config-current \
    /opt/thing-connect/var/installer \
    /opt/thing-connect/admin-server/var \
    /opt/thing-connect/admin-server/migration-config.yaml \
    /opt/thing-connect/*-server/config.yaml \
    /etc/nginx/conf.d/thing-connect.conf \
    /etc/supervisor/conf.d/thing-connect.conf; do
    if [ -e "$path" ] || [ -L "$path" ]; then
      BACKUP_PATHS+=("${path#/}")
    fi
  done
  [ "${#BACKUP_PATHS[@]}" -gt 0 ]
  sudo tar --create --gzip --file "$CONFIG_BACKUP" --directory=/ \
    "${BACKUP_PATHS[@]}"
  sudo chmod 0600 "$CONFIG_BACKUP"
  sudo test -s "$CONFIG_BACKUP"
  echo "配置备份完成: $CONFIG_BACKUP"
)
```

`gzip -t` 和非空检查只证明备份文件完整生成。生产环境仍需定期在隔离数据库执行恢复演练。

### 6.2 日常更新

```bash
sudo /opt/thing-connect/deploy-prod.sh update
```

`update` 依次执行快进拉取、完整构建、文件备份、数据库迁移、发布、按顺序重启和逐服务 readiness 检查。没有配置的可选服务不会启动。

只执行迁移：

```bash
sudo /opt/thing-connect/deploy-prod.sh migrate
```

只有外部迁移系统已经完成相同版本迁移时，才可设置 `SKIP_MIGRATIONS=1`。不要用它绕过迁移错误。

迁移不能随二进制自动回滚：

- schema 未变化时，发布失败会尝试恢复上一份文件。
- schema 已变化或可能变化时，脚本停止整个服务组，避免新旧二进制混跑。恢复数据库备份或确认版本兼容后再人工启动。

脚本文件备份位于 `${DEPLOY_ROOT}/releases/`，默认保留 10 份；它不替代数据库和配置备份。

## 7. 安装后配置与常用命令

登录 Admin Web 后先填写并发布 `common / tirtc`，再按已安装服务配置 SMTP、人机验证、微信应用和 AI 资源。

动态配置没有数据库记录时使用后端注册表默认值，不读取服务 YAML 中的同名业务值。普通配置和密钥都以明文 JSON 存储在 MySQL；Admin Web 对密钥默认显示 `*`，具备权限的管理员可点击眼睛查看原值。必须限制数据库和备份访问并启用审计。

```bash
sudo /opt/thing-connect/deploy-prod.sh help
sudo /opt/thing-connect/deploy-prod.sh status
sudo /opt/thing-connect/deploy-prod.sh validate
sudo /opt/thing-connect/deploy-prod.sh start
sudo /opt/thing-connect/deploy-prod.sh stop
sudo /opt/thing-connect/deploy-prod.sh restart
```

| 环境变量 | 作用 | 默认值 |
|---|---|---|
| `DEPLOY_ROOT` | 绝对部署目录 | `/opt/thing-connect` |
| `REPO_URL` | 首次安装克隆地址 | `https://github.com/tangeai/tirtc-server-example.git` |
| `REPO_PATH` | Git 仓库目录 | `${DEPLOY_ROOT}/tirtc-server-example` |
| `SUPERVISOR_GROUP` | Supervisor 组名 | `thing-connect` |
| `MIGRATION_CONFIG` | 迁移配置路径 | `${DEPLOY_ROOT}/admin-server/migration-config.yaml` |
| `HEALTH_WAIT_SECONDS` | readiness 等待秒数 | `30` |
| `BACKUP_KEEP_COUNT` | 成功文件备份保留数 | `10` |
| `ALLOW_INSECURE_ADMIN_COOKIE` | 允许 HTTP 下非 Secure Admin Cookie；设为 `0` 时要求 `admin.cookie_secure: true` | `1` |

`deploy-prod.sh` 无参数运行时进入交互菜单；自动化发布使用明确的单一命令。

## 8. 手工部署和 Schema

`scripts/schema.sql` 只用于全新空库。已有 ThingConnect 数据库必须使用 `/opt/thing-connect/deploy-prod.sh migrate`，不能重新导入该文件。

手工运行服务时，六份 `config.yaml.example` 只是字段模板，必须替换所有占位密钥。先启动 Admin，再启动 Device、User 和所需可选服务。业务服务首次启动无法从 `admin.server_url` 取得有效动态配置时会拒绝监听。

Admin 功能、RBAC、配置中心和任务说明见 [Admin Server README](admin/admin-server/README.md)。业务接口见 [API Reference](api-reference.md)，数据库迁移结构见 [迁移文件说明](internal/store/mysql/migrate/migrations/README.md)。
