#!/usr/bin/env bash

set -Eeuo pipefail

PRODUCTION_DEPLOY_ROOT="/opt/thing-connect"
DEFAULT_SERVICE_GROUP="thing-connect"
DEFAULT_REPO_URL="https://github.com/tangeai/tirtc-server-example.git"

DEPLOY_ROOT="${DEPLOY_ROOT:-$PRODUCTION_DEPLOY_ROOT}"
REPO_URL="${REPO_URL:-$DEFAULT_REPO_URL}"
REPO_PATH="${REPO_PATH:-$DEPLOY_ROOT/tirtc-server-example}"
BUILD_DIR="${BUILD_DIR:-$REPO_PATH/thing-connect}"
SERVICE_GROUP="${SERVICE_GROUP:-$DEFAULT_SERVICE_GROUP}"
LOCAL_CONTROLLER="$DEPLOY_ROOT/service-local.sh"
SETUP_PORT=9000
SETUP_BIND="${SETUP_BIND:-0.0.0.0}"
HEALTH_HOST="127.0.0.1"
HEALTH_WAIT_SECONDS="${HEALTH_WAIT_SECONDS:-30}"
HEALTH_REQUEST_TIMEOUT_SECONDS="${HEALTH_REQUEST_TIMEOUT_SECONDS:-3}"
LOCAL_SERVICE_WAIT_SECONDS="${LOCAL_SERVICE_WAIT_SECONDS:-15}"

ALL_SERVICES=("admin-server" "device-server" "user-server" "voip-server" "ai-server" "call-server")
BUSINESS_SERVICES=("device-server" "user-server" "voip-server" "ai-server" "call-server")

export DEPLOY_ROOT SERVICE_GROUP SETUP_PORT SETUP_BIND

log() { echo -e "[INFO] $1"; }
warn() { echo -e "[WARN] $1"; }
err() { echo -e "[ERROR] $1" >&2; }

usage() {
    cat <<'USAGE'
用法: install.sh

仅用于空服务器的首次安装：拉取源码、构建并发布服务，通过本地启动
脚本在 9000 端口启动一次性 Web 安装页。

安装完成后可用 /opt/thing-connect/service-local.sh 管理本地进程；配置
Supervisor 后，更新和迁移使用 /opt/thing-connect/deploy-prod.sh。
USAGE
}

require_root() {
    [ "${EUID:-$(id -u)}" -eq 0 ] || {
        err "首次安装需要写入部署目录，请使用 sudo 运行"
        return 1
    }
}

