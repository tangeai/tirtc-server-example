#!/usr/bin/env bash

set -Eeuo pipefail

# ================== 配置 ==================
# 常规部署只需按实际环境修改下面三行；同名环境变量可临时覆盖。
PRODUCTION_DEPLOY_ROOT="/data/demo-open.tangeai.cn"
DEFAULT_REPO_URL="git@github.com:tangeai/tirtc-server-example.git"
DEFAULT_SUPERVISOR_GROUP="demo-open"

REPO_URL="${REPO_URL:-$DEFAULT_REPO_URL}"
SUPERVISOR_GROUP="${SUPERVISOR_GROUP:-$DEFAULT_SUPERVISOR_GROUP}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="tirtc-server-example"
WORK_DIR="thing-connect"

# 生产发布根目录。现有部署可继续通过 DEPLOY_ROOT 指向原目录。
DEPLOY_ROOT_INPUT="${DEPLOY_ROOT:-$PRODUCTION_DEPLOY_ROOT}"
if ! DEPLOY_ROOT="$(cd "$DEPLOY_ROOT_INPUT" 2>/dev/null && pwd)"; then
    echo "[ERROR] 部署目录不存在: ${DEPLOY_ROOT_INPUT}"
    exit 1
fi
REPO_PATH="${REPO_PATH:-$DEPLOY_ROOT/$REPO_DIR}"
BUILD_DIR="${BUILD_DIR:-$REPO_PATH/$WORK_DIR}"

# 生产服务仅由 Supervisor 托管。
SUPERVISORCTL="${SUPERVISORCTL:-supervisorctl}"
# 对应 deploy/supervisor/demo-open.supervisor.conf 的 [group:demo-open]。
SUPERVISOR_WAIT_SECONDS="${SUPERVISOR_WAIT_SECONDS:-15}"
SUPERVISOR_STABLE_SECONDS="${SUPERVISOR_STABLE_SECONDS:-2}"
HEALTH_WAIT_SECONDS="${HEALTH_WAIT_SECONDS:-30}"
HEALTH_REQUEST_TIMEOUT_SECONDS="${HEALTH_REQUEST_TIMEOUT_SECONDS:-3}"
HEALTH_HOST="${HEALTH_HOST:-127.0.0.1}"
BACKUP_KEEP_COUNT="${BACKUP_KEEP_COUNT:-10}"
MIGRATION_CONFIG="${MIGRATION_CONFIG:-$DEPLOY_ROOT/admin-server/migration-config.yaml}"
SKIP_MIGRATIONS="${SKIP_MIGRATIONS:-0}"
ALLOW_INSECURE_ADMIN_COOKIE="${ALLOW_INSECURE_ADMIN_COOKIE:-0}"

mkdir -p "$DEPLOY_ROOT/logs"

# 同一时间只允许一个发布脚本运行。
exec 9>"$DEPLOY_ROOT/deploy.lock"
if ! flock -n 9; then
    echo "[ERROR] 另一个发布任务正在运行"
    exit 1
fi

BUSINESS_SERVICES=("device-server" "user-server" "voip-server" "ai-server" "call-server")
ALL_SERVICES=("${BUSINESS_SERVICES[@]}" "admin-server")

# ================== 工具函数 ==================
log() { echo -e "[INFO] $1"; }
warn() { echo -e "[WARN] $1"; }
err() { echo -e "[ERROR] $1"; }

validate_deploy_root() {
    [ -d "$DEPLOY_ROOT" ] || { err "部署目录不存在: $DEPLOY_ROOT"; return 1; }
}

