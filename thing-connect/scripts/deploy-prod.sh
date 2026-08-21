#!/usr/bin/env bash

set -Eeuo pipefail

# ================== 配置 ==================
# 常规部署只需按实际环境修改下面三行；同名环境变量可临时覆盖。
PRODUCTION_DEPLOY_ROOT="/opt/thing-connect"
DEFAULT_REPO_URL="git@github.com:tangeai/tirtc-server-example.git"
DEFAULT_SUPERVISOR_GROUP="thing-connect"

REPO_URL="${REPO_URL:-$DEFAULT_REPO_URL}"
SUPERVISOR_GROUP="${SUPERVISOR_GROUP:-$DEFAULT_SUPERVISOR_GROUP}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="tirtc-server-example"
WORK_DIR="thing-connect"

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
REPO_PATH="${REPO_PATH:-$DEPLOY_ROOT/$REPO_DIR}"
BUILD_DIR="${BUILD_DIR:-$REPO_PATH/$WORK_DIR}"

# 生产服务仅由 Supervisor 托管。
SUPERVISORCTL="${SUPERVISORCTL:-supervisorctl}"
# 对应 deploy/supervisor/thing-connect.supervisor.conf 的 [group:thing-connect]。
SUPERVISOR_WAIT_SECONDS="${SUPERVISOR_WAIT_SECONDS:-15}"
SUPERVISOR_STABLE_SECONDS="${SUPERVISOR_STABLE_SECONDS:-2}"
HEALTH_WAIT_SECONDS="${HEALTH_WAIT_SECONDS:-30}"
HEALTH_REQUEST_TIMEOUT_SECONDS="${HEALTH_REQUEST_TIMEOUT_SECONDS:-3}"
HEALTH_HOST="${HEALTH_HOST:-127.0.0.1}"
SETUP_PORT="${SETUP_PORT:-9000}"
BACKUP_KEEP_COUNT="${BACKUP_KEEP_COUNT:-10}"
MIGRATION_CONFIG="${MIGRATION_CONFIG:-$DEPLOY_ROOT/admin-server/migration-config.yaml}"
SKIP_MIGRATIONS="${SKIP_MIGRATIONS:-0}"
ALLOW_INSECURE_ADMIN_COOKIE="${ALLOW_INSECURE_ADMIN_COOKIE:-0}"
MIGRATION_CHANGED=0
MIGRATION_OUTPUT_FILE=""
MIGRATION_PID=""

mkdir -p "$DEPLOY_ROOT/logs"

BUSINESS_SERVICES=("device-server" "user-server" "voip-server" "ai-server" "call-server")
REQUIRED_SERVICES=("admin-server" "device-server" "user-server")
# Business services must load their initial registry values from Admin before
# listening, so a full deployment starts/restarts Admin first.
ALL_SERVICES=("admin-server" "${BUSINESS_SERVICES[@]}")
STOP_ORDER=("${BUSINESS_SERVICES[@]}" "admin-server")
ACTIVE_SERVICES=()
ACTIVE_BUSINESS_SERVICES=()
ACTIVE_STOP_ORDER=()

# ================== 工具函数 ==================
log() { echo -e "[INFO] $1"; }
warn() { echo -e "[WARN] $1"; }
err() { echo -e "[ERROR] $1"; }

# 锁只覆盖一次会修改代码、文件、数据库或进程状态的操作。交互菜单本身
# 不持锁，否则首次安装页面会永远等不到安装器所需的同一把部署锁。
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

