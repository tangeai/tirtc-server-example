#!/usr/bin/env bash
# Deploy a reproducible snapshot of the local workspace through the normal installer.
set -Eeuo pipefail
umask 077

LOCAL_SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_DEPLOY_ROOT="${DEPLOY_ROOT:-/opt/thing-connect}"
LOCAL_SOURCE_ROOT="${SOURCE_ROOT:-}"
LOCAL_ARCHIVE_EXISTING=0
LOCAL_SNAPSHOT=""

local_usage() {
    cat <<'USAGE'
用法: deploy-local.sh <命令> [--archive-existing]

  prepare   从本地源码生成独立快照，完整构建六个服务和 Web；不替换服务
  install   发布快照并启动 Web 首次安装页面；仅接受空安装目录
  update    保留配置和数据库升级；要求现有库通过正式迁移预检
  start     启动已安装服务并检查 readiness
  stop      停止本地脚本管理的服务
  status    显示本地服务状态
  gateway   启动独立 Nginx 本机入口（默认 127.0.0.1:18080）
  gateway-stop  停止这个脚本启动的独立 Nginx

环境变量：
  SOURCE_ROOT       本地仓库根目录，默认脚本所在仓库（包含未提交文件）
  DEPLOY_ROOT       安装目录，默认 /opt/thing-connect
  GATEWAY_PORT      独立 Nginx 端口，默认 18080
  MIGRATION_CONFIG  update 使用的数据库配置；默认复用当前已激活的 Admin 配置

install --archive-existing 仅用于全新安装：要求旧服务全部停止，将旧目录
整体移动到旁边的带时间戳目录，旧数据库保持原样。Web 安装必须填写新的空库。
不自动安装系统依赖、配置 HTTPS、修改旧库台账或注册开机服务。
gateway 使用独立配置和 PID 文件，不覆盖系统 Nginx 配置。
USAGE
}

local_error() { printf '[ERROR] %s\n' "$*" >&2; }
local_log() { printf '[INFO] %s\n' "$*"; }

