#!/usr/bin/env python3
from __future__ import annotations
"""rtc_ai_hw.py — TiRTC AI 对话会话管理（跨平台硬件音频版）

接口与 rtc_ai.py 完全兼容，区别：
  - 上行音频：sounddevice 麦克风采集（替代读文件）
  - 下行音频：sounddevice 扬声器播放（替代写文件）
  - 跨平台：Windows / macOS / Linux 通用

start_session() 的 audio_file 参数保留但忽略。
"""

import audioop
import ctypes
import json
import os
import queue
import sys
import threading
import time
import urllib.parse
import uuid

import requests
import http_trace
from g711 import alaw_decode, alaw_encode
from media_formats import (
    AUDIO_FORMATS,
    ai_audio_descriptor,
    normalize_audio_format,
)
from sdk_callback_guard import SdkCallbackGuard, join_worker_before_uninit

try:
    import sounddevice as sd
except ImportError:
    print("\033[1;33m⚠ sounddevice 未安装，无法使用硬件音频。请安装:\033[0m")
    print("  pip install sounddevice numpy soxr")
    raise

try:
    from rtc_echo_gate import EchoGate
    _ECHO_GATE_AVAILABLE = True
except ImportError:
    EchoGate = None
    _ECHO_GATE_AVAILABLE = False

# 半双工门控：远端有声时衰减近端麦克风（默认 30dB）
_ECHO_GATE_ACTIVE = True
_ECHO_GATE_ATTEN_DB = 24

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
    TIRTC_AUDIO_ALAW, TIRTC_AUDIO_PCM,
    TIRTC_AUDIOSAMPLE_16K16B1C,
    CONN_FATAL_ERRORS,
)

SAMPLE_RATE       = 16000  # 本地声卡和回声门控固定使用 PCM 16k
CHANNELS          = 1
DTYPE             = "int16"
AUDIO_PKT_MS      = 20
AUDIO_PKT_SIZE    = SAMPLE_RATE * 2 * CHANNELS * AUDIO_PKT_MS // 1000   # 640 字节
AI_PLAYBACK_PKT_MS = 40
AI_CMD            = 0x2100
AI_AUDIO_STREAM_ID = 1

# 下行播放队列：on_audio 回调 → 播放线程
_play_queue: "queue.Queue[tuple[bytes, int, int] | None]" = queue.Queue(maxsize=100)

# 音频设备（由 _select_audio_devices 在 init_sdk 时自动选定）
_mic_device: "int | None" = None
_spk_device: "int | None" = None

# 回声门控（远端有声时衰减近端麦克风）
_echo_gate = None

# 延迟计算：记录 round_end 时刻，收到本轮首帧下行时打印
_round_end_ts: "float | None" = None
_round_first_frame = False   # 每轮 round_end 后重置，收到首帧时打印延迟

_LOG_LEVEL = 10
_up_audio_format = "alaw_8khz"
_down_audio_format = "alaw_8khz"


def configure_audio_formats(up_format: str = "alaw_8khz",
                            down_format: str = "alaw_8khz") -> None:
    """Configure the AI wire format; sound devices remain PCM 16k locally."""
    global _up_audio_format, _down_audio_format
    up = normalize_audio_format(up_format)
    down = normalize_audio_format(down_format)
    for direction, name in (("上行", up), ("下行", down)):
        if AUDIO_FORMATS[name].codec not in ("pcm", "alaw"):
            raise ValueError(f"AI 硬件音频{direction}仅支持 pcm/g711a: {name}")
    _up_audio_format = up
    _down_audio_format = down

def set_log_level(level: str) -> None:
    global _LOG_LEVEL
    _LOG_LEVEL = {"debug": 10, "info": 20, "warn": 30, "error": 40}.get(level.lower(), 10)

def _ts() -> str:
    import datetime
    return datetime.datetime.now().strftime("%H:%M:%S.%f")[:-3]

def _log(msg):
    if _LOG_LEVEL <= 10:
        print(f"{_ts()} \033[0;36m[rtc_ai_hw]\033[0m {msg}", flush=True)
