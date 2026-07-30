#!/usr/bin/env python3
from __future__ import annotations
"""rtc_voip.py — TiRTC VoIP WHIP 对讲会话管理（主动发起连接模式）

接口约定：
  runtime_callbacks()
  start_service()
  stop_service()
  start_session(peer_id, token, audio_file)
  stop_session()
  is_active() -> bool
  get_state() -> str   # "IDLE" | "CONNECTING" | "IN_CALL"
  report_profile(voip_server, mqtt_token) -> list
  reject_session(wx_app_id, wx_model_id, wx_session_token, wx_room_id, wx_payload, hangup_reason)
"""

import audioop
import ctypes
import json
import math
import os
import requests
import http_trace
import sys
import threading
import time

from callback_work_queue import CallbackWorkQueue
from g711 import alaw_decode, alaw_encode
from media_source import VIDEO_FRAME_MS
from media_postprocess import convert_audio_to_wav, convert_video_to_mp4
from sdk_callback_guard import SdkCallbackGuard, join_worker_before_uninit

import tirtc_sdk as sdk
from tirtc_runtime import ServiceKind, process_tirtc_runtime
from media_file_reader import AudioFileReader, VideoFileReader
from media_formats import (
    AUDIO_FORMATS,
    VIDEO_FORMATS,
    normalize_audio_format,
    supports_live_audio_capture,
    video_file_extension,
)
from tirtc_sdk import (
    TIRTCFRAMEINFO, TIRTCCALLBACKS,
    OnEventCB, OnConnAcceptCB, OnConnErrCB, OnDisconnCB,
    OnAudioCB, OnVideoCB, OnMsgCB, OnCmdCB, OnKeyFrameCB,
    OnSubVideoCB, OnUnsubVideoCB, OnSubAudioCB, OnUnsubAudioCB,
    ConnectCB, ServiceReqCB,
    TIRTC_EVENT_SYS_STARTED, TIRTC_EVENT_SYS_STOPPED,
    TIRTC_OPT_SERVICE_ENDPOINT, TIRTC_OPT_MAX_SEND_BUFFER,
    TIRTC_OPT_DEVICE_SECRET_KEY,
    AUDIO_STREAM_ID, VIDEO_STREAM_ID,
    TIRTC_AUDIO_ALAW, TIRTC_AUDIO_PCM,
    TIRTC_FRAME_FLAG_KEY_FRAME,
    CONN_FATAL_ERRORS,
)

# ── 常量 ──────────────────────────────────────────────────────────────────────
AUDIO_PKT_MS    = 40
WHIP_CONNECT_TIMEOUT_SEC = 10
CONNECT_ACK_TIMEOUT_SEC = 10

# ── 日志 ──────────────────────────────────────────────────────────────────────
_LOG_LEVEL = 10  # debug=10 info=20 warn=30 error=40

def set_log_level(level: str) -> None:
    global _LOG_LEVEL
    _LOG_LEVEL = {"debug": 10, "info": 20, "warn": 30, "error": 40}.get(level.lower(), 10)

import datetime as _dt

def _ts() -> str:
    return _dt.datetime.now().strftime("%H:%M:%S.%f")[:-3]

def _log(msg):   # debug
    if _LOG_LEVEL <= 10:
        print(f"{_ts()} \033[0;36m[rtc_voip]\033[0m {msg}", flush=True)
def _info(msg):  # info
    if _LOG_LEVEL <= 20:
        print(f"{_ts()} \033[0;32m[rtc_voip]\033[0m {msg}", flush=True)
def _warn(msg):  # warn
    if _LOG_LEVEL <= 30:
        print(f"{_ts()} \033[1;33m[rtc_voip]\033[0m {msg}", flush=True)
def _err(msg):   # error
    if _LOG_LEVEL <= 40:
        print(f"{_ts()} \033[0;31m[rtc_voip]\033[0m {msg}", file=sys.stderr, flush=True)


def _log_whip_connect_params(peer_id: str, token: str) -> None:
    scheme = peer_id.split("?", 1)[0] if peer_id else ""
    _log(
        "TiRtcWhipConnect 参数 "
        f"peer_scheme={scheme or '<none>'} "
        f"description_length={len(peer_id)} token_length={len(token)} "
        "(敏感内容已隐藏)"
    )

# ── 模块状态 ──────────────────────────────────────────────────────────────────
_service_active = False
_cbs_ref: "TIRTCCALLBACKS | None" = None
_device_id = ""
_session_end_callback = None
_callback_guard = SdkCallbackGuard()

# ── 会话状态 ──────────────────────────────────────────────────────────────────
_state_lock    = threading.Lock()
_session_state = "IDLE"          # IDLE | CONNECTING | IN_CALL | DISCONNECTING
_active_hconn: "int | None" = None   # hconn_val (c_void_p 的整数值)
_audio_file_path: "str | None" = None
_recv_file: "object | None" = None   # 接收音频文件句柄
_recv_video_file: "object | None" = None
_recv_audio_path = ""
_recv_video_path = ""
_recv_root = ""
_stream_thread: "threading.Thread | None" = None
_stream_stop   = threading.Event()
_connect_cb_ref: "ConnectCB | None" = None  # 防止 GC
_connect_cb_refs: "dict[int, ConnectCB]" = {}  # 取消连接后仍需保活旧回调
_whip_connect_timer: "threading.Timer | None" = None
_connect_timer: "threading.Timer | None" = None
_session_generation = 0
_playback_enabled = False
_speaker: "object | None" = None
_mic_capture: "object | None" = None
_video_file_path = ""
_session_video_file = ""
_video_thread: "threading.Thread | None" = None
_force_key_frame = threading.Event()
_up_audio_format = "alaw_8khz"
_down_audio_format = "alaw_8khz"
_up_video_format = "h264"
_down_video_format = "h264"
_session_role = "unknown"
_cancel_on_connect = False
_receive_work: "CallbackWorkQueue | None" = None


