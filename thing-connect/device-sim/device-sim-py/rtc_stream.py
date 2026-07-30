#!/usr/bin/env python3
from __future__ import annotations
"""rtc_stream.py — TiRTC 音视频推流（被动接受连接模式）

接口约定：
  runtime_callbacks()
  start_service(media_factory)
  stop_service()
  is_active() -> bool
  get_state() -> str   # "IDLE" | "IN_CALL"

对讲功能（H5 按住说话 → stream 14 → 设备端接收并保存文件）：
  configure_talkback(enabled=True, recv_dir="./received", device_id="xxx")
  在 start_service() 之前调用，on_audio 回调中检查 stream_id==14 时写入 received_audio.raw。
"""

import ctypes
import os
import sys
import threading
import time

from typing import Callable
from callback_work_queue import CallbackWorkQueue
from media_source import MediaSource, VIDEO_FRAME_MS
from sdk_callback_guard import SdkCallbackGuard, join_worker_before_uninit
import tirtc_sdk as sdk
from tirtc_sdk import (
    TIRTCFRAMEINFO, TIRTCCALLBACKS,
    OnEventCB, OnConnAcceptCB, OnConnErrCB, OnDisconnCB,
    OnAudioCB, OnVideoCB, OnMsgCB, OnCmdCB, OnKeyFrameCB,
    OnSubVideoCB, OnUnsubVideoCB, OnSubAudioCB, OnUnsubAudioCB,
    TIRTC_EVENT_SYS_STARTED, TIRTC_EVENT_SYS_STOPPED,
    TIRTC_OPT_SERVICE_ENDPOINT, TIRTC_OPT_MAX_SEND_BUFFER,
    TIRTC_OPT_DEVICE_SECRET_KEY,
    AUDIO_STREAM_ID, VIDEO_STREAM_ID,
    TIRTC_FRAME_FLAG_KEY_FRAME,
    CONN_FATAL_ERRORS,
    TIRTC_E_BUSY, TIRTC_E_INVALID_HANDLE, TIRTC_E_CONN_CLOSED,
)

# H5 对讲 stream_id（与 Web SDK TiRtcAudioInput streamId 一致）
TALKBACK_STREAM_ID = 14

# ── 模块状态 ──────────────────────────────────────────────────────────────────
_state_lock   = threading.Lock()
_stop_event   = threading.Event()
_active_conn  = None           # tirtc_conn_t (ctypes c_void_p value)
_active_thread: threading.Thread | None = None
_force_key_frame = threading.Event()
_media_factory: "Callable[[], MediaSource] | None" = None

# 保持对回调对象的引用，防止 GC
_cbs_ref: TIRTCCALLBACKS | None = None
_service_active = False
_callback_guard = SdkCallbackGuard()

# 对讲录音
from audio_recorder import AudioRecorder
_talkback_recorder: AudioRecorder | None = None
_talkback_work: CallbackWorkQueue | None = None

_LOG_LEVEL = 10  # debug=10 info=20 warn=30 error=40

def set_log_level(level: str) -> None:
    global _LOG_LEVEL
    _LOG_LEVEL = {"debug": 10, "info": 20, "warn": 30, "error": 40}.get(level.lower(), 10)


def configure_talkback(enabled: bool = False, recv_dir: str = "", device_id: str = "") -> None:
    """启用/禁用 H5 对讲录音。在 start() 之前调用。"""
    global _talkback_recorder
    if enabled:
        _talkback_recorder = AudioRecorder(
            recv_dir, device_id, "received_audio.raw", _info, _warn)
        _info(f"对讲录音已启用: stream={TALKBACK_STREAM_ID} dir={recv_dir} device={device_id}")
    else:
        _talkback_recorder = None


def _open_talkback_file() -> None:
    if _talkback_recorder is None:
        return
    path = _talkback_recorder.open()
    _info(f"对讲录音文件已创建: {path}")


def _close_talkback_file() -> None:
    if _talkback_work is not None:
        _talkback_work.drain()
    if _talkback_recorder is None or not _talkback_recorder.is_open:
        return
    _talkback_recorder.close()
    _info(f"对讲录音已保存，共 {_talkback_recorder.frame_count} 帧")

import datetime as _dt

def _ts() -> str:
    return _dt.datetime.now().strftime("%H:%M:%S.%f")[:-3]

def _log(msg):
    if _LOG_LEVEL <= 10:
        print(f"{_ts()} \033[0;36m[rtc_stream]\033[0m {msg}", flush=True)
