#!/usr/bin/env bash
# run.sh — TiRTC 设备模拟器启动器（自动激活 venv）
#
# 前提: 已运行 ./scripts/setup_mac.sh（仅需一次）
# 用法: ./run.sh --with-mic
#       ./run.sh --up-audio-format=pcm_s16le_16khz

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
VENV_DIR="$PROJECT_DIR/venv"

if [ ! -f "$VENV_DIR/bin/activate" ]; then
    echo "虚拟环境未安装，请先运行:"
    echo "  ./scripts/setup_mac.sh"
    exit 1
fi

source "$VENV_DIR/bin/activate"
cd "$PROJECT_DIR/device-sim-py"
exec python3 device_sim_main.py "$@"
