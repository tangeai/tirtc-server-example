#!/usr/bin/env python3

import ctypes
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

    def test_callback_guard_constructor_does_not_start_a_worker(self):
        guard = SdkCallbackGuard()
        self.assertIsNone(guard._work._thread)
        guard.close()

    def test_callback_guard_control_queue_waits_and_preserves_order(self):
        guard = SdkCallbackGuard()
        guard.start()
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
        service_guard.start()
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
        old_connections = rtc_stream._connections.copy()
        old_thread = rtc_stream._active_thread
        release, callback_thread = self._start_blocked_callback(
            rtc_stream._callback_guard
        )
        rtc_stream._service_active = True
        rtc_stream._connections.clear()
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
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            rtc_stream._active_thread = old_thread

        self.assertFalse(stop_thread.is_alive())
        sdk_stop.assert_not_called()
        sdk_uninit.assert_not_called()

    def test_stream_video_subscription_requests_key_frame(self):
        rtc_stream._force_key_frame.clear()
        callbacks = rtc_stream.runtime_callbacks()
        old_connections = rtc_stream._connections.copy()
        try:
            rtc_stream._connections.clear()
            rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
                pending=False)
            result = callbacks.on_subscribe_video(
                ctypes.c_void_p(0x101), rtc_stream.VIDEO_STREAM_ID)

            self.assertEqual(result, 0)
            self.assertTrue(
                rtc_stream._connections[0x101].send_video_subscribed)
            self.assertTrue(rtc_stream._force_key_frame.is_set())

            rtc_stream._force_key_frame.clear()
            rtc_stream._connections[0x202] = rtc_stream._StreamConnection(
                pending=False)
            result = callbacks.on_subscribe_video(
                ctypes.c_void_p(0x202), rtc_stream.VIDEO_STREAM_ID)
            self.assertEqual(result, 0)
            self.assertFalse(
                rtc_stream._force_key_frame.is_set(),
                "第二个观看端不能跳转所有观看端共用的媒体源",
            )
            callbacks.on_request_key_frame(
                ctypes.c_void_p(0x202), rtc_stream.VIDEO_STREAM_ID)
            self.assertFalse(
                rtc_stream._force_key_frame.is_set(),
                "多端时单端关键帧请求不能跳转共享媒体源",
            )
        finally:
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            rtc_stream._force_key_frame.clear()

    def test_stream_accepts_subscriptions_before_deferred_activation(self):
        callbacks = rtc_stream.runtime_callbacks()
        old_active = rtc_stream._service_active
        old_connections = rtc_stream._connections.copy()
        rtc_stream._service_active = True
        rtc_stream._connections.clear()
        try:
            with mock.patch.object(
                    rtc_stream._callback_guard, "defer", return_value=True):
                callbacks.on_conn_accepted(ctypes.c_void_p(0x202))
            self.assertTrue(rtc_stream._connections[0x202].pending)
            self.assertEqual(
                callbacks.on_subscribe_audio(0x202, rtc_stream.AUDIO_STREAM_ID),
                0,
            )
            self.assertEqual(
                callbacks.on_subscribe_video(0x202, rtc_stream.VIDEO_STREAM_ID),
                0,
            )
            self.assertTrue(
                rtc_stream._connections[0x202].send_audio_subscribed)
            self.assertTrue(
                rtc_stream._connections[0x202].send_video_subscribed)
        finally:
            rtc_stream._service_active = old_active
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)

    def test_stream_connection_subscribes_talkback_before_starting_media(self):
        old_active = rtc_stream._service_active
        old_connections = rtc_stream._connections.copy()
        old_thread = rtc_stream._active_thread
        old_factory = rtc_stream._media_factory
        source = mock.Mock()
        thread = mock.Mock()
        rtc_stream._service_active = True
        rtc_stream._connections.clear()
        rtc_stream._connections[0x202] = rtc_stream._StreamConnection()
        rtc_stream._active_thread = None
        rtc_stream._media_factory = lambda: source
        try:
            with mock.patch.object(
                    rtc_stream.sdk, "TiRtcSubscribeAudio",
                    create=True, return_value=0) as subscribe, \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcSubscribeVideo",
                        return_value=0), \
                    mock.patch.object(rtc_stream, "_open_talkback_file"), \
                    mock.patch.object(
                        rtc_stream.threading, "Thread", return_value=thread):
                rtc_stream._activate_connection_after_callback(0x202)
            subscribe.assert_called_once()
            self.assertEqual(
                subscribe.call_args.args[1], rtc_stream.TALKBACK_STREAM_ID)
            self.assertTrue(
                rtc_stream._connections[0x202].talkback_subscribed)
            thread.start.assert_called_once_with()
        finally:
            rtc_stream._service_active = old_active
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            rtc_stream._active_thread = old_thread
            rtc_stream._media_factory = old_factory

    def test_stream_video_downlink_subscription_failure_does_not_drop_view(self):
        old_active = rtc_stream._service_active
        old_connections = rtc_stream._connections.copy()
        old_thread = rtc_stream._active_thread
        old_factory = rtc_stream._media_factory
        source = mock.Mock()
        thread = mock.Mock()
        rtc_stream._service_active = True
        rtc_stream._connections.clear()
        rtc_stream._connections[0x303] = rtc_stream._StreamConnection()
        rtc_stream._active_thread = None
        rtc_stream._media_factory = lambda: source
        try:
            with mock.patch.object(
                    rtc_stream.sdk, "TiRtcSubscribeAudio", return_value=0), \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcSubscribeVideo", return_value=-1), \
                    mock.patch.object(rtc_stream, "_open_talkback_file"), \
                    mock.patch.object(
                        rtc_stream.threading, "Thread", return_value=thread), \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcDisconnect") as disconnect:
                rtc_stream._activate_connection_after_callback(0x303)
            self.assertTrue(
                rtc_stream._connections[0x303].talkback_subscribed)
            self.assertFalse(
                rtc_stream._connections[0x303].talkback_video_subscribed)
            thread.start.assert_called_once_with()
            disconnect.assert_not_called()
        finally:
            rtc_stream._service_active = old_active
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            rtc_stream._active_thread = old_thread
            rtc_stream._media_factory = old_factory

    def test_stream_talkback_decodes_alaw_for_speaker(self):
        old_recorder = rtc_stream._talkback_recorder
        old_speaker = rtc_stream._talkback_speaker
        speaker = mock.Mock()
        frame = rtc_stream.TIRTCFRAMEINFO()
        frame.stream_id = rtc_stream.TALKBACK_STREAM_ID
        frame.media = rtc_stream.TIRTC_AUDIO_ALAW
        frame.flags = rtc_stream.TIRTC_AUDIOSAMPLE_16K16B1C
        payload = b"\xd5\x55"
        rtc_stream._talkback_recorder = None
        rtc_stream._talkback_speaker = speaker
        try:
            rtc_stream._process_talkback_item((frame, payload))
        finally:
            rtc_stream._talkback_recorder = old_recorder
            rtc_stream._talkback_speaker = old_speaker

        from alaw import alaw_decode
        speaker.play.assert_called_once_with(
            alaw_decode(payload), source_rate=16000)

    def test_stream_receive_requires_matching_local_subscription(self):
        callbacks = rtc_stream.runtime_callbacks()
        old_connections = rtc_stream._connections.copy()
        old_work = rtc_stream._talkback_work
        work = mock.Mock()
        frame = rtc_stream.TIRTCFRAMEINFO()
        frame.stream_id = rtc_stream.TALKBACK_STREAM_ID
        frame.length = 1
        payload = (ctypes.c_uint8 * 1)(1)
        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
            pending=False)
        rtc_stream._connections[0x202] = rtc_stream._StreamConnection()
        rtc_stream._talkback_work = work
        try:
            callbacks.on_audio(0x101, ctypes.byref(frame), payload)
            work.submit.assert_not_called()

            rtc_stream._connections[0x101].talkback_subscribed = True
            callbacks.on_audio(0x101, ctypes.byref(frame), payload)
            work.submit.assert_called_once()

            callbacks.on_audio(0x202, ctypes.byref(frame), payload)
            self.assertEqual(work.submit.call_count, 1)
        finally:
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            rtc_stream._talkback_work = old_work

    def test_stream_talkback_playback_closes_speaker(self):
        old_speaker = rtc_stream._talkback_speaker
        speaker = mock.Mock()
        rtc_stream._talkback_speaker = speaker
        try:
            rtc_stream._close_talkback_playback()
            self.assertIsNone(rtc_stream._talkback_speaker)
        finally:
            rtc_stream._talkback_speaker = old_speaker

        speaker.close.assert_called_once_with()

    def test_stream_second_viewer_reuses_source_and_keeps_first_connection(self):
        old_active = rtc_stream._service_active
        old_connections = rtc_stream._connections.copy()
        old_thread = rtc_stream._active_thread
        old_factory = rtc_stream._media_factory
        existing_thread = mock.Mock()
        media_factory = mock.Mock()
        rtc_stream._service_active = True
        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
            pending=False, send_video_subscribed=True)
        rtc_stream._connections[0x202] = rtc_stream._StreamConnection()
        rtc_stream._active_thread = existing_thread
        rtc_stream._media_factory = media_factory
        try:
            with mock.patch.object(
                    rtc_stream.sdk, "TiRtcDisconnect", return_value=0), \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcSubscribeAudio",
                        return_value=0), \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcSubscribeVideo",
                        return_value=0), \
                    mock.patch.object(rtc_stream, "_open_talkback_file"):
                rtc_stream._activate_connection_after_callback(0x202)
            self.assertIn(0x101, rtc_stream._connections)
            self.assertIn(0x202, rtc_stream._connections)
            self.assertFalse(rtc_stream._connections[0x202].pending)
            self.assertTrue(
                rtc_stream._connections[0x101].send_video_subscribed)
        finally:
            rtc_stream._service_active = old_active
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            rtc_stream._active_thread = old_thread
            rtc_stream._media_factory = old_factory

        media_factory.assert_not_called()

    def test_stream_viewers_keep_independent_subscriptions_and_disconnects(self):
        callbacks = rtc_stream.runtime_callbacks()
        old_connections = rtc_stream._connections.copy()
        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
            pending=False)
        rtc_stream._connections[0x202] = rtc_stream._StreamConnection(
            pending=False)
        try:
            self.assertEqual(
                callbacks.on_subscribe_video(0x101, rtc_stream.VIDEO_STREAM_ID),
                0,
            )
            self.assertTrue(
                rtc_stream._connections[0x101].send_video_subscribed)
            self.assertFalse(
                rtc_stream._connections[0x202].send_video_subscribed)

            callbacks.on_disconnected(0x101)
            self.assertNotIn(0x101, rtc_stream._connections)
            self.assertIn(0x202, rtc_stream._connections)
        finally:
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)

    def test_stream_worker_fans_one_frame_out_to_all_subscribed_viewers(self):
        old_connections = rtc_stream._connections.copy()
        old_stop_requested = rtc_stream._stop_event.is_set()
        source = mock.Mock()
        source.has_video.return_value = False
        source.next_audio_packet.return_value = (b"\xd5", 40)
        source.get_audio_format.return_value = mock.Mock(media=1, flags=0)
        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
            pending=False, send_audio_subscribed=True)
        rtc_stream._connections[0x202] = rtc_stream._StreamConnection(
            pending=False, send_audio_subscribed=True)
        rtc_stream._stop_event.clear()

        def sent(_hconn, _frame, _data):
            if send.call_count == 2:
                rtc_stream._stop_event.set()
            return 0

        try:
            with mock.patch.object(
                    rtc_stream.sdk, "TiRtcSendAudioStream",
                    side_effect=sent) as send:
                rtc_stream._stream_worker(source)
            self.assertEqual(2, send.call_count)
            self.assertEqual(
                {0x101, 0x202},
                {call.args[0].value for call in send.call_args_list},
            )
            source.next_audio_packet.assert_called_once_with()
        finally:
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            if old_stop_requested:
                rtc_stream._stop_event.set()
            else:
                rtc_stream._stop_event.clear()

    def test_stream_worker_realigns_after_all_viewers_stop_and_resume(self):
        old_connections = rtc_stream._connections.copy()
        old_stop_requested = rtc_stream._stop_event.is_set()
        source = mock.Mock()
        source.has_video.return_value = True
        source.next_audio_packet.return_value = (b"audio", 40)
        source.get_audio_format.return_value = mock.Mock(media=1, flags=0)
        source.next_video.return_value = (b"frame", False)
        source.get_video_format.return_value = mock.Mock(media=1)
        now_ms = 0
        phase = "initial"
        sends_after_resume = 0

        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
            pending=False, send_video_subscribed=True)
        rtc_stream._stop_event.clear()

        def current_time():
            return now_ms

        def sleep(_seconds):
            nonlocal now_ms, phase
            if phase == "initial":
                rtc_stream._connections[0x101].send_video_subscribed = False
                now_ms = 300_000
                phase = "idle"
            elif phase == "idle":
                rtc_stream._connections[0x101].send_video_subscribed = True
                phase = "resumed"
            else:
                rtc_stream._stop_event.set()

        def send_video(_hconn, _frame, _data):
            nonlocal sends_after_resume
            if phase == "resumed":
                sends_after_resume += 1
                if sends_after_resume >= 2:
                    rtc_stream._stop_event.set()
            return 0

        try:
            with mock.patch.object(
                    rtc_stream, "_now_ms", side_effect=current_time), \
                    mock.patch.object(
                        rtc_stream.time, "sleep", side_effect=sleep), \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcSendVideoStream",
                        side_effect=send_video):
                rtc_stream._stream_worker(source)
            self.assertEqual(
                sends_after_resume,
                1,
                "恢复订阅后必须先恢复实时节奏，不能补发空闲期帧",
            )
        finally:
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            if old_stop_requested:
                rtc_stream._stop_event.set()
            else:
                rtc_stream._stop_event.clear()

    def test_stream_worker_keeps_media_content_aligned_when_audio_subscribes_late(self):
        old_connections = rtc_stream._connections.copy()
        old_stop_requested = rtc_stream._stop_event.is_set()
        old_key_requested = rtc_stream._force_key_frame.is_set()
        source = mock.Mock()
        source.has_video.return_value = True
        source.get_audio_format.return_value = mock.Mock(media=1, flags=0)
        source.get_video_format.return_value = mock.Mock(media=1)
        audio_index = 0
        video_index = 0
        last_video_sent = -1
        first_audio_sent = None
        now_ms = 0.0

        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
            pending=False, send_video_subscribed=True)
        rtc_stream._stop_event.clear()
        rtc_stream._force_key_frame.clear()

        def next_audio_packet():
            nonlocal audio_index
            payload = bytes([audio_index])
            audio_index += 1
            return payload, 40.0

        def next_video(force_key=False):
            nonlocal video_index
            payload = bytes([video_index])
            is_key = video_index == 0 or (force_key and video_index == 0)
            video_index += 1
            return payload, is_key

        def current_time():
            return int(now_ms)

        def sleep(seconds):
            nonlocal now_ms
            now_ms += seconds * 1000.0

        def send_video(_hconn, _frame, data):
            nonlocal last_video_sent
            last_video_sent = bytes(data)[0]
            if last_video_sent == 4:
                with rtc_stream._state_lock:
                    rtc_stream._connections[0x101].send_audio_subscribed = True
            return 0

        def send_audio(_hconn, _frame, data):
            nonlocal first_audio_sent
            first_audio_sent = bytes(data)[0]
            rtc_stream._stop_event.set()
            return 0

        source.next_audio_packet.side_effect = next_audio_packet
        source.next_video.side_effect = next_video
        try:
            with mock.patch.object(
                    rtc_stream, "_now_ms", side_effect=current_time), \
                    mock.patch.object(
                        rtc_stream.time, "sleep", side_effect=sleep), \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcSendVideoStream",
                        side_effect=send_video), \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcSendAudioStream",
                        side_effect=send_audio):
                rtc_stream._stream_worker(source)

            self.assertIsNotNone(first_audio_sent)
            audio_content_ms = first_audio_sent * 40.0
            video_content_ms = last_video_sent * rtc_stream.VIDEO_FRAME_MS
            self.assertLessEqual(
                abs(audio_content_ms - video_content_ms),
                rtc_stream.VIDEO_FRAME_MS,
                "晚订阅音频必须从当前视频内容附近开始，不能从旧位置播放",
            )
        finally:
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            if old_stop_requested:
                rtc_stream._stop_event.set()
            else:
                rtc_stream._stop_event.clear()
            if old_key_requested:
                rtc_stream._force_key_frame.set()
            else:
                rtc_stream._force_key_frame.clear()

    def test_stream_busy_viewer_waits_for_keyframe_without_seek_loop(self):
        old_connections = rtc_stream._connections.copy()
        old_stop_requested = rtc_stream._stop_event.is_set()
        old_key_requested = rtc_stream._force_key_frame.is_set()
        source = mock.Mock()
        source.has_video.return_value = True
        source.next_audio_packet.return_value = (b"audio", 40)
        source.get_audio_format.return_value = mock.Mock(media=1, flags=0)
        source.get_video_format.return_value = mock.Mock(media=1)
        force_key_values = []
        now_ms = -100

        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
            pending=False,
            send_video_subscribed=True,
            video_waiting_for_key_frame=True,
        )
        rtc_stream._stop_event.clear()
        rtc_stream._force_key_frame.clear()

        def current_time():
            nonlocal now_ms
            now_ms += 100
            return now_ms

        def next_video(force_key=False):
            force_key_values.append(force_key)
            if len(force_key_values) == 2:
                rtc_stream._stop_event.set()
            return b"frame", force_key

        source.next_video.side_effect = next_video
        try:
            with mock.patch.object(
                    rtc_stream, "_now_ms", side_effect=current_time), \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcSendVideoStream",
                        return_value=rtc_stream.TIRTC_E_BUSY):
                rtc_stream._stream_worker(source)
            self.assertEqual(force_key_values, [True, False])
        finally:
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            if old_stop_requested:
                rtc_stream._stop_event.set()
            else:
                rtc_stream._stop_event.clear()
            if old_key_requested:
                rtc_stream._force_key_frame.set()
            else:
                rtc_stream._force_key_frame.clear()

    def test_stream_abnormal_disconnect_flushes_last_talkback_playback(self):
        callbacks = rtc_stream.runtime_callbacks()
        old_connections = rtc_stream._connections.copy()
        old_speaker = rtc_stream._talkback_speaker
        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
            pending=False, talkback_subscribed=True)
        speaker = mock.Mock()
        rtc_stream._talkback_speaker = speaker

        def run_deferred(callback, *args, **_kwargs):
            callback(*args)
            return True

        try:
            with mock.patch.object(
                    rtc_stream._callback_guard, "defer",
                    side_effect=run_deferred):
                callbacks.on_disconnected(0x101)
            speaker.flush.assert_called_once_with()
        finally:
            rtc_stream._talkback_speaker = old_speaker
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)

    def test_stream_disconnect_keeps_talkback_for_surviving_viewer(self):
        callbacks = rtc_stream.runtime_callbacks()
        old_connections = rtc_stream._connections.copy()
        old_speaker = rtc_stream._talkback_speaker
        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
            pending=False, talkback_subscribed=True)
        rtc_stream._connections[0x202] = rtc_stream._StreamConnection(
            pending=False, talkback_subscribed=True)
        speaker = mock.Mock()
        rtc_stream._talkback_speaker = speaker

        def run_deferred(callback, *args, **_kwargs):
            callback(*args)
            return True

        try:
            with mock.patch.object(
                    rtc_stream._callback_guard, "defer",
                    side_effect=run_deferred):
                callbacks.on_disconnected(0x101)
            speaker.flush.assert_not_called()
            self.assertIn(0x202, rtc_stream._connections)
        finally:
            rtc_stream._talkback_speaker = old_speaker
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)

    def test_stream_worker_does_not_send_to_viewer_removed_after_snapshot(self):
        old_connections = rtc_stream._connections.copy()
        old_stop_requested = rtc_stream._stop_event.is_set()
        source = mock.Mock()
        source.has_video.return_value = False
        source.get_audio_format.return_value = mock.Mock(media=1, flags=0)
        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
            pending=False, send_audio_subscribed=True)
        rtc_stream._stop_event.clear()

        def next_audio_packet():
            with rtc_stream._state_lock:
                rtc_stream._connections.pop(0x101, None)
            rtc_stream._stop_event.set()
            return b"audio", 40

        source.next_audio_packet.side_effect = next_audio_packet
        try:
            with mock.patch.object(
                    rtc_stream.sdk, "TiRtcSendAudioStream") as send_audio:
                rtc_stream._stream_worker(source)
            send_audio.assert_not_called()
        finally:
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            if old_stop_requested:
                rtc_stream._stop_event.set()
            else:
                rtc_stream._stop_event.clear()

    def test_stream_fatal_send_flushes_last_talkback_playback(self):
        old_connections = rtc_stream._connections.copy()
        old_stop_requested = rtc_stream._stop_event.is_set()
        old_speaker = rtc_stream._talkback_speaker
        source = mock.Mock()
        source.has_video.return_value = False
        source.get_audio_format.return_value = mock.Mock(media=1, flags=0)
        source.next_audio_packet.return_value = (b"audio", 40)
        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = rtc_stream._StreamConnection(
            pending=False,
            send_audio_subscribed=True,
            talkback_subscribed=True,
        )
        rtc_stream._stop_event.clear()
        speaker = mock.Mock()
        rtc_stream._talkback_speaker = speaker

        def run_deferred(callback, *args, **_kwargs):
            callback(*args)
            return True

        def disconnect(_hconn):
            rtc_stream._stop_event.set()
            return 0

        try:
            with mock.patch.object(
                    rtc_stream.sdk, "TiRtcSendAudioStream",
                    return_value=next(iter(rtc_stream.CONN_FATAL_ERRORS))), \
                    mock.patch.object(
                        rtc_stream.sdk, "TiRtcDisconnect",
                        side_effect=disconnect), \
                    mock.patch.object(
                        rtc_stream._callback_guard, "defer",
                        side_effect=run_deferred):
                rtc_stream._stream_worker(source)
            speaker.flush.assert_called_once_with()
            self.assertNotIn(0x101, rtc_stream._connections)
        finally:
            rtc_stream._talkback_speaker = old_speaker
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)
            if old_stop_requested:
                rtc_stream._stop_event.set()
            else:
                rtc_stream._stop_event.clear()

    def test_stream_rejects_viewer_above_bounded_capacity(self):
        callbacks = rtc_stream.runtime_callbacks()
        old_active = rtc_stream._service_active
        old_connections = rtc_stream._connections.copy()
        rtc_stream._service_active = True
        rtc_stream._connections.clear()
        for index in range(rtc_stream.MAX_STREAM_CONNECTIONS):
            rtc_stream._connections[0x100 + index] = (
                rtc_stream._StreamConnection(pending=False)
            )
        try:
            with mock.patch.object(
                    rtc_stream._callback_guard, "defer", return_value=True
            ) as defer:
                callbacks.on_conn_accepted(0x999)
            self.assertNotIn(0x999, rtc_stream._connections)
            defer.assert_called_once()
        finally:
            rtc_stream._service_active = old_active
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)

    def test_stream_duplicate_accept_keeps_existing_viewer(self):
        callbacks = rtc_stream.runtime_callbacks()
        old_active = rtc_stream._service_active
        old_connections = rtc_stream._connections.copy()
        existing = rtc_stream._StreamConnection(
            pending=False, send_video_subscribed=True)
        rtc_stream._service_active = True
        rtc_stream._connections.clear()
        rtc_stream._connections[0x101] = existing
        try:
            with mock.patch.object(
                    rtc_stream._callback_guard, "defer") as defer:
                callbacks.on_conn_accepted(0x101)
            defer.assert_not_called()
            self.assertIs(existing, rtc_stream._connections[0x101])
            self.assertTrue(existing.send_video_subscribed)
        finally:
            rtc_stream._service_active = old_active
            rtc_stream._connections.clear()
            rtc_stream._connections.update(old_connections)

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
