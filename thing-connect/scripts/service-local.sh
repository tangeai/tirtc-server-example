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
# Nginx internal entry for ai/voip/call internal APIs (deploy/nginx template).
INTERNAL_GATEWAY_PORT=9100
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

# Counts are persisted separately from generated service configuration. Ports
# use the same +10 convention as the Supervisor template; Admin stays singleton.
declare -A LOCAL_INSTANCE_COUNT=()
load_instance_counts() {
    local service count extra file="$DEPLOY_ROOT/local-instances"
    LOCAL_INSTANCE_COUNT=()
    [ -e "$file" ] || { [ ! -L "$file" ]; return; }
    [ -f "$file" ] && [ ! -L "$file" ] || { err 'local-instances 必须为普通文件'; return 1; }
    while read -r service count extra || [ -n "$service" ]; do
        [ -n "$service" ] && [ -n "${SERVICE_PORT[$service]+present}" ] &&
            [[ "$count" =~ ^([1-9]|[12][0-9]|3[0-2])$ ]] && [ -z "$extra" ] &&
            [ -z "${LOCAL_INSTANCE_COUNT[$service]+present}" ] || { err 'local-instances 包含无效或重复配置'; return 1; }
        [ "$service" != "$ADMIN_SERVICE" ] || [ "$count" = 1 ] || { err 'Admin 只支持单实例'; return 1; }
        LOCAL_INSTANCE_COUNT[$service]="$count"
    done <"$file"
    validate_instance_ports
}

validate_instance_ports() {
    local service program port
    local -A seen=([$INTERNAL_GATEWAY_PORT]=内网入口)
    for service in "${ALL_SERVICES[@]}"; do
        for program in $(service_programs "$service"); do
            port="$(service_port "$program")" || return 1
            [ "$port" -le 65535 ] || { err "实例端口超出范围: $program ($port)"; return 1; }
            [ -z "${seen[$port]+present}" ] || { err "实例端口冲突: $program 与 ${seen[$port]} 都使用 $port"; return 1; }
            seen[$port]="$program"
        done
    done
}

base_service() {
    local value="${1#"$SERVICE_GROUP:"}" base suffix
    if [ -n "${SERVICE_PORT[$value]+present}" ]; then printf '%s\n' "$value"; return; fi
    base="${value%-*}"; suffix="${value##*-}"
    if [ -n "${SERVICE_PORT[$base]+present}" ] && [ "$base" != "$ADMIN_SERVICE" ] &&
        [[ "$suffix" =~ ^([2-9]|[12][0-9]|3[0-2])$ ]]; then
        printf '%s\n' "$base"; return
    fi
    err "未知服务或实例: $1"; return 2
}

service_name() {
    base_service "$1" >/dev/null || return $?
    printf '%s\n' "${1#"$SERVICE_GROUP:"}"
}

service_programs() {
    local service="$1" i
    printf '%s\n' "$service"
    for ((i=2; i<=${LOCAL_INSTANCE_COUNT[$service]:-1}; i++)); do
        printf '%s-%s\n' "$service" "$i"
    done
}

set_instances() (
    local service="$1" count="$2" program item pending
    [ "$service" = "$(base_service "$service")" ] || { err '请对服务名设置实例数'; return 2; }
    [[ "$count" =~ ^([1-9]|[12][0-9]|3[0-2])$ ]] || { err '实例数必须为 1-32'; return 2; }
    [ "$service" != "$ADMIN_SERVICE" ] || [ "$count" = 1 ] || { err 'Admin 只支持单实例'; return 2; }
    exec 6>"$DEPLOY_ROOT/deploy.lock"
    flock -n 6 || { err '另一个安装或发布任务正在运行'; return 1; }
    # Increasing the inventory need not interrupt the primary. Shrinking must
    # not orphan removed runners, even if they lost their PID files.
    for program in $(seq -f "$service-%g" "$((count+1))" 32); do
        [ -z "$(managed_pids "$program")" ] || { err "请先停止待移除实例 $program 再减少数量"; return 1; }
    done
    LOCAL_INSTANCE_COUNT[$service]="$count"
    validate_instance_ports || return 1
    pending="$(mktemp "$DEPLOY_ROOT/.local-instances.XXXXXX")" || return 1
    trap 'rm -f -- "$pending"' EXIT
    for item in "${ALL_SERVICES[@]}"; do
        printf '%s\t%s\n' "$item" "${LOCAL_INSTANCE_COUNT[$item]:-1}" >>"$pending"
    done
    chmod 600 "$pending"
    mv -f -- "$pending" "$DEPLOY_ROOT/local-instances"
    echo "$service 实例数=$count；执行 start $service 启动，再刷新本地网关配置"
)

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
    # deploy-prod.sh sets this only while restoring a release that already ran.
    [ "${THING_CONNECT_SKIP_START_PREFLIGHT:-0}" != 1 ] || return 0
    local service checker="$DEPLOY_ROOT/$ADMIN_SERVICE/$ADMIN_SERVICE"
    service="$(base_service "$1")" || return 1
    local -a instance_args=()
    if [ "${LOCAL_INSTANCE_COUNT[$service]:-1}" -gt 1 ]; then instance_args=(-check-service-instances "${LOCAL_INSTANCE_COUNT[$service]}"); fi
    [ "$service" != "$ADMIN_SERVICE" ] || return 0
    [ -x "$checker" ] || {
        err "无法检查 $service 配置：Admin 程序不存在或不可执行: $checker"
        return 1
    }
    echo "检查 $service 的基础配置、必填配置和依赖连接..." >&2
    if "$checker" -c "$DEPLOY_ROOT/$ADMIN_SERVICE/config.yaml" \
        -deploy-root "$DEPLOY_ROOT" -check-service-config "$service" "${instance_args[@]}"; then
        return 0
    else
        local status=$?
        err "$service 启动前检查失败，未启动服务"
        err "处理建议：登录 Admin 完成页面列出的必填配置；修复后重新执行 $DEPLOY_ROOT/service-local.sh start $SERVICE_GROUP:$service"
        return "$status"
    fi
}

