#!/usr/bin/env python3

import ctypes
import os
import tempfile
import threading
import unittest
from unittest import mock

os.environ.setdefault("TIRTC_SDK_VERSION", "2.3.0")

import rtc_call_media


_STATE_FIELDS = {
    "send_audio_path": "_send_audio_path",
    "send_video_path": "_send_video_path",
    "recv_dir": "_recv_dir",
    "device_id": "_device_id",
    "send_audio_fmt": "_send_audio_fmt",
    "send_video_fmt": "_send_video_fmt",
    "recv_video_fmt": "_recv_video_fmt",
    "recv_recorder": "_recv_recorder",
    "recv_vf": "_recv_vf",
    "recv_video_path": "_recv_video_path",
    "stream_thread": "_stream_thread",
    "mic_thread": "_mic_thread",
    "speaker_thread": "_speaker_thread",
    "video_thread": "_video_thread",
    "hconn": "_hconn",
    "hw_audio": "_hw_audio",
    "hw_audio_fmt": "_hw_audio_fmt",
    "hw_mic_device": "_hw_mic_device",
    "hw_spk_device": "_hw_spk_device",
    "hw_spk": "_hw_spk",
    "play_queue": "_play_queue",
    "receive_work": "_receive_work",
    "echo_gate": "_echo_gate",
    "session_video_capable": "_session_video_capable",
    "video_enabled": "_video_enabled",
    "video_generation": "_video_generation",
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


class RtcCallMediaTests(unittest.TestCase):
    def setUp(self):
        self._saved = {
            name: getattr(rtc_call_media, attr_name)
            for name, attr_name in _STATE_FIELDS.items()
        }
        rtc_call_media._stream_stop.clear()

    def tearDown(self):
        if rtc_call_media._recv_vf is not None:
            rtc_call_media._recv_vf.close()
            rtc_call_media._recv_vf = None
        if rtc_call_media._recv_recorder is not None:
            rtc_call_media._recv_recorder.close()
            rtc_call_media._recv_recorder = None
        for name, attr_name in _STATE_FIELDS.items():
            setattr(rtc_call_media, attr_name, self._saved[name])
        rtc_call_media._stream_stop.set()

    def test_start_allows_audio_only_file_mode(self):
        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            with open(audio_path, "wb") as target:
                target.write(b"\xd5" * 320)

            rtc_call_media.configure(
                "DEV000001",
                audio_path,
                "",
                root,
                audio_fmt="alaw_8khz",
                up_video_fmt="h264",
                down_video_fmt="h264",
            )
            rtc_call_media.set_hconn(0x1234)
            rtc_call_media._hw_audio = False
            rtc_call_media._stream_thread = None

            original_thread = rtc_call_media.threading.Thread
            rtc_call_media.threading.Thread = _FakeThread
            try:
                rtc_call_media.start()
                self.assertIsNotNone(rtc_call_media._stream_thread)
                self.assertTrue(rtc_call_media._stream_thread.started)
                self.assertIsNone(rtc_call_media._recv_vf)
                self.assertEqual(rtc_call_media._recv_video_path, "")
            finally:
                rtc_call_media.threading.Thread = original_thread
                rtc_call_media.stop()

    def test_audio_call_does_not_start_configured_video(self):
        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            video_path = os.path.join(root, "video.h264")
            with open(audio_path, "wb") as target:
                target.write(b"\xd5" * 320)
            with open(video_path, "wb") as target:
                target.write(b"\x00\x00\x00\x01\x65\x80")

            rtc_call_media.configure(
                "DEV000001",
                audio_path,
                video_path,
                root,
                audio_fmt="alaw_8khz",
                up_video_fmt="h264",
                down_video_fmt="h264",
            )
            rtc_call_media.prepare_session(False)
            rtc_call_media.set_hconn(0x1234)
            rtc_call_media._hw_audio = False
            rtc_call_media._stream_thread = None

            with mock.patch.object(
                    rtc_call_media.threading, "Thread",
                    side_effect=_FakeThread):
                rtc_call_media.start()

            self.assertIsNotNone(rtc_call_media._stream_thread)
            self.assertIsNone(rtc_call_media._video_thread)
            self.assertIsNone(rtc_call_media._recv_vf)
            self.assertEqual(rtc_call_media._recv_video_path, "")
            rtc_call_media.stop()

    def test_video_subscription_cannot_override_audio_call(self):
        rtc_call_media._send_video_path = "video.h264"

        rtc_call_media.prepare_session(False)
        self.assertFalse(
            rtc_call_media.subscribe_video(rtc_call_media.VIDEO_STREAM_ID))
        self.assertEqual(
            rtc_call_media._video_state()[:2], (False, False))

        rtc_call_media.prepare_session(True)
        self.assertEqual(
            rtc_call_media._video_state()[:2], (True, True))
        self.assertTrue(
            rtc_call_media.unsubscribe_video(rtc_call_media.VIDEO_STREAM_ID))
        self.assertEqual(
            rtc_call_media._video_state()[:2], (True, False))
        self.assertFalse(
            rtc_call_media.request_video_key_frame(
                rtc_call_media.VIDEO_STREAM_ID))
        self.assertTrue(
            rtc_call_media.subscribe_video(rtc_call_media.VIDEO_STREAM_ID))
        self.assertTrue(
            rtc_call_media.request_video_key_frame(
                rtc_call_media.VIDEO_STREAM_ID))

    def test_start_without_file_audio_skips_output_initialization(self):
        with tempfile.TemporaryDirectory() as root:
            rtc_call_media.configure(
                "DEV000001",
                "",
                "",
                root,
                audio_fmt="alaw_8khz",
                up_video_fmt="h264",
                down_video_fmt="h264",
            )
            rtc_call_media.set_hconn(0x1234)
            rtc_call_media._hw_audio = False

            rtc_call_media.start()

            self.assertIsNone(rtc_call_media._recv_recorder)
            self.assertIsNone(rtc_call_media._recv_vf)
            self.assertEqual(rtc_call_media._recv_video_path, "")
            self.assertIsNone(rtc_call_media._stream_thread)

    def test_hardware_audio_uses_system_default_mic_when_device_index_is_none(self):
        with tempfile.TemporaryDirectory() as root:
            rtc_call_media.configure(
                "DEV000001",
                "",
                "",
                root,
                audio_fmt="pcm_s16le_16khz",
                up_video_fmt="h264",
                down_video_fmt="h264",
            )
            rtc_call_media.set_hconn(0x1234)
            rtc_call_media._hw_audio = True
            rtc_call_media._hw_audio_fmt = "pcm_s16le_16khz"
            rtc_call_media._hw_mic_device = None
            rtc_call_media._hw_spk = mock.Mock()
            rtc_call_media._stream_thread = None

            with mock.patch.object(
                    rtc_call_media.threading, "Thread", side_effect=_FakeThread), \
                    mock.patch.object(rtc_call_media, "_ECHO_GATE_AVAILABLE", False):
                rtc_call_media.start()

            self.assertIsNotNone(rtc_call_media._mic_thread)
            self.assertTrue(rtc_call_media._mic_thread.started)
            self.assertIsNotNone(rtc_call_media._speaker_thread)
            self.assertTrue(rtc_call_media._speaker_thread.started)
            self.assertIsNone(rtc_call_media._stream_thread)
            rtc_call_media.stop()

    def test_start_ignores_duplicate_start_when_media_thread_is_alive(self):
        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            with open(audio_path, "wb") as target:
                target.write(b"\xd5" * 320)

            rtc_call_media.configure(
                "DEV000001",
                audio_path,
                "",
                root,
                audio_fmt="alaw_8khz",
                up_video_fmt="h264",
                down_video_fmt="h264",
            )
            rtc_call_media.set_hconn(0x1234)
            rtc_call_media._hw_audio = False
            rtc_call_media._stream_thread = _FakeThread(target=lambda: None)
            rtc_call_media._stream_thread.started = True

            with mock.patch.object(rtc_call_media, "AudioRecorder") as recorder_cls, \
                    mock.patch.object(rtc_call_media.threading, "Thread", side_effect=_FakeThread) as thread_ctor:
                rtc_call_media.start()
                recorder_cls.assert_not_called()
                self.assertEqual(thread_ctor.call_count, 0)

    def test_real_file_media_worker_sends_and_stops_cleanly(self):
        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            with open(audio_path, "wb") as target:
                target.write(b"\xd5" * 320)

            rtc_call_media.configure(
                "DEV000001",
                audio_path,
                "",
                root,
                audio_fmt="alaw_8khz",
                up_video_fmt="h264",
                down_video_fmt="h264",
            )
            rtc_call_media.prepare_session(False)
            rtc_call_media.set_hconn(0x1234)
            rtc_call_media._hw_audio = False
            rtc_call_media._stream_thread = None
            sent = threading.Event()

            def send_audio(_hconn, _frame, _buffer):
                sent.set()
                return 0

            with mock.patch.object(
                    rtc_call_media.sdk,
                    "TiRtcSendAudioStream",
                    side_effect=send_audio), \
                    mock.patch.object(rtc_call_media, "_warn") as warn:
                rtc_call_media.start()
                worker = rtc_call_media._stream_thread
                try:
                    self.assertIsNotNone(worker)
                    self.assertTrue(sent.wait(timeout=1.0))
                finally:
                    rtc_call_media.stop()

                self.assertFalse(worker.is_alive())
                self.assertFalse(
                    any(
                        "内未退出" in str(call)
                        for call in warn.call_args_list
                    )
                )


if __name__ == "__main__":
    unittest.main()
