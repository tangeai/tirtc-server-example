#!/usr/bin/env bash

set -Eeuo pipefail

# ================== 配置 ==================
REPO_URL="${REPO_URL:-https://github.com/tangeai/tirtc-server-example.git}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="tirtc-server-example"
WORK_DIR="thing-connect"

# 生产发布根目录。现有部署可继续通过 DEPLOY_ROOT 指向原目录。
PRODUCTION_DEPLOY_ROOT="/opt/thing-connect"
DEPLOY_ROOT_INPUT="${DEPLOY_ROOT:-$PRODUCTION_DEPLOY_ROOT}"
if ! DEPLOY_ROOT="$(cd "$DEPLOY_ROOT_INPUT" 2>/dev/null && pwd)"; then
    echo "[ERROR] 部署目录不存在: ${DEPLOY_ROOT_INPUT}"
    exit 1
fi
REPO_PATH="${REPO_PATH:-$DEPLOY_ROOT/$REPO_DIR}"
BUILD_DIR="${BUILD_DIR:-$REPO_PATH/$WORK_DIR}"

# 生产服务仅由 Supervisor 托管。
SUPERVISORCTL="${SUPERVISORCTL:-supervisorctl}"
# 对应 supervisor/thing-connect.conf.example 的 [group:thing-connect]。
SUPERVISOR_GROUP="${SUPERVISOR_GROUP:-thing-connect}"
SUPERVISOR_WAIT_SECONDS="${SUPERVISOR_WAIT_SECONDS:-15}"
SUPERVISOR_STABLE_SECONDS="${SUPERVISOR_STABLE_SECONDS:-2}"
BACKUP_KEEP_COUNT="${BACKUP_KEEP_COUNT:-10}"

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
        $0 == section ":" { inside=1; next }
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
        $0 == section ":" { inside=1; next }
        /^[^[:space:]]/ { inside=0 }
        inside && $0 ~ "^[[:space:]]+" key ":[[:space:]]*" { found=1; exit }
        END { exit !found }
    ' "$file"
}

validate_configs() {
    local svc cfg jwt expected_jwt="" internal expected_internal="" url
    for svc in "${BUSINESS_SERVICES[@]}"; do
        cfg="$DEPLOY_ROOT/$svc/config.yaml"
        [ -f "$cfg" ] || { err "$svc: config.yaml 不存在"; return 1; }
        chmod 600 "$cfg"
        jwt="$(yaml_top_value "$cfg" jwt_secret)"
        [ -n "$jwt" ] || { err "$svc: jwt_secret 未配置"; return 1; }
        [ -z "$expected_jwt" ] && expected_jwt="$jwt"
        [ "$jwt" = "$expected_jwt" ] || { err "$svc: jwt_secret 与其他服务不一致"; return 1; }
    done

    for svc in "${ALL_SERVICES[@]}"; do
        cfg="$DEPLOY_ROOT/$svc/config.yaml"
        internal="$(yaml_section_value "$cfg" internal key)"
        [ -n "$internal" ] || { err "$svc: internal.key 未配置"; return 1; }
        [ -z "$expected_internal" ] && expected_internal="$internal"
        [ "$internal" = "$expected_internal" ] || { err "$svc: internal.key 与其他服务不一致"; return 1; }
        if yaml_has_section_key "$cfg" call internal_key; then
            err "$svc: 请将旧配置 call.internal_key 迁移为 internal.key"
            return 1
        fi
    done

    cfg="$DEPLOY_ROOT/admin-server/config.yaml"
    [ -n "$(yaml_section_value "$cfg" admin jwt_secret)" ] || { err "admin-server: admin.jwt_secret 未配置"; return 1; }
    [ -n "$(yaml_section_value "$cfg" security config_encryption_key)" ] || { err "admin-server: security.config_encryption_key 未配置"; return 1; }

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
    log "拉取代码..."
    if [ -d "$REPO_PATH/.git" ]; then
        git -C "$REPO_PATH" pull
    else
        git clone "$REPO_URL" "$REPO_PATH"
    fi
    log "待发布版本: $(git -C "$REPO_PATH" rev-parse --short HEAD)"
}

# ================== 编译 ==================
build_services() {
    [ -f "$BUILD_DIR/go.mod" ] || { err "源码目录未就绪（缺少 $BUILD_DIR/go.mod）"; return 1; }
    "$BUILD_DIR/build.sh" "$@"
}

# ================== 停服务 ==================
stop_one() {
    local svc="$1"

    log "停止 $svc ..."

    local state
    require_supervisor_service "$svc"
    state="$(supervisor_state "$svc")"
    if [ "$state" = "STOPPED" ]; then
        log "$svc 已停止（Supervisor）"
        return
    fi
    "$SUPERVISORCTL" stop "$(supervisor_program "$svc")" || true
    wait_for_supervisor_state "$svc" "STOPPED"
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

    mkdir -p "$target_dir"

    cp "$src_bin" "$target_dir/$svc.tmp"
    mv -f "$target_dir/$svc.tmp" "$target_dir/$svc"

    chmod +x "$target_dir/$svc"

    # 配置
    local config_example="$BUILD_DIR/$svc/config.yaml.example"
    [ "$svc" != "admin-server" ] || config_example="$BUILD_DIR/admin/admin-server/config.yaml.example"
    if [ ! -f "$target_dir/config.yaml" ] && [ -f "$config_example" ]; then
        cp "$config_example" "$target_dir/config.yaml"
        chmod 600 "$target_dir/config.yaml"
    fi

    # 静态目录先完整复制到临时目录，再原子切换。
    case "$svc" in user-server|ai-server|admin-server)
        local static_source="$BUILD_DIR/$svc/static"
        [ "$svc" != "admin-server" ] || static_source="$BUILD_DIR/admin/admin-web/dist"
        if [ -d "$static_source" ]; then
            rm -rf "$target_dir/static.new"
            cp -a "$static_source" "$target_dir/static.new"
            rm -rf "$target_dir/static.old"
            [ ! -d "$target_dir/static" ] || mv "$target_dir/static" "$target_dir/static.old"
            mv "$target_dir/static.new" "$target_dir/static"
            rm -rf "$target_dir/static.old"
            log "$svc: static/ 已同步"
        fi
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
    require_supervisor_service "$svc"
    state="$(supervisor_state "$svc")"
    if [ "$state" = "RUNNING" ]; then
        log "$svc 已在运行（Supervisor）"
        return
    fi
    log "启动 $svc（Supervisor）..."
    "$SUPERVISORCTL" start "$(supervisor_program "$svc")"
    wait_for_supervisor_state "$svc" "RUNNING"
    log "$svc 启动成功（Supervisor）"
}

