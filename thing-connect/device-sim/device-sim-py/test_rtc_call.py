#!/usr/bin/env python3

import unittest
from unittest import mock

import rtc_call


class RtcCallVideoPolicyTests(unittest.TestCase):
    def setUp(self):
        self._call_type = rtc_call._session_call_type

    def tearDown(self):
        rtc_call._session_call_type = self._call_type
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


if __name__ == "__main__":
    unittest.main()
