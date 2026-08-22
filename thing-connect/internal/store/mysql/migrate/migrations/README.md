# 数据库迁移文件

数据库结构按拥有它的服务领域拆分为 SQL 文件，但只由首次安装器或
`admin-server -migrate-only` 统一执行。常驻业务服务不会在启动时执行 DDL。

- `core/001_user.sql`：用户账号。
- `core/001_device.sql`：设备、绑定和清理 outbox。
- `core/001_voip.sql`：VoIP 授权与资料。
- `core/001_ai.sql`：AI 角色与资源。
- `core/001_call.sql`：设备联系人。
- `admin/`：Admin Server 拥有的表及后续版本；`004_installation_state.sql`
  也是空库领取与中断恢复共用的唯一所有权标记定义。

文件通过 `go:embed` 打包进二进制。执行器启动时自动发现
`core/NNN_*.sql` 和 `admin/NNN_*.sql`，校验每个组件从 1 开始版本连续，按版本和
文件名顺序逐条执行，并从发现结果计算当前版本。首次安装和
`deploy-prod.sh update` 因而使用同一套迁移，不需要在 Go 代码或安装脚本中再登记
版本数字和文件路径。

新增表或修改表时：

1. 在所属组件中追加下一个连续版本的 `NNN_description.sql`；业务服务共享表使用
   `core`，Admin 私有表使用 `admin`。
2. 同步更新 `scripts/schema.sql`，使全新部署的基线结构与所有迁移执行后的结构一致。
3. 不修改已经发布版本的语义，不复用旧版本号；迁移需支持中断后安全重试并保留既有数据。
4. 运行 `go test ./internal/store/mysql/migrate`；测试会比较迁移与
   `scripts/schema.sql` 的完整表、列和索引结构，并拒绝版本缺口。

所有新增表必须提供表级 `COMMENT`，所有新增或修改字段必须提供准确的中文
`COMMENT`。字段注释说明业务含义、单位、枚举值和特殊默认值，不重复字段名。
历史结构的注释由 `core/003_schema_comments.sql` 和
`admin/005_schema_comments.sql` 回填，已安装数据库在正常版本升级迁移时获得相同
元数据。

执行器先完成目标库零写入识别，再持有 MySQL 命名锁，逐条执行 SQL，最后写入
`schema_migrations`。受管表清单也从当前迁移结构导出，因此新迁移增加的表会自动
进入安装所有权和结构校验。为兼容已有数据库，业务领域共同记录为 `core` 组件，
Admin 记录为 `admin` 组件。