def _tracked_callback(callback):
    """记录仍在执行的 SDK 回调，供业务切换和进程退出等待。"""
    return _callback_guard.wrap(callback)


def _wait_for_callbacks_idle() -> None:
    """等待所有 SDK 回调返回；只能从非 SDK 回调线程调用。"""
    _callback_guard.wait_for_idle()


def _schedule_disconnect_after_callback(hconn_val: int) -> None:
    """在当前 SDK 回调返回后断开，避免从命令回调中重入 Disconnect。"""
    _stream_stop.set()

    def disconnect():
        global _session_state
        with _state_lock:
            if _active_hconn != hconn_val or _session_state == "DISCONNECTING":
                return
            # 由本线程取得断开所有权，避免终端 hangup/会话切换同时再调一次
            # Disconnect。保留 active_hconn，供 on_disconnected 精确匹配。
            _session_state = "DISCONNECTING"
        rc = sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
        if rc != 0:
            _err(
                f"TiRtcDisconnect 失败 hconn={hconn_val:#x} "
                f"rc={rc} ({sdk.TiRtcGetErrorStr(rc).decode()})"
            )
            _handle_disconnect(hconn_val)

    _callback_guard.defer(
        disconnect, name="voip-remote-disconnect")


def _disconnect_stale_after_callback(hconn_val: int) -> None:
    """Disconnect an unowned connection after leaving the SDK callback."""
    def disconnect():
        sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))

    _callback_guard.defer(
        disconnect, name="voip-stale-disconnect")


def configure_media_formats(up_audio: str, down_audio: str,
                            up_video: str, down_video: str) -> None:
    global _up_audio_format, _down_audio_format, _up_video_format, _down_video_format
    _up_audio_format = normalize_audio_format(up_audio)
    _down_audio_format = normalize_audio_format(down_audio)
    _up_video_format, _down_video_format = up_video, down_video


def configure_video(video_file: str = "") -> None:
    """配置 VoIP 上行 H.264 Annex-B 文件；空字符串表示纯音频设备。"""
    global _video_file_path
    _video_file_path = video_file


def configure_receive_dir(recv_dir: str = "") -> None:
    """设置 VoIP 接收音视频目录，实际文件放在 recv_dir/device_id/ 下。"""
    global _recv_root
    _recv_root = recv_dir


def has_video() -> bool:
    return bool(_video_file_path)


def configure_playback(enable: bool) -> None:
    """启用/关闭 VoIP 下行 G.711 A-law 扬声器播放。"""
    global _playback_enabled, _speaker
    _playback_enabled = enable
    if not enable:
        if _speaker is not None:
            _speaker.close()
            _speaker = None
        return

    try:
        from audio_device import SpeakerPlayback, select_speaker
        device = select_speaker()
        if device is None:
            import sounddevice as sd
            default_output = sd.default.device[1]
            if default_output is None or int(default_output) < 0:
                raise RuntimeError("系统没有可用的音频输出设备")
            device = int(default_output)
        info = sd.query_devices(device) if 'sd' in locals() else None
        if info is not None and info.get("max_output_channels", 0) < 1:
            raise RuntimeError(f"设备 [{device}] 不支持音频输出")
        _speaker = SpeakerPlayback(device)
        _info(f"VoIP 扬声器播放已启用 device={device}")
        _warn("建议降低电脑音量并使用耳机，避免回声")
    except Exception as e:
        _playback_enabled = False
        _speaker = None
        raise RuntimeError(f"无法启用 VoIP 扬声器: {e}") from e


def configure_hardware_audio(enable: bool, fmt: str = "alaw_8khz") -> None:
    """VoIP 使用 PC 麦克风上行、扬声器下行；主要用于 Windows 测试。"""
    global _mic_capture
    if not enable:
        if _mic_capture is not None:
            _mic_capture.close()
            _mic_capture = None
        configure_playback(False)
        return
    fmt = normalize_audio_format(fmt)
    if not supports_live_audio_capture(fmt):
        raise RuntimeError(f"VoIP 实时麦克风不支持 {fmt}，仅支持 pcm/g711a")
    try:
        from audio_device import MicCapture, select_mic
        configure_playback(True)
        mic_device = select_mic()
        if mic_device is None:
            raise RuntimeError("系统没有可用的麦克风输入设备")
        _mic_capture = MicCapture(mic_device)
        _info(f"VoIP PC 音频已启用 mic={mic_device}")
    except Exception as e:
        if _mic_capture is not None:
            _mic_capture.close()
            _mic_capture = None
        configure_playback(False)
        raise RuntimeError(f"无法启用 VoIP PC 音频: {e}") from e


def runtime_callbacks() -> TIRTCCALLBACKS:
    global _cbs_ref
    if _cbs_ref is None:
        _cbs_ref = _build_callbacks()
    return _cbs_ref


def callback_guard() -> SdkCallbackGuard:
    return _callback_guard


def start_service(device_id: str) -> None:
    global _service_active, _device_id, _receive_work
    if _service_active:
        return
    _device_id = device_id
    if _receive_work is None:
        _receive_work = CallbackWorkQueue(
            "voip-downlink",
            _process_receive_item,
            _warn,
        )
    _receive_work.start()
    _service_active = True
    _info("VoIP 业务已就绪")


