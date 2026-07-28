#!/usr/bin/env python3
"""
gen_tts.py — 用 Microsoft Edge TTS 生成 PCM 音频片段，供 device-server TTS 接口使用。

依赖：edge-tts, ffmpeg
  pip3 install edge-tts
  apt install ffmpeg

输出到 ../ttsdata/（Go embed 包目录）：
  0.pcm ~ 9.pcm           数字 0-9（中文女声："零"~"九"）
  prefix_zh.pcm            "您的验证码是"（整句，自然连读）
  _silence_200ms.pcm       200ms 静音间隔
  各文件为 PCM 16-bit signed, 8kHz, mono, 无 WAV 头，可直接拼接播放。
"""

import asyncio
import subprocess
import os
import sys

ASSETS_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "ttsdata")

VOICE = "zh-CN-XiaoxiaoNeural"  # 微软中文女声，自然度最高
RATE = 8000                      # 8kHz
SAMPLE_FMT = "s16le"             # 16-bit signed PCM, little-endian

SEGMENTS = {
    "零": "0.pcm",
    "一": "1.pcm",
    "二": "2.pcm",
    "三": "3.pcm",
    "四": "4.pcm",
    "五": "5.pcm",
    "六": "6.pcm",
    "七": "7.pcm",
    "八": "8.pcm",
    "九": "9.pcm",
}

PHRASES = {
    "您的验证码是": "prefix_zh.pcm",
}


async def synthesize(text: str, output_path: str) -> None:
    """用 edge-tts 合成单段语音，转为 PCM 16-bit 8kHz mono raw。"""
    tmp_mp3 = output_path + ".mp3"
    try:
        proc = await asyncio.create_subprocess_exec(
            "edge-tts",
            "--voice", VOICE,
            "--text", text,
            "--write-media", tmp_mp3,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        rc = await proc.wait()
        if rc != 0 or not os.path.exists(tmp_mp3) or os.path.getsize(tmp_mp3) == 0:
            raise RuntimeError(f"edge-tts 合成失败: {text}")

        subprocess.run([
            "ffmpeg", "-y", "-v", "error",
            "-i", tmp_mp3,
            "-f", SAMPLE_FMT,
            "-acodec", "pcm_s16le",
            "-ar", str(RATE),
            "-ac", "1",
            output_path,
        ], check=True)

        size = os.path.getsize(output_path)
        duration_ms = (size / 2 / RATE) * 1000
        print(f"  OK {os.path.basename(output_path)}: {size}B ({duration_ms:.0f}ms) <- \"{text}\"")
    finally:
        if os.path.exists(tmp_mp3):
            os.remove(tmp_mp3)


async def main():
    os.makedirs(ASSETS_DIR, exist_ok=True)

    for cmd in ["edge-tts", "ffmpeg"]:
        try:
            subprocess.run([cmd, "--version"], capture_output=True, check=False)
        except FileNotFoundError:
            print(f"ERROR: missing dependency: {cmd}")
            print("  Install: pip3 install edge-tts && apt install ffmpeg")
            sys.exit(1)

    print(f"Voice: {VOICE}")
    print(f"Format: PCM 16-bit {RATE}Hz mono")
    print()

    print("== Digits ==")
    for text, filename in SEGMENTS.items():
        output_path = os.path.join(ASSETS_DIR, filename)
        if os.path.exists(output_path):
            print(f"  Skip {filename} (exists)")
            continue
        await synthesize(text, output_path)

    print("== Phrases ==")
    for text, filename in PHRASES.items():
        output_path = os.path.join(ASSETS_DIR, filename)
        if os.path.exists(output_path):
            print(f"  Skip {filename} (exists)")
            continue
        await synthesize(text, output_path)

    # 200ms silence for inter-digit gap
    silence_path = os.path.join(ASSETS_DIR, "_silence_200ms.pcm")
    if not os.path.exists(silence_path):
        silence_samples = int(RATE * 0.2)
        silence_data = b'\x00\x00' * silence_samples
        with open(silence_path, 'wb') as f:
            f.write(silence_data)
        print(f"  OK _silence_200ms.pcm: {len(silence_data)}B (200ms)")

    print()
    print("== Output ==")
    total = 0
    for f in sorted(os.listdir(ASSETS_DIR)):
        path = os.path.join(ASSETS_DIR, f)
        size = os.path.getsize(path)
        total += size
        print(f"  {f:30s} {size:>6}B")
    print(f"  {'Total':30s} {total:>6}B ({total/1024:.1f}KB)")

    print()
    print("Done. Files in assets/tts/ are ready for //go:embed.")


if __name__ == "__main__":
    asyncio.run(main())
