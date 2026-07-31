#!/usr/bin/env bash
# gen_assets.sh — 一键生成所有测试素材（音频 + 视频）
#
# 输出到 ../assets/：
#   音频（“1~10 + 格式播报”循环）：
#     number.pcm_s16le_8khz / number.pcm_s16le_16khz
#     number.alaw_8khz / number.alaw_16khz
#     number.opus_8khz / number.opus_16khz        （Ogg/Opus）
#     number.amr_nb / number.amr_wb
#
#   视频：
#     video_h264_annexb_1280x720_15fps_10s_150frames.h264
#     video_mjpeg_240x320_8fps_10s_80frames.mjpeg
#     video_mjpeg_320x240_8fps_10s_80frames.mjpeg
#     video_mjpeg_640x480_8fps_10s_80frames.mjpeg
#     video_mjpeg_480x640_8fps_10s_80frames.mjpeg
#     preview_h264_1280x720_15fps_10s_150frames.mp4
#     preview_mjpeg_<分辨率>_8fps_10s_80frames.mp4
#
#   仓库随附、不由本脚本覆盖的默认素材：
#     audio.g711a                 — 默认 A-law 8k 上行音频
#     video.h264                  — 默认 H.264 上行视频
#
# 语音：
#   - 默认优先使用 Microsoft Edge TTS（zh-CN-XiaoxiaoNeural）
#   - 未安装 edge-tts 时脚本会提示并尝试安装
#   - 安装或在线合成失败时自动回退到 espeak-ng，不中断素材生成
#
# 依赖：
#   - 系统命令：espeak-ng（回退语音）, ffmpeg, python3
#   - Python 包：numpy, soxr；Python 3.13+ 还需 audioop-lts；
#     edge-tts 由脚本按需安装
#   - AMR 编码（生成 number.amr_nb / number.amr_wb）：
#       ffmpeg 需带 libopencore_amrnb / libvo_amrwbenc
#
# 可直接安装：
#   macOS:
#     brew install espeak-ng ffmpeg python
#     python3 -m pip install numpy soxr edge-tts
#     # Python 3.13+:
#     python3 -m pip install audioop-lts
#
#   Ubuntu / Debian:
#     apt update
#     apt install -y espeak-ng ffmpeg python3 python3-pip python3-numpy \
#       libavcodec-extra
#     python3 -m pip install soxr edge-tts
#     # Python 3.13+:
#     python3 -m pip install audioop-lts
#
#   Windows:
#     1. 安装 Python 3
#     2. 安装 ffmpeg、espeak-ng 并加入 PATH
#     3. 执行: py -m pip install numpy soxr edge-tts
#        Python 3.13+ 再执行: py -m pip install audioop-lts

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# 可用 ASSETS_DIR 指向临时目录做生成验证，不污染默认素材目录。
ASSETS_DIR="${ASSETS_DIR:-$SCRIPT_DIR/../assets}"
# 默认素材随仓库提供；ASSETS_DIR 可指向临时目录生成扩展素材，
# 因而默认素材目录单独保留为脚本所在的 assets 目录。
DEFAULT_MEDIA_DIR="${DEFAULT_MEDIA_DIR:-$SCRIPT_DIR/../assets}"
PYTHON_BIN="${PYTHON_BIN:-python3}"
# 默认约 30 秒，适合放入嵌入式设备 Flash；模拟器会循环读取文件。
AUDIO_LOOPS="${AUDIO_LOOPS:-2}"
# auto: 优先 Microsoft，失败回退 espeak；也可显式指定 microsoft / espeak。
TTS_ENGINE="${TTS_ENGINE:-auto}"
MICROSOFT_TTS_VOICE="${MICROSOFT_TTS_VOICE:-zh-CN-XiaoxiaoNeural}"
MICROSOFT_TTS_RATE="${MICROSOFT_TTS_RATE:-+0%}"
EDGE_TTS_AUTO_INSTALL="${EDGE_TTS_AUTO_INSTALL:-1}"
mkdir -p "$ASSETS_DIR"
TMP_AUDIO_DIR="$(mktemp -d /tmp/gen_assets_audio.XXXXXX)"
trap 'rm -rf "$TMP_AUDIO_DIR"' EXIT

