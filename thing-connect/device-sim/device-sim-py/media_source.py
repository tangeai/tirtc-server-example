#!/usr/bin/env python3
from __future__ import annotations
"""media_source.py — 音视频数据源抽象与文件实现"""

import sys
from abc import ABC, abstractmethod

from media_file_reader import AudioFileReader, VideoFileReader
from media_formats import AUDIO_FORMATS, VIDEO_FORMATS


class MediaSource(ABC):
    def has_video(self) -> bool:
        """是否包含可发送视频。默认认为存在视频。"""
        return True

    @abstractmethod
    def get_audio_format(self):
        """返回上行音频格式描述。"""

    @abstractmethod
    def next_audio_packet(self) -> "tuple[bytes, float] | None":
        """返回 (音频帧数据, 帧时长毫秒)。"""

    @abstractmethod
    def get_video_format(self):
        """返回上行视频格式描述。"""

    @abstractmethod
    def next_audio(self) -> bytes:
        """返回一包音频编码帧。"""

    @abstractmethod
    def next_video(self, force_key: bool = False) -> "tuple[bytes, bool] | None":
        """返回 (帧数据, is_key_frame)。"""

    @abstractmethod
    def close(self) -> None:
        """释放底层资源。"""


DEFAULT_AUDIO_PKT_MS = 40
AUDIO_PKT_MS = DEFAULT_AUDIO_PKT_MS
VIDEO_FRAME_MS = 1000 / 15


class FileMediaSource(MediaSource):
    """从本地文件循环读取已经编码好的音视频帧。"""

    def __init__(self, video_path: str, audio_path: str,
                 audio_format: str = "alaw_8khz",
                 video_format: str = "h264") -> None:
        self._audio_format = AUDIO_FORMATS[audio_format]
        self._video_format = VIDEO_FORMATS[video_format] if video_path else None
        try:
            self._audio_reader = AudioFileReader(audio_path, audio_format, DEFAULT_AUDIO_PKT_MS)
            self._video_reader = VideoFileReader(video_path, video_format) if video_path else None
        except OSError as exc:
            sys.exit(f"[media] 文件打开失败: {exc}")
        except ValueError as exc:
            sys.exit(f"[media] 文件格式无效: {exc}")
        if self._video_reader is not None:
            self._audio_reader.skip_duration(self._video_reader.first_key_index() * VIDEO_FRAME_MS)

    def has_video(self) -> bool:
        return self._video_reader is not None

    def get_audio_format(self):
        return self._audio_format

    def get_video_format(self):
        return self._video_format

    def next_audio(self) -> bytes:
        packet = self.next_audio_packet()
        if packet is None:
            raise RuntimeError("循环音频源不应返回空帧")
        return packet[0]

    def next_audio_packet(self) -> "tuple[bytes, float] | None":
        packet = self._audio_reader.next_packet()
        if packet is None:
            return None
        return packet.payload, packet.duration_ms

    def next_video(self, force_key: bool = False) -> "tuple[bytes, bool] | None":
        if self._video_reader is None:
            return None
        requested_index = self._video_reader.current_index()
        result = self._video_reader.next_frame(force_key=force_key)
        selected_index = self._video_reader.last_frame_index()
        if (force_key and result is not None and selected_index is not None
                and selected_index != requested_index):
            # A pre-encoded file cannot create an IDR at the current media
            # position.  When recovery seeks video to the next IDR, move the
            # looping audio file to the same content position.  Output PTS
            # remain monotonic in the sender; only the file read positions
            # change together.
            self._audio_reader.seek_duration(selected_index * VIDEO_FRAME_MS)
        return result

    def close(self) -> None:
        pass
