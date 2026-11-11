import unittest
from unittest import mock
import numpy as np
from rtc_echo_gate import EchoGate


class EchoGateTests(unittest.TestCase):
    def test_remote_audio_expiry_releases_microphone_without_silent_packets(self):
        for frame_ms in (10, 20, 40):
            with self.subTest(frame_ms=frame_ms):
                gate = EchoGate.create(frame_ms=frame_ms, attenuation_db=24)
                pcm = np.full(16 * frame_ms, 10000, dtype=np.int16).tobytes()
                with mock.patch('rtc_echo_gate.time.monotonic', return_value=10):
                    for _ in range(10):
                        gate.feed_far_end(pcm, 16000)
                    quiet = gate.process(pcm)
                self.assertLess(np.max(np.frombuffer(quiet, dtype=np.int16)), 1000)
                with mock.patch('rtc_echo_gate.time.monotonic', return_value=11):
                    for _ in range(100):
                        recovered = gate.process(pcm)
                self.assertEqual(recovered, pcm)

    def test_fresh_remote_audio_keeps_gate_active(self):
        gate = EchoGate.create(attenuation_db=24)
        pcm = np.full(320, 10000, dtype=np.int16).tobytes()
        for tick in range(100):
            with mock.patch('rtc_echo_gate.time.monotonic', return_value=tick * .02):
                gate.feed_far_end(pcm)
                output = gate.process(pcm)
        self.assertLess(np.max(np.frombuffer(output, dtype=np.int16)), 1000)
        gate.close()
        self.assertEqual(gate.process(pcm), pcm)
