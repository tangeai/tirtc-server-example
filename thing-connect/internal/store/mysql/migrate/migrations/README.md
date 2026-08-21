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

文件通过 `go:embed` 打包进二进制。执行器先完成目标库零写入识别，再持有
MySQL 命名锁，逐条执行 SQL，最后写入 `schema_migrations`。新增迁移只能追加
更高版本文件；不得修改已经发布版本的语义。为兼容已有数据库，五个业务领域
仍共同记录为 `core` 组件，Admin 记录为 `admin` 组件。