local_validate() {
    local executable candidate
    case "$LOCAL_DEPLOY_ROOT" in /*) ;; *) local_error 'DEPLOY_ROOT 必须是绝对路径'; return 1 ;; esac
    candidate="$(realpath -m -- "$LOCAL_DEPLOY_ROOT")"
    [ "$candidate" != / ] && [ "$candidate" != /opt ] || {
        local_error 'DEPLOY_ROOT 必须指向专用安装子目录'; return 1;
    }
    [ ! -L "$LOCAL_DEPLOY_ROOT" ] || { local_error '安装根目录不能是符号链接'; return 1; }
    LOCAL_DEPLOY_ROOT="$candidate"
    for executable in git tar flock realpath sha256sum curl go npm setsid; do
        command -v "$executable" >/dev/null || { local_error "缺少命令: $executable"; return 1; }
    done
    if [ -z "$LOCAL_SOURCE_ROOT" ]; then
        if [ -f "$LOCAL_SCRIPT_DIR/../go.mod" ]; then
            LOCAL_SOURCE_ROOT="$(cd "$LOCAL_SCRIPT_DIR/../.." && pwd)"
        elif [ -r "$LOCAL_DEPLOY_ROOT/local-source-path" ]; then
            IFS= read -r LOCAL_SOURCE_ROOT <"$LOCAL_DEPLOY_ROOT/local-source-path"
        else
            local_error '请用 SOURCE_ROOT 指定包含 thing-connect 的本地仓库'; return 1
        fi
    fi
    LOCAL_SOURCE_ROOT="$(cd -- "$LOCAL_SOURCE_ROOT" && pwd)"
    [ "$(git -C "$LOCAL_SOURCE_ROOT" rev-parse --show-toplevel)" = "$LOCAL_SOURCE_ROOT" ] &&
        [ -f "$LOCAL_SOURCE_ROOT/thing-connect/go.mod" ] || {
        local_error 'SOURCE_ROOT 必须是 ThingConnect 仓库根目录'; return 1;
    }
    case "$LOCAL_SOURCE_ROOT/" in "$LOCAL_DEPLOY_ROOT/"*)
        [ "$LOCAL_ARCHIVE_EXISTING" = 0 ] || {
            local_error '归档安装目录时，SOURCE_ROOT 必须位于安装目录之外'; return 1;
        } ;;
    esac
}

local_prepare() {
    local archive_root file
    # Keep snapshots outside the installation root so archiving it cannot invalidate paths.
    archive_root="${LOCAL_DEPLOY_ROOT}.sources"
    mkdir -p -- "$archive_root"
    chmod 0700 "$archive_root"
    LOCAL_SNAPSHOT="$(mktemp -d "$archive_root/$(date +%Y%m%d-%H%M%S).XXXXXX")"
    local_log "打包本地源码到 $LOCAL_SNAPSHOT"
    git -C "$LOCAL_SOURCE_ROOT" rev-parse HEAD >"$LOCAL_SNAPSHOT/source-commit"
    git -C "$LOCAL_SOURCE_ROOT" status --porcelain=v1 --untracked-files=all -- thing-connect >"$LOCAL_SNAPSHOT/source-status"
    # Git supplies the allowlist: ignored credentials, node_modules and build outputs are excluded.
    git -C "$LOCAL_SOURCE_ROOT" ls-files -z --cached --others --exclude-standard -- .gitignore thing-connect >"$LOCAL_SNAPSHOT/files.all"
    : >"$LOCAL_SNAPSHOT/files.list"
    while IFS= read -r -d '' file; do
        [ -e "$LOCAL_SOURCE_ROOT/$file" ] || [ -L "$LOCAL_SOURCE_ROOT/$file" ] || continue
        [ ! -L "$LOCAL_SOURCE_ROOT/$file" ] || {
            local_error "源码快照不接受符号链接，请改用普通源码文件: $file"; return 1;
        }
        [ -f "$LOCAL_SOURCE_ROOT/$file" ] || {
            local_error "源码快照只接受普通文件: $file"; return 1;
        }
        printf '%s\0' "$file" >>"$LOCAL_SNAPSHOT/files.list"
    done <"$LOCAL_SNAPSHOT/files.all"
    tar -C "$LOCAL_SOURCE_ROOT" --null --verbatim-files-from --no-recursion \
        -T "$LOCAL_SNAPSHOT/files.list" -cf "$LOCAL_SNAPSHOT/source.tar"
    sha256sum "$LOCAL_SNAPSHOT/source.tar" >"$LOCAL_SNAPSHOT/source.sha256"
    mkdir "$LOCAL_SNAPSHOT/repository"
    tar -xf "$LOCAL_SNAPSHOT/source.tar" -C "$LOCAL_SNAPSHOT/repository"
    # This private commit identifies the exact deployed worktree, without touching the user's Git index.
    git -C "$LOCAL_SNAPSHOT/repository" -c init.templateDir= init -q
    git -C "$LOCAL_SNAPSHOT/repository" config core.autocrlf false
    git -C "$LOCAL_SNAPSHOT/repository" add --all
    git -C "$LOCAL_SNAPSHOT/repository" -c core.hooksPath=/dev/null \
        -c user.name=local-deploy -c user.email=local-deploy@localhost \
        -c commit.gpgSign=false commit -qm 'Local deployment source snapshot'
    (
        cd "$LOCAL_SNAPSHOT/repository/thing-connect"
        ./build.sh
    ) >"$LOCAL_SNAPSHOT/build.log" 2>&1 || {
        local_error "构建失败，未替换运行文件；日志: $LOCAL_SNAPSHOT/build.log"; return 1;
    }
    (
        cd "$LOCAL_SNAPSHOT/repository/thing-connect"
        find bin user-server/static ai-server/static admin/admin-web/dist -type f -print0 |
            sort -z | xargs -0 sha256sum
    ) >"$LOCAL_SNAPSHOT/artifacts.sha256"
    [ -z "$(git -C "$LOCAL_SNAPSHOT/repository" status --porcelain --untracked-files=normal)" ] || {
        local_error "构建改变了源码，拒绝发布；请核对 $LOCAL_SNAPSHOT/repository"; return 1;
    }
    local_log "构建完成，源码与产物摘要保存在 $LOCAL_SNAPSHOT"
}

local_require_stopped() {
    local service port output process executable gateway_pid
    if [ -f "$LOCAL_DEPLOY_ROOT/nginx-local/nginx.pid" ]; then
        IFS= read -r gateway_pid <"$LOCAL_DEPLOY_ROOT/nginx-local/nginx.pid"
        if [[ "$gateway_pid" =~ ^[1-9][0-9]*$ ]] && kill -0 "$gateway_pid" 2>/dev/null; then
            local_error '请先执行 deploy-local.sh gateway-stop 停止本部署的独立入口'; return 1
        fi
    fi
    # Do not take over Supervisor-owned processes, including currently stopped entries.
    if command -v supervisorctl >/dev/null; then
        output="$(supervisorctl status 2>/dev/null || true)"
        if [[ "$output" == *'thing-connect:'* ]]; then
            local_error '已有 ThingConnect Supervisor 托管，请用正式部署入口管理，避免混用'; return 1
        fi
    fi
    if [ -x "$LOCAL_DEPLOY_ROOT/service-local.sh" ]; then
        output="$(DEPLOY_ROOT="$LOCAL_DEPLOY_ROOT" "$LOCAL_DEPLOY_ROOT/service-local.sh" status-all)"
        if [[ "$output" == *' RUNNING '* || "$output" == *' STARTING '* || "$output" == *' CONFLICT '* ]]; then
            local_error "请先执行 $LOCAL_DEPLOY_ROOT/service-local.sh stop-all"; return 1
        fi
    fi
    # Service-local and the standard installer use the catalog's fixed ports.
    source "$LOCAL_SOURCE_ROOT/thing-connect/scripts/service-catalog.sh"
    load_service_catalog "$LOCAL_SOURCE_ROOT/thing-connect/internal/installer/service_catalog.tsv"
    for service in "${ALL_SERVICES[@]}"; do
        port="${SERVICE_PORT[$service]}"
        if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
            local_error "$service 所需端口 $port 已被占用；请先确认并停止占用进程"; return 1
        fi
    done
    for process in /proc/[0-9]*/exe; do
        executable="$(readlink -- "$process" 2>/dev/null || true)"
        case "$executable" in "$LOCAL_DEPLOY_ROOT/"*)
            local_error "安装目录中仍有进程运行: ${process%/exe}"; return 1 ;;
        esac
    done
}