GREEN='\033[0;32m'
CYAN='\033[0;36m'
RED='\033[0;31m'
RESET='\033[0m'

echo -e "${CYAN}══════════════════════════════════════════${RESET}"
echo -e "${CYAN}  生成测试素材${RESET}"
echo -e "${CYAN}══════════════════════════════════════════${RESET}"
echo ""

# ── 依赖检查 ────────────────────────────────────────────────────────────────
_missing=""
for _cmd in espeak-ng ffmpeg ffprobe "$PYTHON_BIN"; do
    if ! command -v "$_cmd" &>/dev/null; then
        echo -e "${RED}✗ 缺少依赖: $_cmd${RESET}"
        _missing="$_missing $_cmd"
    fi
done
if [ -n "$_missing" ]; then
    echo ""
    echo -e "${RED}请安装缺失的依赖后重试:${RESET}"
    echo "  macOS:"
    echo "    brew install espeak-ng ffmpeg python"
    echo "    python3 -m pip install -r $SCRIPT_DIR/../device-sim-py/requirements.txt"
    echo "  Ubuntu / Debian:"
    echo "    apt update"
    echo "    apt install -y espeak-ng ffmpeg python3 python3-pip python3-numpy libavcodec-extra"
    echo "    python3 -m pip install -r $SCRIPT_DIR/../device-sim-py/requirements.txt"
    echo "  Windows:"
    echo "    py -m pip install -r $SCRIPT_DIR/../device-sim-py/requirements.txt"
    exit 1
fi

# 推流默认使用 H.264；缺少编码器时，继续执行只会在耗时较久后失败。
# 不用 grep -q：在 pipefail 下它会提前关闭管道，令 ffmpeg 收到 SIGPIPE，
# 从而把已安装的 libx264 误判为缺失。
if ! ffmpeg -hide_banner -encoders 2>/dev/null | grep -F 'libx264' >/dev/null; then
    echo -e "${RED}✗ 当前 ffmpeg 不支持 libx264，无法生成 H.264 测试视频${RESET}"
    echo "  请安装包含 libx264 的 ffmpeg 完整版后重试。"
    exit 1
fi
if ! ffmpeg -hide_banner -filters 2>/dev/null | grep -F 'drawtext' >/dev/null; then
    echo -e "${RED}✗ 当前 ffmpeg 不支持 drawtext，无法在测试视频中标注媒体信息${RESET}"
    echo "  请安装包含 libfreetype 和 drawtext 滤镜的 ffmpeg 完整版后重试。"
    exit 1
fi

# 检查 Python 依赖（3.13+ 的 audioop 由 audioop-lts 提供）
"$PYTHON_BIN" -c "import audioop, numpy, soxr" 2>/dev/null || {
    echo -e "${RED}✗ Python 依赖缺失 (audioop, numpy, soxr)${RESET}"
    echo "  Ubuntu / Debian:"
    echo "    apt install -y python3-pip python3-numpy"
    echo "    python3 -m pip install -r $SCRIPT_DIR/../device-sim-py/requirements.txt"
    echo "  macOS / Windows:"
    echo "    python3 -m pip install -r $SCRIPT_DIR/../device-sim-py/requirements.txt"
    exit 1
}

_edge_tts_available() {
    "$PYTHON_BIN" -c "import edge_tts" >/dev/null 2>&1
}

