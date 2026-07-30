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
from tirtc_runtime import (
    ServiceKind,
    process_tirtc_runtime,
)


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

    def __init__(
        self,
        config: RuntimeConfig,
        stream,
        voip,
        ai,
        call,
        sdk_runtime=process_tirtc_runtime,
    ):
        self.config = config
        self.stream = stream
        self.voip_module = voip
        self.ai_module = ai
        self.call_module = call
        self.sdk_runtime = sdk_runtime
        self._shutdown_lock = threading.Lock()
        self._shutdown = False
        self._lease_lock = threading.Lock()
        self._leases = {}
        self._rtc_generations = {}
        for module in (stream, voip, ai, call):
            module.set_log_level(config.log_level)
        voip.configure_video(config.up_video_file)
        voip.configure_receive_dir(config.down_media_dir)
        voip.configure_media_formats(
            config.up_audio_format, config.down_audio_format,
            config.up_video_format, config.down_video_format)
        configure_ai_receive_dir = getattr(ai, "configure_receive_dir", None)
        if callable(configure_ai_receive_dir):
            configure_ai_receive_dir(config.down_media_dir)
        self._ai_audio_file, self._ai_up_audio_format, self._ai_down_audio_format = \
            self._resolve_ai_defaults(config)
        self.sdk_runtime.set_log_level(config.log_level)
        self.sdk_runtime.register_service(
            ServiceKind.STREAM,
            stream.runtime_callbacks(),
            accepts_inbound=True,
            callback_guard=stream.callback_guard(),
        )
        self.sdk_runtime.register_service(
            ServiceKind.VOIP,
            voip.runtime_callbacks(),
            accepts_inbound=False,
            callback_guard=voip.callback_guard(),
        )
        self.sdk_runtime.register_service(
            ServiceKind.AI,
            ai.runtime_callbacks(),
            accepts_inbound=False,
            callback_guard=ai.callback_guard(),
        )
        self.sdk_runtime.register_service(
            ServiceKind.CALL,
            call.runtime_callbacks(),
            accepts_inbound=True,
            callback_guard=call.callback_guard(),
        )

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
            video_capable=bool(config.up_video_file),
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
            self.arbiter, self.voip, self.ai, self.call,
            video_capable=bool(config.up_video_file))

    def start(self) -> None:
        self._print_media_config()
        c = self.config
        try:
            self.sdk_runtime.start(
                c.device_id,
                c.device_key,
                c.client_id,
                c.tirtc_endpoint or None,
            )
            self._prime_voip_profile()
            self.coordinator.start_stream()
        except BaseException:
            try:
                self.shutdown()
            except BaseException as cleanup_error:
                print(
                    f"[device] 启动失败后的清理也失败: {cleanup_error}",
                    flush=True,
                )
            raise

    def shutdown(self) -> None:
        with self._shutdown_lock:
            if self._shutdown:
                return
            self._shutdown = True

        first_error = None
        try:
            self.message_handler.shutdown()
        except BaseException as exc:
            first_error = exc
        try:
            self.arbiter.shutdown()
        except BaseException as exc:
            if first_error is None:
                first_error = exc
        try:
            self.sdk_runtime.stop()
        except BaseException as exc:
            if first_error is None:
                first_error = exc
        if first_error is not None:
            raise first_error

    def run_cmd_loop(self, stop_event) -> None:
        self.terminal.run_cmd_loop(stop_event)

    def _start_stream(self) -> None:
        c = self.config
        generation = self._activate_runtime(
            SessionKind.STREAM, ServiceKind.STREAM)
        try:
            self.stream.configure_talkback(
                True, c.down_media_dir, c.device_id)
            self.stream.start_service(
                lambda: FileMediaSource(
                    c.up_video_file, c.up_audio_file,
                    audio_format=c.up_audio_format,
                    video_format=c.up_video_format),
            )
        except BaseException:
            self._deactivate_runtime(
                SessionKind.STREAM, ServiceKind.STREAM, generation)
            self.stream.stop_service()
            raise

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
        self._deactivate_runtime(
            SessionKind.STREAM, ServiceKind.STREAM)
        self.stream.stop_service()

    def _start_voip(self) -> None:
        c = self.config
        generation = self._activate_runtime(
            SessionKind.VOIP, ServiceKind.VOIP)
        try:
            self.voip_module.start_service(c.device_id)
            self.voip_module.configure_video(c.up_video_file)
            self.voip_module.configure_receive_dir(c.down_media_dir)
            if c.hardware_audio:
                self.voip_module.configure_hardware_audio(
                    True, fmt=c.up_audio_format)
        except BaseException:
            self._deactivate_runtime(
                SessionKind.VOIP, ServiceKind.VOIP, generation)
            self.voip_module.stop_service()
            raise

    def _stop_voip(self) -> None:
        self._deactivate_runtime(
            SessionKind.VOIP, ServiceKind.VOIP)
        self.voip_module.stop_service()

    def _start_ai(self) -> None:
        generation = self._activate_runtime(
            SessionKind.AI, ServiceKind.AI)
        try:
            self.ai_module.start_service()
        except BaseException:
            self._deactivate_runtime(
                SessionKind.AI, ServiceKind.AI, generation)
            self.ai_module.stop_service()
            raise

    def _stop_ai(self) -> None:
        self._deactivate_runtime(SessionKind.AI, ServiceKind.AI)
        self.ai_module.stop_service()

    def _start_call(self) -> None:
        c = self.config
        generation = self._activate_runtime(
            SessionKind.CALL, ServiceKind.CALL)
        try:
            self.call_module.start_service()
            if c.hardware_audio:
                self.call_module.configure_hardware_audio(
                    True, fmt=c.up_audio_format)
        except BaseException:
            self._deactivate_runtime(
                SessionKind.CALL, ServiceKind.CALL, generation)
            self.call_module.stop_service()
            raise

    def _stop_call(self) -> None:
        self._deactivate_runtime(SessionKind.CALL, ServiceKind.CALL)
        self.call_module.stop_service()

    def _activate_runtime(
        self, kind: SessionKind, service: ServiceKind
    ) -> int:
        generation = self.sdk_runtime.activate(service)
        self._rtc_generations[kind] = generation
        return generation

    def _deactivate_runtime(
        self,
        kind: SessionKind,
        service: ServiceKind,
        generation: int | None = None,
    ) -> None:
        active_generation = self._rtc_generations.pop(kind, None)
        if generation is not None:
            active_generation = generation
        if active_generation is not None:
            self.sdk_runtime.deactivate(service, active_generation)

    def _prime_voip_profile(self) -> None:
        """启动实时流前先上报 VoIP profile，确保微信回调能找到设备媒体能力。"""
        callers = self.voip_module.report_profile(self.config.voip_server, self.config.mqtt_token)
        if callers:
            self.voip.replace_callers(callers)

    def _finish_async(self, kind: SessionKind) -> None:
        """SDK 回调线程只提交业务结束，不在回调栈中切换媒体连接。"""
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
