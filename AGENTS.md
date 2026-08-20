# ThingConnect repository instructions

本文件适用于整个仓库，只记录 ThingConnect 项目特有、无法从单个配置文件直接推断的约束。它定义目标工程标准；现有代码与本文件不一致时视为待治理技术债。普通功能任务不因此扩张为全仓重构，但新增和改动代码不得继续加深偏差；显式架构优化任务必须使触及的完整业务切片收敛到本标准。更深目录中的 `AGENTS.md` 可为其目录树补充或覆盖规则。

## 架构边界

Go 服务向以下目标依赖方向收敛：

```text
*-server/main.go（组合根）
        ├── handler（HTTP / MQTT 传输 adapter）──→ 业务模块
        └── 基础设施 adapter ──实现──→ 业务模块拥有的 port interface
              ├── MySQL / Redis
              └── MQTT / HTTP / Mail / Captcha / TiRTC
```

- 模块应在真实 seam 后隐藏完整行为，以窄 interface 承载不变量、顺序、错误和性能约束。interface 由使用它的业务模块拥有；只有外部依赖或确实变化的 adapter 需要 seam，禁止增加只透传参数和返回值的浅包装。
- 各 `main.go` 只负责加载配置、创建并注入 adapter、注册路由、持有后台任务生命周期和优雅退出。
- Handler 只负责鉴权、请求解析、输入校验和响应转换。跨多步状态变化、权限、配额、幂等、重试和事务语义属于业务模块；Handler 不直接访问 SQL、Redis 或第三方 SDK。
- 设备身份、用户和绑定等共享用例保留在 `internal/service`。Call、VoIP、AI 等服务私有业务在重构时进入对应的 `internal/<domain>` 深模块；不要继续扩大通用 `internal/service` 或 Handler。
- SQL、事务、锁和 MySQL 错误转换属于 `internal/store/mysql` adapter。现有 `internal/store` 可承载真正跨域的稳定契约；新的服务私有 port interface 与消费它的业务模块共置，避免形成全局 store 上帝接口。
- 数据类型优先由所属业务模块拥有；`internal/model` 只保留确实跨模块共享且语义一致的模型。Admin 领域逻辑留在 `internal/admin`，Admin HTTP 层通过其业务模块 interface 和权限检查访问。
- 业务服务之间不导入彼此的可执行目录，也不直接改写其他服务拥有的数据；通过已有内部 HTTP、MQTT 或持久化 outbox 协作。Admin Server 的管理职责按其权限模型处理。
- H5、Admin Web、微信小程序、Python/C/ESP32 设备示例都是服务端协议的消费者。服务端契约变化必须搜索并同步检查所有消费者。
- 设备示例用于展示协议和接入流程，不等于量产实现。修改 Linux C 产品适配层时保留 `DeviceAdapterV1` seam；平台、SDK ABI、资源和会话生命周期不得泄漏到业务协议模块。

### 渐进收敛标准

- 普通功能或缺陷任务只治理触及的业务切片；保持现有公开契约，不顺带迁移无关模块。无法在本任务安全消除的旧偏差应明确报告，不复制到新代码。
- 显式架构优化任务以完整用例为单位：Handler 退回传输职责，业务规则进入一个深模块，持久化和外部调用移到 adapter，组合根完成注入，调用方与测试只通过模块 interface。旧路径和重复实现必须在同一切片中删除。
- 重构完成标志是复杂度从调用方消失并集中到模块，而不是新增一层转发；相关行为、错误、并发与集成测试全部通过，且无兼容性回归。

## 按改动读取契约

- 业务 HTTP 接口、鉴权、字段或错误：完整阅读 `thing-connect/api-reference.md` 和 `thing-connect/error-response-policy.md`；Admin 接口另读 `thing-connect/admin/admin-server/API.md`。
- 配置、动态配置、服务发现、部署或数据库：阅读 `thing-connect/deployment.md`、`thing-connect/admin/admin-server/README.md`、相关 `config.yaml.example`、`thing-connect/internal/config`、`thing-connect/internal/db/migrate.go`、`thing-connect/internal/admin/config_registry.go` 和 `thing-connect/scripts/schema.sql`。
- 设备上线、MQTT 或 TiRTC 信令：阅读相应 `thing-connect/device-*.md`；涉及业务互斥、资源抢占、迟到回调或会话代次时，阅读 `thing-connect/device-session-model.md` 和 `thing-connect/device-session-arbiter.md`。
- Admin 产品行为、RBAC 或运维任务：阅读 `thing-connect/admin/admin-server/README.md`、`thing-connect/admin/admin-server/API.md` 和 `thing-connect/internal/admin` 中相邻测试。
- Python、Linux C 或 ESP32：先读 `thing-connect/device-sim/` 下对应示例目录的 `README.md`。SDK 头文件是 SDK 接口事实源，服务端文档与集成测试是业务协议事实源。
- 构建、依赖或检查命令：以 `.github/workflows/ci.yaml`、`thing-connect/.golangci.yml`、各 `package.json`、`thing-connect/build.sh` 和 `thing-connect/device-sim/device-sim-c/Makefile` 为准。

## 项目契约与稳定性