local_install() (
    local previous=""
    local_require_stopped
    if [ "$LOCAL_ARCHIVE_EXISTING" = 0 ]; then
        DEPLOY_ROOT="$LOCAL_DEPLOY_ROOT" source "$LOCAL_SOURCE_ROOT/thing-connect/scripts/install.sh"
        DEPLOY_ROOT="$LOCAL_DEPLOY_ROOT"
        validate_empty_deployment
    fi
    local_prepare
    if [ "$LOCAL_ARCHIVE_EXISTING" = 1 ]; then
        [ ! -L "$LOCAL_DEPLOY_ROOT" ] || { local_error '拒绝归档符号链接'; return 1; }
        previous="${LOCAL_DEPLOY_ROOT}.backup-$(date +%Y%m%d-%H%M%S)-$$"
        [ ! -e "$previous" ] || { local_error '备份目标已存在'; return 1; }
        mv -- "$LOCAL_DEPLOY_ROOT" "$previous"
        mkdir -m 0755 -- "$LOCAL_DEPLOY_ROOT"
        # Preserve the same deploy lock inode across the directory rename.
        ln -- "$previous/deploy.lock" "$LOCAL_DEPLOY_ROOT/deploy.lock"
        local_log "旧安装已归档到 $previous；旧数据库未修改。请在安装页使用新的空库。"
    fi
    export DEPLOY_ROOT="$LOCAL_DEPLOY_ROOT"
    export REPO_PATH="$LOCAL_SNAPSHOT/repository"
    export BUILD_DIR="$REPO_PATH/thing-connect"
    source "$BUILD_DIR/scripts/install.sh"
    # A reviewed local snapshot is the release source; never pull or rebuild a different revision.
    pull_source() { [ -f "$BUILD_DIR/bin/.release-commit" ]; }
    build_release() { [ -f "$BUILD_DIR/bin/.release-commit" ]; }
    run_install
    cp -- "$BUILD_DIR/scripts/deploy-local.sh" "$LOCAL_DEPLOY_ROOT/.deploy-local.sh.new"
    chmod 0755 "$LOCAL_DEPLOY_ROOT/.deploy-local.sh.new"
    mv -f "$LOCAL_DEPLOY_ROOT/.deploy-local.sh.new" "$LOCAL_DEPLOY_ROOT/deploy-local.sh"
    printf '%s\n' "$LOCAL_SOURCE_ROOT" >"$LOCAL_DEPLOY_ROOT/local-source-path"
    printf '%s\n' "$LOCAL_SNAPSHOT" >"$LOCAL_DEPLOY_ROOT/local-release-path"
    local_log "安装页面: http://127.0.0.1:9000/admin/；完整验收步骤见源码 deployment-local.md"
)

