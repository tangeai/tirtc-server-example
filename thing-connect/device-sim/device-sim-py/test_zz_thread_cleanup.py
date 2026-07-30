#!/usr/bin/env python3

import threading
import time
import unittest


class ProcessThreadCleanupTests(unittest.TestCase):
    def test_no_background_thread_survives_the_test_suite(self):
        # This module sorts after the functional tests. Give workers one short
        # scheduling window, then make every missing shutdown deterministic.
        time.sleep(0.05)
        alive = [
            (thread.name, thread.daemon)
            for thread in threading.enumerate()
            if thread is not threading.main_thread() and thread.is_alive()
        ]
        self.assertEqual(alive, [])


if __name__ == "__main__":
    unittest.main()
