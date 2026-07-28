#!/usr/bin/env python3
from __future__ import annotations
"""rtc_ai.py — TiRTC AI 对话会话管理

接口：
  init_sdk(device_id, secret_key, endpoint=None)
  uninit_sdk()
  get_ai_token(ai_server, mqtt_token, device_id) -> dict
  start_session(peer_id, token, audio_file, device_id="")
  stop_session()
  is_active() -> bool
  get_state() -> str   # "IDLE" | "CONNECTING" | "IN_CALL"
  set_message_callback(cb)   # cb(method, params, msg_id)
  set_log_level(level)
"""

import ctypes
import json
import os
import requests
import http_trace
import sys
import threading
import time
import urllib.parse
import uuid

from audio_recorder import AudioRecorder
from media_file_reader import AudioFileReader
from media_formats import (
    AUDIO_FORMATS,
    ai_audio_descriptor,
    normalize_audio_format,
)
from sdk_callback_guard import SdkCallbackGuard, join_worker_before_uninit

import tirtc_sdk as sdk
from tirtc_sdk import (
    TIRTCFRAMEINFO, TIRTCCALLBACKS,
    OnEventCB, OnConnAcceptCB, OnConnErrCB, OnDisconnCB,
    OnAudioCB, OnVideoCB, OnMsgCB, OnCmdCB, OnKeyFrameCB,
    OnSubVideoCB, OnUnsubVideoCB, OnSubAudioCB, OnUnsubAudioCB,
    ConnectCB,
    TIRTC_EVENT_SYS_STARTED, TIRTC_EVENT_SYS_STOPPED,
    TIRTC_OPT_SERVICE_ENDPOINT, TIRTC_OPT_MAX_SEND_BUFFER,
    TIRTC_OPT_DEVICE_SECRET_KEY,
    AUDIO_STREAM_ID, TIRTC_AUDIO_ALAW, TIRTC_AUDIO_PCM,
    TIRTC_AUDIOSAMPLE_8K16B1C, TIRTC_AUDIOSAMPLE_16K16B1C,
    CONN_FATAL_ERRORS,
)

AUDIO_PKT_MS      = 20
AI_CMD            = 0x2100   # 探鸽平台保留的 AI 信令命令字
AI_AUDIO_STREAM_ID = 1    # AI 对话音频流 stream_id（与 VoIP 的 10 不同）
AI_CONNECT_TIMEOUT_SEC = 10.0
AI_START_RESPONSE_TIMEOUT_SEC = 10.0

_LOG_LEVEL = 10
_up_audio_format = "alaw_8khz"
_down_audio_format = "alaw_8khz"

def configure_audio_formats(up_format: str = "alaw_8khz",
                            down_format: str = "alaw_8khz") -> None:
    global _up_audio_format, _down_audio_format
    _up_audio_format = normalize_audio_format(up_format)
    _down_audio_format = normalize_audio_format(down_format)

def set_log_level(level: str) -> None:
    global _LOG_LEVEL
    _LOG_LEVEL = {"debug": 10, "info": 20, "warn": 30, "error": 40}.get(level.lower(), 10)

import datetime as _dt

def _ts() -> str:
    return _dt.datetime.now().strftime("%H:%M:%S.%f")[:-3]

def _log(msg):
    if _LOG_LEVEL <= 10:
        print(f"{_ts()} \033[0;36m[rtc_ai]\033[0m {msg}", flush=True)
def _info(msg):
    if _LOG_LEVEL <= 20:
        print(f"{_ts()} \033[0;32m[rtc_ai]\033[0m {msg}", flush=True)
def _warn(msg):
    if _LOG_LEVEL <= 30:
        print(f"{_ts()} \033[1;33m[rtc_ai]\033[0m {msg}", flush=True)
def _err(msg):
    if _LOG_LEVEL <= 40:
        print(f"{_ts()} \033[0;31m[rtc_ai]\033[0m {msg}", file=sys.stderr, flush=True)

# ── 模块状态 ──────────────────────────────────────────────────────────────
_sdk_running   = False
_sdk_started   = threading.Event()
_sdk_stopped   = threading.Event()
_cbs_ref: "TIRTCCALLBACKS | None" = None
_sdk_log_cb_ref = None   # 防止 GC
_callback_guard = SdkCallbackGuard()

