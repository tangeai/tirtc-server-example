#!/usr/bin/env python3
"""TiRTC ctypes 回调生命周期屏障。"""

import threading


class SdkCallbackGuard:
    """跟踪正在执行的 SDK 回调，确保 Uninit 不会释放其调用栈。"""

    def __init__(self) -> None:
        self._condition = threading.Condition()
        self._active = 0
        self._local = threading.local()

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

    @property
    def active_count(self) -> int:
        with self._condition:
            return self._active


def join_worker_before_uninit(worker, warn, label: str, timeout: float = 5.0) -> None:
    """确保媒体线程退出后才反初始化 SDK。"""
    if worker is None or worker is threading.current_thread():
        return
    worker.join(timeout=timeout)
    is_alive = getattr(worker, "is_alive", None)
    if callable(is_alive) and is_alive():
        warn(f"{label}线程 {timeout:g}s 内未退出，继续等待以避免 SDK 释放竞态")
        worker.join()