def _info(msg):
    if _LOG_LEVEL <= 20:
        print(f"{_ts()} \033[0;32m[rtc_ai_hw]\033[0m {msg}", flush=True)
def _warn(msg):
    if _LOG_LEVEL <= 30:
        print(f"{_ts()} \033[1;33m[rtc_ai_hw]\033[0m {msg}", flush=True)
def _err(msg):
    if _LOG_LEVEL <= 40:
        print(f"{_ts()} \033[0;31m[rtc_ai_hw]\033[0m {msg}", file=sys.stderr, flush=True)

# ── 音频设备自动选择 ──────────────────────────────────────────────────────

def _select_audio_devices() -> None:
    """使用 audio_device 选择麦克风/扬声器，跨平台通用。"""
    global _mic_device, _spk_device

    from audio_device import select_mic, select_speaker
    _mic_device = select_mic()
    _spk_device = select_speaker()

    _info(f"麦克风: [{_mic_device}]" if _mic_device is not None else "麦克风: 系统默认")
    _info(f"扬声器: [{_spk_device}]" if _spk_device is not None else "扬声器: 系统默认")
    gate_status = "回声门控已启用" if (_ECHO_GATE_AVAILABLE and _ECHO_GATE_ACTIVE) else "回声门控已禁用"
    _info(f"音频模式: 全双工 + {gate_status}")
    _warn("⚠ 建议将电脑音量调至 15% 左右，防止扬声器回声")


# ── 模块状态 ──────────────────────────────────────────────────────────────
_sdk_running    = False
_sdk_started    = threading.Event()
_sdk_stopped    = threading.Event()
_cbs_ref: "TIRTCCALLBACKS | None" = None
_sdk_log_cb_ref = None
_callback_guard = SdkCallbackGuard()

# ── 会话状态 ──────────────────────────────────────────────────────────────
_state_lock     = threading.Lock()
_session_state  = "IDLE"  # IDLE | CONNECTING | IN_CALL | DISCONNECTING
_active_hconn: "int | None" = None
_stream_thread: "threading.Thread | None" = None
_play_thread: "threading.Thread | None" = None
_stream_stop    = threading.Event()
_connect_cb_ref: "ConnectCB | None" = None
_msg_callback: "callable | None" = None
_session_end_callback = None
_audio_recv_count = 0


def _schedule_disconnect_after_callback(hconn_val: int) -> None:
    """SDK 回调返回后再断开，避免在 ctypes 回调栈中重入 SDK。"""
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
        name="ai-hw-remote-disconnect",
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

    _select_audio_devices()
    _sdk_started.clear()
    _sdk_stopped.clear()

    buf = ctypes.c_uint32(1024 * 1024)
    sdk.TiRtcSetOption(sdk.TIRTC_OPT_MAX_SEND_BUFFER, ctypes.byref(buf), ctypes.sizeof(buf))

    rc = sdk.TiRtcInit()
    if rc != 0:
        sys.exit(f"[rtc_ai_hw] TiRtcInit failed: {sdk.TiRtcGetErrorStr(rc).decode()}")

    sdk.TiRtcLogConfig(0, None, 0)
    sdk.TiRtcLogSetLevel(3)
    if _LOG_LEVEL <= 10:
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
        sys.exit(f"[rtc_ai_hw] TiRtcStart failed: {sdk.TiRtcGetErrorStr(rc).decode()}")

    _sdk_running = True
    ver = sdk.TiRtcGetVersion().decode()
    _info(f"TiRTC {ver} 启动中 device_id={device_id}，等待 SYS_STARTED…")
    _sdk_started.wait(timeout=10.0)
    _info("TiRTC SDK 已就绪")


