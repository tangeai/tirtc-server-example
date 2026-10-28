import io
import json
import threading
from types import SimpleNamespace
from contextlib import redirect_stdout
import tempfile
import unittest
import wave
from pathlib import Path
from unittest.mock import MagicMock, patch

from audio_recognition import AudioRecognition


def wav():
    buffer = io.BytesIO()
    with wave.open(buffer, 'wb') as output:
        output.setnchannels(1)
        output.setsampwidth(2)
        output.setframerate(16000)
        output.writeframes(b'\0' * 32000)
    return buffer.getvalue()


class RecognitionTest(unittest.TestCase):
    def test_upload_uses_device_revision_and_pcm(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'audio with spaces.wav'
            path.write_bytes(wav())
            client = AudioRecognition('http://localhost:9006', 'test-token', directory)
            config = {'effective_enabled': True, 'policy': {'revision': 7},
                      'upload_limits': {'max_file_bytes': 100000, 'max_audio_seconds': 10}}
            with patch.object(client, '_json', side_effect=[config, {'status': 'queued'}]) as request:
                result = client.upload(str(path))
                self.assertEqual(result['status'], 'queued')
                kwargs = request.call_args.kwargs
                self.assertEqual(kwargs['data']['config_revision'], '7')
                self.assertEqual(kwargs['files']['files'][1], wav())
                self.assertTrue(kwargs['data']['request_id'].startswith('sim_'))

    def test_raw_pcm_upload_is_byte_identical(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'cry.pcm'
            raw = bytes(range(256)) * 125
            path.write_bytes(raw)
            client = AudioRecognition('http://localhost:9006', 'test-token', directory)
            config = {'effective_enabled': True, 'policy': {'revision': 7},
                      'upload_limits': {'max_file_bytes': 100000, 'max_audio_seconds': 10}}
            with patch.object(client, '_json', side_effect=[config, {'status': 'queued'}]) as request:
                client.upload(str(path))
                kwargs = request.call_args.kwargs
                self.assertEqual(kwargs['files']['files'][1], raw)
                self.assertEqual(kwargs['data']['audio_format'], 'pcm_s16le')
                self.assertEqual(kwargs['data']['sample_rate'], '16000')
                self.assertEqual(kwargs['data']['channels'], '1')
            for raw in [b'1' * 31999, b'1' * 32001, b'1' * 100000]:
                path.write_bytes(raw)
                with patch.object(client, '_json', return_value=config) as request:
                    with self.assertRaises(ValueError):
                        client.upload(str(path))
                    self.assertEqual(request.call_count, 1)

    def test_explicit_pcm_command_preserves_windows_path(self):
        client = AudioRecognition('', '', '.')
        with patch.object(client, 'upload', return_value={}) as upload:
            client.command(r'pcm 8000 2 "D:\audio samples\cry.raw"')
            upload.assert_called_once_with(r'D:\audio samples\cry.raw', sample_rate=8000,
                                           channels=2, audio_format='pcm_s16le')

    def test_mqtt_result_routes_to_recognition_without_http(self):
        import device_flow
        from session_router import SessionMessageRouter
        client = AudioRecognition('', '', '.', device_id='dev')
        router = SessionMessageRouter(MagicMock(), MagicMock(), MagicMock(), recognition=client)
        self.addCleanup(router.shutdown)
        mqtt = MagicMock()
        stop = threading.Event()
        def deliver():
            for revision in [2, 2, 1, 3]:
                message = {'type': 'recognition_result', 'channel': 'recognition', 'event_id': 'event',
                           'payload': {'code': 200, 'data': {'device_id': 'dev', 'request_id': 'task1',
                           'status_revision': revision, 'status': 'succeeded',
                           'results': [{'result': {'summary': '疑似哭声'}, 'speech': {'status': 'ready'}}]}}}
                mqtt.on_message(mqtt, None, SimpleNamespace(topic='device/sn_dev/notify',
                                     payload=json.dumps(message).encode()))
            stop.set()
        mqtt.loop_start.side_effect = deliver
        output = io.StringIO()
        with patch.object(device_flow, '_new_mqtt_client', return_value=mqtt), \
                patch.object(client, '_request') as http, redirect_stdout(output):
            device_flow.connect_mqtt_blocking('localhost', 1883, 'dev', 'test-token', router,
                                              stop_event=stop, use_tls=False)
        self.assertEqual(output.getvalue().count('异步识别结果'), 2)
        self.assertEqual(output.getvalue().count('疑似哭声'), 2)
        self.assertIn('recognize audio task1', output.getvalue())
        http.assert_not_called()

    def test_mqtt_invalid_or_foreign_result_is_ignored(self):
        client = AudioRecognition('', '', '.', device_id='dev')
        output = io.StringIO()
        with redirect_stdout(output):
            for data in [None, {}, {'request_id': 'task', 'status_revision': True, 'results': []},
                         {'request_id': 'task', 'status_revision': 1, 'results': [], 'device_id': 'other'}]:
                client.on_notification({'type': 'recognition_result', 'channel': 'recognition',
                                        'payload': {'code': 200, 'data': data}})
        self.assertEqual(output.getvalue(), '')
        self.assertFalse(client._revisions)

    def test_configuration_notification_is_visible(self):
        client = AudioRecognition('', '', '.')
        output = io.StringIO()
        with redirect_stdout(output), patch.object(client, '_request') as http:
            client.on_notification({'type': 'recognition_config_changed', 'channel': 'recognition',
                                    'payload': {'code': 200, 'data': {}}})
        self.assertIn('recognize config', output.getvalue())
        http.assert_not_called()

    def test_missing_file_reports_path_and_never_posts(self):
        with tempfile.TemporaryDirectory() as directory:
            missing = str(Path(directory) / 'missing.wav')
            client = AudioRecognition('http://localhost:9006', 'test-token', directory)
            config = {'effective_enabled': True, 'policy': {'revision': 7},
                      'upload_limits': {'max_file_bytes': 100000, 'max_audio_seconds': 10}}
            output = io.StringIO()
            with patch.object(client, '_json', return_value=config) as request, redirect_stdout(output):
                client.command('"' + missing + '"')
            self.assertIn('音频文件不存在', output.getvalue())
            self.assertIn(missing, output.getvalue())
            self.assertNotIn('网络', output.getvalue())
            request.assert_called_once_with('GET', '/config')

    def test_only_ready_audio_is_scheduled_for_automatic_playback(self):
        schedule = MagicMock()
        client = AudioRecognition('', '', '.', device_id='dev', audio_ready=schedule)
        message = {'type': 'recognition_result', 'channel': 'recognition', 'payload': {'code': 200,
                   'data': {'device_id': 'dev', 'request_id': 'req', 'status_revision': 1,
                   'results': [{'item_id': 'text', 'speech': {'status': 'skipped'}},
                               {'item_id': 'voice', 'speech': {'status': 'pending'}}]}}}
        with redirect_stdout(io.StringIO()):
            client.on_notification(message)
            schedule.assert_not_called()
            message['payload']['data']['status_revision'] = 2
            message['payload']['data']['results'][1]['speech']['status'] = 'ready'
            client.on_notification(message)
            client.on_notification(message)
        schedule.assert_called_once_with('req', 'voice')

    def test_disabled_never_uploads(self):
        client = AudioRecognition('http://localhost:9006', 'test-token', '.')
        with patch.object(client, '_json', return_value={'effective_enabled': False}) as request:
            with self.assertRaises(ValueError):
                client.upload('not-read.wav')
            self.assertEqual(request.call_count, 1)

    def test_unavailable_reason_distinguishes_switch_quota_and_model_config(self):
        import copy
        import sys
        base = {'effective_enabled': False,
                'policy': {'enabled': True, 'allowed': True, 'monthly_limit': 100,
                           'schemes': [{'scheme_id': 'cry', 'scheme_version': 1}]},
                'schemes': [{'scheme_id': 'cry', 'scheme_version': 1, 'enabled': True}],
                'usage': {'used': 0}}
        cases = [(None, '千问 Key'),
                 (lambda s: s['policy'].update(enabled=False), '开关未开启'),
                 (lambda s: s['policy'].update(allowed=False), '管理员已禁止'),
                 (lambda s: s['policy'].update(schemes=[]), '尚未选择'),
                 (lambda s: s['schemes'][0].update(enabled=False), '类型不可用'),
                 (lambda s: s['usage'].update(used=100), '剩余额度不足')]
        for change, message in cases:
            state = copy.deepcopy(base)
            if change:
                change(state)
            for mode in ('upload', 'record'):
                with self.subTest(reason=message, mode=mode):
                    capture = MagicMock()
                    client = AudioRecognition('http://localhost:9006', 'test-token', '.', capture=capture)
                    with patch.object(sys, 'platform', 'win32'), patch.object(client, '_json', return_value=state) as request:
                        with self.assertRaisesRegex(ValueError, message):
                            if mode == 'upload':
                                client.upload('not-read.wav')
                            else:
                                client.record_once(threading.Event())
                    capture.assert_not_called()
                    request.assert_called_once_with('GET', '/config')

    def test_download_uses_authenticated_local_route(self):
        with tempfile.TemporaryDirectory() as directory:
            client = AudioRecognition('http://localhost:9006', 'test-token', directory)
            task = {'results': [{'item_id': 'item1', 'speech': {'status': 'ready', 'url': 'https://evil.invalid'}}]}
            response = MagicMock()
            response.__enter__.return_value = response
            response.status_code = 200
            response.raw.read1.side_effect = [json.dumps({"code": 200, "data": task}).encode(), b"", wav(), b""]
            with patch.object(client, 'status', return_value=task), patch('audio_recognition.requests.request', return_value=response) as request:
                files = client.download('task1')
            self.assertEqual(Path(files[0]).read_bytes(), wav())
            self.assertEqual(request.call_args.args[1], 'http://localhost:9006/v1/recognition/requests/task1/audio/item1')
            self.assertFalse(request.call_args.kwargs['allow_redirects'])
            self.assertEqual(request.call_args.kwargs['headers']['Authorization'], 'Bearer test-token')

    def test_download_cancelled_before_network(self):
        stop = threading.Event()
        stop.set()
        client = AudioRecognition('http://localhost:9006', 'test', '.')
        with patch.object(client, '_request') as request, self.assertRaisesRegex(ValueError, '取消'):
            client.download('task1', stop_event=stop)
        request.assert_not_called()

    def test_trickle_download_checks_deadline_and_closes_response(self):
        client = AudioRecognition('http://localhost:9006', 'test', '.')
        response = MagicMock()
        response.__enter__.return_value = response
        response.status_code = 200
        response.raw.read1.return_value = b'x'
        with patch.object(client, '_request', return_value=response), patch('audio_recognition.time.monotonic', side_effect=[0, 1, 61]), self.assertRaisesRegex(ValueError, '超时'):
            client._download_body('/requests/task1', 1024, None, 60)
        response.__exit__.assert_called_once()
        response.raw.read1.assert_called_once()

    def test_download_cancelled_during_read_leaves_no_file(self):
        stop = threading.Event()
        response = MagicMock()
        response.__enter__.return_value = response
        response.status_code = 200
        def read(*args, **kwargs):
            stop.set()
            return b'x'
        response.raw.read1.side_effect = read
        with tempfile.TemporaryDirectory() as directory:
            client = AudioRecognition('http://localhost:9006', 'test', directory)
            with patch.object(client, '_request', return_value=response), self.assertRaisesRegex(ValueError, '取消'):
                client.download('task1', stop_event=stop)
            self.assertFalse(client.output_dir.exists())
        response.__exit__.assert_called_once()

    def test_download_read_error_is_sanitized(self):
        from urllib3.exceptions import ReadTimeoutError
        client = AudioRecognition('http://localhost:9006', 'test', '.')
        response = MagicMock()
        response.__enter__.return_value = response
        response.status_code = 200
        response.raw.read1.side_effect = ReadTimeoutError(None, 'private-address', 'raw details')
        with patch.object(client, '_request', return_value=response), self.assertRaisesRegex(ValueError, '^语音下载中断或超时，请重试$'):
            client.download('task1')
        response.__exit__.assert_called_once()

    def test_unbound_response_uses_code_not_message(self):
        client = AudioRecognition('http://localhost:9006', 'test', '.')
        response = MagicMock()
        response.__enter__.return_value = response
        response.status_code = 410
        response.json.return_value = {"code": 6006, "msg": "arbitrary diagnostic"}
        with patch.object(client, '_request', return_value=response), self.assertRaisesRegex(ValueError, '设备已解绑或重置.*6006'):
            client._json('GET', '/config')

    def test_windows_path_not_shell_parsed(self):
        client = AudioRecognition('', '', '.')
        with patch.object(client, 'upload', return_value={}) as upload:
            client.command('"D:\\audio samples\\cry.wav"')
            upload.assert_called_once_with('D:\\audio samples\\cry.wav')

    def test_invalid_task_path_rejected(self):
        client = AudioRecognition('http://localhost:9006', '', '.')
        with self.assertRaises(ValueError):
            client.status('../secret')


if __name__ == '__main__':
    unittest.main()
