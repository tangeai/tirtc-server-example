#!/usr/bin/env python3

import ctypes
import os
import tempfile
import threading
import time
import unittest
from unittest import mock

os.environ.setdefault("TIRTC_SDK_VERSION", "2.3.0")

import rtc_voip


_STATE_FIELDS = {
    "state": "_session_state",
    "hconn": "_active_hconn",
    "audio_file": "_audio_file_path",
    "recv_file": "_recv_file",
    "recv_video_file": "_recv_video_file",
    "recv_audio_path": "_recv_audio_path",
    "recv_video_path": "_recv_video_path",
    "recv_root": "_recv_root",
    "stream_thread": "_stream_thread",
    "video_thread": "_video_thread",
    "video_file": "_video_file_path",
    "session_video_file": "_session_video_file",
    "role": "_session_role",
    "device_id": "_device_id",
    "connect_cb_ref": "_connect_cb_ref",
    "connect_cb_refs": "_connect_cb_refs",
    "whip_connect_timer": "_whip_connect_timer",
    "connect_timer": "_connect_timer",
    "session_generation": "_session_generation",
    "service_active": "_service_active",
    "speaker": "_speaker",
    "mic_capture": "_mic_capture",
}


class _FakeThread:
    def __init__(self, target, args=(), daemon=None, name=None):
        self.target = target
        self.args = args
        self.daemon = daemon
        self.name = name
        self.started = False

    def start(self):
        self.started = True

    def is_alive(self):
        return self.started

    def join(self, timeout=None):
        self.started = False


class _FakeTimer:
    def __init__(self, interval, callback, args=(), kwargs=None):
        self.interval = interval
        self.callback = callback
        self.args = args
        self.kwargs = kwargs or {}
        self.daemon = False
        self.started = False
        self.cancelled = False

    def start(self):
        self.started = True

    def cancel(self):
        self.cancelled = True

    def fire(self):
        if not self.cancelled:
            self.callback(*self.args, **self.kwargs)