def stop_service() -> None:
    global _service_active, _speaker, _mic_capture
    if not _service_active:
        return
    _service_active = False
    stop_session()
    _cancel_connect_timer()
    _callback_guard.wait_for_all()
    if _receive_work is not None:
        _receive_work.stop()
    if _speaker is not None:
        _speaker.close()
        _speaker = None
    if _mic_capture is not None:
        _mic_capture.close()
        _mic_capture = None
    _info("VoIP 业务已停止")


def is_service_active() -> bool:
    return _service_active


def _build_callbacks() -> TIRTCCALLBACKS:
    def on_conn_error(hconn, error):
        hval = ctypes.cast(hconn, ctypes.c_void_p).value
        _err(f"on_conn_error hconn={hval:#x} error={sdk.TiRtcGetErrorStr(error).decode()}")
        _schedule_disconnect_after_callback(hval)

    def on_disconnected(hconn):
        hval = ctypes.cast(hconn, ctypes.c_void_p).value
        _log(f"on_disconnected hconn={hval:#x}")
        _callback_guard.defer(
            _handle_disconnect,
            hval,
            name="voip-disconnected-cleanup",
        )

    def on_audio(hconn, pFi, data):
        if not data:
            return
        fi = ctypes.cast(pFi, ctypes.POINTER(TIRTCFRAMEINFO)).contents
        payload = ctypes.string_at(data, fi.length)
        if _receive_work is not None:
            _receive_work.submit(("audio", payload))

    def on_video(hconn, pFi, data):
        if not data:
            return
        fi = ctypes.cast(pFi, ctypes.POINTER(TIRTCFRAMEINFO)).contents
        payload = ctypes.string_at(data, fi.length)
        if _receive_work is not None:
            _receive_work.submit(("video", payload))
    def on_message(hconn, pFi, data): pass
    def on_command(hconn, cmdw, data, length):
        if cmdw == 0x2000:
            hval = ctypes.cast(hconn, ctypes.c_void_p).value
            _info(f"收到接通命令 0x2000 hconn={hval:#x}，双方对讲建立成功")
            _callback_guard.defer(
                _start_media_threads,
                hval,
                name="voip-media-start",
            )
        elif cmdw == 0x2001:
            hval = ctypes.cast(hconn, ctypes.c_void_p).value
            _log(f"收到对端挂断命令 0x2001 hconn={hval:#x}，回调返回后主动断开")
            _schedule_disconnect_after_callback(hval)
    def on_request_key_frame(hconn, stream_id):
        if stream_id == VIDEO_STREAM_ID:
            _force_key_frame.set()
    def on_subscribe_video(hconn, stream_id): return 0
    def on_unsubscribe_video(hconn, stream_id): pass
    def on_subscribe_audio(hconn, stream_id): return 0
    def on_unsubscribe_audio(hconn, stream_id): pass

    cbs = TIRTCCALLBACKS()
    cbs.on_conn_error        = OnConnErrCB(_tracked_callback(on_conn_error))
    cbs.on_disconnected      = OnDisconnCB(_tracked_callback(on_disconnected))
    cbs.on_audio             = OnAudioCB(_tracked_callback(on_audio))
    cbs.on_video             = OnVideoCB(_tracked_callback(on_video))
    cbs.on_message           = OnMsgCB(_tracked_callback(on_message))
    cbs.on_command           = OnCmdCB(_tracked_callback(on_command))
    cbs.on_request_key_frame = OnKeyFrameCB(_tracked_callback(on_request_key_frame))
    cbs.on_subscribe_video   = OnSubVideoCB(_tracked_callback(on_subscribe_video))
    cbs.on_unsubscribe_video = OnUnsubVideoCB(_tracked_callback(on_unsubscribe_video))
    cbs.on_subscribe_audio   = OnSubAudioCB(_tracked_callback(on_subscribe_audio))
    cbs.on_unsubscribe_audio = OnUnsubAudioCB(_tracked_callback(on_unsubscribe_audio))

    # Keep callback wrapper objects alive to prevent GC of Python ctypes CFUNCTYPE wrappers.
    # ctypes Structure fields hold C function pointers, not Python references, so the Python
    # wrapper objects can be GC'd. Storing them here keeps reference counts alive.
    cbs._cb_refs = [
        cbs.on_event, cbs.on_conn_accepted, cbs.on_conn_error,
        cbs.on_disconnected, cbs.on_audio, cbs.on_video,
        cbs.on_message, cbs.on_command, cbs.on_request_key_frame,
        cbs.on_subscribe_video, cbs.on_unsubscribe_video,
        cbs.on_subscribe_audio, cbs.on_unsubscribe_audio,
    ]
    return cbs


def get_state() -> str:
    with _state_lock:
        return _session_state


def is_active() -> bool:
    with _state_lock:
        return _session_state in ("CONNECTING", "IN_CALL")


def set_session_end_callback(callback) -> None:
    global _session_end_callback
    _session_end_callback = callback


def _cancel_connect_timer() -> None:
    global _connect_timer, _whip_connect_timer
    timer, _connect_timer = _connect_timer, None
    whip_timer, _whip_connect_timer = _whip_connect_timer, None
    for item in (timer, whip_timer):
        if item is not None:
            item.cancel()


