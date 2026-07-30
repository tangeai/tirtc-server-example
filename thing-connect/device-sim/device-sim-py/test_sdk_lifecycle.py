#!/usr/bin/env python3

import threading
import time
import unittest
from unittest import mock

import rtc_ai
import rtc_call
import rtc_stream
from sdk_callback_guard import SdkCallbackGuard
from tirtc_runtime import TiRtcRuntime
from tirtc_runtime import ServiceKind
from tirtc_sdk import TIRTCCALLBACKS


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
        guard.close()

    def test_callback_guard_control_queue_waits_and_preserves_order(self):
        guard = SdkCallbackGuard()
        order = []
        entered = threading.Event()
        release = threading.Event()

        def callback():
            guard.defer(lambda: order.append(1), name="first")
            guard.defer(lambda: order.append(2), name="second")
            entered.set()
            release.wait(timeout=2.0)

        callback_thread = threading.Thread(target=guard.wrap(callback))
        callback_thread.start()
        self.assertTrue(entered.wait(timeout=1.0))
        time.sleep(0.02)
        self.assertEqual([], order)

        release.set()
        callback_thread.join(timeout=1.0)
        guard.wait_for_all()
        self.assertEqual([1, 2], order)
        guard.close()

    def test_runtime_stop_waits_for_callback_return(self):
        runtime = TiRtcRuntime()
        release, callback_thread = self._start_blocked_callback(
            runtime._callback_guard
        )
        runtime._initialized = True
        runtime._start_submitted = True
        runtime._started = True
        runtime._stopped_event.set()
        with mock.patch.object(
                rtc_ai.sdk, "TiRtcStop", return_value=0) as sdk_stop, \
                mock.patch.object(rtc_ai.sdk, "TiRtcUninit") as sdk_uninit:
            stop_thread = threading.Thread(target=runtime.stop)
            stop_thread.start()
            time.sleep(0.05)
            sdk_stop.assert_not_called()
            sdk_uninit.assert_not_called()

            release.set()
            callback_thread.join(timeout=1.0)
            stop_thread.join(timeout=1.0)

        self.assertFalse(stop_thread.is_alive())
        sdk_stop.assert_called_once_with()
        sdk_uninit.assert_called_once_with()

    def test_runtime_stop_drains_service_deferred_tasks_before_sdk_stop(self):
        runtime = TiRtcRuntime()
        service_guard = SdkCallbackGuard()
        runtime.register_service(
            ServiceKind.AI,
            TIRTCCALLBACKS(),
            accepts_inbound=False,
            callback_guard=service_guard,
        )
        runtime._initialized = True
        runtime._start_submitted = True
        runtime._started = True
        runtime._stopped_event.set()
        entered = threading.Event()
        release = threading.Event()

        def deferred_action():
            entered.set()
            release.wait(timeout=2.0)

        service_guard.defer(
            deferred_action, name="test-service-deferred")
        self.assertTrue(entered.wait(timeout=1.0))

        with mock.patch.object(
                rtc_ai.sdk, "TiRtcStop", return_value=0) as sdk_stop, \
                mock.patch.object(rtc_ai.sdk, "TiRtcUninit") as sdk_uninit:
            stop_thread = threading.Thread(target=runtime.stop)
            stop_thread.start()
            time.sleep(0.05)
            sdk_stop.assert_not_called()
            sdk_uninit.assert_not_called()

            release.set()
            stop_thread.join(timeout=1.0)

        self.assertFalse(stop_thread.is_alive())
        sdk_stop.assert_called_once_with()
        sdk_uninit.assert_called_once_with()

    def test_stream_service_stop_waits_for_callback_return(self):
        old_active = rtc_stream._service_active
        old_conn = rtc_stream._active_conn
        old_thread = rtc_stream._active_thread
        release, callback_thread = self._start_blocked_callback(
            rtc_stream._callback_guard
        )
        rtc_stream._service_active = True
        rtc_stream._active_conn = None
        rtc_stream._active_thread = None
        try:
            with mock.patch.object(rtc_stream, "_close_talkback_file"), \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcStop", return_value=0
                    ) as sdk_stop, \
                    mock.patch.object(rtc_stream.sdk, "TiRtcUninit") as sdk_uninit:
                stop_thread = threading.Thread(
                    target=rtc_stream.stop_service)
                stop_thread.start()
                time.sleep(0.05)
                sdk_stop.assert_not_called()
                sdk_uninit.assert_not_called()

                release.set()
                callback_thread.join(timeout=1.0)
                stop_thread.join(timeout=1.0)
        finally:
            rtc_stream._service_active = old_active
            rtc_stream._active_conn = old_conn
            rtc_stream._active_thread = old_thread

        self.assertFalse(stop_thread.is_alive())
        sdk_stop.assert_not_called()
        sdk_uninit.assert_not_called()

    def test_device_call_service_stop_waits_for_callback_return(self):
        old_active = rtc_call._service_active
        release, callback_thread = self._start_blocked_callback(
            rtc_call._callback_guard
        )
        rtc_call._service_active = True
        try:
            with mock.patch.object(rtc_call, "hangup"), \
                    mock.patch.object(rtc_call._media, "shutdown"), \
                    mock.patch.object(rtc_call.sdk, "TiRtcStop") as sdk_stop, \
                    mock.patch.object(rtc_call.sdk, "TiRtcUninit") as sdk_uninit:
                stop_thread = threading.Thread(
                    target=rtc_call.stop_service)
                stop_thread.start()
                time.sleep(0.05)
                sdk_stop.assert_not_called()
                sdk_uninit.assert_not_called()

                release.set()
                callback_thread.join(timeout=1.0)
                stop_thread.join(timeout=1.0)
        finally:
            rtc_call._service_active = old_active

        self.assertFalse(stop_thread.is_alive())
        sdk_stop.assert_not_called()
        sdk_uninit.assert_not_called()

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