# A service is installed when it has an active config. Admin, device and user
# are mandatory; VoIP, AI and call are included only when the installer (or a
# manual operator) provided their configs.
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
    ACTIVE_SERVICES=("admin-server")
    for svc in "${BUSINESS_SERVICES[@]}"; do
        if service_config_path "$svc" >/dev/null; then
            ACTIVE_SERVICES+=("$svc")
            ACTIVE_BUSINESS_SERVICES+=("$svc")
        fi
    done
    ACTIVE_STOP_ORDER=("${ACTIVE_BUSINESS_SERVICES[@]}" "admin-server")
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
        err "找不到 $SUPERVISORCTL；生产环境必须安装并配置 Supervisor"
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
    command -v "$SUPERVISORCTL" >/dev/null 2>&1 || {
        err "找不到 $SUPERVISORCTL；生产环境必须安装并配置 Supervisor"
        return 1
    }
    for svc in "${ALL_SERVICES[@]}"; do
        require_supervisor_service "$svc" || return 1
    done
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
    [[ "$SETUP_PORT" =~ ^[1-9][0-9]*$ ]] && [ "$SETUP_PORT" -le 65535 ] || {
        err "SETUP_PORT 必须是 1-65535 的整数"
        return 1
    }
    command -v curl >/dev/null 2>&1 || {
        err "找不到 curl；生产发布必须执行 readiness 检查"
        return 1
    }
    validate_supervisor_inventory || return 1
    log "服务管理器校验通过：Supervisor"
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

is_mqtt_url() {
    [[ "$1" =~ ^(mqtt|mqtts|tcp|ssl)://[^[:space:]@]+$ ]]
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
    local mqtt_broker mqtt_username mqtt_client_id mqtt_password expected_mqtt_broker=""
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

        if yaml_has_section_key "$cfg" mqtt broker; then
            mqtt_broker="$(yaml_section_value "$cfg" mqtt broker)"
            mqtt_username="$(yaml_section_value "$cfg" mqtt username)"
            mqtt_client_id="$(yaml_section_value "$cfg" mqtt client_id)"
            mqtt_password="$(yaml_section_value "$cfg" mqtt password)"
            is_mqtt_url "$mqtt_broker" || {
                err "$svc: mqtt.broker 必须是有效的 mqtt/mqtts/tcp/ssl 地址且不能在 URL 中携带账号"
                return 1
            }
            [ -n "$mqtt_username" ] || [ -n "$mqtt_client_id" ] || {
                err "$svc: mqtt.username 与 mqtt.client_id 至少配置一个"
                return 1
            }
            ! is_placeholder_secret "$mqtt_password" || {
                err "$svc: mqtt.password 仍是公开占位值"
                return 1
            }
            [ -z "$expected_mqtt_broker" ] && expected_mqtt_broker="$mqtt_broker"
            [ "$mqtt_broker" = "$expected_mqtt_broker" ] || {
                err "$svc: mqtt.broker 与其他 MQTT 服务不一致"
                return 1
            }
        elif [ "$svc" != "admin-server" ] && [ "$svc" != "ai-server" ]; then
            err "$svc: mqtt.broker 未配置"
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
            warn "admin-server: admin.cookie_secure 未启用，仅允许用于明确的非 HTTPS 环境"
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
    log "配置校验通过"
}

# ================== 拉代码 ==================
pull_code() {
    local worktree_status
    log "拉取代码..."
    if [ -d "$REPO_PATH/.git" ]; then
        worktree_status="$(git -C "$REPO_PATH" status --porcelain --untracked-files=normal)" || return 1
        if [ -n "$worktree_status" ]; then
            err "源码目录存在未提交修改，拒绝覆盖: $REPO_PATH"
            return 1
        fi
        git -C "$REPO_PATH" pull --ff-only || return 1
    else
        git clone "$REPO_URL" "$REPO_PATH" || return 1
    fi
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
	local migration_output migration_status=0
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
        read -r -s -p "首个管理员密码（至少 12 个字符）: " password
        echo
        read -r -s -p "再次输入密码: " confirmation
        echo
        [ "$password" = "$confirmation" ] || {
            err "两次输入的管理员密码不一致"
            return 1
        }
    fi
    [ "${#password}" -ge 12 ] || {
        err "管理员密码至少需要 12 个字符"
        return 1
    }

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

wait_for_setup_liveness() {
    local url="http://${HEALTH_HOST}:${SETUP_PORT}/health/live" elapsed=0 body state
    while [ "$elapsed" -lt "$HEALTH_WAIT_SECONDS" ]; do
        body="$(curl -fsS --max-time "$HEALTH_REQUEST_TIMEOUT_SECONDS" "$url" 2>/dev/null || true)"
        if [[ "$body" == *'"mode":"setup"'* ]]; then
            log "首次安装服务 liveness 检查通过"
            return
        fi
        state="$(supervisor_state "admin-server")"
        if [ "$state" != "RUNNING" ] && [ "$state" != "STARTING" ]; then
            err "首次安装服务检查期间 Supervisor 状态变为 ${state:-UNKNOWN}"
            return 1
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    err "首次安装服务在 ${HEALTH_WAIT_SECONDS}s 内未通过 $url"
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

    # Linux 的 rename 可安全替换正在运行的可执行文件；运行中的 Supervisor
    # 子进程继续使用旧 inode，直到后续 restart_one 切换到新版本。

    if [ ! -f "$src_bin" ]; then
        err "找不到 $src_bin"
        return 1
    fi

    mkdir -p "$target_dir" || return 1

    cp "$src_bin" "$target_dir/$svc.tmp" || return 1
    mv -f "$target_dir/$svc.tmp" "$target_dir/$svc" || return 1

    chmod +x "$target_dir/$svc" || return 1

    # 配置
    local config_example="$BUILD_DIR/$svc/config.yaml.example"
    [ "$svc" != "admin-server" ] || config_example="$BUILD_DIR/admin/admin-server/config.yaml.example"
    if [ "${FIRST_RUN_DEPLOY:-0}" != "1" ] &&
       ! service_config_path "$svc" >/dev/null 2>&1 &&
       [ -f "$config_example" ]; then
        cp "$config_example" "$target_dir/config.yaml" || return 1
        chmod 600 "$target_dir/config.yaml" || return 1
    fi

    # 静态目录先完整复制到临时目录，再原子切换。
    case "$svc" in user-server|ai-server|admin-server)
        local static_source="$BUILD_DIR/$svc/static"
        [ "$svc" != "admin-server" ] || static_source="$BUILD_DIR/admin/admin-web/dist"
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
        ;;
    esac

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
        start|restart) validate_service_manager || return 1 ;;
        stop|status) validate_supervisor_inventory || return 1 ;;
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
        stop_one "$svc" || rollback_failed=1
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
        if [ -f "$backup/$svc" ]; then
            if ! start_one "$svc"; then
                err "$svc 回滚后启动失败，请人工处理"
                rollback_failed=1
            fi
        else
            warn "$svc 没有可回滚版本，已保持停止状态"
        fi
    done
    mark_backup_failed || rollback_failed=1
    err "回滚完成；失败版本备份目录: $BACKUP_DIR"
    [ "$rollback_failed" -eq 0 ]
}