def _reset_session_state_locked() -> None:
    global _session_state, _active_hconn, _audio_file_path, _session_video_file
    global _stream_thread, _video_thread, _session_role, _cancel_on_connect
    global _session_generation
    _session_generation += 1
    _session_state = "IDLE"
    _active_hconn = None
    _audio_file_path = None
    _stream_thread = None
    _video_thread = None
    _session_video_file = ""
    _session_role = "unknown"
    _cancel_on_connect = False


def _prepare_receive_files(selected_video_file: str) -> tuple[object, object | None, str, str]:
    base_dir = _recv_root or os.path.join(os.path.dirname(os.path.abspath(__file__)), "received")
    recv_dir = os.path.join(base_dir, _device_id)
    os.makedirs(recv_dir, exist_ok=True)
    audio_recv_path = os.path.join(recv_dir, "received_audio.raw")
    video_recv_path = os.path.join(
        recv_dir, f"received_video.{video_file_extension(_down_video_format)}")
    af = None
    vf = None
    try:
        af = open(audio_recv_path, "wb")
        vf = open(video_recv_path, "wb") if selected_video_file else None
    except OSError:
        for f in (af, vf):
            if f is not None:
                f.close()
        raise
    return af, vf, audio_recv_path, (video_recv_path if vf is not None else "")


def _on_connect_ack_timeout(hconn_val: int) -> None:
    with _state_lock:
        if _active_hconn != hconn_val or _session_state != "CONNECTING":
            return
    _warn(f"等待 0x2000 超时（{CONNECT_ACK_TIMEOUT_SEC}s）hconn={hconn_val:#x}，主动断开")
    sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
    _handle_disconnect(hconn_val)


def _on_whip_connect_timeout(generation: int) -> None:
    global _whip_connect_timer
    with _state_lock:
        if (_session_generation != generation
                or _session_state != "CONNECTING"
                or _active_hconn is not None):
            return
        _whip_connect_timer = None
        _reset_session_state_locked()
    _close_receive_files()
    _warn(
        f"等待 WHIP 连接回调超时（{WHIP_CONNECT_TIMEOUT_SEC}s），"
        "已结束 VoIP 会话"
    )
    if _session_end_callback:
        _session_end_callback()


def _handle_disconnect(hconn_val: int):
    global _session_state, _active_hconn
    with _state_lock:
        if _active_hconn != hconn_val:
            return
        # 先进入 DISCONNECTING，阻止新的同类会话复用尚未退出的媒体线程。
        # 线程引用必须在 reset 前保存，否则上层立即 Uninit 时可能仍有
        # TiRtcSendAudioStream/TiRtcSendVideoStream 正在底层 SDK 内执行。
        _session_state = "DISCONNECTING"
        _active_hconn = None
        t = _stream_thread
        vt = _video_thread
    _cancel_connect_timer()
    _stream_stop.set()

    join_worker_before_uninit(t, _warn, "VoIP 音频推流")
    join_worker_before_uninit(vt, _warn, "VoIP 视频推流")

    _close_receive_files()
    with _state_lock:
        if _session_state == "DISCONNECTING":
            _reset_session_state_locked()
    _info("连接已断开，回到 IDLE")
    if _session_end_callback:
        _session_end_callback()


def _handle_audio(data: bytes):
    global _recv_file
    with _state_lock:
        f = _recv_file
    if f is not None:
        try:
            f.write(data)
            f.flush()
        except (OSError, ValueError) as e:
            _err(f"写接收音频失败: {e}")
    if _playback_enabled and _speaker is not None:
        try:
            audio_format = AUDIO_FORMATS[_down_audio_format]
            if audio_format.media == TIRTC_AUDIO_ALAW:
                _speaker.play(alaw_decode(data), source_rate=audio_format.sample_rate)
            elif audio_format.media == TIRTC_AUDIO_PCM:
                _speaker.play(data, source_rate=audio_format.sample_rate)
        except Exception as e:
            _err(f"播放接收音频失败: {e}")


def _handle_video(data: bytes) -> None:
    with _state_lock:
        f = _recv_video_file
    if f is not None:
        try:
            f.write(data)
            f.flush()
        except (OSError, ValueError) as e:
            _err(f"写接收视频失败: {e}")


def _process_receive_item(item) -> None:
    kind, data = item
    if kind == "audio":
        _handle_audio(data)
    else:
        _handle_video(data)


def _close_receive_files() -> None:
    global _recv_file, _recv_video_file, _recv_audio_path, _recv_video_path
    if _receive_work is not None:
        _receive_work.drain()
    with _state_lock:
        af, _recv_file = _recv_file, None
        vf, _recv_video_file = _recv_video_file, None
        audio_path, _recv_audio_path = _recv_audio_path, ""
        video_path, _recv_video_path = _recv_video_path, ""
    for f in (af, vf):
        if f is not None:
            try:
                f.close()
            except OSError:
                pass
    if audio_path:
        spec = AUDIO_FORMATS[_down_audio_format]
        fmt_info = {
            "encoding": "s16le" if spec.codec == "pcm" else spec.codec,
            "sample_rate": spec.sample_rate,
        }
        convert_audio_to_wav(audio_path, fmt_info, _info, _warn)
    if video_path:
        convert_video_to_mp4(video_path, _down_video_format, _info, _warn)


