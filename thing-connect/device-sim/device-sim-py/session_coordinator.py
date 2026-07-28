#!/usr/bin/env python3
"""TiRTC SDK 生命周期协调器。

本模块只执行 SessionArbiter 已决定的生命周期切换，不处理业务优先级、
MQTT 协议、音视频帧、采集设备或文件读写。
"""

from dataclasses import dataclass
from enum import Enum
import threading
from typing import Callable, Dict, Optional


class SessionKind(str, Enum):
    STREAM = "stream"
    VOIP = "voip"
    AI = "ai"
    CALL = "call"


@dataclass(frozen=True)
class SessionAdapter:
    start: Callable[[], None]
    stop: Callable[[], None]


class SessionCoordinator:
    """串行切换唯一的 TiRTC 使用者，并在通话结束后恢复实时流。"""

    def __init__(self, adapters: Dict[SessionKind, SessionAdapter],
                 cancel_pending: Optional[Callable[[SessionKind], None]] = None):
        self._adapters = adapters
        # 兼容旧调用方。新的统一仲裁器不传此回调，由仲裁器自身精确清理
        # pending ticket；直接使用 SessionCoordinator 的旧调用方仍保持原语义。
        self._cancel_pending = cancel_pending or (lambda _winner: None)
        self._transition_lock = threading.RLock()
        self._state_lock = threading.Lock()
        self._current: Optional[SessionKind] = None
        self._closed = False

    @property
    def current(self) -> Optional[SessionKind]:
        with self._state_lock:
            return self._current

    def start_stream(self) -> None:
        with self._transition_lock:
            self._ensure_open()
            if self.current == SessionKind.STREAM:
                return
            self._switch_locked(SessionKind.STREAM)

    def begin(self, kind: SessionKind, action: Callable[[], None]) -> None:
        """切换到通话模块后执行接听/发起动作；失败时恢复实时流。"""
        if kind == SessionKind.STREAM:
            raise ValueError("begin() 仅用于通话会话")
        with self._transition_lock:
            self._ensure_open()
            current = self.current
            if current not in (None, SessionKind.STREAM, kind):
                raise RuntimeError(f"{current.value} 会话正在进行中")
            self._cancel_pending(kind)
            if self.current != kind:
                self._switch_locked(kind)
            try:
                action()
            except BaseException:
                self._stop_current_locked()
                self._start_locked(SessionKind.STREAM)
                raise

    def finish(self, kind: SessionKind) -> None:
        """结束指定通话并恢复实时流；过期回调不会影响新会话。"""
        with self._state_lock:
            if self._closed or self._current != kind:
                return
        with self._transition_lock:
            if self.current != kind:
                return
            self._stop_current_locked()
            self._start_locked(SessionKind.STREAM)

    def shutdown(self) -> None:
        with self._transition_lock:
            with self._state_lock:
                if self._closed:
                    return
                self._closed = True
            self._stop_current_locked()

    def _switch_locked(self, target: SessionKind) -> None:
        self._stop_current_locked()
        try:
            self._start_locked(target)
        except BaseException:
            if target != SessionKind.STREAM and not self._closed:
                self._start_locked(SessionKind.STREAM)
            raise

    def _start_locked(self, kind: SessionKind) -> None:
        adapter = self._adapters.get(kind)
        if adapter is None:
            raise RuntimeError(f"{kind.value} 模块不可用")
        adapter.start()
        with self._state_lock:
            self._current = kind

    def _stop_current_locked(self) -> None:
        with self._state_lock:
            kind, self._current = self._current, None
        if kind is not None:
            self._adapters[kind].stop()

    def _ensure_open(self) -> None:
        with self._state_lock:
            closed = self._closed
        if closed:
            raise RuntimeError("会话协调器已关闭")