quiesce_after_schema_change() {
	local svc failed=0
	err "数据库 schema 已变化；停止整个服务组，避免新旧二进制混合运行"
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
    validate_service_manager || return 1
    validate_backup_retention || return 1
    validate_release_options || return 1
    pull_code || return 1
    validate_paths || return 1
	resolve_active_services || return 1
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
        if restart_one "$svc"; then
            :
        else
            status=$?
            rollback_active_release "$status"
            return "$status"
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
    publish_deploy_script || warn "服务已发布成功，但根目录部署脚本刷新失败；请继续使用 $BUILD_DIR/scripts/deploy-prod.sh"
    prune_successful_backups || warn "清理过期备份失败，请稍后人工清理"
    log "全流程发布成功: $(git -C "$REPO_PATH" rev-parse --short HEAD)"
    log "回滚备份保留在: $BACKUP_DIR"
    log "手工配置部署如尚无管理员，可执行 init-admin；Web 首次安装会在安装流程中创建"
)

prepare_first_run() (
    local svc state setup_output
    validate_deploy_root || return 1
    validate_service_manager || return 1
    pull_code || return 1
    validate_paths || return 1

    if [ -e "$DEPLOY_ROOT/config-current" ] || [ -L "$DEPLOY_ROOT/config-current" ] ||
       [ -e "$DEPLOY_ROOT/var/installer/installed.json" ]; then
        err "检测到已安装配置，拒绝重新开放首次安装入口"
        return 1
    fi
    for svc in "${ALL_SERVICES[@]}"; do
        if [ -e "$DEPLOY_ROOT/$svc/config.yaml" ]; then
            err "$svc 已存在 config.yaml；请使用正常发布或离线恢复流程"
            return 1
        fi
    done

    run_batch build "${ALL_SERVICES[@]}" || return 1
    validate_build_release "${ALL_SERVICES[@]}" || return 1
    FIRST_RUN_DEPLOY=1 run_batch deploy "${ALL_SERVICES[@]}" || return 1
    publish_deploy_script || warn "根目录部署脚本发布失败；可继续使用 $BUILD_DIR/scripts/deploy-prod.sh"

    for svc in "${BUSINESS_SERVICES[@]}"; do
        stop_one "$svc" || return 1
    done

    setup_output="$("$DEPLOY_ROOT/admin-server/admin-server" \
        -c "$DEPLOY_ROOT/admin-server/config.yaml" \
        -deploy-root "$DEPLOY_ROOT" \
        -setup-port "$SETUP_PORT" \
        -supervisorctl "$SUPERVISORCTL" \
        -supervisor-group "$SUPERVISOR_GROUP" \
        -prepare-setup)" || return 1

    # 即使后续 Supervisor 启动失败，也先把一次性令牌交给当前终端；再次
    # 执行 first-install 会在仍未提交配置时安全轮换令牌。
    printf '%s\n' "$setup_output"
    warn "安装令牌只显示这一次；不要发送到聊天、工单或监控系统"

    state="$(supervisor_state "admin-server")"
    if [ "$state" = "RUNNING" ]; then
        "$SUPERVISORCTL" restart "$(supervisor_program "admin-server")" || return 1
    else
        "$SUPERVISORCTL" start "$(supervisor_program "admin-server")" || return 1
    fi
    wait_for_supervisor_state "admin-server" "RUNNING" || return 1
    wait_for_setup_liveness || return 1

    log "首次安装页面已启动：http://${HEALTH_HOST}:${SETUP_PORT}/admin/"
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
    command -v "$SUPERVISORCTL" >/dev/null 2>&1 || {
        err "找不到 $SUPERVISORCTL"
        return 1
    }
    for svc in "${ALL_SERVICES[@]}"; do
        status_one "$svc" || failed=1
    done
    return "$failed"
}