def uninit_sdk() -> None:
    global _sdk_running
    if not _sdk_running:
        return
    stop_session()
    _sdk_running = False
    _callback_guard.wait_for_idle()
    sdk.TiRtcStop()
    _sdk_stopped.wait(timeout=8.0)
    _callback_guard.wait_for_idle()
    sdk.TiRtcUninit()
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
        global _audio_recv_count, _round_first_frame
        fi = ctypes.cast(pFi, ctypes.POINTER(TIRTCFRAMEINFO)).contents
        if fi.stream_id != AI_AUDIO_STREAM_ID:
            return
        raw = (ctypes.c_uint8 * fi.length).from_address(
            ctypes.cast(data, ctypes.c_void_p).value
        )
        _audio_recv_count += 1
        source_rate = 16000 if fi.flags == TIRTC_AUDIOSAMPLE_16K16B1C else 8000
        n = _audio_recv_count
        if _round_first_frame and _round_end_ts is not None:
            latency_ms = int((time.monotonic() - _round_end_ts) * 1000)
            _info(f"AI 延迟 {latency_ms} ms")
            _round_first_frame = False
        elif n == 1:
            _fmt_names = {getattr(sdk, a): a for a in dir(sdk) if a.startswith("TIRTC_AUDIO_")}
            _fmt_label = _fmt_names.get(fi.media, str(fi.media))
            _khz = 16 if fi.flags == TIRTC_AUDIOSAMPLE_16K16B1C else 8
            _info(f"收到AI音频格式: {_fmt_label} {_khz}kHz {fi.length}bytes/帧")
        if n % 50 == 0:
            _log(f"下行 AI 音频累计 {n} 帧")
        _handle_audio(bytes(raw), fi.media, source_rate)

    def on_command(hconn, cmdw, data, length):
        if cmdw != AI_CMD or data is None or length == 0:
            return
        try:
            raw_bytes = (ctypes.c_uint8 * length).from_address(
                ctypes.cast(data, ctypes.c_void_p).value
            )
            msg = json.loads(bytes(raw_bytes).decode())
        except Exception as e:
            _err(f"AI cmd JSON 解析失败: {e}")
            return
        _handle_ai_message(ctypes.cast(hconn, ctypes.c_void_p).value, msg)

    def on_video(hconn, pFi, data): pass
    def on_message(hconn, pFi, data): pass
    def on_request_key_frame(hconn, stream_id): pass
    def on_subscribe_video(hconn, stream_id): return 0
    def on_unsubscribe_video(hconn, stream_id): pass
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


def _handle_ai_message(hconn_val: int, msg: dict) -> None:
    global _echo_gate, _session_state, _stream_thread, _play_thread
    with _state_lock:
        if (_active_hconn != hconn_val
                or _session_state in ("IDLE", "DISCONNECTING")):
            _log(f"忽略过期 AI 消息 hconn={hconn_val:#x}")
            return
    method  = msg.get("method", "")
    params  = msg.get("params") or {}
    msg_id  = msg.get("id")
    is_resp = ("result" in msg or "error" in msg) and "method" not in msg

    if is_resp:
        if "error" in msg:
            _err(f"start_session 失败: {msg['error']}")
            return
        result = msg.get("result", {})
        session_id = result.get("session_id", "")
        _info(f"start_session 成功 session_id={session_id}")
        _info(f"  服务端确认 input_audio={result.get('input_audio')} output_audio={result.get('output_audio')}")
        # 创建回声门控
        if _ECHO_GATE_AVAILABLE and _ECHO_GATE_ACTIVE:
            _echo_gate = EchoGate.create(sample_rate=16000, frame_ms=20, attenuation_db=_ECHO_GATE_ATTEN_DB)
            _info(f"回声门控已启用 (衰减 {_ECHO_GATE_ATTEN_DB}dB)")
        with _state_lock:
            _session_state = "IN_CALL"
            hconn_v = _active_hconn
            _stream_stop.clear()
            # 清空旧队列
            while not _play_queue.empty():
                try:
                    _play_queue.get_nowait()
                except queue.Empty:
                    break
            st = threading.Thread(
                target=_mic_stream_worker,
                args=(hconn_v,),
                daemon=True,
                name="ai-mic-stream",
            )
            pt = threading.Thread(
                target=_speaker_play_worker,
                daemon=True,
                name="ai-speaker-play",
            )
            _stream_thread = st
            _play_thread   = pt
        st.start()
        pt.start()
        _info("AI 会话建立，开始麦克风采集 + 扬声器播放")
        return

    if method not in ("caption", "round_start", "round_end"):
        _log(f"AI 消息 method={method} id={msg_id} params={params}")

    if _msg_callback:
        try:
            _msg_callback(method, params, msg_id)
        except Exception as e:
            _err(f"msg_callback 异常: {e}")

    if method == "round_start":
        # AI 开始说话（AEC 全双工，无需暂停上行）
        _log("AI round_start")

    elif method == "round_end":
        # 用户开始说话；记录时刻用于计算 AI 延迟
        global _round_end_ts, _round_first_frame
        _round_end_ts    = time.monotonic()
        _round_first_frame = True

    elif method == "end_session":
        _info("收到 end_session，关闭连接")
        _schedule_disconnect_after_callback(hconn_val)

    elif method == "device_action" and msg_id is not None:
        reply = json.dumps({"jsonrpc": "2.0", "id": msg_id, "result": {}}).encode()
        sdk.TiRtcSendCommand(ctypes.c_void_p(hconn_val), AI_CMD, reply, len(reply))