_prepare_tts_engine() {
    case "$TTS_ENGINE" in
        auto|microsoft)
            if _edge_tts_available; then
                ACTIVE_TTS_ENGINE=microsoft
                return
            fi

            echo -e "${CYAN}[audio] 未检测到 Microsoft Edge TTS（edge-tts）${RESET}"
            if [ "$EDGE_TTS_AUTO_INSTALL" = "1" ]; then
                echo -e "${CYAN}        正在尝试安装: $PYTHON_BIN -m pip install edge-tts${RESET}"
                if "$PYTHON_BIN" -m pip install edge-tts; then
                    if _edge_tts_available; then
                        ACTIVE_TTS_ENGINE=microsoft
                        echo -e "${GREEN}        Microsoft Edge TTS 安装成功${RESET}"
                        return
                    fi
                fi
                echo -e "${CYAN}        edge-tts 安装失败，自动使用 espeak-ng 默认语音${RESET}"
            else
                echo -e "${CYAN}        已关闭自动安装，使用 espeak-ng 默认语音${RESET}"
                echo -e "${CYAN}        手工安装: $PYTHON_BIN -m pip install edge-tts${RESET}"
            fi
            ACTIVE_TTS_ENGINE=espeak
            ;;
        espeak)
            ACTIVE_TTS_ENGINE=espeak
            ;;
        *)
            echo -e "${RED}✗ TTS_ENGINE 只支持 auto、microsoft 或 espeak，当前值: $TTS_ENGINE${RESET}"
            exit 1
            ;;
    esac
}

_verify_bundled_default_media() {
    local audio="$DEFAULT_MEDIA_DIR/audio.g711a"
    local video="$DEFAULT_MEDIA_DIR/video.h264"
    for required in "$audio" "$video"; do
        if [ ! -s "$required" ]; then
            echo -e "${RED}✗ 缺少仓库随附的默认素材: $required${RESET}"
            echo "  请重新获取仓库中的 assets 文件，或显式设置 DEFAULT_MEDIA_DIR。"
            exit 1
        fi
    done
    if ! ffprobe -v error -f h264 -show_entries stream=codec_name \
        -of default=nw=1 "$video" 2>/dev/null | grep -qx 'codec_name=h264'; then
        echo -e "${RED}✗ 默认视频不是有效的 H.264 裸流: $video${RESET}"
        exit 1
    fi
    echo -e "${GREEN}内置默认素材可用${RESET}"
    echo -e "  audio: $audio"
    echo -e "  video: $video"
}

_verify_bundled_default_media

# 检查 AMR 编码器（非必需，缺失时跳过）
_probe_amr_encoder() {
    local codec=$1
    local rate=$2
    local bitrate=$3
    local out="/tmp/.gen_assets_${codec}_$$.amr"
    if [ -n "$bitrate" ]; then
        ffmpeg -hide_banner -loglevel error \
            -f lavfi -i "anullsrc=r=${rate}:cl=mono" -t 0.2 \
            -c:a "$codec" -b:a "$bitrate" -ar "$rate" -ac 1 \
            -f amr "$out" >/dev/null 2>&1
    else
        ffmpeg -hide_banner -loglevel error \
            -f lavfi -i "anullsrc=r=${rate}:cl=mono" -t 0.2 \
            -c:a "$codec" -ar "$rate" -ac 1 \
            -f amr "$out" >/dev/null 2>&1
    fi
    if [ $? -eq 0 ]; then
        rm -f "$out"
        return 0
    fi
    rm -f "$out"
    return 1
}

_has_amr_nb=false
_has_amr_wb=false
_probe_amr_encoder libopencore_amrnb 8000 12.2k && _has_amr_nb=true
_probe_amr_encoder libvo_amrwbenc 16000 "" && _has_amr_wb=true

ACTIVE_TTS_ENGINE=espeak
_prepare_tts_engine

echo -e "${GREEN}依赖检查通过${RESET}"
echo -e "  script: $0"
echo -e "  pwd: $PWD"
echo -e "  ffmpeg: $(command -v ffmpeg)"
echo -e "  python: $(command -v "$PYTHON_BIN")"
if [ "$ACTIVE_TTS_ENGINE" = "microsoft" ]; then
    echo -e "  TTS: Microsoft Edge TTS ($MICROSOFT_TTS_VOICE)"