local_update() (
    if [ -z "${MIGRATION_CONFIG:-}" ]; then
        if [ -r "$LOCAL_DEPLOY_ROOT/admin-server/config.yaml" ]; then
            MIGRATION_CONFIG="$LOCAL_DEPLOY_ROOT/admin-server/config.yaml"
        else
            MIGRATION_CONFIG="$LOCAL_DEPLOY_ROOT/config-current/admin-server/config.yaml"
        fi
    fi
    [ -r "$MIGRATION_CONFIG" ] || {
        local_error "迁移配置不可读: $MIGRATION_CONFIG"; return 1;
    }
    [ "${SKIP_MIGRATIONS:-0}" = 0 ] || { local_error '本地模拟生产部署必须检查迁移，不能跳过'; return 1; }
    local_prepare
    export DEPLOY_ROOT="$LOCAL_DEPLOY_ROOT" REPO_PATH="$LOCAL_SNAPSHOT/repository"
    export BUILD_DIR="$REPO_PATH/thing-connect" FORCE_UPDATE=1
    "$BUILD_DIR/bin/admin-server" -c "$MIGRATION_CONFIG" -deploy-root "$DEPLOY_ROOT" \
        -require-runtime-target -migration-check-only || {
        local_error '数据库预检失败，未停止服务或替换文件。旧迁移历史不能直接改版本号；请先完成兼容升级或选择保留旧库的全新安装。'; return 1;
    }
    local_require_stopped
    source "$BUILD_DIR/scripts/deploy-prod.sh"
    pull_code() { [ -f "$BUILD_DIR/bin/.release-commit" ]; }
    build_services() { validate_build_release "$@"; }
    # Production updates require a verified restorable backup. This command is
    # explicitly for the disposable local integration environment, so keep the
    # production gate intact and override it only inside this subshell.
    validate_database_backup() {
        local_log '本地开发部署跳过生产数据库恢复演练门槛'
        return 0
    }
    full_deploy
    if ! DEPLOY_ROOT="$LOCAL_DEPLOY_ROOT" "$LOCAL_DEPLOY_ROOT/service-local.sh" start-all; then
        DEPLOY_ROOT="$LOCAL_DEPLOY_ROOT" "$LOCAL_DEPLOY_ROOT/service-local.sh" stop-all || true
        local_error '启动验收失败，服务保持停止；请检查 logs 下的服务日志'; return 1
    fi
    printf '%s\n' "$LOCAL_SNAPSHOT" >"$LOCAL_DEPLOY_ROOT/local-release-path"
    cp -- "$BUILD_DIR/scripts/deploy-local.sh" "$LOCAL_DEPLOY_ROOT/.deploy-local.sh.new"
    chmod 0755 "$LOCAL_DEPLOY_ROOT/.deploy-local.sh.new"
    mv -f "$LOCAL_DEPLOY_ROOT/.deploy-local.sh.new" "$LOCAL_DEPLOY_ROOT/deploy-local.sh"
    printf '%s\n' "$LOCAL_SOURCE_ROOT" >"$LOCAL_DEPLOY_ROOT/local-source-path"
    local_log '发布完成，已安装服务全部通过 readiness'
)