# ── 会话状态 ──────────────────────────────────────────────────────────────
_state_lock    = threading.Lock()
_session_state = "IDLE"  # IDLE | CONNECTING | IN_CALL | DISCONNECTING
_active_hconn: "int | None" = None
_audio_file_path: "str | None" = None
_recv_recorder: AudioRecorder | None = None
_stream_thread: "threading.Thread | None" = None
_stream_stop   = threading.Event()
_connect_cb_ref: "ConnectCB | None" = None
_connect_cb_refs: "dict[int, ConnectCB]" = {}
_connect_timer: "threading.Timer | None" = None
_start_response_timer: "threading.Timer | None" = None
_session_generation = 0
_terminal_notified_generation = 0
_msg_callback: "callable | None" = None
_session_end_callback = None


def _cancel_session_timers_locked() -> None:
    global _connect_timer, _start_response_timer
    timers = (_connect_timer, _start_response_timer)
    _connect_timer = None
    _start_response_timer = None
    for timer in timers:
        if timer is not None:
            timer.cancel()


def _notify_session_end_once(generation: int) -> None:
    global _terminal_notified_generation
    with _state_lock:
        if (generation != _session_generation
                or _terminal_notified_generation == generation):
            return
        _terminal_notified_generation = generation
        callback = _session_end_callback
    if callback:
        callback()


def _fail_current_session(generation: int, reason: str) -> bool:
    """Deterministically retire one AI generation and notify its owner once."""
    global _session_state, _active_hconn, _stream_thread, _recv_recorder
    with _state_lock:
        if (generation != _session_generation
                or _session_state == "IDLE"):
            return False
        _cancel_session_timers_locked()
        hconn_val, _active_hconn = _active_hconn, None
        t, _stream_thread = _stream_thread, None
        recorder, _recv_recorder = _recv_recorder, None
        _session_state = "IDLE"
    _stream_stop.set()
    join_worker_before_uninit(t, _warn, "AI 音频推流")
    if recorder is not None:
        recorder.close()
    if hconn_val is not None:
        sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
    _warn(reason)
    _notify_session_end_once(generation)
    return True


def _fail_after_callback(generation: int, reason: str) -> None:
    """Leave the ctypes callback stack before disconnecting its SDK handle."""
    def retire():
        _callback_guard.wait_for_idle()
        _fail_current_session(generation, reason)

    threading.Thread(
        target=retire,
        daemon=True,
        name="ai-terminal-cleanup",
    ).start()


def _arm_connect_timeout(generation: int) -> None:
    global _connect_timer

    def expire():
        _fail_current_session(
            generation,
            f"等待 WHIP 连接回调超时（{AI_CONNECT_TIMEOUT_SEC:g}s），已结束 AI 会话",
        )

    with _state_lock:
        if (generation != _session_generation
                or _session_state != "CONNECTING"
                or _active_hconn is not None):
            return
        timer = threading.Timer(AI_CONNECT_TIMEOUT_SEC, expire)
        timer.daemon = True
        _connect_timer = timer
    timer.start()


def _arm_start_response_timeout(generation: int, hconn_val: int) -> None:
    global _start_response_timer

    def expire():
        _fail_current_session(
            generation,
            f"等待 AI start_session 响应超时"
            f"（{AI_START_RESPONSE_TIMEOUT_SEC:g}s），已结束 AI 会话",
        )

    with _state_lock:
        if (generation != _session_generation
                or _session_state != "CONNECTING"
                or _active_hconn != hconn_val):
            return
        timer = threading.Timer(AI_START_RESPONSE_TIMEOUT_SEC, expire)
        timer.daemon = True
        _start_response_timer = timer
    timer.start()


def _schedule_disconnect_after_callback(hconn_val: int) -> None:
    """SDK 回调返回后再断开，避免回调内重入 Disconnect。"""
    _stream_stop.set()

    def disconnect():
        global _session_state
        _callback_guard.wait_for_idle()
        with _state_lock:
            if _active_hconn != hconn_val or _session_state == "DISCONNECTING":
                return
            _session_state = "DISCONNECTING"
        rc = sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
        if rc != 0:
            _err(
                f"TiRtcDisconnect 失败 hconn={hconn_val:#x} "
                f"rc={rc} ({sdk.TiRtcGetErrorStr(rc).decode()})"
            )
            _handle_disconnect(hconn_val)

    threading.Thread(
        target=disconnect,
        daemon=True,
        name="ai-remote-disconnect",
    ).start()


def set_message_callback(cb) -> None:
    global _msg_callback
    _msg_callback = cb


def set_session_end_callback(callback) -> None:
    global _session_end_callback
    _session_end_callback = callback


