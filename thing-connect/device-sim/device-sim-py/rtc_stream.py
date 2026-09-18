#!/usr/bin/env python3
from __future__ import annotations
"""rtc_stream.py — TiRTC 音视频推流（被动接受连接模式）

接口约定：
  runtime_callbacks()
  start_service(media_factory)
  stop_service()
  is_active() -> bool
  get_state() -> str   # "IDLE" | "IN_CALL"

对讲功能（H5 按住说话 → stream 14 → 设备端接收）：
  configure_talkback(enabled=True, recv_dir="./received", device_id="xxx",
                     playback=True)
  在 start_service() 之前调用；音频保存到 received_audio.raw，playback=True
  时同时解码并送入 Windows 扬声器。
"""

import ctypes
import os
import sys
import threading
import time

from dataclasses import dataclass
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
    TIRTC_AUDIO_ALAW, TIRTC_AUDIO_PCM,
    TIRTC_AUDIOSAMPLE_16K16B1C,
    TIRTC_FRAME_FLAG_KEY_FRAME,
    CONN_FATAL_ERRORS,
    TIRTC_E_BUSY, TIRTC_E_INVALID_HANDLE, TIRTC_E_CONN_CLOSED,
)

# H5 对讲 stream_id（与 Web SDK TiRtcAudioInput streamId 一致）
TALKBACK_STREAM_ID = 14
TALKBACK_VIDEO_STREAM_ID = 15
MAX_STREAM_CONNECTIONS = sdk.TIRTC_REFERENCE_MAX_CONNECTIONS


@dataclass
class _StreamConnection:
    pending: bool = True
    send_audio_subscribed: bool = False
    send_video_subscribed: bool = False
    talkback_subscribed: bool = False
    talkback_video_subscribed: bool = False
    consecutive_send_failures: int = 0

# ── 模块状态 ──────────────────────────────────────────────────────────────────
_state_lock   = threading.Lock()
_activation_lock = threading.Lock()
_stop_event   = threading.Event()
_connections: dict[int, _StreamConnection] = {}
_active_thread: threading.Thread | None = None
_force_key_frame = threading.Event()
_force_key_lock = threading.Lock()
_media_factory: "Callable[[], MediaSource] | None" = None

# 保持对回调对象的引用，防止 GC
_cbs_ref: TIRTCCALLBACKS | None = None
_service_active = False
_callback_guard = SdkCallbackGuard()

# 对讲录音
from audio_recorder import AudioRecorder
_talkback_recorder: AudioRecorder | None = None
_talkback_work: CallbackWorkQueue | None = None
_talkback_speaker = None

_LOG_LEVEL = 10  # debug=10 info=20 warn=30 error=40

def set_log_level(level: str) -> None:
    global _LOG_LEVEL
    _LOG_LEVEL = {"debug": 10, "info": 20, "warn": 30, "error": 40}.get(level.lower(), 10)


def configure_talkback(enabled: bool = False, recv_dir: str = "",
                       device_id: str = "", playback: bool = False) -> None:
    """配置 H5 对讲录音及可选扬声器播放。在 start_service() 前调用。"""
    global _talkback_recorder, _talkback_speaker
    _close_talkback_playback()
    if enabled:
        _talkback_recorder = AudioRecorder(
            recv_dir, device_id, "received_audio.raw", _info, _warn)
        _info(f"对讲录音已启用: stream={TALKBACK_STREAM_ID} dir={recv_dir} device={device_id}")
    else:
        _talkback_recorder = None
    if playback:
        try:
            from audio_device import SpeakerPlayback, select_speaker
            speaker_device = select_speaker()
            _talkback_speaker = SpeakerPlayback(speaker_device)
            _info(f"Web 对讲扬声器播放已启用: device={speaker_device}")
        except Exception as exc:
            _talkback_speaker = None
            _warn(f"Web 对讲扬声器不可用，将仅保存录音: {exc}")


def _close_talkback_playback() -> None:
    global _talkback_speaker
    if _talkback_speaker is None:
        return
    _talkback_speaker.close()
    _talkback_speaker = None


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


def _request_key_frame() -> None:
    with _force_key_lock:
        _force_key_frame.set()


def _take_key_frame_request() -> bool:
    with _force_key_lock:
        requested = _force_key_frame.is_set()
        _force_key_frame.clear()
        return requested


def _schedule_disconnect_after_callback(hconn_val: int) -> None:
    """避免从 SDK 回调内部重入 Disconnect。"""
    def disconnect():
        if not _service_active:
            return
        rc = sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
        if rc != 0:
            _sdk_err("TiRtcDisconnect", rc)

    _callback_guard.defer(
        disconnect, name="stream-callback-disconnect")


