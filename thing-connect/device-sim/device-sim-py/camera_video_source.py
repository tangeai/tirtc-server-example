#!/usr/bin/env python3
from __future__ import annotations
"""Windows 摄像头采集与实时 H.264 Annex-B 编码。"""

from collections import deque
from fractions import Fraction
import threading
import time

from media_file_reader import VideoFileReader


CAMERA_URI_PREFIX = "camera://"
CAMERA_WIDTH = 1280
CAMERA_HEIGHT = 720
CAMERA_FPS = 15
CAMERA_BIT_RATE = 2_000_000
CAMERA_FIRST_FRAME_TIMEOUT_SEC = 5.0


def camera_source_uri(camera_index: int = 0) -> str:
    if camera_index < 0:
        raise ValueError("摄像头编号不能小于 0")
    return f"{CAMERA_URI_PREFIX}{camera_index}"


def is_camera_source(source: str) -> bool:
    return bool(source) and source.startswith(CAMERA_URI_PREFIX)


def camera_index_from_uri(source: str) -> int:
    if not is_camera_source(source):
        raise ValueError(f"不是摄像头视频源: {source}")
    value = source[len(CAMERA_URI_PREFIX):]
    try:
        camera_index = int(value)
    except ValueError as exc:
        raise ValueError(f"无效的摄像头视频源: {source}") from exc
    if camera_index < 0:
        raise ValueError(f"无效的摄像头视频源: {source}")
    return camera_index


def describe_video_source(source: str, video_format: str = "h264") -> str:
    if is_camera_source(source):
        camera_index = camera_index_from_uri(source)
        return (
            f"摄像头[{camera_index}] "
            f"{CAMERA_WIDTH}x{CAMERA_HEIGHT} {CAMERA_FPS}fps H.264"
        )
    return f"{source or '未启用'} ({video_format})"


def open_video_source(source: str, video_format: str):
    """打开文件或摄像头编码视频源，两者均提供 next_frame()."""
    if is_camera_source(source):
        if video_format != "h264":
            raise ValueError("摄像头视频源仅支持 h264")
        return CameraVideoSource(camera_index_from_uri(source))
    return VideoFileReader(source, video_format)


def close_video_source(source) -> None:
    close = getattr(source, "close", None)
    if callable(close):
        close()


def validate_camera_source(camera_index: int) -> None:
    """启动前验证摄像头可采集且本机 PyAV 可编码 H.264。"""
    source = CameraVideoSource(camera_index)
    try:
        frame = source.next_frame(force_key=True)
        if not frame[0] or not frame[1]:
            raise RuntimeError("摄像头首帧不是有效的 H.264 关键帧")
    finally:
        source.close()


