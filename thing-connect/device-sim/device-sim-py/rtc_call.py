#!/usr/bin/env python3
from __future__ import annotations
"""rtc_call.py — TiRTC 设备间 P2P 通话（SDK 生命周期 + 连接状态机）

跟 rtc_voip.py 的关键区别：
  - rtc_voip.py 用 TiRtcWhipConnect（WHIP client 模式，接到微信 VoIP 中转）
  - 这里用 TiRtcConnect（真正的设备↔设备 P2P），双方都已经 TiRtcStart 常驻监听

角色区分（同一份代码，看谁先调 TiRtcConnect）：
  - 被叫（接听来电的一方）：拿到 call-server 签发的 token 后，
    主动 TiRtcConnect(caller_device_id, token) —— 走 connect_cb，成功后发 0x2000 接通确认
  - 主叫（发起呼叫的一方）：不调用任何 P2P 函数，只是已经 TiRtcStart 常驻监听，
    被叫连过来时走 on_conn_accepted —— 被动收到入站连接

媒体处理（音频格式、文件推流、硬件采集/播放）委托给 rtc_call_media.py。
"""

import ctypes
import json
import sys
import threading

import tirtc_sdk as sdk
from sdk_callback_guard import SdkCallbackGuard
from tirtc_sdk import (
    TIRTCCALLBACKS, TIRTCFRAMEINFO,
    OnEventCB, OnConnAcceptCB, OnConnErrCB, OnDisconnCB,
    OnAudioCB, OnVideoCB, OnMsgCB, OnCmdCB, OnKeyFrameCB,
    OnSubVideoCB, OnUnsubVideoCB, OnSubAudioCB, OnUnsubAudioCB,
    ConnectCB,
    TIRTC_EVENT_SYS_STARTED, TIRTC_EVENT_SYS_STOPPED,
    TIRTC_OPT_SERVICE_ENDPOINT, TIRTC_OPT_DEVICE_SECRET_KEY,
    TIRTC_E_BUSY, TIRTC_E_INVALID_HANDLE, TIRTC_E_CONN_CLOSED,
)

import rtc_call_media as _media


# ── 重新导出（向后兼容：rtc_call_session.py / device_sim_main.py 不感知拆分）─
AUDIO_FORMATS          = _media.AUDIO_FORMATS
configure_media        = _media.configure
configure_hardware_audio = _media.configure_hardware_audio


# ── 日志 ──────────────────────────────────────────────────────────────────────
# 注：rtc_call_session.py 直接读取 _LOG_LEVEL，因此必须保留为模块变量
_LOG_LEVEL = 10

def set_log_level(level: str) -> None:
    global _LOG_LEVEL
    _LOG_LEVEL = {"debug": 10, "info": 20, "warn": 30, "error": 40}.get(level.lower(), 10)
    _media.set_log_level(level)

def _log(msg):
    if _LOG_LEVEL <= 10:
        print(f"\033[0;36m[rtc_call]\033[0m {msg}", flush=True)
def _info(msg):
    if _LOG_LEVEL <= 20:
        print(f"\033[0;32m[rtc_call]\033[0m {msg}", flush=True)
def _warn(msg):
    if _LOG_LEVEL <= 30:
        print(f"\033[1;33m[rtc_call]\033[0m {msg}", flush=True)
def _err(msg):
    if _LOG_LEVEL <= 40:
        print(f"\033[0;31m[rtc_call]\033[0m {msg}", file=sys.stderr, flush=True)


# ── 模块状态 ──────────────────────────────────────────────────────────────────
_sdk_running = False
_sdk_started = threading.Event()
_sdk_stopped = threading.Event()
_cbs_ref: "TIRTCCALLBACKS | None" = None
_device_id_b: "bytes | None" = None
_callback_guard = SdkCallbackGuard()

_state_lock    = threading.Lock()
_session_state = "IDLE"          # IDLE | CONNECTING | IN_CALL | DISCONNECTING
_active_hconn: "int | None" = None
_connect_cb_ref: "ConnectCB | None" = None  # 防止 GC
_connect_cb_refs: "list[ConnectCB]" = []    # SDK 反初始化前保活超时重试的旧回调

_expected_room_id: "str | None" = None
_session_call_type = "video"
_on_p2p_connected_cb: "Callable[[str], None] | None" = None
_on_connect_failed_cb: "Callable[[], None] | None" = None
_session_end_callback = None


# ── SDK 生命周期 ──────────────────────────────────────────────────────────────