- 公开 JSON 字段、HTTP 状态、业务码、鉴权方式、MQTT topic/payload、Redis key/TTL、服务发现字段和配置默认值都属于兼容性契约。除非需求明确允许破坏性变化，保持现有消费者可继续工作。
- 客户端按数值 `code` 处理业务，不按 `msg` 文本分支。`msg` 使用可操作的简体中文；SQL、Redis、MQTT、上游 SDK、内网地址、堆栈和密钥等原始信息只进入脱敏日志。
- 所有内部接口，包括 `/v1/internal/*` 与 `/v1/{ai,voip,call}/internal/*`，只用于服务间调用，使用共同的 `internal.key`，不进入公网代理。五个业务服务共享 `jwt_secret`，六个服务共享 `internal.key`，Admin 使用独立的 `admin.jwt_secret`。
- MySQL 8 是数据库语义基线。数据库集成测试使用真实 MySQL/Redis 和库名以 `_test` 结尾的专用数据库，不能用 SQLite 替代。
- Schema 变化同时更新有序、可重复执行的迁移与 `thing-connect/scripts/schema.sql`。迁移保留既有数据；应用运行账号只依赖 DML 权限，DDL 由迁移账号执行。
- 多表、配额或所有权不变量必须在同一数据库事务内完成，并用唯一约束、条件更新或锁处理并发。事务中不等待 HTTP、MQTT、邮件等远端副作用。
- 数据库提交与跨服务副作用需要一致时使用事务 outbox。异步调用必须有超时、幂等或去重语义以及有界退避；关键状态传播不能降级成只记日志的 best effort。
- 新增或修改动态配置时同步处理 YAML 回退值、严格解析、Admin 注册表的命名空间/范围/默认值/密钥属性、运行时原子应用、revision 可观测性以及未配置 Admin 时的行为。
- 后台任务必须由服务组合根持有取消路径并在退出时释放；HTTP、MQTT、Redis、TiRTC 回调和设备会话变化需覆盖超时、重复、乱序、迟到回调、部分失败和重启恢复。
- `/health/live` 只表示进程存活；`/health/ready` 反映当前服务的必需依赖。新增必需依赖时同步 readiness、服务状态上报和关闭顺序。
- 不提交真实密钥、令牌、用户数据、设备凭证、证书或生产配置。修改日志、中间件和错误响应时检查请求体、验证码、邮箱、设备密钥和第三方凭证的脱敏。
- 修改 npm 依赖要同步对应 lockfile；修改 Tailwind 源文件后通过脚本生成 CSS。预编译 TiRTC SDK、WASM、压缩 JS 和其他第三方二进制只作为外部产物使用，除非任务明确要求替换指定版本。

## 验证矩阵

先运行修改范围内的测试，再按影响面执行以下门槛。下表命令除非另有说明，均从 `thing-connect/` 执行：

| 改动范围 | 最低验证 |
|---|---|
| Go 服务与共享包 | 对自改 Go 文件运行 `gofmt`；在 `thing-connect/` 运行相关 `go test` 和 `go vet ./...`；具备 MySQL/Redis 时运行 `THING_CONNECT_TEST_CONFIG=tests/testdata/config.yaml go test ./... -p 1 -count=1` |
| Go 组合根、依赖或构建 | 上述检查，加 `./build.sh <受影响服务>`；跨服务或发布级改动运行完整 `./build.sh` |
| Admin Web | `npm --prefix admin/admin-web run check` |
| 用户 H5 / Tailwind | `npm run build:css`，并检查生成 CSS 只有预期差异 |
| 微信小程序工具逻辑 | `node --test weixin-mini-program/tests/*.test.js`；页面生命周期变化再用微信开发者工具验收 |
| Python 模拟器 | 在 `device-sim/device-sim-py/` 运行 `python -m unittest discover -p 'test_*.py'`；SDK、硬件和媒体场景单独说明 |
| Linux C 参考实现 | 在 `device-sim/device-sim-c/` 运行 `make WERROR=1 test`；适配接口变化再运行 `make WERROR=1 framework adapter-template-check`，生命周期风险按该目录 README 运行 sanitizer |

Go 集成测试会在本地依赖不可用时跳过，但 CI 中缺少测试配置或依赖必须失败。报告验证结果时单独列出 `Skip` 和缺失的 SDK、硬件、浏览器或外部服务，不能把关键用例全部跳过视为完整通过。

## 对外文档

- `README.md`、接入指南、API Reference 和各示例目录的说明文档面向使用者与二次开发者，只描述稳定的当前行为、使用步骤、接入约束、验收标准和故障排查。
- 使用当前时态表达，不写某次提交“新增了什么、修复了什么、改成了什么”，不使用“本次修改、已经修复、现已支持、不再”等变更记录口吻。
- 版本差异、修复清单、性能对比和提交历史只进入 CHANGELOG、release notes 或用户明确指定的发布文档；未被要求时不创建变更日志。
- 只有公开接口、配置、命令、操作流程、兼容性、接入约束或用户可见行为变化时才同步对应使用文档。内部重构、测试补充和不改变使用方式的缺陷修复通常不改对外文档。
- API Reference 只在请求/响应字段、鉴权、错误码、接口语义或兼容性契约变化时修改；已有接口仅新增前端入口时，不重复改写接口定义。
- 提交前逐段检查文档差异：每段都应回答“使用者怎么用”或“二次开发必须遵守什么”。只能回答“这次代码改了什么”的内容应删除或放入明确要求的发布文档。
