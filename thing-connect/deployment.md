# ThingConnect 部署与二次开发

ThingConnect 包含五个业务服务、一个 Admin Server、用户 H5 和 Admin Web。完整生产部署步骤、密钥关系、Supervisor、Nginx、多实例和升级流程见 [Admin Server 完整部署指南](admin/admin-server/README.md)。

## 依赖

- Go 1.21+
- Node.js 20.19+ 或 22.12+，npm 10+
- MySQL 8.0+
- Redis 7+
- MQTT Broker

## 构建

```bash
chmod +x build.sh
./build.sh
```

`bin/` 中生成：

- `device-server`
- `user-server`
- `voip-server`
- `ai-server`
- `call-server`
- `admin-server`

用户 H5 静态文件位于 `user-server/static/`，Admin Web 位于 `admin/admin-web/dist/`。

## 数据库

```bash
mysql -u root -p -e "CREATE DATABASE thing_connect CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
mysql -u root -p thing_connect < scripts/schema.sql
```

`schema.sql` 用于全新安装。可执行迁移按服务领域位于 `internal/store/mysql/migrate/migrations/`，通过 `go:embed` 随迁移程序发布；执行器在 MySQL 命名锁内逐条运行，并由 `schema_migrations` 记录兼容的 `core/admin` 版本。应用账号只授予 DML 权限；数据库升级由具备 DDL 权限的迁移账号执行，常驻服务不自行运行 SQL 文件。

## 首次 Web 安装

Supervisor 部署使用 `scripts/deploy-prod.sh first-install`，或在交互菜单选择“首次 Web 安装（仅空部署）”。该入口只接受没有正式配置、没有 `config-current` 且没有安装完成标记的部署目录。脚本预置可选组件的程序包、停止尚未配置的业务服务、创建一次性安装授权并启动 Admin 的安装模式；终端只显示一次 256 位安装令牌。安装模式默认只监听 `127.0.0.1:9010`，通过同机 HTTPS 反向代理或 SSH 端口转发打开 `/admin/`，选择业务服务并填写 MySQL、Redis、MQTT、对外访问地址和首个管理员。`device-server`、`user-server` 固定启用；`voip-server`、`ai-server`、`call-server` 按需选择。令牌输出丢失且尚未提交配置时，可以重新执行同一命令安全轮换令牌。

迁移账号与运行账号都必须填写且用户名不同。运行账号对 `schema_migrations` 只需 SELECT，对其余受管表需要 SELECT、INSERT、UPDATE、DELETE；安装器使用不产生数据行的事务语句逐表验证这些权限。安装器先执行只读预检，再展示确定的数据库动作：

- 数据库不存在时创建数据库并初始化表。
- 数据库存在且没有表时初始化表。
- 具有可信 ThingConnect 所有权和迁移记录的旧数据库只执行缺失的版本化迁移，不删除、重建或清空已有数据。
- 陌生非空库、迁移记录与结构不一致的库、版本高于当前程序的库不执行任何自动写入。

执行阶段同时持有部署目录文件锁和 MySQL 命名锁。迁移、首个管理员和所选服务配置按持久状态向前收敛；MySQL DDL 中断后保留已提交步骤供恢复，不自动删除数据库或表。Admin、两个必需业务服务及所选可选服务的配置先写入权限为 `0600` 的 immutable revision 并完整解析；数据库安装状态随后提交 `config_activation_pending` 激活意图，只有该事务成功后才原子切换 `config-current`。中断恢复按操作 ID、清单和逐文件 SHA-256 校验同一 revision；若数据库意图已提交但文件指针尚未切换，“继续安装”会验证该意图并重放激活，不要求再次输入连接信息。Admin 重启并通过 `/health/ready` 后，安装器只启动已选择的业务服务并逐个等待 readiness，全部就绪后永久关闭安装入口。

安装表单中的密码字段默认隐藏。安装令牌、数据库/Redis/MQTT 密码、管理员密码和生成密钥不写入安装状态或 HTTP 响应。安装中断时重新打开同一页面可查看脱敏进度；DDL 前可以重新填写连接信息，DDL 后只允许继续当前数据库上的幂等恢复。

## 配置

每个服务从自己的 `config.yaml` 启动，示例文件位于对应服务目录。首次 Web 安装使用部署根目录的 `config-current/<service>/config.yaml`；仅当传统的服务目录配置不存在时才读取该原子 bundle。配置解析会拒绝未知字段和公开占位密钥，已有但损坏的服务目录配置不会静默回退到 bundle。

