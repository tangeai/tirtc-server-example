#!/usr/bin/env python3
"""接收媒体收尾处理：原始文件转可播放 WAV / MP4。"""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile


def _noop(_msg: str) -> None:
    pass


def _ffmpeg_exists() -> bool:
    return shutil.which("ffmpeg") is not None


def convert_audio_to_wav(raw_path: str, fmt_info: dict | None,
                         info=None, warn=None) -> str | None:
    info = info or _noop
    warn = warn or _noop
    if not raw_path or not fmt_info or not os.path.exists(raw_path):
        return None
    if os.path.getsize(raw_path) == 0:
        return None
    if not _ffmpeg_exists():
        warn("未找到 ffmpeg，跳过接收音频 WAV 转换")
        return None

    encoding = fmt_info.get("encoding", "")
    sample_rate = int(fmt_info.get("sample_rate", 8000) or 8000)
    wav_path = os.path.splitext(raw_path)[0] + ".wav"
    temp_input = None

    try:
        if encoding == "s16le":
            command = ["ffmpeg", "-y", "-f", "s16le", "-ar", str(sample_rate), "-ac", "1",
                       "-i", raw_path, "-acodec", "pcm_s16le", wav_path]
        elif encoding == "alaw":
            command = ["ffmpeg", "-y", "-f", "alaw", "-ar", str(sample_rate), "-ac", "1",
                       "-i", raw_path, "-acodec", "pcm_s16le", wav_path]
        elif encoding == "aac":
            command = ["ffmpeg", "-y", "-f", "aac", "-i", raw_path,
                       "-acodec", "pcm_s16le", wav_path]
        elif encoding == "amr":
            temp_input = _wrap_amr_input(raw_path, sample_rate)
            command = ["ffmpeg", "-y", "-i", temp_input,
                       "-acodec", "pcm_s16le", wav_path]
        elif encoding == "opus":
            command = ["ffmpeg", "-y", "-f", "opus", "-ar", str(sample_rate), "-ac", "1",
                       "-i", raw_path, "-acodec", "pcm_s16le", wav_path]
        else:
            warn(f"接收音频编码 {encoding or '?'} 暂不支持自动转 WAV")
            return None
        return _run_ffmpeg(command, wav_path, info, warn, f"接收音频已转为可播放 WAV: {wav_path}")
    finally:
        if temp_input and os.path.exists(temp_input):
            try:
                os.remove(temp_input)
            except OSError:
                pass


def convert_video_to_mp4(video_path: str, video_format: str,
                         info=None, warn=None) -> str | None:
    info = info or _noop
    warn = warn or _noop
    if not video_path or not video_format or not os.path.exists(video_path):
        return None
    if os.path.getsize(video_path) == 0:
        return None
    if not _ffmpeg_exists():
        warn("未找到 ffmpeg，跳过接收视频 MP4 转换")
        return None

    mp4_path = os.path.splitext(video_path)[0] + ".mp4"
    video_format = video_format.lower()
    if video_format == "h264":
        command = ["ffmpeg", "-y", "-framerate", "15", "-f", "h264",
                   "-i", video_path, "-c:v", "copy", mp4_path]
    elif video_format == "h265":
        command = ["ffmpeg", "-y", "-framerate", "15", "-f", "hevc",
                   "-i", video_path, "-c:v", "copy", "-tag:v", "hvc1", mp4_path]
    elif video_format == "mjpeg":
        command = ["ffmpeg", "-y", "-framerate", "15", "-f", "mjpeg",
                   "-i", video_path, "-c:v", "libx264", "-pix_fmt", "yuv420p", mp4_path]
    else:
        warn(f"接收视频格式 {video_format} 暂不支持自动转 MP4")
        return None
    return _run_ffmpeg(command, mp4_path, info, warn, f"接收视频已转为可播放 MP4: {mp4_path}")


def _run_ffmpeg(command: list[str], out_path: str, info, warn, success_msg: str) -> str | None:
    try:
        result = subprocess.run(command, capture_output=True, text=True, check=False)
    except OSError as exc:
        warn(f"执行 ffmpeg 失败: {exc}")
        return None
    if result.returncode != 0:
        lines = (result.stderr or result.stdout or "").strip().splitlines()
        warn(f"ffmpeg 转换失败: {lines[-1] if lines else '未知错误'}")
        return None
    if not os.path.exists(out_path):
        warn(f"ffmpeg 未生成输出文件: {out_path}")
        return None
    info(success_msg)
    return out_path


def _wrap_amr_input(raw_path: str, sample_rate: int) -> str:
    header = b"#!AMR-WB\n" if sample_rate >= 16000 else b"#!AMR\n"
    suffix = ".amrwb" if sample_rate >= 16000 else ".amr"
    with open(raw_path, "rb") as src, tempfile.NamedTemporaryFile(delete=False, suffix=suffix) as dst:
        dst.write(header)
        dst.write(src.read())
        return dst.name
