import unittest

from session_coordinator import SessionAdapter, SessionCoordinator, SessionKind


class SessionCoordinatorTests(unittest.TestCase):
    def setUp(self):
        self.events = []
        self.adapters = {
            kind: SessionAdapter(
                lambda k=kind: self.events.append(("start", k.value)),
                lambda k=kind: self.events.append(("stop", k.value)),
            )
            for kind in SessionKind
        }

    def test_call_temporarily_replaces_stream(self):
        coordinator = SessionCoordinator(self.adapters)
        coordinator.start_stream()
        coordinator.begin(SessionKind.VOIP, lambda: self.events.append(("action", "voip")))
        coordinator.finish(SessionKind.VOIP)
        self.assertEqual(self.events, [
            ("start", "stream"), ("stop", "stream"), ("start", "voip"),
            ("action", "voip"), ("stop", "voip"), ("start", "stream"),
        ])

    def test_failed_action_restores_stream(self):
        coordinator = SessionCoordinator(self.adapters)
        coordinator.start_stream()
        with self.assertRaisesRegex(RuntimeError, "boom"):
            coordinator.begin(SessionKind.AI,
                              lambda: (_ for _ in ()).throw(RuntimeError("boom")))
        self.assertEqual(coordinator.current, SessionKind.STREAM)

    def test_sdk_system_exit_also_restores_stream(self):
        adapters = dict(self.adapters)
        adapters[SessionKind.AI] = SessionAdapter(
            lambda: (_ for _ in ()).throw(SystemExit(1)),
            lambda: self.events.append(("stop", "ai")),
        )
        coordinator = SessionCoordinator(adapters)
        coordinator.start_stream()
        with self.assertRaises(SystemExit):
            coordinator.begin(SessionKind.AI, lambda: None)
        self.assertEqual(coordinator.current, SessionKind.STREAM)

    def test_stale_finish_does_not_stop_new_session(self):
        coordinator = SessionCoordinator(self.adapters)
        coordinator.start_stream()
        coordinator.begin(SessionKind.CALL, lambda: None)
        coordinator.finish(SessionKind.VOIP)
        self.assertEqual(coordinator.current, SessionKind.CALL)

    def test_two_calls_cannot_be_active_together(self):
        coordinator = SessionCoordinator(self.adapters)
        coordinator.start_stream()
        coordinator.begin(SessionKind.VOIP, lambda: None)
        with self.assertRaisesRegex(RuntimeError, "voip 会话正在进行中"):
            coordinator.begin(SessionKind.AI, lambda: None)
        self.assertEqual(coordinator.current, SessionKind.VOIP)


if __name__ == "__main__":
    unittest.main()
