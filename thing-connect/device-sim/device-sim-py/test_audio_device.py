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


class AudioSelectionTests(unittest.TestCase):
    def setUp(self):
        self.devices = [
            dict(name="Microphone Array", hostapi=0, max_input_channels=1, max_output_channels=0),
            dict(name="USB Microphone", hostapi=0, max_input_channels=1, max_output_channels=0),
            dict(name="USB Speakers", hostapi=0, max_input_channels=0, max_output_channels=2),
            dict(name="USB Microphone", hostapi=1, max_input_channels=1, max_output_channels=0),
        ]
        self.sd = types.SimpleNamespace(
            query_devices=lambda: self.devices,
            query_hostapis=lambda: [dict(name="Windows WASAPI"), dict(name="Windows WDM-KS")],
            default=types.SimpleNamespace(device=(1, 2)),
        )
        for key, value in [("sd", self.sd), ("HAS_SD", True)]:
            patch = mock.patch.object(audio_device, key, value)
            patch.start()
            self.addCleanup(patch.stop)
        patch = mock.patch.object(audio_device.sys, "platform", "win32")
        patch.start()
        self.addCleanup(patch.stop)
        # Selection configuration belongs to startup, before any audio workers.
        self.addCleanup(lambda: setattr(audio_device, "_selected_devices", None))
        audio_device._selected_devices = None

    def test_usb_system_default_beats_builtin_array(self):
        self.assertEqual(audio_device.select_mic(), 1)
        self.assertEqual(audio_device.select_speaker(), 2)

    def test_windows_default_endpoint_beats_mme_mapper(self):
        self.sd.default.device = (0, 2)
        self.sd.query_hostapis = lambda: [dict(
            name="Windows WASAPI", default_input_device=1, default_output_device=2),
            dict(name="Windows WDM-KS")]
        self.assertEqual(audio_device.select_mic(), 1)

    def test_startup_reports_selected_names(self):
        import io
        from contextlib import redirect_stdout
        out = io.StringIO()
        with redirect_stdout(out):
            audio_device.configure_audio_devices()
        self.assertIn("麦克风: [1] USB Microphone", out.getvalue())
        self.assertIn("扬声器: [2] USB Speakers", out.getvalue())

    def test_explicit_selection_is_shared_by_all_consumers(self):
        audio_device.configure_audio_devices(0, 2)
        self.assertEqual(audio_device.select_mic(), 0)
        self.assertEqual(audio_device.select_speaker(), 2)

    def test_invalid_selection_does_not_silently_fallback(self):
        for mic, speaker in [(2, 2), (-1, 2), (99, 2), (3, 2), (1, 1)]:
            with self.subTest(mic=mic, speaker=speaker):
                with self.assertRaises(ValueError):
                    audio_device.configure_audio_devices(mic, speaker)
                self.assertIsNone(audio_device._selected_devices)

    def test_missing_default_requires_user_selection(self):
        self.sd.default.device = (-1, 2)
        with self.assertRaisesRegex(ValueError, "默认"):
            audio_device.select_mic()

    def test_listing_includes_names_indices_and_directions(self):
        import io
        from contextlib import redirect_stdout
        out = io.StringIO()
        with redirect_stdout(out):
            audio_device.print_audio_devices()
        self.assertIn("[1] USB Microphone", out.getvalue())
        self.assertIn("默认输入", out.getvalue())
        self.assertIn("默认输出", out.getvalue())
        self.assertIn("不支持", out.getvalue())


if __name__ == "__main__":
    unittest.main()