def init_sdk(device_id: str, secret_key: str, endpoint: str | None = None, client_id: str = "") -> None:
    global _sdk_running, _cbs_ref

    if _sdk_running:
        _log("SDK 已运行，跳过重复初始化")
        return

    _sdk_started.clear()
    _sdk_stopped.clear()

    buf = ctypes.c_uint32(1024 * 1024)
    sdk.TiRtcSetOption(sdk.TIRTC_OPT_MAX_SEND_BUFFER, ctypes.byref(buf), ctypes.sizeof(buf))

    rc = sdk.TiRtcInit()
    if rc != 0:
        sys.exit(f"[rtc_ai] TiRtcInit failed: {sdk.TiRtcGetErrorStr(rc).decode()}")

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
        sdk.TiRtcSetOption(sdk.TIRTC_OPT_SERVICE_ENDPOINT, ctypes.c_char_p(ep_b), len(ep_b))

    sk = secret_key.encode()
    sdk.TiRtcSetOption(sdk.TIRTC_OPT_DEVICE_SECRET_KEY, ctypes.c_char_p(sk), len(sk))
    cid = (client_id or device_id).encode()
    sdk.set_client_id(cid)
    _cbs_ref = _build_callbacks()
    _device_id_b = sdk.device_id_for_start(device_id, secret_key)
    rc = sdk.TiRtcStart(_device_id_b, ctypes.byref(_cbs_ref))
    if rc != 0:
        sys.exit(f"[rtc_ai] TiRtcStart failed: {sdk.TiRtcGetErrorStr(rc).decode()}")

    _sdk_running = True
    ver = sdk.TiRtcGetVersion().decode()
    _info(f"TiRTC {ver} 启动中 device_id={device_id}，等待 SYS_STARTED…")
    _sdk_started.wait(timeout=10.0)
    _info("TiRTC SDK 已就绪")


def uninit_sdk() -> None:
    global _sdk_running, _connect_cb_ref
    if not _sdk_running:
        return
    stop_session()
    _sdk_running = False
    _callback_guard.wait_for_idle()
    sdk.TiRtcStop()
    _sdk_stopped.wait(timeout=8.0)
    _callback_guard.wait_for_idle()
    sdk.TiRtcUninit()
    with _state_lock:
        _cancel_session_timers_locked()
        _connect_cb_refs.clear()
        _connect_cb_ref = None
    _info("TiRTC SDK 已停止")


def _build_callbacks() -> TIRTCCALLBACKS:
    def on_event(event, data, length):
        if event == sdk.TIRTC_EVENT_SYS_STARTED:
            _sdk_started.set()
            _info("SYS_STARTED")
        elif event == sdk.TIRTC_EVENT_SYS_STOPPED:
            _sdk_stopped.set()
            _info("SYS_STOPPED")

    def on_conn_accepted(hconn):
        pass

    def on_conn_error(hconn, error):
        hval = ctypes.cast(hconn, ctypes.c_void_p).value
        _err(f"on_conn_error hconn={hval:#x} error={sdk.TiRtcGetErrorStr(error).decode()}")
        _schedule_disconnect_after_callback(hval)

    def on_disconnected(hconn):
        hval = ctypes.cast(hconn, ctypes.c_void_p).value
        _info(f"on_disconnected hconn={hval:#x}")
        _handle_disconnect(hval)

    def on_audio(hconn, pFi, data):
        fi = ctypes.cast(pFi, ctypes.POINTER(TIRTCFRAMEINFO)).contents
        if fi.stream_id != AI_AUDIO_STREAM_ID:
            _log(f"忽略非 AI 音频 stream_id={fi.stream_id}")
            return
        raw = (ctypes.c_uint8 * fi.length).from_address(
            ctypes.cast(data, ctypes.c_void_p).value
        )
        n = _recv_recorder.frame_count + 1 if _recv_recorder else 0
        if n == 1:
            _info(f"收到首帧下行 AI 音频 stream_id={fi.stream_id} length={fi.length}")
        elif n % 50 == 0:
            _log(f"下行 AI 音频累计 {n} 帧")
        _handle_audio(fi, bytes(raw))

    def on_command(hconn, cmdw, data, length):
        if cmdw != AI_CMD or data is None or length == 0:
            return
        try:
            raw_bytes = (ctypes.c_uint8 * length).from_address(
                ctypes.cast(data, ctypes.c_void_p).value
            )
            raw_str = bytes(raw_bytes).decode()
            msg = json.loads(raw_str)
        except Exception as e:
            _err(f"AI cmd JSON 解析失败: {e}")
            return
        _handle_ai_message(ctypes.cast(hconn, ctypes.c_void_p).value, msg)

    def on_video(hconn, pFi, data): pass
    def on_message(hconn, pFi, data): pass
    def on_request_key_frame(hconn, stream_id): pass
    def on_subscribe_video(hconn, stream_id): return 0
    def on_unsubscribe_video(hconn, stream_id): pass
    def on_subscribe_audio(hconn, stream_id):
        return 0
    def on_unsubscribe_audio(hconn, stream_id): pass

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


