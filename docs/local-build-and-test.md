# ThingConnect 本地构建与部署测试指南

本文说明如何把本地工作区中的服务端改动部署到已经安装的 ThingConnect 实例进行体验测试。整个流程不依赖 GitHub，也不要求把提交推送到远程仓库。

示例路径：

- 本地源码：`/home/workspace/tirtc-server-example`
- 已安装实例：`/opt/thing-connect`
- 本地进程管理器：`/opt/thing-connect/service-local.sh`

路径不同时按实际环境替换。本文只适用于测试环境或明确允许本地旁加载的服务器。

## 1. 使用边界

本地旁加载适合以下改动：

- Go 业务逻辑、错误处理和日志。
- Admin Web 或其他静态页面。
- 不涉及数据库 Schema 的配置读取和协议兼容修正。

出现以下情况时，不要只替换二进制：

- 新增或修改数据库迁移。
- 修改服务配置结构、共享密钥或动态配置注册表。
- 多个服务必须按同一版本同时切换。
- 需要验证完整首次安装、升级、备份或回滚流程。

这些场景应使用独立测试数据库和隔离部署目录，或执行项目正式的迁移与发布流程。

同一服务只能由一个进程管理器托管。使用 `service-local.sh` 时不要再由 Supervisor 启动同一个服务；已经切换到 Supervisor 时，本文中的重启命令应改为：

```bash
sudo supervisorctl restart thing-connect:admin-server
```

## 2. 快速测试 Admin 后端改动

### 2.1 在本地工作区构建

```bash
cd /home/workspace/tirtc-server-example/thing-connect
./build.sh admin-server
test -x bin/admin-server
```

构建产物位于：

```text
/home/workspace/tirtc-server-example/thing-connect/bin/admin-server
```

源码有未提交修改时仍可用于本地体验，构建版本会带有 `dirty` 标记。生产发布应使用干净、可追踪的提交。

### 2.2 备份并原子替换二进制

以下命令不会修改数据库或配置。先保留一份本地测试前的二进制，再通过临时文件和 `mv` 原子替换：

```bash
export TC_SOURCE_ROOT=/home/workspace/tirtc-server-example
export TC_DEPLOY_ROOT=/opt/thing-connect

if [ ! -f "$TC_DEPLOY_ROOT/admin-server/admin-server.before-local-test" ]; then
  sudo cp -a -- \
    "$TC_DEPLOY_ROOT/admin-server/admin-server" \
    "$TC_DEPLOY_ROOT/admin-server/admin-server.before-local-test"
fi

sudo install -m 0755 \
  "$TC_SOURCE_ROOT/thing-connect/bin/admin-server" \
  "$TC_DEPLOY_ROOT/admin-server/admin-server.local-new"

sudo mv -f -- \
  "$TC_DEPLOY_ROOT/admin-server/admin-server.local-new" \
  "$TC_DEPLOY_ROOT/admin-server/admin-server"
```

Linux 可以安全地用 `rename` 替换正在运行的可执行文件。旧进程继续使用原 inode，直到执行重启。

### 2.3 重启并验收

首次安装后仍由本地服务脚本托管时执行：

```bash
sudo /opt/thing-connect/service-local.sh \
  restart thing-connect:admin-server

curl -i http://127.0.0.1:9000/health/ready
sudo /opt/thing-connect/service-local.sh \
  status thing-connect:admin-server
```

查看最新日志：

```bash
tail -n 100 /opt/thing-connect/logs/admin-server.err.log
```

验收标准：

- `/health/ready` 返回 HTTP 200。
- Admin Web 可以登录。
- 日志没有持续重启、数据库连接失败或配置加载失败。
- 目标接口返回预期的 HTTP 状态、业务 `code` 和中文 `msg`。

## 3. 测试 Admin Web 改动

`./build.sh admin-server` 会同时生成 Admin Web：

```text
/home/workspace/tirtc-server-example/thing-connect/admin/admin-web/dist
```

静态资源需要整体切换，不能只复制某个带哈希文件。测试前备份现有目录：

```bash
export TC_SOURCE_ROOT=/home/workspace/tirtc-server-example
export TC_DEPLOY_ROOT=/opt/thing-connect

if [ ! -d "$TC_DEPLOY_ROOT/admin-server/static.before-local-test" ]; then
  sudo cp -a -- \
    "$TC_DEPLOY_ROOT/admin-server/static" \
    "$TC_DEPLOY_ROOT/admin-server/static.before-local-test"
fi

[ ! -e "$TC_DEPLOY_ROOT/admin-server/static.local-new" ] || {
  echo "static.local-new 已存在，请先确认并移走该目录"
  exit 1
}

[ ! -e "$TC_DEPLOY_ROOT/admin-server/static.local-old" ] || {
  echo "static.local-old 已存在，请先确认并移走该目录"
  exit 1
}

sudo cp -a -- \
  "$TC_SOURCE_ROOT/thing-connect/admin/admin-web/dist" \
  "$TC_DEPLOY_ROOT/admin-server/static.local-new"

sudo mv -- \
  "$TC_DEPLOY_ROOT/admin-server/static" \
  "$TC_DEPLOY_ROOT/admin-server/static.local-old"

sudo mv -- \
  "$TC_DEPLOY_ROOT/admin-server/static.local-new" \
  "$TC_DEPLOY_ROOT/admin-server/static"
```

确认页面正常后删除临时旧目录；确认前保留它用于回滚。浏览器可能缓存入口 HTML，测试时可使用无痕窗口或强制刷新。

## 4. 回滚本地旁加载

