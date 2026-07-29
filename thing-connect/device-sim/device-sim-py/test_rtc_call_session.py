import unittest
import threading
from unittest import mock
import io
from contextlib import redirect_stdout

from rtc_call_session import CallState


class DeleteContactTests(unittest.TestCase):
    def test_delete_contact_prints_full_http_request(self):
        state = CallState.__new__(CallState)
        state._call_server = "https://call.example"
        state._mqtt_token = "secret-token"
        state._contact_list = []

        response = mock.Mock(status_code=200)
        response.json.return_value = {"code": 200, "data": {}}
        with mock.patch("rtc_call_session.requests.delete", return_value=response) as delete:
            with mock.patch("http_trace._emit") as trace:
                state.do_delete_contact("peer device")

        delete.assert_called_once_with(
            "https://call.example/v1/call/device/contacts",
            params={"peer_id": "peer device"},
            headers={
                "Authorization": "Bearer secret-token",
                "Content-Type": "application/json",
            },
            timeout=10,
        )
        trace.assert_any_call(
            "请求: DELETE "
            "https://call.example/v1/call/device/contacts?peer_id=peer+device"
        )
        trace.assert_any_call(
            '请求头: {"Authorization":"Bearer secret-token","Content-Type":"application/json"}'
        )

    def test_remark_contact_sends_peer_id_and_full_remark(self):
        state = CallState.__new__(CallState)
        state._call_server = "https://call.example"
        state._mqtt_token = "secret-token"

        response = mock.Mock(status_code=200)
        response.json.return_value = {"code": 200, "data": None}
        with mock.patch("rtc_call_session.requests.put", return_value=response) as put:
            with mock.patch("http_trace._emit") as trace:
                state.do_remark("peer-device", "客厅 的 设备")

        put.assert_called_once_with(
            "https://call.example/v1/call/device/contacts/remark",
            json={"peer_id": "peer-device", "remark": "客厅 的 设备"},
            headers={
                "Authorization": "Bearer secret-token",
                "Content-Type": "application/json",
            },
            timeout=10,
        )
        trace.assert_any_call('请求体: {"peer_id":"peer-device","remark":"客厅 的 设备"}')
        self.assertTrue(any("secret-token" in call.args[0] for call in trace.call_args_list))


class ContactHTTPTests(unittest.TestCase):
    def setUp(self):
        self.state = CallState.__new__(CallState)
        self.state._call_server = "https://call.example"
        self.state._mqtt_token = "secret-token"
        self.state._contact_list = []
        self.state._lock = threading.Lock()
        self.state._room_id = None
        self.state._before_start = lambda action: action()
        self.state._after_stop = lambda: None

    @staticmethod
    def response(data):
        response = mock.Mock(status_code=200)
        response.json.return_value = {"code": 200, "data": data}
        return response

    def test_empty_contact_list_clears_cached_contacts(self):
        self.state._contact_list = [{"type": "device", "device_id": "stale-peer"}]
        response = self.response({"contacts": []})

        with mock.patch("rtc_call_session.requests.get", return_value=response), \
                redirect_stdout(io.StringIO()) as output:
            contacts = self.state.do_list_contacts()

        self.assertEqual(contacts, [])
        self.assertEqual(self.state.get_cached_contacts(), [])
        self.assertIn("╔═ 联系人列表（共 0 条）", output.getvalue())
        self.assertIn("当前没有联系人", output.getvalue())

    def test_pending_list_displays_contact_type(self):
        response = self.response({"pending": [{
            "type": "device",
            "peer_device_id": "peer-device",
            "created_at": "2026-07-23T10:00:00+08:00",
        }]})

        with mock.patch("rtc_call_session.requests.get", return_value=response) as get:
            with redirect_stdout(io.StringIO()) as output:
                pending = self.state.do_list_pending_contacts()

        get.assert_called_once_with(
            "https://call.example/v1/call/device/contacts/pending",
            headers={
                "Authorization": "Bearer secret-token",
                "Content-Type": "application/json",
            },
            timeout=10,
        )
        self.assertEqual(pending[0]["type"], "device")
        self.assertIn("╔═ 待审批联系人（共 1 条）", output.getvalue())
        self.assertIn(
            "[0] type=device  peer_device_id=peer-device",
            output.getvalue(),
        )
        self.assertIn(
            "created_at=2026-07-23T10:00:00+08:00",
            output.getvalue(),
        )

    def test_contact_list_is_printed_in_highlighted_box(self):
        response = self.response({"contacts": [{
            "type": "device",
            "device_id": "peer-device",
            "online": True,
            "remark": "客厅",
            "source": "manual",
        }]})

        with mock.patch("rtc_call_session.requests.get", return_value=response), \
                redirect_stdout(io.StringIO()) as output:
            contacts = self.state.do_list_contacts()

        self.assertEqual(len(contacts), 1)
        rendered = output.getvalue()
        self.assertIn("\033[1;33m[contacts] ╔═ 联系人列表（共 1 条）", rendered)
        self.assertIn("[0] device_id=peer-device  type=device", rendered)
        self.assertIn("remark=客厅", rendered)
        self.assertIn("╚", rendered)

    def test_add_contact_sends_target_device_id(self):
        response = self.response({"status": "pending", "source": "manual"})

        with mock.patch("rtc_call_session.requests.post", return_value=response) as post:
            self.state.do_add_contact("peer-device")

        post.assert_called_once_with(
            "https://call.example/v1/call/device/contacts/request",
            json={"target_device_id": "peer-device"},
            headers={
                "Authorization": "Bearer secret-token",
                "Content-Type": "application/json",
            },
            timeout=10,
        )

    def test_call_prints_full_http_exchange(self):
        response = mock.Mock(status_code=200)
        response.json.return_value = {
            "code": 40201,
            "msg": "all targets offline",
        }

        with mock.patch("rtc_call_session.requests.post", return_value=response) as post:
            with mock.patch("http_trace._emit") as trace:
                self.state.do_call("TG3883MDUMN6", "video")

        post.assert_called_once_with(
            "https://call.example/v1/call/request",
            json={"targets": ["TG3883MDUMN6"], "call_type": "video"},
            headers={
                "Authorization": "Bearer secret-token",
                "Content-Type": "application/json",
            },
            timeout=10,
        )
        trace.assert_any_call("请求: POST https://call.example/v1/call/request")
        trace.assert_any_call(
            '请求头: {"Authorization":"Bearer secret-token","Content-Type":"application/json"}'
        )
        trace.assert_any_call(
            '请求体: {"targets":["TG3883MDUMN6"],"call_type":"video"}'
        )
        trace.assert_any_call(
            '响应: HTTP 200 body={"code":40201,"msg":"all targets offline"}'
        )
        self.assertTrue(any("secret-token" in call.args[0] for call in trace.call_args_list))

    def test_call_success_without_room_releases_runtime_owner(self):
        response = mock.Mock(status_code=200)
        response.json.return_value = {"code": 200, "data": {}}
        self.state._after_stop = mock.Mock()

        with mock.patch(
                "rtc_call_session.requests.post", return_value=response):
            self.state.do_call("peer-device", "video")

        self.state._after_stop.assert_called_once_with()
        self.assertIsNone(self.state._room_id)

    def test_respond_contact_sends_peer_and_action(self):
        for accept, action in ((True, "accept"), (False, "reject")):
            with self.subTest(action=action):
                response = self.response({"status": "accepted" if accept else "rejected"})
                with mock.patch("rtc_call_session.requests.post", return_value=response) as post:
                    self.state.do_respond_contact("peer-device", accept)

                post.assert_called_once_with(
                    "https://call.example/v1/call/device/contacts/respond",
                    json={"peer_device_id": "peer-device", "action": action},
                    headers={
                        "Authorization": "Bearer secret-token",
                        "Content-Type": "application/json",
                    },
                    timeout=10,
                )


