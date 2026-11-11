"""Business invariants under redelivery, cancellation and partial failure."""
import json
import unittest
from types import SimpleNamespace
from unittest import mock

import device_flow
from rtc_call_session import CallState
from session_arbiter import SessionArbiter
from session_coordinator import SessionKind
from session_router import SessionMessageRouter, TerminalController


class CallBusinessTests(unittest.TestCase):
    def setUp(self):
        patch = mock.patch('rtc_call_session.rtc_call')
        self.rtc = patch.start()
        self.addCleanup(patch.stop)
        self.rtc._LOG_LEVEL = 40
        coordinator = mock.Mock()
        coordinator.begin.side_effect = lambda kind, action: action()
        self.arbiter = SessionArbiter(coordinator)
        self.addCleanup(self.arbiter.shutdown)
        self.state = CallState('https://call.test', 'device', 'test',
            before_accept_ticket=lambda action, room: self.arbiter.begin(
                SessionKind.CALL, action, consume_pending=True, session_id=room),
            after_stop=lambda: self.arbiter.finish_async(SessionKind.CALL))
        self.router = SessionMessageRouter(self.arbiter, mock.Mock(), self.state)
        self.addCleanup(self.router.shutdown)
        self.incoming('room-a')

    def incoming(self, room):
        self.router.on_device_call_incoming({'room_id': room, 'caller_id': 'caller', 'call_type': 'audio'})

    def test_duplicate_pending_and_active_call_never_rejects_original(self):
        with mock.patch.object(self.state, 'reject_incoming') as reject:
            self.incoming('room-a')
            self.arbiter.begin(SessionKind.CALL, lambda: None, consume_pending=True, session_id='room-a')
            self.incoming('room-a')
            self.router.shutdown()
        reject.assert_not_called()

    def test_old_rejection_cannot_clear_new_pending_ticket(self):
        terminal = TerminalController(self.arbiter, mock.Mock(), mock.Mock(), self.state)
        def request(*args, **kwargs):
            self.router.on_room_cancel({'room_id': 'room-a'})
            self.incoming('room-b')
        with mock.patch('rtc_call_session.http_trace.request', side_effect=request):
            terminal._reject('decline')
        self.assertTrue(self.arbiter.has_pending(SessionKind.CALL, 'room-b'))
        self.assertEqual(self.state._pending_call['room_id'], 'room-b')

    def test_resource_failure_does_not_answer_remote(self):
        self.state._before_accept_ticket = mock.Mock(side_effect=RuntimeError('resource'))
        with mock.patch('rtc_call_session.http_trace.request') as request:
            with self.assertRaises(RuntimeError):
                self.state.do_accept()
        request.assert_not_called()
        self.assertTrue(self.state.has_pending())

    def test_failed_answer_or_connect_compensates_exact_room(self):
        import requests
        for failure in ('transport', 'json', 'token', 'connect'):
            with self.subTest(failure=failure):
                self.arbiter.finish(SessionKind.CALL)
                self.arbiter.clear_pending(SessionKind.CALL)
                self.state._room_id = None
                self.incoming('room-a')
                response = mock.Mock()
                response.json.return_value = {'code': 200, 'data': {'token': 'token'}}
                if failure == 'json':
                    response.json.side_effect = ValueError('malformed')
                elif failure == 'token':
                    response.json.return_value = {'code': 200, 'data': {}}
                self.rtc.connect_to.side_effect = RuntimeError('sdk') if failure == 'connect' else None
                def request(method, url, **kwargs):
                    if url.endswith('/hangup'):
                        return mock.Mock(json=lambda: {'code': 200})
                    if url.endswith('/device/info') and failure == 'transport':
                        raise requests.Timeout()
                    return response
                with mock.patch('rtc_call_session.http_trace.request', side_effect=request) as request_mock:
                    self.state.do_accept()
                self.assertEqual(request_mock.call_args_list[-1].args[1], 'https://call.test/v1/call/hangup')
                self.assertEqual(request_mock.call_args_list[-1].kwargs['json']['room_id'], 'room-a')
                self.assertFalse(self.state.has_pending())
                self.assertFalse(self.state.has_room())

    def test_sdk_failure_callback_retries_exact_room_release(self):
        self.rtc.connect_to.side_effect = lambda *a, **kw: self.state._on_connect_failed()
        releases = []
        def request(method, url, **kwargs):
            if url.endswith('/device/info'):
                return mock.Mock(json=lambda: {'code': 200, 'data': {'token': 'token'}})
            releases.append(kwargs['json']['room_id'])
            return mock.Mock(json=lambda: {'code': 50000 if len(releases) < 3 else 200})
        with mock.patch('rtc_call_session.http_trace.request', side_effect=request), \
                mock.patch('rtc_call_session.time.sleep') as sleep:
            self.state.do_accept()
        self.assertEqual(releases, ['room-a'] * 3)
        self.assertEqual(sleep.call_args_list, [mock.call(0.2), mock.call(0.4)])
        self.assertFalse(self.state.has_room())
        self.rtc.hangup.assert_called_once()

    def test_answer_notification_retains_deadline_until_matching_p2p(self):
        self.state._room_id, self.state._role = 'outgoing', 'caller'
        self.state._before_continue = lambda action: action()
        with mock.patch('rtc_call_session.threading.Timer') as timer_factory:
            self.state._start_ring_timer('outgoing')
            timer = timer_factory.return_value
            timeout = timer_factory.call_args.args[1]
            for _ in range(2):
                self.state.on_callee_answered({'room_id': 'outgoing'})
            self.state._on_p2p_connected('old-room')
            timer.cancel.assert_not_called()
            self.state._on_p2p_connected('outgoing')
            timer.cancel.assert_called_once()
            with mock.patch('rtc_call_session.http_trace.request') as request:
                timeout()
            request.assert_not_called()
            self.assertTrue(self.state.has_room())

    def test_answered_without_p2p_expires_and_late_http_preserves_new_call(self):
        self.state._room_id, self.state._role = 'outgoing', 'caller'
        self.state._before_continue = lambda action: action()
        with mock.patch('rtc_call_session.threading.Timer') as timers:
            self.state._start_ring_timer('outgoing')
            timeout = timers.call_args.args[1]
            self.state.on_callee_answered({'room_id': 'outgoing'})
            def request(*args, **kwargs):
                self.assertFalse(self.state.has_room())
                self.state._room_id = 'new-room'
            with mock.patch('rtc_call_session.http_trace.request', side_effect=request) as http:
                timeout()
            self.assertEqual(http.call_args.kwargs['json']['room_id'], 'outgoing')
            self.assertEqual(self.state._room_id, 'new-room')

    def test_voip_reject_preserves_next_ticket(self):
        from rtc_voip_session import VoipCallState
        import threading
        voip = VoipCallState.__new__(VoipCallState)
        voip._lock = threading.Lock()
        voip._incoming_generation = 1
        voip._pending_call = dict(wx_room_id='wx-a', wx_app_id='app', wx_model_id='model',
                                  wx_server_token='test', wx_payload={})
        self.arbiter.clear_pending(SessionKind.CALL)
        self.arbiter.offer_pending(SessionKind.VOIP, 'wx-a')
        terminal = TerminalController(self.arbiter, voip, mock.Mock(), self.state)
        def reject(*args):
            self.assertFalse(self.arbiter.has_pending(SessionKind.VOIP, 'wx-a'))
            self.arbiter.offer_pending(SessionKind.VOIP, 'wx-b')
            voip._pending_call = {'wx_room_id': 'wx-b'}
        with mock.patch('rtc_voip_session.rtc_voip.reject_session', side_effect=reject):
            terminal._reject('decline')
        self.assertTrue(self.arbiter.has_pending(SessionKind.VOIP, 'wx-b'))
        self.assertEqual(voip._pending_call['wx_room_id'], 'wx-b')


