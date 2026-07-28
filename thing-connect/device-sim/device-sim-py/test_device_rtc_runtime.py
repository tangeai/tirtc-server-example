import unittest
from unittest import mock
import os
import tempfile

os.environ.setdefault("TIRTC_SDK_VERSION", "2.2.1")
from device_rtc_runtime import DeviceRtcRuntime, RuntimeConfig


class DeviceRtcRuntimeTests(unittest.TestCase):
    def test_hardware_audio_keeps_ai_wire_format_on_alaw(self):
        config = RuntimeConfig(
            device_id="dev-1",
            device_key="key-1",
            client_id="client-1",
            mqtt_token="mqtt-token",
            tirtc_endpoint="https://rtc.example.com",
            voip_server="https://voip.example.com",
            ai_server="https://ai.example.com",
            call_server="https://call.example.com",
            up_audio_file="audio.g711a",
            up_video_file="video.h264",
            down_media_dir="./received",
            hardware_audio=True,
        )

        self.assertEqual(
            DeviceRtcRuntime._resolve_ai_defaults(config),
            ("audio.g711a", "alaw_8khz", "alaw_8khz"),
        )

    def test_start_primes_voip_profile_before_stream(self):
        stream = mock.Mock()
        voip_module = mock.Mock()
        ai_module = mock.Mock()
        call_module = mock.Mock()
        voip_module.report_profile.return_value = [{"wx_open_id": "openid-1"}]

        runtime = DeviceRtcRuntime(
            RuntimeConfig(
                device_id="dev-1",
                device_key="key-1",
                client_id="client-1",
                mqtt_token="mqtt-token",
                tirtc_endpoint="https://rtc.example.com",
                voip_server="https://voip.example.com",
                ai_server="https://ai.example.com",
                call_server="https://call.example.com",
                up_audio_file="audio.g711a",
                up_video_file="video.h264",
                down_media_dir="./received",
            ),
            stream, voip_module, ai_module, call_module,
        )
        runtime.voip = mock.Mock(wraps=runtime.voip)

        runtime.start()

        voip_module.report_profile.assert_called_once_with(
            "https://voip.example.com", "mqtt-token"
        )
        runtime.voip.replace_callers.assert_called_once_with([{"wx_open_id": "openid-1"}])
        stream.start.assert_called_once()

    def test_stream_media_factory_supports_audio_only_config(self):
        stream = mock.Mock()
        voip_module = mock.Mock()
        ai_module = mock.Mock()
        call_module = mock.Mock()
        voip_module.report_profile.return_value = []

        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            with open(audio_path, "wb") as target:
                target.write(b"\xd5" * 320)

            runtime = DeviceRtcRuntime(
                RuntimeConfig(
                    device_id="dev-1",
                    device_key="key-1",
                    client_id="client-1",
                    mqtt_token="mqtt-token",
                    tirtc_endpoint="https://rtc.example.com",
                    voip_server="https://voip.example.com",
                    ai_server="https://ai.example.com",
                    call_server="https://call.example.com",
                    up_audio_file=audio_path,
                    up_video_file="",
                    down_media_dir=root,
                ),
                stream, voip_module, ai_module, call_module,
            )

            runtime.start()

            media_factory = stream.start.call_args.args[2]
            source = media_factory()
            try:
                self.assertFalse(source.has_video())
                packet = source.next_audio_packet()
                self.assertIsNotNone(packet)
            finally:
                source.close()


if __name__ == "__main__":
    unittest.main()