def init_sdk(device_id: str, secret_key: str, endpoint: str | None = None, client_id: str = "") -> None:
    global _sdk_running, _cbs_ref, _device_id_b

    if _sdk_running:
        _log("SDK 已运行，跳过重复初始化")
        return

    _sdk_started.clear()
    _sdk_stopped.clear()

    rc = sdk.TiRtcInit()
    if rc != 0:
        sys.exit(f"[rtc_call] TiRtcInit failed: {sdk.TiRtcGetErrorStr(rc).decode()}")

    sdk.TiRtcLogConfig(0, None, 0)
    sdk.TiRtcLogSetLevel(3)

    if endpoint:
        ep_b = endpoint.encode()
        sdk.TiRtcSetOption(sdk.TIRTC_OPT_SERVICE_ENDPOINT, ctypes.c_char_p(ep_b), len(ep_b))

    _cbs_ref = _build_callbacks()
    sk = secret_key.encode()
    sdk.TiRtcSetOption(sdk.TIRTC_OPT_DEVICE_SECRET_KEY, ctypes.c_char_p(sk), len(sk))
    cid = (client_id or device_id).encode()
    sdk.set_client_id(cid)
    _device_id_b = sdk.device_id_for_start(device_id, secret_key)
    rc = sdk.TiRtcStart(_device_id_b, ctypes.byref(_cbs_ref))
    if rc != 0:
        sys.exit(f"[rtc_call] TiRtcStart failed: {sdk.TiRtcGetErrorStr(rc).decode()}")

    _sdk_running = True
    ver = sdk.TiRtcGetVersion().decode()
    _info(f"TiRTC {ver} 启动中 device_id={device_id}，等待 SYS_STARTED…")
    _sdk_started.wait(timeout=10.0)
    _info("TiRTC SDK 已就绪，常驻监听入站连接")


def uninit_sdk() -> None:
    global _sdk_running, _connect_cb_ref
    hangup()
    _media.shutdown()
    if not _sdk_running:
        return
    _sdk_running = False
    _callback_guard.wait_for_idle()
    sdk.TiRtcStop()
    _sdk_stopped.wait(timeout=8.0)
    _callback_guard.wait_for_idle()
    sdk.TiRtcUninit()
    _connect_cb_ref = None
    _connect_cb_refs.clear()
    _info("TiRTC SDK 已停止")


# ── 状态访问 ──────────────────────────────────────────────────────────────────

def get_state() -> str:
    with _state_lock:
        return _session_state

def is_active() -> bool:
    with _state_lock:
        return _session_state in ("CONNECTING", "IN_CALL")

def set_expected_room(room_id: str) -> None:
    global _expected_room_id
    with _state_lock:
        _expected_room_id = room_id

def clear_expected_room() -> None:
    global _expected_room_id
    with _state_lock:
        _expected_room_id = None

def set_call_type(call_type: str) -> None:
    """Set media policy before either side can establish the P2P connection."""
    global _session_call_type
    normalized = (call_type or "video").lower()
    if normalized not in ("audio", "video"):
        raise ValueError("call_type must be audio or video")
    with _state_lock:
        _session_call_type = normalized
    _media.prepare_session(normalized == "video")

def clear_call_type() -> None:
    global _session_call_type
    with _state_lock:
        _session_call_type = "video"
    _media.reset_session()

def register_p2p_connected_cb(cb) -> None:
    global _on_p2p_connected_cb
    _on_p2p_connected_cb = cb

def register_connect_failed_cb(cb) -> None:
    global _on_connect_failed_cb
    _on_connect_failed_cb = cb

def set_session_end_callback(callback) -> None:
    global _session_end_callback
    _session_end_callback = callback


# ── 内部辅助 ──────────────────────────────────────────────────────────────────

def _handle_disconnect(hconn_val: int):
    global _session_state, _active_hconn
    with _state_lock:
        if _active_hconn != hconn_val:
            return
        _session_state = "DISCONNECTING"
        _active_hconn  = None
    _media.stop()
    _media.set_hconn(None)
    clear_call_type()
    with _state_lock:
        if _session_state == "DISCONNECTING":
            _session_state = "IDLE"
    _info("P2P 连接已断开，回到 IDLE")
    if _session_end_callback:
        _session_end_callback()


def _schedule_disconnect_after_callback(hconn_val: int) -> None:
    """SDK 回调返回后再断开，避免在 ctypes 回调栈中重入 SDK。"""
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
        name="call-remote-disconnect",
    ).start()


def _disconnect_stale_handle_after_callback(hconn_val: int) -> None:
    """断开超时后才成功的旧连接，但不触碰当前会话状态。"""
    def disconnect():
        _callback_guard.wait_for_idle()
        sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))

    threading.Thread(
        target=disconnect,
        daemon=True,
        name="call-stale-disconnect",
    ).start()

