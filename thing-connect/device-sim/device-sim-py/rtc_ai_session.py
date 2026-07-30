#!/usr/bin/env python3
"""
rtc_ai_session.py — AI 对话状态机

管理状态：IDLE → CONNECTING → IN_CALL
实现 device_flow.connect_mqtt_blocking 期望的 handler 接口：
  on_call_incoming(payload)
  on_callers_update()
  on_call_cancel(payload)
"""

import threading
import datetime
import rtc_ai


def _ts() -> str:
    return datetime.datetime.now().strftime("%H:%M:%S.%f")[:-3]

def _info(msg):
    if rtc_ai._LOG_LEVEL <= 20:
        print(f"{_ts()} \033[0;32m[ai]\033[0m {msg}", flush=True)

def _warn(msg):
    if rtc_ai._LOG_LEVEL <= 30:
        print(f"{_ts()} \033[1;33m[ai]\033[0m {msg}", flush=True)

def _log(msg):
    if rtc_ai._LOG_LEVEL <= 10:
        print(f"{_ts()} \033[0;36m[ai]\033[0m {msg}", flush=True)


class AiCallState:
    """AI 对话状态机，实现 handler 接口供 device_flow 路由消息。"""

    def __init__(self, ai_server: str, device_id: str,
                 mqtt_token: str, ai_audio: str,
                 up_audio_format: str = "alaw_8khz",
                 down_audio_format: str = "alaw_8khz",
                 before_start=None, after_stop=None):
        self._ai_server  = ai_server
        self._device_id  = device_id
        self._mqtt_token = mqtt_token
        self._ai_audio   = ai_audio
        self._before_start = before_start or (lambda action: action())
        self._after_stop = after_stop or (lambda: None)
        if hasattr(rtc_ai, "configure_audio_formats"):
            rtc_ai.configure_audio_formats(up_audio_format, down_audio_format)
        rtc_ai.set_message_callback(self._on_ai_message)

    # ── device_flow handler 接口（AI 模式不处理 wx 消息）────────────────
    def on_call_incoming(self, payload: dict) -> None:
        pass

    def on_callers_update(self) -> None:
        pass

    def on_call_cancel(self, payload: dict) -> None:
        pass

    # ── AI 消息回调 ──────────────────────────────────────────────────────
    def _on_ai_message(self, method: str, params: dict, msg_id) -> None:
        if method == "caption":
            caption_type = params.get("caption_type", 0)
            text         = params.get("text", "")
            is_final     = params.get("is_final", False)
            prefix = "ASR" if caption_type == 0 else "TTS"
            suffix = " [final]" if is_final else ""
            _info(f"[{prefix}] {text}{suffix}")
        elif method == "round_start":
            _info("── 本轮对话开始 ──")
        elif method == "round_end":
            _info("── 用户发言结束，等待 AI 回复 ──")
        elif method == "end_session":
            _warn("AI 服务端结束会话")
            self._after_stop()
        elif method == "device_action":
            action = params.get("action", "")
            data   = params.get("data", {})
            _info(f"device_action: action={action} data={data}")

    # ── 命令循环 ─────────────────────────────────────────────────────────
    def run_cmd_loop(self, stop_event: threading.Event) -> None:
        """终端命令输入线程：aicall / hangup"""
        Y = "\033[1;33m"
        R = "\033[0m"
        print(f"{Y}[ai] ╔══════════════════════════════════════════════════╗{R}")
        print(f"{Y}[ai]   终端命令就绪：{R}")
        print(f"{Y}[ai]     aicall  — 发起 AI 对话{R}")
        print(f"{Y}[ai]     hangup  — 挂断对话{R}")
        print(f"{Y}[ai]     exit    — 退出程序{R}")
        print(f"{Y}[ai] ╚══════════════════════════════════════════════════╝{R}")
        while not stop_event.is_set():
            try:
                line = input().strip().lower()
            except EOFError:
                break
            if not line:
                continue

            if line == "aicall":
                self._do_aicall()

            elif line == "hangup":
                if not rtc_ai.is_active():
                    _warn(f"当前未在对话中（state={rtc_ai.get_state()}）")
                    continue
                rtc_ai.stop_session()
                self._after_stop()
                _info("挂断完成")

            elif line == "exit":
                _info("正在退出…")
                if rtc_ai.is_active():
                    rtc_ai.stop_session()
                stop_event.set()
                break

            else:
                _warn(f"未知命令: {line}（可用：aicall / hangup / exit）")

    def _do_aicall(self) -> None:
        if rtc_ai.is_active():
            _warn(f"已在对话中（state={rtc_ai.get_state()}），请先 hangup")
            return
        _info("获取 AI token…")
        creds = rtc_ai.get_ai_token(self._ai_server, self._mqtt_token, self._device_id)
        if not creds:
            _warn("获取 AI token 失败，取消")
            return
        peer_id = creds.get("peer_id", "")
        token   = creds.get("token", "")
        if not peer_id or not token:
            _warn(
                "AI token 响应缺少必要字段 "
                f"(peer_id={'yes' if peer_id else 'no'}, token={'yes' if token else 'no'})"
            )
            return
        _info(f"连接 AI 服务 peer_id={peer_id[:40]}…")
        self._before_start(lambda: rtc_ai.start_session(
            peer_id, token, self._ai_audio, self._device_id))

    def call(self) -> None:
        self._do_aicall()

    def hangup(self) -> None:
        if rtc_ai.is_active():
            rtc_ai.stop_session()
        self._after_stop()