def _handle_ai_message(hconn_val: int, msg: dict) -> None:
    global _session_state, _stream_thread
    with _state_lock:
        if (_active_hconn != hconn_val
                or _session_state in ("IDLE", "DISCONNECTING")):
            _log(f"忽略过期 AI 消息 hconn={hconn_val:#x}")
            return
    method   = msg.get("method", "")
    params   = msg.get("params") or {}
    msg_id   = msg.get("id")
    is_resp  = ("result" in msg or "error" in msg) and "method" not in msg

    if is_resp:
        if "error" in msg:
            with _state_lock:
                generation = _session_generation
                if _start_response_timer is not None:
                    _start_response_timer.cancel()
            _err(f"start_session 失败: {msg['error']}")
            _fail_after_callback(
                generation, "AI start_session 被服务端拒绝，已结束会话")
            return
        result = msg.get("result", {})
        if not isinstance(result, dict):
            with _state_lock:
                generation = _session_generation
                if _start_response_timer is not None:
                    _start_response_timer.cancel()
            _err("start_session 响应 result 不是对象")
            _fail_after_callback(
                generation, "AI start_session 响应无效，已结束会话")
            return
        session_id = result.get("session_id", "")
        _info(f"start_session 成功 session_id={session_id}")
        _info(f"  服务端确认 input_audio={result.get('input_audio')} output_audio={result.get('output_audio')}")
        with _state_lock:
            if (_active_hconn != hconn_val
                    or _session_state != "CONNECTING"):
                return
            if _start_response_timer is not None:
                _start_response_timer.cancel()
            _session_state = "IN_CALL"
            hconn_v = _active_hconn
            af = _audio_file_path
            _stream_stop.clear()
            t = threading.Thread(
                target=_audio_stream_worker,
                args=(hconn_v, af),
                daemon=True,
                name="ai-audio-stream",
            )
            _stream_thread = t
        t.start()
        _info("AI 会话建立，开始收发音频")
        return

    if method not in ("caption", "round_start", "round_end"):
        _log(f"AI 消息 method={method} id={msg_id} params={params}")

    if _msg_callback:
        try:
            _msg_callback(method, params, msg_id)
        except Exception as e:
            _err(f"msg_callback 异常: {e}")

    if method == "end_session":
        _info("收到 end_session，关闭连接")
        _schedule_disconnect_after_callback(hconn_val)

    elif method == "device_action" and msg_id is not None:
        # 回复成功
        reply = json.dumps({"jsonrpc": "2.0", "id": msg_id, "result": {}}).encode()
        hconn = ctypes.c_void_p(hconn_val)
        sdk.TiRtcSendCommand(hconn, AI_CMD, reply, len(reply))


def _handle_audio(fi, data: bytes) -> None:
    if _recv_recorder is not None and _recv_recorder.is_open:
        try:
            _recv_recorder.write_frame(fi, data)
        except OSError as e:
            _err(f"写接收音频失败: {e}")


def _handle_disconnect(hconn_val: int) -> None:
    global _session_state, _active_hconn, _stream_thread, _recv_recorder
    with _state_lock:
        if _active_hconn != hconn_val:
            return
        generation = _session_generation
        _cancel_session_timers_locked()
        _session_state = "DISCONNECTING"
        _active_hconn  = None
        t, _stream_thread = _stream_thread, None
        r, _recv_recorder = _recv_recorder, None
    _stream_stop.set()

    join_worker_before_uninit(t, _warn, "AI 音频推流")
    if r is not None:
        r.close()

    with _state_lock:
        if _session_state == "DISCONNECTING":
            _session_state = "IDLE"
    _info("连接已断开，回到 IDLE")
    _notify_session_end_once(generation)