def _is_audio_call() -> bool:
    with _state_lock:
        return _session_call_type == "audio"


def _apply_video_downlink_policy(hconn_val: int) -> None:
    if not _is_audio_call():
        return
    rc = sdk.TiRtcUnsubscribeVideo(
        ctypes.c_void_p(hconn_val), sdk.VIDEO_STREAM_ID)
    if rc == 0:
        _info(
            f"纯音频设备通话已退订下行视频 "
            f"stream={sdk.VIDEO_STREAM_ID}"
        )
    else:
        _warn(
            f"退订下行视频失败 stream={sdk.VIDEO_STREAM_ID} "
            f"rc={rc} ({sdk.TiRtcGetErrorStr(rc).decode()})"
        )


def _schedule_video_downlink_policy_after_callback(hconn_val: int) -> None:
    """Return from the ctypes callback before calling back into the SDK."""
    def apply_policy():
        _callback_guard.wait_for_idle()
        with _state_lock:
            active = _active_hconn == hconn_val
        if active:
            _apply_video_downlink_policy(hconn_val)

    threading.Thread(
        target=apply_policy,
        daemon=True,
        name="call-video-downlink-policy",
    ).start()


# ── SDK 回调构建 ──────────────────────────────────────────────────────────────

def _build_callbacks() -> TIRTCCALLBACKS:
    def on_event(event, data, length):
        if event == sdk.TIRTC_EVENT_SYS_STARTED:
            _sdk_started.set()
            _info("SYS_STARTED")
        elif event == sdk.TIRTC_EVENT_SYS_STOPPED:
            _sdk_stopped.set()
            _info("SYS_STOPPED")

    def on_conn_accepted(hconn):
        global _session_state, _active_hconn
        hval = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            _session_state = "IN_CALL"
            _active_hconn  = hval
        _media.set_hconn(hval)
        _info(f"收到入站 P2P 连接 hconn={hval:#x}（对方已接听，等待 0x2000 接通确认）")
        _schedule_video_downlink_policy_after_callback(hval)

    def on_conn_error(hconn, error):
        hval = ctypes.cast(hconn, ctypes.c_void_p).value
        _err(f"on_conn_error hconn={hval:#x} error={sdk.TiRtcGetErrorStr(error).decode()}")
        _schedule_disconnect_after_callback(hval)

    def on_disconnected(hconn):
        hval = ctypes.cast(hconn, ctypes.c_void_p).value
        _log(f"on_disconnected hconn={hval:#x}")
        _handle_disconnect(hval)

    # ── 媒体回调：委托给 rtc_call_media ────────────────────────────────

    def on_audio(hconn, pFi, data):
        if not data:
            return
        try:
            fi = ctypes.cast(pFi, ctypes.POINTER(TIRTCFRAMEINFO)).contents
            buf = ctypes.string_at(data, fi.length)
            _media.on_audio_frame(fi, buf)
        except Exception:
            pass

    def on_video(hconn, pFi, data):
        if not data:
            return
        try:
            fi = ctypes.cast(pFi, ctypes.POINTER(TIRTCFRAMEINFO)).contents
            buf = ctypes.string_at(data, fi.length)
            _media.on_video_frame(buf)
        except Exception:
            pass

    def on_message(hconn, pFi, data):
        pass

    def on_command(hconn, cmdw, data, length):
        if cmdw == 0x2000:
            raw = ctypes.string_at(data, length) if data else b"{}"
            try:
                body = json.loads(raw) if raw else {}
            except json.JSONDecodeError:
                body = {}
            received_room = body.get("room_id", "")
            _info(f"收到 0x2000 room_id={received_room}")
            with _state_lock:
                expected = _expected_room_id
            if expected and received_room != expected:
                _warn(f"0x2000 room_id 不匹配（期望 {expected}，收到 {received_room}），断开")
                _schedule_disconnect_after_callback(
                    ctypes.cast(hconn, ctypes.c_void_p).value
                )
                return
            _media.start()
            if _on_p2p_connected_cb:
                _on_p2p_connected_cb(received_room)

    def on_request_key_frame(hconn, stream_id):
        hval = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            active = _active_hconn == hval
        if active:
            _media.request_video_key_frame(stream_id)

    def on_subscribe_video(hconn, stream_id):
        hval = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            active = _active_hconn == hval
        accepted = active and _media.subscribe_video(stream_id)
        _info(
            f"设备通话视频订阅 stream={stream_id} "
            f"{'已接受' if accepted else '已拒绝'}"
        )
        return 0 if accepted else -1

    def on_unsubscribe_video(hconn, stream_id):
        hval = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            active = _active_hconn == hval
        if active and _media.unsubscribe_video(stream_id):
            _info(f"对端已退订设备通话视频 stream={stream_id}；音频继续发送")

    def on_subscribe_audio(hconn, stream_id): return 0
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