def _handle_audio(data: bytes, media: int, sample_rate: int) -> None:
    try:
        _play_queue.put_nowait((data, media, sample_rate))
    except queue.Full:
        # 队列满时丢最旧的帧再入队，防止累积延迟
        try:
            _play_queue.get_nowait()
        except queue.Empty:
            pass
        try:
            _play_queue.put_nowait((data, media, sample_rate))
        except queue.Full:
            pass


def _handle_disconnect(hconn_val: int) -> None:
    global _session_state, _active_hconn, _stream_thread, _play_thread
    global _round_first_frame, _echo_gate
    with _state_lock:
        if _active_hconn != hconn_val:
            return
        _session_state = "DISCONNECTING"
        _active_hconn  = None
        t_stream, _stream_thread = _stream_thread, None
        t_play, _play_thread = _play_thread, None
    _stream_stop.set()
    if _echo_gate is not None:
        _echo_gate.close()
        _echo_gate = None
    _round_first_frame = False
    # 发送哨兵让播放线程退出
    try:
        _play_queue.put_nowait(None)
    except queue.Full:
        pass
    join_worker_before_uninit(t_stream, _warn, "AI 麦克风采集")
    join_worker_before_uninit(t_play, _warn, "AI 扬声器播放")
    with _state_lock:
        if _session_state == "DISCONNECTING":
            _session_state = "IDLE"
    _info("连接已断开，回到 IDLE")
    if _session_end_callback:
        _session_end_callback()


# _open_input / _open_output 已收敛到 audio_device.open_input_stream / open_output_stream


