#!/usr/bin/env python3
"""Bounded worker queue for data copied from TiRTC SDK callbacks."""

from __future__ import annotations

import queue
import threading

_THREAD_CLASS = threading.Thread
_STOP = object()


class CallbackWorkQueue:
    """Keep file, device and other blocking work off SDK callback threads."""

    def __init__(
        self,
        name: str,
        handler,
        warn,
        *,
        maxsize: int = 256,
    ) -> None:
        self._name = name
        self._handler = handler
        self._warn = warn
        self._queue = queue.Queue(maxsize=maxsize)
        self._lock = threading.Lock()
        self._thread = None
        self._dropped = 0

    def start(self) -> None:
        with self._lock:
            if self._thread is not None and self._thread.is_alive():
                return
            self._discard_queued_locked()
            thread = _THREAD_CLASS(
                target=self._run,
                daemon=True,
                name=self._name,
            )
            self._thread = thread
        thread.start()

    def submit(self, item) -> bool:
        with self._lock:
            if self._thread is None:
                return False
            try:
                self._queue.put_nowait(item)
                return True
            except queue.Full:
                self._dropped += 1
                if self._dropped == 1 or self._dropped % 100 == 0:
                    self._warn(
                        f"{self._name} 队列已满，"
                        f"丢弃任务 count={self._dropped}")
                return False

    def drain(self) -> None:
        self._queue.join()

    def stop(self) -> None:
        with self._lock:
            thread, self._thread = self._thread, None
        if thread is None:
            return
        self._queue.join()
        self._queue.put(_STOP)
        thread.join()

    def _run(self) -> None:
        while True:
            item = self._queue.get()
            try:
                if item is _STOP:
                    return
                self._handler(item)
            except BaseException as exc:
                self._warn(f"{self._name} 任务失败: {exc}")
            finally:
                self._queue.task_done()

    def _discard_queued_locked(self) -> None:
        while True:
            try:
                self._queue.get_nowait()
            except queue.Empty:
                return
            else:
                self._queue.task_done()