def _start_media_threads(hconn_val: int) -> None:
    global _session_state, _stream_thread, _video_thread

    with _state_lock:
        if _active_hconn != hconn_val:
            _log(f"忽略媒体启动：当前活动连接已切换 hconn={hconn_val:#x}")
            return
        if _stream_thread is not None and _stream_thread.is_alive():
            _log(f"忽略重复媒体启动 hconn={hconn_val:#x}")
            return
        audio_file = _audio_file_path
        video_file = _session_video_file
        _session_state = "IN_CALL"

    _cancel_connect_timer()

    t = threading.Thread(
        target=_audio_stream_worker,
        args=(hconn_val, audio_file),
        daemon=True,
        name="voip-audio-stream",
    )
    with _state_lock:
        _stream_thread = t
    t.start()

    if video_file:
        vt = threading.Thread(
            target=_video_stream_worker,
            args=(hconn_val, video_file),
            daemon=True,
            name="voip-video-stream",
        )
        with _state_lock:
            _video_thread = vt
        vt.start()
        _info("收到 0x2000，视频对讲建立，开始发送本地音频和视频")
    else:
        _info("收到 0x2000，音频对讲建立，开始收发音频")


def _audio_stream_worker(hconn_val: int, audio_file: str) -> None:
    hconn = ctypes.c_void_p(hconn_val)
    audio_format = AUDIO_FORMATS[_up_audio_format]
    source_kind = "mic" if _mic_capture is not None else "file"
    _info(
        "音频推流线程启动"
        f" role={_session_role}"
        f" source={source_kind}"
        f" format={audio_format.name}"
        f" codec={audio_format.codec}"
        f" sample_rate={audio_format.sample_rate}"
        f" media={audio_format.media}"
        f" flags={audio_format.flags}"
        f" file={audio_file}"
    )

    reader = None
    if _mic_capture is None:
        try:
            reader = AudioFileReader(audio_file, _up_audio_format, AUDIO_PKT_MS)
        except (OSError, ValueError) as e:
            _err(f"无法打开音频文件 {audio_file}: {e}")
            return

    audio_pts_ms  = 0
    wall_start_ms = int(time.monotonic() * 1000)
    sent_packets = 0
    sent_bytes = 0

    try:
        while not _stream_stop.is_set():
            if _mic_capture is not None:
                pcm16 = _mic_capture.read()
                audio_format = AUDIO_FORMATS[_up_audio_format]
                target_rate = audio_format.sample_rate
                pcm_target = pcm16
                if target_rate != 16000:
                    pcm_target, _ = audioop.ratecv(pcm16, 2, 1, 16000, target_rate, None)
                if audio_format.codec == "alaw":
                    pkt = alaw_encode(pcm_target)
                elif audio_format.codec == "pcm":
                    pkt = pcm_target
                else:
                    raise RuntimeError(f"VoIP 实时麦克风不支持编码 {audio_format.name}")
                duration_ms = float(AUDIO_PKT_MS)
            else:
                packet = reader.next_packet()
                if packet is None:
                    break
                pkt, duration_ms = packet.payload, packet.duration_ms

            # pacing
            elapsed  = int(time.monotonic() * 1000) - wall_start_ms
            wait_ms  = audio_pts_ms - elapsed
            if wait_ms > 2:
                time.sleep(wait_ms / 1000.0)
                if _stream_stop.is_set():
                    break

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
                _log("连接已关闭，退出推流")
                break
            if rc < 0:
                _err(
                    f"TiRtcSendAudioStream 失败 rc={rc} "
                    f"({sdk.TiRtcGetErrorStr(rc).decode()})"
                )
                break
            sent_packets += 1
            sent_bytes += len(pkt)
            audio_pts_ms += duration_ms
            if sent_packets == 1 or sent_packets % 100 == 0:
                wall_elapsed_ms = int(time.monotonic() * 1000) - wall_start_ms
                drift_ms = int(audio_pts_ms - wall_elapsed_ms)
                _info(
                    "VoIP 音频发送统计"
                    f" role={_session_role}"
                    f" packets={sent_packets}"
                    f" bytes={sent_bytes}"
                    f" last_pkt={len(pkt)}"
                    f" last_dur={duration_ms:.1f}ms"
                    f" audio_ts={int(audio_pts_ms)}ms"
                    f" wall={wall_elapsed_ms}ms"
                    f" drift={drift_ms}ms"
                )
    finally:
        _log("音频推流线程退出")


def _video_stream_worker(hconn_val: int, video_file: str) -> None:
    """循环发送本地视频文件，固定按 15fps 节奏。"""
    hconn = ctypes.c_void_p(hconn_val)
    _log(f"视频推流线程启动 file={video_file}")
    try:
        reader = VideoFileReader(video_file, _up_video_format)
    except (OSError, ValueError) as e:
        _err(f"无法打开视频文件 {video_file}: {e}")
        return

    video_pts_ms = 0.0
    wall_start_ms = int(time.monotonic() * 1000)
    first_video = True
    sent_frames = 0
    consecutive_errors = 0
    try:
        while not _stream_stop.is_set():
            result = reader.next_frame(force_key=_force_key_frame.is_set())
            _force_key_frame.clear()
            if result is None:
                _err("视频文件不包含可发送帧")
                break
            frame_data, is_key = result

            elapsed = int(time.monotonic() * 1000) - wall_start_ms
            wait_ms = video_pts_ms - elapsed
            if wait_ms > 2:
                time.sleep(wait_ms / 1000.0)
                if _stream_stop.is_set():
                    break

            fi = TIRTCFRAMEINFO()
            fi.stream_id = VIDEO_STREAM_ID
            fi.media = VIDEO_FORMATS[_up_video_format].media
            fi.flags = TIRTC_FRAME_FLAG_KEY_FRAME if (is_key or first_video) else 0
            fi.reserved = 0
            fi.ts = int(video_pts_ms) & 0xFFFFFFFF
            fi.length = len(frame_data)
            buf = (ctypes.c_uint8 * len(frame_data)).from_buffer_copy(frame_data)
            rc = sdk.TiRtcSendVideoStream(hconn, ctypes.byref(fi), buf)
            if rc in CONN_FATAL_ERRORS:
                _err(f"视频发送连接已关闭 rc={rc} ({sdk.TiRtcGetErrorStr(rc).decode()})")
                break
            if rc < 0:
                consecutive_errors += 1
                _err(f"TiRtcSendVideoStream 失败 rc={rc} ({sdk.TiRtcGetErrorStr(rc).decode()}) frame={sent_frames}")
                if consecutive_errors >= 10:
                    _err("视频连续发送失败 10 次，停止视频线程")
                    break
            else:
                consecutive_errors = 0
                sent_frames += 1
                if sent_frames == 1:
                    _info(f"VoIP 首个 {VIDEO_FORMATS[_up_video_format].name} 视频帧发送成功 bytes={len(frame_data)} key={is_key}")
                elif sent_frames % 150 == 0:
                    _log(f"VoIP 视频已发送 {sent_frames} 帧")
            first_video = first_video and rc < 0
            video_pts_ms += VIDEO_FRAME_MS
    finally:
        _log("视频推流线程退出")


