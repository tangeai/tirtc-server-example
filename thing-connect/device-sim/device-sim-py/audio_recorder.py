#!/usr/bin/env python3
"""audio_recorder.py — 音频接收器（写文件 + 格式检测 + fmt.json）

rtc_ai / rtc_call_media / rtc_stream 共用。
"""

import json
import os

from media_postprocess import convert_audio_to_wav

# TiRTC 媒体类型 → ffmpeg -f 参数名
MEDIA_TO_FFMPEG_FMT = {
    1: "s16le",   # TIRTC_AUDIO_PCM
    2: "alaw",    # TIRTC_AUDIO_ALAW
    3: "aac",     # TIRTC_AUDIO_AAC
    4: "opus",    # TIRTC_AUDIO_OPUS
    5: "amr",     # TIRTC_AUDIO_AMR
}

# TiRTC flags → 采样率
FLAGS_TO_RATE = {
    0: 8000,    # TIRTC_AUDIOSAMPLE_8K16B1C
    1: 16000,   # TIRTC_AUDIOSAMPLE_16K16B1C
}


class AudioRecorder:
    """接收 TiRTC 音频帧，写入文件并记录格式元数据。"""

    def __init__(self, recv_dir: str, device_id: str = "",
                 filename: str = "received_audio.raw", info=None, warn=None):
        self._dir = recv_dir
        self._device_id = device_id
        self._filename = filename
        self._info = info or (lambda _msg: None)
        self._warn = warn or (lambda _msg: None)
        self._file = None
        self._path = ""
        self._fmt_info = None
        self._frame_count = 0

    # ── 生命周期 ──────────────────────────────────────────────────────────

    def open(self) -> str:
        """创建输出目录和文件，返回完整路径。"""
        out_dir = os.path.join(self._dir, self._device_id) if self._device_id else self._dir
        os.makedirs(out_dir, exist_ok=True)
        path = os.path.join(out_dir, self._filename)
        self._file = open(path, "wb")
        self._path = path
        self._fmt_info = None
        self._frame_count = 0
        return path

    def close(self) -> None:
        """关闭文件并写入 fmt.json。"""
        if self._file is None:
            return
        self._file.close()
        self._file = None

        if self._fmt_info:
            out_dir = os.path.join(self._dir, self._device_id) if self._device_id else self._dir
            base, _ = os.path.splitext(self._filename)
            fmt_path = os.path.join(out_dir, f"{base}.fmt.json")
            try:
                with open(fmt_path, "w") as f:
                    json.dump(self._fmt_info, f)
            except OSError:
                pass
            convert_audio_to_wav(self._path, self._fmt_info, self._info, self._warn)

    # ── 写入 ──────────────────────────────────────────────────────────────

    def write_frame(self, fi, buf: bytes) -> None:
        """写入一帧音频数据。

        fi: TIRTCFRAMEINFO (ctypes struct with .media, .flags, .stream_id, .length)
        buf: raw audio bytes
        """
        if self._file is None:
            return
        self._file.write(buf)
        self._frame_count += 1

        if self._fmt_info is None:
            enc = MEDIA_TO_FFMPEG_FMT.get(fi.media, "alaw")
            rate = FLAGS_TO_RATE.get(fi.flags, 8000)
            self._fmt_info = {
                "stream_id": fi.stream_id,
                "media": fi.media,
                "flags": fi.flags,
                "encoding": enc,
                "sample_rate": rate,
            }

    # ── 查询 ──────────────────────────────────────────────────────────────

    @property
    def is_open(self) -> bool:
        return self._file is not None

    @property
    def frame_count(self) -> int:
        return self._frame_count

    @property
    def fmt_info(self) -> dict | None:
        return self._fmt_info
