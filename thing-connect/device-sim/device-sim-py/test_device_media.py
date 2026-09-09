import unittest
from types import SimpleNamespace
from unittest.mock import Mock, patch

import requests
import device_flow
from device_rtc_runtime import DeviceRtcRuntime


class DeviceMediaTests(unittest.TestCase):
    def test_scene_profiles_follow_config_not_voip_defaults(self):
        runtime = object.__new__(DeviceRtcRuntime)
        runtime.config = SimpleNamespace(
            up_audio_format="pcm_s16le_16khz", down_audio_format="opus_16khz",
            up_video_file="camera.h265", up_video_format="h265", down_video_format="mjpeg")
        profiles = runtime.media_profiles()
        self.assertNotIn("voip", profiles)
        self.assertEqual(profiles["stream"]["down_audio_mt"], ["alaw"])
        self.assertEqual(profiles["call"]["down_audio_mt"], ["opus"])
        self.assertEqual(profiles["call"]["up_video_mt"], ["h265"])
        self.assertEqual(profiles["call"]["down_video_mt"], ["mjpeg"])
        self.assertNotIn("camera_rotation", profiles["stream"])
        runtime.config.up_video_file = ""
        profiles = runtime.media_profiles()
        self.assertEqual(profiles["call"]["down_video_mt"], [])
        self.assertTrue(profiles["call"]["no_video"])

    @patch("device_flow.time.sleep")
    @patch("device_flow.http_trace.request")
    def test_retry_reuses_payload_and_numeric_code(self, request, sleep):
        request.side_effect = [requests.Timeout(), Mock(status_code=503),
                               Mock(status_code=200, json=lambda: {"code": 200, "msg": "anything"})]
        profile = {"stream": {"hor_mirror": False, "camera_rotation": 0}}
        self.assertTrue(device_flow.report_media_profiles("https://device.example/", "test-token", profile))
        self.assertEqual(request.call_count, 3)
        self.assertEqual([c.args[0] for c in sleep.call_args_list], [1, 2])
        for call in request.call_args_list:
            self.assertEqual(call.args, ("POST", "https://device.example/v1/device/profile"))
            self.assertEqual(call.kwargs["json"], {"media_profiles": profile})
            self.assertEqual(call.kwargs["timeout"], 10)

    @patch("device_flow.time.sleep")
    @patch("device_flow.http_trace.request")
    def test_permanent_failure_and_retry_limit(self, request, sleep):
        request.return_value = Mock(status_code=410, json=lambda: {"code": 6006})
        self.assertFalse(device_flow.report_media_profiles("https://device.example", "token", {"call": {}}))
        request.assert_called_once()
        sleep.assert_not_called()
        request.reset_mock()
        request.side_effect = requests.Timeout()
        self.assertFalse(device_flow.report_media_profiles("https://device.example", "token", {"call": {}}))
        self.assertEqual(request.call_count, 3)


if __name__ == "__main__":
    unittest.main()