# ================== 重启 ==================
restart_one() {
    local svc="$1"

    require_supervisor_service "$svc"
    log "重启 $svc（Supervisor）..."
    "$SUPERVISORCTL" restart "$(supervisor_program "$svc")"
    wait_for_supervisor_state "$svc" "RUNNING"
    log "$svc 重启成功（Supervisor）"
}

# ================== 状态 ==================
status_one() {
    local svc="$1"
    require_supervisor_service "$svc"
    "$SUPERVISORCTL" status "$(supervisor_program "$svc")"
}

# ================== 批量 ==================
run_batch() {
    local action=$1
    shift

    validate_services "$@"
    if [ "$action" = "build" ]; then
        build_services "$@"
        return
    fi
    case "$action" in
        deploy|start|stop|restart|status) validate_service_manager ;;
    esac
    for svc in "$@"; do
        case "$action" in
            deploy) deploy_one "$svc" ;;
            start) start_one "$svc" ;;
            stop) stop_one "$svc" ;;
            restart) restart_one "$svc" ;;
            status) status_one "$svc" ;;
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
    mkdir -p "$BACKUP_DIR"
    : > "$BACKUP_DIR/.deploying"
    for svc in "$@"; do
        target="$DEPLOY_ROOT/$svc"
        backup="$BACKUP_DIR/$svc"
        mkdir -p "$backup"
        [ ! -f "$target/$svc" ] || cp -a "$target/$svc" "$backup/$svc"
        [ ! -d "$target/static" ] || cp -a "$target/static" "$backup/static"
    done
    log "当前版本已备份到 $BACKUP_DIR"
}

mark_backup_successful() {
    rm -f "$BACKUP_DIR/.deploying"
    : > "$BACKUP_DIR/.successful"
}

mark_backup_failed() {
    rm -f "$BACKUP_DIR/.deploying"
    : > "$BACKUP_DIR/.failed"
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
    local svc backup target
    err "发布失败，开始回滚"
    for svc in "${DEPLOYED_SERVICES[@]}"; do
        backup="$BACKUP_DIR/$svc"
        target="$DEPLOY_ROOT/$svc"
        stop_one "$svc" || true
        if [ -f "$backup/$svc" ]; then
            cp -a "$backup/$svc" "$target/$svc.rollback"
            mv -f "$target/$svc.rollback" "$target/$svc"
            chmod +x "$target/$svc"
        else
            # 首次发布时没有可恢复的旧版本，不能保留失败的新二进制。
            rm -f "$target/$svc"
        fi
        if [ -d "$backup/static" ]; then
            rm -rf "$target/static.rollback"
            cp -a "$backup/static" "$target/static.rollback"
            rm -rf "$target/static"
            mv "$target/static.rollback" "$target/static"
        elif [ -d "$target/static" ]; then
            rm -rf "$target/static"
        fi
        if [ -f "$backup/$svc" ]; then
            start_one "$svc" || err "$svc 回滚后启动失败，请人工处理"
        else
            warn "$svc 没有可回滚版本，已保持停止状态"
        fi
    done
    mark_backup_failed
    err "回滚完成；失败版本备份目录: $BACKUP_DIR"
}

full_deploy() {
    local services=("${ALL_SERVICES[@]}") svc
    validate_deploy_root
    validate_service_manager
    validate_backup_retention
    pull_code
    validate_paths
    run_batch build "${services[@]}"
    validate_configs
    backup_release "${services[@]}"
    DEPLOYED_SERVICES=()

    for svc in "${services[@]}"; do
        # Include the current service before touching it so a mid-deploy error
        # also restores and restarts that service.
        DEPLOYED_SERVICES+=("$svc")
        if ! deploy_one "$svc"; then
            rollback_release
            return 1
        fi
        if ! restart_one "$svc"; then
            rollback_release
            return 1
        fi
    done
    mark_backup_successful
    prune_successful_backups
    log "全流程发布成功: $(git -C "$REPO_PATH" rev-parse --short HEAD)"
    log "回滚备份保留在: $BACKUP_DIR"
}

# ================== 选择 ==================
select_all() {
    echo "${ALL_SERVICES[@]}"
}

# ================== 菜单 ==================
menu() {
    echo ""
    echo "1) 拉代码"
    echo "2) 编译"
    echo "3) 发布"
    echo "4) 启动"
    echo "5) 停止"
    echo "6) 重启"
    echo "7) 状态"
    echo "8) 全流程"
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
        9) exit 0 ;;
    esac
}

# ================== 主循环 ==================
while true; do
    menu
done
