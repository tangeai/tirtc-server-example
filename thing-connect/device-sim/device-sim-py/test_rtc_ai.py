import ctypes
import json
import unittest
from unittest import mock

import rtc_ai


class _FakeRecorder:
    instances = []

    def __init__(self, *args, **kwargs):
        self.args = args
        self.kwargs = kwargs
        self.closed = False
        self.is_open = False
        self.frame_count = 0
        self.__class__.instances.append(self)

    def open(self):
        self.is_open = True
        return "/tmp/ai-test.raw"

    def close(self):
        self.closed = True
        self.is_open = False


class _FakeTimer:
    instances = []

    def __init__(self, interval, callback, args=None):
        self.interval = interval
        self.callback = callback
        self.args = tuple(args or ())
        self.cancelled = False
        self.started = False
        self.daemon = False
        self.__class__.instances.append(self)

    def start(self):
        self.started = True

    def cancel(self):
        self.cancelled = True

    def fire(self):
        if not self.cancelled:
            self.callback(*self.args)


class _ImmediateThread:
    def __init__(self, target, args=(), kwargs=None, **_ignored):
        self.target = target
        self.args = args
        self.kwargs = kwargs or {}

    def start(self):
        self.target(*self.args, **self.kwargs)


class RtcAiLifecycleTests(unittest.TestCase):
    def setUp(self):
        rtc_ai._callback_guard.start()
        rtc_ai.configure_receive_dir()
        self.ended = []
        _FakeRecorder.instances.clear()
        _FakeTimer.instances.clear()
        with rtc_ai._state_lock:
            rtc_ai._cancel_session_timers_locked()
            rtc_ai._session_state = "IDLE"
            rtc_ai._active_hconn = None
            rtc_ai._recv_recorder = None
            rtc_ai._stream_thread = None
            rtc_ai._connect_cb_ref = None
            rtc_ai._connect_cb_refs.clear()
            rtc_ai._session_generation = 0
            rtc_ai._terminal_notified_generation = 0
        rtc_ai.set_session_end_callback(lambda: self.ended.append("end"))
        self.patches = [
            mock.patch.object(rtc_ai, "AudioRecorder", _FakeRecorder),
            mock.patch.object(rtc_ai.threading, "Timer", _FakeTimer),
            mock.patch.object(
                rtc_ai.sdk, "TiRtcGetErrorStr", return_value=b"test error"),
            mock.patch.object(rtc_ai.sdk, "TiRtcDisconnect", return_value=0),
            mock.patch.object(
                rtc_ai.process_tirtc_runtime,
                "bind_active_connection",
                return_value=True,
            ),
        ]
        for patcher in self.patches:
            patcher.start()

    def tearDown(self):
        rtc_ai.stop_session()
        rtc_ai._callback_guard.close()
        rtc_ai.set_session_end_callback(None)
        for patcher in reversed(self.patches):
            patcher.stop()

    def test_immediate_connect_failure_notifies_terminal_once(self):
        with mock.patch.object(
                rtc_ai.sdk, "TiRtcWhipConnect", return_value=-123):
            rtc_ai.start_session("peer", "token", "audio.pcm", "dev")

        self.assertEqual(rtc_ai.get_state(), "IDLE")
        self.assertEqual(self.ended, ["end"])
        self.assertEqual(rtc_ai._connect_cb_refs, {})
        self.assertTrue(_FakeRecorder.instances[-1].closed)

    def test_connect_timeout_releases_session(self):
        with mock.patch.object(
                rtc_ai.sdk, "TiRtcWhipConnect", return_value=0):
            rtc_ai.start_session("peer", "token", "audio.pcm", "dev")

        timer = rtc_ai._connect_timer
        self.assertIsNotNone(timer)
        timer.fire()

        self.assertEqual(rtc_ai.get_state(), "IDLE")
        self.assertEqual(self.ended, ["end"])
        self.assertTrue(_FakeRecorder.instances[-1].closed)

    def test_late_success_callback_cannot_replace_new_generation(self):
        callbacks = []

        def remember_callback(peer, token, callback, user):
            callbacks.append(callback)
            return 0

        with mock.patch.object(
                rtc_ai.sdk, "TiRtcWhipConnect",
                side_effect=remember_callback):
            rtc_ai.start_session("old-peer", "token", "old.pcm", "dev")
            first_generation = rtc_ai._session_generation
            rtc_ai.stop_session()
            rtc_ai.start_session("new-peer", "token", "new.pcm", "dev")
            second_generation = rtc_ai._session_generation

        with mock.patch.object(
                rtc_ai.threading, "Thread", _ImmediateThread):
            callbacks[0](0, ctypes.c_void_p(0x1234), None)
        rtc_ai._callback_guard.wait_for_all()

        self.assertNotEqual(first_generation, second_generation)
        self.assertEqual(rtc_ai.get_state(), "CONNECTING")
        self.assertIsNone(rtc_ai._active_hconn)
        disconnected = rtc_ai.sdk.TiRtcDisconnect.call_args.args[0]
        self.assertEqual(disconnected.value, 0x1234)

    def test_start_response_timeout_releases_connected_session(self):
        with rtc_ai._state_lock:
            rtc_ai._session_generation = 7
            rtc_ai._terminal_notified_generation = 0
            rtc_ai._session_state = "CONNECTING"
            rtc_ai._active_hconn = 0x4567
            rtc_ai._recv_recorder = _FakeRecorder()
            rtc_ai._recv_recorder.open()
        rtc_ai._arm_start_response_timeout(7, 0x4567)

        timer = rtc_ai._start_response_timer
        self.assertIsNotNone(timer)
        timer.fire()

        self.assertEqual(rtc_ai.get_state(), "IDLE")
        self.assertEqual(self.ended, ["end"])
        disconnected = rtc_ai.sdk.TiRtcDisconnect.call_args.args[0]
        self.assertEqual(disconnected.value, 0x4567)

    def test_start_session_uses_configured_receive_dir_and_device_subdir(self):
        rtc_ai.configure_receive_dir("/configured/received")
        with mock.patch.object(
                rtc_ai.sdk, "TiRtcWhipConnect", return_value=0):
            rtc_ai.start_session("peer", "token", "audio.pcm", "dev-1")

        recorder = _FakeRecorder.instances[-1]
        self.assertEqual(recorder.args[0], "/configured/received")
        self.assertEqual(recorder.args[1], "dev-1")
        self.assertRegex(recorder.args[2], r"^ai_\d+\.raw$")

    def test_start_session_accepts_positive_send_count_and_declares_g711a(self):
        callbacks = []

        def remember_callback(peer, token, callback, user):
            callbacks.append(callback)
            return 0

        rtc_ai.configure_audio_formats("alaw_8khz", "alaw_8khz")
        with mock.patch.object(
                rtc_ai.sdk, "TiRtcWhipConnect",
                side_effect=remember_callback), \
                mock.patch.object(
                    rtc_ai.sdk, "TiRtcSendCommand", return_value=302
                ) as send_command, \
                mock.patch.object(rtc_ai.threading, "Thread", _ImmediateThread), \
                mock.patch.object(rtc_ai.time, "sleep"):
            rtc_ai.start_session(
                "whips://ai.example?role_id=role-1",
                "token",
                "audio.g711a",
                "dev",
            )
            callbacks[0](0, ctypes.c_void_p(0x1234), None)
            rtc_ai._callback_guard.wait_for_all()

            raw_message = send_command.call_args.args[2]
            message = json.loads(raw_message.decode())
            expected = {"codec": "g711a", "sample_rate": 8000, "channels": 1}
            self.assertEqual(message["params"]["input_audio"], expected)
            self.assertEqual(message["params"]["output_audio"], expected)
            self.assertEqual(rtc_ai.get_state(), "CONNECTING")
            self.assertIsNotNone(rtc_ai._start_response_timer)
            rtc_ai.stop_session()


if __name__ == "__main__":
    unittest.main()
