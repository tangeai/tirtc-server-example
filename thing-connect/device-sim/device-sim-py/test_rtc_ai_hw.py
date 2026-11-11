"""Exercise hardware failure recovery without requiring PortAudio or a device."""
import importlib.util
from pathlib import Path
import threading
import tempfile
import json
import sys
import os
import types
import unittest
from unittest import mock


class AiHardwareFailureTests(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location(
            "ai_hw_under_test", Path(__file__).with_name("rtc_ai_hw.py"))
        self.ai = importlib.util.module_from_spec(spec)
        fake_audio = types.SimpleNamespace(default=types.SimpleNamespace(device=(0, 1)))
        # Replace only sounddevice: restoring all sys.modules would unload NumPy
        # imported by rtc_echo_gate, which cannot be reimported on Windows.
        previous = sys.modules.get('sounddevice')
        sys.modules['sounddevice'] = fake_audio
        try:
            spec.loader.exec_module(self.ai)
        finally:
            if previous is None:
                sys.modules.pop('sounddevice', None)
            else:
                sys.modules['sounddevice'] = previous
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.record_dir = Path(temp.name)
        self.ai.configure_receive_dir(str(self.record_dir))
        self.ai._device_id = 'test-device'
        self.convert = mock.Mock()
        mock.patch.dict(self.ai.AudioRecorder.close.__globals__,
                        convert_audio_to_wav=self.convert).start()
        self.ai._callback_guard.start()
        self.ai._session_generation = 7
        self.ai._session_state = "IN_CALL"
        self.ai._active_hconn = 123
        self.ended = mock.Mock()
        self.ai.set_session_end_callback(self.ended)
        self.disconnect = mock.patch.object(self.ai.sdk, 'TiRtcDisconnect', return_value=0).start()
        mock.patch.object(self.ai.sdk, 'TiRtcSendCommand', return_value=0).start()
        self.addCleanup(mock.patch.stopall)
        self.addCleanup(self.ai._callback_guard.close)

    def test_microphone_failure_releases_session(self):
        with mock.patch('audio_device.open_input_stream', side_effect=OSError('injected')):
            self.ai._mic_stream_worker(123, 7)
        self.ai._callback_guard.wait_for_all()
        self.assertEqual(self.ai.get_state(), 'IDLE')
        self.ended.assert_called_once_with()
        self.disconnect.assert_called_once()

    def test_speaker_failure_releases_session(self):
        with mock.patch('audio_device.open_output_stream', side_effect=OSError('injected')):
            self.ai._speaker_play_worker(7)
        self.ai._callback_guard.wait_for_all()
        self.assertEqual(self.ai.get_state(), 'IDLE')
        self.ended.assert_called_once_with()

    def test_downlink_opus_is_saved_and_finalized_even_when_playback_fails(self):
        from ai_audio_codec import AudioCodec, validate_live_codecs
        try:
            validate_live_codecs('opus_16khz', 'opus_16khz')
        except RuntimeError as exc:
            if sys.platform == 'win32' or os.environ.get('REQUIRE_AI_CODECS') == '1':
                raise
            self.skipTest(str(exc))
        self.ai._down_audio_format = 'opus_16khz'
        with AudioCodec('opus_16khz', encode=True) as encoder:
            packet = encoder.encode(bytes(640))
            spec = encoder.spec
        for fail in (False, True):
            with self.subTest(playback_failure=fail):
                spk = mock.MagicMock()
                if fail:
                    spk.write.side_effect = OSError('speaker disconnected')
                self.ai._play_queue.put((packet, spec.media, spec.sample_rate, spec.flags))
                if not fail:
                    self.ai._play_queue.put(None)
                with mock.patch('audio_device.open_output_stream', return_value=(spk, 16000)), \
                        mock.patch.object(self.ai.sd, 'query_devices', create=True, return_value={'name': 'test'}), \
                        mock.patch.object(self.ai, '_media_failed') as failed:
                    self.ai._speaker_play_worker(7)
                self.assertEqual(failed.call_count, int(fail))
        recordings = list((self.record_dir / 'test-device').glob('ai_*.raw'))
        self.assertEqual(len(recordings), 2)
        for raw in recordings:
            self.assertEqual(raw.read_bytes(), packet)
            self.assertTrue(raw.with_suffix('.opus').read_bytes().startswith(b'OggS'))
            metadata = json.loads(raw.with_suffix('.fmt.json').read_text())
            self.assertEqual(metadata['encoding'], 'opus')
            self.assertEqual(metadata['sample_rate'], 16000)
        self.assertEqual(self.convert.call_count, 2)

    def test_recording_failure_does_not_interrupt_playback(self):
        spk = mock.MagicMock()
        for _ in range(2):
            self.ai._play_queue.put((bytes(160), self.ai.sdk.TIRTC_AUDIO_ALAW, 8000, 0))
        self.ai._play_queue.put(None)
        with mock.patch('audio_device.open_output_stream', return_value=(spk, 16000)), \
                mock.patch.object(self.ai.sd, 'query_devices', create=True, return_value={'name': 'test'}), \
                mock.patch.object(self.ai, 'AudioRecorder') as recorder, \
                mock.patch.object(self.ai, '_media_failed') as failed:
            recorder.return_value.open.side_effect = OSError('disk full')
            self.ai._speaker_play_worker(7)
        failed.assert_not_called()
        recorder.return_value.open.assert_called_once()
        recorder.return_value.close.assert_called_once()
        self.assertEqual(spk.write.call_count, 2)

    def test_simultaneous_media_failures_are_idempotent(self):
        barrier = threading.Barrier(3)
        def fail():
            barrier.wait()
            self.ai._media_failed(7, 'injected')
        workers = [threading.Thread(target=fail) for _ in range(2)]
        for worker in workers:
            worker.start()
        barrier.wait()
        for worker in workers:
            worker.join(timeout=2)
            self.assertFalse(worker.is_alive())
        self.ai._callback_guard.wait_for_all()
        self.ended.assert_called_once_with()
        self.disconnect.assert_called_once()

    def test_late_media_failure_does_not_stop_new_generation(self):
        self.ai._media_failed(6, 'old failure')
        self.ai._callback_guard.wait_for_all()
        self.assertEqual(self.ai.get_state(), 'IN_CALL')
        self.assertFalse(self.ai._stream_stop.is_set())
        self.ended.assert_not_called()
        self.disconnect.assert_not_called()

    def test_media_start_failures_join_both_workers_before_disconnect(self):
        self.ai._session_state = 'CONNECTING'
        workers = []
        real_thread = threading.Thread
        def remember_thread(*args, **kwargs):
            worker = real_thread(*args, **kwargs)
            workers.append(worker)
            return worker
        def disconnected(conn):
            self.assertTrue(all(not worker.is_alive() for worker in workers))
            return 0
        self.disconnect.side_effect = disconnected
        with mock.patch('audio_device.open_input_stream', side_effect=OSError('input')), \
                mock.patch('audio_device.open_output_stream', side_effect=OSError('output')), \
                mock.patch.object(self.ai.threading, 'Thread', side_effect=remember_thread):
            self.ai._callback_guard.defer(self.ai._start_media_after_callback, 7, 123,
                                           name='test-media-start')
            self.ai._callback_guard.wait_for_all()
        self.assertEqual(len(workers), 2)
        self.assertEqual(self.ai.get_state(), 'IDLE')
        self.ended.assert_called_once_with()
        self.disconnect.assert_called_once()

    def test_microphone_sends_twenty_ms_with_negotiated_metadata(self):
        import ctypes
        from ai_audio_codec import AudioCodec, validate_live_codecs
        from media_formats import AI_AUDIO_FORMAT_CHOICES
        for name in AI_AUDIO_FORMAT_CHOICES:
            with self.subTest(format=name):
                try:
                    validate_live_codecs(name, name)
                except RuntimeError as exc:
                    import os
                    if os.environ.get('REQUIRE_AI_CODECS') == '1':
                        raise
                    self.skipTest(str(exc))
                self.ai._stream_stop.clear()
                self.ai._up_audio_format = name
                mic = mock.MagicMock()
                mic.read.return_value = (bytes(640), False)
                packets = []
                def send(conn, ptr, buf):
                    frame = ctypes.cast(ptr, ctypes.POINTER(self.ai.TIRTCFRAMEINFO)).contents
                    packets.append((frame.media, frame.flags, frame.ts, bytes(buf)))
                    if len(packets) == 2:
                        self.ai._stream_stop.set()
                    return frame.length
                with mock.patch('audio_device.open_input_stream', return_value=(mic, 16000)), \
                        mock.patch.object(self.ai.sd, 'query_devices', create=True, return_value={'name': 'test'}), \
                        mock.patch.object(self.ai.sdk, 'TiRtcSendAudioStream', side_effect=send), \
                        mock.patch.object(self.ai, '_media_failed') as failed:
                    self.ai._mic_stream_worker(123, 7)
                failed.assert_not_called()
                self.assertEqual(len(packets), 2)
                with AudioCodec(name, encode=False) as decoder:
                    for index, (media, flags, ts, packet) in enumerate(packets):
                        self.assertEqual((media, flags, ts), (decoder.spec.media, decoder.spec.flags, index * 20))
                        self.assertEqual(len(decoder.decode(packet)), decoder.samples * 2)

    def test_speaker_decodes_without_padding_twenty_ms_to_forty(self):
        from ai_audio_codec import AudioCodec, validate_live_codecs
        from media_formats import AI_AUDIO_FORMAT_CHOICES
        for name in AI_AUDIO_FORMAT_CHOICES:
            with self.subTest(format=name):
                try:
                    validate_live_codecs(name, name)
                except RuntimeError as exc:
                    import os
                    if os.environ.get('REQUIRE_AI_CODECS') == '1':
                        raise
                    self.skipTest(str(exc))
                self.ai._down_audio_format = name
                with AudioCodec(name, encode=True) as encoder:
                    packet = encoder.encode(bytes(encoder.samples * 2))
                    spec = encoder.spec
                spk = mock.MagicMock()
                self.ai._play_queue.put((packet, spec.media, spec.sample_rate, spec.flags))
                self.ai._play_queue.put(None)
                with mock.patch('audio_device.open_output_stream', return_value=(spk, spec.sample_rate)), \
                        mock.patch.object(self.ai.sd, 'query_devices', create=True, return_value={'name': 'test'}), \
                        mock.patch.object(self.ai, '_media_failed') as failed:
                    self.ai._speaker_play_worker(7)
                failed.assert_not_called()
                spk.write.assert_called_once()
                self.assertEqual(len(spk.write.call_args.args[0]), spec.sample_rate * 2 // 50)

    def test_negotiation_mismatch_and_stale_response_never_start_media(self):
        self.ai._session_state = 'CONNECTING'
        self.ai._start_request_id = 'expected'
        good = {'codec': 'alaw', 'sample_rate': 8000, 'channels': 1}
        with mock.patch.object(self.ai._callback_guard, 'defer') as defer, \
                mock.patch.object(self.ai, '_fail_after_callback') as fail:
            self.ai._handle_ai_message(123, {'id': 'stale', 'result': {}})
            defer.assert_not_called()
            fail.assert_not_called()
            self.ai._handle_ai_message(123, {'id': 'expected', 'result': {'input_audio': good}})
            defer.assert_not_called()
            fail.assert_called_once()
            self.ai._handle_ai_message(123, {'id': 'expected', 'result': {
                'input_audio': good, 'output_audio': good}})
            defer.assert_called_once()