else
    echo -e "  TTS: espeak-ng（默认回退语音）"
fi
echo -e "  AMR-NB encoder: $($_has_amr_nb && echo yes || echo no)"
echo -e "  AMR-WB encoder: $($_has_amr_wb && echo yes || echo no)"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# 音频
# ══════════════════════════════════════════════════════════════════════════════
cd "$SCRIPT_DIR"

_show_file() {
    local path=$1
    echo -e "  $(basename "$path")  $(du -h "$path" | cut -f1)"
}

_build_espeak_announce_pcm() {
    local rate=$1
    local phrase=$2
    local out_raw=$3
    local workdir="$TMP_AUDIO_DIR/${rate}_$(basename "$out_raw")"
    local digits_prefix="$workdir/digits"
    local format_prefix="$workdir/format"
    local digits_raw="${digits_prefix}_pcm.raw"
    local format_raw="${format_prefix}_pcm.raw"
    local seq_raw="$workdir/sequence.raw"

    mkdir -p "$workdir"

    "$PYTHON_BIN" number2alaw.py \
      --lang zh --rate "$rate" --format pcm --gap 1.0 --speed 220 \
      --output-prefix "$digits_prefix" \
      --text 一 --text 二 --text 三 --text 四 --text 五 \
      --text 六 --text 七 --text 八 --text 九 --text 十 \
      >/dev/null

    "$PYTHON_BIN" number2alaw.py \
      --lang zh --rate "$rate" --format pcm --gap 2.4 --speed 210 \
      --output-prefix "$format_prefix" \
      --text "$phrase" \
      >/dev/null

    : > "$seq_raw"
    for ((i = 0; i < AUDIO_LOOPS; i++)); do
        cat "$digits_raw" >> "$seq_raw"
        cat "$format_raw" >> "$seq_raw"
    done
    cp "$seq_raw" "$out_raw"
}

_build_microsoft_announce_pcm() {
    local rate=$1
    local phrase=$2
    local out_raw=$3
    local workdir="$TMP_AUDIO_DIR/microsoft_${rate}_$(basename "$out_raw")"
    local speech_mp3="$workdir/speech.mp3"
    local speech_raw="$workdir/speech.raw"
    local seq_raw="$workdir/sequence.raw"
    local error_log="$workdir/edge-tts.log"
    local text="一，二，三，四，五，六，七，八，九，十。${phrase}，单声道。"

    mkdir -p "$workdir"
    if ! "$PYTHON_BIN" -m edge_tts \
        --voice "$MICROSOFT_TTS_VOICE" \
        --rate "$MICROSOFT_TTS_RATE" \
        --text "$text" \
        --write-media "$speech_mp3" \
        >"$error_log" 2>&1; then
        echo -e "${CYAN}        Microsoft Edge TTS 在线合成失败${RESET}"
        tail -n 1 "$error_log" 2>/dev/null || true
        return 1
    fi
    if [ ! -s "$speech_mp3" ]; then
        echo -e "${CYAN}        Microsoft Edge TTS 未生成有效音频${RESET}"
        return 1
    fi
    if ! ffmpeg -y -hide_banner -loglevel error \
        -i "$speech_mp3" \
        -f s16le -acodec pcm_s16le -ar "$rate" -ac 1 \
        "$speech_raw"; then
        echo -e "${CYAN}        Microsoft Edge TTS 音频转 PCM 失败${RESET}"
        return 1
    fi

    : > "$seq_raw"
    for ((i = 0; i < AUDIO_LOOPS; i++)); do
        cat "$speech_raw" >> "$seq_raw"
    done
    cp "$seq_raw" "$out_raw"
}

