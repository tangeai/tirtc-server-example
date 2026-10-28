import io
import sys
import threading
import wave
import struct
import tempfile
from pathlib import Path
import unittest
from unittest.mock import Mock, patch
from recognition_capture import capture_pcm, play_wav, _playback_wav_bytes
from recognition_playback import RecognitionPlayback
from audio_recognition import AudioRecognition


class CaptureTest(unittest.TestCase):
    def test_exact_ten_seconds_through_shared_input_adapter(self):
        stream = Mock()
        stream.read.side_effect = lambda count: (b'\x01\x02' * count, False)
        with patch.object(sys, 'platform', 'win32'), patch('audio_device.open_input_stream', return_value=(stream,16000)) as opener:
            data = capture_pcm(lambda: False, device=3)
        self.assertEqual(data, b'\x01\x02' * 160000)
        opener.assert_called_once_with(3, target_rate=16000, channels=1, pkt_ms=20)
        self.assertEqual(sum(call.args[0] for call in stream.read.call_args_list), 160000)
        stream.stop.assert_called_once()
        stream.close.assert_called_once()

    def test_native_rate_fallback_yields_ten_seconds_pcm16(self):
        stream = Mock()
        stream.read.side_effect = lambda count: (bytes(count*2), False)
        with patch.object(sys, 'platform', 'win32'), patch('audio_device.open_input_stream', return_value=(stream,48000)):
            data = capture_pcm(lambda: False, device=3)
        self.assertEqual(len(data), 320000)
        self.assertEqual(sum(call.args[0] for call in stream.read.call_args_list), 480000)
        stream.close.assert_called_once()

    def test_provider_placeholder_wav_uses_actual_length(self):
        buffer = io.BytesIO()
        with wave.open(buffer, 'wb') as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(24000)
            w.writeframes(bytes(48000))
        audio = bytearray(buffer.getvalue())
        struct.pack_into('<I', audio, 4, 2147483583)
        struct.pack_into('<I', audio, 40, 2147483547)
        stream = Mock()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'provider.wav'
            path.write_bytes(audio)
            with patch.object(sys, 'platform', 'win32'), patch('audio_device.open_output_stream', return_value=(stream,24000)):
                play_wav(str(path), lambda: False)
        self.assertEqual(sum(len(c.args[0]) for c in stream.write.call_args_list), 48000)
        stream.close.assert_called_once()

    def test_regular_truncated_wav_is_rejected(self):
        buffer = io.BytesIO()
        with wave.open(buffer, 'wb') as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(24000)
            w.writeframes(bytes(48000))
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'broken.wav'
            path.write_bytes(buffer.getvalue()[:-100])
            with self.assertRaisesRegex(ValueError, '不完整'):
                _playback_wav_bytes(str(path))

    def test_non_windows_and_cancelled_do_not_open_mic(self):
        with patch('audio_device.open_input_stream') as opener:
            with patch.object(sys, 'platform', 'linux'), self.assertRaises(ValueError):
                capture_pcm(lambda: False)
            with patch.object(sys, 'platform', 'win32'), self.assertRaises(ValueError):
                capture_pcm(lambda: True)
        opener.assert_not_called()

    def test_overflow_and_start_failure_release_stream(self):
        for fail_start in (False, True):
            stream = Mock()
            stream.read.return_value = (bytes(640), True)
            if fail_start:
                stream.start.side_effect = RuntimeError('PortAudio test error')
            with patch.object(sys, 'platform', 'win32'), patch('audio_device.open_input_stream', return_value=(stream,16000)), self.assertRaises(ValueError):
                capture_pcm(lambda: False)
            stream.close.assert_called_once()

    def test_playback_uses_shared_output_adapter_and_native_rate(self):
        buffer = io.BytesIO()
        with wave.open(buffer, 'wb') as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(24000)
            w.writeframes(bytes(48000))
        stream = Mock()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'audio.wav'
            path.write_bytes(buffer.getvalue())
            with patch.object(sys, 'platform', 'win32'), patch('audio_device.open_output_stream', return_value=(stream,48000)) as opener:
                play_wav(str(path), lambda: False, device=4)
        opener.assert_called_once_with(4, target_rate=24000, channels=1, pkt_ms=20)
        self.assertEqual(sum(len(c.args[0]) for c in stream.write.call_args_list), 96000)
        stream.stop.assert_called_once()
        stream.close.assert_called_once()

    def test_record_once_checks_limits_and_posts_exactly_once(self):
        stop = threading.Event()
        capture = Mock(return_value=b'\x01\x02'*160000)
        client = AudioRecognition('http://localhost:9006', '', '.', capture=capture)
        state = {'effective_enabled': True, 'policy': {'revision': 3},
                 'upload_limits': {'max_file_bytes': 400000, 'max_audio_seconds': 10}}
        with patch.object(sys, 'platform', 'win32'), patch.object(client, '_json', side_effect=[state, {}]) as request:
            client.record_once(stop)
        capture.assert_called_once_with(stop)
        self.assertEqual(request.call_count, 2)
        self.assertEqual(request.call_args.args, ('POST', '/requests'))
        self.assertEqual(request.call_args.kwargs['files']['files'][1], capture.return_value)
        capture.reset_mock()
        state['upload_limits']['max_audio_seconds'] = 9
        with patch.object(sys, 'platform', 'win32'), patch.object(client, '_json', return_value=state) as request, self.assertRaises(ValueError):
            client.record_once(stop)
        capture.assert_not_called()
        self.assertEqual(request.call_count, 1)

    def test_cancel_after_recording_never_uploads(self):
        stop = threading.Event()
        def capture(event):
            event.set()
            return bytes(320000)
        client = AudioRecognition('', '', '.', capture=capture)
        state = {'effective_enabled': True, 'upload_limits': {'max_file_bytes':400000, 'max_audio_seconds':10}}
        with patch.object(sys, 'platform', 'win32'), patch.object(client, '_json', return_value=state) as request, self.assertRaises(ValueError):
            client.record_once(stop)
        self.assertEqual(request.call_count, 1)


class PlaybackTest(unittest.TestCase):
    def test_download_and_play_once_for_repeated_notification(self):
        download = Mock(return_value=['audio.wav'])
        play = Mock()
        player = RecognitionPlayback(download, play)
        self.addCleanup(player.close)
        player.submit('req', 'item_1')
        player.submit('req', 'item_1')
        player._work.drain()
        player.submit('req', 'item_1')
        player._work.drain()
        download.assert_called_once_with('req', item_id='item_1')
        play.assert_called_once()

    def test_failure_can_be_retried_and_shutdown_blocks_new_work(self):
        download = Mock(side_effect=[ValueError('unavailable'), ['audio.wav']])
        play = Mock()
        player = RecognitionPlayback(download, play)
        player.submit('req', 'item_1')
        player._work.drain()
        player.submit('req', 'item_1')
        player._work.drain()
        player.close()
        player.submit('req2', 'item_1')
        self.assertEqual(download.call_count, 2)
        play.assert_called_once()
