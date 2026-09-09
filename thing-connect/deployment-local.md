# 本地开发联调部署

[`scripts/deploy-local.sh`](scripts/deploy-local.sh) 将本地工作区部署到 `/opt/thing-connect`。
源码可以包含未提交的修改。脚本在独立快照中构建六个服务和 Web，不修改开发仓库的
Git 索引，也不拉取远程代码。它使用现有 Web 安装器创建数据库结构，使用
`service-local.sh` 管理进程，适用于 Linux / WSL 开发联调。

准备 Go、Node.js、npm、Git、curl、tar、util-linux，以及 MySQL 8、Redis。
使用统一 H5 入口时还需要 Nginx。完整依赖版本要求见 [部署指南](deployment.md)。
本地 HTTP 联调在安装页关闭 HTTPS Cookie；开机自启、证书和日志轮转不属于本地脚本。

## 首次部署

从当前源码仓库执行：

```bash
cd /home/workspace/tirtc-server-example
sudo bash thing-connect/scripts/deploy-local.sh install
```

`sudo` 环境需要能找到 `go` 和 `npm`；它不会自动继承用户版本管理器的路径。

安装目录已有实例时，普通 `install` 会拒绝覆盖。如果选择全新建立联调环境，先停止
旧服务，再显式归档旧目录：

```bash
sudo /opt/thing-connect/service-local.sh stop-all
sudo bash thing-connect/scripts/deploy-local.sh install --archive-existing
```

如果使用过脚本的独立 Nginx，先执行 `gateway-stop`。端口被其他实例占用时，脚本会
报出服务和端口，不会替你杀进程。Supervisor 托管的实例应使用原有部署流程。

归档保存在 `/opt/thing-connect.backup-时间戳-进程号`。旧数据库保持原样。
首次安装页面必须使用新的空数据库，例如 `thing_connect_dev`；不能填写旧的已安装库。
MySQL 运行账号必须拥有新库的 SELECT、INSERT、UPDATE、DELETE 权限，迁移账号负责 DDL。
账号准备步骤见部署指南的“MySQL 账号”一节。开发联调实例也应使用独立的 Redis DB。

脚本显示一次性安装令牌后，打开 `http://127.0.0.1:9000/admin/`，完成 MySQL、Redis
和管理员初始化。在 Admin 中发布 TiRTC、User / VoIP / Call 的 MQTT 配置，然后执行：

```bash
sudo /opt/thing-connect/deploy-local.sh start
sudo /opt/thing-connect/deploy-local.sh gateway
sudo /opt/thing-connect/deploy-local.sh status
```

- 用户 H5：`http://127.0.0.1:18080/`
- Admin：`http://127.0.0.1:18080/admin/`
- 多人对讲 `/v1/call/room/*` 转发到 call-server（9005）。
- 设备资料 `/v1/device/profile` 转发到 device-server（9001）。

独立 Nginx 仅监听本机，配置、PID 和日志位于 `/opt/thing-connect/nginx-local/`，
不会修改系统 Nginx。端口冲突时使用 `GATEWAY_PORT=18081` 执行 `gateway`。
首次安装接口不通过该入口代理，安装时直接访问 9000。
设备模拟器可按各自 README 指定服务地址；独立入口不提供 `/services` 发现文件。

安装完成后的数据库应有 `device_profile` 和六张房间业务表：
`call_rooms`、`call_room_codes`、`call_assignments`、`call_leases`、`call_requests`、
`call_outbox`。`prepare` 只构建代码，不安装数据库，不会创建这些表。

## 构建与后续更新

只检查当前源码能否构建：

```bash
sudo bash thing-connect/scripts/deploy-local.sh prepare
```

源码快照、构建日志和 SHA-256 摘要保存在 `/opt/thing-connect.sources/`。该目录包含源码，
仅部署用户可读。源码中的忽略文件不会进入快照；符号链接会被拒绝。快照不自动删除。

保留已安装实例的数据更新时，使用 Admin 专用迁移账号配置，并遵守现有迁移备份门槛：

```bash
sudo /opt/thing-connect/deploy-local.sh gateway-stop
sudo /opt/thing-connect/deploy-local.sh stop
sudo env \
  SOURCE_ROOT=/home/workspace/tirtc-server-example \
  MIGRATION_CONFIG=/opt/thing-connect/admin-server/migration-config.yaml \
  DATABASE_BACKUP_FILE=/absolute/path/verified-backup.sql \
  DATABASE_BACKUP_RESTORE_VERIFIED=1 \
  bash thing-connect/scripts/deploy-local.sh update
sudo /opt/thing-connect/deploy-local.sh gateway
```

备份必须真实完成恢复演练，不能仅设置确认变量。有待执行的迁移时，脚本复用
`deploy-prod.sh` 的备份与失败处理；迁移失败后不自动启动旧版本。备份和恢复步骤见
[部署指南](deployment.md)。

如果预检提示数据库版本比二进制新，可能是另一个迁移历史的旧开发库。
此时不替换服务文件，不改台账，也不补写 SQL 绕过检查。需要保留数据时先完成兼容升级；
允许全新联调时使用上述归档安装流程和新空库。