_build_announce_pcm() {
    local rate=$1
    local phrase=$2
    local out_raw=$3

    if [ "$ACTIVE_TTS_ENGINE" = "microsoft" ]; then
        if _build_microsoft_announce_pcm "$rate" "$phrase" "$out_raw"; then
            return
        fi
        echo -e "${CYAN}        自动回退到 espeak-ng，后续音频不再重复联网尝试${RESET}"
        ACTIVE_TTS_ENGINE=espeak
    fi
    _build_espeak_announce_pcm "$rate" "$phrase" "$out_raw"
}

_encode_from_pcm() {
    local rate=$1
    local pcm_raw=$2
    shift 2
    ffmpeg -y -f s16le -ar "$rate" -ac 1 -i "$pcm_raw" "$@" 2>/dev/null
}

PCM8_PCM="$TMP_AUDIO_DIR/pcm8_pcm.raw"
PCM16_PCM="$TMP_AUDIO_DIR/pcm16_pcm.raw"
ALAW8_PCM="$TMP_AUDIO_DIR/alaw8_pcm.raw"
ALAW16_PCM="$TMP_AUDIO_DIR/alaw16_pcm.raw"
OPUS8_PCM="$TMP_AUDIO_DIR/opus8_pcm.raw"
OPUS16_PCM="$TMP_AUDIO_DIR/opus16_pcm.raw"
AMRNB_PCM="$TMP_AUDIO_DIR/amrnb_pcm.raw"
AMRWB_PCM="$TMP_AUDIO_DIR/amrwb_pcm.raw"

echo -e "${GREEN}[audio] PCM 16kHz${RESET}"
_build_announce_pcm 16000 "当前音频格式，P C M，十六千赫兹" "$PCM16_PCM"
cp "$PCM16_PCM" "$ASSETS_DIR/number.pcm_s16le_16khz"
_show_file "$ASSETS_DIR/number.pcm_s16le_16khz"
echo ""

echo -e "${GREEN}[audio] PCM 8kHz${RESET}"
_build_announce_pcm 8000 "当前音频格式，P C M，八千赫兹" "$PCM8_PCM"
cp "$PCM8_PCM" "$ASSETS_DIR/number.pcm_s16le_8khz"
_show_file "$ASSETS_DIR/number.pcm_s16le_8khz"
echo ""

echo -e "${GREEN}[audio] A-law 16kHz${RESET}"
_build_announce_pcm 16000 "当前音频格式，G 七一一 A，十六千赫兹" "$ALAW16_PCM"
_encode_from_pcm 16000 "$ALAW16_PCM" -f alaw "$ASSETS_DIR/number.alaw_16khz"
_show_file "$ASSETS_DIR/number.alaw_16khz"
echo ""

echo -e "${GREEN}[audio] A-law 8kHz${RESET}"
_build_announce_pcm 8000 "当前音频格式，G 七一一 A，八千赫兹" "$ALAW8_PCM"
_encode_from_pcm 8000 "$ALAW8_PCM" -f alaw "$ASSETS_DIR/number.alaw_8khz"
_show_file "$ASSETS_DIR/number.alaw_8khz"
echo ""

echo -e "${GREEN}[audio] Opus 16kHz${RESET}"
_build_announce_pcm 16000 "当前音频格式，Opus，十六千赫兹" "$OPUS16_PCM"
_encode_from_pcm 16000 "$OPUS16_PCM" \
  -c:a libopus -b:a 32k -frame_duration 20 -ar 16000 -ac 1 \
  -f ogg "$ASSETS_DIR/number.opus_16khz"
_show_file "$ASSETS_DIR/number.opus_16khz"
echo ""

echo -e "${GREEN}[audio] Opus 8kHz${RESET}"
_build_announce_pcm 8000 "当前音频格式，Opus，八千赫兹" "$OPUS8_PCM"
_encode_from_pcm 8000 "$OPUS8_PCM" \
  -c:a libopus -b:a 24k -frame_duration 20 -ar 8000 -ac 1 \
  -f ogg "$ASSETS_DIR/number.opus_8khz"
_show_file "$ASSETS_DIR/number.opus_8khz"
echo ""