class RtcVoipTests(unittest.TestCase):
    def setUp(self):
        rtc_voip._callback_guard.start()
        self._saved = {
            name: getattr(rtc_voip, attr_name)
            for name, attr_name in _STATE_FIELDS.items()
        }
        self._bind_runtime = mock.patch.object(
            rtc_voip.process_tirtc_runtime,
            "bind_active_connection",
            return_value=True,
        )
        self._bind_runtime.start()
        self._safe_disconnect = mock.patch.object(
            rtc_voip.sdk, "TiRtcDisconnect", return_value=0)
        self._safe_disconnect.start()
        rtc_voip._stream_stop.clear()

    def tearDown(self):
        rtc_voip._callback_guard.close()
        for handle_name in ("_recv_file", "_recv_video_file"):
            handle = getattr(rtc_voip, handle_name)
            if handle is not None:
                handle.close()
                setattr(rtc_voip, handle_name, None)
        for name, attr_name in _STATE_FIELDS.items():
            setattr(rtc_voip, attr_name, self._saved[name])
        self._safe_disconnect.stop()
        self._bind_runtime.stop()
        rtc_voip._stream_stop.set()

    def test_report_profile_includes_camera_rotation(self):
        profile_response = mock.Mock(
            status_code=200,
            headers={"Content-Type": "application/json"},
            text="",
        )
        profile_response.json.return_value = {"code": 0}
        callers_response = mock.Mock(
            status_code=200,
            headers={"Content-Type": "application/json"},
            text="",
        )
        callers_response.json.return_value = {"code": 0, "data": {"contacts": []}}
        rtc_voip._video_file_path = "video.h264"

        with mock.patch.dict(os.environ, {
            "VOIP_CAMERA_ROTATION": "270",
            "VOIP_ASPECT_RATIO": "1.7777777778",
            "VOIP_SCREEN_WIDTH": "1024",
            "VOIP_SCREEN_HEIGHT": "600",
            "VOIP_HOR_MIRROR": "true",
            "VOIP_VERT_MIRROR": "false",
            "VOIP_OBJECT_FIT": "contain",
            "VOIP_VIDEO_RES_MODE": "fit_screen",
        }), \
                mock.patch.object(rtc_voip, "_down_video_format", "mjpeg"), \
                mock.patch.object(
                    rtc_voip.http_trace,
                    "request",
                    side_effect=[profile_response, callers_response],
                ) as request:
            rtc_voip.report_profile("https://voip.example", "token", with_video=True)

        profile = request.call_args_list[0].kwargs["json"]
        self.assertEqual(profile["camera_rotation"], 270)
        self.assertEqual(profile["screen_width"], 1024)
        self.assertEqual(profile["screen_height"], 600)
        self.assertEqual(profile["aspect_ratio"], 1.7777777778)
        self.assertIs(profile["hor_mirror"], True)
        self.assertIs(profile["vert_mirror"], False)
        self.assertEqual(profile["object_fit"], "contain")
        self.assertEqual(profile["down_video_mt"], "mjpeg")
        self.assertEqual(profile["video_res_mode"], "fit_screen")
        self.assertEqual(
            request.call_args_list[1].args[1],
            "https://voip.example/v1/voip/device/contacts",
        )

    def test_start_session_waits_for_0x2000_before_starting_audio_and_video_threads(self):
        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            video_path = os.path.join(root, "video.h264")
            with open(audio_path, "wb") as f:
                f.write(b"\xd5" * 320)
            with open(video_path, "wb") as f:
                f.write(b"\x00\x00\x00\x01\x67\x00\x00\x00\x01\x68\x00\x00\x00\x01\x65")

            rtc_voip._session_state = "IDLE"
            rtc_voip._active_hconn = None
            rtc_voip._audio_file_path = None
            rtc_voip._recv_root = root
            rtc_voip._stream_thread = None
            rtc_voip._video_thread = None
            rtc_voip._video_file_path = video_path
            rtc_voip._session_video_file = ""
            rtc_voip._session_role = "unknown"
            rtc_voip._device_id = "DEV000001"

            def whip_connect(_peer_id, _token, callback, _user_data):
                callback(0, ctypes.c_void_p(0x1234), None)
                return 0

            with mock.patch.object(rtc_voip, "_log_whip_connect_params"), \
                    mock.patch.object(rtc_voip, "convert_audio_to_wav"), \
                    mock.patch.object(rtc_voip, "convert_video_to_mp4"), \
                    mock.patch.object(rtc_voip.sdk, "TiRtcWhipConnect", side_effect=whip_connect), \
                    mock.patch.object(rtc_voip.threading, "Timer", side_effect=_FakeTimer), \
                    mock.patch.object(rtc_voip.threading, "Thread", side_effect=_FakeThread) as thread_ctor:
                rtc_voip.start_session(
                    "whips://peer",
                    "token",
                    audio_path,
                    with_video=True,
                    session_role="wx_caller",
                )
                self.assertEqual(rtc_voip._session_state, "CONNECTING")
                self.assertEqual(rtc_voip._active_hconn, 0x1234)
                self.assertIsNone(rtc_voip._stream_thread)
                self.assertIsNone(rtc_voip._video_thread)
                self.assertIsNotNone(rtc_voip._connect_timer)
                self.assertEqual(thread_ctor.call_count, 0)

                rtc_voip._start_media_threads(0x1234)
                self.assertEqual(rtc_voip._session_state, "IN_CALL")
                self.assertIsNotNone(rtc_voip._stream_thread)
                self.assertTrue(rtc_voip._stream_thread.started)
                self.assertIsNotNone(rtc_voip._video_thread)
                self.assertTrue(rtc_voip._video_thread.started)
                self.assertEqual(thread_ctor.call_count, 2)

                rtc_voip._start_media_threads(0x1234)
                self.assertEqual(thread_ctor.call_count, 2)

    def test_connect_ack_timeout_disconnects_and_recovers(self):
        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            with open(audio_path, "wb") as f:
                f.write(b"\xd5" * 320)

            rtc_voip._session_state = "IDLE"
            rtc_voip._active_hconn = None
            rtc_voip._audio_file_path = None
            rtc_voip._recv_root = root
            rtc_voip._stream_thread = None
            rtc_voip._video_thread = None
            rtc_voip._video_file_path = ""
            rtc_voip._session_video_file = ""
            rtc_voip._session_role = "unknown"
            rtc_voip._device_id = "DEV000001"

            def whip_connect(_peer_id, _token, callback, _user_data):
                callback(0, ctypes.c_void_p(0x1234), None)
                return 0

            with mock.patch.object(rtc_voip, "_log_whip_connect_params"), \
                    mock.patch.object(rtc_voip, "convert_audio_to_wav"), \
                    mock.patch.object(rtc_voip, "convert_video_to_mp4"), \
                    mock.patch.object(rtc_voip.sdk, "TiRtcWhipConnect", side_effect=whip_connect), \
                    mock.patch.object(rtc_voip.sdk, "TiRtcDisconnect") as disconnect, \
                    mock.patch.object(rtc_voip.threading, "Timer", side_effect=_FakeTimer):
                rtc_voip.start_session(
                    "whips://peer",
                    "token",
                    audio_path,
                    with_video=False,
                    session_role="wx_caller",
                )
                self.assertEqual(rtc_voip._session_state, "CONNECTING")
                self.assertIsNotNone(rtc_voip._connect_timer)
                rtc_voip._connect_timer.fire()
                self.assertEqual(rtc_voip._session_state, "IDLE")
                disconnect.assert_called_once()

    def test_missing_whip_callback_times_out_and_recovers(self):
        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            with open(audio_path, "wb") as f:
                f.write(b"\xd5" * 320)

            rtc_voip._session_state = "IDLE"
            rtc_voip._active_hconn = None
            rtc_voip._recv_root = root
            rtc_voip._video_file_path = ""
            rtc_voip._device_id = "DEV000001"
            session_end = mock.Mock()
            old_callback = rtc_voip._session_end_callback
            rtc_voip.set_session_end_callback(session_end)
            try:
                with mock.patch.object(rtc_voip, "_log_whip_connect_params"), \
                        mock.patch.object(
                            rtc_voip.sdk, "TiRtcWhipConnect", return_value=0), \
                        mock.patch.object(
                            rtc_voip.threading, "Timer",
                            side_effect=_FakeTimer):
                    rtc_voip.start_session(
                        "whips://peer", "token", audio_path,
                        with_video=False)
                    timer = rtc_voip._whip_connect_timer
                    self.assertIsNotNone(timer)
                    timer.fire()
            finally:
                rtc_voip.set_session_end_callback(old_callback)

            self.assertEqual(rtc_voip._session_state, "IDLE")
            session_end.assert_called_once_with()

    def test_late_connect_callback_after_cancel_is_disconnected(self):
        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            with open(audio_path, "wb") as f:
                f.write(b"\xd5" * 320)

            rtc_voip._session_state = "IDLE"
            rtc_voip._active_hconn = None
            rtc_voip._recv_root = root
            rtc_voip._video_file_path = ""
            rtc_voip._device_id = "DEV000001"
            rtc_voip._connect_cb_refs = {}
            captured = {}

            def whip_connect(_peer_id, _token, callback, _user_data):
                captured["callback"] = callback
                return 0

            with mock.patch.object(rtc_voip, "_log_whip_connect_params"), \
                    mock.patch.object(rtc_voip.sdk, "TiRtcWhipConnect", side_effect=whip_connect), \
                    mock.patch.object(rtc_voip.sdk, "TiRtcDisconnect") as disconnect:
                rtc_voip.start_session("whips://peer", "token", audio_path, with_video=False)
                self.assertEqual(rtc_voip._session_state, "CONNECTING")

                rtc_voip.stop_session()
                self.assertEqual(rtc_voip._session_state, "IDLE")

                captured["callback"](0, ctypes.c_void_p(0x4321), None)
                rtc_voip._callback_guard.wait_for_all()
                self.assertEqual(rtc_voip._session_state, "IDLE")
                self.assertIsNone(rtc_voip._active_hconn)
                disconnect.assert_called_once()
                self.assertEqual(disconnect.call_args.args[0].value, 0x4321)

    def test_async_connect_failure_notifies_session_end(self):
        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            with open(audio_path, "wb") as f:
                f.write(b"\xd5" * 320)

            rtc_voip._session_state = "IDLE"
            rtc_voip._active_hconn = None
            rtc_voip._recv_root = root
            rtc_voip._video_file_path = ""
            rtc_voip._device_id = "DEV000001"
            rtc_voip._connect_cb_refs = {}
            captured = {}

            def whip_connect(_peer_id, _token, callback, _user_data):
                captured["callback"] = callback
                return 0

            session_end = mock.Mock()
            old_callback = rtc_voip._session_end_callback
            rtc_voip.set_session_end_callback(session_end)
            try:
                with mock.patch.object(rtc_voip, "_log_whip_connect_params"), \
                        mock.patch.object(rtc_voip.sdk, "TiRtcWhipConnect", side_effect=whip_connect), \
                        mock.patch.object(rtc_voip.sdk, "TiRtcGetErrorStr", return_value=b"connect failed"):
                    rtc_voip.start_session("whips://peer", "token", audio_path, with_video=False)
                    captured["callback"](123, ctypes.c_void_p(), None)
            finally:
                rtc_voip.set_session_end_callback(old_callback)

            self.assertEqual(rtc_voip._session_state, "IDLE")
            session_end.assert_called_once_with()

    def test_cancel_on_connect_notifies_session_end_after_explicit_stop(self):
        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            with open(audio_path, "wb") as f:
                f.write(b"\xd5" * 320)

            rtc_voip._session_state = "IDLE"
            rtc_voip._active_hconn = None
            rtc_voip._recv_root = root
            rtc_voip._video_file_path = ""
            rtc_voip._device_id = "DEV000001"
            def whip_connect(_peer_id, _token, callback, _user_data):
                callback(0, ctypes.c_void_p(0x1234), None)
                return 0

            session_end = mock.Mock()
            old_callback = rtc_voip._session_end_callback
            rtc_voip.set_session_end_callback(session_end)
            try:
                with mock.patch.object(rtc_voip, "_log_whip_connect_params"), \
                        mock.patch.object(rtc_voip, "convert_audio_to_wav"), \
                        mock.patch.object(rtc_voip.sdk, "TiRtcWhipConnect", side_effect=whip_connect), \
                        mock.patch.object(rtc_voip.sdk, "TiRtcSendCommand"), \
                        mock.patch.object(rtc_voip.sdk, "TiRtcDisconnect"):
                    rtc_voip.start_session(
                        "whips://peer",
                        "token",
                        audio_path,
                        with_video=False,
                        cancel_on_connect=True,
                    )
                    rtc_voip._callback_guard.wait_for_all()
            finally:
                rtc_voip.set_session_end_callback(old_callback)

            self.assertEqual(rtc_voip._session_state, "IDLE")
            session_end.assert_called_once_with()

    def test_stop_service_stops_session_without_stopping_sdk(self):
        class _Closable:
            def __init__(self):
                self.closed = False

            def close(self):
                self.closed = True

        speaker = _Closable()
        mic = _Closable()
        rtc_voip._service_active = True
        rtc_voip._speaker = speaker
        rtc_voip._mic_capture = mic

        with mock.patch.object(rtc_voip, "stop_session") as stop_session, \
                mock.patch.object(rtc_voip.sdk, "TiRtcStop") as sdk_stop, \
                mock.patch.object(rtc_voip.sdk, "TiRtcUninit") as sdk_uninit:
            rtc_voip.stop_service()

        stop_session.assert_called_once_with()
        sdk_stop.assert_not_called()
        sdk_uninit.assert_not_called()
        self.assertFalse(rtc_voip._service_active)
        self.assertTrue(speaker.closed)
        self.assertTrue(mic.closed)
        self.assertIsNone(rtc_voip._speaker)
        self.assertIsNone(rtc_voip._mic_capture)

    def test_stop_service_waits_until_active_sdk_callback_returns(self):
        callback_entered = threading.Event()
        release_callback = threading.Event()

        def callback():
            callback_entered.set()
            release_callback.wait(timeout=2.0)

        tracked = rtc_voip._tracked_callback(callback)
        callback_thread = threading.Thread(target=tracked)
        callback_thread.start()
        self.assertTrue(callback_entered.wait(timeout=1.0))

        rtc_voip._service_active = True
        with mock.patch.object(rtc_voip, "stop_session"), \
                mock.patch.object(rtc_voip.sdk, "TiRtcStop") as sdk_stop, \
                mock.patch.object(rtc_voip.sdk, "TiRtcUninit") as sdk_uninit:
            stop_thread = threading.Thread(target=rtc_voip.stop_service)
            stop_thread.start()
            time.sleep(0.05)
            sdk_stop.assert_not_called()
            sdk_uninit.assert_not_called()

            release_callback.set()
            callback_thread.join(timeout=1.0)
            stop_thread.join(timeout=1.0)

        self.assertFalse(stop_thread.is_alive())
        sdk_stop.assert_not_called()
        sdk_uninit.assert_not_called()

    def test_remote_hangup_disconnects_only_after_command_callback_returns(self):
        rtc_voip._session_state = "IN_CALL"
        rtc_voip._active_hconn = 0x1234
        callbacks = rtc_voip._build_callbacks()

        with mock.patch.object(
                rtc_voip.sdk, "TiRtcDisconnect", return_value=0) as disconnect:
            callbacks.on_command(ctypes.c_void_p(0x1234), 0x2001, None, 0)
            rtc_voip._callback_guard.wait_for_all()

        disconnect.assert_called_once()
        self.assertEqual(disconnect.call_args.args[0].value, 0x1234)

    def test_remote_disconnect_joins_media_threads_before_session_end(self):
        order = []

        class _MediaThread:
            def __init__(self, name):
                self.name = name

            def join(self, timeout=None):
                order.append(f"join:{self.name}")

        rtc_voip._session_state = "IN_CALL"
        rtc_voip._active_hconn = 0x1234
        rtc_voip._stream_thread = _MediaThread("audio")
        rtc_voip._video_thread = _MediaThread("video")
        old_callback = rtc_voip._session_end_callback
        rtc_voip.set_session_end_callback(lambda: order.append("session_end"))
        try:
            with mock.patch.object(rtc_voip, "_close_receive_files") as close_files:
                rtc_voip._handle_disconnect(0x1234)
        finally:
            rtc_voip.set_session_end_callback(old_callback)

        self.assertEqual(
            order,
            ["join:audio", "join:video", "session_end"],
        )
        close_files.assert_called_once_with()
        self.assertEqual(rtc_voip._session_state, "IDLE")
        self.assertIsNone(rtc_voip._active_hconn)


if __name__ == "__main__":
    unittest.main()