def get_ai_token(ai_server: str, mqtt_token: str, device_id: str) -> dict:
    """GET {ai_server}/v1/ai/token → {"peer_id": ..., "token": ...}"""
    url = f"{ai_server}/v1/ai/token"
    headers = {"Authorization": f"Bearer {mqtt_token}"}
    _log(f"获取 AI token  GET {url}")
    try:
        resp = http_trace.request("GET", url, headers=headers, timeout=10)
    except requests.RequestException as e:
        _err(f"获取 AI token 请求失败: {e}")
        return {}
    try:
        data = resp.json()
    except Exception as e:
        _err(f"获取 AI token 响应非 JSON (HTTP {resp.status_code}): {e}\n  body: {resp.text[:200]}")
        return {}
    if data.get("code") != 200:
        _err(f"获取 AI token 失败 code={data.get('code')} msg={data.get('msg')}")
        return {}
    _info("AI token 获取成功")
    return data["data"]  # {"peer_id": ..., "token": ...}


def get_state() -> str:
    with _state_lock:
        return _session_state


def is_active() -> bool:
    with _state_lock:
        return _session_state in ("CONNECTING", "IN_CALL")


def _audio_stream_worker(hconn_val: int, audio_file: str) -> None:
    hconn = ctypes.c_void_p(hconn_val)
    _log(f"音频推流线程启动 file={audio_file}")

    try:
        reader = AudioFileReader(audio_file, _up_audio_format, AUDIO_PKT_MS, loop=False)
    except (OSError, ValueError) as e:
        _err(f"无法打开音频文件 {audio_file}: {e}")
        hconn = ctypes.c_void_p(hconn_val)
        _handle_disconnect(hconn_val)
        sdk.TiRtcDisconnect(hconn)
        return

    audio_format = AUDIO_FORMATS[_up_audio_format]
    media, flags = audio_format.media, audio_format.flags
    audio_pts_ms  = 0
    wall_start_ms = int(time.monotonic() * 1000)

    try:
        while not _stream_stop.is_set():
            packet = reader.next_packet()
            if packet is None:
                _log("音频文件发送完毕，等待服务端 VAD…")
                break
            pkt, duration_ms = packet.payload, packet.duration_ms

            elapsed = int(time.monotonic() * 1000) - wall_start_ms
            wait_ms = audio_pts_ms - elapsed
            if wait_ms > 2:
                time.sleep(wait_ms / 1000.0)
                if _stream_stop.is_set():
                    break

            fi = TIRTCFRAMEINFO()
            fi.stream_id = AI_AUDIO_STREAM_ID
            fi.media     = media
            fi.flags     = flags
            fi.reserved  = 0
            fi.ts        = int(audio_pts_ms) & 0xFFFFFFFF
            fi.length    = len(pkt)

            buf = (ctypes.c_uint8 * len(pkt)).from_buffer_copy(pkt)
            rc = sdk.TiRtcSendAudioStream(hconn, ctypes.byref(fi), buf)
            if rc in CONN_FATAL_ERRORS:
                _log("连接已关闭，退出推流")
                break
            audio_pts_ms += duration_ms

    finally:
        _log("音频推流线程退出")


def _parse_peer_id(peer_id: str) -> dict:
    """从 peer_id URL 解析 query string 参数，如 role_id。"""
    if "?" in peer_id:
        qs = peer_id.split("?", 1)[1]
        return dict(urllib.parse.parse_qsl(qs))
    return {}


