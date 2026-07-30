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
        self._expected_room_id = rtc_call._expected_room_id

    def tearDown(self):
        rtc_call._callback_guard.close()
        rtc_call._session_call_type = self._call_type
        rtc_call._service_active = self._service_active
        rtc_call._session_state = self._session_state
        rtc_call._active_hconn = self._active_hconn
        rtc_call._expected_room_id = self._expected_room_id
        rtc_call._media.reset_session()

    def test_audio_call_unsubscribes_remote_video(self):
        with mock.patch.object(rtc_call._media, "prepare_session"), \
                mock.patch.object(
                    rtc_call.sdk, "TiRtcUnsubscribeVideo",
                    return_value=0) as unsubscribe:
            rtc_call.set_call_type("audio")
            rtc_call._apply_video_downlink_policy(0x1234)

        unsubscribe.assert_called_once()
        self.assertEqual(
            unsubscribe.call_args.args[1], rtc_call.sdk.VIDEO_STREAM_ID)

    def test_video_call_keeps_remote_video(self):
        with mock.patch.object(rtc_call._media, "prepare_session"), \
                mock.patch.object(
                    rtc_call.sdk, "TiRtcUnsubscribeVideo") as unsubscribe:
            rtc_call.set_call_type("video")
            rtc_call._apply_video_downlink_policy(0x1234)

        unsubscribe.assert_not_called()

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
                mock.patch.object(rtc_call, "_apply_video_downlink_policy"):
            rtc_call._accept_inbound_connection_after_callback(0x1234)
            self.assertEqual("CONNECTING", rtc_call.get_state())
            media_start.assert_not_called()

            rtc_call._process_command_after_callback(
                0x1234, b'{"room_id":"room-1"}')
            self.assertEqual("IN_CALL", rtc_call.get_state())
            media_start.assert_called_once_with()


if __name__ == "__main__":
    unittest.main()