# ── 连接管理 ──────────────────────────────────────────────────────────────────

def connect_to(remote_device_id: str, token: str, room_id: str,
               max_retries: int = 3, timeout_s: float = 10.0,
               call_type: "str | None" = None) -> None:
    """被叫侧：重试连接主叫，全部失败后触发 _on_connect_failed_cb。"""
    global _session_state, _active_hconn, _connect_cb_ref

    if call_type is not None:
        set_call_type(call_type)

    with _state_lock:
        if _session_state != "IDLE":
            _err(f"connect_to: 当前状态 {_session_state}，不能发起新连接")
            return
        _session_state = "CONNECTING"

    for attempt in range(1, max_retries + 1):
        _info(f"connect_to 尝试 {attempt}/{max_retries} remote={remote_device_id}")
        done = threading.Event()
        result_lock = threading.Lock()
        result: dict = {"error": None, "hconn": None, "expired": False}

        def connect_cb(error, hconn, user_data):
            hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value if hconn else None
            with result_lock:
                expired = result["expired"]
                if not expired:
                    result["error"] = error
                    result["hconn"] = hconn
                    done.set()
            if expired and error == 0 and hconn_val is not None:
                _warn(f"忽略超时后到达的旧连接 hconn={hconn_val:#x}，主动断开")
                _disconnect_stale_handle_after_callback(hconn_val)

        _connect_cb_ref = ConnectCB(_callback_guard.wrap(connect_cb))
        _connect_cb_refs.append(_connect_cb_ref)
        tk = token.encode() if attempt == 1 else None
        rc = sdk.TiRtcConnect(remote_device_id.encode(), tk, _connect_cb_ref, None)
        if rc == -40011:
            with result_lock:
                result["expired"] = True
            _err("TiRtcConnect 缓存已过期，停止重试")
            break
        if rc != 0:
            with result_lock:
                result["expired"] = True
            _err(f"TiRtcConnect 调用失败: rc={rc} ({sdk.TiRtcGetErrorStr(rc).decode()})")
            continue

        fired = done.wait(timeout=timeout_s)
        if not fired:
            with result_lock:
                # 处理超时边界上回调刚好完成的竞态。
                fired = done.is_set()
                if not fired:
                    result["expired"] = True
        if not fired:
            _err(f"connect_to 超时（{timeout_s}s），尝试 {attempt}/{max_retries}")
            continue

        if result["error"] != 0:
            _err(f"connect_to 回调失败: rc={result['error']} "
                 f"({sdk.TiRtcGetErrorStr(result['error']).decode()})")
            continue

        hconn = result["hconn"]
        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        with _state_lock:
            _session_state = "IN_CALL"
            _active_hconn  = hconn_val
        _media.set_hconn(hconn_val)
        _apply_video_downlink_policy(hconn_val)
        _info(f"P2P 连接成功 hconn={hconn_val:#x}，发送 0x2000 room_id={room_id}")
        body = json.dumps({"room_id": room_id}).encode()
        sdk.TiRtcSendCommand(hconn, 0x2000, body, len(body))
        _media.start()
        if _on_p2p_connected_cb:
            _on_p2p_connected_cb(room_id)
        return

    _err(f"connect_to 全部 {max_retries} 次失败")
    with _state_lock:
        _session_state = "IDLE"
        _active_hconn  = None
    _media.set_hconn(None)
    clear_call_type()
    if _on_connect_failed_cb:
        _on_connect_failed_cb()


def hangup() -> None:
    global _session_state, _active_hconn

    with _state_lock:
        state     = _session_state
        hconn_val = _active_hconn
        if state == "DISCONNECTING":
            _log("hangup: 正在断开，等待 SDK 回调完成")
            return
        if state != "IDLE":
            # 本地主动停止取得清理权，让同步到达的 on_disconnected 失效。
            _session_state = "DISCONNECTING"
            _active_hconn = None

    if state == "IDLE":
        clear_call_type()
        _log("hangup: 已是 IDLE，忽略")
        return

    _media.stop()
    _media.set_hconn(None)
    clear_call_type()

    if hconn_val is not None:
        sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))

    with _state_lock:
        if _session_state == "DISCONNECTING":
            _session_state = "IDLE"
    _info("hangup 完成")