if $_has_amr_nb; then
    echo -e "${GREEN}[audio] AMR-NB${RESET}"
    _build_announce_pcm 8000 "当前音频格式，A M R，八千赫兹" "$AMRNB_PCM"
    _encode_from_pcm 8000 "$AMRNB_PCM" \
      -c:a libopencore_amrnb -b:a 12.2k -ar 8000 -ac 1 \
      -f amr "$ASSETS_DIR/number.amr_nb"
    _show_file "$ASSETS_DIR/number.amr_nb"
    echo ""
else
    echo -e "${CYAN}[audio] AMR-NB 跳过（当前 ffmpeg 未编入 libopencore_amrnb）${RESET}"
    echo -e "${CYAN}           需安装带该编码器的 ffmpeg；libavcodec-extra 不一定包含它${RESET}"
    echo ""
fi

if $_has_amr_wb; then
    echo -e "${GREEN}[audio] AMR-WB${RESET}"
    _build_announce_pcm 16000 "当前音频格式，A M R，十六千赫兹" "$AMRWB_PCM"
    _encode_from_pcm 16000 "$AMRWB_PCM" \
      -c:a libvo_amrwbenc -ar 16000 -ac 1 \
      -f amr "$ASSETS_DIR/number.amr_wb"
    _show_file "$ASSETS_DIR/number.amr_wb"
    echo ""
else
    echo -e "${CYAN}[audio] AMR-WB 跳过（当前 ffmpeg 未编入 libvo_amrwbenc）${RESET}"
    echo -e "${CYAN}           需安装带该编码器的 ffmpeg；libavcodec-extra 不一定包含它${RESET}"
    echo ""
fi

# ══════════════════════════════════════════════════════════════════════════════
# 视频
#   - H.264：仅 1280x720，10 秒，15fps
#   - MJPEG：240x320 / 320x240 / 640x480 / 480x640，10 秒，8fps
#   - 裸流供模拟器读取；每份裸流另生成一个 MP4 预览文件
# ══════════════════════════════════════════════════════════════════════════════
VIDEO_DURATION=10
H264_FPS=15
MJPEG_FPS=8
H264_FRAME_COUNT=$((H264_FPS * VIDEO_DURATION))
MJPEG_FRAME_COUNT=$((MJPEG_FPS * VIDEO_DURATION))

_video_label_filter() {
    local WIDTH=$1 HEIGHT=$2 CODEC=$3 FPS=$4
    local FONT_SIZE=$((WIDTH / 18))
    [ "$FONT_SIZE" -lt 16 ] && FONT_SIZE=16
    [ "$FONT_SIZE" -gt 48 ] && FONT_SIZE=48
    printf "drawtext=text='%sx%s | %s | %s FPS':fontcolor=white:fontsize=%s:box=1:boxcolor=black@0.75:boxborderw=6:x=(w-text_w)/2:y=8" \
        "$WIDTH" "$HEIGHT" "$CODEC" "$FPS" "$FONT_SIZE"
}