def start_session(peer_id: str, token: str, audio_file: str, device_id: str = "") -> None:
    global _session_state, _active_hconn, _audio_file_path
    global _recv_recorder, _connect_cb_ref, _stream_stop, _stream_thread
    global _session_generation

    with _state_lock:
        if _session_state != "IDLE":
            _err(f"start_session: 当前状态 {_session_state}，不能发起新会话")
            return
        _cancel_session_timers_locked()
        _session_generation += 1
        generation = _session_generation
        _session_state   = "CONNECTING"
        _audio_file_path = audio_file

    _DIR = os.path.dirname(os.path.abspath(__file__))
    recv_dir = os.path.join(_DIR, "received")
    ts = int(time.time())
    recorder = AudioRecorder(recv_dir, "", f"ai_{ts}.raw", _info, _warn)
    try:
        recv_path = recorder.open()
    except OSError as e:
        _err(f"无法创建接收文件: {e}")
        _fail_current_session(
            generation, f"无法创建 AI 接收文件，已结束会话: {e}")
        return
    with _state_lock:
        _recv_recorder = recorder
    _info(f"接收音频 → {recv_path}")

    _stream_stop.clear()

    _peer_params = _parse_peer_id(peer_id)
    role_id = _peer_params.get("role_id", "")
    _info(f"发起 AI 对话 device_id={device_id} role_id={role_id}")

    def connect_cb(error, hconn, user_data):
        global _active_hconn
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value if hconn else None
        with _state_lock:
            _connect_cb_refs.pop(generation, None)
            current = (
                generation == _session_generation
                and _session_state == "CONNECTING"
            )
            if current and _connect_timer is not None:
                _connect_timer.cancel()
        if not current:
            if error == 0 and hconn_val is not None:
                _log(f"忽略过期 AI 连接回调 hconn={hconn_val:#x}，主动断开")
                sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
            return

        if error != 0 or hconn_val is None:
            detail = (
                f"rc={error} ({sdk.TiRtcGetErrorStr(error).decode()})"
                if error != 0 else "成功回调未返回 hconn"
            )
            _err(f"TiRtcWhipConnect 失败: {detail}")
            _fail_current_session(
                generation, "AI WHIP 连接失败，已结束会话")
            return

        _info(f"WHIP 连接成功 hconn={hconn_val:#x}")

        with _state_lock:
            if (generation != _session_generation
                    or _session_state != "CONNECTING"):
                stale_success = True
            else:
                stale_success = False
                _active_hconn = hconn_val
        if stale_success:
            sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
            return

        def _send_start_session():
            time.sleep(0.3)  # 等 KCP 握手完成
            with _state_lock:
                if (
                    generation != _session_generation
                    or _active_hconn != hconn_val
                    or _session_state in ("IDLE", "DISCONNECTING")
                ):
                    return
            params = {
                "device_id": device_id,
                "role_id":   role_id,
                "input_audio": ai_audio_descriptor(_up_audio_format),
                "output_audio": ai_audio_descriptor(_down_audio_format),
            }
            envelope = {
                "jsonrpc": "2.0",
                "id": uuid.uuid4().hex,
                "method": "start_session",
                "params": params,
            }
            msg = json.dumps(envelope).encode()
            _log(f"发送 start_session device_id={device_id} role_id={role_id}")
            rc = sdk.TiRtcSendCommand(
                ctypes.c_void_p(hconn_val), AI_CMD, msg, len(msg))
            if rc != 0:
                _fail_current_session(
                    generation,
                    f"发送 AI start_session 失败 rc={rc}，已结束会话",
                )
                return
            _arm_start_response_timeout(generation, hconn_val)

        threading.Thread(target=_send_start_session, daemon=True, name="ai-start-session").start()
        _info("等待 start_session 响应后开始推流…")

    _connect_cb_ref = ConnectCB(_callback_guard.wrap(connect_cb))
    with _state_lock:
        _connect_cb_refs[generation] = _connect_cb_ref
    rc = sdk.TiRtcWhipConnect(peer_id.encode(), token.encode(), _connect_cb_ref, None)
    _log(f"TiRtcWhipConnect rc={rc}")
    if rc != 0:
        _err(f"TiRtcWhipConnect 调用失败: rc={rc} ({sdk.TiRtcGetErrorStr(rc).decode()})")
        with _state_lock:
            _connect_cb_refs.pop(generation, None)
        _fail_current_session(
            generation,
            f"TiRtcWhipConnect 调用失败 rc={rc}，已结束 AI 会话",
        )
        return
    _arm_connect_timeout(generation)


def stop_session() -> None:
    global _session_state, _active_hconn, _recv_recorder, _stream_thread

    with _state_lock:
        state     = _session_state
        hconn_val = _active_hconn
        if state == "DISCONNECTING":
            _log("stop_session: 正在断开，等待 SDK 回调完成")
            return
        if state != "IDLE":
            _cancel_session_timers_locked()
            _session_state = "DISCONNECTING"
            _active_hconn = None
            t, _stream_thread = _stream_thread, None
            r, _recv_recorder = _recv_recorder, None
        else:
            t = None
            r = None

    if state == "IDLE":
        _log("stop_session: 已是 IDLE，忽略")
        return

    _stream_stop.set()

    if hconn_val is not None:
        _log(f"发送 end_session hconn={hconn_val:#x}")
        msg = json.dumps({"jsonrpc": "2.0", "method": "end_session"}).encode()
        sdk.TiRtcSendCommand(ctypes.c_void_p(hconn_val), AI_CMD, msg, len(msg))
        sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))

    join_worker_before_uninit(t, _warn, "AI 音频推流")
    if r is not None:
        r.close()
    with _state_lock:
        if _session_state == "DISCONNECTING":
            _session_state = "IDLE"
    _info("stop_session 完成")
