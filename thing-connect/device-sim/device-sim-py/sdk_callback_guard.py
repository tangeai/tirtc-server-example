#!/usr/bin/env python3
"""TiRTC ctypes 回调生命周期屏障。"""

import threading

from callback_work_queue import CallbackWorkQueue


class SdkCallbackGuard:
    """跟踪 SDK 回调，并用一个有界常驻队列执行回调后控制任务。"""

    def __init__(self) -> None:
        self._condition = threading.Condition()
        self._active = 0
        self._pending = 0
        self._local = threading.local()
        self._work = CallbackWorkQueue(
            "sdk-callback-control",
            self._run_deferred,
            lambda _message: None,
            maxsize=256,
        )

    def start(self) -> None:
        """Start or restart the process-lifetime control worker."""
        self._work.start()

    def wrap(self, callback):
        def tracked(*args):
            with self._condition:
                self._active += 1
            self._local.depth = getattr(self._local, "depth", 0) + 1
            try:
                return callback(*args)
            finally:
                self._local.depth -= 1
                with self._condition:
                    self._active -= 1
                    if self._active == 0:
                        self._condition.notify_all()

        return tracked

    def wait_for_idle(self) -> None:
        if getattr(self._local, "depth", 0):
            raise RuntimeError("不能在 TiRTC SDK 回调线程中等待回调退出")
        with self._condition:
            self._condition.wait_for(lambda: self._active == 0)

    def defer(self, callback, *args, name: str) -> bool:
        """Queue SDK-affecting work to run after callbacks unwind."""
        with self._condition:
            self._pending += 1
        if not self._work.submit((callback, args, name)):
            with self._condition:
                self._pending -= 1
                if self._active == 0 and self._pending == 0:
                    self._condition.notify_all()
            raise RuntimeError(f"SDK 回调控制队列已满或未启动: {name}")
        return True

    def _run_deferred(self, item) -> None:
        callback, args, name = item
        try:
            self.wait_for_idle()
            callback(*args)
        except BaseException as exc:
            print(f"[sdk-callback] 延后任务失败 name={name}: {exc}", flush=True)
        finally:
            with self._condition:
                self._pending -= 1
                if self._active == 0 and self._pending == 0:
                    self._condition.notify_all()

    def wait_for_all(self) -> None:
        if getattr(self._local, "depth", 0):
            raise RuntimeError("不能在 TiRTC SDK 回调线程中等待回调任务退出")
        with self._condition:
            self._condition.wait_for(
                lambda: self._active == 0 and self._pending == 0)

    def close(self) -> None:
        """Drain and stop the control worker after native SDK shutdown."""
        self.wait_for_all()
        self._work.stop()

    @property
    def active_count(self) -> int:
        with self._condition:
            return self._active

    @property
    def pending_count(self) -> int:
        with self._condition:
            return self._pending


def join_worker_before_uninit(worker, warn, label: str, timeout: float = 5.0) -> None:
    """确保媒体线程退出后再释放其连接或 SDK 资源。"""
    if worker is None or worker is threading.current_thread():
        return
    worker.join(timeout=timeout)
    is_alive = getattr(worker, "is_alive", None)
    if callable(is_alive) and is_alive():
        warn(f"{label}线程 {timeout:g}s 内未退出，继续等待以避免 SDK 释放竞态")
        worker.join()
