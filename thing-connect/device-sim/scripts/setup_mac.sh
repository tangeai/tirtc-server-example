#!/usr/bin/env bash
# setup_mac.sh — macOS 开发环境一键安装
#
# 用法:
#   chmod +x scripts/setup_mac.sh
#   ./scripts/setup_mac.sh
#
# 完成后激活虚拟环境:
#   source venv/bin/activate
#   然后运行: python3 device_sim_main.py ...

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
RESET='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo -e "${CYAN}══════════════════════════════════════════${RESET}"
echo -e "${CYAN}  TiRTC 设备模拟器 — macOS 环境安装${RESET}"
echo -e "${CYAN}══════════════════════════════════════════${RESET}"
echo ""

# ── 1. 检查 Python 3.10–3.12 ───────────────────────────────────────────────
PYTHON=""
for _py in python3.12 python3.11 python3.10 python3; do
    if command -v "$_py" &>/dev/null; then
        _ver=$("$_py" --version 2>&1 | awk '{print $2}')
        _major=$(echo "$_ver" | cut -d. -f1)
        _minor=$(echo "$_ver" | cut -d. -f2)
        if [ "$_major" -eq 3 ] && [ "$_minor" -ge 10 ] && [ "$_minor" -le 12 ]; then
            PYTHON="$_py"
            break
        fi
    fi
done

if [ -z "$PYTHON" ]; then
    echo -e "${RED}✗ 未找到受支持的 Python 3.10–3.12${RESET}"
    echo ""
    echo "请安装 Python 3.12:"
    echo "  brew install python@3.12"
    echo ""
    echo "或从官网下载: https://www.python.org/downloads/"
    exit 1
fi
echo -e "${GREEN}✓ Python: $($PYTHON --version)${RESET}"

# ── 2. 创建虚拟环境 ─────────────────────────────────────────────────────────
VENV_DIR="$PROJECT_DIR/venv"
if [ ! -d "$VENV_DIR" ]; then
    echo -e "${CYAN}创建虚拟环境...${RESET}"
    $PYTHON -m venv "$VENV_DIR"
    echo -e "${GREEN}✓ 虚拟环境: $VENV_DIR${RESET}"
else
    echo -e "${GREEN}✓ 虚拟环境已存在: $VENV_DIR${RESET}"
fi

# ── 3. 激活并安装依赖 ───────────────────────────────────────────────────────
echo ""
echo -e "${CYAN}安装 Python 依赖...${RESET}"
source "$VENV_DIR/bin/activate"
pip install --upgrade pip -q
pip install -r "$PROJECT_DIR/device-sim-py/requirements.txt"

# ── 4. 检查扩展素材生成依赖（非首次启动必需） ──────────────────────────────
echo ""
echo -e "${CYAN}检查扩展素材生成依赖（可选）...${RESET}"
_brew_missing=""
for _cmd in espeak-ng ffmpeg; do
    if command -v "$_cmd" &>/dev/null; then
        echo -e "${GREEN}  ✓ $_cmd${RESET}"
    else
        echo -e "${RED}  ✗ $_cmd${RESET}"
        _brew_missing="$_brew_missing $_cmd"
    fi
done

if [ -n "$_brew_missing" ]; then
    echo ""
    echo -e "${CYAN}未安装可选依赖，不影响使用仓库随附的默认音视频素材。${RESET}"
    echo "需要生成 Opus、AMR、H264 或 MJPEG 扩展素材时，请执行:"
    echo "  brew install$_brew_missing"
fi

# ── 5. 完成 ─────────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}══════════════════════════════════════════${RESET}"
echo -e "${GREEN}  安装完成！${RESET}"
echo -e "${GREEN}══════════════════════════════════════════${RESET}"
echo ""
echo "激活虚拟环境后运行:"
echo "  source venv/bin/activate"
echo "  # 如需 Windows 同类的麦克风/扬声器硬件链路，再额外安装: pip install sounddevice"
echo "  python3 device-sim-py/device_sim_main.py --endpoint=http://ep-open.tangeopen.com"
echo ""
