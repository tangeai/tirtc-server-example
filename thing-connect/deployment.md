# 部署与运维

五个 Go 服务的环境准备、配置、构建、运行与二次开发。

> 系统架构与快速体验见 [README.md](README.md)；设备端上线流程见 [device-integration.md](device-integration.md)；接口字段定义见 [api-reference.md](api-reference.md)。

## 目录

- [前置条件](#前置条件)
- [数据库初始化](#数据库初始化)
- [配置详解](#配置详解)
- [构建与运行](#构建与运行)
- [微信公众平台配置](#微信公众平台配置)
- [数据库设计](#数据库设计)
- [EMQX 配置要点](#emqx-配置要点)
- [二次开发](#二次开发)
- [运行测试](#运行测试)

---

## 前置条件

- Go 1.21+
- MySQL 5.7+ 或 MariaDB 10.3+
- Redis 6+
- MQTT Broker（支持 TLS，如 EMQX）
- 微信公众平台已开通 IoT VoIP 能力
- tange.ai TiRTC 账号（AppID / AccessKeyID / SecretKeyID）

---

## 数据库初始化

```bash
# 全新安装
mysql -u root -p < scripts/schema.sql

# 升级已有数据库时执行迁移脚本
mysql -u root -p < scripts/migrate_bind_refactor.sql   # 绑定系统重构
mysql -u root -p < scripts/migrate_ai_prefix.sql        # AI 表加前缀
```

> `scripts/schema.sql` 与 `internal/db/migrate.go` 保持同步；服务启动时也会自动执行迁移。

### 初始化全局设备池

`device_pool` 表需预先写入已授权的设备凭证，每台设备一行。`device_id` 与 `device_key` 由探鸽平台统一签发，设备端不可自行生成——非官方签发的凭证将无法通过探鸽云平台认证。

```sql
INSERT INTO device_pool (device_id, device_key, status) VALUES
  ('TIRZ00000001', '<平台签发的 device_key>', 0),
  ('TIRZ00000002', '<平台签发的 device_key>', 0);
```

---

## 配置详解

五个服务共用同一套配置结构（`internal/config/`），每个服务一份 `config.yaml`：

```bash
cp device-server/config.yaml.example device-server/config.yaml
cp user-server/config.yaml.example   user-server/config.yaml
cp voip-server/config.yaml.example   voip-server/config.yaml
cp ai-server/config.yaml.example     ai-server/config.yaml
cp call-server/config.yaml.example   call-server/config.yaml
```

> **`jwt_secret` 五个服务必须相同**（device-server 签发的 `mqtt_token` 由 voip-server、ai-server、call-server 验证）。

### device-server

```yaml
server:
  http_port: 9001

database:
  dsn: "user:pass@tcp(127.0.0.1:3306)/tirtc_thing_connect?parseTime=true"

redis:
  addr: "127.0.0.1:6379"
  password: ""

mqtt:
  broker: "mqtts://your-broker:8883"
  client_id: "devicesrv_main"
  password: "your-mqtt-password"

jwt_secret: "shared-secret"   # 与所有服务相同

service:
  quota_per_user: 10                  # 兼容字段；当前注册不读取，配额见下方说明
  code_ttl: 190s                      # 验证码有效期
  rate_limit_window: 190s             # 同一指纹限频窗口（L3，默认 190s，与 code_ttl 一致）
  rate_limit_max_hits: 10             # 窗口内最大请求数（默认 10）
  ip_rate_limit_window: 60s           # 单 IP 指纹多样性窗口（L2，默认 60s）
  ip_rate_limit_max_fingerprints: 50  # 单 IP 窗口内最多不同指纹数（默认 50）
  global_max_pending_codes: 10000     # 全局同时最多未使用验证码（L4，默认 10000）
  token_expiry: 168h                  # mqtt_token 有效期（默认 7 天）
  mqtt_ack_timeout: 5s                # 等待设备 ACK 超时
```

> `rate_limit_*` 为 L3（同指纹限频+回放）参数，`ip_rate_limit_*` 为 L2（单 IP 指纹多样性）参数，两者窗口独立配置。
>
> 新用户可绑设备配额来自 `users.bind_quota` 列默认值（当前 schema 默认 10），
> 注册流程不读取 `service.quota_per_user`。调整默认配额时应修改数据库列默认值；
> 已注册用户需按运营策略单独更新 `users.bind_quota`。

### user-server

与 device-server 配置结构相同，额外需要 TiRTC 凭据（用于签发 H5 直连 token）：

```yaml
server:
  http_port: 9002

# database / redis / mqtt / jwt_secret / service 同 device-server

tirtc:
  app_id: "your-tirtc-app-id"
  access_key_id: "your-access-key-id"
  secret_key_id: "your-secret-key-id"
  endpoint: "https://your-tirtc-endpoint"

yidun:                         # 可选：网易易盾人机验证
  captcha_id: "xxx"
  secret_id: "xxx"
  secret_key: "xxx"

smtp:                          # 邮件发送（注册验证码）
  host: "smtp.example.com"
  port: 465
  username: "no-reply@example.com"
  password: "xxx"
  from: "no-reply@example.com"

call:                          # 三个解绑清理接口共用同一内部密钥
  internal_key: "shared-internal-key"
  call_server_url: "http://localhost:9005"

ai:                            # 解绑时清理设备 AI 角色绑定
  server_url: "http://localhost:9004"

voip:                          # 解绑时清理 VoIP profile、授权和外呼状态
  server_url: "http://localhost:9003"
```

> 配置任一 `ai.server_url`、`voip.server_url` 或 `call.call_server_url` 时，
> `call.internal_key` 必须同时配置，并与 ai-server、voip-server、
> call-server 中的值完全一致。三个地址都配置后，解绑清理才覆盖全部业务服务。

### voip-server

```yaml
server:
  http_port: 9003

database:
  dsn: "user:pass@tcp(127.0.0.1:3306)/tirtc_thing_connect?parseTime=true"

redis:
  addr: "127.0.0.1:6379"

mqtt:
  broker: "mqtts://your-broker:8883"
  client_id: "voipsrv_main"
  password: "your-mqtt-password"

jwt_secret: "shared-secret"   # 与所有服务相同

# 服务间解绑清理接口鉴权；与 user/ai/call-server 使用同一个值
call:
  internal_key: "shared-internal-key"

tirtc:
  app_id: "your-tirtc-app-id"
  access_key_id: "your-access-key-id"
  secret_key_id: "your-secret-key-id"
  endpoint: "https://your-tirtc-endpoint"

wechat:
  default_app_id: "wxYOUR_APP_ID"
  apps:
    wxYOUR_APP_ID:
      secret: "your-wechat-app-secret"
      token: "your-wechat-server-token"
      encoding_aes_key: ""          # 43 字符，留空则使用明文模式
      model_id: "your-voip-model-id"
```

多小程序支持：在 `wechat.apps` 下追加新的 AppID 即可，无需改代码。

### ai-server

```yaml
server:
  http_port: 9004

database:
  dsn: "user:pass@tcp(127.0.0.1:3306)/tirtc_thing_connect?parseTime=true"

jwt_secret: "shared-secret"   # 与其他服务相同

tirtc:
  app_id: "your-tirtc-app-id"
  access_key_id: "your-access-key-id"
  secret_key_id: "your-secret-key-id"

tirtc_aichat:
  base_url: "https://api-tirtc.tange365.com"    # 探鸽云 Agent API 地址
  default_role_id: "fin63bby1og0"               # 默认 AI 角色 ID
  base_role_url: "https://..."                  # 可选，角色 API 独立地址

call:
  internal_key: "shared-internal-key"            # 服务间调用的 X-Internal-Key
```

> `tirtc_aichat.base_role_url` 缺省时回退到 `tirtc_aichat.base_url`。角色详情全部存于探鸽云端，本地 `ai_device_role` 表只记录设备→角色绑定。

### call-server

```yaml
server:
  http_port: 9005

database:
  dsn: "user:pass@tcp(127.0.0.1:3306)/tirtc_thing_connect?parseTime=true"

redis:
  addr: "127.0.0.1:6379"

mqtt:
  broker: "mqtts://your-broker:8883"
  client_id: "callsrv_main"
  password: "your-mqtt-password"

jwt_secret: "shared-secret"   # 与其他四个服务相同

tirtc:
  app_id: "your-tirtc-app-id"
  access_key_id: "your-access-key-id"
  secret_key_id: "your-secret-key-id"

service:
  max_contacts_per_device: 200   # 单设备联系人上限
  room_ttl_hours: 12             # 房间 Redis key 兜底 TTL

call:
  internal_key: "your-internal-service-key"   # 与 user/ai/voip-server 共用的解绑清理密钥
```

---

## 构建与运行

```bash
# 构建
go build -o bin/device-server ./device-server/
go build -o bin/user-server   ./user-server/
go build -o bin/voip-server   ./voip-server/
go build -o bin/ai-server     ./ai-server/
go build -o bin/call-server   ./call-server/

# 或使用构建脚本（构建所有二进制 + 打包 dist/）
bash build.sh

# 手动分终端启动
./bin/device-server -c device-server/config.yaml
./bin/user-server   -c user-server/config.yaml
./bin/voip-server   -c voip-server/config.yaml
./bin/ai-server     -c ai-server/config.yaml
./bin/call-server   -c call-server/config.yaml
```

访问 H5 页面：`http://localhost:{user-server-port}/`

---

## 微信公众平台配置

在微信公众平台「开发 → 基本设置」将服务器地址配置为：

```
https://your-domain.com/v1/voip/notification/{wx_app_id}
```

---

## 数据库设计

见 `internal/db/migrate.go`（权威来源，服务启动时自动执行），主要业务表如下：

| 表名 | 描述 |
|---|---|
| `users` | 注册用户（email + bcrypt 密码）；`bind_quota` 字段记录剩余设备配额 |
| `device_pool` | 全局 ID+KEY 池，运维预导入；`status`: 0=未分配 / 1=已分配 |
| `device_bind` | 当前绑定状态（物理标识 ↔ 逻辑设备 ↔ 用户）；`user_id=0` 无主；`assign` 区分 dynamic/preburn；`active_time` 首次上线时间 |
| `device_bind_log` | 绑定操作流水（只增不改，记录每次 bind/unbind/reset，含 assign） |
| `ai_user_role` | 用户创建的角色 ID 索引（角色详情存于探鸽云端） |
| `ai_device_role` | 设备→角色绑定（一个设备一个角色，ai-server 查询用） |
| `ai_user_resource` | 用户拥有的 MCP、设备插件、知识库及知识文件索引 |
| `voip_device_profile` | 设备媒体能力（JSON 存储） |
| `voip_device_auth` | 设备 ↔ 用户 VoIP 授权记录（wx_open_id + wx_app_id + wx_model_id），remark 为兼容读取保留的同步值 |
| `voip_user_profile` | 小程序用户统一联系人名称（同一 wx_open_id + wx_app_id 在所有设备上共用） |
| `call_contact` | 设备间联系人（一对已接受/待处理关系，`device_id_a < device_id_b` 字典序去重） |

> `user_device_pool` 和 `device_bind_relation` 两张旧表已废弃并删除（功能并入 `device_bind`）。`scripts/schema.sql` 已与 `migrate.go` 同步，全新安装可直接导入。

---

## EMQX 配置要点

- JWT 认证中「Secret 使用 Base64 编码」项须**关闭**，否则 HMAC 签名与 EMQX 验签不匹配
- 认证链顺序：**内置数据库（先）→ JWT（后）**，内置数据库处理服务自身连接，JWT 处理设备连接
- 建议 JWT Payload 添加 Claim `device_id = ${username}`，防止设备冒用其他设备的 token
- `temp_token` 须为 JWT 格式，随机 hex 字符串在 JWT 鉴权模式下会被 Broker 拒绝

---

## 二次开发

### 替换存储实现

`internal/store/store.go` 定义七个接口：`DeviceStore`、`UserStore`、`BindStore`、`CacheStore`、`RoleBindingStore`、`UserRoleStore`、`UserResourceStore`，默认实现在 `internal/store/mysql/`。替换时只需实现对应接口，在 `main.go` 中换掉构造函数即可，业务层无需改动。

### 调整业务参数

超时和限频参数通过 `config.yaml` 的 `service:` 节配置，无需改代码：

```yaml
service:
  code_ttl: 300s         # 验证码延长到 5 分钟
  token_expiry: 720h     # mqtt_token 有效期 30 天
  mqtt_ack_timeout: 10s  # MQTT 超时延长
```

新用户默认设备配额不由 `service.quota_per_user` 控制。以 MySQL 为例，可修改列默认值：

```sql
ALTER TABLE users
  MODIFY bind_quota INT NOT NULL DEFAULT 20 COMMENT '剩余可绑额度';
```

该操作只影响后续未显式填写 `bind_quota` 的新记录；已有用户配额需单独更新。

### 多小程序支持

在 `voip-server/config.yaml` 的 `wechat.apps` 下追加新 AppID 配置即可，无需改代码。

---

## 运行测试

```bash
# 单元测试（无需 DB/Redis）
go test ./internal/... -v

# 集成测试（需真实 MySQL + Redis）
go test ./tests/ -v -count=1 -timeout 90s
```
