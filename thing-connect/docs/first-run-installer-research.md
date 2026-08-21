# ThingConnect 首次启动 Web 安装器调研与设计建议

> 状态：设计调研，不是当前行为或实现说明
> 调研日期：2026-08-21
> 适用范围：当前 Supervisor 部署、两个必需业务服务、三个可选业务服务、一个 Admin Server、MySQL 8、Redis 7 和 MQTT

## 结论

建议把首次安装实现成一个不依赖 MySQL、Redis、MQTT 的独立安装模式，由一次性 Web 向导采集启动引导配置，再通过受限的本机控制接口完成数据库初始化、配置发布和 Supervisor 启动。安装器不能复用正常 Admin Server 的运行入口，因为 Admin Server 在监听 HTTP 前已经需要配置文件、MySQL、迁移和 Redis。

核心约束如下：

1. 数据库不存在时才创建数据库；数据库存在且为空时初始化 ThingConnect 表。
2. 已有 ThingConnect 数据库不删除、不重建、不清空、不覆盖数据，只在识别出合法旧版本并展示迁移计划后执行缺失的版本化迁移。
3. 非空但不能确认属于 ThingConnect、结构漂移、部分未知、版本高于当前程序的数据库一律停止，不能提供“强制继续”按钮。
4. 安装过程采用可恢复状态机。MySQL 8 的原子 DDL 只保证单条 DDL 原子，DDL 会隐式提交，整次安装不能用一个事务整体回滚。[MySQL 原子 DDL](https://dev.mysql.com/doc/refman/8.4/en/atomic-ddl.html)
5. 同机并发用现有 `${DEPLOY_ROOT}/deploy.lock` 的文件锁隔离，跨节点并发再用 MySQL `GET_LOCK()` 隔离；锁连接丢失或数据库切主时立即中止并重新检测，不继续执行后续步骤。[MySQL `GET_LOCK()`](https://dev.mysql.com/doc/refman/8.4/en/locking-functions.html)
6. 配置和密钥先生成到同文件系统的暂存目录，校验 Admin、两个必需服务及所选可选服务的配置后再原子发布；已经生成的密钥在重试时保持不变。
7. 先启动 Admin 并通过 `/health/ready`，再启动两个必需业务服务和用户选择的可选服务并逐个检查 readiness。Supervisor 的 `RUNNING` 只能作为进程状态，不能替代应用 readiness。
8. “关闭安装入口”和“必需及所选服务全部就绪”使用两个状态：一旦数据库、首个管理员和配置已经安全提交，安装入口立即永久锁定；服务启动失败只开放持有原安装令牌的恢复页，不重新开放初始化表单。

## 本项目现状与边界

当前代码和部署已经具备以下可复用能力：

- `internal/store/mysql/migrate.MigrateAdmin` 统一执行 core 与 admin 版本化迁移，`schema_migrations` 记录版本。
- `admin-server -migrate-only`、`admin-server -init-admin` 已有迁移和首个管理员入口。
- `scripts/deploy-prod.sh` 已使用 `${DEPLOY_ROOT}/deploy.lock` 的 `flock` 防止同机并发发布。
- 发布脚本先处理 Admin，再处理 `device-server`、`user-server`、`voip-server`、`ai-server`、`call-server`，并检查每个服务的 `/health/ready`。
- 已安装服务共享 MySQL、Redis 和 `internal.key`；已安装业务服务共享 `jwt_secret`；Admin 使用独立的 `admin.jwt_secret` 和 MFA 加密密钥。
- 五个业务服务启动时必须先从 Admin 加载动态配置。因此 Admin 可用是业务服务的启动前置条件。

需要在实现安装器时收敛的技术债：

- 生产安装由安装器或 `-migrate-only` 使用 DDL 账号串行迁移；常驻服务只检查 schema 兼容性，保持应用账号只有 DML 权限。
- 当前迁移是“查询版本后逐条执行再写版本”的流程。虽然多数语句具有重复执行保护，但尚没有数据库级迁移互斥；安装器不能依赖多个服务同时迁移来完成初始化。
- 当前 Supervisor 示例让六个进程以 `root` 运行。Web 安装器不能获得任意 root 命令能力；服务应改用专用系统用户，Supervisor Unix socket 只授予一个受限本机控制组件。

## 可借鉴系统

### Gitea

Gitea 在 `INSTALL_LOCK=true` 时关闭安装页；安装流程先测试数据库，执行迁移，生成内部令牌、JWT 密钥和全局密钥，保存配置后切换到正常 Web 服务。[Gitea 配置说明](https://docs.gitea.com/1.24/administration/config-cheat-sheet/#security-security)、[Gitea 安装源码](https://github.com/go-gitea/gitea/blob/main/routers/install/install.go)

Gitea 还会根据旧用户和迁移版本判断数据库可能被使用过。但它允许用户多次确认后继续“重新安装”，这不符合 ThingConnect 的数据库保护要求；ThingConnect 应在陌生非空库上硬拒绝，而不是增加一个危险确认按钮。[Gitea 数据库检测源码](https://github.com/go-gitea/gitea/blob/main/routers/install/install.go#L1515-L1605)

可借鉴：

- 安装锁是独立、持久的安全状态。
- 先测试依赖和完整校验输入，再迁移和切换运行模式。
- 只在密钥不存在时生成，不能在重试时覆盖已有密钥。

不直接照搬：

- 不允许向非空未知数据库强制安装。
- 不把普通配置保存函数当作崩溃原子的多文件提交。
- 不在安装未完全提交前让常驻应用进入正常服务模式。

### Nextcloud

Nextcloud 的向导可填写管理员和数据库信息；数据库不存在且账号具有权限时会创建数据库，还可以尝试创建权限受限的专用数据库用户。[Nextcloud 安装向导](https://docs.nextcloud.com/server/stable/admin_manual/installation/installation_wizard.html)

Nextcloud 在安装末尾才写入 `installed=true` 并移除 `CAN_INSTALL`，说明“已安装”应是显式完成状态，而不应由“配置文件存在”或“某张表存在”推断。[Nextcloud Setup 源码](https://github.com/nextcloud/server/blob/master/lib/private/Setup.php#L603-L612)、[Nextcloud installed 参数](https://github.com/nextcloud/server/blob/master/config/config.sample.php)

其自动配置文件只在首次安装使用，应用后自动删除，适合借鉴为“一次性引导输入不能成为长期第二配置源”。[Nextcloud 自动配置](https://docs.nextcloud.com/server/stable/admin_manual/configuration_server/automatic_configuration.html)

Nextcloud 的 `config.php` 写入使用文件锁、截断和重写，不提供跨崩溃的临时文件原子替换；ThingConnect 应采用更严格的 temp + sync + rename 方案。[Nextcloud Config 源码](https://github.com/nextcloud/server/blob/master/lib/private/Config.php)

### GitLab Linux package（Omnibus）

GitLab 的 `reconfigure` 把用户配置、默认值和生成的密钥收敛为可重复执行的配置过程；生成的密钥写入 `/etc/gitlab/gitlab-secrets.json`，后续运行复用，在多节点间必须保持一致。[GitLab reconfigure 说明](https://docs.gitlab.com/omnibus/development/reconfigure_in_detail/)

GitLab 还明确区分只读代码、可写数据、日志和用户编辑的配置，并要求配置与密钥单独备份。[GitLab Omnibus 目录约定](https://docs.gitlab.com/development/omnibus/)、[GitLab 配置备份](https://docs.gitlab.com/omnibus/settings/backups/)

可借鉴：

- 生成一次、长期复用的密钥集合；多实例读取同一组值。
- 用户输入是声明式源，生成文件不可由用户和程序同时维护。
- 配置、密钥、数据和日志分目录并独立备份。

ThingConnect 不需要引入完整 Chef/Cinc 模型，但安装步骤必须幂等，安装计划和结果必须可审计。

## 推荐架构

```text
浏览器（一次性 token + CSRF）
              │
              ▼
installer-web（普通用户、无业务依赖、只处理固定 DTO）
              │ 本机 Unix socket；固定方法，不接受 shell 字符串
              ▼
installer-controller（部署用户）
   ├── deploy.lock / 安装状态日志
   ├── MySQL 建库、分类、GET_LOCK、迁移
   ├── 已选择服务的配置 bundle 原子发布
   └── Supervisor：Admin → readiness → 五业务服务
```

`installer-controller` 只暴露“检测依赖、执行给定安装计划、查询状态、恢复当前安装”四类操作。Web 请求不能传命令、可执行文件路径、部署根目录或 Supervisor 程序名。部署根目录、Supervisor 组名、安装监听地址由包安装或本机启动参数确定。

同机简单部署可以把两部分编译进同一二进制，但权限边界仍应保留：进程不能以 root 运行，Supervisor 通过权限受限的 Unix socket 控制。Supervisor 官方 API 可以按明确名称查询、启动和停止进程；Unix socket 可以设置属主和权限。[Supervisor XML-RPC API](https://www.supervisord.org/api.html)、[Supervisor Unix socket 配置](https://docs.supervisord.org/configuration.html#unix-http-server-section-settings)

## 安装状态机

安装状态不能只保存在浏览器 session。建议同时使用：

- 主机状态文件：`${DEPLOY_ROOT}/var/install-state.json`，权限 `0600`，记录 `install_id`、阶段、配置 bundle 哈希、错误码和更新时间，不记录密码。
- 数据库表：`thingconnect_installation_state`，仅在确认目标数据库为空或已确认属于 ThingConnect 后创建，记录同一 `install_id`、状态、schema 版本和递增 `generation`。
- 最终锁文件：`${DEPLOY_ROOT}/var/installed`，权限 `0400`，只在安全提交后创建。

状态建议为：

```text
uninitialized
  → validating
  → database_claimed
  → schema_ready
  → admin_ready
  → config_committed
  → install_locked
  → starting_admin
  → starting_services
  → ready
```

任一步失败写入 `failed_at=<阶段>`，重试从持久状态重新读取并验证已完成步骤，而不是盲目从头执行。`generation` 在每次取得恢复所有权时递增，用于阻止旧控制器在失去数据库连接后继续执行文件发布或 Supervisor 操作。

`install_locked` 与 `ready` 分离：配置与首个管理员提交后立即关闭普通安装入口；服务启动失败仍可使用原一次性令牌查看错误并执行“继续启动”，但不能修改数据库目标、重新生成密钥或再次创建管理员。

## 数据库分类与处理矩阵

安装器先连接 MySQL 实例而不选择目标数据库，用 `INFORMATION_SCHEMA.SCHEMATA` 判断数据库是否存在，再用 `INFORMATION_SCHEMA.TABLES` 读取目标库的真实表名。MySQL 官方分别定义了这两个元数据表；只在具备可见权限时才能安全分类。[SCHEMATA](https://dev.mysql.com/doc/refman/8.4/en/information-schema-schemata-table.html)、[TABLES](https://dev.mysql.com/doc/refman/8.4/en/information-schema-tables-table.html)

| 检测结果 | 行为 | 是否自动修改 |
|---|---|---|
| 数据库不存在 | 在安装锁内执行 `CREATE DATABASE ... CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci`，随后初始化 | 是 |
| 数据库存在、无任何 base table | 写入安装状态并执行全量版本化迁移 | 是 |
| 有安装状态且属于同一个未完成安装 | 校验 `install_id`/generation/已完成版本后向前恢复 | 是，仅缺失步骤 |
| 有合法 `schema_migrations` 和 ThingConnect 核心表，版本等于当前 | 不改 schema；校验漂移后进入配置/启动恢复 | 否 |
| 可确认是 ThingConnect，版本低于当前 | 显示迁移版本、备份要求和预计 DDL；确认后只执行缺失迁移 | 是，仅版本化迁移 |
| schema 版本高于当前程序 | 拒绝，要求使用同版本或更高版本程序 | 否 |
| 有外部表但没有 ThingConnect 指纹 | 拒绝，要求更换数据库名 | 否 |
| 只有部分 ThingConnect 表但没有合法安装状态或迁移记录 | 标记结构不一致，转人工恢复 | 否 |
| 版本记录完整但预期表、列或索引缺失 | 标记 schema drift，转人工恢复 | 否 |
| 无权限读取完整元数据 | 无法证明安全，拒绝 | 否 |

MySQL `CREATE DATABASE IF NOT EXISTS` 只能避免“已存在”错误，不能证明已有同名库适合 ThingConnect，因此不能用它代替上述分类。[MySQL `CREATE DATABASE`](https://dev.mysql.com/doc/refman/8.4/en/create-database.html)

数据库名不是 SQL 参数，产品层只接受 1–64 个 ASCII 字母、数字和下划线，并由专用 identifier quote 函数生成 SQL；密码和 DSN 不拼接到日志。

推荐提供两种账号模式：

- 推荐：输入一次性的建库/迁移账号，安装器为新数据库创建专用应用账号并生成随机密码；长期服务配置只保存 DML 应用账号。
- 受管数据库：用户分别输入已准备好的迁移账号和应用账号。迁移账号只用于本次安装或受保护的升级配置，不能复制到五个业务服务配置。

对于已有数据库，不自动改数据库用户或授权。MySQL 官方建议账号只拥有所需权限；`CREATE USER` 需要额外全局权限，且包含明文密码的语句可能进入服务器日志或客户端历史。[MySQL 权限指南](https://dev.mysql.com/doc/refman/8.4/en/privileges-provided.html#privilege-guidelines)、[MySQL `CREATE USER`](https://dev.mysql.com/doc/refman/8.4/en/create-user.html)

## 数据库锁和迁移

在连接到目标 MySQL 主写节点后，固定一条专用连接并执行：

```sql
SELECT GET_LOCK('thingconnect.install.<database-name>', 10);
```

只有返回 `1` 才能继续。每个重要阶段前后校验锁仍由当前连接持有，结束时显式 `RELEASE_LOCK()`。`GET_LOCK()` 是协作式、会话级排他锁，连接关闭时自动释放，不随事务提交释放；等待者的获得顺序未定义。[MySQL 锁函数](https://dev.mysql.com/doc/refman/8.4/en/locking-functions.html)

限制：`GET_LOCK()` 只约束单个 `mysqld`，不适用于多写入口或 NDB。使用 MySQL HA 代理时，安装流量必须固定到唯一主写节点；切主导致连接断开时，本轮安装立即失败，重连后重新获取锁、递增 generation 并重新分类，不能从内存中的下一步继续。

迁移执行要求：

- 安装器持有数据库锁时调用共享的 Go 迁移模块，不通过 shell 拼接命令。
- 每个迁移版本在执行前、每条 DDL 后和写入 `schema_migrations` 后记录阶段。
- 使用有界 `lock_wait_timeout`，避免被长事务的 metadata lock 无限阻塞；超时后显示可操作错误，不杀死业务会话。MySQL 说明事务持有的 metadata lock 会阻塞 DDL，默认等待时间可能很长。[MySQL metadata locking](https://dev.mysql.com/doc/refman/8.4/en/metadata-locking.html)、[`lock_wait_timeout`](https://dev.mysql.com/doc/refman/8.4/en/server-system-variables.html)
- 不在失败时执行 `DROP DATABASE`、`DROP TABLE` 或反向猜测 DDL。MySQL 8 只保证每条受支持的 InnoDB DDL 原子，整批 DDL 不是事务。[MySQL 原子 DDL](https://dev.mysql.com/doc/refman/8.4/en/atomic-ddl.html)
- 升级已有大表前单独评审 `ALGORITHM`/`LOCK`、磁盘空间和耗时；即使 online DDL 也需要 metadata lock，可能被长事务阻塞。[MySQL online DDL](https://dev.mysql.com/doc/refman/8.4/en/innodb-online-ddl-performance.html)

## 配置与密钥发布

向导只收集启动引导参数：

- MySQL 连接及数据库名；
- Redis 地址、密码和 DB；
- MQTT Broker、认证模式和凭证；
- 对外 HTTPS/MQTTS 地址、可信代理和服务端口；
- 首个管理员邮箱、昵称和密码；
- 是否使用安全 Cookie。

TiRTC、SMTP、人机验证、微信应用、AI 默认资源等业务配置不属于启动必填项，服务启动后在 Admin 配置中心填写。

以下值由安装器使用 Go `crypto/rand` 自动生成，不能要求用户手工复制：

- 五个业务服务共享的 `jwt_secret`；
- 六个服务共享的 `internal.key`；
- 独立的 `admin.jwt_secret`；
- Base64 编码的 32 字节 Admin MFA 加密密钥；
- `install_id`、一次性安装令牌及必要的数据库应用密码。

Go 官方将 `crypto/rand` 定义为密码学安全随机源，并明确建议密钥使用 `crypto/rand` 而不是 `math/rand`。[Go `crypto/rand`](https://pkg.go.dev/crypto/rand)、[Go Code Review：Crypto Rand](https://go.dev/wiki/CodeReviewComments#crypto-rand)

推荐使用单一、带版本的配置 bundle：

```text
${DEPLOY_ROOT}/config-releases/<install_id>/
  ├── manifest.json       # 版本、非敏感哈希、生成时间
  ├── admin-server.yaml
  ├── device-server.yaml
  ├── user-server.yaml
  ├── voip-server.yaml
  ├── ai-server.yaml
  └── call-server.yaml
${DEPLOY_ROOT}/config-current -> config-releases/<install_id>
```

Supervisor 中六个程序读取 `config-current` 下对应文件。发布流程为：

1. 在目标同一文件系统创建私有暂存目录，目录 `0700`、文件 `0600`。
2. 从一个强类型 `InstallSpec` 渲染六份 YAML，禁止字符串替换。
3. 使用现有 `LoadFile`/`LoadAppConfig` 完整解析，再校验共享 DSN、Redis、JWT、internal key、URL、端口无冲突和占位值。
4. 对每个文件执行写入、`Sync`、关闭；对目录执行 `Sync`。
5. 原子重命名 bundle，再原子切换 `config-current` 指针并同步父目录。
6. 已有目标 bundle 或现有手写 `config.yaml` 时不覆盖；内容相同可恢复，内容不同要求 CLI 人工接管并先备份。

Linux `rename` 在同一文件系统内可原子替换目标；文件 `fsync` 不能保证目录项已经持久化，重命名后还需要同步目录。[Linux `rename(2)`](https://man7.org/linux/man-pages/man2/rename.2.html)、[Linux `fsync(2)`](https://man7.org/linux/man-pages/man2/fsync.2.html)

服务配置包含明文基础设施凭证，因此备份、日志和安装状态接口都必须脱敏。GitLab 的经验也表明，生成密钥必须跨 reconfigure 和多节点保持一致，配置与密钥需要纳入独立备份。[GitLab secrets](https://docs.gitlab.com/omnibus/development/reconfigure_in_detail/#handling-of-secrets)

## 完整执行顺序

1. **启动安装模式**：包安装只注册 installer；六服务组尚未加入 Supervisor 活动配置，避免因缺配置反复 BACKOFF。
2. **认证向导**：校验一次性安装令牌，创建短时 HttpOnly/Secure/SameSite session 和 CSRF token。
3. **预检本机**：检查部署目录权限、磁盘空间、六个二进制和静态资源、端口占用、Supervisor socket 与程序白名单。
4. **预检依赖**：以独立超时测试 MySQL、Redis 和 MQTT；Redis 除 `PING` 外用带 TTL 的随机命名键验证读写并清理；MQTT 使用独立临时 ClientID 连接后断开。
5. **生成计划**：只读分类数据库，展示“创建新库、初始化空库、恢复安装、接管已有 ThingConnect、拒绝”的确定结果；任何写入前再次确认。
6. **取得锁**：先取得与发布脚本相同的主机文件锁，再在固定 MySQL 连接上取得命名锁。锁顺序固定，防止死锁。
7. **声明安装所有权**：数据库不存在则创建；空库写入安装状态；恢复库校验 install_id/generation。
8. **准备配置**：生成或读取本次安装已有密钥，渲染、校验和持久化暂存 bundle。
9. **迁移 schema**：调用共享 Go 迁移模块；只向前记录完成的版本，不做破坏性回滚。
10. **初始化 Admin 数据**：在事务内 seed RBAC 默认值并创建唯一首个超级管理员；若已有管理员，转入“接管已有安装”，绝不能创建第二个首管。
11. **发布配置**：原子切换配置 bundle，记录配置哈希和 `config_committed`。
12. **永久锁定普通安装**：写数据库和本机安装锁，销毁普通初始化 session；后续只允许原 token 恢复启动。
13. **注册并启动 Supervisor 服务组**：原子安装最终 Supervisor 配置，`reread/update`；先启动 Admin。
14. **验收 Admin**：同时要求 Supervisor 稳定 `RUNNING` 和 Admin `/health/ready` 成功。
15. **启动业务服务**：按当前发布顺序逐个启动并检查 readiness；失败时停止本轮已启动的业务服务，不停止已经验证健康的 Admin，记录失败点。
16. **完成**：六服务全部 ready 后写 `ready`，删除一次性令牌原文，停止 installer，浏览器跳转到 `/admin/`。

Supervisor 官方区分“启动进程并等待进入 RUNNING”和进程自身业务健康，因此安装器应继续使用本项目的 HTTP readiness 门槛。[Supervisor process control](https://www.supervisord.org/api.html#process-control)

## 并发、高可用与故障恢复

### 并发安装

- 同一主机：复用 `deploy.lock`，安装和 `deploy-prod.sh` 互斥。安装器不能直接调用会再次取得同一锁的脚本，否则会自锁；应抽取共享部署库或直接使用受限 Supervisor API。
- 多个安装器指向同一 MySQL：数据库命名锁决定唯一执行者，其他页面只显示“另一个安装正在进行”。
- 同时提交：安装 POST 带一次性 idempotency key；服务端按 install_id 返回同一任务，不创建第二个任务。
- 锁连接断开：立即取消 context，不发布配置、不启动服务；重新连接后重新分类和恢复。

### MySQL 高可用

- 安装只支持单主写入口。代理切主导致原会话锁释放时，本轮失败并进入恢复，不在新主上静默续跑。
- 跨地域多主、NDB 或可将两个写连接路由到不同 `mysqld` 的拓扑不以 `GET_LOCK()` 保证正确性，必须由外部部署编排提供全局互斥。
- 运行中升级与首次安装使用同一个数据库锁名和迁移引擎，避免发布脚本与安装器并发 DDL。

### Redis 高可用

Redis 是待配置的依赖，不能作为首次建库和迁移的唯一协调器；即使可用，安装正确性仍以 MySQL 主写节点和持久安装状态为准。Redis 只用于连接/读写预检和服务运行期能力。这样也避免 Sentinel/Cluster 切换窗口影响安装所有权。

### 失败处理

| 失败阶段 | 自动处理 | 重试方式 |
|---|---|---|
| 依赖预检前 | 无持久修改 | 修正输入后重试 |
| 已建新数据库但未建表 | 保留空库和安装状态，不删除 | 同 install_id 继续 |
| 迁移中断 | 保留单条已提交 DDL和版本日志 | 重新加锁、重新分类、幂等向前 |
| 首管事务失败 | 回滚该事务，不回滚 schema | 修正管理员输入后重试 |
| 配置暂存失败 | 删除未发布 temp；保留已生成密钥的私有恢复文件 | 原密钥继续 |
| 配置已发布、Admin 启动失败 | 不开放初始化，不改数据库；保留旧/当前 bundle | 原 token 查看日志并继续启动 |
| 部分业务服务启动失败 | 停止本轮新启动的业务服务，Admin 保持可诊断 | 修复依赖后继续 |
| 浏览器断开 | 后台任务继续，重新打开显示持久进度 | 不重复提交 |
| 安装器进程崩溃 | 文件锁和 MySQL 会话锁释放，持久状态保留 | 新进程递增 generation 后恢复 |

“自动回滚”只适用于未发布的临时文件和本轮启动的进程；数据库 DDL 不做自动删除式回滚。这是保护已有数据和支持崩溃恢复的关键。

## Web 安全控制

- 默认只监听 `127.0.0.1`，通过 SSH 端口转发打开；需要公网安装时必须由 Nginx 提供 TLS、来源 IP allowlist 和严格 Host 校验。
- 包安装时生成至少 128 bit 的一次性 token，控制台只显示一次；服务端只保存哈希。成功后删除 token，普通安装接口统一返回 404/410。
- 所有修改请求使用 POST、CSRF token 和 Origin/Host 校验；OWASP 推荐服务端 synchronizer token，并指出未登录表单同样需要 CSRF 防护。[OWASP CSRF](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)
- 登录令牌、数据库/Redis/MQTT 密码和首管密码只能通过 TLS；密码框默认隐藏，可临时显示，但响应和状态查询永不返回原值。[OWASP Authentication](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)
- 对 token 校验、依赖测试和提交操作分别限速；失败使用有界退避。设置请求体上限、连接/读写超时和任务总超时。
- 设置 `Cache-Control: no-store`、CSP `default-src 'self'`、`frame-ancestors 'none'`、`X-Content-Type-Options: nosniff` 和 `Referrer-Policy: no-referrer`。
- 依赖测试是受保护的 SSRF 能力。只接受结构化 host/port，不接受 URL 路径、shell、Unix 任意文件路径；禁止云 metadata、组播、广播和不符合部署策略的地址。
- 日志只记录 install_id、阶段、耗时、目标主机脱敏值、数据库名、错误分类和 correlation ID；禁止记录 DSN、Authorization、Cookie、密码、密钥、完整 SQL 或请求体。
- Supervisor 控制只允许固定组中的六个程序和 installer 自身；不要把未认证的 TCP XML-RPC 暴露到网络。

## 建议的数据与接口契约

安装 API 应异步化，避免浏览器请求承担长时间 DDL：

```text
GET  /v1/install/status
POST /v1/install/session          # 一次性 token → 短时 session
POST /v1/install/preflight        # 只读/临时探测
POST /v1/install/plan             # 返回数据库分类和将执行的步骤
POST /v1/install/runs             # idempotency key 创建任务
GET  /v1/install/runs/{install_id}
POST /v1/install/runs/{install_id}/resume
```

响应只返回阶段、百分比、开始/更新时间、稳定错误码和可执行建议，不返回原始基础设施错误。安装任务应提供结构化审计事件，例如：

```text
INSTALL_PREFLIGHT_OK
INSTALL_DATABASE_CREATED
INSTALL_SCHEMA_VERSION_APPLIED
INSTALL_ADMIN_CREATED
INSTALL_CONFIG_COMMITTED
INSTALL_SERVICE_READY
INSTALL_FAILED
INSTALL_COMPLETED
```

## 验收与故障注入矩阵

实现不能只测成功路径，最低需要覆盖：

- 数据库不存在、空库、当前 ThingConnect 库、旧版本库、未来版本库、陌生非空库、混合表、结构漂移库。
- 两个浏览器同时提交、两个安装器节点同时提交、安装与发布脚本同时运行。
- 每条迁移前后进程崩溃、MySQL 连接断开、主库切换、metadata lock 超时。
- 六文件每一步写入失败、磁盘写满、`fsync`/rename 失败、已有配置冲突、安装器重启后密钥一致。
- Redis 只读、认证失败、超时；MQTT TLS/认证失败和 ClientID 冲突。
- Admin 启动失败、Admin ready 超时、每个业务服务单独失败、Supervisor 报 RUNNING 但 readiness 失败。
- 安装完成后旧 token、CSRF 重放、跨 Origin 请求、无 TLS 公网访问和强制访问安装 API。
- 日志、审计、HTTP 错误、Supervisor stderr 中没有基础设施密码和生成密钥。
- 完成后重启主机：Supervisor 自动拉起 Admin 和五业务服务，installer 不再监听。

## 分阶段实现建议

1. **安装内核**：提取 schema 检查/迁移、首管初始化、配置渲染与原子 bundle、安装状态机；先提供 CLI 并完成故障注入测试。
2. **本机编排**：增加受限 Supervisor controller、专用系统用户和安装期/正常期 Supervisor 模板。
3. **Web 向导**：实现 token、CSRF、依赖预检、计划确认、异步进度和恢复页。
4. **HA 加固**：数据库锁、generation fencing、MySQL 切主测试、多节点密钥分发和备份恢复演练。
5. **交付验收**：在全新主机、已有当前库、旧版本库和各种中断点执行可重复安装，确认任何失败都不会删除或覆盖已有数据。

第一阶段应先把安装状态机和数据库保护做成可测试的深模块，再接 Web 页面。Web 向导只是传输层；数据库分类、幂等、密钥不变量、迁移顺序和恢复策略不能写在 Handler 或 shell 字符串中。