class CallLifecycleRaceTests(unittest.TestCase):
    def make_state(self):
        state = CallState.__new__(CallState)
        state._call_server = "https://call.example"
        state._mqtt_token = "secret-token"
        state._lock = threading.Lock()
        state._room_id = "new-room"
        state._role = "caller"
        state._pending_call = None
        state._cancel_timer = mock.Mock()
        state._after_stop = mock.Mock()
        return state

    def test_stale_reject_does_not_cancel_new_room_timer(self):
        state = self.make_state()
        with mock.patch("rtc_call_session.rtc_call.clear_expected_room"):
            state.on_device_call_reject({
                "room_id": "old-room", "reason": "decline"})

        state._cancel_timer.cancel.assert_not_called()
        state._after_stop.assert_not_called()
        self.assertEqual(state._room_id, "new-room")

    def test_stale_room_cancel_does_not_cancel_new_room_timer(self):
        state = self.make_state()
        state.on_room_cancel({"room_id": "old-room", "reason": "timeout"})

        state._cancel_timer.cancel.assert_not_called()
        state._after_stop.assert_not_called()
        self.assertEqual(state._room_id, "new-room")

    def test_cancel_during_token_request_does_not_resurrect_or_connect(self):
        with mock.patch("rtc_call_session.rtc_call") as rtc_call:
            rtc_call._LOG_LEVEL = 40
            state = CallState(
                "https://call.example", "dev-1", "mqtt-token",
                before_accept_ticket=mock.Mock(),
            )
            state._after_stop = mock.Mock()
            state.on_device_call_incoming({
                "room_id": "room-1",
                "caller_id": "caller-1",
                "caller_name": "主叫",
                "call_type": "video",
            })
            response = mock.Mock()
            response.json.return_value = {
                "code": 200, "data": {"token": "token-1"}}

            def cancel_before_response(*_args, **_kwargs):
                state.on_room_cancel({
                    "room_id": "room-1", "reason": "remote_cancel"})
                return response

            with mock.patch(
                    "rtc_call_session.http_trace.request",
                    side_effect=cancel_before_response):
                state.do_accept()

            self.assertFalse(state.has_pending())
            self.assertIsNone(state._room_id)
            state._before_accept_ticket.assert_not_called()
            rtc_call.connect_to.assert_not_called()

    def test_accept_audio_call_passes_type_to_media_connection(self):
        with mock.patch("rtc_call_session.rtc_call") as rtc_call:
            rtc_call._LOG_LEVEL = 40
            state = CallState(
                "https://call.example", "dev-1", "mqtt-token",
                before_accept=lambda action: action(),
            )
            state.on_device_call_incoming({
                "room_id": "room-audio",
                "caller_id": "caller-1",
                "caller_name": "主叫",
                "call_type": "audio",
            })
            response = mock.Mock()
            response.json.return_value = {
                "code": 200, "data": {"token": "token-1"}}

            with mock.patch(
                    "rtc_call_session.http_trace.request",
                    return_value=response):
                state.do_accept()

            rtc_call.connect_to.assert_called_once_with(
                "caller-1",
                "token-1",
                "room-audio",
                call_type="audio",
            )
            self.assertEqual(state._call_type, "audio")


if __name__ == "__main__":
    unittest.main()
