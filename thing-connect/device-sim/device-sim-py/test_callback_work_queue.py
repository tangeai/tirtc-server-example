#!/usr/bin/env python3

import threading
import unittest

from callback_work_queue import CallbackWorkQueue


class CallbackWorkQueueTests(unittest.TestCase):
    def test_queue_is_bounded_and_stop_drains_accepted_work(self):
        entered = threading.Event()
        release = threading.Event()
        handled = []
        warnings = []

        def handle(item):
            handled.append(item)
            if item == "first":
                entered.set()
                self.assertTrue(release.wait(timeout=2.0))

        work = CallbackWorkQueue(
            "test-callback-work",
            handle,
            warnings.append,
            maxsize=1,
        )
        work.start()
        self.assertTrue(work.submit("first"))
        self.assertTrue(entered.wait(timeout=2.0))
        self.assertTrue(work.submit("second"))
        self.assertFalse(work.submit("overflow"))

        release.set()
        work.stop()

        self.assertEqual(["first", "second"], handled)
        self.assertEqual(1, len(warnings))
        self.assertFalse(work.submit("after-stop"))

    def test_handler_error_does_not_block_later_work_or_shutdown(self):
        handled = []
        warnings = []

        def handle(item):
            handled.append(item)
            if item == "bad":
                raise RuntimeError("expected")

        work = CallbackWorkQueue(
            "test-callback-error",
            handle,
            warnings.append,
        )
        work.start()
        self.assertTrue(work.submit("bad"))
        self.assertTrue(work.submit("good"))
        work.stop()

        self.assertEqual(["bad", "good"], handled)
        self.assertEqual(1, len(warnings))


if __name__ == "__main__":
    unittest.main()