def start_session(peer_id: str, token: str, audio_file: str,
                  with_video: bool | None = None,
                  session_role: str = "unknown",
                  cancel_on_connect: bool = False) -> None:
    global _session_state, _audio_file_path, _session_role, _session_video_file
    global _recv_file, _recv_video_file, _connect_cb_ref, _connect_timer
    global _whip_connect_timer
    global _recv_audio_path, _recv_video_path
    global _stream_stop, _cancel_on_connect, _session_generation
    if not peer_id or not token:
        raise ValueError("peer_id 和 token 不能为空")
    _cancel_connect_timer()
    selected_video_file = _video_file_path if (with_video is not False and _video_file_path) else ""
    selected_audio = AUDIO_FORMATS[_up_audio_format]
    selected_down_audio = AUDIO_FORMATS[_down_audio_format]

    with _state_lock:
        if _session_state != "IDLE":
            raise RuntimeError(f"start_session: 当前状态 {_session_state}，不能发起新会话")
        _session_generation += 1
        generation = _session_generation
        _session_state = "CONNECTING"
        _audio_file_path = audio_file
        _session_video_file = selected_video_file
        _session_role = session_role or "unknown"
        _cancel_on_connect = bool(cancel_on_connect)

    _info(
        "启动 VoIP 会话"
        f" role={_session_role}"
        f" up_audio={selected_audio.name}"
        f" down_audio={selected_down_audio.name}"
        f" with_video={'yes' if bool(selected_video_file) else 'no'}"
        f" cancel_on_connect={'yes' if cancel_on_connect else 'no'}"
        f" audio_file={audio_file}"
        f" video_file={selected_video_file or '-'}"
    )

    try:
        af, vf, audio_recv_path, video_recv_path = _prepare_receive_files(selected_video_file)
    except OSError as e:
        _err(f"无法创建接收文件: {e}")
        with _state_lock:
            _reset_session_state_locked()
        raise RuntimeError(f"无法创建 VoIP 接收文件: {e}") from e
    with _state_lock:
        _recv_file = af
        _recv_video_file = vf
        _recv_audio_path = audio_recv_path
        _recv_video_path = video_recv_path
    _info(f"接收音频 → {audio_recv_path}")
    if vf is not None:
        _info(f"接收视频 → {video_recv_path}")

    _stream_stop.clear()

    def connect_cb(error, hconn, user_data):
        global _active_hconn, _connect_timer, _whip_connect_timer
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value if hconn else None
        with _state_lock:
            is_current = (
                _session_generation == generation
                and _session_state == "CONNECTING"
            )
            _connect_cb_refs.pop(generation, None)
            whip_timer, _whip_connect_timer = _whip_connect_timer, None
        if whip_timer is not None:
            whip_timer.cancel()
        if not is_current:
            if error == 0 and hconn_val is not None:
                _log(f"忽略过期 WHIP 连接回调 hconn={hconn_val:#x}，主动断开")
                _disconnect_stale_after_callback(hconn_val)
            return

        if error != 0 or hconn_val is None:
            detail = (
                f"rc={error} ({sdk.TiRtcGetErrorStr(error).decode()})"
                if error != 0
                else "连接成功回调未返回 hconn"
            )
            _err(f"TiRtcWhipConnect 连接回调失败: {detail}")
            with _state_lock:
                if (
                    _session_generation != generation
                    or _session_state != "CONNECTING"
                ):
                    return
                _reset_session_state_locked()
            _close_receive_files()
            if _session_end_callback:
                _session_end_callback()
            return

        if not process_tirtc_runtime.bind_active_connection(
            ServiceKind.VOIP, hconn_val
        ):
            _warn(
                f"WHIP 连接完成时 VoIP 会话已切换 "
                f"hconn={hconn_val:#x}，丢弃连接")
            with _state_lock:
                if (
                    _session_generation == generation
                    and _session_state == "CONNECTING"
                ):
                    _reset_session_state_locked()
            _close_receive_files()
            _disconnect_stale_after_callback(hconn_val)
            return

        with _state_lock:
            if (
                _session_generation != generation
                or _session_state != "CONNECTING"
            ):
                stale_success = True
                cancel_after_connect = False
            else:
                stale_success = False
                cancel_after_connect = _cancel_on_connect
                _active_hconn = hconn_val
            if not stale_success and not cancel_after_connect:
                _connect_timer = threading.Timer(
                    CONNECT_ACK_TIMEOUT_SEC, _on_connect_ack_timeout, args=(hconn_val,)
                )
                _connect_timer.daemon = True
                _connect_timer.start()
        if stale_success:
            _log(f"WHIP 连接完成时会话已结束 hconn={hconn_val:#x}，主动断开")
            _disconnect_stale_after_callback(hconn_val)
            return

        if cancel_after_connect:
            _info(f"WHIP 连接成功 hconn={hconn_val:#x}，本次会话用于取消外呼，立即发送 0x2001 挂断")
        else:
            _info(f"WHIP 连接成功 hconn={hconn_val:#x}，等待平台下发 cmdw=0x2000 后再启动音视频线程")

        with _state_lock:
            still_current = (
                _session_generation == generation
                and _active_hconn == hconn_val
            )
        if not still_current:
            _disconnect_stale_after_callback(hconn_val)
            return

        if cancel_after_connect:
            def stop_cancelled_session():
                stop_session()
                # 显式取消会先把低层状态置为 IDLE，随后到达的 disconnected
                # 回调不会再触发生命周期通知，因此这里负责让协调器恢复推流。
                if _session_end_callback:
                    _session_end_callback()

            _callback_guard.defer(
                stop_cancelled_session,
                name="voip-cancel-on-connect",
            )

    _connect_cb_ref = ConnectCB(_tracked_callback(connect_cb))
    with _state_lock:
        _connect_cb_refs[generation] = _connect_cb_ref
    _log_whip_connect_params(peer_id, token)
    rc = sdk.TiRtcWhipConnect(peer_id.encode(), token.encode(), _connect_cb_ref, None)
    if rc != 0:
        _err(f"TiRtcWhipConnect 调用失败: rc={rc} ({sdk.TiRtcGetErrorStr(rc).decode()})")
        with _state_lock:
            _connect_cb_refs.pop(generation, None)
            still_current = (
                _session_generation == generation
                and _session_state == "CONNECTING"
            )
            if still_current:
                _reset_session_state_locked()
        if still_current:
            _cancel_connect_timer()
            _close_receive_files()
        raise RuntimeError(
            f"TiRtcWhipConnect 调用失败: rc={rc} "
            f"({sdk.TiRtcGetErrorStr(rc).decode()})"
        )
    with _state_lock:
        if (_session_generation == generation
                and _session_state == "CONNECTING"
                and _active_hconn is None):
            _whip_connect_timer = threading.Timer(
                WHIP_CONNECT_TIMEOUT_SEC,
                _on_whip_connect_timeout,
                args=(generation,),
            )
            _whip_connect_timer.daemon = True
            whip_timer = _whip_connect_timer
        else:
            whip_timer = None
    if whip_timer is not None:
        whip_timer.start()