def _activate_connection_after_callback(hconn_val: int) -> None:
    """Activate one bounded viewer without replacing existing viewers."""
    global _active_thread
    with _activation_lock:
        with _state_lock:
            connection = _connections.get(hconn_val)
            valid = (_service_active and _media_factory is not None and
                     connection is not None and connection.pending)
            need_worker = _active_thread is None
        if not valid:
            sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
            return

        source = None
        if need_worker:
            try:
                source = _media_factory()
            except BaseException as exc:
                _err(f"创建媒体源失败 hconn={hconn_val:#x}: {exc}")
                with _state_lock:
                    _connections.pop(hconn_val, None)
                sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
                return

        with _state_lock:
            connection = _connections.get(hconn_val)
            if not _service_active or connection is None or not connection.pending:
                current = False
            else:
                connection.pending = False
                current = True
                if need_worker:
                    _stop_event.clear()
                    thread = threading.Thread(
                        target=_stream_worker,
                        args=(source,),
                        daemon=True,
                        name="tirtc-stream",
                    )
                    # Start while holding the state lock so stop_service cannot
                    # observe an unstarted Thread object.
                    thread.start()
                    _active_thread = thread
        if not current:
            if source is not None:
                source.close()
            sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
            return

        hconn = ctypes.c_void_p(hconn_val)
        audio_rc = sdk.TiRtcSubscribeAudio(hconn, TALKBACK_STREAM_ID)
        video_rc = sdk.TiRtcSubscribeVideo(hconn, TALKBACK_VIDEO_STREAM_ID)
        audio_subscribed = audio_rc >= 0
        video_subscribed = video_rc >= 0
        with _state_lock:
            connection = _connections.get(hconn_val)
            current = (_service_active and connection is not None and
                       not connection.pending)
            already_receiving_audio = any(
                item.talkback_subscribed
                for key, item in _connections.items() if key != hconn_val)
            if current:
                connection.talkback_subscribed = audio_subscribed
                connection.talkback_video_subscribed = video_subscribed
            active_count = sum(not item.pending for item in _connections.values())
        if not current:
            if audio_subscribed:
                sdk.TiRtcUnsubscribeAudio(hconn, TALKBACK_STREAM_ID)
            if video_subscribed:
                sdk.TiRtcUnsubscribeVideo(hconn, TALKBACK_VIDEO_STREAM_ID)
            sdk.TiRtcDisconnect(hconn)
            return
        if audio_subscribed and not already_receiving_audio:
            _open_talkback_file()
        if not audio_subscribed:
            _warn(f"订阅客户端对讲音频失败 rc={audio_rc}")
        if not video_subscribed:
            _warn(f"订阅客户端下行视频失败 rc={video_rc}")
        _info(
            f"实时流连接已建立 hconn={hconn_val:#x} "
            f"viewers={active_count}/{MAX_STREAM_CONNECTIONS}"
        )


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
    global _service_active, _active_thread, _talkback_work
    with _activation_lock:
        if not _service_active:
            _close_talkback_playback()
            return
        _service_active = False
        with _state_lock:
            _stop_event.set()
            connections = tuple(_connections)
            _connections.clear()
            _active_thread_local = _active_thread
            _active_thread = None

    for hconn_val in connections:
        sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))

    if _active_thread_local is not None:
        _active_thread_local.join(timeout=8.0)

    _callback_guard.wait_for_all()
    _close_talkback_file()
    if _talkback_work is not None:
        _talkback_work.stop()
    _close_talkback_playback()
    if _active_thread_local is not None and _active_thread_local.is_alive():
        join_worker_before_uninit(
            _active_thread_local, _warn, "实时音视频推流", timeout=3.0)
    _info("实时流业务已停止")


def is_active() -> bool:
    return _service_active


def get_state() -> str:
    with _state_lock:
        return "IN_CALL" if any(
            not connection.pending for connection in _connections.values()
        ) else "IDLE"


# ── 推流线程 ──────────────────────────────────────────────────────────────────
def _now_ms() -> int:
    return int(time.monotonic() * 1000)



