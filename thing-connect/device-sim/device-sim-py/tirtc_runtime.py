#!/usr/bin/env python3
"""Process-wide TiRTC SDK lifecycle and generation-aware callback routing."""

from __future__ import annotations

import ctypes
from enum import Enum
import sys
import threading
from typing import Dict, NamedTuple

from callback_work_queue import CallbackWorkQueue
from sdk_callback_guard import SdkCallbackGuard
import tirtc_sdk as sdk
from tirtc_sdk import (
    OnAudioCB,
    OnCmdCB,
    OnConnAcceptCB,
    OnConnErrCB,
    OnDisconnCB,
    OnEventCB,
    OnKeyFrameCB,
    OnMsgCB,
    OnSubAudioCB,
    OnSubVideoCB,
    OnUnsubAudioCB,
    OnUnsubVideoCB,
    OnVideoCB,
    TIRTCCALLBACKS,
)


class ServiceKind(str, Enum):
    STREAM = "stream"
    VOIP = "voip"
    AI = "ai"
    CALL = "device-call"


class _ConnectionOwner(NamedTuple):
    service: ServiceKind
    generation: int


class TiRtcRuntime:
    """Own one SDK instance and route callbacks to the current business."""

    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._started_event = threading.Event()
        self._stopped_event = threading.Event()
        self._initialized = False
        self._start_submitted = False
        self._started = False
        self._active_service: ServiceKind | None = None
        self._active_generation = 0
        self._next_generation = 0
        self._handlers: Dict[ServiceKind, TIRTCCALLBACKS] = {}
        self._accepts_inbound: Dict[ServiceKind, bool] = {}
        self._service_guards: Dict[ServiceKind, SdkCallbackGuard] = {}
        self._connections: Dict[int, _ConnectionOwner] = {}
        self._callback_guard = SdkCallbackGuard()
        self._sdk_log_work = CallbackWorkQueue(
            "tirtc-sdk-log",
            self._write_sdk_log,
            lambda _message: None,
            maxsize=1024,
        )
        self._sdk_callbacks: TIRTCCALLBACKS | None = None
        self._sdk_log_callback = None
        self._log_level = 10

    def set_log_level(self, level: str) -> None:
        self._log_level = {
            "debug": 10, "info": 20, "warn": 30, "error": 40
        }.get(level.lower(), 10)

    def register_service(
        self,
        service: ServiceKind,
        callbacks: TIRTCCALLBACKS,
        *,
        accepts_inbound: bool,
        callback_guard: SdkCallbackGuard | None = None,
    ) -> None:
        with self._lock:
            if self._start_submitted:
                raise RuntimeError(
                    f"TiRTC 已启动，不能修改 {service.value} 回调")
            self._handlers[service] = callbacks
            self._accepts_inbound[service] = accepts_inbound
            if callback_guard is not None:
                self._service_guards[service] = callback_guard

    @property
    def started(self) -> bool:
        with self._lock:
            return self._started

    @property
    def active_service(self) -> ServiceKind | None:
        with self._lock:
            return self._active_service

    def start(
        self,
        device_id: str,
        secret_key: str,
        client_id: str,
        endpoint: str | None = None,
    ) -> None:
        if not device_id or not secret_key or not client_id:
            raise ValueError("device_id、secret_key、client_id 均不能为空")
        with self._lock:
            if self._started:
                return
            if self._start_submitted:
                raise RuntimeError("TiRTC SDK 正在启动")
            if not self._handlers:
                raise RuntimeError("没有注册 TiRTC 业务回调")
            self._started_event.clear()
            self._stopped_event.clear()
            service_guards = tuple(self._service_guards.values())

        self._callback_guard.start()
        for guard in service_guards:
            guard.start()
        self._sdk_log_work.start()
        try:
            send_buffer = ctypes.c_uint32(1024 * 1024)
            self._check(
                "TiRtcSetOption(MAX_SEND_BUFFER)",
                sdk.TiRtcSetOption(
                    sdk.TIRTC_OPT_MAX_SEND_BUFFER,
                    ctypes.byref(send_buffer),
                    ctypes.sizeof(send_buffer),
                ),
            )
            self._check("TiRtcInit", sdk.TiRtcInit())
            with self._lock:
                self._initialized = True

            sdk.TiRtcLogConfig(0, None, 0)
            sdk.TiRtcLogSetLevel(3)
            if self._log_level <= 10:
                self._sdk_log_callback = sdk.LogCB(self._on_sdk_log)
                sdk.TiRtcLogSetCallback(self._sdk_log_callback)
                sdk.TiRtcLogSetLevel(8)

            if endpoint:
                endpoint_bytes = endpoint.encode()
                self._check(
                    "TiRtcSetOption(SERVICE_ENDPOINT)",
                    sdk.TiRtcSetOption(
                        sdk.TIRTC_OPT_SERVICE_ENDPOINT,
                        ctypes.c_char_p(endpoint_bytes),
                        len(endpoint_bytes),
                    ),
                )

            secret_bytes = secret_key.encode()
            self._check(
                "TiRtcSetOption(DEVICE_SECRET_KEY)",
                sdk.TiRtcSetOption(
                    sdk.TIRTC_OPT_DEVICE_SECRET_KEY,
                    ctypes.c_char_p(secret_bytes),
                    len(secret_bytes),
                ),
            )
            self._check(
                "TiRtcSetOption(CLIENT_ID)",
                sdk.set_client_id(client_id.encode()),
            )

            self._sdk_callbacks = self._build_callbacks()
            rc = sdk.TiRtcStart(
                sdk.device_id_for_start(device_id, secret_key),
                ctypes.byref(self._sdk_callbacks),
            )
            self._check("TiRtcStart", rc)
            with self._lock:
                self._start_submitted = True
            self._info(
                f"TiRTC {sdk.TiRtcGetVersion().decode()} 启动中"
                "（进程级单实例）")
            if not self._started_event.wait(timeout=10.0):
                raise RuntimeError("等待 TiRTC SYS_STARTED 超时")
        except BaseException:
            self.stop()
            raise

    def stop(self) -> None:
        with self._lock:
            submitted = self._start_submitted
            initialized = self._initialized
            self._active_service = None
            self._next_generation += 1
            self._active_generation = self._next_generation

        if submitted:
            # Cut off new business dispatch first.  An in-flight runtime
            # callback can still enqueue service work, and that work can cause
            # one final callback through the unified table.  Drain all three
            # stages before stopping the native SDK.
            self._callback_guard.wait_for_all()
            with self._lock:
                service_guards = tuple(self._service_guards.values())
            for guard in service_guards:
                guard.wait_for_all()
            self._callback_guard.wait_for_all()
            rc = sdk.TiRtcStop()
            if rc != 0:
                self._warn(
                    f"TiRtcStop rc={rc} "
                    f"({sdk.TiRtcGetErrorStr(rc).decode()})")
            if not self._stopped_event.wait(timeout=8.0):
                self._warn("等待 TiRTC SYS_STOPPED 超时，继续执行反初始化")
            self._callback_guard.wait_for_all()
            for guard in service_guards:
                guard.wait_for_all()
            # A service's final deferred Disconnect may produce one last
            # callback through the unified table.
            self._callback_guard.wait_for_all()
        if initialized:
            sdk.TiRtcUninit()
            self._callback_guard.wait_for_all()

        with self._lock:
            service_guards = tuple(self._service_guards.values())
        for guard in service_guards:
            guard.close()
        self._callback_guard.close()
        self._sdk_log_work.stop()

        with self._lock:
            self._initialized = False
            self._start_submitted = False
            self._started = False
            self._connections.clear()
            self._sdk_callbacks = None
            self._sdk_log_callback = None

    def activate(self, service: ServiceKind) -> int:
        with self._lock:
            if not self._started:
                raise RuntimeError("TiRTC SDK 尚未启动")
            if service not in self._handlers:
                raise RuntimeError(f"{service.value} 未注册回调")
            if self._active_service is not None:
                raise RuntimeError(
                    f"{self._active_service.value} 业务仍处于激活状态")
            self._next_generation += 1
            self._active_generation = self._next_generation
            self._active_service = service
            generation = self._active_generation
        self._info(
            f"业务已激活 service={service.value} generation={generation}")
        return generation

    def deactivate(self, service: ServiceKind, generation: int) -> bool:
        with self._lock:
            if (
                self._active_service != service
                or self._active_generation != generation
            ):
                return False
            self._active_service = None
            self._next_generation += 1
            self._active_generation = self._next_generation
        self._info(
            f"业务已停用 service={service.value} generation={generation}")
        return True

    def is_current(self, service: ServiceKind, generation: int) -> bool:
        with self._lock:
            return (
                self._started
                and self._active_service == service
                and self._active_generation == generation
            )

    def bind_active_connection(
        self, service: ServiceKind, hconn
    ) -> bool:
        handle = self._handle_value(hconn)
        if handle is None:
            return False
        with self._lock:
            if not self._started or self._active_service != service:
                self._warn(
                    f"拒绝绑定非当前业务连接 service={service.value} "
                    f"hconn={handle:#x}")
                return False
            generation = self._active_generation
            self._connections[handle] = _ConnectionOwner(
                service, generation)
        self._debug(
            f"出站连接已绑定 service={service.value} "
            f"generation={generation} hconn={handle:#x}")
        return True

    def _current_handler(
        self, hconn, *, remove: bool = False
    ) -> tuple[TIRTCCALLBACKS | None, _ConnectionOwner | None]:
        handle = self._handle_value(hconn)
        if handle is None:
            return None, None
        with self._lock:
            owner = self._connections.get(handle)
            if remove:
                self._connections.pop(handle, None)
            if owner is None:
                return None, None
            current = (
                self._started
                and self._active_service == owner.service
                and self._active_generation == owner.generation
            )
            return (
                self._handlers.get(owner.service) if current else None,
                owner,
            )

    def _on_event(self, event, data, length) -> None:
        del data, length
        if event == sdk.TIRTC_EVENT_SYS_STARTED:
            with self._lock:
                self._started = True
            self._started_event.set()
            self._info("SDK 已启动，等待业务会话")
        elif event == sdk.TIRTC_EVENT_SYS_STOPPED:
            with self._lock:
                self._started = False
            self._stopped_event.set()
            self._info("SDK 已停止")
        else:
            self._warn(f"SDK 系统事件 event={event}")

    def _on_conn_accepted(self, hconn) -> None:
        handle = self._handle_value(hconn)
        with self._lock:
            service = self._active_service
            generation = self._active_generation
            accepted = (
                self._started
                and service is not None
                and self._accepts_inbound.get(service, False)
            )
            if accepted and handle is not None:
                self._connections[handle] = _ConnectionOwner(
                    service, generation)
                handler = self._handlers[service]
            else:
                handler = None
        if handler is None:
            self._warn(
                "拒绝无活动业务接收的入站连接"
                f" hconn={handle if handle is not None else 0:#x}")
            if handle is not None:
                self._disconnect_after_callback(handle)
            return
        self._debug(
            f"入站连接已绑定 service={service.value} "
            f"generation={generation} hconn={handle:#x}")
        self._call_void(handler.on_conn_accepted, hconn)

    def _on_conn_error(self, hconn, error) -> None:
        handler, owner = self._current_handler(hconn)
        if handler is not None:
            self._call_void(handler.on_conn_error, hconn, error)
            return
        handle = self._handle_value(hconn)
        self._debug(
            "丢弃迟到连接错误"
            f" service={owner.service.value if owner else 'unknown'}"
            f" generation={owner.generation if owner else 0}"
            f" hconn={handle if handle is not None else 0:#x} rc={error}")
        if handle is not None:
            self._disconnect_after_callback(handle)

    def _on_disconnected(self, hconn) -> None:
        handler, owner = self._current_handler(hconn, remove=True)
        if handler is not None:
            self._call_void(handler.on_disconnected, hconn)
            return
        handle = self._handle_value(hconn)
        self._debug(
            "清理迟到断连"
            f" service={owner.service.value if owner else 'unknown'}"
            f" generation={owner.generation if owner else 0}"
            f" hconn={handle if handle is not None else 0:#x}")

    def _on_audio(self, hconn, frame, data) -> None:
        self._dispatch_void("on_audio", hconn, frame, data)

    def _on_video(self, hconn, frame, data) -> None:
        self._dispatch_void("on_video", hconn, frame, data)

    def _on_message(self, hconn, frame, data) -> None:
        self._dispatch_void("on_message", hconn, frame, data)

    def _on_command(self, hconn, command, data, length) -> None:
        self._dispatch_void(
            "on_command", hconn, command, data, length)

    def _on_request_key_frame(self, hconn, stream_id) -> None:
        self._dispatch_void(
            "on_request_key_frame", hconn, stream_id)

    def _on_subscribe_video(self, hconn, stream_id) -> int:
        return self._dispatch_result(
            "on_subscribe_video", hconn, stream_id)

    def _on_unsubscribe_video(self, hconn, stream_id) -> None:
        self._dispatch_void(
            "on_unsubscribe_video", hconn, stream_id)

    def _on_subscribe_audio(self, hconn, stream_id) -> int:
        return self._dispatch_result(
            "on_subscribe_audio", hconn, stream_id)

    def _on_unsubscribe_audio(self, hconn, stream_id) -> None:
        self._dispatch_void(
            "on_unsubscribe_audio", hconn, stream_id)

    def _dispatch_void(self, name: str, hconn, *args) -> None:
        handler, _owner = self._current_handler(hconn)
        if handler is None:
            return
        self._call_void(getattr(handler, name), hconn, *args)

    def _dispatch_result(self, name: str, hconn, *args) -> int:
        handler, _owner = self._current_handler(hconn)
        if handler is None:
            return -1
        callback = getattr(handler, name)
        if not callback:
            return -1
        try:
            return int(callback(hconn, *args))
        except BaseException as exc:
            self._error(f"{name} 回调异常: {exc}")
            return -1

    def _call_void(self, callback, *args) -> None:
        if not callback:
            return
        try:
            callback(*args)
        except BaseException as exc:
            self._error(f"业务回调异常: {exc}")

    def _disconnect_after_callback(self, handle: int) -> None:
        with self._lock:
            if not self._started:
                return

        def disconnect() -> None:
            sdk.TiRtcDisconnect(ctypes.c_void_p(handle))

        self._callback_guard.defer(
            disconnect, name="rtc-runtime-disconnect")

    def _build_callbacks(self) -> TIRTCCALLBACKS:
        callbacks = TIRTCCALLBACKS()
        callbacks.on_event = OnEventCB(
            self._callback_guard.wrap(self._on_event))
        callbacks.on_conn_accepted = OnConnAcceptCB(
            self._callback_guard.wrap(self._on_conn_accepted))
        callbacks.on_conn_error = OnConnErrCB(
            self._callback_guard.wrap(self._on_conn_error))
        callbacks.on_disconnected = OnDisconnCB(
            self._callback_guard.wrap(self._on_disconnected))
        callbacks.on_audio = OnAudioCB(
            self._callback_guard.wrap(self._on_audio))
        callbacks.on_video = OnVideoCB(
            self._callback_guard.wrap(self._on_video))
        callbacks.on_message = OnMsgCB(
            self._callback_guard.wrap(self._on_message))
        callbacks.on_command = OnCmdCB(
            self._callback_guard.wrap(self._on_command))
        callbacks.on_request_key_frame = OnKeyFrameCB(
            self._callback_guard.wrap(self._on_request_key_frame))
        callbacks.on_subscribe_video = OnSubVideoCB(
            self._callback_guard.wrap(self._on_subscribe_video))
        callbacks.on_unsubscribe_video = OnUnsubVideoCB(
            self._callback_guard.wrap(self._on_unsubscribe_video))
        callbacks.on_subscribe_audio = OnSubAudioCB(
            self._callback_guard.wrap(self._on_subscribe_audio))
        callbacks.on_unsubscribe_audio = OnUnsubAudioCB(
            self._callback_guard.wrap(self._on_unsubscribe_audio))
        callbacks._cb_refs = [
            callbacks.on_event,
            callbacks.on_conn_accepted,
            callbacks.on_conn_error,
            callbacks.on_disconnected,
            callbacks.on_audio,
            callbacks.on_video,
            callbacks.on_message,
            callbacks.on_command,
            callbacks.on_request_key_frame,
            callbacks.on_subscribe_video,
            callbacks.on_unsubscribe_video,
            callbacks.on_subscribe_audio,
            callbacks.on_unsubscribe_audio,
        ]
        return callbacks

    def _on_sdk_log(self, data, length=None) -> None:
        if not data:
            return
        payload = (
            ctypes.string_at(data, length)
            if length is not None and length > 0
            else ctypes.string_at(data)
        )
        if payload:
            self._sdk_log_work.submit(payload)

    @staticmethod
    def _write_sdk_log(data: bytes) -> None:
        line = data.decode(errors="replace")
        print(f"\033[0;90m[TiRTC-SDK]\033[0m {line}", flush=True)

    @staticmethod
    def _handle_value(hconn) -> int | None:
        if hconn is None:
            return None
        if isinstance(hconn, int):
            return hconn or None
        return ctypes.cast(hconn, ctypes.c_void_p).value

    @staticmethod
    def _check(operation: str, rc: int) -> None:
        if rc != 0:
            detail = sdk.TiRtcGetErrorStr(rc).decode(errors="replace")
            raise RuntimeError(f"{operation} 失败: rc={rc} ({detail})")

    def _debug(self, message: str) -> None:
        if self._log_level <= 10:
            print(f"\033[0;36m[rtc-runtime]\033[0m {message}", flush=True)

    def _info(self, message: str) -> None:
        if self._log_level <= 20:
            print(f"\033[0;32m[rtc-runtime]\033[0m {message}", flush=True)

    def _warn(self, message: str) -> None:
        if self._log_level <= 30:
            print(f"\033[1;33m[rtc-runtime]\033[0m {message}", flush=True)

    def _error(self, message: str) -> None:
        if self._log_level <= 40:
            print(
                f"\033[0;31m[rtc-runtime]\033[0m {message}",
                file=sys.stderr,
                flush=True,
            )

    # Unit-test hook: exercises dispatch without starting the native SDK.
    def _test_mark_started(self) -> None:
        with self._lock:
            self._started = True


process_tirtc_runtime = TiRtcRuntime()
