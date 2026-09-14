import os
import unittest
from types import SimpleNamespace
from unittest.mock import Mock, patch

import requests
import device_flow
from device_rtc_runtime import DeviceRtcRuntime, _stream_presentation_env


class DeviceMediaTests(unittest.TestCase):
    @patch.dict(os.environ, {
        "STREAM_ASPECT_RATIO": "", "STREAM_OBJECT_FIT": "",
        "STREAM_CAMERA_ROTATION": "", "STREAM_HOR_MIRROR": "", "STREAM_VERT_MIRROR": "",
    })
    def test_scene_profiles_follow_config_not_voip_defaults(self):
        runtime = object.__new__(DeviceRtcRuntime)
        runtime.config = SimpleNamespace(
            up_audio_format="pcm_s16le_16khz", down_audio_format="opus_16khz",
            up_video_file="camera.h265", up_video_format="h265", down_video_format="mjpeg")
        profiles = runtime.profiles()
        self.assertEqual(profiles["voip"]["up_video_mt"], "h265")
        self.assertEqual(profiles["voip"]["down_video_mt"], "mjpeg")
        self.assertEqual(profiles["stream"]["down_audio_mt"], ["alaw"])
        self.assertEqual(profiles["call"]["down_audio_mt"], ["opus"])
        self.assertEqual(profiles["call"]["up_video_mt"], ["h265"])
        self.assertEqual(profiles["call"]["down_video_mt"], ["mjpeg"])
        self.assertNotIn("camera_rotation", profiles["stream"])
        runtime.config.up_video_file = ""
        profiles = runtime.profiles()
        self.assertEqual(profiles["call"]["down_video_mt"], [])
        self.assertTrue(profiles["call"]["no_video"])
        self.assertTrue(profiles["voip"]["no_video"])

    @patch.dict(os.environ, {
        "STREAM_ASPECT_RATIO": "4:3", "STREAM_OBJECT_FIT": "cover",
        "STREAM_CAMERA_ROTATION": "90",
        "STREAM_HOR_MIRROR": "true", "STREAM_VERT_MIRROR": "false",
    })
    def test_stream_presentation_env_fields(self):
        fields = _stream_presentation_env()
        self.assertEqual(fields["aspect_ratio"], "4:3")
        self.assertEqual(fields["object_fit"], "cover")
        self.assertEqual(fields["camera_rotation"], 90)
        self.assertTrue(fields["hor_mirror"])
        self.assertFalse(fields["vert_mirror"])

    @patch.dict(os.environ, {
        "STREAM_ASPECT_RATIO": "1.7777777778", "STREAM_OBJECT_FIT": "",
        "STREAM_CAMERA_ROTATION": "45", "STREAM_HOR_MIRROR": "", "STREAM_VERT_MIRROR": "maybe",
    })
    def test_stream_presentation_env_invalid_values_are_dropped(self):
        fields = _stream_presentation_env()
        self.assertEqual(fields["aspect_ratio"], 1.7777777778)
        self.assertNotIn("object_fit", fields)
        self.assertNotIn("camera_rotation", fields)
        self.assertNotIn("hor_mirror", fields)
        self.assertNotIn("vert_mirror", fields)

    @patch("device_flow.time.sleep")
    @patch("device_flow.http_trace.request")
    def test_retry_reuses_payload_and_numeric_code(self, request, sleep):
        request.side_effect = [requests.Timeout(), Mock(status_code=503),
                               Mock(status_code=200, json=lambda: {"code": 200, "msg": "anything"})]
        profile = {"stream": {"hor_mirror": False, "camera_rotation": 0}}
        self.assertTrue(device_flow.report_profiles("https://device.example/", "test-token", profile))
        self.assertEqual(request.call_count, 3)
        self.assertEqual([c.args[0] for c in sleep.call_args_list], [1, 2])
        for call in request.call_args_list:
            self.assertEqual(call.args, ("POST", "https://device.example/v1/device/profile"))
            self.assertEqual(call.kwargs["json"], {"profiles": profile})
            self.assertEqual(call.kwargs["timeout"], 10)

    @patch("device_flow.time.sleep")
    @patch("device_flow.http_trace.request")
    def test_permanent_failure_and_retry_limit(self, request, sleep):
        request.return_value = Mock(status_code=410, json=lambda: {"code": 6006})
        self.assertFalse(device_flow.report_profiles("https://device.example", "token", {"call": {}}))
        request.assert_called_once()
        sleep.assert_not_called()
        request.reset_mock()
        request.side_effect = requests.Timeout()
        self.assertFalse(device_flow.report_profiles("https://device.example", "token", {"call": {}}))
        self.assertEqual(request.call_count, 3)


if __name__ == "__main__":
    unittest.main()