class CameraVideoSource:
    """采集 Windows 摄像头并输出 720P/15fps H.264 Annex-B 帧。"""

    def __init__(self, camera_index: int = 0, *, cv2_module=None,
                 av_module=None) -> None:
        if camera_index < 0:
            raise ValueError("摄像头编号不能小于 0")
        if cv2_module is None:
            import cv2 as cv2_module
        if av_module is None:
            import av as av_module

        self.camera_index = camera_index
        self._cv2 = cv2_module
        self._av = av_module
        self._capture = None
        self._codec = None
        self._capture_thread = None
        self._stop_event = threading.Event()
        self._frame_ready = threading.Condition()
        self._latest_frame = None
        self._capture_error: "BaseException | None" = None
        self._pending_packets = deque()
        self._frame_index = 0
        try:
            self._capture = self._open_capture()
            self._codec = self._open_encoder()
            self._capture_thread = threading.Thread(
                target=self._capture_loop,
                daemon=True,
                name=f"camera-capture-{camera_index}",
            )
            self._capture_thread.start()
            self._wait_for_first_frame()
        except BaseException:
            self.close()
            raise

    def _open_capture(self):
        cv2 = self._cv2
        backend = getattr(cv2, "CAP_DSHOW", None)
        capture = (
            cv2.VideoCapture(self.camera_index, backend)
            if backend is not None
            else cv2.VideoCapture(self.camera_index)
        )
        if not capture.isOpened():
            capture.release()
            # 部分 Windows 摄像头只在 OpenCV 默认的 MSMF 后端可用。
            capture = cv2.VideoCapture(self.camera_index)
        if not capture.isOpened():
            capture.release()
            raise RuntimeError(
                f"无法打开摄像头 {self.camera_index}；请检查设备编号和系统相机权限"
            )

        fourcc = getattr(cv2, "VideoWriter_fourcc", None)
        if callable(fourcc):
            capture.set(cv2.CAP_PROP_FOURCC, fourcc(*"MJPG"))
        capture.set(cv2.CAP_PROP_FRAME_WIDTH, CAMERA_WIDTH)
        capture.set(cv2.CAP_PROP_FRAME_HEIGHT, CAMERA_HEIGHT)
        capture.set(cv2.CAP_PROP_FPS, CAMERA_FPS)
        buffer_size = getattr(cv2, "CAP_PROP_BUFFERSIZE", None)
        if buffer_size is not None:
            capture.set(buffer_size, 1)
        return capture

    def _open_encoder(self):
        try:
            codec = self._av.CodecContext.create("libx264", "w")
            codec.width = CAMERA_WIDTH
            codec.height = CAMERA_HEIGHT
            codec.pix_fmt = "yuv420p"
            codec.time_base = Fraction(1, CAMERA_FPS)
            codec.framerate = Fraction(CAMERA_FPS, 1)
            codec.bit_rate = CAMERA_BIT_RATE
            codec.gop_size = CAMERA_FPS * 2
            codec.max_b_frames = 0
            codec.options = {
                "preset": "veryfast",
                "tune": "zerolatency",
                "x264-params": (
                    "keyint=30:min-keyint=15:scenecut=0:"
                    "repeat-headers=1:annexb=1:aud=1"
                ),
            }
            codec.open()
            return codec
        except Exception as exc:
            raise RuntimeError(
                "无法创建 H.264 编码器；请安装 requirements-camera.txt 中的 PyAV"
            ) from exc

    def _capture_loop(self) -> None:
        failures = 0
        capture = self._capture
        try:
            while not self._stop_event.is_set():
                ok, frame = capture.read()
                if not ok or frame is None:
                    failures += 1
                    if failures >= 30:
                        raise RuntimeError(
                            f"摄像头 {self.camera_index} 连续采集失败"
                        )
                    time.sleep(0.02)
                    continue
                failures = 0
                with self._frame_ready:
                    self._latest_frame = frame
                    self._frame_ready.notify_all()
        except Exception as exc:
            with self._frame_ready:
                self._capture_error = exc
                self._frame_ready.notify_all()

    def _wait_for_first_frame(self) -> None:
        deadline = time.monotonic() + CAMERA_FIRST_FRAME_TIMEOUT_SEC
        with self._frame_ready:
            while self._latest_frame is None and self._capture_error is None:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    break
                self._frame_ready.wait(remaining)
            if self._capture_error is not None:
                raise RuntimeError(str(self._capture_error)) from self._capture_error
            if self._latest_frame is None:
                raise RuntimeError(
                    f"等待摄像头 {self.camera_index} 首帧超时；请检查系统相机权限"
                )

    def next_frame(self, force_key: bool = False) -> tuple[bytes, bool]:
        if self._stop_event.is_set():
            raise RuntimeError("摄像头视频源已关闭")
        if force_key:
            self._pending_packets.clear()
        elif self._pending_packets:
            return self._pending_packets.popleft()

        while not self._pending_packets:
            with self._frame_ready:
                if self._capture_error is not None:
                    raise RuntimeError(str(self._capture_error)) from self._capture_error
                if self._latest_frame is None:
                    raise RuntimeError("摄像头尚未产生视频帧")
                image = self._latest_frame.copy()

            if image.shape[1] != CAMERA_WIDTH or image.shape[0] != CAMERA_HEIGHT:
                image = self._cv2.resize(
                    image,
                    (CAMERA_WIDTH, CAMERA_HEIGHT),
                    interpolation=getattr(self._cv2, "INTER_LINEAR", 1),
                )
            frame = self._av.VideoFrame.from_ndarray(image, format="bgr24")
            frame.pts = self._frame_index
            frame.time_base = Fraction(1, CAMERA_FPS)
            if force_key:
                frame.pict_type = self._av.video.frame.PictureType.I
            self._frame_index += 1
            for packet in self._codec.encode(frame):
                self._pending_packets.append(
                    (_as_annexb(bytes(packet)), bool(packet.is_keyframe))
                )
            # zerolatency + 禁用 B 帧通常每次立即产出一帧；若编码器仍缓存，
            # 继续送入最新画面，直至拿到首个 packet。
            force_key = False
        return self._pending_packets.popleft()

    def close(self) -> None:
        if self._stop_event.is_set():
            return
        self._stop_event.set()
        capture, self._capture = self._capture, None
        if capture is not None:
            capture.release()
        thread, self._capture_thread = self._capture_thread, None
        if (thread is not None and thread.is_alive()
                and thread is not threading.current_thread()):
            thread.join(timeout=2.0)
            if thread.is_alive():
                # release() normally unblocks VideoCapture.read().  Do not
                # leave a capture worker behind while its codec is released.
                thread.join()
        codec, self._codec = self._codec, None
        if codec is not None:
            # CodecContext.close() was removed in PyAV 14.  Older releases
            # expose it, while newer releases release the context on drop.
            close_codec = getattr(codec, "close", None)
            if callable(close_codec):
                close_codec()
        with self._frame_ready:
            self._frame_ready.notify_all()


def _as_annexb(data: bytes) -> bytes:
    """保留 Annex-B，或将常见的 4 字节长度前缀 NAL 转为 Annex-B。"""
    if data.startswith((b"\x00\x00\x01", b"\x00\x00\x00\x01")):
        return data
    output = bytearray()
    offset = 0
    while offset + 4 <= len(data):
        nal_size = int.from_bytes(data[offset:offset + 4], "big")
        offset += 4
        if nal_size <= 0 or offset + nal_size > len(data):
            break
        output.extend(b"\x00\x00\x00\x01")
        output.extend(data[offset:offset + nal_size])
        offset += nal_size
    if output and offset == len(data):
        return bytes(output)
    raise RuntimeError("H.264 编码器没有输出有效的 Annex-B 数据")