service_port() {
    local service index=1
    service="$(base_service "$1")" || return 2
    [ "$1" = "$service" ] || index="${1##*-}"
    printf '%s\n' "$((SERVICE_PORT[$service] + (index-1)*10))"
}

# 与 Supervisor 的 stopwaitsecs 一致。User 等待邮件投递和确认，Recognition 等待在途模型调用。
stop_wait_seconds() {
    case "$1" in
        recognition-server|recognition-server-*) echo 100 ;;
        user-server|user-server-*) echo 50 ;;
        *) echo 15 ;;
    esac
}

port_in_use() {
    local port="$1"
    (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null
}

run_service() {
    local service="$1" base binary config port stopping=0 child=""
    base="$(base_service "$service")" || return 1
    if [ "$base" != "$service" ] && [ "${service##*-}" -gt "${LOCAL_INSTANCE_COUNT[$base]:-1}" ]; then
        err "$service 未配置，请先设置 $base 的实例数"; return 1
    fi
    binary="$DEPLOY_ROOT/$base/$base"
    config="$DEPLOY_ROOT/$base/config.yaml"
    port="$(service_port "$service")" || return 1
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
            GIN_MODE=release SERVICE_INSTANCE_ID="local-$service" THING_CONNECT_HTTP_PORT="$port" \
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
    local base binary
    base="$(base_service "$service")" || return 1
    binary="$DEPLOY_ROOT/$base/$base"
    if [ "$base" != "$service" ] && [ "${service##*-}" -gt "${LOCAL_INSTANCE_COUNT[$base]:-1}" ]; then
        err "$service 未配置，请先设置 $base 的实例数"; return 1
    fi
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
        config_exists "$base" || { err "$service 配置不存在"; return 1; }
        [ "$prechecked" = "1" ] || preflight_service "$service" || return $?
    fi
    port="$(service_port "$service")" || return 1
    if port_in_use "$port"; then
        err "$service 无法启动：端口 $port 已被占用"
        err "处理建议：执行 ss -ltnp | grep ':$port' 确认占用进程；停止旧实例或重复的进程管理器后重试"
        return 1
    fi
    nohup setsid "$DEPLOY_ROOT/service-local.sh" run "$service" \
        >>"$LOG_DIR/$service.out.log" 2>>"$LOG_DIR/$service.err.log" </dev/null 9>&- 5>&- &
    sleep 1
    if ! pid="$(current_pid "$service")"; then
        err "$service 本地启动失败，请检查 $LOG_DIR/$service.err.log"
        return 1
    fi
    print_status "$service" "$pid"
)

stop_one() (
    local service="$1" pid pgid elapsed=0 limit
    local -a pids=() remaining=()
    limit="$(stop_wait_seconds "$service")"
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
    while [ "$elapsed" -lt "$limit" ]; do
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

control_service() {
    local action="$1" target="$2" base program port
    local -a programs=()
    base="$(base_service "$target")" || return 2
    if [ "$target" = "$base" ]; then
        mapfile -t programs < <(service_programs "$base")
    else
        programs=("$target")
    fi
    for program in "${programs[@]}"; do
        case "$action" in
            status) status_one "$program" ;;
            stop) stop_one "$program" || return 1 ;;
            start|restart)
                if [ "$action" = restart ]; then
                    # Preflight before disrupting a healthy instance.
                    [ "$base" = "$ADMIN_SERVICE" ] || preflight_service "$base" || return $?
                    stop_one "$program" >/dev/null || return 1
                    start_one "$program" 1 || return $?
                else
                    start_one "$program" || return $?
                fi
                # Preserve asynchronous single-instance start compatibility.
                # Multi-instance starts and restarts advance only after readiness.
                if [ "${LOCAL_INSTANCE_COUNT[$base]:-1}" -gt 1 ]; then
                    port="$(service_port "$program")" || return 1
                    wait_ready "$program" "$port" || return 1
                fi
                ;;
        esac
    done
}