def _info(msg):
    if _LOG_LEVEL <= 20:
        print(f"{_ts()} \033[0;32m[rtc_stream]\033[0m {msg}", flush=True)
def _warn(msg):
    if _LOG_LEVEL <= 30:
        print(f"{_ts()} \033[1;33m[rtc_stream]\033[0m {msg}", flush=True)
def _err(msg):
    if _LOG_LEVEL <= 40:
        print(f"{_ts()} \033[0;31m[rtc_stream]\033[0m {msg}", file=sys.stderr, flush=True)

def _sdk_err(fn_name, rc):
    _err(f"{fn_name} failed: rc={rc} ({sdk.TiRtcGetErrorStr(rc).decode()})")


def _schedule_disconnect_after_callback(hconn_val: int) -> None:
    """避免从 SDK 回调内部重入 Disconnect。"""
    def disconnect():
        if not _service_active:
            return
        _close_talkback_file()
        rc = sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
        if rc != 0:
            _sdk_err("TiRtcDisconnect", rc)

    _callback_guard.defer(
        disconnect, name="stream-callback-disconnect")


def _activate_connection_after_callback(hconn_val: int) -> None:
    """Replace the active stream connection outside the SDK callback stack."""
    global _active_conn, _active_thread

    if not _service_active or _media_factory is None:
        sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
        return
    try:
        source = _media_factory()
    except BaseException as exc:
        _err(f"创建媒体源失败 hconn={hconn_val:#x}: {exc}")
        sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
        return

    with _state_lock:
        if not _service_active:
            old_conn = None
            old_thread = None
            install = False
        elif _active_conn == hconn_val:
            old_conn = None
            old_thread = None
            install = False
        else:
            old_conn, _active_conn = _active_conn, None
            old_thread, _active_thread = _active_thread, None
            _stop_event.set()
            install = True

    if not install:
        source.close()
        if not _service_active:
            sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
        return
    if old_conn is not None:
        sdk.TiRtcDisconnect(ctypes.c_void_p(old_conn))
    join_worker_before_uninit(old_thread, _warn, "旧实时流")
    _close_talkback_file()

    with _state_lock:
        if not _service_active:
            install = False
        else:
            _stop_event.clear()
            _active_conn = hconn_val
            thread = threading.Thread(
                target=_stream_worker,
                args=(hconn_val, source),
                daemon=True,
                name="tirtc-stream",
            )
            _active_thread = thread
            install = True
    if not install:
        source.close()
        sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
        return

    _open_talkback_file()
    thread.start()
    _info(f"实时流连接已建立 hconn={hconn_val:#x}")


def runtime_callbacks() -> TIRTCCALLBACKS:
    global _cbs_ref
    if _cbs_ref is None:
        _cbs_ref = _build_callbacks()
    return _cbs_ref


def callback_guard() -> SdkCallbackGuard:
    return _callback_guard


def start_service(
    media_factory: "Callable[[], MediaSource]",
) -> None:
    global _media_factory, _service_active, _talkback_work

    if _service_active:
        return

    _media_factory = media_factory
    if _talkback_work is None:
        _talkback_work = CallbackWorkQueue(
            "stream-talkback",
            _process_talkback_item,
            _warn,
        )
    _talkback_work.start()
    _stop_event.clear()
    _service_active = True
    _info("实时流业务已就绪，等待客户端连接")


def stop_service() -> None:
    global _service_active, _active_conn, _active_thread, _talkback_work
    if not _service_active:
        return
    _service_active = False
    with _state_lock:
        _stop_event.set()
        active_conn_local, _active_conn = _active_conn, None
        _active_thread_local = _active_thread
        _active_thread = None

    if active_conn_local is not None:
        sdk.TiRtcDisconnect(ctypes.c_void_p(active_conn_local))

    if _active_thread_local is not None:
        _active_thread_local.join(timeout=8.0)

    _callback_guard.wait_for_all()
    _close_talkback_file()
    if _talkback_work is not None:
        _talkback_work.stop()
    if _active_thread_local is not None and _active_thread_local.is_alive():
        join_worker_before_uninit(
            _active_thread_local, _warn, "实时音视频推流", timeout=3.0)
    _info("实时流业务已停止")


def is_active() -> bool:
    return _service_active


def get_state() -> str:
    with _state_lock:
        return "IN_CALL" if _active_conn is not None else "IDLE"


# ── 推流线程 ──────────────────────────────────────────────────────────────────
def _now_ms() -> int:
    return int(time.monotonic() * 1000)