def stop_session() -> None:
    global _session_state, _active_hconn

    with _state_lock:
        state     = _session_state
        hconn_val = _active_hconn
        if state == "DISCONNECTING":
            _log("stop_session: 正在断开，等待 SDK 回调完成")
            return
        if state != "IDLE":
            # 本地主动停止拥有清理权；先让断开回调失效，避免同步触发的
            # on_disconnected 与当前线程重复清理或丢失媒体线程引用。
            t = _stream_thread
            vt = _video_thread
            _session_state = "DISCONNECTING"
            _active_hconn = None
        else:
            t = None
            vt = None

    if state == "IDLE":
        _log("stop_session: 已是 IDLE，忽略")
        return

    _cancel_connect_timer()
    _stream_stop.set()

    if hconn_val is not None:
        _log(f"发送挂断信令 0x2001 hconn={hconn_val:#x}")
        _cmd_hangup = b'{"reason":0}'
        sdk.TiRtcSendCommand(ctypes.c_void_p(hconn_val), 0x2001, _cmd_hangup, len(_cmd_hangup))
        sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))

    join_worker_before_uninit(t, _warn, "VoIP 音频推流")
    join_worker_before_uninit(vt, _warn, "VoIP 视频推流")
    _close_receive_files()
    with _state_lock:
        if _session_state == "DISCONNECTING":
            _reset_session_state_locked()
    _info("stop_session 完成")


def reject_session(
    wx_app_id: str,
    wx_model_id: str,
    wx_session_token: str,
    wx_room_id: str,
    wx_payload: str,
    hangup_reason: int,
) -> None:
    body = json.dumps({
        "wx_app_id":        wx_app_id,
        "wx_model_id":      wx_model_id,
        "wx_session_token": wx_session_token,
        "wx_room_id":       wx_room_id,
        "wx_payload":       wx_payload or "",
        "hangup_reason":    hangup_reason,
    })
    _info(f"reject_session"
         f" wx_app_id={wx_app_id}"
         f" wx_model_id={wx_model_id}"
         f" wx_room_id={wx_room_id}"
         f" hangup_reason={hangup_reason}"
         f" wx_session_token={wx_session_token}"
         f" wx_payload={wx_payload!r}")

    cb_holder = []   # 局部列表：保持 wrapped 存活直到回调触发

    def cb(resp_body, user_data):
        if resp_body:
            _info(f"reject 响应: {resp_body.decode()}")
        cb_holder.clear()   # 回调触发后释放

    wrapped = ServiceReqCB(cb)
    cb_holder.append(wrapped)   # 保持存活

    rc = sdk.TiRtcServiceRequest(
        b"/v1/wxvoip/reject",
        body.encode(),
        None,
        wrapped,
        None,
    )
    if rc != 0:
        _err(f"TiRtcServiceRequest(reject) 返回 {rc}")
        cb_holder.clear()


