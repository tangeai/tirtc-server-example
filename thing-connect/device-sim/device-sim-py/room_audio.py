"""Local Room microphone and speaker; no cloud recording or microphone control."""
import time
import numpy as np
from media_formats import AUDIO_FORMATS


class RoomAudio:
    def __init__(self, up_format, down_format):
        self.up = AUDIO_FORMATS[up_format]
        self.down = AUDIO_FORMATS[down_format]
        if self.up.codec not in ('pcm', 'alaw') or self.down.codec not in ('pcm', 'alaw'):
            raise ValueError('本机音频对讲支持 PCM 或 G.711 A-law')
        self.mic = None
        self.speaker = None
        self._stats = {}

    def _level(self, direction, pcm):
        samples = np.frombuffer(pcm, dtype=np.int16).astype(np.int32)
        peak = int(np.max(np.abs(samples))) if len(samples) else 0
        count, maximum, last = self._stats.get(direction, (0, 0, None))
        count += 1
        maximum = max(maximum, peak)
        now = time.monotonic()
        if last is None or now - last >= 2:
            print(f'[room-audio] {direction} frames={count} peak={maximum}/32768 '
                  f'近静音={"是" if maximum <= 8 else "否"}', flush=True)
            maximum, last = 0, now
        self._stats[direction] = (count, maximum, last)

    def open_speaker(self):
        from audio_device import SpeakerPlayback, select_speaker
        if self.speaker is None:
            self.speaker = SpeakerPlayback(select_speaker(), diagnostic=True)

    def capture(self):
        from audio_device import MicCapture, select_mic
        if self.mic is None:
            self.mic = MicCapture(select_mic())
        pcm = self.mic.read()
        self._level("麦克风采集16k", pcm)
        if self.up.sample_rate == 8000:
            import numpy as np
            samples = np.frombuffer(pcm, dtype=np.int16).astype(np.int32)
            pcm = ((samples[::2] + samples[1::2]) // 2).astype(np.int16).tobytes()
        if self.up.codec == 'alaw':
            from alaw import alaw_encode
            pcm = alaw_encode(pcm)
        return pcm, 40

    def play(self, media, flags, payload):
        if self.speaker is None or media != self.down.media or flags != self.down.flags:
            return
        if self.down.codec == 'alaw':
            from alaw import alaw_decode
            payload = alaw_decode(payload)
        self._level("下行解码", payload)
        self.speaker.play(payload, self.down.sample_rate)

    def release_mic(self):
        if self.mic is not None:
            self.mic.close()
            self.mic = None
            self._stats.pop("麦克风采集16k", None)

    def close(self):
        self.release_mic()
        if self.speaker is not None:
            self.speaker.close()
            self.speaker = None