def _stream_worker(source: MediaSource) -> None:
    global _active_thread
    _log("共享推流线程启动")

    has_video = source.has_video()
    audio_pts_ms  = 0
    video_pts_ms  = 0.0 if has_video else float("inf")
    first_video   = True
    wall_start_ms = _now_ms()
    audio_was_enabled = False
    video_was_enabled = False

    def _send_to_targets(targets, fi, buf, is_video: bool) -> None:
        for hconn_val in targets:
            hconn = ctypes.c_void_p(hconn_val)
            rc = (sdk.TiRtcSendVideoStream(hconn, ctypes.byref(fi), buf)
                  if is_video else
                  sdk.TiRtcSendAudioStream(hconn, ctypes.byref(fi), buf))
            disconnect = rc in CONN_FATAL_ERRORS
            with _state_lock:
                connection = _connections.get(hconn_val)
                if connection is None or connection.pending:
                    continue
                if rc >= 0:
                    connection.consecutive_send_failures = 0
                elif rc not in (
                        TIRTC_E_BUSY, TIRTC_E_INVALID_HANDLE,
                        TIRTC_E_CONN_CLOSED) and not disconnect:
                    connection.consecutive_send_failures += 1
                    disconnect = connection.consecutive_send_failures >= 3
            if rc == TIRTC_E_BUSY and is_video:
                _request_key_frame()
            elif rc < 0 and rc not in (
                    TIRTC_E_BUSY, TIRTC_E_INVALID_HANDLE,
                    TIRTC_E_CONN_CLOSED) and not disconnect:
                _err(
                    f"Send{'Video' if is_video else 'Audio'}Stream "
                    f"hconn={hconn_val:#x} rc={rc}: "
                    f"{sdk.TiRtcGetErrorStr(rc).decode()}"
                )
            if disconnect:
                _warn(f"单个实时流连接发送失败，断开 hconn={hconn_val:#x}")
                with _state_lock:
                    _connections.pop(hconn_val, None)
                sdk.TiRtcDisconnect(hconn)

    def _send_audio(targets) -> bool:
        nonlocal audio_pts_ms
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
        _send_to_targets(targets, fi, buf, False)
        audio_pts_ms += duration_ms
        return True

    def _send_video(targets) -> bool:
        nonlocal video_pts_ms, first_video
        key_requested = _take_key_frame_request()
        force_key = first_video or key_requested

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
        _send_to_targets(targets, fi, buf, True)
        first_video = False
        video_pts_ms += VIDEO_FRAME_MS
        return True

    try:
        while not _stop_event.is_set():
            with _state_lock:
                audio_targets = tuple(
                    hconn_val for hconn_val, connection in _connections.items()
                    if not connection.pending and
                    connection.send_audio_subscribed
                )
                video_targets = tuple(
                    hconn_val for hconn_val, connection in _connections.items()
                    if not connection.pending and has_video and
                    connection.send_video_subscribed
                )
            audio_enabled = bool(audio_targets)
            video_enabled = bool(video_targets)
            if not audio_enabled and not video_enabled:
                time.sleep(0.01)
                continue
            elapsed = _now_ms() - wall_start_ms
            if audio_enabled and not audio_was_enabled:
                audio_pts_ms = max(audio_pts_ms, elapsed)
            if video_enabled and not video_was_enabled:
                video_pts_ms = max(video_pts_ms, float(elapsed))
                first_video = True
                _request_key_frame()
            audio_was_enabled = audio_enabled
            video_was_enabled = video_enabled
            target_pts = (
                min(audio_pts_ms, video_pts_ms)
                if audio_enabled and video_enabled
                else audio_pts_ms if audio_enabled else video_pts_ms
            )
            wait_ms    = target_pts - elapsed
            if wait_ms > 2:
                time.sleep(wait_ms / 1000.0)
                continue

            if audio_enabled and (
                    not video_enabled or audio_pts_ms <= video_pts_ms):
                if not _send_audio(audio_targets):
                    break
            else:
                if not _send_video(video_targets):
                    break
    finally:
        source.close()
        with _state_lock:
            if _active_thread is threading.current_thread():
                _active_thread = None
        _log("共享推流线程退出")


def _process_talkback_item(item) -> None:
    frame, buf = item
    recorder = _talkback_recorder
    if recorder is not None and recorder.is_open:
        recorder.write_frame(frame, buf)
    speaker = _talkback_speaker
    if speaker is not None:
        if frame.media == TIRTC_AUDIO_ALAW:
            from alaw import alaw_decode
            pcm = alaw_decode(buf)
        elif frame.media == TIRTC_AUDIO_PCM:
            pcm = buf
        else:
            pcm = None
        if pcm is not None:
            sample_rate = (16000 if frame.flags == TIRTC_AUDIOSAMPLE_16K16B1C
                           else 8000)
            speaker.play(pcm, source_rate=sample_rate)
    if recorder is not None and recorder.frame_count == 1:
        _info(
            f"H5 对讲音频流检测到: stream={frame.stream_id} "
            f"media={frame.media} {frame.length}bytes/帧"
        )