def report_profile(voip_server: str, mqtt_token: str,
                   with_video: bool | None = None,
                   contacts_error_none: bool = False) -> list | None:
    """上报设备 VoIP profile，拉取并返回授权用户列表。

    POST /v1/voip/device/profile   Authorization: Bearer {mqtt_token}
    GET  /v1/voip/device/contacts  Authorization: Bearer {mqtt_token}
    """
    headers = {"Authorization": f"Bearer {mqtt_token}",
               "Content-Type": "application/json"}
    try:
        camera_rotation = int(os.getenv("VOIP_CAMERA_ROTATION", "0"))
    except ValueError:
        camera_rotation = 0
    if camera_rotation not in (0, 90, 180, 270):
        _warn("VOIP_CAMERA_ROTATION 仅支持 0/90/180/270，已回退为 0")
        camera_rotation = 0
    try:
        aspect_ratio = float(os.getenv("VOIP_ASPECT_RATIO", str(4 / 3)))
    except ValueError:
        aspect_ratio = 4 / 3
    if not math.isfinite(aspect_ratio) or aspect_ratio <= 0:
        _warn("VOIP_ASPECT_RATIO 必须大于 0，已回退为 4/3")
        aspect_ratio = 4 / 3

    def env_dimension(name: str, default: int) -> int:
        raw_value = os.getenv(name, str(default))
        try:
            value = int(raw_value)
        except ValueError:
            _warn(f"{name} 必须是正整数，已回退为 {default}")
            return default
        if value <= 0:
            _warn(f"{name} 必须是正整数，已回退为 {default}")
            return default
        return value

    screen_width = env_dimension("VOIP_SCREEN_WIDTH", 1280)
    screen_height = env_dimension("VOIP_SCREEN_HEIGHT", 720)

    def env_bool(name: str) -> bool:
        value = os.getenv(name, "false").strip().lower()
        if value in ("1", "true", "yes", "on"):
            return True
        if value not in ("0", "false", "no", "off"):
            _warn(f"{name} 必须是 true/false，已回退为 false")
        return False

    hor_mirror = env_bool("VOIP_HOR_MIRROR")
    vert_mirror = env_bool("VOIP_VERT_MIRROR")
    object_fit = os.getenv("VOIP_OBJECT_FIT", "").strip().lower()
    if object_fit not in ("", "fill", "contain"):
        _warn("VOIP_OBJECT_FIT 仅支持 fill/contain，已回退为微信默认值")
        object_fit = ""
    selected_has_video = has_video() if with_video is None else (with_video and bool(_video_file_path))
    if selected_has_video:
        profile = {
            "screen_width": screen_width, "screen_height": screen_height,
            "camera_rotation": camera_rotation,
            "aspect_ratio": aspect_ratio,
            "hor_mirror": hor_mirror,
            "vert_mirror": vert_mirror,
            "audio_rate": AUDIO_FORMATS[_down_audio_format].sample_rate,
            "audio_channels": 1,
            "up_video_mt": VIDEO_FORMATS[_up_video_format].codec,
            "down_video_mt": VIDEO_FORMATS[_down_video_format].codec,
            "down_audio_mt": AUDIO_FORMATS[_down_audio_format].codec,
            "no_video": False,
            "calling_timeout_sec": 30,
        }
    else:
        profile = {
            "screen_width": 1, "screen_height": 1,
            "camera_rotation": camera_rotation,
            "aspect_ratio": aspect_ratio,
            "hor_mirror": hor_mirror,
            "vert_mirror": vert_mirror,
            "audio_rate": AUDIO_FORMATS[_down_audio_format].sample_rate,
            "audio_channels": 1,
            "up_video_mt": "none", "down_video_mt": "none",
            "down_audio_mt": AUDIO_FORMATS[_down_audio_format].codec,
            "no_video": True,
            "calling_timeout_sec": 30,
        }
    if object_fit:
        profile["object_fit"] = object_fit
    try:
        r = http_trace.request("POST", f"{voip_server}/v1/voip/device/profile",
                               json=profile, headers=headers, timeout=10)
        resp = r.json() if r.headers.get("Content-Type", "").startswith("application/json") else {}
        if r.status_code == 200 and resp.get("code", -1) == 0:
            mode = (f"视频（{VIDEO_FORMATS[_up_video_format].name} + {_up_audio_format}）"
                    if selected_has_video else f"纯音频（{_up_audio_format}）")
            _info(f"上报 VoIP {mode} profile 成功")
        else:
            message = resp.get("msg") or "<响应体已省略>"
            _warn(f"上报 voip profile 失败（code={resp.get('code', r.status_code)}）: {message}")
    except requests.RequestException as e:
        _warn(f"上报 voip profile 异常: {e}")

    try:
        r = http_trace.request("GET", f"{voip_server}/v1/voip/device/contacts",
                               headers=headers, timeout=10)
        resp = r.json() if r.headers.get("Content-Type", "").startswith("application/json") else {}
        if r.status_code == 200 and resp.get("code", -1) == 0:
            auth_list = resp.get("data", {}).get("contacts", [])
            _info(f"授权用户列表（共 {len(auth_list)} 条）")
            return auth_list
        else:
            message = resp.get("msg") or "<响应体已省略>"
            _warn(f"拉取授权列表失败（code={resp.get('code', r.status_code)}）: {message}")
    except requests.RequestException as e:
        _warn(f"拉取授权列表异常: {e}")
    return None if contacts_error_none else []