def _mic_stream_worker(hconn_val: int) -> None:
    """Capture local PCM 16k, then transcode to the negotiated AI wire format."""
    hconn  = ctypes.c_void_p(hconn_val)
    device = _mic_device if _mic_device is not None else sd.default.device[0]

    try:
        from audio_device import open_input_stream
        mic, actual = open_input_stream(device, SAMPLE_RATE, CHANNELS, AUDIO_PKT_MS)
        rate_note = f"{actual}Hz" if actual == SAMPLE_RATE else f"{actual}Hz → 重采样到 {SAMPLE_RATE}Hz"
        _info(f"麦克风采集线程启动  设备=[{device}] {sd.query_devices(device)['name']}  {rate_note}")
        wire = AUDIO_FORMATS[_up_audio_format]
        _info(
            f"发送音频格式: {_up_audio_format} codec={wire.codec} "
            f"sample_rate={wire.sample_rate}Hz"
        )

        capture_resample_state = None
        wire_resample_state = None
        audio_ts_ms = 0
        with mic:
            frames = actual * AUDIO_PKT_MS // 1000
            while not _stream_stop.is_set():
                pkt, overflowed = mic.read(frames)
                if overflowed:
                    _warn("麦克风输入溢出")

                raw = bytes(pkt)
                if actual != SAMPLE_RATE:
                    raw, capture_resample_state = audioop.ratecv(
                        raw, 2, CHANNELS, actual, SAMPLE_RATE,
                        capture_resample_state,
                    )

                # 回声门控（替代 AEC）
                if _echo_gate is not None:
                    fb = _echo_gate.frame_bytes
                    if len(raw) < fb:
                        raw = raw + b'\x00' * (fb - len(raw))
                    elif len(raw) > fb:
                        raw = raw[:fb]
                    raw = _echo_gate.process(raw)
                # 对齐至 AUDIO_PKT_SIZE
                if len(raw) < AUDIO_PKT_SIZE:
                    raw = raw + b'\x00' * (AUDIO_PKT_SIZE - len(raw))
                elif len(raw) > AUDIO_PKT_SIZE:
                    raw = raw[:AUDIO_PKT_SIZE]

                wire_data = raw
                if wire.sample_rate != SAMPLE_RATE:
                    wire_data, wire_resample_state = audioop.ratecv(
                        wire_data, 2, CHANNELS, SAMPLE_RATE,
                        wire.sample_rate, wire_resample_state,
                    )
                if wire.codec == "alaw":
                    wire_data = alaw_encode(wire_data)

                fi = TIRTCFRAMEINFO()
                fi.stream_id = AI_AUDIO_STREAM_ID
                fi.media     = wire.media
                fi.flags     = wire.flags
                fi.reserved  = 0
                fi.ts        = audio_ts_ms & 0xFFFFFFFF
                fi.length    = len(wire_data)

                buf = (ctypes.c_uint8 * len(wire_data)).from_buffer_copy(wire_data)
                rc = sdk.TiRtcSendAudioStream(hconn, ctypes.byref(fi), buf)
                if rc in CONN_FATAL_ERRORS:
                    _log("连接已关闭，退出麦克风采集")
                    break
                audio_ts_ms += AUDIO_PKT_MS

    except sd.PortAudioError as e:
        _err(f"麦克风打开失败: {e}")
    finally:
        _log("麦克风采集线程退出")


def _speaker_play_worker() -> None:
    """Decode and play each 40 ms AI downlink frame without truncation."""
    device = _spk_device if _spk_device is not None else sd.default.device[1]

    try:
        from audio_device import open_output_stream
        spk, actual = open_output_stream(
            device, SAMPLE_RATE, CHANNELS, AI_PLAYBACK_PKT_MS,
        )
        rate_note = f"{actual}Hz" if actual == SAMPLE_RATE else f"{SAMPLE_RATE}Hz → 重采样到 {actual}Hz"
        _info(f"扬声器播放线程启动  设备=[{device}] {sd.query_devices(device)['name']}  {rate_note}")

        playback_resample_state = None
        playback_resample_rates = None
        frames  = actual * AI_PLAYBACK_PKT_MS // 1000
        target_bytes = frames * 2
        silence = b'\x00' * target_bytes

        with spk:
            while True:
                try:
                    item = _play_queue.get(timeout=AI_PLAYBACK_PKT_MS / 1000)
                except queue.Empty:
                    if _stream_stop.is_set():
                        break
                    spk.write(silence)
                    continue
                if item is None:
                    break
                data, media, source_rate = item
                if media == TIRTC_AUDIO_ALAW:
                    data = alaw_decode(data)
                elif media != TIRTC_AUDIO_PCM:
                    _warn(f"AI 下行音频编码暂不支持播放 media={media}")
                    continue

                if _echo_gate is not None:
                    _echo_gate.feed_far_end(data, source_rate=source_rate)

                if source_rate != actual:
                    rates = (source_rate, actual)
                    if playback_resample_rates != rates:
                        playback_resample_state = None
                        playback_resample_rates = rates
                    data, playback_resample_state = audioop.ratecv(
                        data, 2, CHANNELS, source_rate, actual,
                        playback_resample_state,
                    )
                else:
                    playback_resample_state = None
                    playback_resample_rates = None
                if len(data) < target_bytes:
                    data += b'\x00' * (target_bytes - len(data))
                spk.write(data)
    except sd.PortAudioError as e:
        _err(f"扬声器打开失败: {e}")
    finally:
        _log("扬声器播放线程退出")


def get_ai_token(ai_server: str, mqtt_token: str, device_id: str) -> dict:
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
    return data["data"]


def get_state() -> str:
    with _state_lock:
        return _session_state


def is_active() -> bool:
    with _state_lock:
        return _session_state in ("CONNECTING", "IN_CALL")