回滚 Admin 二进制：

```bash
export TC_DEPLOY_ROOT=/opt/thing-connect

sudo install -m 0755 \
  "$TC_DEPLOY_ROOT/admin-server/admin-server.before-local-test" \
  "$TC_DEPLOY_ROOT/admin-server/admin-server.rollback"

sudo mv -f -- \
  "$TC_DEPLOY_ROOT/admin-server/admin-server.rollback" \
  "$TC_DEPLOY_ROOT/admin-server/admin-server"

sudo "$TC_DEPLOY_ROOT/service-local.sh" \
  restart thing-connect:admin-server

curl -i http://127.0.0.1:9000/health/ready
```

回滚 Admin Web 静态资源：

```bash
export TC_DEPLOY_ROOT=/opt/thing-connect

sudo mv -- \
  "$TC_DEPLOY_ROOT/admin-server/static" \
  "$TC_DEPLOY_ROOT/admin-server/static.local-failed"

sudo cp -a -- \
  "$TC_DEPLOY_ROOT/admin-server/static.before-local-test" \
  "$TC_DEPLOY_ROOT/admin-server/static"
```

回滚不撤销数据库写入。涉及迁移或业务数据变化时，必须使用对应的数据库备份和恢复流程。

## 5. 隔离体验首次安装页面

已经安装完成的实例会永久关闭 `/v1/setup/*` 写入口，因此不能在正式端口上重新体验首次安装。可以使用本地构建的 Admin 二进制、临时部署目录和备用端口启动一个隔离安装页面。

以下流程只用于执行 Preview，不点击“执行安装”。如果需要测试完整安装，必须使用名称以 `_test` 结尾的独立 MySQL 数据库和独立测试账号，不能指向现有业务数据库。

```bash
export TC_SOURCE_ROOT=/home/workspace/tirtc-server-example
export TC_SETUP_ROOT
TC_SETUP_ROOT="$(mktemp -d /tmp/thingconnect-setup-test.XXXXXX)"

mkdir -p "$TC_SETUP_ROOT/admin-server"

"$TC_SOURCE_ROOT/thing-connect/bin/admin-server" \
  -c "$TC_SETUP_ROOT/admin-server/config.yaml" \
  -deploy-root "$TC_SETUP_ROOT" \
  -setup-bind 127.0.0.1 \
  -setup-port 19000 \
  -setup-static-dir "$TC_SOURCE_ROOT/thing-connect/admin/admin-web/dist" \
  -prepare-setup
```

终端会显示一次性安装令牌。令牌不要发送到聊天、工单或日志平台。随后在同一个 Shell 中启动测试服务：

```bash
"$TC_SOURCE_ROOT/thing-connect/bin/admin-server" \
  -c "$TC_SETUP_ROOT/admin-server/config.yaml" \
  -deploy-root "$TC_SETUP_ROOT" \
  -setup-bind 127.0.0.1 \
  -setup-port 19000 \
  -setup-static-dir "$TC_SOURCE_ROOT/thing-connect/admin/admin-web/dist"
```

浏览器打开：

```text
http://127.0.0.1:19000/admin/
```

测试安装错误分类时，可填写能够通过 Redis、MQTT 和 MySQL 连接检查的测试凭据，只执行“连接检查并生成安装计划”。例如目标数据库已经被正式实例锁定时，服务端日志应显示 `installer: already installed`，不应错误显示 `mysql unavailable`。

结束后按 `Ctrl+C` 停止测试服务。确认变量仍指向本次 `mktemp` 目录后清理：

```bash
case "$TC_SETUP_ROOT" in
  /tmp/thingconnect-setup-test.*)
    rm -rf -- "$TC_SETUP_ROOT"
    ;;
  *)
    echo "拒绝清理非预期目录: $TC_SETUP_ROOT" >&2
    ;;
esac
unset TC_SETUP_ROOT
```

## 6. 不经过 GitHub 的本地 Git 传递

需要让 `/opt/thing-connect/tirtc-server-example` 保持干净、可复现时，可以先在开发工作区创建本地提交，再让安装目录直接从本地路径获取。`git commit` 只写本地仓库，不等于 `git push`。

在开发工作区完成本地提交后执行：

```bash
sudo git -C /opt/thing-connect/tirtc-server-example \
  fetch /home/workspace/tirtc-server-example HEAD

sudo git -C /opt/thing-connect/tirtc-server-example \
  merge --ff-only FETCH_HEAD
```

这两条命令只在本机文件系统之间传递 Git 对象，不访问 GitHub。要求安装目录工作树干净，且当前历史可以快进到开发提交。

需要走项目的完整文件发布校验时，在安装目录执行：

```bash
cd /opt/thing-connect/tirtc-server-example/thing-connect
sudo env "PATH=$PATH" /opt/thing-connect/deploy-prod.sh build
sudo env "PATH=$PATH" /opt/thing-connect/deploy-prod.sh deploy
```

`build` 会构建全部服务并生成完整发布标记；`deploy` 只发布文件，不迁移数据库，也不重启进程。发布后根据改动范围使用当前唯一的进程管理器重启受影响服务。

## 7. 建议的日常顺序

1. 在开发工作区运行修改范围内的单元测试和静态检查。
2. 直接构建并旁加载到本地安装实例。
3. 验证 readiness、日志和目标业务流程。
4. 不满意时使用本地备份立即回滚。
5. 验收通过后创建正式提交。
6. 需要安装目录保持可复现时，使用本地 `fetch` 和快进合并。
7. 确认版本稳定后再决定是否推送 GitHub。
