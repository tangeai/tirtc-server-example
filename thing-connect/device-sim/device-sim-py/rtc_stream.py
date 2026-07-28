#!/usr/bin/env python3
from __future__ import annotations
"""rtc_stream.py — TiRTC 音视频推流（被动接受连接模式）

接口约定（见 tirtc_sdk.py 顶部注释）：
  start(device_id, secret_key, media_factory, endpoint=None)
  stop()
  is_active() -> bool
  get_state() -> str   # "IDLE" | "IN_CALL"

对讲功能（H5 按住说话 → stream 14 → 设备端接收并保存文件）：
  configure_talkback(enabled=True, recv_dir="./received", device_id="xxx")
  在 start() 之前调用，on_audio 回调中检查 stream_id==14 时写入 received_audio.raw。
"""

import ctypes
import os
import sys
import threading
import time

from typing import Callable
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
_sdk_started  = threading.Event()
_sdk_stopped  = threading.Event()
_stop_event   = threading.Event()
_active_conn  = None           # tirtc_conn_t (ctypes c_void_p value)
_active_thread: threading.Thread | None = None
_force_key_frame = threading.Event()
_media_factory: "Callable[[], MediaSource] | None" = None

# 保持对回调对象的引用，防止 GC
_cbs_ref: TIRTCCALLBACKS | None = None
_device_id_ref: bytes | None = None
_sdk_log_cb_ref = None
_sdk_running = False  # guard against duplicate start() calls
_callback_guard = SdkCallbackGuard()

# 对讲录音
from audio_recorder import AudioRecorder
_talkback_recorder: AudioRecorder | None = None

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
        _callback_guard.wait_for_idle()
        if not _sdk_running:
            return
        rc = sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
        if rc != 0:
            _sdk_err("TiRtcDisconnect", rc)

    threading.Thread(
        target=disconnect,
        daemon=True,
        name="stream-callback-disconnect",
    ).start()


def start(device_id: str, secret_key: str,
          media_factory: "Callable[[], MediaSource]",
          endpoint: str = None, client_id: str = "") -> None:
    global _media_factory, _cbs_ref, _device_id_ref, _sdk_running

    if _sdk_running:
        _log("TiRTC 已在运行，跳过重复启动")
        return

    _media_factory = media_factory
    _stop_event.clear()
    _sdk_started.clear()
    _sdk_stopped.clear()

    ver = sdk.TiRtcGetVersion()
    _log(f"TiRTC version: {ver.decode()}")

    # 设置发送缓冲区（须在 Init 前）
    buf = ctypes.c_uint32(1024 * 1024)
    rc = sdk.TiRtcSetOption(TIRTC_OPT_MAX_SEND_BUFFER,
                         ctypes.byref(buf), ctypes.sizeof(buf))
    if rc != 0:
        _sdk_err("TiRtcSetOption(MAX_SEND_BUFFER)", rc)
        sys.exit(1)

    rc = sdk.TiRtcInit()
    if rc != 0:
        _sdk_err("TiRtcInit", rc)
        sys.exit(1)

    sdk.TiRtcLogConfig(0, None, 0)
    sdk.TiRtcLogSetLevel(3)   # 默认静默，仅输出 SDK 错误级别日志
    if _LOG_LEVEL <= 10:
        # debug 级别：开启 SDK 详细日志，通过回调打印（不含 WebRTC 底层）
        global _sdk_log_cb_ref
        def _sdk_log_cb(line):
            if line:
                print(f"\033[0;90m[TiRTC-SDK]\033[0m {line.decode(errors='replace')}", flush=True)
        _sdk_log_cb_ref = sdk.LogCB(_sdk_log_cb)
        sdk.TiRtcLogSetCallback(_sdk_log_cb_ref)
        sdk.TiRtcLogSetLevel(8)

    if endpoint:
        ep_b = endpoint.encode()
        rc = sdk.TiRtcSetOption(TIRTC_OPT_SERVICE_ENDPOINT,
                             ctypes.c_char_p(ep_b), len(ep_b))
        if rc != 0:
            _sdk_err("TiRtcSetOption(ENDPOINT)", rc)
            sdk.TiRtcUninit()
            sys.exit(1)

    _cbs_ref = _build_callbacks()
    sk = secret_key.encode()
    rc = sdk.TiRtcSetOption(TIRTC_OPT_DEVICE_SECRET_KEY, ctypes.c_char_p(sk), len(sk))
    cid = (client_id or device_id).encode()
    sdk.set_client_id(cid)
    _device_id_ref = sdk.device_id_for_start(device_id, secret_key)
    rc = sdk.TiRtcStart(_device_id_ref, ctypes.byref(_cbs_ref))
    if rc != 0:
        _sdk_err("TiRtcStart", rc)
        sdk.TiRtcUninit()
        sys.exit(1)

    _sdk_running = True
    _log(f"TiRTC 启动中，等待客户端连接 (device_id={device_id})…")