class BindingPersistenceTests(unittest.TestCase):
    def run_grant(self, save, payload=None, existing=('', '')):
        client = mock.Mock()
        grant = {'type': 'auth_grant', 'payload': payload or {}}
        def deliver():
            client.on_message(client, None, SimpleNamespace(topic='device/temp/cmd', payload=json.dumps(grant).encode()))
            save.assert_not_called()
            client.publish.assert_not_called()
        client.loop_start.side_effect = deliver
        with mock.patch.object(device_flow, '_new_mqtt_client', return_value=client):
            try:
                device_flow.connect_temp_mqtt('localhost', 1883, 'temp', 'test', 1, False,
                    persist_credentials=save, existing_credentials=existing)
            finally:
                client.disconnect.assert_called_once()
                client.loop_stop.assert_called_once()
        return client

    def test_persist_before_ack(self):
        save = mock.Mock(return_value=True)
        client = self.run_grant(save, {'device_id': 'test', 'device_key': 'key'})
        save.assert_called_once_with('test', 'key')
        client.publish.assert_called_once()
        client.publish.return_value.wait_for_publish.assert_called_once()

    def test_save_failure_never_acknowledges(self):
        client = mock.Mock()
        client.loop_start.side_effect = lambda: client.on_message(client, None,
            SimpleNamespace(topic='device/temp/cmd', payload=b'{"type":"auth_grant","payload":{"device_id":"d","device_key":"k"}}'))
        with mock.patch.object(device_flow, '_new_mqtt_client', return_value=client):
            with self.assertRaises(RuntimeError):
                device_flow.connect_temp_mqtt('localhost', 1883, 'temp', 'test', 1, False,
                    persist_credentials=mock.Mock(return_value=False))
        client.publish.assert_not_called()
        client.disconnect.assert_called_once()

    def test_empty_grant_requires_persisted_local_pair(self):
        with self.assertRaises(RuntimeError):
            self.run_grant(mock.Mock(return_value=True))
        save = mock.Mock(return_value=True)
        self.run_grant(save, existing=('local', 'key'))
        save.assert_called_once_with('local', 'key')
