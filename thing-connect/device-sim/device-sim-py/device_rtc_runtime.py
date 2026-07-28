#!/usr/bin/env python3
"""设备 RTC 主干运行时。

向入口提供少量生命周期方法；各协议状态机和媒体实现留在各自模块中。
"""

from dataclasses import dataclass
import threading

from media_source import FileMediaSource
from rtc_ai_session import AiCallState
from rtc_call_session import CallState
from rtc_voip_session import VoipCallState
from session_arbiter import SessionArbiter
from session_coordinator import SessionAdapter, SessionCoordinator, SessionKind
from session_router import SessionMessageRouter, TerminalController


@dataclass(frozen=True)
class RuntimeConfig:
    device_id: str
    device_key: str
    client_id: str
    mqtt_token: str
    tirtc_endpoint: str
    voip_server: str
    ai_server: str
    call_server: str
    up_audio_file: str
    up_video_file: str
    down_media_dir: str
    up_audio_format: str = "alaw_8khz"
    down_audio_format: str = "alaw_8khz"
    up_video_format: str = "h264"
    down_video_format: str = "h264"
    hardware_audio: bool = False
    log_level: str = "debug"


class DeviceRtcRuntime:
    """组合四个清晰模块，实现实时流被通话临时抢占的主干流程。"""

    def __init__(self, config: RuntimeConfig, stream, voip, ai, call):
        self.config = config
        self.stream = stream
        self.voip_module = voip
        self.ai_module = ai
        self.call_module = call
        self._lease_lock = threading.Lock()
        self._leases = {}
        for module in (stream, voip, ai, call):
            module.set_log_level(config.log_level)
        voip.configure_video(config.up_video_file)
        voip.configure_receive_dir(config.down_media_dir)
        voip.configure_media_formats(
            config.up_audio_format, config.down_audio_format,
            config.up_video_format, config.down_video_format)
        self._ai_audio_file, self._ai_up_audio_format, self._ai_down_audio_format = \
            self._resolve_ai_defaults(config)

        adapters = {
            SessionKind.STREAM: SessionAdapter(self._start_stream, self._stop_stream),
            SessionKind.VOIP: SessionAdapter(self._start_voip, self._stop_voip),
            SessionKind.AI: SessionAdapter(self._start_ai, self._stop_ai),
            SessionKind.CALL: SessionAdapter(self._start_call, self._stop_call),
        }
        self.coordinator = SessionCoordinator(adapters)
        self.arbiter = SessionArbiter(self.coordinator)
        ai.set_session_end_callback(lambda: self._finish_async(SessionKind.AI))
        call.set_session_end_callback(lambda: self._finish_async(SessionKind.CALL))

        self.voip = VoipCallState(
            config.voip_server, config.device_id, config.mqtt_token,
            config.up_audio_file,
            before_start=lambda action: self._begin(
                SessionKind.VOIP, action),
            before_accept=lambda action: self._begin(
                SessionKind.VOIP, action, consume_pending=True),
            before_accept_ticket=lambda action, session_id: self._begin(
                SessionKind.VOIP, action, consume_pending=True,
                session_id=session_id),
            before_continue=lambda action: self._continue(
                SessionKind.VOIP, action),
            after_stop=lambda: self._finish_async(SessionKind.VOIP),
        )
        voip.set_session_end_callback(self.voip.on_session_end)
        self.ai = AiCallState(
            config.ai_server, config.device_id, config.mqtt_token,
            "" if config.hardware_audio else self._ai_audio_file,
            self._ai_up_audio_format, self._ai_down_audio_format,
            before_start=lambda action: self._begin(
                SessionKind.AI, action),
            after_stop=lambda: self._finish_async(SessionKind.AI),
        )
        self.call = CallState(
            config.call_server, config.device_id, config.mqtt_token,
            "" if config.hardware_audio else config.up_audio_file,
            config.up_video_file, config.down_media_dir, config.up_audio_format,
            config.up_video_format, config.down_video_format,
            before_start=lambda action: self._begin(
                SessionKind.CALL, action),
            before_accept=lambda action: self._begin(
                SessionKind.CALL, action, consume_pending=True),
            before_accept_ticket=lambda action, session_id: self._begin(
                SessionKind.CALL, action, consume_pending=True,
                session_id=session_id),
            before_continue=lambda action: self._continue(
                SessionKind.CALL, action),
            after_stop=lambda: self._finish_async(SessionKind.CALL),
        )
        self.message_handler = SessionMessageRouter(
            self.arbiter, self.voip, self.call)
        self.terminal = TerminalController(
            self.arbiter, self.voip, self.ai, self.call)

    def start(self) -> None:
        self._print_media_config()
        self._prime_voip_profile()
        self.coordinator.start_stream()

    def shutdown(self) -> None:
        self.message_handler.shutdown()
        self.arbiter.shutdown()

    def run_cmd_loop(self, stop_event) -> None:
        self.terminal.run_cmd_loop(stop_event)

    def _start_stream(self) -> None:
        c = self.config
        self.stream.configure_talkback(True, c.down_media_dir, c.device_id)
        self.stream.start(
            c.device_id, c.device_key,
            lambda: FileMediaSource(
                c.up_video_file, c.up_audio_file,
                audio_format=c.up_audio_format,
                video_format=c.up_video_format),
            c.tirtc_endpoint or None, client_id=c.client_id,
        )

    def _print_media_config(self) -> None:
        """在首次启动时打印实际选用的文件，便于确认默认素材与参数覆盖。"""
        c = self.config
        if c.hardware_audio:
            print(
                f"[device] 文件媒体配置: 视频={c.up_video_file} ({c.up_video_format})，"
                f"音频=本机麦克风/扬声器（本地 PCM 16k，"
                f"线上 {c.up_audio_format}/{c.down_audio_format}）",
                flush=True,
            )
            return

        print("[device] 文件媒体配置（循环读取）:", flush=True)
        print(
            f"[device]   推流 / VoIP / 设备互呼: "
            f"音频={c.up_audio_file} ({c.up_audio_format})，"
            f"视频={c.up_video_file or '未启用'} ({c.up_video_format})",
            flush=True,
        )
        print(
            f"[device]   AI 对讲: 音频={self._ai_audio_file} "
            f"({self._ai_up_audio_format})",
            flush=True,
        )

    def _stop_stream(self) -> None:
        self.stream.stop()

    def _start_voip(self) -> None:
        c = self.config
        self.voip_module.init_sdk(c.device_id, c.device_key,
                                  c.tirtc_endpoint or None, client_id=c.client_id)
        self.voip_module.configure_video(c.up_video_file)
        self.voip_module.configure_receive_dir(c.down_media_dir)
        if c.hardware_audio:
            self.voip_module.configure_hardware_audio(True, fmt=c.up_audio_format)
        self.voip.replace_callers(self.voip_module.report_profile(
            c.voip_server, c.mqtt_token))

    def _stop_voip(self) -> None:
        self.voip_module.stop_session()
        self.voip_module.uninit_sdk()

    def _start_ai(self) -> None:
        c = self.config
        self.ai_module.init_sdk(c.device_id, c.device_key,
                                c.tirtc_endpoint or None, client_id=c.client_id)

    def _stop_ai(self) -> None:
        self.ai_module.stop_session()
        self.ai_module.uninit_sdk()

    def _start_call(self) -> None:
        c = self.config
        self.call_module.init_sdk(c.device_id, c.device_key,
                                  c.tirtc_endpoint or None, client_id=c.client_id)
        if c.hardware_audio:
            self.call_module.configure_hardware_audio(True, fmt=c.up_audio_format)

    def _stop_call(self) -> None:
        self.call_module.hangup()
        self.call_module.uninit_sdk()

    def _prime_voip_profile(self) -> None:
        """启动实时流前先上报 VoIP profile，确保微信回调能找到设备媒体能力。"""
        callers = self.voip_module.report_profile(self.config.voip_server, self.config.mqtt_token)
        if callers:
            self.voip.replace_callers(callers)

    def _finish_async(self, kind: SessionKind) -> None:
        """SDK 回调线程不得直接 Stop/Uninit，交给独立生命周期线程。"""
        with self._lease_lock:
            lease = self._leases.get(kind)
        self.arbiter.finish_async(
            kind, lease.generation if lease is not None else None)

    def _store_lease(self, lease) -> None:
        with self._lease_lock:
            self._leases[lease.kind] = lease

    def _begin(self, kind: SessionKind, action, **kwargs):
        lease = self.arbiter.begin(
            kind, action, lease_ready=self._store_lease, **kwargs)
        self._store_lease(lease)
        return lease

    def _continue(self, kind: SessionKind, action, **kwargs):
        lease = self.arbiter.continue_session(
            kind, action, lease_ready=self._store_lease, **kwargs)
        self._store_lease(lease)
        return lease

    @staticmethod
    def _resolve_ai_defaults(config: RuntimeConfig) -> tuple[str, str, str]:
        """Keep AI, VoIP, live streaming, and device calls on one wire format."""
        return config.up_audio_file, config.up_audio_format, config.down_audio_format
