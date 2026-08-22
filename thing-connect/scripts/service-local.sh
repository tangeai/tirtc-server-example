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

ALL_SERVICES=("admin-server" "device-server" "user-server" "voip-server" "ai-server" "call-server")
BUSINESS_SERVICES=("device-server" "user-server" "voip-server" "ai-server" "call-server")

err() { echo "[ERROR] $1" >&2; }

service_name() {
    local value="$1"
    value="${value#"$SERVICE_GROUP:"}"
    case "$value" in
        admin-server|device-server|user-server|voip-server|ai-server|call-server)
            printf '%s\n' "$value"
            ;;
        *)
            err "未知服务: $1"
            return 2
            ;;
    esac
}

pid_file() {
    printf '%s/%s.pid\n' "$STATE_DIR" "$1"
}

managed_pid() {
    local service="$1" pid="$2" command_line
    [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 1
    kill -0 "$pid" 2>/dev/null || return 1
    [ -r "/proc/$pid/cmdline" ] || return 1
    command_line="$(tr '\0' ' ' <"/proc/$pid/cmdline")"
    [[ "$command_line" == *"$DEPLOY_ROOT/service-local.sh run $service"* ]]
}

current_pid() {
    local service="$1" file pid
    file="$(pid_file "$service")"
    [ -f "$file" ] || return 1
    pid="$(tr -d '[:space:]' <"$file")"
    if managed_pid "$service" "$pid"; then
        printf '%s\n' "$pid"
        return 0
    fi
    rm -f -- "$file"
    return 1
}

config_exists() {
    local service="$1"
    [ -f "$DEPLOY_ROOT/$service/config.yaml" ] ||
        [ -f "$DEPLOY_ROOT/config-current/$service/config.yaml" ]
}

service_port() {
    case "$1" in
        admin-server) printf '9000\n' ;;
        device-server) printf '9001\n' ;;
        user-server) printf '9002\n' ;;
        voip-server) printf '9003\n' ;;
        ai-server) printf '9004\n' ;;
        call-server) printf '9005\n' ;;
        *) return 2 ;;
    esac
}

port_in_use() {
    local port="$1"
    (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null
}

run_service() {
    local service="$1" binary="$DEPLOY_ROOT/$service/$service"
    local config="$DEPLOY_ROOT/$service/config.yaml" stopping=0 child=""
    local file
    file="$(pid_file "$service")"

    stop_child() {
        stopping=1
        [ -z "$child" ] || kill -TERM "$child" 2>/dev/null || true
    }
    trap stop_child INT TERM

    while [ "$stopping" -eq 0 ]; do
        if [ "$service" = "admin-server" ]; then
            GIN_MODE=release SERVICE_INSTANCE_ID="local-$service" \
                "$binary" -c "$config" -deploy-root "$DEPLOY_ROOT" \
                -setup-bind "$SETUP_BIND" -setup-port "$SETUP_PORT" \
                -supervisorctl "$DEPLOY_ROOT/service-local.sh" \
                -supervisor-group "$SERVICE_GROUP" &
        else
            GIN_MODE=release SERVICE_INSTANCE_ID="local-$service" \
                "$binary" -c "$config" &
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

start_one() (
    local service="$1" file pid runner port
    local binary="$DEPLOY_ROOT/$service/$service"
    mkdir -p "$STATE_DIR" "$LOG_DIR"
    exec 9>"$STATE_DIR/$service.lock"
    flock 9
    if pid="$(current_pid "$service")"; then
        echo "$SERVICE_GROUP:$service RUNNING pid $pid"
        return 0
    fi
    [ -x "$binary" ] || { err "服务尚未发布: $binary"; return 1; }
    if [ "$service" != "admin-server" ]; then
        config_exists "$service" || { err "$service 配置不存在"; return 1; }
    fi
    port="$(service_port "$service")" || return 1
    if port_in_use "$port"; then
        err "$service 无法启动：端口 $port 已被占用"
        err "处理建议：执行 ss -ltnp | grep ':$port' 确认占用进程；停止旧实例或重复的进程管理器后重试"
        return 1
    fi
    file="$(pid_file "$service")"
    nohup setsid "$DEPLOY_ROOT/service-local.sh" run "$service" \
        >>"$LOG_DIR/$service.out.log" 2>>"$LOG_DIR/$service.err.log" </dev/null 9>&- &
    runner=$!
    printf '%s\n' "$runner" >"$file.tmp"
    mv -f -- "$file.tmp" "$file"
    sleep 1
    pid="$(current_pid "$service")" || {
        err "$service 本地启动失败，请检查 $LOG_DIR/$service.err.log"
        return 1
    }
    echo "$SERVICE_GROUP:$service RUNNING pid $pid"
)

stop_one() (
    local service="$1" pid elapsed=0
    mkdir -p "$STATE_DIR"
    exec 9>"$STATE_DIR/$service.lock"
    flock 9
    if ! pid="$(current_pid "$service")"; then
        echo "$SERVICE_GROUP:$service STOPPED"
        return 0
    fi
    kill -TERM "$pid" 2>/dev/null || true
    while managed_pid "$service" "$pid" && [ "$elapsed" -lt 15 ]; do
        sleep 1
        elapsed=$((elapsed + 1))
    done
    if managed_pid "$service" "$pid"; then
        kill -KILL -- "-$pid" 2>/dev/null || true
    fi
    rm -f -- "$(pid_file "$service")"
    echo "$SERVICE_GROUP:$service STOPPED"
)

status_one() {
    local service="$1" pid
    if pid="$(current_pid "$service")"; then
        echo "$SERVICE_GROUP:$service RUNNING pid $pid"
    else
        echo "$SERVICE_GROUP:$service STOPPED"
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
    start_one admin-server >/dev/null || return 1
    if ! wait_ready admin-server "$SETUP_PORT"; then
        stop_one admin-server >/dev/null || true
        return 1
    fi
    for service in "${BUSINESS_SERVICES[@]}"; do
        if config_exists "$service"; then
            start_one "$service" >/dev/null || return 1
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
    stop_one admin-server >/dev/null
}

usage() {
    cat <<'USAGE'
用法: service-local.sh <命令> [thing-connect:服务名]

命令：status、start、stop、restart、start-all、stop-all、status-all
该脚本只用于首次安装和切换 Supervisor 前的本机运行，不替代生产进程托管。
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
