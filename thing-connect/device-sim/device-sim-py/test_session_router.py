import unittest
from unittest import mock
import io
from contextlib import redirect_stdout

from session_coordinator import SessionKind
from session_arbiter import IncomingDecision
from session_router import SessionMessageRouter, TerminalController


class SessionMessageRouterTests(unittest.TestCase):
    def _router(self, arbiter, voip, call):
        router = SessionMessageRouter(arbiter, voip, call)
        self.addCleanup(router.shutdown)
        return router

    def test_current_voip_ringback_is_routed_to_voip_state(self):
        arbiter = mock.Mock(current=SessionKind.VOIP)
        arbiter.admit_incoming.return_value = IncomingDecision.CURRENT
        voip, call = mock.Mock(), mock.Mock()
        voip.is_active.return_value = True
        router = self._router(arbiter, voip, call)
        payload = {"wx_room_id": "own-outgoing-room"}

        router.on_call_incoming(payload)

        voip.on_call_incoming.assert_called_once_with(payload)
        voip.reject_incoming.assert_not_called()

    def test_device_call_is_rejected_while_voip_active(self):
        arbiter = mock.Mock(current=SessionKind.VOIP)
        arbiter.offer_pending.return_value = False
        voip, call = mock.Mock(), mock.Mock()
        voip.has_pending.return_value = False
        voip.is_outgoing.return_value = False
        router = self._router(arbiter, voip, call)
        payload = {"room_id": "room-1"}
        router.on_device_call_incoming(payload)
        router.wait_for_idle()
        router.shutdown()
        call.on_device_call_incoming.assert_not_called()
        call.reject_incoming.assert_called_once_with(payload, "busy")

    def test_voip_call_is_rejected_while_ai_active(self):
        arbiter = mock.Mock(current=SessionKind.AI)
        arbiter.admit_incoming.return_value = IncomingDecision.BUSY
        voip, call = mock.Mock(), mock.Mock()
        call.has_pending.return_value = False
        call.is_outgoing.return_value = False
        router = self._router(arbiter, voip, call)
        payload = {"wx_room_id": "room-1"}
        router.on_call_incoming(payload)
        router.wait_for_idle()
        router.shutdown()
        voip.on_call_incoming.assert_not_called()
        voip.reject_incoming.assert_called_once_with(payload, 5)

    def test_voip_call_is_rejected_while_device_call_is_waiting(self):
        arbiter = mock.Mock(current=None)
        arbiter.admit_incoming.return_value = IncomingDecision.BUSY
        voip, call = mock.Mock(), mock.Mock()
        call.has_pending.return_value = False
        call.is_outgoing.return_value = True
        router = self._router(arbiter, voip, call)

        router.on_call_incoming({"wx_room_id": "wx-room"})
        router.wait_for_idle()
        router.shutdown()

        voip.on_call_incoming.assert_not_called()
        voip.reject_incoming.assert_called_once_with({"wx_room_id": "wx-room"}, 5)

    def test_fresh_voip_grant_replaces_expired_local_pending(self):
        arbiter = mock.Mock(current=None)
        arbiter.admit_incoming.return_value = IncomingDecision.PENDING
        voip, call = mock.Mock(), mock.Mock()
        voip.has_pending.return_value = True
        router = self._router(arbiter, voip, call)
        payload = {
            "wx_room_id": "new-room",
            "peer_id": "peer-1",
            "token": "token-1",
        }

        router.on_call_incoming(payload)

        voip.on_call_incoming.assert_called_once_with(
            payload, replace_pending=True)
        router.shutdown()

    def test_device_call_is_rejected_while_voip_is_waiting(self):
        arbiter = mock.Mock(current=None)
        arbiter.offer_pending.return_value = False
        voip, call = mock.Mock(), mock.Mock()
        voip.has_pending.return_value = False
        voip.is_outgoing.return_value = True
        router = self._router(arbiter, voip, call)

        router.on_device_call_incoming({"room_id": "device-room"})
        router.wait_for_idle()
        router.shutdown()

        call.on_device_call_incoming.assert_not_called()
        call.reject_incoming.assert_called_once_with(
            {"room_id": "device-room"}, "busy")

    def test_contact_update_keeps_legacy_and_payload_callbacks(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, call = mock.Mock(), mock.Mock()
        router = self._router(coordinator, voip, call)
        payload = {"action": "request", "contact_type": "device", "peer_id": "dev-peer"}

        router.on_device_callers_update()
        call.on_device_callers_update.assert_called_once_with()

        router.on_device_callers_update_payload(payload)
        call.on_device_callers_update.assert_called_with(payload)
        router.shutdown()

    def test_busy_reject_does_not_block_mqtt_callback(self):
        import threading

        arbiter = mock.Mock(current=SessionKind.VOIP)
        arbiter.offer_pending.return_value = False
        voip, call = mock.Mock(), mock.Mock()
        started = threading.Event()
        release = threading.Event()

        def slow_reject(_payload, _reason):
            started.set()
            release.wait(timeout=1)

        call.reject_incoming.side_effect = slow_reject
        router = self._router(arbiter, voip, call)
        router.on_device_call_incoming({
            "room_id": "room-async", "caller_id": "caller-1"})

        self.assertTrue(started.wait(timeout=1))
        self.assertFalse(release.is_set())
        release.set()
        router.wait_for_idle()
        router.shutdown()

    def test_voip_busy_reject_does_not_block_mqtt_callback(self):
        import threading

        arbiter = mock.Mock(current=SessionKind.AI)
        arbiter.admit_incoming.return_value = IncomingDecision.BUSY
        voip, call = mock.Mock(), mock.Mock()
        started = threading.Event()
        release = threading.Event()

        def slow_reject(_payload, _reason):
            started.set()
            release.wait(timeout=1)

        voip.reject_incoming.side_effect = slow_reject
        router = self._router(arbiter, voip, call)
        router.on_call_incoming({"wx_room_id": "wx-async"})

        self.assertTrue(started.wait(timeout=1))
        self.assertFalse(release.is_set())
        release.set()
        router.wait_for_idle()
        router.shutdown()

    def test_stale_cancel_is_forwarded_with_exact_room_ticket(self):
        arbiter = mock.Mock(current=None)
        voip, call = mock.Mock(), mock.Mock()
        call.has_pending.return_value = False
        router = self._router(arbiter, voip, call)

        router.on_room_cancel({"room_id": "stale-room"})

        arbiter.clear_pending.assert_called_once_with(
            SessionKind.CALL, "stale-room")
        router.shutdown()


class TerminalControllerTests(unittest.TestCase):
    def test_contact_add_sends_contact_request(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("ct add dev-peer", mock.Mock())
        call.do_add_contact.assert_called_once_with("dev-peer")

    def test_contact_accept_accepts_contact_request(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("ct accept dev-peer", mock.Mock())
        call.do_respond_contact.assert_called_once_with("dev-peer", True)

    def test_contact_reject_rejects_contact_request(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("ct reject dev-peer", mock.Mock())
        call.do_respond_contact.assert_called_once_with("dev-peer", False)

    def test_contact_del_deletes_manual_contact(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("ct del dev-peer", mock.Mock())
        call.do_delete_contact.assert_called_once_with("dev-peer")

    def test_contact_remark_preserves_spaces(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("ct remark dev-peer 客厅 的 设备", mock.Mock())
        call.do_remark.assert_called_once_with("dev-peer", "客厅 的 设备")

    def test_contact_remark_can_be_cleared(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("ct remark dev-peer", mock.Mock())
        call.do_remark.assert_called_once_with("dev-peer", "")

    def test_contact_pending_lists_contact_requests(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("ct pending", mock.Mock())
        call.do_list_pending_contacts.assert_called_once_with()

    def test_old_contact_commands_are_not_supported(self):
        commands = (
            "contacts",
            "pending",
            "addcontact dev-peer",
            "acceptcontact dev-peer",
            "rejectcontact dev-peer",
            "respond dev-peer accept",
            "delcontact dev-peer",
            "remark dev-peer old-name",
        )
        for command in commands:
            with self.subTest(command=command):
                coordinator = mock.Mock(current=SessionKind.STREAM)
                voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
                terminal = TerminalController(coordinator, voip, ai, call)
                with redirect_stdout(io.StringIO()) as output:
                    terminal.execute(command, mock.Mock())
                self.assertIn("未知命令", output.getvalue())
                self.assertEqual(call.method_calls, [])

    def test_wxcall_then_plain_index_dials(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.list_callers.return_value = [{"wx_open_id": "openid-1"}]
        call.get_cached_contacts.return_value = []
        call.has_pending.return_value = False
        call.is_outgoing.return_value = False
        terminal = TerminalController(coordinator, voip, ai, call)
        with redirect_stdout(io.StringIO()):
            terminal.execute("wxcall", mock.Mock())
        terminal.execute("0", mock.Mock())
        voip.do_call.assert_called_once_with({"wx_open_id": "openid-1"}, "video")

    def test_audio_only_default_dials_audio(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.list_callers.return_value = [{"wx_open_id": "openid-1"}]
        call.get_cached_contacts.return_value = []
        terminal = TerminalController(
            coordinator, voip, ai, call, video_capable=False)

        terminal.execute("wxcall 0", mock.Mock())

        voip.do_call.assert_called_once_with(
            {"wx_open_id": "openid-1"}, "audio")

    def test_audio_only_rejects_explicit_video(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        terminal = TerminalController(
            coordinator, voip, ai, call, video_capable=False)

        with redirect_stdout(io.StringIO()) as output:
            terminal.execute("call peer-device video", mock.Mock())

        self.assertIn("未配置上行视频文件", output.getvalue())
        voip.do_call.assert_not_called()
        call.do_call.assert_not_called()

    def test_invalid_call_type_is_not_silently_treated_as_video(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        terminal = TerminalController(coordinator, voip, ai, call)

        with redirect_stdout(io.StringIO()) as output:
            terminal.execute("call peer-device voice", mock.Mock())

        self.assertIn("仅支持 video 或 audio", output.getvalue())
        voip.do_call.assert_not_called()
        call.do_call.assert_not_called()

    def test_wxcall_selection_is_cleared_by_other_command(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.list_callers.return_value = [{"wx_open_id": "openid-1"}]
        call.get_cached_contacts.return_value = []
        terminal = TerminalController(coordinator, voip, ai, call)
        with redirect_stdout(io.StringIO()):
            terminal.execute("wxcall", mock.Mock())
        terminal.execute("help", mock.Mock())
        self.assertIsNone(terminal._pending_selection)

    def test_wxcall_selection_supports_audio(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.list_callers.return_value = [{"wx_open_id": "openid-1"}]
        call.get_cached_contacts.return_value = []
        call.has_pending.return_value = False
        call.is_outgoing.return_value = False
        terminal = TerminalController(coordinator, voip, ai, call)
        with redirect_stdout(io.StringIO()):
            terminal.execute("wxcall", mock.Mock())
        terminal.execute("0 audio", mock.Mock())
        voip.do_call.assert_called_once_with({"wx_open_id": "openid-1"}, "audio")

    def test_call_selection_routes_device_contact(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        call.do_list_contacts.return_value = [{"device_id": "dev-1", "type": "device"}]
        call.get_cached_contacts.return_value = [{"device_id": "dev-1", "type": "device"}]
        voip.has_pending.return_value = False
        voip.is_outgoing.return_value = False
        terminal = TerminalController(coordinator, voip, ai, call)
        with redirect_stdout(io.StringIO()):
            terminal.execute("call", mock.Mock())
        terminal.execute("0 audio", mock.Mock())
        call.do_call.assert_called_once_with("dev-1", "audio")
        voip.do_call.assert_not_called()

    def test_call_selection_routes_voip_contact(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        call.do_list_contacts.return_value = [{
            "type": "voip", "wx_open_id": "openid-1", "wx_app_id": "app-1", "wx_model_id": "model-1"
        }]
        call.get_cached_contacts.return_value = [{
            "type": "voip", "wx_open_id": "openid-1", "wx_app_id": "app-1", "wx_model_id": "model-1"
        }]
        call.has_pending.return_value = False
        call.is_outgoing.return_value = False
        terminal = TerminalController(coordinator, voip, ai, call)
        with redirect_stdout(io.StringIO()):
            terminal.execute("call", mock.Mock())
        terminal.execute("0", mock.Mock())
        voip.do_call.assert_called_once_with({
            "wx_open_id": "openid-1", "wx_app_id": "app-1", "wx_model_id": "model-1"
        }, "video")

    def test_call_openid_routes_to_wxcall(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        call.get_cached_contacts.return_value = []
        voip.list_callers.return_value = [{"wx_open_id": "openid-1", "wx_app_id": "app-1", "wx_model_id": "model-1"}]
        call.has_pending.return_value = False
        call.is_outgoing.return_value = False
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("call openid-1 audio", mock.Mock())
        voip.do_call.assert_called_once_with(
            {"wx_open_id": "openid-1", "wx_app_id": "app-1", "wx_model_id": "model-1"},
            "audio",
        )
        call.do_call.assert_not_called()

    def test_wxcall_delegates_conflict_check_to_session_state(self):
        coordinator = mock.Mock(current=None)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.list_callers.return_value = [{"wx_open_id": "openid-1"}]
        call.has_pending.return_value = False
        call.is_outgoing.return_value = True
        terminal = TerminalController(coordinator, voip, ai, call)

        with redirect_stdout(io.StringIO()):
            terminal.execute("wxcall 0", mock.Mock())

        voip.do_call.assert_called_once_with(
            {"wx_open_id": "openid-1"}, "video")

    def test_accept_routes_to_voip_pending(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        coordinator.has_pending.side_effect = (
            lambda kind: kind == SessionKind.VOIP)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.has_pending.return_value = True
        call.has_pending.return_value = False
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("accept", mock.Mock())
        voip.accept.assert_called_once_with()
        call.do_accept.assert_not_called()

    def test_accept_alias_routes_to_voip_pending(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        coordinator.has_pending.side_effect = (
            lambda kind: kind == SessionKind.VOIP)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.has_pending.return_value = True
        call.has_pending.return_value = False
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("a", mock.Mock())
        voip.accept.assert_called_once_with()
        call.do_accept.assert_not_called()

    def test_accept_routes_to_call_pending(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        coordinator.has_pending.side_effect = (
            lambda kind: kind == SessionKind.CALL)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.has_pending.return_value = False
        call.has_pending.return_value = True
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("accept", mock.Mock())
        call.do_accept.assert_called_once_with()
        voip.accept.assert_not_called()

    def test_cancel_routes_to_voip(self):
        coordinator = mock.Mock(current=SessionKind.VOIP)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.is_outgoing.return_value = False
        call.is_outgoing.return_value = False
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("cancel", mock.Mock())
        voip.cancel.assert_called_once_with()
        call.do_cancel.assert_not_called()

    def test_cancel_without_outgoing_prints_message(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.is_outgoing.return_value = False
        call.is_outgoing.return_value = False
        terminal = TerminalController(coordinator, voip, ai, call)

        with redirect_stdout(io.StringIO()) as output:
            terminal.execute("cancel", mock.Mock())

        self.assertIn("当前没有可取消的外呼", output.getvalue())
        voip.cancel.assert_not_called()
        call.do_cancel.assert_not_called()

    def test_wxcall_list_is_printed_in_highlighted_box(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.list_callers.return_value = [{
            "remark": "家人",
            "wx_open_id": "openid-full-value",
        }]
        terminal = TerminalController(coordinator, voip, ai, call)

        with redirect_stdout(io.StringIO()) as output:
            terminal.execute("wxcall", mock.Mock())

        rendered = output.getvalue()
        self.assertIn("\033[1;33m[contacts] ╔═ 微信联系人列表（共 1 条）", rendered)
        self.assertIn("[0] remark=家人", rendered)
        self.assertIn("wx_open_id=openid-full-value", rendered)
        self.assertIn("╚", rendered)

    def test_hangup_targets_only_active_session(self):
        coordinator = mock.Mock(current=SessionKind.AI)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("hangup", mock.Mock())
        ai.hangup.assert_called_once_with()
        voip.hangup.assert_not_called()
        call.do_hangup.assert_not_called()

    def test_hangup_targets_recovered_call_room_while_stream_is_active(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        call.has_room.return_value = True
        terminal = TerminalController(coordinator, voip, ai, call)

        terminal.execute("hangup", mock.Mock())

        call.do_hangup.assert_called_once_with()
        voip.hangup.assert_not_called()
        ai.hangup.assert_not_called()

    def test_reject_alias_routes_to_voip_pending(self):
        coordinator = mock.Mock(current=SessionKind.STREAM)
        coordinator.has_pending.side_effect = (
            lambda kind: kind == SessionKind.VOIP)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        voip.has_pending.return_value = True
        call.has_pending.return_value = False
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("r", mock.Mock())
        voip.reject.assert_called_once_with()
        call.do_reject.assert_not_called()

    def test_hangup_alias_targets_only_active_session(self):
        coordinator = mock.Mock(current=SessionKind.AI)
        voip, ai, call = mock.Mock(), mock.Mock(), mock.Mock()
        terminal = TerminalController(coordinator, voip, ai, call)
        terminal.execute("h", mock.Mock())
        ai.hangup.assert_called_once_with()
        voip.hangup.assert_not_called()
        call.do_hangup.assert_not_called()


if __name__ == "__main__":
    unittest.main()
