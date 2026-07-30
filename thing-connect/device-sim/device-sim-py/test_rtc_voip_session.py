import unittest
from unittest import mock
import io
from contextlib import redirect_stdout

from rtc_voip_session import OUTGOING_RING_TIMEOUT_SEC, VoipCallState


class _FakeTimer:
    def __init__(self, interval, callback):
        self.interval = interval
        self.callback = callback
        self.daemon = False
        self.started = False
        self.cancelled = False

    def start(self):
        self.started = True

    def cancel(self):
        self.cancelled = True

    def fire(self):
        if not self.cancelled:
            self.callback()


class VoipCallStateTests(unittest.TestCase):
    def test_explicit_video_is_rejected_without_video(self):
        target = {
            "wx_open_id": "openid-1",
            "wx_app_id": "app-1",
            "wx_model_id": "model-1",
        }
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip, \
                mock.patch("rtc_voip_session.http_trace.request") as request:
            rtc_voip._LOG_LEVEL = 40
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
                auth_list=[target],
                video_capable=False,
            )

            state.do_call(target, "video")

            rtc_voip.report_profile.assert_not_called()
            request.assert_not_called()

    def test_failed_accept_restores_local_pending_call(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.is_active.return_value = False
            state = VoipCallState(
                "https://voip.example.com", "dev-1", "mqtt-token",
                "audio.g711a",
                before_accept=lambda _action: (_ for _ in ()).throw(
                    RuntimeError("start failed")),
            )
            state.on_call_incoming({
                "peer_id": "peer-1",
                "token": "token-1",
                "wx_room_id": "room-1",
                "wx_user_openid": "openid-1",
            })

            state.accept()

            self.assertTrue(state.has_pending())

    def test_cancel_during_starting_does_not_restore_pending_call(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.is_active.return_value = False
            rtc_voip.get_state.return_value = "IDLE"
            state = VoipCallState(
                "https://voip.example.com", "dev-1", "mqtt-token",
                "audio.g711a",
            )
            state._after_stop = mock.Mock()

            def cancel_start(_action, room_id):
                state.on_call_cancel({"wx_room_id": room_id})
                raise RuntimeError("cancelled")

            state._before_accept_ticket = cancel_start
            state.on_call_incoming({
                "peer_id": "peer-1",
                "token": "token-1",
                "wx_room_id": "room-1",
                "wx_user_openid": "openid-1",
            })

            state.accept()

            self.assertFalse(state.has_pending())
            self.assertEqual(state._active_room_id, "")
            state._after_stop.assert_called_once_with()
            rtc_voip.start_session.assert_not_called()

    def test_incoming_call_displays_payload_remark(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip.is_active.return_value = False
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
            )

            with redirect_stdout(io.StringIO()) as output:
                state.on_call_incoming({
                    "peer_id": "peer-1",
                    "token": "token-1",
                    "wx_room_id": "room-1",
                    "wx_user_openid": "openid-1",
                    "wx_user_remark": "刘德华1",
                })

            rendered = output.getvalue()
            self.assertIn("微信来电", rendered)
            self.assertIn("联系人=刘德华1", rendered)
            self.assertIn("wx_open_id=openid-1", rendered)

    def test_incoming_call_uses_cached_contact_remark_for_legacy_payload(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip.is_active.return_value = False
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
                auth_list=[{"wx_open_id": "openid-1", "remark": "缓存备注"}],
            )

            with redirect_stdout(io.StringIO()) as output:
                state.on_call_incoming({
                    "peer_id": "peer-1",
                    "token": "token-1",
                    "wx_room_id": "room-1",
                    "wx_user_openid": "openid-1",
                })

            self.assertIn("联系人=缓存备注", output.getvalue())

    def test_callers_update_preserves_cached_contacts_when_refresh_fails(self):
        cached = [{"wx_open_id": "openid-1", "remark": "缓存备注"}]
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.report_profile.return_value = None
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
                auth_list=cached,
            )
            state.set_callers_refresh_submitter(
                lambda callback: (callback(), True)[1])

            state.on_callers_update()

            self.assertEqual(state.list_callers(), cached)
            rtc_voip.report_profile.assert_called_once_with(
                "https://voip.example.com",
                "mqtt-token",
                contacts_error_none=True,
            )

    def test_outgoing_ringback_accepts_when_payload_uses_wx_open_id(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.is_active.return_value = False
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
                before_start=lambda action: action(),
            )
            state._outgoing_call = True
            state._outgoing_openid = "openid-1"

            state.on_call_incoming({
                "peer_id": "peer-1",
                "token": "token-1",
                "wx_room_id": "room-1",
                "wx_open_id": "openid-1",
            })

            rtc_voip.start_session.assert_called_once_with(
                "peer-1", "token-1", "audio.g711a",
                with_video=True, session_role="device_caller",
            )

    def test_outgoing_ringback_matches_call_id_when_openid_is_missing(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.is_active.return_value = False
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
                before_start=lambda action: action(),
            )
            state._outgoing_call = True
            state._outgoing_openid = "openid-1"
            state._outgoing_call_id = "call-1"

            state.on_call_incoming({
                "peer_id": "peer-1",
                "token": "token-1",
                "wx_room_id": "room-1",
                "wx_call_id": "call-1",
                "wx_from": "dev-1",
            })

            rtc_voip.start_session.assert_called_once()
            rtc_voip.reject_session.assert_not_called()

    def test_same_openid_incoming_does_not_steal_outgoing_room(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.is_active.return_value = False
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
            )
            state._outgoing_call = True
            state._outgoing_openid = "openid-1"
            state._outgoing_call_id = "call-1"

            state.on_call_incoming({
                "peer_id": "peer-2",
                "token": "token-2",
                "wx_room_id": "room-2",
                "wx_user_openid": "openid-1",
                "wx_app_id": "app-1",
                "wx_model_id": "model-1",
                "wx_server_token": "server-token",
                "wx_payload": "",
            })

            rtc_voip.reject_session.assert_called_once()
            rtc_voip.start_session.assert_not_called()

    def test_own_outgoing_callback_recovers_after_http_response_is_lost(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.is_active.return_value = False
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
                before_start=lambda action: action(),
            )

            state.on_call_incoming({
                "peer_id": "peer-1",
                "token": "token-1",
                "wx_room_id": "room-1",
                "wx_call_id": "call-1",
                "wx_from": "dev-1",
                "wx_room_type": "voice",
            })

            rtc_voip.start_session.assert_called_once_with(
                "peer-1", "token-1", "audio.g711a",
                with_video=False, session_role="device_caller",
            )

    def test_outgoing_ringback_accepts_when_payload_openid_missing(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.is_active.return_value = False
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
                before_start=lambda action: action(),
            )
            state._outgoing_call = True
            state._outgoing_openid = "openid-1"

            state.on_call_incoming({
                "peer_id": "peer-1",
                "token": "token-1",
                "wx_room_id": "room-1",
            })

            rtc_voip.start_session.assert_called_once_with(
                "peer-1", "token-1", "audio.g711a",
                with_video=True, session_role="device_caller",
            )
            rtc_voip.reject_session.assert_not_called()

    def test_outgoing_ringback_rejects_explicit_other_openid(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.is_active.return_value = False
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
            )
            state._outgoing_call = True
            state._outgoing_openid = "openid-1"

            state.on_call_incoming({
                "peer_id": "peer-2",
                "token": "token-2",
                "wx_room_id": "room-2",
                "wx_user_openid": "openid-2",
                "wx_app_id": "app-1",
                "wx_model_id": "model-1",
                "wx_server_token": "server-token",
                "wx_payload": "{}",
            })

            rtc_voip.reject_session.assert_called_once_with(
                "app-1", "model-1", "server-token", "room-2", "{}", 5
            )
            rtc_voip.start_session.assert_not_called()

    def test_call_cancel_clears_outgoing_wait_state(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.get_state.return_value = "IDLE"
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
            )
            state._outgoing_call = True
            state._outgoing_openid = "openid-1"

            state.on_call_cancel({
                "wx_room_id": "room-1",
                "wx_user_openid": "openid-1",
            })

            self.assertFalse(state.is_outgoing())
            rtc_voip.stop_session.assert_not_called()

    def test_call_cancel_clears_requested_outgoing_cancel_state(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.get_state.return_value = "IDLE"
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
            )
            state._outgoing_call = True
            state._outgoing_openid = "openid-1"
            state._outgoing_cancel_requested = True

            state.on_call_cancel({
                "wx_room_id": "room-1",
                "wx_user_openid": "openid-1",
            })

            self.assertFalse(state.is_outgoing())
            self.assertFalse(state._outgoing_cancel_requested)
            rtc_voip.stop_session.assert_not_called()

    def test_stale_room_only_cancel_does_not_clear_unrelated_outgoing_call(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.get_state.return_value = "IDLE"
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
            )
            state._outgoing_call = True
            state._outgoing_openid = "openid-1"
            state._outgoing_call_id = "call-1"

            state.on_call_cancel({"wx_room_id": "stale-room"})

            self.assertTrue(state.is_outgoing())
            rtc_voip.stop_session.assert_not_called()

    def test_do_call_clears_stale_contact_when_refresh_is_empty(self):
        target = {
            "wx_open_id": "openid-1",
            "wx_app_id": "app-1",
            "wx_model_id": "model-1",
        }
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip, \
                mock.patch("rtc_voip_session.http_trace.request") as request:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.is_active.return_value = False
            rtc_voip.report_profile.return_value = []
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
                auth_list=[target],
            )

            state.do_call(target, "audio")

            self.assertEqual(state._auth_list, [])
            request.assert_not_called()

    def test_do_call_uses_refreshed_contact_fields(self):
        old_target = {
            "wx_open_id": "openid-1",
            "wx_app_id": "old-app",
            "wx_model_id": "old-model",
        }
        refreshed = {
            "wx_open_id": "openid-1",
            "wx_app_id": "new-app",
            "wx_model_id": "new-model",
            "remark": "新备注",
        }
        response = mock.Mock()
        response.status_code = 200
        response.headers = {"Content-Type": "application/json"}
        response.json.return_value = {"code": 0, "msg": "ok", "data": {"call_id": "call-1"}}
        timers = []
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip, \
                mock.patch("rtc_voip_session.http_trace.request", return_value=response) as request, \
                mock.patch(
                    "rtc_voip_session.threading.Timer",
                    side_effect=lambda interval, callback: timers.append(
                        _FakeTimer(interval, callback)
                    ) or timers[-1],
                ):
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.is_active.return_value = False
            rtc_voip.report_profile.return_value = [refreshed]
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
                auth_list=[old_target],
            )

            state.do_call(old_target, "audio")

            body = request.call_args.kwargs["json"]
            self.assertEqual(body["wx_app_id"], "new-app")
            self.assertEqual(body["wx_model_id"], "new-model")
            self.assertTrue(state.is_outgoing())
            self.assertEqual(state._outgoing_call_id, "call-1")
            self.assertEqual(len(timers), 1)
            self.assertEqual(timers[0].interval, OUTGOING_RING_TIMEOUT_SEC)

            timers[0].fire()
            self.assertFalse(state.is_outgoing())

            state.on_call_incoming({
                "peer_id": "late-peer",
                "token": "late-token",
                "wx_room_id": "late-room",
                "wx_call_id": "call-1",
                "wx_from": "dev-1",
                "wx_app_id": "new-app",
                "wx_model_id": "new-model",
                "wx_server_token": "server-token",
                "wx_payload": "",
            })
            rtc_voip.start_session.assert_not_called()
            rtc_voip.reject_session.assert_called_once()

    def test_do_call_only_removes_contact_for_voip_auth_error(self):
        target = {
            "wx_open_id": "openid-1",
            "wx_app_id": "app-1",
            "wx_model_id": "model-1",
        }
        cases = (
            (200, 40205, []),
            (401, 401, [target]),
            (200, 6006, [target]),
        )
        for http_status, code, expected_contacts in cases:
            with self.subTest(http_status=http_status, code=code), \
                    mock.patch("rtc_voip_session.rtc_voip") as rtc_voip, \
                    mock.patch("rtc_voip_session.http_trace.request") as request:
                rtc_voip._LOG_LEVEL = 40
                rtc_voip.is_active.return_value = False
                rtc_voip.report_profile.return_value = [target]
                response = mock.Mock(
                    status_code=http_status,
                    headers={"Content-Type": "application/json"},
                    text="",
                )
                response.json.return_value = {
                    "code": code,
                    "msg": "测试错误",
                }
                request.return_value = response
                state = VoipCallState(
                    "https://voip.example.com",
                    "dev-1",
                    "mqtt-token",
                    "audio.g711a",
                    auth_list=[target],
                    before_start=lambda action: action(),
                )

                state.do_call(target, "audio")

                self.assertEqual(state._auth_list, expected_contacts)
                self.assertFalse(state.is_outgoing())

    def test_outgoing_callback_before_http_response_does_not_rearm_wait_state(self):
        target = {
            "wx_open_id": "openid-1",
            "wx_app_id": "app-1",
            "wx_model_id": "model-1",
            "remark": "朋友",
        }
        response = mock.Mock(
            status_code=200,
            headers={"Content-Type": "application/json"},
            text="",
        )
        response.json.return_value = {
            "code": 0,
            "msg": "ok",
            "data": {"call_id": "call-1"},
        }
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            rtc_voip.is_active.return_value = False
            rtc_voip.report_profile.return_value = [target]
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
                auth_list=[target],
                before_start=lambda action: action(),
            )

            def request_with_early_callback(*_args, **_kwargs):
                state.on_call_incoming({
                    "peer_id": "peer-1",
                    "token": "token-1",
                    "wx_room_id": "room-1",
                    "wx_user_openid": "openid-1",
                    "wx_call_id": "call-1",
                    "wx_from": "dev-1",
                })
                return response

            with mock.patch(
                "rtc_voip_session.http_trace.request",
                side_effect=request_with_early_callback,
            ):
                state.do_call(target, "video")

            rtc_voip.start_session.assert_called_once()
            self.assertFalse(state.is_outgoing())
            self.assertEqual(state._active_room_id, "room-1")

    def test_provisional_outgoing_rejects_same_openid_reverse_call(self):
        with mock.patch("rtc_voip_session.rtc_voip") as rtc_voip:
            rtc_voip._LOG_LEVEL = 40
            state = VoipCallState(
                "https://voip.example.com",
                "dev-1",
                "mqtt-token",
                "audio.g711a",
            )
            state._outgoing_call = True
            state._outgoing_openid = "openid-1"

            state.on_call_incoming({
                "peer_id": "peer-2",
                "token": "token-2",
                "wx_room_id": "room-2",
                "wx_user_openid": "openid-1",
                "wx_call_id": "mini-call-2",
                "wx_from": "",
                "wx_app_id": "app-1",
                "wx_model_id": "model-1",
                "wx_server_token": "server-token",
                "wx_payload": "",
            })

            rtc_voip.start_session.assert_not_called()
            rtc_voip.reject_session.assert_called_once()


if __name__ == "__main__":
    unittest.main()