def _stream_worker(hconn_val: int, source: MediaSource) -> None:
    hconn = ctypes.c_void_p(hconn_val)
    _log(f"推流线程启动 hconn={hconn_val:#x}")

    has_video = source.has_video()
    audio_pts_ms  = 0
    video_pts_ms  = 0.0 if has_video else float("inf")
    first_video   = True
    wall_start_ms = _now_ms()
    consec_fail   = 0

    def _send_audio() -> bool:
        nonlocal audio_pts_ms, consec_fail
        packet = source.next_audio_packet()
        if packet is None:
            return False
        pkt, duration_ms = packet
        audio_format = source.get_audio_format()

        fi = TIRTCFRAMEINFO()
        fi.stream_id = AUDIO_STREAM_ID
        fi.media     = audio_format.media
        fi.flags     = audio_format.flags
        fi.reserved  = 0
        fi.ts        = int(audio_pts_ms) & 0xFFFFFFFF
        fi.length    = len(pkt)

        buf = (ctypes.c_uint8 * len(pkt)).from_buffer_copy(pkt)
        rc = sdk.TiRtcSendAudioStream(hconn, ctypes.byref(fi), buf)
        if rc in CONN_FATAL_ERRORS:
            return False
        elif rc < 0 and rc not in (TIRTC_E_BUSY, TIRTC_E_INVALID_HANDLE, TIRTC_E_CONN_CLOSED):
            _err(f"SendAudioStream rc={rc}: {sdk.TiRtcGetErrorStr(rc).decode()}")
            consec_fail += 1
        elif rc < 0 and rc == TIRTC_E_INVALID_HANDLE:
            time.sleep(0.005)
        elif rc >= 0:
            consec_fail = max(0, consec_fail - 1)
        audio_pts_ms += duration_ms
        return True

    def _send_video() -> bool:
        nonlocal video_pts_ms, first_video, consec_fail
        force_key = first_video or _force_key_frame.is_set()
        _force_key_frame.clear()

        result = source.next_video(force_key=force_key)
        if result is None:
            _err("视频源无法读取帧，停止推流")
            return False
        frame_data, is_key = result
        video_format = source.get_video_format()

        fi = TIRTCFRAMEINFO()
        fi.stream_id = VIDEO_STREAM_ID
        fi.media     = video_format.media
        fi.flags     = TIRTC_FRAME_FLAG_KEY_FRAME if is_key else 0
        fi.reserved  = 0
        fi.ts        = int(video_pts_ms) & 0xFFFFFFFF
        fi.length    = len(frame_data)

        buf = (ctypes.c_uint8 * len(frame_data)).from_buffer_copy(frame_data)
        rc = sdk.TiRtcSendVideoStream(hconn, ctypes.byref(fi), buf)
        if rc in CONN_FATAL_ERRORS:
            return False
        elif rc < 0 and rc not in (TIRTC_E_BUSY, TIRTC_E_INVALID_HANDLE, TIRTC_E_CONN_CLOSED):
            _err(f"SendVideoStream rc={rc}: {sdk.TiRtcGetErrorStr(rc).decode()}")
            consec_fail += 1
        elif rc < 0 and rc == TIRTC_E_INVALID_HANDLE:
            time.sleep(0.005)
        elif rc >= 0:
            first_video = False
            consec_fail = max(0, consec_fail - 1)
        video_pts_ms += VIDEO_FRAME_MS
        return True

    try:
        while not _stop_event.is_set():
            if consec_fail >= 3:
                _err("连续 3 次发送失败，断开连接")
                sdk.TiRtcDisconnect(hconn)
                break

            # 纯音频模式只按音频时钟推进；有视频时按最早的音视频 pts 推进。
            target_pts = audio_pts_ms if not has_video else min(audio_pts_ms, video_pts_ms)
            elapsed    = _now_ms() - wall_start_ms
            wait_ms    = target_pts - elapsed
            if wait_ms > 2:
                time.sleep(wait_ms / 1000.0)
                continue

            if not has_video or audio_pts_ms <= video_pts_ms:
                if not _send_audio():
                    break
            else:
                if not _send_video():
                    break
    finally:
        source.close()
        _log(f"推流线程退出 hconn={hconn_val:#x}")


def _process_talkback_item(item) -> None:
    frame, buf = item
    recorder = _talkback_recorder
    if recorder is None or not recorder.is_open:
        return
    recorder.write_frame(frame, buf)
    if recorder.frame_count == 1:
        _info(
            f"H5 对讲音频流检测到: stream={frame.stream_id} "
            f"media={frame.media} {frame.length}bytes/帧"
        )