# ── SDK 回调 ──────────────────────────────────────────────────────────────────
def _build_callbacks() -> TIRTCCALLBACKS:

    def on_conn_accepted(hconn):
        _log(f"on_conn_accepted: 连接已接受 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x}")
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            duplicate = hconn_val in _connections
            added = (_service_active and not duplicate and
                     len(_connections) < MAX_STREAM_CONNECTIONS)
            if added:
                _connections[hconn_val] = _StreamConnection()
        if duplicate:
            _log(f"忽略重复连接回调 hconn={hconn_val:#x}")
            return
        try:
            queued = added and _callback_guard.defer(
                _activate_connection_after_callback,
                hconn_val,
                name="stream-accept",
            )
        except RuntimeError:
            queued = False
        if not queued:
            with _state_lock:
                connection = _connections.get(hconn_val)
                if added and connection is not None and connection.pending:
                    _connections.pop(hconn_val, None)
            _schedule_disconnect_after_callback(hconn_val)


    def on_conn_error(hconn, error):
        _log(f"on_conn_error: 连接错误 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} error={error}")
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        _err(f"连接错误 hconn={hconn_val:#x}: {sdk.TiRtcGetErrorStr(error).decode()}")
        with _state_lock:
            _connections.pop(hconn_val, None)
        _schedule_disconnect_after_callback(hconn_val)

    def on_disconnected(hconn):
        _log(f"on_disconnected: 连接已断开 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x}")
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            _connections.pop(hconn_val, None)

    def on_audio(hconn, pFi, data):
        if not data:
            return
        try:
            fi = ctypes.cast(pFi, ctypes.POINTER(TIRTCFRAMEINFO)).contents
        except Exception:
            return
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            connection = _connections.get(hconn_val)
            accepted = (
                connection is not None
                and not connection.pending
                and connection.talkback_subscribed
                and fi.stream_id == TALKBACK_STREAM_ID
            )
        if not accepted:
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
        if not data:
            return
        try:
            fi = ctypes.cast(pFi, ctypes.POINTER(TIRTCFRAMEINFO)).contents
        except Exception:
            return
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            connection = _connections.get(hconn_val)
            accepted = (
                connection is not None
                and not connection.pending
                and connection.talkback_video_subscribed
                and fi.stream_id == TALKBACK_VIDEO_STREAM_ID
            )
        if accepted:
            _log(
                "收到已订阅的客户端视频帧 "
                f"stream={fi.stream_id} length={fi.length}"
            )

    def on_message(hconn, pFi, data):
        pass

    def on_command(hconn, cmdw, data, length):
        pass

    def on_request_key_frame(hconn, stream_id):
        _log(f"on_request_key_frame: 收到关键帧请求 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} stream_id={stream_id}")
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            connection = _connections.get(hconn_val)
            accepted = connection is not None and not connection.pending
        if accepted and stream_id == VIDEO_STREAM_ID:
            _request_key_frame()

    def on_subscribe_video(hconn, stream_id):
        _log(f"on_subscribe_video: 视频订阅 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} stream_id={stream_id}")
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            connection = _connections.get(hconn_val)
            accepted = stream_id == VIDEO_STREAM_ID and connection is not None
            if accepted:
                connection.send_video_subscribed = True
        if accepted:
            # H5 attaches/subscribes only after connect() resolves, so the
            # IDR sent at connection acceptance may already be gone.
            _request_key_frame()
        return 0 if accepted else -1

    def on_unsubscribe_video(hconn, stream_id):
        _log(f"on_unsubscribe_video: 视频取消订阅 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} stream_id={stream_id}")
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            connection = _connections.get(hconn_val)
            if connection is not None and stream_id == VIDEO_STREAM_ID:
                connection.send_video_subscribed = False

    def on_subscribe_audio(hconn, stream_id):
        _log(f"on_subscribe_audio: 音频订阅 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} stream_id={stream_id}")
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            connection = _connections.get(hconn_val)
            accepted = stream_id == AUDIO_STREAM_ID and connection is not None
            if accepted:
                connection.send_audio_subscribed = True
        return 0 if accepted else -1

    def on_unsubscribe_audio(hconn, stream_id):
        _log(f"on_unsubscribe_audio: 音频取消订阅 hconn={ctypes.cast(hconn, ctypes.c_void_p).value:#x} stream_id={stream_id}")
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            connection = _connections.get(hconn_val)
            if connection is not None and stream_id == AUDIO_STREAM_ID:
                connection.send_audio_subscribed = False

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
