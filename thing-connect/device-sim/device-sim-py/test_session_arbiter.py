import threading
import time
import unittest
from unittest import mock

from session_arbiter import IncomingDecision, SessionArbiter, SessionConflict
from session_coordinator import SessionAdapter, SessionCoordinator, SessionKind


class SessionArbiterTests(unittest.TestCase):
    def setUp(self):
        self.events = []
        adapters = {
            kind: SessionAdapter(
                lambda k=kind: self.events.append(("start", k.value)),
                lambda k=kind: self.events.append(("stop", k.value)),
            )
            for kind in SessionKind
        }
        self.coordinator = SessionCoordinator(adapters)
        self.arbiter = SessionArbiter(self.coordinator)
        self.coordinator.start_stream()

    def tearDown(self):
        self.arbiter.shutdown()

    def test_pending_first_wins_without_stopping_stream(self):
        barrier = threading.Barrier(12)
        results = []
        result_lock = threading.Lock()

        def offer(kind):
            barrier.wait()
            granted = self.arbiter.offer_pending(kind)
            with result_lock:
                results.append((kind, granted))

        threads = [
            threading.Thread(
                target=offer,
                args=(SessionKind.VOIP if index % 2 else SessionKind.CALL,),
            )
            for index in range(12)
        ]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()

        winners = [kind for kind, granted in results if granted]
        self.assertEqual(len(winners), 1)
        self.assertEqual(self.coordinator.current, SessionKind.STREAM)
        self.assertEqual(self.arbiter.pending, winners[0])

    def test_pending_blocks_other_business_and_accept_consumes_it(self):
        self.assertTrue(self.arbiter.offer_pending(SessionKind.VOIP))
        with self.assertRaises(SessionConflict):
            self.arbiter.begin(SessionKind.AI, lambda: None)

        self.arbiter.begin(
            SessionKind.VOIP,
            lambda: self.events.append(("action", "accept")),
            consume_pending=True,
        )
        self.assertIsNone(self.arbiter.pending)
        self.assertEqual(self.arbiter.current, SessionKind.VOIP)
        self.assertEqual(self.coordinator.current, SessionKind.VOIP)

    def test_incoming_classification_is_atomic(self):
        self.assertEqual(
            self.arbiter.admit_incoming(SessionKind.VOIP),
            IncomingDecision.PENDING,
        )
        self.assertEqual(
            self.arbiter.admit_incoming(SessionKind.VOIP),
            IncomingDecision.BUSY,
        )
        self.arbiter.begin(
            SessionKind.VOIP, lambda: None, consume_pending=True)
        self.assertEqual(
            self.arbiter.admit_incoming(SessionKind.VOIP),
            IncomingDecision.CURRENT,
        )

    def test_duplicate_begin_rejected_but_internal_continue_allowed(self):
        self.arbiter.begin(SessionKind.CALL, lambda: None)
        with self.assertRaises(SessionConflict):
            self.arbiter.begin(SessionKind.CALL, lambda: None)
        self.arbiter.continue_session(
            SessionKind.CALL,
            lambda: self.events.append(("action", "connected")),
        )
        self.assertIn(("action", "connected"), self.events)

    def test_failed_accept_restores_pending_slot(self):
        self.assertTrue(self.arbiter.offer_pending(SessionKind.CALL))
        with self.assertRaisesRegex(RuntimeError, "connect failed"):
            self.arbiter.begin(
                SessionKind.CALL,
                lambda: (_ for _ in ()).throw(RuntimeError("connect failed")),
                consume_pending=True,
            )
        self.assertTrue(self.arbiter.has_pending(SessionKind.CALL))
        self.assertIsNone(self.arbiter.current)
        self.assertEqual(self.coordinator.current, SessionKind.STREAM)

    def test_cancel_during_failed_accept_prevents_pending_resurrection(self):
        self.assertTrue(self.arbiter.offer_pending(SessionKind.CALL))

        def cancelled_failure():
            self.arbiter.clear_pending(SessionKind.CALL)
            raise RuntimeError("cancelled while connecting")

        with self.assertRaisesRegex(RuntimeError, "cancelled"):
            self.arbiter.begin(
                SessionKind.CALL, cancelled_failure, consume_pending=True)

        self.assertFalse(self.arbiter.has_pending(SessionKind.CALL))
        self.assertIsNone(self.arbiter.current)

    def test_old_generation_cannot_finish_new_same_kind_session(self):
        first = self.arbiter.begin(SessionKind.VOIP, lambda: None)
        self.arbiter.finish(SessionKind.VOIP, first.generation)
        second = self.arbiter.begin(SessionKind.VOIP, lambda: None)

        self.arbiter.finish(SessionKind.VOIP, first.generation)

        self.assertNotEqual(first.generation, second.generation)
        self.assertEqual(self.arbiter.current, SessionKind.VOIP)
        self.assertEqual(self.coordinator.current, SessionKind.VOIP)

    def test_new_lease_is_published_before_synchronous_terminal_event(self):
        leases = {}

        def publish(lease):
            leases[lease.kind] = lease

        first = self.arbiter.begin(
            SessionKind.CALL, lambda: None, lease_ready=publish)
        self.arbiter.finish(SessionKind.CALL, first.generation)

        def fail_synchronously():
            current = leases[SessionKind.CALL]
            self.arbiter.finish_async(
                SessionKind.CALL, current.generation)

        second = self.arbiter.begin(
            SessionKind.CALL, fail_synchronously, lease_ready=publish)
        self.assertNotEqual(first.generation, second.generation)
        self.assertEqual(
            leases[SessionKind.CALL].generation, second.generation)

        with self.arbiter._finish_idle:
            while self.arbiter._finish_queue or self.arbiter._finish_active:
                self.arbiter._finish_idle.wait(timeout=1)
        self.assertIsNone(self.arbiter.current)
        self.assertEqual(self.coordinator.current, SessionKind.STREAM)

    def test_stale_room_cancel_cannot_clear_new_pending_ticket(self):
        self.assertTrue(self.arbiter.offer_pending(
            SessionKind.CALL, "room-new"))

        self.arbiter.clear_pending(SessionKind.CALL, "room-old")

        self.assertTrue(self.arbiter.has_pending(
            SessionKind.CALL, "room-new"))

    def test_pending_ticket_expires(self):
        self.assertTrue(self.arbiter.offer_pending(
            SessionKind.CALL, "room-expiring", ttl=0.01))
        time.sleep(0.02)
        self.assertFalse(self.arbiter.has_pending(
            SessionKind.CALL, "room-expiring"))

    def test_cancel_during_starting_prevents_action_commit(self):
        self.assertTrue(self.arbiter.offer_pending(
            SessionKind.CALL, "room-cancel"))

        def cancel_while_starting():
            self.arbiter.clear_pending(
                SessionKind.CALL, "room-cancel")

        with self.assertRaisesRegex(SessionConflict, "启动期间已取消"):
            self.arbiter.begin(
                SessionKind.CALL,
                cancel_while_starting,
                consume_pending=True,
                session_id="room-cancel",
            )

        self.assertIsNone(self.arbiter.current)
        self.assertEqual(self.coordinator.current, SessionKind.STREAM)

    def test_stream_restore_is_retried_without_killing_worker(self):
        coordinator = mock.Mock()
        coordinator.finish.side_effect = RuntimeError("first restore failed")
        coordinator.start_stream.side_effect = [
            RuntimeError("retry failed"),
            None,
        ]
        arbiter = SessionArbiter(coordinator)
        try:
            arbiter.begin(SessionKind.AI, lambda: None)
            arbiter.finish_async(SessionKind.AI)
            with arbiter._finish_idle:
                while arbiter._finish_queue or arbiter._finish_active:
                    arbiter._finish_idle.wait(timeout=1)
            self.assertEqual(coordinator.start_stream.call_count, 2)
            self.assertTrue(arbiter._worker.is_alive())
        finally:
            arbiter.shutdown()


if __name__ == "__main__":
    unittest.main()