_gen_h264_video() {
    local WIDTH=$1 HEIGHT=$2
    local NAME="video_h264_annexb_${WIDTH}x${HEIGHT}_${H264_FPS}fps_${VIDEO_DURATION}s_${H264_FRAME_COUNT}frames"
    local PREVIEW_NAME="preview_h264_${WIDTH}x${HEIGHT}_${H264_FPS}fps_${VIDEO_DURATION}s_${H264_FRAME_COUNT}frames"
    local LABEL_FILTER
    LABEL_FILTER="$(_video_label_filter "$WIDTH" "$HEIGHT" H264 "$H264_FPS")"
    rm -f "$ASSETS_DIR/${NAME}.h264" "$ASSETS_DIR/${PREVIEW_NAME}.mp4"

    echo -e "${GREEN}[video ${STEP}] ${NAME}${RESET}"
    if ! ffmpeg -y \
         -f lavfi -i "testsrc=duration=$VIDEO_DURATION:size=${WIDTH}x${HEIGHT}:rate=$H264_FPS" \
         -map 0:v:0 \
         -vf "$LABEL_FILTER" \
         -pix_fmt yuv420p -r "$H264_FPS" \
         -c:v libx264 -preset veryfast \
         -x264opts keyint=30:min-keyint=30:no-scenecut -bf 0 \
         -frames:v "$H264_FRAME_COUNT" \
         -an -f h264 \
         "$ASSETS_DIR/${NAME}.h264" >/dev/null 2>&1; then
        echo -e "  ${RED}✗ H264 素材生成失败，请确认 ffmpeg 支持 libx264、drawtext 和 lavfi${RESET}"
        return 1
    fi
    if ! ffmpeg -y -hide_banner -loglevel error \
         -r "$H264_FPS" -f h264 -i "$ASSETS_DIR/${NAME}.h264" \
         -map 0:v:0 -c:v copy -an -movflags +faststart \
         "$ASSETS_DIR/${PREVIEW_NAME}.mp4"; then
        echo -e "  ${RED}✗ H264 MP4 预览文件生成失败${RESET}"
        return 1
    fi
    echo -e "  ${NAME}.h264  $(du -h "$ASSETS_DIR/${NAME}.h264" 2>/dev/null | cut -f1 || echo 'N/A')"
    echo -e "  ${PREVIEW_NAME}.mp4  $(du -h "$ASSETS_DIR/${PREVIEW_NAME}.mp4" 2>/dev/null | cut -f1 || echo 'N/A')"
}

_gen_mjpeg_video() {
    local WIDTH=$1 HEIGHT=$2
    local NAME="video_mjpeg_${WIDTH}x${HEIGHT}_${MJPEG_FPS}fps_${VIDEO_DURATION}s_${MJPEG_FRAME_COUNT}frames"
    local PREVIEW_NAME="preview_mjpeg_${WIDTH}x${HEIGHT}_${MJPEG_FPS}fps_${VIDEO_DURATION}s_${MJPEG_FRAME_COUNT}frames"
    local LABEL_FILTER
    LABEL_FILTER="$(_video_label_filter "$WIDTH" "$HEIGHT" MJPEG "$MJPEG_FPS")"
    rm -f "$ASSETS_DIR/${NAME}.mjpeg" "$ASSETS_DIR/${PREVIEW_NAME}.mp4"

    echo -e "${GREEN}[video ${STEP}] ${NAME} MJPEG ${VIDEO_DURATION}秒/${MJPEG_FPS}fps${RESET}"
    if ! ffmpeg -y \
         -f lavfi -i "testsrc=duration=$VIDEO_DURATION:size=${WIDTH}x${HEIGHT}:rate=$MJPEG_FPS" \
         -map 0:v:0 \
         -vf "$LABEL_FILTER" \
         -c:v mjpeg -q:v 3 \
         -frames:v "$MJPEG_FRAME_COUNT" \
         -f mjpeg \
         "$ASSETS_DIR/${NAME}.mjpeg" >/dev/null 2>&1; then
        echo -e "  ${RED}✗ MJPEG 素材生成失败，请确认 ffmpeg 支持 mjpeg、drawtext 和 lavfi${RESET}"
        return 1
    fi
    if ! ffmpeg -y -hide_banner -loglevel error \
         -r "$MJPEG_FPS" -f mjpeg -i "$ASSETS_DIR/${NAME}.mjpeg" \
         -map 0:v:0 -c:v libx264 -preset veryfast -pix_fmt yuv420p \
         -r "$MJPEG_FPS" -an -movflags +faststart \
         "$ASSETS_DIR/${PREVIEW_NAME}.mp4"; then
        echo -e "  ${RED}✗ MJPEG MP4 预览文件生成失败${RESET}"
        return 1
    fi
    echo -e "  ${NAME}.mjpeg  $(du -h "$ASSETS_DIR/${NAME}.mjpeg" 2>/dev/null | cut -f1 || echo 'N/A')"
    echo -e "  ${PREVIEW_NAME}.mp4  $(du -h "$ASSETS_DIR/${PREVIEW_NAME}.mp4" 2>/dev/null | cut -f1 || echo 'N/A')"
}

