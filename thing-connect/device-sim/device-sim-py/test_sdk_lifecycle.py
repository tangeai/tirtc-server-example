#!/usr/bin/env python3

import threading
import time
import unittest
from unittest import mock

import rtc_ai
import rtc_call
import rtc_stream
from sdk_callback_guard import SdkCallbackGuard


class SdkLifecycleTests(unittest.TestCase):
    def _start_blocked_callback(self, guard):
        entered = threading.Event()
        release = threading.Event()

        def callback():
            entered.set()
            release.wait(timeout=2.0)

        thread = threading.Thread(target=guard.wrap(callback))
        thread.start()
        self.assertTrue(entered.wait(timeout=1.0))
        return release, thread

    def test_callback_guard_waits_for_callback_return(self):
        guard = SdkCallbackGuard()
        release, callback_thread = self._start_blocked_callback(guard)
        wait_finished = threading.Event()

        waiter = threading.Thread(
            target=lambda: (guard.wait_for_idle(), wait_finished.set())
        )
        waiter.start()
        time.sleep(0.05)
        self.assertFalse(wait_finished.is_set())

        release.set()
        callback_thread.join(timeout=1.0)
        waiter.join(timeout=1.0)
        self.assertTrue(wait_finished.is_set())
        self.assertEqual(guard.active_count, 0)

    def test_ai_uninit_waits_for_callback_return(self):
        old_running = rtc_ai._sdk_running
        stopped_was_set = rtc_ai._sdk_stopped.is_set()
        release, callback_thread = self._start_blocked_callback(
            rtc_ai._callback_guard
        )
        rtc_ai._sdk_running = True
        rtc_ai._sdk_stopped.set()
        try:
            with mock.patch.object(rtc_ai, "stop_session"), \
                    mock.patch.object(rtc_ai.sdk, "TiRtcStop") as sdk_stop, \
                    mock.patch.object(rtc_ai.sdk, "TiRtcUninit") as sdk_uninit:
                uninit_thread = threading.Thread(target=rtc_ai.uninit_sdk)
                uninit_thread.start()
                time.sleep(0.05)
                sdk_stop.assert_not_called()
                sdk_uninit.assert_not_called()

                release.set()
                callback_thread.join(timeout=1.0)
                uninit_thread.join(timeout=1.0)
        finally:
            rtc_ai._sdk_running = old_running
            if stopped_was_set:
                rtc_ai._sdk_stopped.set()
            else:
                rtc_ai._sdk_stopped.clear()

        self.assertFalse(uninit_thread.is_alive())
        sdk_stop.assert_called_once_with()
        sdk_uninit.assert_called_once_with()

    def test_stream_stop_waits_for_callback_return(self):
        old_running = rtc_stream._sdk_running
        old_conn = rtc_stream._active_conn
        old_thread = rtc_stream._active_thread
        stopped_was_set = rtc_stream._sdk_stopped.is_set()
        release, callback_thread = self._start_blocked_callback(
            rtc_stream._callback_guard
        )
        rtc_stream._sdk_running = True
        rtc_stream._active_conn = None
        rtc_stream._active_thread = None
        rtc_stream._sdk_stopped.set()
        try:
            with mock.patch.object(rtc_stream, "_close_talkback_file"), \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcStop", return_value=0
                    ) as sdk_stop, \
                    mock.patch.object(rtc_stream.sdk, "TiRtcUninit") as sdk_uninit:
                stop_thread = threading.Thread(target=rtc_stream.stop)
                stop_thread.start()
                time.sleep(0.05)
                sdk_stop.assert_not_called()
                sdk_uninit.assert_not_called()

                release.set()
                callback_thread.join(timeout=1.0)
                stop_thread.join(timeout=1.0)
        finally:
            rtc_stream._sdk_running = old_running
            rtc_stream._active_conn = old_conn
            rtc_stream._active_thread = old_thread
            if stopped_was_set:
                rtc_stream._sdk_stopped.set()
            else:
                rtc_stream._sdk_stopped.clear()

        self.assertFalse(stop_thread.is_alive())
        sdk_stop.assert_called_once_with()
        sdk_uninit.assert_called_once_with()

    def test_device_call_uninit_waits_for_callback_return(self):
        old_running = rtc_call._sdk_running
        stopped_was_set = rtc_call._sdk_stopped.is_set()
        release, callback_thread = self._start_blocked_callback(
            rtc_call._callback_guard
        )
        rtc_call._sdk_running = True
        rtc_call._sdk_stopped.set()
        try:
            with mock.patch.object(rtc_call, "hangup"), \
                    mock.patch.object(rtc_call._media, "shutdown"), \
                    mock.patch.object(rtc_call.sdk, "TiRtcStop") as sdk_stop, \
                    mock.patch.object(rtc_call.sdk, "TiRtcUninit") as sdk_uninit:
                uninit_thread = threading.Thread(target=rtc_call.uninit_sdk)
                uninit_thread.start()
                time.sleep(0.05)
                sdk_stop.assert_not_called()
                sdk_uninit.assert_not_called()

                release.set()
                callback_thread.join(timeout=1.0)
                uninit_thread.join(timeout=1.0)
        finally:
            rtc_call._sdk_running = old_running
            if stopped_was_set:
                rtc_call._sdk_stopped.set()
            else:
                rtc_call._sdk_stopped.clear()

        self.assertFalse(uninit_thread.is_alive())
        sdk_stop.assert_called_once_with()
        sdk_uninit.assert_called_once_with()

    def test_stale_device_call_disconnect_does_not_stop_current_media(self):
        old_state = rtc_call._session_state
        old_hconn = rtc_call._active_hconn
        rtc_call._session_state = "IN_CALL"
        rtc_call._active_hconn = 0x2222
        try:
            with mock.patch.object(rtc_call._media, "stop") as media_stop:
                rtc_call._handle_disconnect(0x1111)
                media_stop.assert_not_called()
        finally:
            rtc_call._session_state = old_state
            rtc_call._active_hconn = old_hconn


if __name__ == "__main__":
    unittest.main()
