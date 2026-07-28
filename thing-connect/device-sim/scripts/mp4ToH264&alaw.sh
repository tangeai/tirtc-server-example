#!/usr/bin/env bash
# 将 mp4 转换为模拟器所需的 video.h264 和 audio.g711a
# 用法: bash convert_media.sh input.mp4 [output_dir]
#
# 要点：
#   - 重新编码（不用 copy），保证 SPS/PPS 在文件头
#   - 禁用 B 帧（-bf 0），避免 DTS/PTS 不一致导致音画不同步
#   - 固定 15fps + keyint=30，与 TiRTC C demo 一致，避免音画不同步
#   - 音频转为 G.711 A-law 8kHz 单声道 raw

set -e

INPUT="${1:?用法: $0 input.mp4 [output_dir]}"
OUTDIR="${2:-assets}"

mkdir -p "$OUTDIR"

echo "[1/2] 提取视频 → $OUTDIR/video.h264"
ffmpeg -y -i "$INPUT" \
  -map 0:v:0 -c:v libx264 \
  -r 15 \
  -x264opts "keyint=30:min-keyint=30:no-scenecut" \
  -bf 0 \
  -vf scale=1280:720 \
  -bsf:v h264_mp4toannexb \
  -an "$OUTDIR/video.h264"

echo "[2/2] 提取音频 → $OUTDIR/audio.g711a"
ffmpeg -y -i "$INPUT" \
  -map 0:a:0 -ar 8000 -ac 1 -acodec pcm_alaw \
  -f alaw "$OUTDIR/audio.g711a"

echo "验证时长对齐..."
python3 - "$OUTDIR" <<'EOF'
import sys, os, subprocess

outdir = sys.argv[1]

audio_bytes = os.path.getsize(f"{outdir}/audio.g711a")
audio_ms = (audio_bytes // 320) * 40

r = subprocess.run(
    ["ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
     "-show_entries", "stream=nb_read_frames", "-of", "default",
     f"{outdir}/video.h264"],
    capture_output=True, text=True
)
frames = int([l for l in r.stdout.splitlines() if "nb_read_frames" in l][0].split("=")[1])
video_ms = int(frames * 1000 / 15)

# 验证无 B 帧
r2 = subprocess.run(
    ["ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
     "-show_entries", "frame=pict_type", "-of", "default",
     f"{outdir}/video.h264"],
    capture_output=True, text=True
)
b_frames = r2.stdout.count("pict_type=B")

print(f"  视频: {frames} 帧 @ 15fps → {video_ms}ms = {video_ms/1000:.2f}s")
print(f"  音频: {audio_bytes} 字节 → {audio_ms}ms = {audio_ms/1000:.2f}s")
print(f"  时长差: {abs(audio_ms - video_ms)}ms")
print(f"  B 帧数: {b_frames} {'✓' if b_frames == 0 else '✗ 警告：存在 B 帧，会导致音画不同步'}")
EOF

echo "完成"