STEP=1 _gen_h264_video 1280 720
echo ""
STEP=2 _gen_mjpeg_video 240 320
echo ""
STEP=3 _gen_mjpeg_video 320 240
echo ""
STEP=4 _gen_mjpeg_video 640 480
echo ""
STEP=5 _gen_mjpeg_video 480 640

# ══════════════════════════════════════════════════════════════════════════════
# 验证视频编码参数
# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo -e "${GREEN}验证 H.264 编码参数...${RESET}"
H264_FILE="$ASSETS_DIR/video_h264_annexb_1280x720_${H264_FPS}fps_${VIDEO_DURATION}s_${H264_FRAME_COUNT}frames.h264"
b_frames=$(ffprobe -v error -select_streams v:0 \
    -show_entries frame=pict_type -of default "$H264_FILE" 2>/dev/null | grep -c "pict_type=B" || true)
h264_frames=$(ffprobe -v error -count_frames -select_streams v:0 \
    -show_entries stream=nb_read_frames -of default "$H264_FILE" 2>/dev/null | grep nb_read_frames | cut -d= -f2)
bf=$(echo "$b_frames" | tr -d ' \n')
echo -e "  $(basename "$H264_FILE"): ${h264_frames} 帧, B帧=${bf} $([ "${bf:-0}" -eq 0 ] && echo '✓' || echo '✗')"

echo -e "${GREEN}验证 MJPEG 编码参数...${RESET}"
for resolution in 240x320 320x240 640x480 480x640; do
    MJPEG_FILE="$ASSETS_DIR/video_mjpeg_${resolution}_${MJPEG_FPS}fps_${VIDEO_DURATION}s_${MJPEG_FRAME_COUNT}frames.mjpeg"
    mjpeg_frames=$(ffprobe -v error -count_frames -select_streams v:0 \
        -show_entries stream=nb_read_frames -of default "$MJPEG_FILE" 2>/dev/null | grep nb_read_frames | cut -d= -f2)
    echo -e "  $(basename "$MJPEG_FILE"): ${mjpeg_frames} 帧"
done

echo ""
echo -e "${GREEN}══════════════════════════════════════════${RESET}"
echo -e "${GREEN}  生成完毕${RESET}"
echo -e "${GREEN}══════════════════════════════════════════${RESET}"
ls -lh "$ASSETS_DIR"/number.* "$H264_FILE" \
    "$ASSETS_DIR/preview_h264_1280x720_${H264_FPS}fps_${VIDEO_DURATION}s_${H264_FRAME_COUNT}frames.mp4" \
    "$ASSETS_DIR/video_mjpeg_240x320_${MJPEG_FPS}fps_${VIDEO_DURATION}s_${MJPEG_FRAME_COUNT}frames.mjpeg" \
    "$ASSETS_DIR/video_mjpeg_320x240_${MJPEG_FPS}fps_${VIDEO_DURATION}s_${MJPEG_FRAME_COUNT}frames.mjpeg" \
    "$ASSETS_DIR/video_mjpeg_640x480_${MJPEG_FPS}fps_${VIDEO_DURATION}s_${MJPEG_FRAME_COUNT}frames.mjpeg" \
    "$ASSETS_DIR/video_mjpeg_480x640_${MJPEG_FPS}fps_${VIDEO_DURATION}s_${MJPEG_FRAME_COUNT}frames.mjpeg" \
    "$ASSETS_DIR"/preview_mjpeg_*_"${MJPEG_FPS}fps_${VIDEO_DURATION}s_${MJPEG_FRAME_COUNT}frames.mp4" \
    2>/dev/null
