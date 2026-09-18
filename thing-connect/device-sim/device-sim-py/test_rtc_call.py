#!/usr/bin/env python3

import unittest
from unittest import mock

import rtc_call


class RtcCallVideoPolicyTests(unittest.TestCase):
    def setUp(self):
        rtc_call._callback_guard.start()
        self._call_type = rtc_call._session_call_type
        self._service_active = rtc_call._service_active
        self._session_state = rtc_call._session_state
        self._active_hconn = rtc_call._active_hconn
        self._pending_hconn = rtc_call._pending_hconn
        self._expected_room_id = rtc_call._expected_room_id
        self._local_audio_subscribed = rtc_call._local_audio_subscribed
        self._local_video_subscribed = rtc_call._local_video_subscribed
        self._pending_send_audio = rtc_call._pending_send_audio_subscribed
        self._pending_send_video = rtc_call._pending_send_video_subscribed

    def tearDown(self):
        rtc_call._callback_guard.close()
        rtc_call._session_call_type = self._call_type
        rtc_call._service_active = self._service_active
        rtc_call._session_state = self._session_state
        rtc_call._active_hconn = self._active_hconn
        rtc_call._pending_hconn = self._pending_hconn
        rtc_call._expected_room_id = self._expected_room_id
        rtc_call._local_audio_subscribed = self._local_audio_subscribed
        rtc_call._local_video_subscribed = self._local_video_subscribed
        rtc_call._pending_send_audio_subscribed = self._pending_send_audio
        rtc_call._pending_send_video_subscribed = self._pending_send_video
        rtc_call._media.reset_session()

    def test_audio_call_subscribes_only_remote_audio(self):
        with mock.patch.object(rtc_call._media, "prepare_session"), \
                mock.patch.object(
                    rtc_call.sdk, "TiRtcSubscribeAudio",
                    return_value=1) as subscribe_audio, \
                mock.patch.object(
                    rtc_call.sdk, "TiRtcSubscribeVideo") as subscribe_video:
            rtc_call.set_call_type("audio")
            rtc_call._active_hconn = 0x1234
            self.assertTrue(rtc_call._subscribe_peer_media(0x1234))

        subscribe_audio.assert_called_once()
        subscribe_video.assert_not_called()

    def test_video_call_subscribes_remote_audio_and_video(self):
        with mock.patch.object(rtc_call._media, "prepare_session"), \
                mock.patch.object(
                    rtc_call.sdk, "TiRtcSubscribeAudio",
                    return_value=0) as subscribe_audio, \
                mock.patch.object(
                    rtc_call.sdk, "TiRtcSubscribeVideo",
                    return_value=0) as subscribe_video:
            rtc_call.set_call_type("video")
            rtc_call._active_hconn = 0x1234
            self.assertTrue(rtc_call._subscribe_peer_media(0x1234))

        subscribe_audio.assert_called_once()
        subscribe_video.assert_called_once()

    def test_peer_unsubscribe_stops_only_video_policy(self):
        callbacks = rtc_call._build_callbacks()
        old_hconn = rtc_call._active_hconn
        try:
            rtc_call._active_hconn = 0x1234
            with mock.patch.object(
                    rtc_call._media, "unsubscribe_video",
                    return_value=True) as unsubscribe:
                callbacks.on_unsubscribe_video(0x1234, 11)
            unsubscribe.assert_called_once_with(11)
        finally:
            rtc_call._active_hconn = old_hconn

    def test_inbound_call_becomes_in_call_only_after_2000(self):
        rtc_call._service_active = True
        rtc_call._session_state = "IDLE"
        rtc_call._active_hconn = None
        rtc_call._expected_room_id = "room-1"

        with mock.patch.object(rtc_call._media, "set_hconn"), \
                mock.patch.object(rtc_call._media, "start") as media_start, \
                mock.patch.object(rtc_call.sdk, "TiRtcSubscribeAudio", create=True, return_value=0) as sub_audio, \
                mock.patch.object(rtc_call.sdk, "TiRtcSubscribeVideo", return_value=0) as sub_video:
            rtc_call.set_call_type("video")
            rtc_call._pending_hconn = 0x1234
            rtc_call._accept_inbound_connection_after_callback(0x1234)
            self.assertEqual("CONNECTING", rtc_call.get_state())
            media_start.assert_not_called()

            rtc_call._process_command_after_callback(
                0x1234, b'{"room_id":"room-1"}')
            self.assertEqual("IN_CALL", rtc_call.get_state())
            sub_audio.assert_called_once()
            sub_video.assert_called_once()
            media_start.assert_called_once_with()

    def test_early_peer_subscriptions_survive_deferred_inbound_activation(self):
        callbacks = rtc_call._build_callbacks()
        rtc_call._service_active = True
        rtc_call._active_hconn = None
        rtc_call._pending_hconn = 0x1234
        rtc_call._session_call_type = "video"
        rtc_call._pending_send_audio_subscribed = False
        rtc_call._pending_send_video_subscribed = False

        self.assertEqual(callbacks.on_subscribe_audio(0x1234, 10), 0)
        self.assertEqual(callbacks.on_subscribe_video(0x1234, 11), 0)

        with mock.patch.object(rtc_call._media, "set_hconn"), \
                mock.patch.object(
                    rtc_call._media, "subscribe_audio", return_value=True
                ) as audio, \
                mock.patch.object(
                    rtc_call._media, "subscribe_video", return_value=True
                ) as video:
            rtc_call._accept_inbound_connection_after_callback(0x1234)

        audio.assert_called_once_with(10)
        video.assert_called_once_with(11)

    def test_receive_callbacks_only_accept_locally_subscribed_streams(self):
        callbacks = rtc_call._build_callbacks()
        rtc_call._active_hconn = 0x1234
        rtc_call._local_audio_subscribed = False
        rtc_call._local_video_subscribed = False
        audio = rtc_call.TIRTCFRAMEINFO()
        audio.stream_id = rtc_call.sdk.AUDIO_STREAM_ID
        audio.length = 1
        video = rtc_call.TIRTCFRAMEINFO()
        video.stream_id = rtc_call.sdk.VIDEO_STREAM_ID
        video.length = 1
        payload = (rtc_call.ctypes.c_uint8 * 1)(1)

        with mock.patch.object(rtc_call._media, "on_audio_frame") as on_audio, \
                mock.patch.object(rtc_call._media, "on_video_frame") as on_video:
            callbacks.on_audio(0x1234, rtc_call.ctypes.byref(audio), payload)
            callbacks.on_video(0x1234, rtc_call.ctypes.byref(video), payload)
            on_audio.assert_not_called()
            on_video.assert_not_called()

            rtc_call._local_audio_subscribed = True
            rtc_call._local_video_subscribed = True
            callbacks.on_audio(0x1234, rtc_call.ctypes.byref(audio), payload)
            callbacks.on_video(0x1234, rtc_call.ctypes.byref(video), payload)
            on_audio.assert_called_once()
            on_video.assert_called_once()


if __name__ == "__main__":
    unittest.main()
