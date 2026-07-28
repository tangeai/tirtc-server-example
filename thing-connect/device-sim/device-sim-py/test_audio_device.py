import types
import unittest
from unittest import mock

import audio_device


class _FakePortAudioError(Exception):
    pass


class _FakeInputStream:
    def __init__(self, **_kwargs):
        self.started = False
        self.closed = False

    def start(self):
        self.started = True

    def read(self, frames):
        if not self.started:
            raise _FakePortAudioError("Stream is stopped")
        return b"\x00\x00" * frames, False

    def stop(self):
        self.started = False

    def close(self):
        self.closed = True


class MicCaptureTests(unittest.TestCase):
    def test_open_starts_stream_before_first_read(self):
        streams = []

        def new_stream(**kwargs):
            stream = _FakeInputStream(**kwargs)
            streams.append(stream)
            return stream

        fake_sd = types.SimpleNamespace(
            PortAudioError=_FakePortAudioError,
            RawInputStream=new_stream,
            default=types.SimpleNamespace(device=(12, 11)),
        )

        with mock.patch.object(audio_device, "sd", fake_sd), \
                mock.patch.object(audio_device, "HAS_SD", True):
            capture = audio_device.MicCapture(12)
            try:
                self.assertTrue(streams[0].started)
                self.assertEqual(len(capture.read()), audio_device.AUDIO_PKT_BYTES)
            finally:
                capture.close()

        self.assertTrue(streams[0].closed)


if __name__ == "__main__":
    unittest.main()