def stop() -> None:
    global _sdk_running, _active_conn, _active_thread
    if not _sdk_running:
        return
    _sdk_running = False
    with _state_lock:
        _stop_event.set()
        active_conn_local, _active_conn = _active_conn, None
        _active_thread_local = _active_thread
        _active_thread = None

    if active_conn_local is not None:
        sdk.TiRtcDisconnect(ctypes.c_void_p(active_conn_local))

    if _active_thread_local is not None:
        _active_thread_local.join(timeout=8.0)

    _callback_guard.wait_for_idle()
    _close_talkback_file()
    rc = sdk.TiRtcStop()
    if rc != 0:
        _sdk_err("TiRtcStop", rc)

    _sdk_stopped.wait(timeout=8.0)
    _callback_guard.wait_for_idle()
    # 再次确认推流线程已退出，防止 Uninit 时访问已释放的 SDK 资源
    if _active_thread_local is not None and _active_thread_local.is_alive():
        join_worker_before_uninit(
            _active_thread_local, _warn, "实时音视频推流", timeout=3.0)
    sdk.TiRtcUninit()
    _log("TiRTC 已停止")


def is_active() -> bool:
    return _sdk_running


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


# ── SDK 回调 ──────────────────────────────────────────────────────────────────
def _build_callbacks() -> TIRTCCALLBACKS:

    def on_event(event, data, length):
        _log(f"SDK 事件 event={event} data={data} length={length}") 
        if event == TIRTC_EVENT_SYS_STARTED:
            _sdk_started.set()
            _log("SDK 已启动，等待客户端连接…")
        elif event == TIRTC_EVENT_SYS_STOPPED:
            _sdk_stopped.set()
            _log("SDK 已停止")

    def on_conn_accepted(hconn):
        _log(f"on_conn_accepted: 连接已接受 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x}")
        global _active_conn, _active_thread
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        _log(f"客户端已连接 hconn={hconn_val:#x}")

        with _state_lock:
            # 踢掉旧连接
            old_conn = _active_conn
            _active_conn = hconn_val

            _open_talkback_file()

            t = threading.Thread(
                target=_stream_worker,
                args=(hconn_val, _media_factory()),
                daemon=True,
                name="tirtc-stream",
            )
            _active_thread = t
            t.start()
        if old_conn is not None:
            _schedule_disconnect_after_callback(old_conn)
        return 0


    def on_conn_error(hconn, error):
        _log(f"on_conn_error: 连接错误 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} error={error}")
        global _active_conn
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        _err(f"连接错误 hconn={hconn_val:#x}: {sdk.TiRtcGetErrorStr(error).decode()}")
        with _state_lock:
            if _active_conn == hconn_val:
                _active_conn = None
        _close_talkback_file()
        _schedule_disconnect_after_callback(hconn_val)

    def on_disconnected(hconn):
        _log(f"on_disconnected: 连接已断开 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x}")
        global _active_conn
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            if _active_conn == hconn_val:
                _active_conn = None
        _close_talkback_file()

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
        _talkback_recorder.write_frame(fi, buf)
        if _talkback_recorder.frame_count == 1:
            _info(f"H5 对讲音频流检测到: stream={fi.stream_id} media={fi.media} {fi.length}bytes/帧")

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
    cbs.on_event             = OnEventCB(_callback_guard.wrap(on_event))
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