# ── SDK 回调 ──────────────────────────────────────────────────────────────────
def _build_callbacks() -> TIRTCCALLBACKS:

    def on_conn_accepted(hconn):
        _log(f"on_conn_accepted: 连接已接受 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x}")
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        _callback_guard.defer(
            _activate_connection_after_callback,
            hconn_val,
            name="stream-accept",
        )


    def on_conn_error(hconn, error):
        _log(f"on_conn_error: 连接错误 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} error={error}")
        global _active_conn
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        _err(f"连接错误 hconn={hconn_val:#x}: {sdk.TiRtcGetErrorStr(error).decode()}")
        with _state_lock:
            if _active_conn == hconn_val:
                _active_conn = None
        _schedule_disconnect_after_callback(hconn_val)

    def on_disconnected(hconn):
        _log(f"on_disconnected: 连接已断开 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x}")
        global _active_conn
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            if _active_conn == hconn_val:
                _active_conn = None
        _callback_guard.defer(
            _close_talkback_file,
            name="stream-talkback-close",
        )

    def on_audio(hconn, pFi, data):
        if not data or _talkback_recorder is None or not _talkback_recorder.is_open:
            return
        try:
            fi = ctypes.cast(pFi, ctypes.POINTER(TIRTCFRAMEINFO)).contents
        except Exception:
            return
        if fi.stream_id != TALKBACK_STREAM_ID:
            return
        buf = ctypes.string_at(data, fi.length)
        frame = TIRTCFRAMEINFO()
        frame.stream_id = fi.stream_id
        frame.media = fi.media
        frame.flags = fi.flags
        frame.reserved = fi.reserved
        frame.ts = fi.ts
        frame.length = len(buf)
        if _talkback_work is not None:
            _talkback_work.submit((frame, buf))

    def on_video(hconn, pFi, data):
        pass

    def on_message(hconn, pFi, data):
        pass

    def on_command(hconn, cmdw, data, length):
        pass

    def on_request_key_frame(hconn, stream_id):
        _log(f"on_request_key_frame: 收到关键帧请求 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} stream_id={stream_id}")
        if stream_id == VIDEO_STREAM_ID:
            _force_key_frame.set()

    def on_subscribe_video(hconn, stream_id):
        _log(f"on_subscribe_video: 视频订阅 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} stream_id={stream_id}")
        return 0

    def on_unsubscribe_video(hconn, stream_id):
        _log(f"on_unsubscribe_video: 视频取消订阅 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} stream_id={stream_id}")

    def on_subscribe_audio(hconn, stream_id):
        _log(f"on_subscribe_audio: 音频订阅 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} stream_id={stream_id}")
        return 0

    def on_unsubscribe_audio(hconn, stream_id):
        _log(f"on_unsubscribe_audio: 音频取消订阅 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} stream_id={stream_id}")

    cbs = TIRTCCALLBACKS()
    cbs.on_conn_accepted     = OnConnAcceptCB(_callback_guard.wrap(on_conn_accepted))
    cbs.on_conn_error        = OnConnErrCB(_callback_guard.wrap(on_conn_error))
    cbs.on_disconnected      = OnDisconnCB(_callback_guard.wrap(on_disconnected))
    cbs.on_audio             = OnAudioCB(_callback_guard.wrap(on_audio))
    cbs.on_video             = OnVideoCB(_callback_guard.wrap(on_video))
    cbs.on_message           = OnMsgCB(_callback_guard.wrap(on_message))
    cbs.on_command           = OnCmdCB(_callback_guard.wrap(on_command))
    cbs.on_request_key_frame = OnKeyFrameCB(_callback_guard.wrap(on_request_key_frame))
    cbs.on_subscribe_video   = OnSubVideoCB(_callback_guard.wrap(on_subscribe_video))
    cbs.on_unsubscribe_video = OnUnsubVideoCB(_callback_guard.wrap(on_unsubscribe_video))
    cbs.on_subscribe_audio   = OnSubAudioCB(_callback_guard.wrap(on_subscribe_audio))
    cbs.on_unsubscribe_audio = OnUnsubAudioCB(_callback_guard.wrap(on_unsubscribe_audio))
    cbs._cb_refs = [
        cbs.on_event, cbs.on_conn_accepted, cbs.on_conn_error,
        cbs.on_disconnected, cbs.on_audio, cbs.on_video,
        cbs.on_message, cbs.on_command, cbs.on_request_key_frame,
        cbs.on_subscribe_video, cbs.on_unsubscribe_video,
        cbs.on_subscribe_audio, cbs.on_unsubscribe_audio,
    ]
    return cbs
