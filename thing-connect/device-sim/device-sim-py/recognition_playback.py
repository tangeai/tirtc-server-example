"""Bounded automatic recognition playback, independent of MQTT callback threads."""
from collections import OrderedDict
import threading
from callback_work_queue import CallbackWorkQueue


class RecognitionPlayback:
    def __init__(self, download, play):
        self._download, self._play = download, play
        self._stop = threading.Event()
        self._lock = threading.Lock()
        self._pending = set()
        self._played = OrderedDict()
        self._started = False
        self._work = CallbackWorkQueue('recognition-playback', self._consume, self._warn, maxsize=32)

    @staticmethod
    def _warn(message):
        print('[recognition] ' + message, flush=True)

    def submit(self, request_id, item_id):
        key = (request_id, item_id)
        with self._lock:
            if self._stop.is_set() or key in self._pending or key in self._played:
                return
            if not self._started:
                self._work.start()
                self._started = True
            self._pending.add(key)
            if not self._work.submit(key):
                self._pending.discard(key)
                self._warn('自动播放队列已满，请手动下载语音')

    def _consume(self, key):
        try:
            if self._stop.is_set():
                return
            files = self._download(key[0], item_id=key[1], stop_event=self._stop)
            if self._stop.is_set():
                return
            if not files:
                raise ValueError('语音尚不可下载')
            self._play(files[0], self._stop)
            if not self._stop.is_set():
                with self._lock:
                    self._played[key] = True
                    if len(self._played) > 512:
                        self._played.popitem(last=False)
                self._warn(f'语音播放完成：{key[0]} / {key[1]}')
        except Exception as exc:
            if not self._stop.is_set():
                if isinstance(exc, ValueError):
                    self._warn(str(exc))
                self._warn(f'自动下载或播放未完成，可手动下载：recognize audio {key[0]}')
        finally:
            with self._lock:
                self._pending.discard(key)

    def close(self):
        with self._lock:
            self._stop.set()
        self._work.stop()
