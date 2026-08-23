#!/usr/bin/env bash

set -Eeuo pipefail

# ================== 配置 ==================
# 常规部署只需按实际环境修改下面两行；同名环境变量可临时覆盖。
PRODUCTION_DEPLOY_ROOT="/opt/thing-connect"
DEFAULT_SUPERVISOR_GROUP="thing-connect"

SUPERVISOR_GROUP="${SUPERVISOR_GROUP:-$DEFAULT_SUPERVISOR_GROUP}"

# 生产发布根目录。现有部署可继续通过 DEPLOY_ROOT 指向原目录。
DEPLOY_ROOT_INPUT="${DEPLOY_ROOT:-$PRODUCTION_DEPLOY_ROOT}"
case "$DEPLOY_ROOT_INPUT" in
    /*) ;;
    *)
        echo "[ERROR] DEPLOY_ROOT 必须是绝对路径: ${DEPLOY_ROOT_INPUT}"
        exit 1
        ;;
esac
if [ "$DEPLOY_ROOT_INPUT" = "/" ]; then
    echo "[ERROR] DEPLOY_ROOT 不能是文件系统根目录"
    exit 1
fi
if ! mkdir -p -- "$DEPLOY_ROOT_INPUT"; then
    echo "[ERROR] 无法创建部署目录: ${DEPLOY_ROOT_INPUT}"
    exit 1
fi
DEPLOY_ROOT="$(cd "$DEPLOY_ROOT_INPUT" && pwd)"
REPO_PATH="${REPO_PATH:-$DEPLOY_ROOT/tirtc-server-example}"
BUILD_DIR="${BUILD_DIR:-$REPO_PATH/thing-connect}"
DEPLOY_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

load_deployment_service_catalog() {
    local loader catalog
    if [ -f "$BUILD_DIR/scripts/service-catalog.sh" ] &&
       [ -f "$BUILD_DIR/internal/installer/service_catalog.tsv" ]; then
        loader="$BUILD_DIR/scripts/service-catalog.sh"
        catalog="$BUILD_DIR/internal/installer/service_catalog.tsv"
    elif [ -f "$DEPLOY_SCRIPT_DIR/service-catalog.sh" ] &&
         [ -f "$DEPLOY_SCRIPT_DIR/../internal/installer/service_catalog.tsv" ]; then
        loader="$DEPLOY_SCRIPT_DIR/service-catalog.sh"
        catalog="$DEPLOY_SCRIPT_DIR/../internal/installer/service_catalog.tsv"
    else
        loader="$DEPLOY_ROOT/service-catalog.sh"
        catalog="$DEPLOY_ROOT/service-catalog.tsv"
    fi
    [ -f "$loader" ] && [ -f "$catalog" ] || {
        echo "[ERROR] 服务清单或加载器不存在" >&2
        return 1
    }
    # shellcheck source=service-catalog.sh
    source "$loader"
    load_service_catalog "$catalog"
    STOP_ORDER=("${BUSINESS_SERVICES[@]}" "$ADMIN_SERVICE")
}

load_deployment_service_catalog || exit 1

# Supervisor 是可选的自动进程管理器；未接入 Supervisor 时，发布脚本只更新
# 文件和数据库，服务由运维人员通过 service-local.sh 手动停止、启动和验收。
SUPERVISORCTL="${SUPERVISORCTL:-supervisorctl}"
# 对应 deploy/supervisor/thing-connect.supervisor.conf 的 [group:thing-connect]。
SUPERVISOR_WAIT_SECONDS="${SUPERVISOR_WAIT_SECONDS:-15}"
SUPERVISOR_STABLE_SECONDS="${SUPERVISOR_STABLE_SECONDS:-2}"
SERVICE_MANAGER="${SERVICE_MANAGER:-auto}"
ACTIVE_SERVICE_MANAGER=""
LOCAL_SERVICECTL="${LOCAL_SERVICECTL:-$DEPLOY_ROOT/service-local.sh}"
HEALTH_WAIT_SECONDS="${HEALTH_WAIT_SECONDS:-30}"
HEALTH_REQUEST_TIMEOUT_SECONDS="${HEALTH_REQUEST_TIMEOUT_SECONDS:-3}"
HEALTH_HOST="${HEALTH_HOST:-127.0.0.1}"
BACKUP_KEEP_COUNT="${BACKUP_KEEP_COUNT:-10}"
# Database migrations are irreversible by the binary rollback path. When the
# read-only migration check reports pending DDL, operators must provide a
# non-empty backup file whose restore has been tested before migration starts.
DATABASE_BACKUP_FILE="${DATABASE_BACKUP_FILE:-}"
DATABASE_BACKUP_RESTORE_VERIFIED="${DATABASE_BACKUP_RESTORE_VERIFIED:-0}"
MIGRATION_CONFIG="${MIGRATION_CONFIG:-$DEPLOY_ROOT/admin-server/migration-config.yaml}"
SKIP_MIGRATIONS="${SKIP_MIGRATIONS:-0}"
ALLOW_INSECURE_ADMIN_COOKIE="${ALLOW_INSECURE_ADMIN_COOKIE:-1}"
MIGRATION_CHANGED=0
MIGRATION_OUTPUT_FILE=""
MIGRATION_PID=""

mkdir -p "$DEPLOY_ROOT/logs"

# Business services must load their initial registry values from Admin before
# listening, so a full deployment starts/restarts Admin first.
ACTIVE_SERVICES=()
ACTIVE_BUSINESS_SERVICES=()
ACTIVE_STOP_ORDER=()

# ================== 工具函数 ==================
log() { echo -e "[INFO] $1"; }
warn() { echo -e "[WARN] $1"; }
err() { echo -e "[ERROR] $1"; }

# 锁只覆盖一次会修改代码、文件、数据库或进程状态的操作。交互菜单本身不持锁。
with_deploy_lock() (
    command -v flock >/dev/null 2>&1 || {
        err "找不到 flock；无法保证部署与安装任务互斥"
        return 1
    }
    exec 9>"$DEPLOY_ROOT/deploy.lock"
    if ! flock -n 9; then
        err "另一个发布或安装任务正在运行"
        return 1
    fi
    "$@"
)

validate_deploy_root() {
    [ -d "$DEPLOY_ROOT" ] || { err "部署目录不存在: $DEPLOY_ROOT"; return 1; }
}

validate_paths() {
    validate_deploy_root || return 1
    [ -f "$BUILD_DIR/go.mod" ] || {
        err "源码目录未就绪（缺少 $BUILD_DIR/go.mod）"
        err "请检查 DEPLOY_ROOT=$DEPLOY_ROOT 与 BUILD_DIR=$BUILD_DIR"
        return 1
    }
}

service_config_path() {
    local svc="$1"
    local direct="$DEPLOY_ROOT/$svc/config.yaml"
    local bundled="$DEPLOY_ROOT/config-current/$svc/config.yaml"
    if [ -f "$direct" ] || [ -L "$direct" ]; then
        printf '%s\n' "$direct"
        return
    fi
    if [ -f "$bundled" ]; then
        printf '%s\n' "$bundled"
        return
    fi
    return 1
}

# A service is installed when it has an active config. Required/optional
# membership comes from the shared service catalog.
resolve_active_services() {
    local svc
    ACTIVE_SERVICES=()
    ACTIVE_BUSINESS_SERVICES=()
    ACTIVE_STOP_ORDER=()
    for svc in "${REQUIRED_SERVICES[@]}"; do
        service_config_path "$svc" >/dev/null || {
            err "$svc 是必需服务，但 config.yaml 和已激活配置包均不存在"
            return 1
        }
    done
    ACTIVE_SERVICES=("$ADMIN_SERVICE")
    for svc in "${BUSINESS_SERVICES[@]}"; do
        if service_config_path "$svc" >/dev/null; then
            ACTIVE_SERVICES+=("$svc")
            ACTIVE_BUSINESS_SERVICES+=("$svc")
        fi
    done
    ACTIVE_STOP_ORDER=("${ACTIVE_BUSINESS_SERVICES[@]}" "$ADMIN_SERVICE")
}

# ================== 服务管理器 ==================
supervisor_program() {
    printf '%s:%s\n' "$SUPERVISOR_GROUP" "$1"
}

supervisor_state() {
    local svc="$1" program status
    program="$(supervisor_program "$svc")"
    status="$("$SUPERVISORCTL" status "$program" 2>&1 || true)"
    # supervisorctl 查询单个组成员时，输出名称可能是 group:program 或 program；
    # 两种格式的状态都位于第二列。
    awk '$2 ~ /^(STOPPED|STARTING|BACKOFF|RUNNING|EXITED|FATAL|UNKNOWN)$/ { print $2; exit }' <<< "$status"
}

require_supervisor_service() {
    local svc="$1" state
    command -v "$SUPERVISORCTL" >/dev/null 2>&1 || {
        err "找不到 $SUPERVISORCTL；当前操作不能使用 Supervisor 自动管理服务"
        return 1
    }
    state="$(supervisor_state "$svc")"
    [ -n "$state" ] || {
        err "Supervisor 中未找到服务 $(supervisor_program "$svc")"
        return 1
    }
}

wait_for_supervisor_state() {
    local svc="$1" expected="$2" state elapsed=0
    while [ "$elapsed" -lt "$SUPERVISOR_WAIT_SECONDS" ]; do
        state="$(supervisor_state "$svc")"
        if [ "$state" = "$expected" ]; then
            # Supervisor 刚报告 RUNNING 时，进程仍可能马上因端口或配置错误退出。
            # 在成功前保持短暂观察，避免把瞬时 RUNNING 误判为发布成功。
            if [ "$expected" = "RUNNING" ] && [ "$SUPERVISOR_STABLE_SECONDS" -gt 0 ]; then
                sleep "$SUPERVISOR_STABLE_SECONDS"
                state="$(supervisor_state "$svc")"
                [ "$state" = "RUNNING" ] || continue
            fi
            return 0
        fi
        if [ "$state" = "FATAL" ] || [ "$state" = "BACKOFF" ] || [ "$state" = "EXITED" ]; then
            err "$svc 未能启动，Supervisor 状态: $state"
            "$SUPERVISORCTL" status "$(supervisor_program "$svc")" || true
            return 1
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    err "$svc 在 ${SUPERVISOR_WAIT_SECONDS}s 内未进入 $expected 状态（当前: ${state:-UNKNOWN}）"
    "$SUPERVISORCTL" status "$(supervisor_program "$svc")" || true
    return 1
}

validate_supervisor_inventory() {
    local svc
    local -a services=("$@")
    command -v "$SUPERVISORCTL" >/dev/null 2>&1 || {
        err "找不到 $SUPERVISORCTL"
        return 1
    }
    if [ "${#services[@]}" -eq 0 ]; then
        services=("${ACTIVE_SERVICES[@]}")
    fi
    if [ "${#services[@]}" -eq 0 ]; then
        services=("${ALL_SERVICES[@]}")
    fi
    for svc in "${services[@]}"; do
        require_supervisor_service "$svc" || return 1
    done
}

select_service_manager() {
    local svc state configured=0 total="${#ACTIVE_SERVICES[@]}"
    case "$SERVICE_MANAGER" in
        auto|supervisor|manual) ;;
        *)
            err "SERVICE_MANAGER 只能是 auto、supervisor 或 manual"
            return 1
            ;;
    esac

    if [ "$SERVICE_MANAGER" = "manual" ]; then
        if command -v "$SUPERVISORCTL" >/dev/null 2>&1; then
            for svc in "${ACTIVE_SERVICES[@]}"; do
                state="$(supervisor_state "$svc")"
                [ -z "$state" ] || configured=$((configured + 1))
            done
            if [ "$configured" -ne 0 ]; then
                err "SERVICE_MANAGER=manual 不能忽略 Supervisor 中已有的 $configured 个 ThingConnect 服务"
                err "请先停止并移除这些 Supervisor 条目，避免两个进程管理器重复启动服务"
                return 1
            fi
        fi
        ACTIVE_SERVICE_MANAGER="manual"
    elif [ "$SERVICE_MANAGER" = "supervisor" ]; then
        validate_supervisor_inventory "${ACTIVE_SERVICES[@]}" || return 1
        ACTIVE_SERVICE_MANAGER="supervisor"
    elif ! command -v "$SUPERVISORCTL" >/dev/null 2>&1; then
        ACTIVE_SERVICE_MANAGER="manual"
    else
        for svc in "${ACTIVE_SERVICES[@]}"; do
            state="$(supervisor_state "$svc")"
            [ -z "$state" ] || configured=$((configured + 1))
        done
        if [ "$configured" -eq 0 ]; then
            ACTIVE_SERVICE_MANAGER="manual"
        elif [ "$configured" -eq "$total" ]; then
            ACTIVE_SERVICE_MANAGER="supervisor"
        else
            err "Supervisor 只配置了 $configured/$total 个已安装服务，拒绝混用进程管理器"
            err "请补齐 Supervisor 清单，或停止并移除 ThingConnect 的 Supervisor 条目后使用 $LOCAL_SERVICECTL"
            return 1
        fi
    fi

    if [ "$ACTIVE_SERVICE_MANAGER" = "manual" ]; then
        [ -x "$LOCAL_SERVICECTL" ] || {
            err "未使用 Supervisor，且本地服务脚本不存在或不可执行: $LOCAL_SERVICECTL"
            return 1
        }
        log "未检测到完整的 ThingConnect Supervisor 清单；使用手动服务管理模式"
    else
        validate_supervisor_inventory "${ACTIVE_SERVICES[@]}" || return 1
        log "服务管理器校验通过：Supervisor"
    fi
}

manual_service_control_hint() {
    local action="$1"
    case "$action" in
        start)
            err "请手动执行: sudo $LOCAL_SERVICECTL start-all"
            ;;
        stop)
            err "请手动执行: sudo $LOCAL_SERVICECTL stop-all"
            ;;
        restart)
            err "请手动执行: sudo $LOCAL_SERVICECTL stop-all"
            err "然后执行: sudo $LOCAL_SERVICECTL start-all"
            ;;
        status)
            err "请手动执行: sudo $LOCAL_SERVICECTL status-all"
            ;;
    esac
}

validate_manual_services_stopped() {
    local output svc cfg port active=0
    output="$("$LOCAL_SERVICECTL" status-all 2>&1)" || {
        err "无法读取本地服务状态: $LOCAL_SERVICECTL status-all"
        [ -z "$output" ] || printf '%s\n' "$output" >&2
        return 1
    }
    if grep -Eq ' (RUNNING|STARTING|CONFLICT)( |$)' <<<"$output"; then
        active=1
    fi
    for svc in "${ACTIVE_SERVICES[@]}"; do
        cfg="$(service_config_path "$svc")" || continue
        port="$(yaml_section_value "$cfg" server http_port)"
        [[ "$port" =~ ^[1-9][0-9]*$ ]] || continue
        if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
            output+=$'\n'"$svc 端口 $port 仍在监听"
            active=1
        fi
    done
    if [ "$active" -ne 0 ]; then
        err "手动服务管理模式要求更新前停止全部本地服务，避免迁移期间混跑新旧版本"
        printf '%s\n' "$output" >&2
        manual_service_control_hint stop
        err "停止后重新执行本次更新命令"
        return 1
    fi
    log "本地服务均已停止，可以安全更新文件和数据库"
}

validate_service_manager() {
    [[ "$SUPERVISOR_WAIT_SECONDS" =~ ^[1-9][0-9]*$ ]] || {
        err "SUPERVISOR_WAIT_SECONDS 必须是正整数"
        return 1
    }
    [[ "$SUPERVISOR_STABLE_SECONDS" =~ ^[0-9]+$ ]] || {
        err "SUPERVISOR_STABLE_SECONDS 必须是非负整数"
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
    command -v curl >/dev/null 2>&1 || {
        err "找不到 curl；生产发布必须执行 readiness 检查"
        return 1
    }
    select_service_manager
}

prepare_update_service_control() {
    resolve_active_services || return 1
    validate_service_manager || return 1
    if [ "$ACTIVE_SERVICE_MANAGER" = "manual" ]; then
        validate_manual_services_stopped || return 1
    fi
}

validate_backup_retention() {
    [[ "$BACKUP_KEEP_COUNT" =~ ^[1-9][0-9]*$ ]] || {
        err "BACKUP_KEEP_COUNT 必须是正整数"
        return 1
    }
}

validate_release_options() {
    [[ "$SKIP_MIGRATIONS" =~ ^[01]$ ]] || {
        err "SKIP_MIGRATIONS 只能是 0 或 1"
        return 1
    }
    [[ "$ALLOW_INSECURE_ADMIN_COOKIE" =~ ^[01]$ ]] || {
        err "ALLOW_INSECURE_ADMIN_COOKIE 只能是 0 或 1"
        return 1
    }
    [[ "$DATABASE_BACKUP_RESTORE_VERIFIED" =~ ^[01]$ ]] || {
        err "DATABASE_BACKUP_RESTORE_VERIFIED 只能是 0 或 1"
        return 1
    }
}

validate_database_backup() {
    local digest
    [ "$SKIP_MIGRATIONS" != "1" ] || return 0
    [ "$DATABASE_BACKUP_RESTORE_VERIFIED" = "1" ] || {
        err "数据库迁移被拒绝：尚未确认备份已完成恢复演练"
        err "先生成专用数据库备份并验证可恢复，再设置 DATABASE_BACKUP_FILE 和 DATABASE_BACKUP_RESTORE_VERIFIED=1"
        return 1
    }
    case "$DATABASE_BACKUP_FILE" in
        /*) ;;
        *) err "DATABASE_BACKUP_FILE 必须是绝对路径"; return 1 ;;
    esac
    [ -f "$DATABASE_BACKUP_FILE" ] && [ -r "$DATABASE_BACKUP_FILE" ] && [ -s "$DATABASE_BACKUP_FILE" ] || {
        err "数据库备份不存在、不可读或为空: $DATABASE_BACKUP_FILE"
        return 1
    }
    command -v sha256sum >/dev/null 2>&1 || {
        err "找不到 sha256sum，无法校验数据库备份文件"
        return 1
    }
    digest="$(sha256sum -- "$DATABASE_BACKUP_FILE" | awk '{print $1}')" || return 1
    [ "${#digest}" -eq 64 ] || {
        err "数据库备份校验和生成失败"
        return 1
    }
    if [ -n "${BACKUP_DIR:-}" ]; then
        printf '%s  %s\n' "$digest" "$DATABASE_BACKUP_FILE" >"$BACKUP_DIR/database-backup.sha256" || return 1
        chmod 0600 "$BACKUP_DIR/database-backup.sha256" || return 1
    fi
    log "数据库备份门槛通过: $DATABASE_BACKUP_FILE (sha256=$digest)"
}

is_valid_service() {
    local candidate="$1" svc
    for svc in "${ALL_SERVICES[@]}"; do
        [ "$candidate" = "$svc" ] && return 0
    done

    return 1
}

validate_services() {
    local svc
    for svc in "$@"; do
        is_valid_service "$svc" || { err "未知服务: $svc"; return 1; }
    done
}

yaml_top_value() {
    local file="$1" key="$2"
    awk -v key="$key" '
    function scalar_value(value, quote, end_quote) {
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
        quote = substr(value, 1, 1)
        if (quote == "\"" || quote == sprintf("%c", 39)) {
            value = substr(value, 2)
            end_quote = quote "[[:space:]]*(#.*)?$"
            sub(end_quote, "", value)
        } else {
            sub(/[[:space:]]+#.*$/, "", value)
            gsub(/[[:space:]]+$/, "", value)
        }
        return value
    }
    { sub(/\r$/, "") } $0 ~ "^" key ":[[:space:]]*" {
        sub("^" key ":[[:space:]]*", ""); print scalar_value($0); exit
    }' "$file"
}

yaml_section_value() {
    local file="$1" section="$2" key="$3"
    awk -v section="$section" -v key="$key" '
        function scalar_value(value, quote, end_quote) {
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
            quote = substr(value, 1, 1)
            if (quote == "\"" || quote == sprintf("%c", 39)) {
                value = substr(value, 2)
                end_quote = quote "[[:space:]]*(#.*)?$"
                sub(end_quote, "", value)
            } else {
                sub(/[[:space:]]+#.*$/, "", value)
                gsub(/[[:space:]]+$/, "", value)
            }
            return value
        }
        { sub(/\r$/, "") }
        $0 ~ "^" section ":[[:space:]]*(#.*)?$" { inside=1; next }
        /^[^[:space:]]/ { inside=0 }
        inside && $0 ~ "^[[:space:]]+" key ":[[:space:]]*" {
            sub("^[^:]+:[[:space:]]*", ""); print scalar_value($0); exit
        }
    ' "$file"
}

yaml_has_section_key() {
    local file="$1" section="$2" key="$3"
    awk -v section="$section" -v key="$key" '
        { sub(/\r$/, "") }
        $0 ~ "^" section ":[[:space:]]*(#.*)?$" { inside=1; next }
        /^[^[:space:]]/ { inside=0 }
        inside && $0 ~ "^[[:space:]]+" key ":[[:space:]]*" { found=1; exit }
        END { exit !found }
    ' "$file"
}

is_placeholder_secret() {
    local normalized="${1,,}"
    normalized="${normalized//[[:space:]]/}"
    case "$normalized" in
        change-me-in-production|replace-with-*|your-*) return 0 ;;
        *) return 1 ;;
    esac
}

is_http_url() {
    [[ "$1" =~ ^https?://[^[:space:]]+$ ]]
}

strict_validate_config_syntax() {
    local validator=""
    if [ -x "$BUILD_DIR/bin/admin-server" ]; then
        validator="$BUILD_DIR/bin/admin-server"
    elif [ -x "$DEPLOY_ROOT/admin-server/admin-server" ]; then
        validator="$DEPLOY_ROOT/admin-server/admin-server"
    else
        err "缺少 Admin 配置校验程序；请先执行 build"
        return 1
    fi
    "$validator" -deploy-root "$DEPLOY_ROOT" -validate-config-bundle || {
        err "配置未通过程序的严格 YAML 与字段校验"
        return 1
    }
}

validate_configs() {
    local svc cfg jwt expected_jwt="" internal expected_internal="" url port
    local database_dsn decoded_key_bytes
    local redis_addr redis_password redis_db
    local expected_redis_addr="" expected_redis_password="" expected_redis_db=""
    local -A configured_ports=()

	resolve_active_services || return 1
    for svc in "${ACTIVE_SERVICES[@]}"; do
        cfg="$(service_config_path "$svc")" || {
            err "$svc: config.yaml 和已激活配置包均不存在"
            return 1
        }
        chmod 600 "$cfg" || return 1

        database_dsn="$(yaml_section_value "$cfg" database dsn)"
        [ -n "$database_dsn" ] || { err "$svc: database.dsn 未配置"; return 1; }
        if [[ "$database_dsn" == *'root:password@tcp(127.0.0.1:3306)/thing_connect'* ]]; then
            err "$svc: database.dsn 仍是示例账号密码"
            return 1
        fi

        port="$(yaml_section_value "$cfg" server http_port)"
        [[ "$port" =~ ^[1-9][0-9]*$ ]] && [ "$port" -le 65535 ] || {
            err "$svc: server.http_port 必须是 1-65535 的整数"
            return 1
        }
        if [ -n "${configured_ports[$port]:-}" ]; then
            err "$svc: server.http_port=$port 与 ${configured_ports[$port]} 冲突"
            return 1
        fi
        configured_ports[$port]="$svc"

        redis_addr="$(yaml_section_value "$cfg" redis addr)"
        redis_password="$(yaml_section_value "$cfg" redis password)"
        redis_db="$(yaml_section_value "$cfg" redis db)"
        [ -n "$redis_addr" ] || { err "$svc: redis.addr 未配置"; return 1; }
        [[ "$redis_db" =~ ^[0-9]+$ ]] || { err "$svc: redis.db 必须是非负整数"; return 1; }
        if [ -z "$expected_redis_addr" ]; then
            expected_redis_addr="$redis_addr"
            expected_redis_password="$redis_password"
            expected_redis_db="$redis_db"
        elif [ "$redis_addr" != "$expected_redis_addr" ] ||
             [ "$redis_password" != "$expected_redis_password" ] ||
             [ "$redis_db" != "$expected_redis_db" ]; then
            err "$svc: Redis 配置与其他服务不一致"
            return 1
        fi

    done

    for svc in "${ACTIVE_BUSINESS_SERVICES[@]}"; do
        cfg="$(service_config_path "$svc")" || return 1
        jwt="$(yaml_top_value "$cfg" jwt_secret)"
        [ -n "$jwt" ] || { err "$svc: jwt_secret 未配置"; return 1; }
        [ "${#jwt}" -ge 32 ] || { err "$svc: jwt_secret 至少需要 32 个字符"; return 1; }
        ! is_placeholder_secret "$jwt" || { err "$svc: jwt_secret 仍是公开占位值"; return 1; }
        [ -z "$expected_jwt" ] && expected_jwt="$jwt"
        [ "$jwt" = "$expected_jwt" ] || { err "$svc: jwt_secret 与其他服务不一致"; return 1; }
    done

    for svc in "${ACTIVE_SERVICES[@]}"; do
        cfg="$(service_config_path "$svc")" || return 1
        internal="$(yaml_section_value "$cfg" internal key)"
        [ -n "$internal" ] || { err "$svc: internal.key 未配置"; return 1; }
        [ "${#internal}" -ge 32 ] || { err "$svc: internal.key 至少需要 32 个字符"; return 1; }
        ! is_placeholder_secret "$internal" || { err "$svc: internal.key 仍是公开占位值"; return 1; }
        [ -z "$expected_internal" ] && expected_internal="$internal"
        [ "$internal" = "$expected_internal" ] || { err "$svc: internal.key 与其他服务不一致"; return 1; }
        if yaml_has_section_key "$cfg" call internal_key; then
            err "$svc: 请将旧配置 call.internal_key 迁移为 internal.key"
            return 1
        fi
    done

    cfg="$(service_config_path "admin-server")" || return 1
    local admin_jwt config_key cookie_secure
    admin_jwt="$(yaml_section_value "$cfg" admin jwt_secret)"
    config_key="$(yaml_section_value "$cfg" security config_encryption_key)"
    cookie_secure="$(yaml_section_value "$cfg" admin cookie_secure)"
    [ -n "$admin_jwt" ] || { err "admin-server: admin.jwt_secret 未配置"; return 1; }
    [ "${#admin_jwt}" -ge 32 ] || { err "admin-server: admin.jwt_secret 至少需要 32 个字符"; return 1; }
    ! is_placeholder_secret "$admin_jwt" || { err "admin-server: admin.jwt_secret 仍是公开占位值"; return 1; }
    [ -n "$config_key" ] || { err "admin-server: security.config_encryption_key 未配置"; return 1; }
    ! is_placeholder_secret "$config_key" || { err "admin-server: security.config_encryption_key 仍是公开占位值"; return 1; }
    command -v base64 >/dev/null 2>&1 || {
        err "找不到 base64；无法校验 Admin 配置加密密钥"
        return 1
    }
    if ! decoded_key_bytes="$(printf '%s' "$config_key" | base64 -d 2>/dev/null | wc -c | tr -d '[:space:]')"; then
        decoded_key_bytes=""
    fi
    [ "$decoded_key_bytes" = "32" ] || {
        err "admin-server: security.config_encryption_key 必须是 Base64 编码的 32 字节随机值"
        return 1
    }
    if [ "$cookie_secure" != "true" ]; then
        if [ "$ALLOW_INSECURE_ADMIN_COOKIE" = "1" ]; then
            warn "admin-server: 当前使用非 Secure Cookie；仅适用于 HTTP，启用 HTTPS 后必须设置 admin.cookie_secure: true"
        else
            err "admin-server: 生产环境必须设置 admin.cookie_secure: true"
            return 1
        fi
    fi

    for svc in "${ACTIVE_BUSINESS_SERVICES[@]}"; do
        cfg="$(service_config_path "$svc")" || return 1
        url="$(yaml_section_value "$cfg" admin server_url)"
        [ -n "$url" ] || { err "$svc: admin.server_url 未配置"; return 1; }
        is_http_url "$url" || { err "$svc: admin.server_url 不是有效的 HTTP(S) 地址"; return 1; }
    done

    cfg="$(service_config_path "user-server")" || return 1
    for svc in ai voip call; do
        url="$(yaml_section_value "$cfg" "$svc" server_url)"
        [ -n "$url" ] || { err "user-server: $svc.server_url 未配置"; return 1; }
        is_http_url "$url" || { err "user-server: $svc.server_url 不是有效的 HTTP(S) 地址"; return 1; }
    done
    if yaml_has_section_key "$cfg" call call_server_url; then
        err "user-server: 请将旧配置 call.call_server_url 迁移为 call.server_url"
        return 1
    fi
    strict_validate_config_syntax || return 1
    log "基础生产配置校验通过；Admin 动态配置和依赖连接由业务服务启动预检确认"
}

# ================== 拉代码 ==================
pull_code() {
    local worktree_status
    log "拉取代码..."
    [ -d "$REPO_PATH/.git" ] || {
        err "日常发布源码不存在: $REPO_PATH"
        err "请先按 deployment.md 运行 install.sh 完成首次安装"
        return 1
    }
    worktree_status="$(git -C "$REPO_PATH" status --porcelain --untracked-files=normal)" || return 1
    if [ -n "$worktree_status" ]; then
        err "源码目录存在未提交修改，拒绝覆盖: $REPO_PATH"
        return 1
    fi
    git -C "$REPO_PATH" pull --ff-only || return 1
    log "待发布版本: $(git -C "$REPO_PATH" rev-parse HEAD)"
}

# ================== 编译 ==================
build_services() {
    local worktree_status
    [ -f "$BUILD_DIR/go.mod" ] || { err "源码目录未就绪（缺少 $BUILD_DIR/go.mod）"; return 1; }
    worktree_status="$(git -C "$REPO_PATH" status --porcelain --untracked-files=normal)" || return 1
    if [ -n "$worktree_status" ]; then
        err "源码目录存在未提交修改，拒绝生成生产发布产物: $REPO_PATH"
        return 1
    fi
    "$BUILD_DIR/build.sh" "$@" || return 1
}

# 将通过语法检查的部署脚本原子发布到部署根目录。根目录脚本始终代表
# 最近一次成功发布使用的运维入口；源码拉取或构建失败时不会覆盖它。
publish_deploy_script() {
    local source="$BUILD_DIR/scripts/deploy-prod.sh"
    local target="$DEPLOY_ROOT/deploy-prod.sh"
    local pending="$DEPLOY_ROOT/.deploy-prod.sh.new"

    [ -f "$source" ] || {
        err "缺少部署脚本: $source"
        return 1
    }
    bash -n "$source" || {
        err "部署脚本语法检查失败: $source"
        return 1
    }
    rm -f -- "$pending" || return 1
    if ! cp -- "$source" "$pending"; then
        rm -f -- "$pending"
        return 1
    fi
    if ! chmod 0755 "$pending" || ! mv -f -- "$pending" "$target"; then
        rm -f -- "$pending"
        return 1
    fi
    log "部署脚本已发布: $target"
}

publish_local_service_script() {
    local source="$BUILD_DIR/scripts/service-local.sh"
    local target="$DEPLOY_ROOT/service-local.sh"
    local pending="$DEPLOY_ROOT/.service-local.sh.new"

    [ -f "$source" ] || {
        err "缺少本地服务脚本: $source"
        return 1
    }
    bash -n "$source" || {
        err "本地服务脚本语法检查失败: $source"
        return 1
    }
    rm -f -- "$pending" || return 1
    if ! cp -- "$source" "$pending"; then
        rm -f -- "$pending"
        return 1
    fi
    if ! chmod 0755 "$pending" || ! mv -f -- "$pending" "$target"; then
        rm -f -- "$pending"
        return 1
    fi
    log "本地服务脚本已发布: $target"
}

publish_service_catalog() {
    local loader_source="$BUILD_DIR/scripts/service-catalog.sh"
    local catalog_source="$BUILD_DIR/internal/installer/service_catalog.tsv"
    local loader_pending="$DEPLOY_ROOT/.service-catalog.sh.new"
    local catalog_pending="$DEPLOY_ROOT/.service-catalog.tsv.new"

    bash -n "$loader_source" || return 1
    cp -- "$loader_source" "$loader_pending" || return 1
    cp -- "$catalog_source" "$catalog_pending" || return 1
    chmod 0644 "$loader_pending" || return 1
    chmod 0644 "$catalog_pending" || return 1
    mv -f -- "$loader_pending" "$DEPLOY_ROOT/service-catalog.sh" || return 1
    mv -f -- "$catalog_pending" "$DEPLOY_ROOT/service-catalog.tsv" || return 1
    log "服务清单已发布: $DEPLOY_ROOT/service-catalog.tsv"
}

validate_build_release() {
    local expected_commit built_commit svc worktree_status
    [ -f "$BUILD_DIR/bin/.release-commit" ] || {
        err "缺少完整构建标记；请先执行 build，不能发布可能混合的旧产物"
        return 1
    }
    expected_commit="$(git -C "$REPO_PATH" rev-parse HEAD)" || return 1
    built_commit="$(tr -d '[:space:]' <"$BUILD_DIR/bin/.release-commit")"
    [ "$built_commit" = "$expected_commit" ] || {
        err "构建产物不属于当前源码提交；请重新执行 build"
        return 1
    }
    worktree_status="$(git -C "$REPO_PATH" status --porcelain --untracked-files=normal)" || return 1
    if [ -n "$worktree_status" ]; then
        err "构建后源码目录出现未提交差异，拒绝发布不可复现产物"
        return 1
    fi
    for svc in "$@"; do
        [ -x "$BUILD_DIR/bin/$svc" ] || {
            err "缺少可执行构建产物: $BUILD_DIR/bin/$svc"
            return 1
        }
    done
}

# ================== 数据库迁移 ==================
abort_migration_on_signal() {
	local status="$1" migration_output=""
	trap - INT TERM
	if [ -n "$MIGRATION_PID" ]; then
		kill -TERM "$MIGRATION_PID" 2>/dev/null || true
		wait "$MIGRATION_PID" 2>/dev/null || true
	fi
	MIGRATION_PID=""
	if [ -n "$MIGRATION_OUTPUT_FILE" ] && [ -f "$MIGRATION_OUTPUT_FILE" ]; then
		migration_output="$(<"$MIGRATION_OUTPUT_FILE")"
		[ -z "$migration_output" ] || printf '%s\n' "$migration_output" >&2
		if [[ "$migration_output" == *"migration_result=changed"* ]] ||
		   [[ "$migration_output" == *"migration_result=change_possible"* ]]; then
			MIGRATION_CHANGED=1
		fi
		rm -f "$MIGRATION_OUTPUT_FILE"
	fi
	MIGRATION_OUTPUT_FILE=""
	if [ "$MIGRATION_CHANGED" = "1" ]; then
		quiesce_after_schema_change || true
		if [ -n "${BACKUP_DIR:-}" ]; then
			mark_backup_failed || true
		fi
		err "迁移被中断且数据库 schema 可能已变化；整个服务组已停止"
	else
		err "数据库迁移被中断；未观察到 schema 变更标记"
	fi
	exit "$status"
}

run_migrations() {
	local migration_output migration_status=0 inspection_output inspection_status=0
	MIGRATION_CHANGED=0
    if [ "$SKIP_MIGRATIONS" = "1" ]; then
        warn "已按 SKIP_MIGRATIONS=1 跳过数据库迁移；仅应在迁移已独立完成时使用"
        return
    fi
    [ -f "$MIGRATION_CONFIG" ] || {
        err "缺少迁移配置: $MIGRATION_CONFIG"
        err "请复制 admin-server/config.yaml，使用具备 DDL 权限的 database.dsn，并设置权限 600"
        return 1
    }
    chmod 600 "$MIGRATION_CONFIG" || return 1
    [ -x "$BUILD_DIR/bin/admin-server" ] || {
        err "缺少迁移程序: $BUILD_DIR/bin/admin-server"
        return 1
    }
	log "只读检查数据库迁移状态..."
	if inspection_output="$("$BUILD_DIR/bin/admin-server" \
		-c "$MIGRATION_CONFIG" \
		-deploy-root "$DEPLOY_ROOT" \
		-require-runtime-target \
		-migration-check-only 2>&1)"; then
		inspection_status=0
	else
		inspection_status=$?
	fi
	if [ "$inspection_status" -eq 0 ]; then
		printf '%s\n' "$inspection_output"
	else
		printf '%s\n' "$inspection_output" >&2
		return "$inspection_status"
	fi
	if [[ "$inspection_output" == *"migration_result=unchanged"* ]]; then
		log "数据库已是当前版本；跳过数据库迁移和备份门槛"
		return 0
	fi
	if [[ "$inspection_output" != *"migration_result=pending"* ]]; then
		err "迁移检查程序没有返回可识别的结果，拒绝继续发布"
		return 1
	fi
    validate_database_backup || return 1
    warn "数据库迁移不能随二进制回滚；执行前应确认已有可恢复的数据库备份"
	log "校验迁移账号与所有已安装服务的运行配置指向同一数据库，并执行零写入所有权预检..."
    log "执行数据库迁移..."
	MIGRATION_OUTPUT_FILE="$(mktemp "${TMPDIR:-/tmp}/thingconnect-migration.XXXXXX")" || {
		err "无法创建迁移输出临时文件"
		return 1
	}
	chmod 600 "$MIGRATION_OUTPUT_FILE" || {
		rm -f "$MIGRATION_OUTPUT_FILE"
		MIGRATION_OUTPUT_FILE=""
		return 1
	}
	trap 'abort_migration_on_signal 130' INT
	trap 'abort_migration_on_signal 143' TERM
	"$BUILD_DIR/bin/admin-server" \
		-c "$MIGRATION_CONFIG" \
		-deploy-root "$DEPLOY_ROOT" \
		-require-runtime-target \
		-migrate-only >"$MIGRATION_OUTPUT_FILE" 2>&1 &
	MIGRATION_PID=$!
	if wait "$MIGRATION_PID"; then
		migration_status=0
	else
		migration_status=$?
	fi
	MIGRATION_PID=""
	trap - INT TERM
	migration_output="$(<"$MIGRATION_OUTPUT_FILE")"
	rm -f "$MIGRATION_OUTPUT_FILE"
	MIGRATION_OUTPUT_FILE=""
	if [ "$migration_status" -eq 0 ]; then
		printf '%s\n' "$migration_output"
	else
		printf '%s\n' "$migration_output" >&2
	fi
	if [[ "$migration_output" == *"migration_result=changed"* ]] ||
	   [[ "$migration_output" == *"migration_result=change_possible"* ]]; then
		MIGRATION_CHANGED=1
	elif [[ "$migration_output" != *"migration_result=unchanged"* ]]; then
		err "迁移程序没有返回可识别的结果，拒绝继续发布"
		return 1
	fi
	[ "$migration_status" -eq 0 ] || return "$migration_status"
    log "数据库迁移完成"
}

# ================== 首个管理员 ==================
restart_admin_after_init() {
    local state
    if ! command -v "$SUPERVISORCTL" >/dev/null 2>&1; then
        warn "未找到 $SUPERVISORCTL；首个管理员已创建，请在启动 admin-server 时加载最新权限"
        return
    fi
    state="$(supervisor_state "admin-server")"
    case "$state" in
        RUNNING|STARTING)
            log "刷新 admin-server 内存权限..."
            restart_one "admin-server" || return 1
            ;;
        STOPPED|EXITED|BACKOFF|FATAL)
            log "admin-server 当前为 $state；下次启动时会加载首个管理员权限"
            ;;
        *)
            warn "Supervisor 中未找到 $(supervisor_program "admin-server")；注册并启动后会加载首个管理员权限"
            ;;
    esac
}

initialize_first_admin() {
    local email="${ADMIN_INIT_EMAIL:-}"
    local nick_name="${ADMIN_INIT_NICK_NAME:-}"
    local password="${ADMIN_INIT_PASSWORD:-}"
    local confirmation=""
    local admin_bin="$DEPLOY_ROOT/admin-server/admin-server"
    local admin_cfg

    validate_paths || return 1
    validate_release_options || return 1
    validate_configs || return 1
    admin_cfg="$(service_config_path "admin-server")" || {
        err "缺少 Admin 配置"
        return 1
    }
    [ -x "$admin_bin" ] || {
        err "缺少初始化程序: $admin_bin；请先执行编译并发布文件"
        return 1
    }
    [ -f "$admin_cfg" ] || {
        err "缺少 Admin 配置: $admin_cfg"
        return 1
    }

    if [ -z "$email" ]; then
        [ -t 0 ] || {
            err "非交互模式必须设置 ADMIN_INIT_EMAIL 和 ADMIN_INIT_PASSWORD"
            return 1
        }
        read -r -p "首个管理员邮箱: " email
    fi
    [[ "$email" == *@* ]] || {
        err "首个管理员邮箱格式无效"
        return 1
    }
    if [ -z "$nick_name" ]; then
        nick_name="${email%%@*}"
    fi
    if [ -z "$password" ]; then
        [ -t 0 ] || {
            err "非交互模式必须设置 ADMIN_INIT_PASSWORD"
            return 1
        }
        read -r -s -p "首个管理员密码（至少 8 位，包含英文大小写字母和数字）: " password
        echo
        read -r -s -p "再次输入密码: " confirmation
        echo
        [ "$password" = "$confirmation" ] || {
            err "两次输入的管理员密码不一致"
            return 1
        }
    fi
    if [ "${#password}" -lt 8 ] ||
        [[ "$password" != *[A-Z]* ]] ||
        [[ "$password" != *[a-z]* ]] ||
        [[ "$password" != *[0-9]* ]]; then
        err "管理员密码至少 8 位，且必须包含英文大写字母、英文小写字母和数字"
        return 1
    fi

    migrate_action || return 1
    log "初始化首个管理员: $email"
    if ! ADMIN_INIT_PASSWORD="$password" "$admin_bin" \
        -c "$admin_cfg" \
        -init-admin \
        -init-email "$email" \
        -init-nick-name "$nick_name"; then
        password=""
        confirmation=""
        err "首个管理员初始化失败；数据库已有管理员时不会重复创建"
        return 1
    fi
    password=""
    confirmation=""
    restart_admin_after_init || return 1
    log "首个管理员初始化完成；常驻 Admin 权限已刷新"
}

# ================== 健康检查 ==================
service_health_url() {
    local svc="$1" port cfg
    cfg="$(service_config_path "$svc")" || {
        err "$svc: config.yaml 和已激活配置包均不存在"
        return 1
    }
    port="$(yaml_section_value "$cfg" server http_port)"
    [[ "$port" =~ ^[1-9][0-9]*$ ]] || {
        err "$svc: server.http_port 无效"
        return 1
    }
    printf 'http://%s:%s/health/ready\n' "$HEALTH_HOST" "$port"
}

wait_for_readiness() {
    local svc="$1" url elapsed=0 state
    url="$(service_health_url "$svc")" || return 1
    while [ "$elapsed" -lt "$HEALTH_WAIT_SECONDS" ]; do
        if curl -fsS --max-time "$HEALTH_REQUEST_TIMEOUT_SECONDS" "$url" >/dev/null 2>&1; then
            log "$svc readiness 检查通过"
            return
        fi
        state="$(supervisor_state "$svc")"
        if [ "$state" != "RUNNING" ] && [ "$state" != "STARTING" ]; then
            err "$svc readiness 检查期间 Supervisor 状态变为 ${state:-UNKNOWN}"
            return 1
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    err "$svc 在 ${HEALTH_WAIT_SECONDS}s 内未通过 $url"
    curl -sS --max-time "$HEALTH_REQUEST_TIMEOUT_SECONDS" "$url" || true
    return 1
}

# ================== 停服务 ==================
stop_one() {
    local svc="$1"

    log "停止 $svc ..."

    local state
    require_supervisor_service "$svc" || return 1
    state="$(supervisor_state "$svc")"
    case "$state" in
        STOPPED|EXITED|FATAL|BACKOFF)
            log "$svc 当前未运行（Supervisor: $state）"
            return
            ;;
    esac
    "$SUPERVISORCTL" stop "$(supervisor_program "$svc")" || true
    wait_for_supervisor_state "$svc" "STOPPED" || return 1
    log "$svc 已停止（Supervisor）"
}

# ================== 发布 ==================
deploy_one() {
    local svc="$1"

    local target_dir="$DEPLOY_ROOT/$svc"
    local src_bin="$BUILD_DIR/bin/$svc"
    local static_dir="${SERVICE_STATIC_DIR[$svc]}"
    local static_source=""

    # Linux 的 rename 可安全替换正在运行的可执行文件；已运行进程继续使用
    # 旧 inode。Supervisor 模式随后自动重启，手动模式要求发布前先停止服务。

    if [ ! -f "$src_bin" ]; then
        err "找不到 $src_bin"
        return 1
    fi

    mkdir -p "$target_dir" || return 1

    cp "$src_bin" "$target_dir/$svc.tmp" || return 1
    mv -f "$target_dir/$svc.tmp" "$target_dir/$svc" || return 1

    chmod +x "$target_dir/$svc" || return 1

    # 静态目录先完整复制到临时目录，再原子切换。是否包含静态资源以及
    # 源目录位置由共享服务清单决定。
    if [ "$static_dir" != "-" ]; then
        static_source="$BUILD_DIR/$static_dir"
        [ -d "$static_source" ] || {
            err "$svc: 静态资源不存在: $static_source"
            return 1
        }
        rm -rf "$target_dir/static.new" || return 1
        cp -a "$static_source" "$target_dir/static.new" || return 1
        rm -rf "$target_dir/static.old" || return 1
        [ ! -d "$target_dir/static" ] || mv "$target_dir/static" "$target_dir/static.old" || return 1
        mv "$target_dir/static.new" "$target_dir/static" || return 1
        rm -rf "$target_dir/static.old" || return 1
        log "$svc: static/ 已同步"
    fi

    log "发布完成 $svc"
}

# ================== 启动 ==================
start_one() {
    local svc="$1"

    local bin="$DEPLOY_ROOT/$svc/$svc"
    local cfg

    if [ ! -f "$bin" ]; then
        err "$svc 未发布"
        return 1
    fi

    if ! cfg="$(service_config_path "$svc")"; then
        err "$svc: config.yaml 和已激活配置包均不存在"
        return 1
    fi

    local state
    require_supervisor_service "$svc" || return 1
    state="$(supervisor_state "$svc")"
    if [ "$state" = "RUNNING" ]; then
        wait_for_readiness "$svc" || return 1
        log "$svc 已在运行且就绪（Supervisor）"
        return
    fi
    log "启动 $svc（Supervisor）..."
    "$SUPERVISORCTL" start "$(supervisor_program "$svc")" || return 1
    wait_for_supervisor_state "$svc" "RUNNING" || return 1
    wait_for_readiness "$svc" || return 1
    log "$svc 启动成功（Supervisor）"
}

# ================== 重启 ==================
restart_one() {
    local svc="$1"

    require_supervisor_service "$svc" || return 1
    log "重启 $svc（Supervisor）..."
    "$SUPERVISORCTL" restart "$(supervisor_program "$svc")" || return 1
    wait_for_supervisor_state "$svc" "RUNNING" || return 1
    wait_for_readiness "$svc" || return 1
    log "$svc 重启成功（Supervisor）"
}

# ================== 状态 ==================
status_one() {
    local svc="$1"
    require_supervisor_service "$svc" || return 1
    "$SUPERVISORCTL" status "$(supervisor_program "$svc")" || return 1
}

# ================== 批量 ==================
run_batch() {
    local action=$1
    shift

    validate_services "$@" || return 1
    if [ "$action" = "build" ]; then
        build_services "$@" || return 1
        return
    fi
    if [ "$action" = "deploy" ]; then
        validate_build_release "$@" || return 1
    fi
    case "$action" in
        start|stop|restart|status)
            validate_service_manager || return 1
            if [ "$ACTIVE_SERVICE_MANAGER" = "manual" ]; then
                manual_service_control_hint "$action"
                return 1
            fi
            ;;
    esac
    for svc in "$@"; do
        case "$action" in
            deploy) deploy_one "$svc" || return 1 ;;
            start) start_one "$svc" || return 1 ;;
            stop) stop_one "$svc" || return 1 ;;
            restart) restart_one "$svc" || return 1 ;;
            status) status_one "$svc" || return 1 ;;
        esac
    done
}

# ================== 备份与回滚 ==================
BACKUP_DIR=""
DEPLOYED_SERVICES=()

backup_release() {
    local release_id svc target backup commit_id
    commit_id="$(git -C "$REPO_PATH" rev-parse --short HEAD)" || return 1
    release_id="$(date +%Y%m%d-%H%M%S)-${commit_id}-$$"
    BACKUP_DIR="$DEPLOY_ROOT/releases/$release_id"
    mkdir -p "$BACKUP_DIR" || return 1
    : > "$BACKUP_DIR/.deploying" || return 1
    for svc in "$@"; do
        target="$DEPLOY_ROOT/$svc"
        backup="$BACKUP_DIR/$svc"
        mkdir -p "$backup" || return 1
        [ ! -f "$target/$svc" ] || cp -a "$target/$svc" "$backup/$svc" || return 1
        [ ! -d "$target/static" ] || cp -a "$target/static" "$backup/static" || return 1
    done
    log "当前版本已备份到 $BACKUP_DIR"
}

mark_backup_successful() {
    rm -f "$BACKUP_DIR/.deploying" || return 1
    : > "$BACKUP_DIR/.successful" || return 1
}

mark_backup_failed() {
    rm -f "$BACKUP_DIR/.deploying" || return 1
    : > "$BACKUP_DIR/.failed" || return 1
}

prune_successful_backups() {
    local releases_dir="$DEPLOY_ROOT/releases" backup i
    local -a backups

    mapfile -t backups < <(
        find "$releases_dir" -mindepth 1 -maxdepth 1 -type d -name '????????-??????-*' -print 2>/dev/null \
            | while IFS= read -r backup; do
                [ -f "$backup/.successful" ] && printf '%s\n' "$backup"
            done \
            | sort -r
    )

    if [ "${#backups[@]}" -le "$BACKUP_KEEP_COUNT" ]; then
        return
    fi

    for ((i = BACKUP_KEEP_COUNT; i < ${#backups[@]}; i++)); do
        backup="${backups[$i]}"
        rm -rf -- "$backup"
        log "已清理过期成功发布备份: $backup"
    done
}

rollback_release() {
    local svc backup target rollback_failed=0
    err "发布失败，开始回滚"
    for svc in "${DEPLOYED_SERVICES[@]}"; do
        backup="$BACKUP_DIR/$svc"
        target="$DEPLOY_ROOT/$svc"
        if [ "$ACTIVE_SERVICE_MANAGER" = "supervisor" ]; then
            stop_one "$svc" || rollback_failed=1
        fi
        if [ -f "$backup/$svc" ]; then
            if ! cp -a "$backup/$svc" "$target/$svc.rollback" ||
               ! mv -f "$target/$svc.rollback" "$target/$svc" ||
               ! chmod +x "$target/$svc"; then
                err "$svc 二进制回滚失败"
                rollback_failed=1
                continue
            fi
        else
            # 首次发布时没有可恢复的旧版本，不能保留失败的新二进制。
            rm -f "$target/$svc" || rollback_failed=1
        fi
        if [ -d "$backup/static" ]; then
            if ! rm -rf "$target/static.rollback" ||
               ! cp -a "$backup/static" "$target/static.rollback" ||
               ! rm -rf "$target/static" ||
               ! mv "$target/static.rollback" "$target/static"; then
                err "$svc 静态资源回滚失败"
                rollback_failed=1
            fi
        elif [ -d "$target/static" ]; then
            rm -rf "$target/static" || rollback_failed=1
        fi
        if [ -f "$backup/$svc" ] && [ "$ACTIVE_SERVICE_MANAGER" = "supervisor" ]; then
            if ! start_one "$svc"; then
                err "$svc 回滚后启动失败，请人工处理"
                rollback_failed=1
            fi
        elif [ ! -f "$backup/$svc" ]; then
            warn "$svc 没有可回滚版本，已保持停止状态"
        fi
    done
    if [ "$ACTIVE_SERVICE_MANAGER" = "manual" ]; then
        warn "文件已回滚，服务保持停止；修复发布问题后请使用 $LOCAL_SERVICECTL 手动启动"
    fi
    mark_backup_failed || rollback_failed=1
    err "回滚完成；失败版本备份目录: $BACKUP_DIR"
    [ "$rollback_failed" -eq 0 ]
}

quiesce_after_schema_change() {
	local svc failed=0
	err "数据库 schema 已变化；停止整个服务组，避免新旧二进制混合运行"
	if [ "$ACTIVE_SERVICE_MANAGER" = "manual" ]; then
		validate_manual_services_stopped || return 1
		return 0
	fi
	for svc in "${STOP_ORDER[@]}"; do
		stop_one "$svc" || failed=1
	done
	[ "$failed" -eq 0 ] || err "部分服务未能自动停止，请立即检查 Supervisor"
	return "$failed"
}

rollback_active_release() {
    local status="$1"
    trap - ERR INT TERM
	if [ "$MIGRATION_CHANGED" = "1" ]; then
		quiesce_after_schema_change || true
		mark_backup_failed || true
		err "数据库 schema 已变化，拒绝自动恢复旧二进制；已保留发布备份供核对"
		err "请先恢复数据库备份，或确认旧版本兼容新 schema 后再人工回滚文件"
		return "$status"
	fi
    if ! rollback_release; then
        err "自动回滚未完整执行，请立即人工检查"
    fi
    return "$status"
}

abort_active_release() {
    local status="$1"
    trap - ERR INT TERM
	if [ "$MIGRATION_CHANGED" = "1" ]; then
		quiesce_after_schema_change || true
		mark_backup_failed || true
		err "收到中断且数据库 schema 已变化，未自动启动旧二进制"
	else
		rollback_release || true
	fi
    exit "$status"
}

full_deploy() (
    local services=() svc
    local status
    validate_deploy_root || return 1
    prepare_update_service_control || return 1
    validate_backup_retention || return 1
    validate_release_options || return 1
    pull_code || return 1
    validate_paths || return 1
	load_deployment_service_catalog || return 1
	prepare_update_service_control || return 1
	services=("${ACTIVE_SERVICES[@]}")
    run_batch build "${ALL_SERVICES[@]}" || return 1
    validate_build_release "${ALL_SERVICES[@]}" || return 1
    validate_configs || return 1
    backup_release "${services[@]}" || return 1
	if run_migrations; then
		:
	else
		status=$?
		mark_backup_failed || true
		if [ "$MIGRATION_CHANGED" = "1" ]; then
			quiesce_after_schema_change || true
			err "迁移失败且数据库可能已部分变化，整个服务组已停止"
		fi
		return "$status"
	fi
    DEPLOYED_SERVICES=()
    trap 'status=$?; rollback_active_release "$status"; exit "$status"' ERR
    trap 'abort_active_release 130' INT
    trap 'abort_active_release 143' TERM

    for svc in "${services[@]}"; do
        # Include the current service before touching it so a mid-deploy error
        # also restores and restarts that service.
        DEPLOYED_SERVICES+=("$svc")
        if deploy_one "$svc"; then
            :
        else
            status=$?
            rollback_active_release "$status"
            return "$status"
        fi
        if [ "$ACTIVE_SERVICE_MANAGER" = "supervisor" ]; then
            if restart_one "$svc"; then
                :
            else
                status=$?
                rollback_active_release "$status"
                return "$status"
            fi
        fi
    done
    if mark_backup_successful; then
        :
    else
        status=$?
        rollback_active_release "$status"
        return "$status"
    fi
    trap - ERR INT TERM
    publish_service_catalog || warn "服务已发布成功，但服务清单刷新失败"
    publish_local_service_script || warn "服务已发布成功，但本地服务脚本刷新失败；请检查 $BUILD_DIR/scripts/service-local.sh"
    publish_deploy_script || warn "服务已发布成功，但根目录部署脚本刷新失败；请继续使用 $BUILD_DIR/scripts/deploy-prod.sh"
    prune_successful_backups || warn "清理过期备份失败，请稍后人工清理"
    if [ "$ACTIVE_SERVICE_MANAGER" = "supervisor" ]; then
        log "全流程发布成功: $(git -C "$REPO_PATH" rev-parse --short HEAD)"
    else
        log "代码、配置校验和数据库迁移已完成: $(git -C "$REPO_PATH" rev-parse --short HEAD)"
        warn "当前为手动服务管理模式，服务仍保持停止"
        log "下一步执行: sudo $LOCAL_SERVICECTL start-all"
        log "启动后检查: sudo $LOCAL_SERVICECTL status-all"
    fi
    log "回滚备份保留在: $BACKUP_DIR"
    log "手工配置部署如尚无管理员，可执行 init-admin；Web 首次安装会在安装流程中创建"
)

# ================== 操作入口 ==================
select_all() {
    echo "${ALL_SERVICES[@]}"
}

pull_action() {
    validate_deploy_root || return 1
    pull_code
}

build_action() {
    validate_paths || return 1
    run_batch build "${ALL_SERVICES[@]}"
}

deploy_files_action() {
    validate_paths || return 1
    validate_configs || return 1
    run_batch deploy "${ACTIVE_SERVICES[@]}" || return 1
    publish_service_catalog || return 1
    publish_local_service_script || return 1
    publish_deploy_script
}

start_action() {
    validate_configs || return 1
    run_batch start "${ACTIVE_SERVICES[@]}"
}

stop_action() {
    # 业务服务先停，Admin 最后停，尽量保留管理与诊断入口。
	resolve_active_services || return 1
    run_batch stop "${ACTIVE_STOP_ORDER[@]}"
}

restart_action() {
    validate_configs || return 1
    run_batch restart "${ACTIVE_SERVICES[@]}"
}

migrate_action() {
	local status
    validate_paths || return 1
    validate_release_options || return 1
	if run_migrations; then
		return 0
	else
		status=$?
	fi
	if [ "$MIGRATION_CHANGED" = "1" ]; then
		quiesce_after_schema_change || true
		err "迁移失败且数据库可能已部分变化，整个服务组已停止"
	fi
	return "$status"
}

status_action() {
    local svc failed=0
    resolve_active_services || return 1
    validate_service_manager || return 1
    if [ "$ACTIVE_SERVICE_MANAGER" = "manual" ]; then
        manual_service_control_hint status
        return 1
    fi
    for svc in "${ALL_SERVICES[@]}"; do
        status_one "$svc" || failed=1
    done
    return "$failed"
}

usage() {
    cat <<'USAGE'
用法: deploy-prod.sh [命令]

命令：
  update         日常更新：拉取、构建、备份、迁移和发布；Supervisor 模式自动重启验收
  migrate        仅执行版本化数据库迁移
  validate       校验必需服务及已启用可选服务的基础生产配置
  status         查看全部服务状态
  start          启动并检查已安装服务
  stop           停止已安装服务
  restart        重启并检查已安装服务

高级命令：
  init-admin     仅手工配置部署时初始化首个管理员
  pull           仅快进拉取代码
  build          仅编译全部服务
  deploy         仅发布已编译文件，不迁移、不重启
  help           显示此帮助

不带命令时进入交互菜单。数据库迁移账号读取 MIGRATION_CONFIG；由外部迁移
平台处理 DDL 时可显式设置 SKIP_MIGRATIONS=1 后执行 update。Supervisor 可选；
未配置时先用 service-local.sh stop-all，更新完成后手动 start-all 和 status-all。
USAGE
}

run_named_command() {
    local command_name="${1:-}"
    if [ "$#" -ne 1 ]; then
        err "每次只能执行一个命令"
        usage
        return 2
    fi
    case "$command_name" in
        update) with_deploy_lock full_deploy ;;
        migrate) with_deploy_lock migrate_action ;;
        init-admin) with_deploy_lock initialize_first_admin ;;
        validate) validate_configs ;;
        pull) with_deploy_lock pull_action ;;
        build) with_deploy_lock build_action ;;
        deploy) with_deploy_lock deploy_files_action ;;
        start) with_deploy_lock start_action ;;
        stop) with_deploy_lock stop_action ;;
        restart) with_deploy_lock restart_action ;;
        status) status_action ;;
        help|-h|--help) usage ;;
        *)
            err "未知命令: ${command_name:-<空>}"
            usage
            return 2
            ;;
    esac
}

# ================== 菜单 ==================
menu() {
    local choice command_name
    echo ""
    echo "1) 日常更新部署（Supervisor 自动重启；无 Supervisor 时手动启停）"
    echo "2) 仅执行数据库迁移"
    echo "3) 校验基础生产配置"
    echo "4) 查看全部服务状态"
    echo "5) 重启并检查已安装服务"
    echo "6) 启动并检查已安装服务"
    echo "7) 停止已安装服务"
    echo "0) 退出"

    if ! read -r -p "选择: " choice; then
        echo
        exit 0
    fi
    case "$choice" in
        1) command_name="update" ;;
        2) command_name="migrate" ;;
        3) command_name="validate" ;;
        4) command_name="status" ;;
        5) command_name="restart" ;;
        6) command_name="start" ;;
        7) command_name="stop" ;;
        0) exit 0 ;;
        *) warn "无效选择: $choice"; return ;;
    esac
    if ! run_named_command "$command_name"; then
        err "操作失败: $command_name"
    fi
}

# ================== 主循环 ==================
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    if [ "$#" -gt 0 ]; then
        run_named_command "$@"
    else
        while true; do
            menu
        done
    fi
fi
