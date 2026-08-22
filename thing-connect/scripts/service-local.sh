#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ "$(basename "$SCRIPT_DIR")" = "scripts" ]; then
    DEFAULT_DEPLOY_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
else
    DEFAULT_DEPLOY_ROOT="$SCRIPT_DIR"
fi
DEPLOY_ROOT="${DEPLOY_ROOT:-$DEFAULT_DEPLOY_ROOT}"
SERVICE_GROUP="${SERVICE_GROUP:-thing-connect}"
STATE_DIR="$DEPLOY_ROOT/var/local-services"
LOG_DIR="$DEPLOY_ROOT/logs"
SETUP_PORT=9000
SETUP_BIND="${SETUP_BIND:-0.0.0.0}"

if [ "$(basename "$SCRIPT_DIR")" = "scripts" ]; then
    CATALOG_LOADER="$SCRIPT_DIR/service-catalog.sh"
    SERVICE_CATALOG="$SCRIPT_DIR/../internal/installer/service_catalog.tsv"
else
    CATALOG_LOADER="$DEPLOY_ROOT/service-catalog.sh"
    SERVICE_CATALOG="$DEPLOY_ROOT/service-catalog.tsv"
fi
# shellcheck source=service-catalog.sh
source "$CATALOG_LOADER"
load_service_catalog "$SERVICE_CATALOG"

err() { echo "[ERROR] $1" >&2; }

service_name() {
    local value="$1"
    value="${value#"$SERVICE_GROUP:"}"
    if [ -n "${SERVICE_PORT[$value]+present}" ]; then
        printf '%s\n' "$value"
        return
    fi
    err "未知服务: $1"
    return 2
}

pid_file() {
    printf '%s/%s.pid\n' "$STATE_DIR" "$1"
}

managed_pid() {
    local service="$1" pid="$2"
    local -a arguments=()
    [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 1
    kill -0 "$pid" 2>/dev/null || return 1
    [ -r "/proc/$pid/cmdline" ] || return 1
    mapfile -d '' -t arguments <"/proc/$pid/cmdline" || true
    [ "${#arguments[@]}" -eq 4 ] &&
        [[ "${arguments[0]}" == *bash ]] &&
        [ "${arguments[1]}" = "$DEPLOY_ROOT/service-local.sh" ] &&
        [ "${arguments[2]}" = "run" ] &&
        [ "${arguments[3]}" = "$service" ]
}

managed_pids() {
    local service="$1" process pid
    for process in /proc/[0-9]*; do
        pid="${process#/proc/}"
        managed_pid "$service" "$pid" && printf '%s\n' "$pid"
    done
}

write_pid() {
    local service="$1" pid="$2" file temporary
    file="$(pid_file "$service")"
    temporary="$file.$pid.tmp"
    printf '%s\n' "$pid" >"$temporary"
    mv -f -- "$temporary" "$file"
}

current_pid() {
    local service="$1" file
    local -a pids=()
    file="$(pid_file "$service")"
    mapfile -t pids < <(managed_pids "$service")
    case "${#pids[@]}" in
        0)
            rm -f -- "$file"
            return 1
            ;;
        1)
            write_pid "$service" "${pids[0]}"
            printf '%s\n' "${pids[0]}"
            return 0
            ;;
        *)
            printf '%s\n' "${pids[*]}"
            return 2
            ;;
    esac
}

config_exists() {
    local service="$1"
    [ -f "$DEPLOY_ROOT/$service/config.yaml" ] ||
        [ -f "$DEPLOY_ROOT/config-current/$service/config.yaml" ]
}

preflight_service() {
    local service="$1" checker="$DEPLOY_ROOT/$ADMIN_SERVICE/$ADMIN_SERVICE"
    [ "$service" != "$ADMIN_SERVICE" ] || return 0
    [ -x "$checker" ] || {
        err "无法检查 $service 配置：Admin 程序不存在或不可执行: $checker"
        return 1
    }
    echo "检查 $service 的基础配置、必填配置和依赖连接..." >&2
    if ! "$checker" -c "$DEPLOY_ROOT/$ADMIN_SERVICE/config.yaml" \
        -deploy-root "$DEPLOY_ROOT" -check-service-config "$service"; then
        err "$service 启动前检查失败，未启动服务"
        err "处理建议：登录 Admin 完成页面列出的必填配置；修复后重新执行 $DEPLOY_ROOT/service-local.sh start $SERVICE_GROUP:$service"
        return 1
    fi
}