usage() {
    cat <<'USAGE'
用法: deploy-prod.sh [命令]

命令：
  first-install  首次部署：构建、发布并打开一次性 Web 安装页（仅空部署）
  update         日常更新：拉取、构建、备份、迁移、逐服务发布与就绪检查
  migrate        仅执行版本化数据库迁移
  init-admin     手工配置部署时初始化首个管理员
  validate       校验必需服务及已启用可选服务的生产配置
  pull           仅快进拉取代码
  build          仅编译全部服务
  deploy         仅发布已编译文件，不迁移、不重启
  start          启动并检查已安装服务
  stop           停止已安装服务
  restart        重启并检查已安装服务
  status         查看全部服务状态
  help           显示此帮助

不带命令时进入交互菜单。数据库迁移账号读取 MIGRATION_CONFIG；由外部迁移
平台处理 DDL 时可显式设置 SKIP_MIGRATIONS=1 后执行 update。
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
        first-install) with_deploy_lock prepare_first_run ;;
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
    echo "1) 首次 Web 安装（仅空部署）"
    echo "2) 日常更新部署（拉取、构建、备份、迁移、发布、就绪检查）"
    echo "3) 仅执行数据库迁移"
    echo "4) 初始化首个管理员（手工配置部署）"
    echo "5) 校验生产配置"
    echo "6) 仅拉取代码"
    echo "7) 仅编译"
    echo "8) 仅发布文件（不迁移、不重启）"
    echo "9) 启动已安装服务"
    echo "10) 停止已安装服务"
    echo "11) 重启已安装服务"
    echo "12) 查看全部服务状态"
    echo "0) 退出"

    if ! read -r -p "选择: " choice; then
        echo
        exit 0
    fi
    case "$choice" in
        1) command_name="first-install" ;;
        2) command_name="update" ;;
        3) command_name="migrate" ;;
        4) command_name="init-admin" ;;
        5) command_name="validate" ;;
        6) command_name="pull" ;;
        7) command_name="build" ;;
        8) command_name="deploy" ;;
        9) command_name="start" ;;
        10) command_name="stop" ;;
        11) command_name="restart" ;;
        12) command_name="status" ;;
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
