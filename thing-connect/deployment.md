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

`schema.sql` 用于全新安装。`schema_migrations` 记录业务和 Admin 数据库版本。应用账号可以只授予 DML 权限；数据库升级由具备 DDL 权限的迁移账号执行。

## 配置

每个服务从自己的 `config.yaml` 启动，示例文件位于对应服务目录。配置解析会拒绝未知字段和公开占位密钥。

- 五个业务服务共享 `jwt_secret`。
- 六个服务共享 `internal.key`。
- Admin 使用独立的 `admin.jwt_secret`。
- Admin 使用 Base64 编码的 32 字节 `security.config_encryption_key` 加密数据库中的配置密钥。
- 多实例共享 MySQL 和 Redis，并设置唯一 `SERVICE_INSTANCE_ID`。
- Nginx 同机部署时设置 `trusted_proxies: ["127.0.0.1"]`。

服务启动先使用 YAML。某项配置在 Admin 后台首次发布后，由数据库值接管并动态同步。系统配置、通用配置和五个服务的业务配置使用不同命名空间。

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

首次管理员使用 `admin-server -init-admin` 初始化，具体命令见完整部署指南。

## 测试

单元测试在无外部依赖时可运行；数据库集成测试使用 `THING_CONNECT_TEST_CONFIG` 指定配置。仓库提供 `tests/testdata/config.yaml`，默认连接本机测试 MySQL 与 Redis。

```bash
go vet ./...
THING_CONNECT_TEST_CONFIG=tests/testdata/config.yaml go test ./... -p 1 -count=1
npm --prefix admin/admin-web run build
```

`/health/live` 表示进程存活，`/health/ready` 检查当前服务的必需依赖。Admin 首页按实例展示五个业务服务状态。

## 对外路由

生产环境使用 [nginx.conf.example](nginx.conf.example) 统一入口：

- `/`：用户 H5 与 user-server
- `/v1/device/`：device-server
- `/v1/voip/`：voip-server
- `/v1/ai/`：ai-server
- `/v1/call/`：call-server
- `/admin/`、`/v1/admin/`：admin-server

`/v1/internal/` 只允许服务间访问，不通过公网代理。

业务 API 见 [api-reference.md](api-reference.md)，Admin API 见 [admin/admin-server/API.md](admin/admin-server/API.md)。