- 五个业务服务共享 `jwt_secret`。
- 六个服务共享 `internal.key`。
- Admin 使用独立的 `admin.jwt_secret`。
- Admin 使用 Base64 编码的 32 字节 `security.config_encryption_key` 加密管理员 MFA 因子，并用于升级时解密旧版配置密钥。
- 多实例共享 MySQL 和 Redis，并设置唯一 `SERVICE_INSTANCE_ID`。
- Nginx 同机部署时设置 `trusted_proxies: ["127.0.0.1"]`。

`config.yaml` 只提供数据库、Redis、MQTT、服务地址和进程认证密钥等启动引导参数。注册到 Admin 配置中心的系统配置、通用配置和五个服务业务配置使用数据库值；数据库中没有发布记录时使用后端注册表默认值，不回退到各服务 YAML 中的同名业务值。五个业务服务启动时必须通过 `admin.server_url` 完成首次配置加载，Admin 不可达或返回无效配置时服务拒绝监听端口；启动完成后的短暂故障继续使用内存中的最后有效值并重试。TiRTC 应用 ID 和访问密钥没有可运行的默认值，必须在通用配置中填写，后台将其标注为阻塞项。

配置中心的普通字段和密钥字段均以明文 JSON 存储。Admin Web 对密钥使用密码控件，默认隐藏字符，具备密钥修改权限的管理员可点击眼睛查看原值。数据库权限、备份和访问审计需要覆盖这些明文凭证。

生产日常更新使用 `scripts/deploy-prod.sh update`。该流程要求 `admin-server/migration-config.yaml` 使用具备 DDL 权限的数据库账号，并在任何 DDL 前确认其主机、端口和库名与所有已安装服务的运行配置完全一致，同时拒绝陌生非空库、未来版本和结构漂移。流程依次执行快进拉取、构建、文件备份、迁移、发布、逐服务重启和 readiness 检查；没有配置的可选服务不发布、不启动也不参与检查。数据库迁移不会随文件回滚，因此执行前必须具备可恢复的数据库备份。若迁移可能已开始且随后失败，或迁移成功改变 schema 后发布失败，脚本停止整个服务组并保留文件备份，不允许新旧二进制继续混合运行；恢复数据库备份或确认兼容性后再人工处理。迁移未改变 schema 或显式设置 `SKIP_MIGRATIONS=1` 时才执行自动文件回滚。常驻服务只读校验 `schema_migrations`，不使用运行账号尝试 DDL。脚本先启动 Admin，再逐个重启已安装业务服务，并以各服务 `server.http_port` 的 `/health/ready` 作为成功门槛；Supervisor 仅显示 `RUNNING` 不代表发布成功。

## 开发运行

准备 MySQL、Redis、MQTT 和六份 `config.yaml` 后，可分别运行：

```bash
go run ./device-server -c device-server/config.yaml
go run ./user-server -c user-server/config.yaml
go run ./voip-server -c voip-server/config.yaml
go run ./ai-server -c ai-server/config.yaml
go run ./call-server -c call-server/config.yaml
go run ./admin/admin-server -c admin/admin-server/config.yaml
```

Admin Web 开发服务器：

```bash
npm --prefix admin/admin-web ci
npm --prefix admin/admin-web run dev
```

首次管理员由首次 Web 安装向导创建。手工配置部署可继续使用 `deploy-prod.sh init-admin` 或菜单中的“初始化首个管理员”，直接运行二进制的命令见完整部署指南。

## 测试

单元测试在无外部依赖时可运行；数据库集成测试使用 `THING_CONNECT_TEST_CONFIG` 指定配置。仓库提供 `tests/testdata/config.yaml`，默认连接本机测试 MySQL 与 Redis。

```bash
go vet ./...
THING_CONNECT_TEST_CONFIG=tests/testdata/config.yaml go test ./... -p 1 -count=1
npm --prefix admin/admin-web run build
```

`/health/live` 表示进程存活，`/health/ready` 检查当前服务的必需依赖。Admin 首页按实例展示五个业务服务状态。

## 对外路由

生产环境使用 [demo-open.nginx.conf](deploy/nginx/demo-open.nginx.conf) 统一入口：

- `/`：用户 H5 与 user-server
- `/v1/device/`：device-server
- `/v1/voip/`：voip-server
- `/v1/ai/`：ai-server
- `/v1/call/`：call-server
- `/admin/`、`/v1/admin/`：admin-server

`/v1/internal/` 只允许服务间访问，不通过公网代理。

业务 API 见 [api-reference.md](api-reference.md)，Admin API 见 [admin/admin-server/API.md](admin/admin-server/API.md)。
