#!/usr/bin/env bash

set -e

# ================== 配置 ==================
REPO_URL="git@github.com:tangeai/tirtc-server-example.git"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="tirtc-server-example"
WORK_DIR="thing-connect"

BUILD_DIR="$SCRIPT_DIR/$REPO_DIR/$WORK_DIR"
DEPLOY_ROOT="$SCRIPT_DIR"

PID_DIR="$DEPLOY_ROOT/run"
LOG_DIR="$DEPLOY_ROOT/logs"

mkdir -p "$PID_DIR" "$LOG_DIR"

ALL_SERVICES=("device-server" "user-server" "voip-server" "ai-server" "call-server")

# ================== 工具函数 ==================
log() { echo -e "[INFO] $1"; }
warn() { echo -e "[WARN] $1"; }
err() { echo -e "[ERROR] $1"; }

# ================== 拉代码 ==================
pull_code() {
    log "拉取代码..."
    if [ -d "$SCRIPT_DIR/$REPO_DIR/.git" ]; then
        git -C "$SCRIPT_DIR/$REPO_DIR" pull
    else
        git clone "$REPO_URL" "$SCRIPT_DIR/$REPO_DIR"
    fi
}

# ================== 编译 ==================
build_services() {
    cd "$BUILD_DIR"
    mkdir -p bin

    for svc in "$@"; do
        log "编译 $svc ..."
        CGO_ENABLED=0 go build -o "bin/$svc" "./$svc/"
    done
}

# ================== 停服务（关键修复） ==================
stop_one() {
    local svc="$1"

    log "停止 $svc ..."

    # 1. PID 文件方式
    local pid_file="$PID_DIR/$svc.pid"
    if [ -f "$pid_file" ]; then
        pid=$(cat "$pid_file")
        kill "$pid" 2>/dev/null || true
    fi

    # 2. 进程名兜底（关键）
    pkill -f "$svc" 2>/dev/null || true

    # 3. 等待退出
    for i in {1..20}; do
        if ! pgrep -f "$svc" >/dev/null; then
            break
        fi
        sleep 0.5
    done

    # 4. 强杀兜底
    if pgrep -f "$svc" >/dev/null; then
        warn "$svc 未退出，强制 kill -9"
        pkill -9 -f "$svc" || true
    fi

    rm -f "$pid_file"
    log "$svc 已停止"
}

# ================== 发布（解决 Text file busy） ==================
deploy_one() {
    local svc="$1"

    local target_dir="$DEPLOY_ROOT/$svc"
    local src_bin="$BUILD_DIR/bin/$svc"

    stop_one "$svc"

    if [ ! -f "$src_bin" ]; then
        err "找不到 $src_bin"
        return 1
    fi

    mkdir -p "$target_dir"

    # ⭐ 原子发布（核心修复）
    cp "$src_bin" "$target_dir/$svc.tmp"
    mv -f "$target_dir/$svc.tmp" "$target_dir/$svc"

    chmod +x "$target_dir/$svc"

    # 配置
    if [ ! -f "$target_dir/config.yaml" ] && [ -f "$BUILD_DIR/$svc/config.yaml.example" ]; then
        cp "$BUILD_DIR/$svc/config.yaml.example" "$target_dir/config.yaml"
    fi

    # user-server / ai-server 都需要 static 目录，启动时找不到会直接 Fatalf。
    if { [ "$svc" = "user-server" ] || [ "$svc" = "ai-server" ]; } && [ -d "$BUILD_DIR/$svc/static" ]; then
        rm -rf "$target_dir/static"
        cp -r "$BUILD_DIR/$svc/static" "$target_dir/"
        log "$svc: static/ 已同步"
    fi

    log "发布完成 $svc"
}

# ================== 启动 ==================
start_one() {
    local svc="$1"

    local bin="$DEPLOY_ROOT/$svc/$svc"
    local cfg="$DEPLOY_ROOT/$svc/config.yaml"
    local pid_file="$PID_DIR/$svc.pid"
    local log_file="$LOG_DIR/$svc.log"

    if [ ! -f "$bin" ]; then
        err "$svc 未发布"
        return 1
    fi

    stop_one "$svc"

    log "启动 $svc ..."

    nohup "$bin" -c "$cfg" >> "$log_file" 2>&1 &

    echo $! > "$pid_file"

    sleep 1

    if kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log "$svc 启动成功"
    else
        err "$svc 启动失败"
        tail -n 20 "$log_file"
        return 1
    fi
}

# ================== 重启 ==================
restart_one() {
    stop_one "$1"
    sleep 1
    start_one "$1"
}

# ================== 状态 ==================
status_one() {
    local svc="$1"
    if pgrep -f "$svc" >/dev/null; then
        echo "$svc: RUNNING"
    else
        echo "$svc: STOPPED"
    fi
}

# ================== 批量 ==================
run_batch() {
    local action=$1
    shift

    for svc in "$@"; do
        case "$action" in
            build) build_services "$svc" ;;
            deploy) deploy_one "$svc" ;;
            start) start_one "$svc" ;;
            stop) stop_one "$svc" ;;
            restart) restart_one "$svc" ;;
            status) status_one "$svc" ;;
        esac
    done
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
        1) pull_code ;;
        2) run_batch build "${services[@]}" ;;
        3) run_batch deploy "${services[@]}" ;;
        4) run_batch start "${services[@]}" ;;
        5) run_batch stop "${services[@]}" ;;
        6) run_batch restart "${services[@]}" ;;
        7) run_batch status "${services[@]}" ;;
        8)
            pull_code
            run_batch build "${services[@]}"
            run_batch deploy "${services[@]}"
            run_batch restart "${services[@]}"
            ;;
        9) exit 0 ;;
    esac
}

# ================== 主循环 ==================
while true; do
    menu
done
