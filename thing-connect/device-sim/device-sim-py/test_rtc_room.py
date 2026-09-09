import ctypes
import json
import os
import threading
import unittest
from types import SimpleNamespace
from unittest import mock

os.environ.setdefault("TIRTC_SDK_VERSION", "2.3.0")
import rtc_room
from rtc_room import RoomSession, RoomError, descriptor


class RoomSessionTests(unittest.TestCase):
    def setUp(self):
        self.config = SimpleNamespace(hardware_audio=False, up_audio_format='alaw_8khz',
            down_audio_format='alaw_8khz', up_audio_file='', device_id='device-1',
            mqtt_token='device-token', call_server='https://call.example.test')
        self.runtime = mock.Mock()
        self.room = RoomSession(self.config, self.runtime, mock.Mock(), mock.Mock(), lambda: True)
        self.room.assignment = {'room_id': 'room-1', 'assignment_version': 1,
                                'desired_state': 'joined'}
        self.room.session_id = 'session-1'
        self.room.generation = 3
        self.room.conn = 123
        self.room.state = 'joining'
        self.send = mock.patch.object(rtc_room.sdk, 'TiRtcSendCommand', side_effect=lambda conn, cmd, data, length: length).start()
        self.disconnect = mock.patch.object(rtc_room.sdk, 'TiRtcDisconnect').start()
        self.addCleanup(mock.patch.stopall)
        self.addCleanup(self.room.http.close)

    def test_snapshot_timeout_diagnostic_is_once_per_query(self):
        self.room.state = 'joined'
        with mock.patch.object(rtc_room.time, 'monotonic', return_value=10), \
                mock.patch.object(self.room, '_diagnostic') as log:
            self.room._request_members()
            log.reset_mock()
            self.room._check_snapshot_wait(14.9)
            log.assert_not_called()
            self.room._check_snapshot_wait(15)
            self.room._check_snapshot_wait(20)
            log.assert_called_once()
            self.assertIn('成员快照等待 5.0s', log.call_args.args[0])
            self.room._request_members()
            log.reset_mock()
            self.room._check_snapshot_wait(16)
            log.assert_called_once()

    def test_snapshot_arrival_clears_pending_wait_and_reports_duration(self):
        self.room.state = 'joined'
        self.room.snapshot_started = 10
        with mock.patch.object(rtc_room.time, 'monotonic', return_value=12), \
                mock.patch.object(self.room, '_diagnostic') as log:
            self.room._signal({'jsonrpc': '2.0', 'method': 'room_snapshot',
                               'params': {'participants': []}})
            self.assertTrue(any('2000ms' in c.args[0] for c in log.call_args_list))
            log.reset_mock()
            self.room._check_snapshot_wait(30)
            log.assert_not_called()
        self.assertIsNone(self.room.snapshot_started)

    def test_diagnostics_do_not_print_remote_error_or_unknown_method(self):
        with mock.patch.object(self.room, '_diagnostic') as log:
            self.room._signal({'jsonrpc': '2.0', 'method': 'secret-remote-text',
                               'error': {'code': 123, 'message': 'secret-token'}})
        self.assertNotIn('secret', str(log.call_args_list))
        self.assertIn('123', str(log.call_args_list))

    def test_mismatched_snapshot_logs_reason_without_applying(self):
        with mock.patch.object(self.room, '_diagnostic') as log:
            self.room._signal({'jsonrpc': '2.0', 'method': 'room_snapshot',
                               'params': {'room_id': 'other-room', 'participants': []}})
        self.assertFalse(self.room.members_synced)
        self.assertIn('received_room=other-room expected_room=room-1', log.call_args.args[0])

    def test_namespaced_snapshot_applies_and_pins_namespace(self):
        self.room.state = 'joined'
        self.room._signal({'jsonrpc': '2.0', 'method': 'room_snapshot', 'params': {
            'room_id': '2818153:room-1', 'participants': [{'participant_id': 'p1'}]}})
        self.assertTrue(self.room.members_synced)
        self.assertIn('p1', self.room.members)
        self.assertFalse(self.room._matches_room('other:room-1'))
        self.assertFalse(self.room._matches_room('2818153:not-room-1'))
        self.assertTrue(self.room._matches_room('room-1'))

    def test_room_diagnostics_are_opt_in_and_can_be_disabled(self):
        import io
        from contextlib import redirect_stdout
        with redirect_stdout(io.StringIO()) as output:
            self.room._diagnostic('hidden')
        self.assertEqual(output.getvalue(), '')
        with redirect_stdout(io.StringIO()) as output:
            self.room.command(['room', 'debug', 'on'])
            self.room._diagnostic('visible')
            self.room.command(['room', 'debug', 'off'])
            self.room._diagnostic('hidden')
        self.assertIn('visible', output.getvalue())
        self.assertNotIn('hidden', output.getvalue())

    def test_room_requests_use_call_namespace(self):
        response = mock.Mock(content=b'{}')
        response.json.return_value = {'code': 200, 'data': {}}
        with mock.patch.object(self.room.http, 'request', return_value=response) as request:
            for path in ('assignment', 'create', 'join', 'leave', 'connect-token', 'presence'):
                self.room._api('GET' if path == 'assignment' else 'POST', path)
                self.assertEqual(request.call_args.args[1],
                                 'https://call.example.test/v1/call/group/device/' + path)

    def test_join_and_members_are_announced_without_private_payloads(self):
        import io
        from contextlib import redirect_stdout
        self.room.assignment['room_code'] = '001234'
        output = io.StringIO()
        with redirect_stdout(output):
            self.joined()
            self.assertIn('等待房间同步', output.getvalue())
            self.room._signal({'jsonrpc': '2.0', 'method': 'room_snapshot', 'params': {
                'room_id': 'room-1', 'participants': [
                    {'participant_id': 'p1', 'device_id': 'device-1', 'mic_state': 'off', 'token': 'private-value'},
                    {'participant_id': 'p2', 'device_id': 'device-2', 'mic_state': 'speaking'}]}})
            self.room.command(['room', 'status'])
        text = output.getvalue()
        self.assertIn('房间号：001234', text)
        self.assertIn('房间 ID：room-1', text)
        self.assertIn('在线成员（2）', text)
        self.assertIn('device-1（本机） · 收听中', text)
        self.assertIn('device-2 · 正在说话', text)
        self.assertNotIn('private-value', text)
        output = io.StringIO()
        with redirect_stdout(output):
            self.room._signal({'jsonrpc': '2.0', 'method': 'participant_left',
                               'params': {'participant_id': 'p2'}})
        self.assertIn('在线成员（1）', output.getvalue())

    def test_missing_initial_snapshot_can_be_requested_again(self):
        self.joined()
        methods = [json.loads(call.args[2])['method'] for call in self.send.call_args_list]
        self.assertIn('get_room_snapshot', methods)
        self.send.reset_mock()
        self.room.command(['room', 'status'])
        self.assertEqual(json.loads(self.send.call_args.args[2]),
                         {'jsonrpc': '2.0', 'method': 'get_room_snapshot'})
        self.assertFalse(self.room.members_synced)
        self.room._signal({'jsonrpc': '2.0', 'method': 'room_snapshot', 'params': {
            'room_id': 'room-1', 'participants': [
                {'participant_id': 'p1', 'device_id': 'device-1'},
                {'participant_id': 'p2', 'device_id': 'device-2'}]}})
        self.assertTrue(self.room.members_synced)
        self.assertEqual(len(self.room.members), 2)
        self.room.state = 'idle'
        self.send.reset_mock()
        self.room.command(['room', 'status'])
        self.send.assert_not_called()

    def test_snapshot_send_uses_sdk_byte_count_success(self):
        import io
        from contextlib import redirect_stdout
        self.room.state = 'joined'
        output = io.StringIO()
        with redirect_stdout(output):
            self.room.command(['room', 'status'])
        self.assertIn('正在查询房间成员', output.getvalue())
        self.assertNotIn('发送失败', output.getvalue())
        self.send.side_effect = None
        self.send.return_value = -40004
        output = io.StringIO()
        with redirect_stdout(output):
            self.room.command(['room', 'status'])
        self.assertIn('成员查询发送失败 code=-40004', output.getvalue())

    def test_snapshot_callback_accepts_sdk_sequence_and_response_flag(self):
        self.joined()
        raw = json.dumps({'jsonrpc': '2.0', 'method': 'room_snapshot', 'params': {
            'room_id': 'room-1', 'participants': [{'participant_id': 'p1', 'device_id': 'device-1'}]}}).encode()
        buf = ctypes.create_string_buffer(raw)
        self.room.callbacks.on_command(123, 0x12342201, buf, len(raw))
        self.room._event(self.room.events.get_nowait())
        self.assertTrue(self.room.members_synced)
        self.assertEqual(len(self.room.members), 1)
        self.room.callbacks.on_command(123, 0x12342101, buf, len(raw))
        self.assertTrue(self.room.events.empty())

    def test_shutdown_releases_lease_after_control_worker_stops(self):
        self.joined()
        self.room.worker = mock.Mock()
        self.room.media_worker = mock.Mock()
        with mock.patch.object(self.room, '_api') as request:
            def after_stop(*args):
                self.room.worker.join.assert_called_once()
                self.room.media_worker.join.assert_called_once()
                self.assertIsNone(self.room.conn)
            request.side_effect = after_stop
            self.room.shutdown()
        request.assert_called_once_with('POST', 'presence', {
            'room_id': 'room-1', 'assignment_version': 1,
            'session_id': 'session-1', 'state': 'suspended'})

    def joined(self):
        self.room._signal({'jsonrpc': '2.0', 'id': 1, 'result': {
            'session_id': 'rtc-session', 'input_audio': descriptor('alaw_8khz'),
            'output_audio': descriptor('alaw_8khz')}})

    def test_join_is_muted_and_release_and_preemption_close_gate(self):
        self.joined()
        self.assertFalse(self.room.ptt)
        self.room.set_ptt(True)
        self.assertTrue(self.room.ptt)
        self.room.set_ptt(False)
        self.assertFalse(self.room.ptt)
        self.room.set_ptt(True)
        self.room.stop_service()
        self.assertFalse(self.room.ptt)
        self.assertIsNone(self.room.conn)
        self.assertEqual(self.room.members, {})
        self.disconnect.assert_called_once_with(123)

    def test_late_connection_does_not_replace_new_generation(self):
        self.room._event(('connected', 2, 456, 0))
        self.disconnect.assert_called_once_with(456)
        self.assertEqual(self.room.conn, 123)
        self.runtime.bind_active_connection.assert_not_called()

    def test_wrong_audio_negotiation_never_opens_media_gate(self):
        with self.assertRaises(RoomError):
            self.room._signal({'jsonrpc': '2.0', 'id': 1, 'result': {
                'session_id': 'rtc-session', 'input_audio': descriptor('pcm_s16le_16khz'),
                'output_audio': descriptor('alaw_8khz')}})
        self.assertEqual(self.room.state, 'joining')
        self.assertFalse(self.room.ptt)

    def test_snapshot_and_duplicate_member_updates(self):
        self.joined()
        def event(method, params):
            self.room._signal({'jsonrpc': '2.0', 'method': method, 'params': params})
        event('room_snapshot', {'room_id': 'room-1', 'participants': [
            {'participant_id': 'p1', 'mic_state': 'off'}]})
        event('participant_joined', {'participant': {'participant_id': 'p1', 'mic_state': 'off'}})
        event('participant_mic_state_changed', {'participant_id': 'p1', 'mic_state': 'speaking'})
        self.assertEqual(len(self.room.members), 1)
        self.assertEqual(self.room.members['p1']['mic_state'], 'speaking')
        event('room_snapshot', {'room_id': 'another', 'participants': []})
        self.assertEqual(len(self.room.members), 1)
        event('participant_left', {'participant_id': 'p1'})
        self.assertEqual(self.room.members, {})

    def test_stale_queued_report_does_not_finish_current_room(self):
        self.room._api = mock.Mock(side_effect=RoomError(40921, 'stale'))
        self.room._event(('report', {'state': 'suspended'}))
        self.room.finish.assert_not_called()
        self.assertEqual(self.room.conn, 123)

    def test_callback_copies_payload_and_queue_is_bounded(self):
        data = ctypes.create_string_buffer(b'{"jsonrpc":"2.0"}')
        self.room.callbacks.on_command(123, rtc_room.ROOM_COMMAND, data, len(data.value))
        data.value = b'changed'
        event = self.room.events.get_nowait()
        self.assertEqual(json.loads(event[3]), {'jsonrpc': '2.0'})
        for _ in range(128):
            self.assertTrue(self.room._enqueue(('report', {})))
        self.assertFalse(self.room._enqueue(('report', {})))

    def test_rejected_join_preserves_current_audio_session(self):
        self.joined()
        self.room._api = mock.Mock(side_effect=RoomError(40320, 'wrong password'))
        with mock.patch('builtins.print'):
            self.room._event(('operation', ('join', '001234', '9999')))
        self.assertEqual(self.room.state, 'joined')
        self.assertEqual(self.room.conn, 123)
        self.room.finish.assert_not_called()

    def test_bare_room_preserves_existing_call_command(self):
        self.assertFalse(self.room.command(['room']))

    def test_packet_captured_before_release_is_not_sent(self):
        self.joined()
        self.room.ptt = True
        captured, proceed = threading.Event(), threading.Event()
        def capture():
            captured.set()
            proceed.wait(1)
            return b'\xd5' * 320, 40
        self.room.hardware = mock.Mock()
        self.room.hardware.capture.side_effect = capture
        worker = threading.Thread(target=self.room._media_loop)
        with mock.patch.object(rtc_room.sdk, 'TiRtcSendAudioStream') as send:
            worker.start()
            try:
                self.assertTrue(captured.wait(1))
                self.room.set_ptt(False)
                proceed.set()
                self.room.closed.set()
            finally:
                proceed.set()
                self.room.closed.set()
                worker.join(2)
            self.assertFalse(worker.is_alive())
            send.assert_not_called()