def _parse_peer_id(peer_id: str) -> dict:
    if "?" in peer_id:
        qs = peer_id.split("?", 1)[1]
        return dict(urllib.parse.parse_qsl(qs))
    return {}


def start_session(peer_id: str, token: str, audio_file: str = "", device_id: str = "") -> None:
    global _session_state, _active_hconn
    global _connect_cb_ref, _stream_stop, _stream_thread, _play_thread, _audio_recv_count

    _audio_recv_count = 0

    with _state_lock:
        if _session_state != "IDLE":
            _err(f"start_session: 当前状态 {_session_state}，不能发起新会话")
            return
        _session_state = "CONNECTING"

    _stream_stop.clear()

    _peer_params = _parse_peer_id(peer_id)
    role_id = _peer_params.get("role_id", "")
    _info(f"发起 AI 对话 device_id={device_id} role_id={role_id}（麦克风模式）")

    def connect_cb(error, hconn, user_data):
        global _session_state, _active_hconn
        if error != 0:
            _err(f"TiRtcWhipConnect 失败: rc={error} ({sdk.TiRtcGetErrorStr(error).decode()})")
            with _state_lock:
                _session_state = "IDLE"
            return

        hconn_val = ctypes.cast(hconn, ctypes.c_void_p).value
        _info(f"WHIP 连接成功 hconn={hconn_val:#x}")
        with _state_lock:
            _active_hconn = hconn_val

        def _send_start_session():
            time.sleep(0.3)
            with _state_lock:
                if (
                    _active_hconn != hconn_val
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
            sdk.TiRtcSendCommand(ctypes.c_void_p(hconn_val), AI_CMD, msg, len(msg))

        threading.Thread(target=_send_start_session, daemon=True, name="ai-start-session").start()
        _info("等待 start_session 响应后开始采集…")

    _connect_cb_ref = ConnectCB(_callback_guard.wrap(connect_cb))
    rc = sdk.TiRtcWhipConnect(peer_id.encode(), token.encode(), _connect_cb_ref, None)
    _log(f"TiRtcWhipConnect rc={rc}")
    if rc != 0:
        _err(f"TiRtcWhipConnect 调用失败: rc={rc} ({sdk.TiRtcGetErrorStr(rc).decode()})")
        with _state_lock:
            _session_state = "IDLE"


def stop_session() -> None:
    global _session_state, _active_hconn, _stream_thread, _play_thread

    with _state_lock:
        state     = _session_state
        hconn_val = _active_hconn
        if state == "DISCONNECTING":
            _log("stop_session: 正在断开，等待 SDK 回调完成")
            return
        if state != "IDLE":
            _session_state = "DISCONNECTING"
            _active_hconn = None
            t_stream, _stream_thread = _stream_thread, None
            t_play, _play_thread = _play_thread, None
        else:
            t_stream = None
            t_play = None

    if state == "IDLE":
        _log("stop_session: 已是 IDLE，忽略")
        return

    global _round_first_frame, _echo_gate
    _stream_stop.set()
    if _echo_gate is not None:
        _echo_gate.close()
        _echo_gate = None
    _round_first_frame = False

    # 发送哨兵让播放线程退出
    try:
        _play_queue.put_nowait(None)
    except queue.Full:
        pass

    # 先等采集线程退出，再断开连接（避免 TiRtcSendAudioStream 访问已释放的 handle）
    join_worker_before_uninit(t_stream, _warn, "AI 麦克风采集")
    join_worker_before_uninit(t_play, _warn, "AI 扬声器播放")

    if hconn_val is not None:
        _log(f"发送 end_session hconn={hconn_val:#x}")
        try:
            msg = json.dumps({"jsonrpc": "2.0", "method": "end_session"}).encode()
            sdk.TiRtcSendCommand(ctypes.c_void_p(hconn_val), AI_CMD, msg, len(msg))
            sdk.TiRtcDisconnect(ctypes.c_void_p(hconn_val))
        except OSError:
            pass  # handle 可能已失效

    with _state_lock:
        if _session_state == "DISCONNECTING":
            _session_state = "IDLE"

    _info("stop_session 完成")