local_gateway() {
    local action="$1" port="${GATEWAY_PORT:-18080}" prefix="$LOCAL_DEPLOY_ROOT/nginx-local" pid args
    command -v nginx >/dev/null || { local_error '请先安装 nginx'; return 1; }
    [[ "$port" =~ ^[1-9][0-9]*$ ]] && [ "$port" -le 65535 ] || {
        local_error 'GATEWAY_PORT 必须在 1-65535 范围'; return 1;
    }
    if [ -f "$prefix/nginx.pid" ]; then
        IFS= read -r pid <"$prefix/nginx.pid"
        [[ "$pid" =~ ^[1-9][0-9]*$ ]] || { local_error 'Nginx PID 文件无效'; return 1; }
        if kill -0 "$pid" 2>/dev/null; then
            args="$(tr '\0' ' ' <"/proc/$pid/cmdline")"
            [[ "$args" == *"nginx: master process"* && "$args" == *"-p $prefix/ -c nginx.conf"* ]] || {
                local_error 'PID 不属于本部署的 Nginx，拒绝发送信号'; return 1;
            }
            if [ "$action" = gateway-stop ]; then
                nginx -p "$prefix/" -c nginx.conf -s quit
            else
                local_log '独立 Nginx 已运行；更改端口时先执行 gateway-stop'
            fi
            return
        fi
    fi
    [ "$action" != gateway-stop ] || return 0
    if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
        local_error "入口端口 $port 已占用；请用 GATEWAY_PORT 指定其他端口"; return 1;
    fi
    mkdir -p "$prefix/logs"
    {
        printf 'pid nginx.pid;\nerror_log logs/error.log;\nevents {}\nhttp {\n'
        printf 'include /etc/nginx/mime.types;\naccess_log logs/access.log;\n'
        sed "s/listen 80;/listen 127.0.0.1:$port;/; s/server_name thing.example.com;/server_name localhost;/" \
            "$LOCAL_SOURCE_ROOT/thing-connect/deploy/nginx/thing-connect.nginx.conf"
        printf '}\n'
    } >"$prefix/nginx.conf.new"
    nginx -t -p "$prefix/" -c nginx.conf.new
    mv -f "$prefix/nginx.conf.new" "$prefix/nginx.conf"
    nginx -p "$prefix/" -c nginx.conf 9>&-
    local_log "用户 H5: http://127.0.0.1:$port/；Admin: http://127.0.0.1:$port/admin/"
}

local_main() {
    local action="${1:-help}"
    shift "$(( $# > 0 ? 1 : 0 ))"
    case "$action" in help|-h|--help) local_usage; return 0 ;; esac
    if [ "$#" = 1 ] && [ "$1" = --archive-existing ] && [ "$action" = install ]; then
        LOCAL_ARCHIVE_EXISTING=1
    elif [ "$#" != 0 ]; then
        local_usage; return 2
    fi
    case "$action" in prepare|install|update|start|stop|status|gateway|gateway-stop) ;; *) local_usage; return 2 ;; esac
    local_validate
    mkdir -p -- "$LOCAL_DEPLOY_ROOT"
    # Serialize all operations with the same lock used by install.sh/deploy-prod.sh.
    exec 9>"$LOCAL_DEPLOY_ROOT/deploy.lock"
    flock -n 9 || { local_error '另一个安装或发布任务正在运行'; return 1; }
    case "$action" in
        prepare) local_prepare ;;
        install) local_install ;;
        update) local_update ;;
        gateway|gateway-stop) local_gateway "$action" ;;
        start|stop|status)
            [ -x "$LOCAL_DEPLOY_ROOT/service-local.sh" ] || { local_error '尚未安装服务控制脚本'; return 1; }
            DEPLOY_ROOT="$LOCAL_DEPLOY_ROOT" "$LOCAL_DEPLOY_ROOT/service-local.sh" "$action-all" 9>&-
            ;;
    esac
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    local_main "$@"
fi