service_port() {
    [ -n "${SERVICE_PORT[$1]+present}" ] || return 2
    printf '%s\n' "${SERVICE_PORT[$1]}"
}

port_in_use() {
    local port="$1"
    (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null
}

run_service() {
    local service="$1" binary="$DEPLOY_ROOT/$service/$service"
    local config="$DEPLOY_ROOT/$service/config.yaml" stopping=0 child=""
    local file lock_file
    file="$(pid_file "$service")"
    lock_file="$STATE_DIR/$service.run.lock"
    mkdir -p "$STATE_DIR" "$LOG_DIR"
    exec 8>"$lock_file"
    if ! flock -n 8; then
        err "$service 已有守护进程，拒绝重复运行"
        return 1
    fi
    write_pid "$service" "$$"

    stop_child() {
        stopping=1
        [ -z "$child" ] || kill -TERM "$child" 2>/dev/null || true
    }
    trap stop_child INT TERM

    while [ "$stopping" -eq 0 ]; do
        if [ "$service" = "$ADMIN_SERVICE" ]; then
            GIN_MODE=release SERVICE_INSTANCE_ID="local-$service" \
                "$binary" -c "$config" -deploy-root "$DEPLOY_ROOT" \
                -setup-bind "$SETUP_BIND" -setup-port "$SETUP_PORT" \
                -supervisorctl "$DEPLOY_ROOT/service-local.sh" \
                -supervisor-group "$SERVICE_GROUP" 8>&- &
        else
            GIN_MODE=release SERVICE_INSTANCE_ID="local-$service" \
                "$binary" -c "$config" 8>&- &
        fi
        child=$!
        wait "$child" || true
        child=""
        [ "$stopping" -ne 0 ] || sleep 1
    done
    if [ -f "$file" ] && [ "$(tr -d '[:space:]' <"$file")" = "$$" ]; then
        rm -f -- "$file"
    fi
}

service_live() {
    local service="$1" port
    port="$(service_port "$service")" || return 1
    curl -fsS --max-time 1 "http://127.0.0.1:$port/health/live" >/dev/null 2>&1
}

print_status() {
    local service="$1" pid="$2"
    if service_live "$service"; then
        echo "$SERVICE_GROUP:$service RUNNING pid $pid"
    else
        echo "$SERVICE_GROUP:$service STARTING pid $pid"
    fi
}

start_one() (
    local service="$1" prechecked="${2:-0}" pid port current_rc
    local binary="$DEPLOY_ROOT/$service/$service"
    mkdir -p "$STATE_DIR" "$LOG_DIR"
    exec 9>"$STATE_DIR/$service.lock"
    flock 9
    if pid="$(current_pid "$service")"; then
        print_status "$service" "$pid"
        return 0
    else
        current_rc=$?
        if [ "$current_rc" -eq 2 ]; then
            err "$service 检测到多个守护进程: $pid"
            err "处理建议：执行 $DEPLOY_ROOT/service-local.sh stop $SERVICE_GROUP:$service 清理重复进程后重试"
            return 1
        fi
    fi
    [ -x "$binary" ] || { err "服务尚未发布: $binary"; return 1; }
    if [ "$service" != "$ADMIN_SERVICE" ]; then
        config_exists "$service" || { err "$service 配置不存在"; return 1; }
        [ "$prechecked" = "1" ] || preflight_service "$service" || return 1
    fi
    port="$(service_port "$service")" || return 1
    if port_in_use "$port"; then
        err "$service 无法启动：端口 $port 已被占用"
        err "处理建议：执行 ss -ltnp | grep ':$port' 确认占用进程；停止旧实例或重复的进程管理器后重试"
        return 1
    fi
    nohup setsid "$DEPLOY_ROOT/service-local.sh" run "$service" \
        >>"$LOG_DIR/$service.out.log" 2>>"$LOG_DIR/$service.err.log" </dev/null 9>&- &
    sleep 1
    if ! pid="$(current_pid "$service")"; then
        err "$service 本地启动失败，请检查 $LOG_DIR/$service.err.log"
        return 1
    fi
    print_status "$service" "$pid"
)

stop_one() (
    local service="$1" pid pgid elapsed=0
    local -a pids=() remaining=()
    mkdir -p "$STATE_DIR"
    exec 9>"$STATE_DIR/$service.lock"
    flock 9
    mapfile -t pids < <(managed_pids "$service")
    if [ "${#pids[@]}" -eq 0 ]; then
        rm -f -- "$(pid_file "$service")"
        echo "$SERVICE_GROUP:$service STOPPED"
        return 0
    fi
    for pid in "${pids[@]}"; do
        kill -TERM "$pid" 2>/dev/null || true
    done
    while [ "$elapsed" -lt 15 ]; do
        mapfile -t remaining < <(managed_pids "$service")
        [ "${#remaining[@]}" -ne 0 ] || break
        sleep 1
        elapsed=$((elapsed + 1))
    done
    mapfile -t remaining < <(managed_pids "$service")
    for pid in "${remaining[@]}"; do
        pgid="$(ps -o pgid= -p "$pid" 2>/dev/null | tr -d '[:space:]')"
        if [ "$pgid" = "$pid" ]; then
            kill -KILL -- "-$pid" 2>/dev/null || true
        else
            kill -KILL "$pid" 2>/dev/null || true
        fi
    done
    rm -f -- "$(pid_file "$service")"
    echo "$SERVICE_GROUP:$service STOPPED"
)

status_one() {
    local service="$1" pid current_rc
    if pid="$(current_pid "$service")"; then
        print_status "$service" "$pid"
    else
        current_rc=$?
        if [ "$current_rc" -eq 2 ]; then
            echo "$SERVICE_GROUP:$service CONFLICT pids $pid"
        else
            echo "$SERVICE_GROUP:$service STOPPED"
        fi
    fi
}

wait_ready() {
    local service="$1" port="$2" elapsed=0
    while [ "$elapsed" -lt 60 ]; do
        if curl -fsS --max-time 3 "http://127.0.0.1:$port/health/ready" >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    err "$service 未通过 readiness"
    return 1
}

start_all() {
    local service port
    start_one "$ADMIN_SERVICE" || return 1
    if ! wait_ready "$ADMIN_SERVICE" "$SETUP_PORT"; then
        stop_one "$ADMIN_SERVICE" >/dev/null || true
        return 1
    fi
    for service in "${BUSINESS_SERVICES[@]}"; do
        if config_exists "$service"; then
            preflight_service "$service" || return 1
        fi
    done
    for service in "${BUSINESS_SERVICES[@]}"; do
        if config_exists "$service"; then
            start_one "$service" 1 || return 1
            port="$(service_port "$service")" || return 1
            if ! wait_ready "$service" "$port"; then
                stop_one "$service" >/dev/null || true
                return 1
            fi
        fi
    done
}

stop_all() {
    local index
    for ((index = ${#BUSINESS_SERVICES[@]} - 1; index >= 0; index--)); do
        stop_one "${BUSINESS_SERVICES[$index]}" >/dev/null
    done
    stop_one "$ADMIN_SERVICE" >/dev/null
}

usage() {
    cat <<'USAGE'
用法: service-local.sh <命令> [thing-connect:服务名]

命令：status、start、stop、restart、start-all、stop-all、status-all
该脚本用于首次安装、故障排查，以及未接入 Supervisor 等进程管理器时的本机运行。
它不注册开机自启、不轮转日志；生产环境需由运维系统补齐这些能力。
USAGE
}

main() {
    local command_name="${1:-}" service item
    case "$command_name" in
        status|start|stop|restart)
            [ "$#" -eq 2 ] || { usage; return 2; }
            service="$(service_name "$2")" || return $?
            case "$command_name" in
                status) status_one "$service" ;;
                start) start_one "$service" ;;
                stop) stop_one "$service" ;;
                restart) stop_one "$service" >/dev/null; start_one "$service" ;;
            esac
            ;;
        start-all) [ "$#" -eq 1 ] || return 2; start_all ;;
        stop-all) [ "$#" -eq 1 ] || return 2; stop_all ;;
        status-all)
            [ "$#" -eq 1 ] || return 2
            for item in "${ALL_SERVICES[@]}"; do status_one "$item"; done
            ;;
        run)
            [ "$#" -eq 2 ] || return 2
            service="$(service_name "$2")" || return $?
            run_service "$service"
            ;;
        help|-h|--help) usage ;;
        *) usage; return 2 ;;
    esac
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    main "$@"
fi