validate_options() {
    case "$DEPLOY_ROOT" in
        /*) ;;
        *) err "DEPLOY_ROOT 必须是绝对路径: $DEPLOY_ROOT"; return 1 ;;
    esac
    [ "$DEPLOY_ROOT" != "/" ] || {
        err "DEPLOY_ROOT 不能是文件系统根目录"
        return 1
    }
    [[ "$SERVICE_GROUP" =~ ^[A-Za-z0-9_-]+$ ]] || {
        err "SERVICE_GROUP 只能包含字母、数字、下划线和连字符"
        return 1
    }
    [[ "$SETUP_PORT" =~ ^[1-9][0-9]*$ ]] && [ "$SETUP_PORT" -le 65535 ] || {
        err "SETUP_PORT 必须是 1-65535 的整数"
        return 1
    }
    [ -n "${SETUP_BIND//[[:space:]]/}" ] || {
        err "SETUP_BIND 不能为空"
        return 1
    }
    [[ "$SETUP_BIND" != *:* ]] || {
        err "SETUP_BIND 当前只接受 IPv4 地址或主机名"
        return 1
    }
    [ -n "${REPO_URL//[[:space:]]/}" ] || {
        err "REPO_URL 不能为空"
        return 1
    }
    [[ "$HEALTH_WAIT_SECONDS" =~ ^[1-9][0-9]*$ ]] || {
        err "HEALTH_WAIT_SECONDS 必须是正整数"
        return 1
    }
    [[ "$HEALTH_REQUEST_TIMEOUT_SECONDS" =~ ^[1-9][0-9]*$ ]] || {
        err "HEALTH_REQUEST_TIMEOUT_SECONDS 必须是正整数"
        return 1
    }
    [[ "$LOCAL_SERVICE_WAIT_SECONDS" =~ ^[1-9][0-9]*$ ]] || {
        err "LOCAL_SERVICE_WAIT_SECONDS 必须是正整数"
        return 1
    }
}

prepare_deploy_root() {
    mkdir -p -- "$DEPLOY_ROOT" || {
        err "无法创建部署目录: $DEPLOY_ROOT"
        return 1
    }
    mkdir -p -- "$DEPLOY_ROOT/logs"
}

require_commands() {
    local command_name
    for command_name in cp curl flock git go mv npm setsid; do
        command -v "$command_name" >/dev/null 2>&1 || {
            err "缺少首次安装命令: $command_name"
            return 1
        }
    done
}

pull_source() {
    local worktree_status
    if [ -d "$REPO_PATH/.git" ]; then
        worktree_status="$(git -C "$REPO_PATH" status --porcelain --untracked-files=normal)" || return 1
        [ -z "$worktree_status" ] || {
            err "源码目录存在未提交修改，拒绝覆盖: $REPO_PATH"
            return 1
        }
        log "更新首次安装源码..."
        git -C "$REPO_PATH" pull --ff-only || return 1
    else
        if [ -e "$REPO_PATH" ] || [ -L "$REPO_PATH" ]; then
            err "源码路径已存在但不是 Git 工作树，拒绝覆盖: $REPO_PATH"
            return 1
        fi
        mkdir -p -- "$(dirname "$REPO_PATH")" || return 1
        log "拉取 ThingConnect 源码..."
        git clone -- "$REPO_URL" "$REPO_PATH" || return 1
    fi
    [ -f "$BUILD_DIR/go.mod" ] && [ -x "$BUILD_DIR/build.sh" ] || {
        err "仓库结构无效，缺少 thing-connect/go.mod 或可执行 build.sh: $REPO_PATH"
        return 1
    }
    worktree_status="$(git -C "$REPO_PATH" status --porcelain --untracked-files=normal)" || return 1
    [ -z "$worktree_status" ] || {
        err "拉取后的源码目录存在未提交修改: $REPO_PATH"
        return 1
    }
    log "待安装版本: $(git -C "$REPO_PATH" rev-parse --short HEAD)"
}

validate_empty_deployment() {
    local service
    if [ -e "$DEPLOY_ROOT/var/installer/installed.json" ] ||
       [ -L "$DEPLOY_ROOT/var/installer/installed.json" ]; then
        err "系统已经完成安装；后续发布请使用 $DEPLOY_ROOT/deploy-prod.sh"
        return 1
    fi
    if [ -e "$DEPLOY_ROOT/config-current" ] || [ -L "$DEPLOY_ROOT/config-current" ]; then
        err "检测到已激活配置；请启动 Admin 让安装器自动恢复，不要重新安装"
        return 1
    fi
    for service in "${ALL_SERVICES[@]}"; do
        if [ -e "$DEPLOY_ROOT/$service/config.yaml" ] || [ -L "$DEPLOY_ROOT/$service/config.yaml" ]; then
            err "$service 已存在 config.yaml；首次安装不会覆盖现有配置"
            return 1
        fi
    done
}

with_install_lock() (
    exec 9>"$DEPLOY_ROOT/deploy.lock"
    if ! flock -n 9; then
        err "另一个安装或发布任务正在运行"
        return 1
    fi
    "$@"
)

build_release() {
    local expected_commit built_commit worktree_status
    log "构建六个服务和 Web 静态资源..."
    "$BUILD_DIR/build.sh" || return 1
    [ -f "$BUILD_DIR/bin/.release-commit" ] || {
        err "完整构建标记不存在"
        return 1
    }
    expected_commit="$(git -C "$REPO_PATH" rev-parse HEAD)" || return 1
    built_commit="$(tr -d '[:space:]' <"$BUILD_DIR/bin/.release-commit")"
    [ "$built_commit" = "$expected_commit" ] || {
        err "构建产物不属于当前源码提交"
        return 1
    }
    worktree_status="$(git -C "$REPO_PATH" status --porcelain --untracked-files=normal)" || return 1
    [ -z "$worktree_status" ] || {
        err "构建后源码目录出现未提交差异，拒绝发布不可复现产物"
        return 1
    }
}

publish_static() {
    local service="$1" source="$2"
    local target="$DEPLOY_ROOT/$service/static"
    local pending="$DEPLOY_ROOT/$service/static.new"
    local previous="$DEPLOY_ROOT/$service/static.old"

    [ -d "$source" ] || {
        err "$service 静态资源不存在: $source"
        return 1
    }
    rm -rf -- "$pending" "$previous"
    cp -a -- "$source" "$pending" || return 1
    if [ -d "$target" ]; then
        mv -- "$target" "$previous" || return 1
    fi
    if ! mv -- "$pending" "$target"; then
        [ ! -d "$previous" ] || mv -- "$previous" "$target" || true
        return 1
    fi
    rm -rf -- "$previous"
}

publish_release() {
    local service source target pending
    for service in "${ALL_SERVICES[@]}"; do
        source="$BUILD_DIR/bin/$service"
        target="$DEPLOY_ROOT/$service/$service"
        pending="$DEPLOY_ROOT/$service/.$service.new"
        [ -x "$source" ] || {
            err "缺少可执行构建产物: $source"
            return 1
        }
        mkdir -p -- "$DEPLOY_ROOT/$service"
        cp -- "$source" "$pending" || return 1
        chmod 0755 "$pending" || return 1
        mv -f -- "$pending" "$target" || return 1
    done
    publish_static "user-server" "$BUILD_DIR/user-server/static" || return 1
    publish_static "ai-server" "$BUILD_DIR/ai-server/static" || return 1
    publish_static "admin-server" "$BUILD_DIR/admin/admin-web/dist" || return 1
    log "首次安装文件已发布到 $DEPLOY_ROOT"
}

publish_deploy_script() {
    local source="$BUILD_DIR/scripts/deploy-prod.sh"
    local pending="$DEPLOY_ROOT/.deploy-prod.sh.new"
    local target="$DEPLOY_ROOT/deploy-prod.sh"
    [ -f "$source" ] || { err "缺少日常发布脚本: $source"; return 1; }
    bash -n "$source" || { err "日常发布脚本语法检查失败"; return 1; }
    cp -- "$source" "$pending" || return 1
    chmod 0755 "$pending" || return 1
    mv -f -- "$pending" "$target" || return 1
}

publish_install_script() {
    local source="$BUILD_DIR/scripts/install.sh"
    local pending="$DEPLOY_ROOT/.install.sh.new"
    local target="$DEPLOY_ROOT/install.sh"
    [ -f "$source" ] || { err "缺少首次安装脚本: $source"; return 1; }
    bash -n "$source" || { err "首次安装脚本语法检查失败"; return 1; }
    cp -- "$source" "$pending" || return 1
    chmod 0755 "$pending" || return 1
    mv -f -- "$pending" "$target" || return 1
}

publish_local_controller() {
    local source="$BUILD_DIR/scripts/service-local.sh"
    local pending="$DEPLOY_ROOT/.service-local.sh.new"
    [ -f "$source" ] || { err "缺少本地启动脚本: $source"; return 1; }
    bash -n "$source" || { err "本地启动脚本语法检查失败"; return 1; }
    cp -- "$source" "$pending" || return 1
    chmod 0755 "$pending" || return 1
    mv -f -- "$pending" "$LOCAL_CONTROLLER" || return 1
}

local_service_state() {
    local status
    status="$("$LOCAL_CONTROLLER" status "$SERVICE_GROUP:$1" 2>&1 || true)"
    awk '$2 ~ /^(STOPPED|STARTING|BACKOFF|RUNNING|EXITED|FATAL|UNKNOWN)$/ { print $2; exit }' <<<"$status"
}

wait_for_admin() {
    local elapsed=0 state body
    while [ "$elapsed" -lt "$LOCAL_SERVICE_WAIT_SECONDS" ]; do
        state="$(local_service_state admin-server)"
        if [ "$state" = "RUNNING" ]; then
            break
        fi
        if [ "$state" = "FATAL" ] || [ "$state" = "BACKOFF" ] || [ "$state" = "EXITED" ]; then
            err "admin-server 本地启动失败，状态: $state"
            return 1
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    [ "$state" = "RUNNING" ] || {
        err "admin-server 在 ${LOCAL_SERVICE_WAIT_SECONDS}s 内未进入 RUNNING"
        return 1
    }

    elapsed=0
    while [ "$elapsed" -lt "$HEALTH_WAIT_SECONDS" ]; do
        body="$(curl -fsS --max-time "$HEALTH_REQUEST_TIMEOUT_SECONDS" \
            "http://${HEALTH_HOST}:${SETUP_PORT}/health/live" 2>/dev/null || true)"
        if [[ "$body" == *'"mode":"setup"'* ]]; then
            return 0
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    err "首次安装页在 ${HEALTH_WAIT_SECONDS}s 内未就绪"
    return 1
}

prepare_setup() {
    "$DEPLOY_ROOT/admin-server/admin-server" \
        -c "$DEPLOY_ROOT/admin-server/config.yaml" \
        -deploy-root "$DEPLOY_ROOT" \
        -setup-bind "$SETUP_BIND" \
        -setup-port "$SETUP_PORT" \
        -supervisorctl "$LOCAL_CONTROLLER" \
        -supervisor-group "$SERVICE_GROUP" \
        -prepare-setup
}

start_setup_server() {
    local service state
    for service in "${BUSINESS_SERVICES[@]}"; do
        "$LOCAL_CONTROLLER" stop "$SERVICE_GROUP:$service" >/dev/null 2>&1 || true
    done
    state="$(local_service_state admin-server)"
    if [ "$state" = "RUNNING" ] || [ "$state" = "STARTING" ]; then
        "$LOCAL_CONTROLLER" restart "$SERVICE_GROUP:admin-server" || return 1
    else
        "$LOCAL_CONTROLLER" start "$SERVICE_GROUP:admin-server" || return 1
    fi
    if ! wait_for_admin; then
        "$LOCAL_CONTROLLER" stop "$SERVICE_GROUP:admin-server" >/dev/null 2>&1 || true
        return 1
    fi
}

run_install() {
    local setup_output
    require_commands || return 1
    validate_empty_deployment || return 1
    pull_source || return 1
    build_release || return 1
    publish_release || return 1
    publish_deploy_script || return 1
    publish_install_script || return 1
    publish_local_controller || return 1
    setup_output="$(prepare_setup)" || return 1
    printf '%s\n' "$setup_output"
    warn "安装令牌只显示这一次；不要发送到聊天、工单或监控系统"
    start_setup_server || return 1
    log "首次安装页面已启动：http://<服务器IP>:${SETUP_PORT}/admin/"
    warn "安装完成前只向受信来源开放 TCP ${SETUP_PORT}；安装令牌提供第二层保护"
    log "本地服务管理入口：$LOCAL_CONTROLLER"
    log "安装完成后的运维入口：$DEPLOY_ROOT/deploy-prod.sh"
}

main() {
    if [ "$#" -gt 1 ]; then
        usage
        return 2
    fi
    case "${1:-}" in
        "") ;;
        help|-h|--help) usage; return 0 ;;
        *) err "未知参数: $1"; usage; return 2 ;;
    esac
    require_root || return 1
    validate_options || return 1
    prepare_deploy_root || return 1
    with_install_lock run_install
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    main "$@"
fi
