import io
import unittest
from contextlib import redirect_stdout
from unittest import mock

import numpy as np

from room_audio import RoomAudio


class RoomAudioDiagnosticsTests(unittest.TestCase):
    def test_capture_logs_peak_and_preserves_alaw_frame(self):
        audio = RoomAudio('alaw_8khz', 'alaw_8khz')
        audio.mic = mock.Mock()
        audio.mic.read.return_value = np.full(640, -32768, dtype=np.int16).tobytes()
        output = io.StringIO()
        with redirect_stdout(output):
            packet, duration = audio.capture()
        self.assertEqual((len(packet), duration), (320, 40))
        self.assertIn('peak=32768/32768', output.getvalue())
        self.assertIn('静音=否', output.getvalue())

    def test_level_is_rate_limited_and_reports_silence(self):
        audio = RoomAudio('alaw_8khz', 'alaw_8khz')
        output = io.StringIO()
        with redirect_stdout(output), mock.patch('room_audio.time.monotonic', side_effect=[0, 1, 2]):
            for _ in range(3):
                audio._level('test', b'\0\0' * 640)
        self.assertEqual(output.getvalue().count('[room-audio]'), 2)
        self.assertIn('frames=3 peak=0/32768 静音=是', output.getvalue())

    def test_downlink_logs_decoded_level_and_preserves_playback(self):
        audio = RoomAudio('alaw_8khz', 'alaw_8khz')
        audio.speaker = mock.Mock()
        with redirect_stdout(io.StringIO()) as output:
            audio.play(audio.down.media, audio.down.flags, b'\xd5' * 320)
        self.assertIn('下行解码', output.getvalue())
        args = audio.speaker.play.call_args.args
        self.assertEqual((len(args[0]), args[1]), (640, 8000))