validate_paths() {
    validate_deploy_root
    [ -f "$BUILD_DIR/go.mod" ] || {
        err "源码目录未就绪（缺少 $BUILD_DIR/go.mod）"
        err "请检查 DEPLOY_ROOT=$DEPLOY_ROOT 与 BUILD_DIR=$BUILD_DIR"
        return 1
    }
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

validate_service_manager() {
    local svc
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
    for svc in "${ALL_SERVICES[@]}"; do
        require_supervisor_service "$svc" || return 1
    done
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

validate_configs() {
    local svc cfg jwt expected_jwt="" internal expected_internal="" url
    local database_dsn
    local redis_addr redis_password redis_db
    local expected_redis_addr="" expected_redis_password="" expected_redis_db=""

    for svc in "${ALL_SERVICES[@]}"; do
        cfg="$DEPLOY_ROOT/$svc/config.yaml"
        [ -f "$cfg" ] || { err "$svc: config.yaml 不存在"; return 1; }
        chmod 600 "$cfg" || return 1

        database_dsn="$(yaml_section_value "$cfg" database dsn)"
        [ -n "$database_dsn" ] || { err "$svc: database.dsn 未配置"; return 1; }

        redis_addr="$(yaml_section_value "$cfg" redis addr)"
        redis_password="$(yaml_section_value "$cfg" redis password)"
        redis_db="$(yaml_section_value "$cfg" redis db)"
        [ -n "$redis_addr" ] || { err "$svc: redis.addr 未配置"; return 1; }
        [ -n "$redis_db" ] || { err "$svc: redis.db 未配置"; return 1; }
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

    for svc in "${BUSINESS_SERVICES[@]}"; do
        cfg="$DEPLOY_ROOT/$svc/config.yaml"
        jwt="$(yaml_top_value "$cfg" jwt_secret)"
        [ -n "$jwt" ] || { err "$svc: jwt_secret 未配置"; return 1; }
        ! is_placeholder_secret "$jwt" || { err "$svc: jwt_secret 仍是公开占位值"; return 1; }
        [ -z "$expected_jwt" ] && expected_jwt="$jwt"
        [ "$jwt" = "$expected_jwt" ] || { err "$svc: jwt_secret 与其他服务不一致"; return 1; }
    done

    for svc in "${ALL_SERVICES[@]}"; do
        cfg="$DEPLOY_ROOT/$svc/config.yaml"
        internal="$(yaml_section_value "$cfg" internal key)"
        [ -n "$internal" ] || { err "$svc: internal.key 未配置"; return 1; }
        ! is_placeholder_secret "$internal" || { err "$svc: internal.key 仍是公开占位值"; return 1; }
        [ -z "$expected_internal" ] && expected_internal="$internal"
        [ "$internal" = "$expected_internal" ] || { err "$svc: internal.key 与其他服务不一致"; return 1; }
        if yaml_has_section_key "$cfg" call internal_key; then
            err "$svc: 请将旧配置 call.internal_key 迁移为 internal.key"
            return 1
        fi
    done

    cfg="$DEPLOY_ROOT/admin-server/config.yaml"
    local admin_jwt config_key cookie_secure
    admin_jwt="$(yaml_section_value "$cfg" admin jwt_secret)"
    config_key="$(yaml_section_value "$cfg" security config_encryption_key)"
    cookie_secure="$(yaml_section_value "$cfg" admin cookie_secure)"
    [ -n "$admin_jwt" ] || { err "admin-server: admin.jwt_secret 未配置"; return 1; }
    ! is_placeholder_secret "$admin_jwt" || { err "admin-server: admin.jwt_secret 仍是公开占位值"; return 1; }
    [ -n "$config_key" ] || { err "admin-server: security.config_encryption_key 未配置"; return 1; }
    ! is_placeholder_secret "$config_key" || { err "admin-server: security.config_encryption_key 仍是公开占位值"; return 1; }
    if [ "$cookie_secure" != "true" ]; then
        if [ "$ALLOW_INSECURE_ADMIN_COOKIE" = "1" ]; then
            warn "admin-server: admin.cookie_secure 未启用，仅允许用于明确的非 HTTPS 环境"
        else
            err "admin-server: 生产环境必须设置 admin.cookie_secure: true"
            return 1
        fi
    fi

    for svc in "${BUSINESS_SERVICES[@]}"; do
        url="$(yaml_section_value "$DEPLOY_ROOT/$svc/config.yaml" admin server_url)"
        [ -n "$url" ] || { err "$svc: admin.server_url 未配置"; return 1; }
    done

    for svc in ai voip call; do
        url="$(yaml_section_value "$DEPLOY_ROOT/user-server/config.yaml" "$svc" server_url)"
        [ -n "$url" ] || { err "user-server: $svc.server_url 未配置"; return 1; }
    done
    if yaml_has_section_key "$DEPLOY_ROOT/user-server/config.yaml" call call_server_url; then
        err "user-server: 请将旧配置 call.call_server_url 迁移为 call.server_url"
        return 1
    fi
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
    [ -f "$BUILD_DIR/go.mod" ] || { err "源码目录未就绪（缺少 $BUILD_DIR/go.mod）"; return 1; }
    "$BUILD_DIR/build.sh" "$@" || return 1
}

# ================== 数据库迁移 ==================
run_migrations() {
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
    log "执行数据库迁移..."
    "$BUILD_DIR/bin/admin-server" -c "$MIGRATION_CONFIG" -migrate-only || return 1
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
    local admin_cfg="$DEPLOY_ROOT/admin-server/config.yaml"

    validate_paths || return 1
    validate_release_options || return 1
    validate_configs || return 1
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

    run_migrations || return 1
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
    local svc="$1" port
    port="$(yaml_section_value "$DEPLOY_ROOT/$svc/config.yaml" server http_port)"
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
    if [ "$state" = "STOPPED" ]; then
        log "$svc 已停止（Supervisor）"
        return
    fi
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
    if [ ! -f "$target_dir/config.yaml" ] && [ -f "$config_example" ]; then
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
    local cfg="$DEPLOY_ROOT/$svc/config.yaml"

    if [ ! -f "$bin" ]; then
        err "$svc 未发布"
        return 1
    fi

    if [ ! -f "$cfg" ]; then
        err "$svc: config.yaml 不存在"
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

    validate_services "$@"
    if [ "$action" = "build" ]; then
        build_services "$@" || return 1
        return
    fi
    case "$action" in
        start|stop|restart|status) validate_service_manager ;;
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
    local release_id svc target backup
    release_id="$(date +%Y%m%d-%H%M%S)-$(git -C "$REPO_PATH" rev-parse --short HEAD)"
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
    local releases_dir="$DEPLOY_ROOT/releases" backup
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

rollback_active_release() {
    local status="$1"
    trap - ERR INT TERM
    if ! rollback_release; then
        err "自动回滚未完整执行，请立即人工检查"
    fi
    return "$status"
}

abort_active_release() {
    local status="$1"
    trap - ERR INT TERM
    rollback_release || true
    exit "$status"
}

full_deploy() (
    local services=("${ALL_SERVICES[@]}") svc
    local status
    validate_deploy_root || return 1
    validate_service_manager || return 1
    validate_backup_retention || return 1
    validate_release_options || return 1
    pull_code || return 1
    validate_paths || return 1
    run_batch build "${services[@]}" || return 1
    validate_configs || return 1
    run_migrations || return 1
    backup_release "${services[@]}" || return 1
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
    prune_successful_backups || warn "清理过期备份失败，请稍后人工清理"
    log "全流程发布成功: $(git -C "$REPO_PATH" rev-parse --short HEAD)"
    log "回滚备份保留在: $BACKUP_DIR"
    log "首次部署如尚无管理员，请在菜单选择“初始化首个管理员”；运行中的 Admin 会自动刷新权限"
)

# ================== 选择 ==================
select_all() {
    echo "${ALL_SERVICES[@]}"
}

# ================== 菜单 ==================
menu() {
    echo ""
    echo "1) 拉代码"
    echo "2) 编译"
    echo "3) 仅发布文件（不迁移、不重启）"
    echo "4) 启动"
    echo "5) 停止"
    echo "6) 重启"
    echo "7) 状态"
    echo "8) 全流程"
    echo "0) 仅执行数据库迁移"
    echo "10) 仅校验配置"
    echo "11) 初始化首个管理员"
    echo "9) 退出"

    read -p "选择: " c

    services=(${ALL_SERVICES[@]})

    case $c in
        1) validate_deploy_root; pull_code ;;
        2) validate_paths; run_batch build "${services[@]}" ;;
        3) validate_configs; run_batch deploy "${services[@]}" ;;
        4) validate_configs; run_batch start "${services[@]}" ;;
        5) run_batch stop "${services[@]}" ;;
        6) validate_configs; run_batch restart "${services[@]}" ;;
        7) run_batch status "${services[@]}" ;;
        8) full_deploy ;;
        0) validate_paths; validate_release_options; run_migrations ;;
        10) validate_configs ;;
        11) initialize_first_admin ;;
        9) exit 0 ;;
    esac
}

# ================== 主循环 ==================
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    while true; do
        menu
    done
fi
