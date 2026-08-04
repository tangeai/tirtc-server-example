#!/usr/bin/env python3
"""跨业务会话竞态仲裁器。

MQTT、终端和 SDK 回调都必须经过本模块。SessionArbiter 只决定谁可以
等待/使用唯一的 RTC 资源；SessionCoordinator 只执行已经决定好的业务
会话切换，进程级 TiRTC SDK 保持运行。
"""

from collections import deque
from dataclasses import dataclass
from enum import Enum
import threading
import time
from typing import Callable, Optional

from session_coordinator import SessionCoordinator, SessionKind


class SessionConflict(RuntimeError):
    """另一个待接或活动会话已经先获得仲裁权。"""


class IncomingDecision(Enum):
    CURRENT = "current"
    DUPLICATE = "duplicate"
    PENDING = "pending"
    BUSY = "busy"


@dataclass(frozen=True)
class SessionLease:
    """会话代次令牌；旧代次的结束事件不能终止新会话。"""

    kind: SessionKind
    generation: int
    session_id: str = ""


@dataclass(frozen=True)
class _PendingTicket:
    kind: SessionKind
    session_id: str
    generation: int
    expires_at: float


class SessionArbiter:
    """实现一个待接槽位、一个活动所有者和 first-wins 规则。"""

    def __init__(self, coordinator: SessionCoordinator):
        self._coordinator = coordinator
        self._state_lock = threading.Lock()
        self._transition_lock = threading.RLock()
        self._finish_idle = threading.Condition(self._state_lock)
        self._owner: Optional[SessionKind] = None
        self._owner_session_id = ""
        self._owner_cancelled = False
        self._pending_ticket: Optional[_PendingTicket] = None
        self._pending_generation = 0
        self._generation = 0
        self._finish_queue = deque()
        self._finish_active = 0
        self._worker_stop = False
        self._closed = False
        self._worker = threading.Thread(
            target=self._lifecycle_worker,
            daemon=True,
            name="session-lifecycle",
        )
        self._worker.start()

    @property
    def current(self) -> Optional[SessionKind]:
        """当前活动业务所有者；空闲 H5 推流不占业务所有者。"""
        with self._state_lock:
            return self._owner

    @property
    def pending(self) -> Optional[SessionKind]:
        with self._state_lock:
            self._expire_pending_locked()
            return self._pending_ticket.kind if self._pending_ticket else None

    def offer_pending(self, kind: SessionKind, session_id: str = "",
                      ttl: float = 45.0) -> bool:
        """原子登记首个来电；待接阶段不停止 H5 实时流。"""
        self._validate_business_kind(kind)
        with self._state_lock:
            self._expire_pending_locked()
            if (self._closed or self._owner is not None
                    or self._pending_ticket is not None):
                return False
            self._pending_generation += 1
            self._pending_ticket = _PendingTicket(
                kind, session_id, self._pending_generation,
                time.monotonic() + max(0.001, ttl),
            )
            return True

    def admit_incoming(self, kind: SessionKind, session_id: str = "",
                       ttl: float = 45.0) -> IncomingDecision:
        """原子区分回铃、重复投递、新来电和忙线。"""
        self._validate_business_kind(kind)
        with self._state_lock:
            self._expire_pending_locked()
            if not self._closed and self._owner == kind:
                if (session_id and self._owner_session_id == session_id):
                    return IncomingDecision.DUPLICATE
                return IncomingDecision.CURRENT
            ticket = self._pending_ticket
            if (ticket is not None and ticket.kind == kind and session_id
                    and ticket.session_id == session_id):
                return IncomingDecision.DUPLICATE
            if (not self._closed and self._owner is None
                    and ticket is None):
                self._pending_generation += 1
                self._pending_ticket = _PendingTicket(
                    kind, session_id, self._pending_generation,
                    time.monotonic() + max(0.001, ttl),
                )
                return IncomingDecision.PENDING
            return IncomingDecision.BUSY

    def clear_pending(self, kind: SessionKind, session_id: Optional[str] = None) -> None:
        """按票据取消待接；session_id 可防止迟到取消影响新房间。"""
        with self._state_lock:
            self._expire_pending_locked()
            ticket = self._pending_ticket
            if (ticket is not None and ticket.kind == kind
                    and (session_id is None or ticket.session_id == session_id)):
                self._pending_ticket = None
                self._pending_generation += 1
            elif (self._owner == kind
                    and (session_id is None
                         or self._owner_session_id == session_id)):
                # The corresponding pending item may currently be consumed by
                # begin(); invalidate its rollback ticket.
                self._owner_cancelled = True
                self._pending_generation += 1

    def has_pending(self, kind: SessionKind,
                    session_id: Optional[str] = None) -> bool:
        with self._state_lock:
            self._expire_pending_locked()
            ticket = self._pending_ticket
            return bool(
                ticket is not None
                and ticket.kind == kind
                and (session_id is None or ticket.session_id == session_id)
            )

    def begin(self, kind: SessionKind, action: Callable[[], None],
              consume_pending: bool = False,
              allow_existing: bool = False,
              session_id: Optional[str] = None,
              lease_ready: Optional[Callable[[SessionLease], None]] = None
              ) -> SessionLease:
        """取得 RTC 并执行启动动作，失败时原子回滚所有权。"""
        self._validate_business_kind(kind)
        with self._transition_lock:
            with self._state_lock:
                self._expire_pending_locked()
                same_owner = self._owner == kind
                ticket = self._pending_ticket
                pending_ok = (
                    ticket is not None
                    and ticket.kind == kind
                    and (session_id is None or ticket.session_id == session_id)
                    if consume_pending
                    else ticket is None
                )
                if (self._closed
                        or (self._owner is not None and not same_owner)
                        or (same_owner and not allow_existing)
                        or (same_owner and session_id is not None
                            and self._owner_session_id != session_id)
                        or not pending_ok):
                    owner = self._owner.value if self._owner else "none"
                    pending = ticket.kind.value if ticket else "none"
                    raise SessionConflict(
                        f"{kind.value} 未获得 RTC（owner={owner}, pending={pending}）"
                    )
                if not same_owner:
                    self._owner = kind
                    self._owner_session_id = (
                        ticket.session_id if consume_pending and ticket else
                        (session_id or "")
                    )
                    self._owner_cancelled = False
                    self._generation += 1
                generation = self._generation
                consumed_ticket = ticket if consume_pending else None
                pending_generation = self._pending_generation
                if consumed_ticket:
                    self._pending_ticket = None
                provisional_lease = SessionLease(
                    kind, generation,
                    consumed_ticket.session_id if consumed_ticket else
                    self._owner_session_id,
                )

            try:
                # The SDK action is allowed to report a terminal event
                # synchronously.  Publish the new generation before entering
                # it, otherwise a same-kind retry can finish with the previous
                # lease and leave this owner permanently occupied.
                if lease_ready is not None:
                    lease_ready(provisional_lease)
                self._coordinator.begin(kind, action)
            except BaseException:
                with self._state_lock:
                    if self._owner == kind and self._generation == generation:
                        self._owner = None
                        self._owner_session_id = ""
                        cancelled = self._owner_cancelled
                        self._owner_cancelled = False
                        if (consumed_ticket and not cancelled
                                and not self._closed
                                and self._pending_generation == pending_generation):
                            self._pending_ticket = consumed_ticket
                raise
            with self._state_lock:
                lease_session_id = self._owner_session_id
                cancelled = (
                    self._owner == kind
                    and self._generation == generation
                    and self._owner_cancelled
                )
                if cancelled:
                    self._owner = None
                    self._owner_session_id = ""
                    self._owner_cancelled = False
            if cancelled:
                self._finish_coordinator_with_recovery(kind)
                raise SessionConflict(
                    f"{kind.value} 来电在启动期间已取消"
                )
            return SessionLease(kind, generation, lease_session_id)

    def continue_session(self, kind: SessionKind,
                         action: Callable[[], None],
                         session_id: Optional[str] = None,
                         lease_ready: Optional[Callable[[SessionLease], None]] = None
                         ) -> SessionLease:
        """在同一所有者内完成回铃/建连阶段，不创建新的业务会话。"""
        return self.begin(
            kind, action, allow_existing=True, session_id=session_id,
            lease_ready=lease_ready)

    def finish(self, kind: SessionKind, generation: Optional[int] = None) -> None:
        """归还所有权；带代次的过期结束事件会被忽略。"""
        with self._transition_lock:
            with self._state_lock:
                current = (
                    not self._closed
                    and self._owner == kind
                    and (generation is None or generation == self._generation)
                )
                if current:
                    self._owner = None
                    self._owner_session_id = ""
                    self._owner_cancelled = False
            if current:
                self._finish_coordinator_with_recovery(kind)

    def finish_async(self, kind: SessionKind,
                     generation: Optional[int] = None) -> None:
        """SDK 回调线程只投递结束任务，不在回调栈内 Stop/Uninit。"""
        with self._finish_idle:
            if self._closed or self._owner != kind:
                return
            target_generation = (
                self._generation if generation is None else generation)
            request = (kind, target_generation)
            if request not in self._finish_queue:
                self._finish_queue.append(request)
                self._finish_idle.notify()

    def shutdown(self) -> None:
        coordinator_error = None
        with self._transition_lock:
            with self._state_lock:
                first = not self._closed
                self._closed = True
                self._owner = None
                self._owner_session_id = ""
                self._owner_cancelled = False
                self._pending_ticket = None
            if first:
                try:
                    self._coordinator.shutdown()
                except BaseException as exc:
                    coordinator_error = exc
        with self._finish_idle:
            self._worker_stop = True
            self._finish_idle.notify_all()
            while self._finish_queue or self._finish_active:
                self._finish_idle.wait()
        if self._worker.is_alive() and self._worker is not threading.current_thread():
            self._worker.join()
        if coordinator_error is not None:
            raise coordinator_error

    def _lifecycle_worker(self) -> None:
        while True:
            with self._finish_idle:
                while not self._finish_queue and not self._worker_stop:
                    self._finish_idle.wait()
                if self._worker_stop and not self._finish_queue:
                    self._finish_idle.notify_all()
                    return
                kind, generation = self._finish_queue.popleft()
                self._finish_active += 1
            try:
                self.finish(kind, generation)
            except BaseException as exc:
                # The worker must survive adapter failures; a later terminal
                # event still needs to be able to release its session.
                print(
                    f"[arbiter] 生命周期结束失败 kind={kind.value}: {exc}",
                    flush=True,
                )
            finally:
                with self._finish_idle:
                    self._finish_active -= 1
                    if not self._finish_queue and not self._finish_active:
                        self._finish_idle.notify_all()

    def _finish_coordinator_with_recovery(self, kind: SessionKind) -> None:
        try:
            self._coordinator.finish(kind)
            return
        except BaseException as first_error:
            last_error = first_error
        for delay in (0.05, 0.2, 0.5):
            time.sleep(delay)
            with self._state_lock:
                if self._closed:
                    return
            try:
                self._coordinator.start_stream()
                return
            except BaseException as exc:
                last_error = exc
        raise RuntimeError(
            f"{kind.value} 结束后恢复 H5 实时流失败"
        ) from last_error

    def _expire_pending_locked(self) -> None:
        ticket = self._pending_ticket
        if ticket is not None and time.monotonic() >= ticket.expires_at:
            self._pending_ticket = None
            self._pending_generation += 1

    @staticmethod
    def _validate_business_kind(kind: SessionKind) -> None:
        if kind == SessionKind.STREAM:
            raise ValueError("STREAM 是空闲基线，不参与业务会话仲裁")