start_all() {
    local service port program
    validate_enabled_services || return 1
    start_one "$ADMIN_SERVICE" || return 1
    if ! wait_ready "$ADMIN_SERVICE" "$SETUP_PORT"; then
        stop_one "$ADMIN_SERVICE" >/dev/null || true
        return 1
    fi
    for service in "${BUSINESS_SERVICES[@]}"; do
        service_is_enabled "$service" || continue
        if [ "$service" = recognition-server ] && [ "${DEFER_RECOGNITION_START:-0}" = 1 ]; then continue; fi
        if config_exists "$service"; then
            preflight_service "$service" || return $?
        elif [ -f "$DEPLOY_ROOT/enabled-services" ]; then
            err "$service 已启用但配置不存在；请配置后重试，或停用该可选服务"
            return 1
        fi
    done
    for service in "${BUSINESS_SERVICES[@]}"; do
        service_is_enabled "$service" || continue
        if [ "$service" = recognition-server ] && [ "${DEFER_RECOGNITION_START:-0}" = 1 ]; then continue; fi
        if config_exists "$service"; then
            for program in $(service_programs "$service"); do
                start_one "$program" 1 || return $?
                port="$(service_port "$program")" || return 1
                if ! wait_ready "$program" "$port"; then
                    stop_one "$program" >/dev/null || true
                    return 1
                fi
            done
        fi
    done
}

stop_all() {
    local index
    for ((index = ${#BUSINESS_SERVICES[@]} - 1; index >= 0; index--)); do
        control_service stop "${BUSINESS_SERVICES[$index]}" >/dev/null
    done
    stop_one "$ADMIN_SERVICE" >/dev/null
}

set_service_enabled() (
    local service="$1" enabled="$2" item pending
    [ "${SERVICE_REQUIRED[$service]}" != true ] || {
        [ "$enabled" = 1 ] && return 0
        err "必需服务不能停用: $service"; return 1
    }
    mkdir -p "$STATE_DIR"
    exec 6>"$DEPLOY_ROOT/deploy.lock"
    flock -n 6 || { err "另一个安装或发布任务正在运行"; return 1; }
    exec 7>"$STATE_DIR/enabled-services.lock"
    flock 7
    validate_enabled_services || return 1
    pending="$(mktemp "$DEPLOY_ROOT/.enabled-services.XXXXXX")" || return 1
    trap 'rm -f -- "$pending"' EXIT
    for item in "${ALL_SERVICES[@]}"; do
        if [ "$item" = "$service" ]; then
            [ "$enabled" != 1 ] || printf '%s\n' "$item" >>"$pending"
        elif service_is_enabled "$item"; then
            printf '%s\n' "$item" >>"$pending"
        fi
    done
    chmod 600 "$pending"
    if [ "$enabled" = 0 ]; then control_service stop "$service" || return 1; fi
    mv -f "$pending" "$DEPLOY_ROOT/enabled-services" || return 1
    echo "$service 启用状态=$enabled；执行 start-all 启动已启用服务"
)

usage() {
    cat <<'USAGE'
用法: service-local.sh <命令> [thing-connect:服务名]

命令：status、start、stop、restart、start-all、stop-all、status-all、enable、disable、instances、ports
instances <服务名> <1-32>：设置数量；缩容前停止待移除实例（Admin 仅 1）。
服务名操作全部实例；<服务名>-2 等只操作指定实例。多实例 restart 逐台等待就绪。
ports <服务名>：输出该服务所有实例的 HTTP 端口，供本地网关使用。
enable/disable 持久化可选服务启用状态；disable 停止该服务。start-all 只启动已启用服务。
该脚本用于首次安装、故障排查，以及未接入 Supervisor 等进程管理器时的本机运行。
它不注册开机自启、不轮转日志；生产环境需由运维系统补齐这些能力。
USAGE
}

main() {
    local command_name="${1:-}" service item
    mkdir -p "$STATE_DIR"
    if [ "$command_name" != run ]; then
        exec 5>"$STATE_DIR/instances.lock"
        if [ "$command_name" = instances ]; then
            flock -n -x 5 || { err '另一个服务操作正在运行'; return 1; }
        else
            flock -s 5
        fi
    fi
    load_instance_counts || return 1
    case "$command_name" in
        status|start|stop|restart)
            [ "$#" -eq 2 ] || { usage; return 2; }
            service="$(service_name "$2")" || return $?
            control_service "$command_name" "$service"
            ;;
        enable|disable)
            [ "$#" -eq 2 ] || { usage; return 2; }
            service="$(base_service "$2")" || return $?
            if [ "$command_name" = enable ]; then set_service_enabled "$service" 1; else set_service_enabled "$service" 0; fi
            ;;
        instances)
            [ "$#" -eq 3 ] || { usage; return 2; }
            service="$(service_name "$2")" || return $?
            set_instances "$service" "$3"
            ;;
        ports)
            [ "$#" -eq 2 ] || return 2
            service="$(base_service "$2")" || return $?
            for item in $(service_programs "$service"); do service_port "$item"; done
            ;;
        start-all) [ "$#" -eq 1 ] || return 2; start_all ;;
        stop-all) [ "$#" -eq 1 ] || return 2; stop_all ;;
        status-all)
            [ "$#" -eq 1 ] || return 2
            for item in "${ALL_SERVICES[@]}"; do control_service status "$item"; done
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
