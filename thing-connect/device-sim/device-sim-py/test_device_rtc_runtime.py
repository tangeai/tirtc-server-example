import unittest
from unittest import mock
import os
import tempfile
import threading
import time

os.environ.setdefault("TIRTC_SDK_VERSION", "2.3.0")
from device_rtc_runtime import DeviceRtcRuntime, RuntimeConfig


class _FakeSdkRuntime:
    def __init__(self):
        self.registered = {}
        self.started = False
        self.stopped = False
        self.start_calls = 0
        self.stop_calls = 0
        self.generation = 0
        self.active = None

    def set_log_level(self, level):
        self.log_level = level

    def register_service(
        self, service, callbacks, *, accepts_inbound, callback_guard
    ):
        self.registered[service] = (
            callbacks, accepts_inbound, callback_guard)

    def start(self, device_id, device_key, client_id, endpoint):
        self.start_args = (device_id, device_key, client_id, endpoint)
        self.start_calls += 1
        self.started = True
        for _, _, guard in self.registered.values():
            start = getattr(guard, "start", None)
            if callable(start):
                start()

    def stop(self):
        self.stop_calls += 1
        self.stopped = True
        self.started = False
        self.active = None
        closed = set()
        for _, _, guard in self.registered.values():
            if id(guard) in closed:
                continue
            closed.add(id(guard))
            close = getattr(guard, "close", None)
            if callable(close):
                close()

    def activate(self, service):
        if self.active is not None:
            raise RuntimeError("service already active")
        self.generation += 1
        self.active = (service, self.generation)
        return self.generation

    def deactivate(self, service, generation):
        if self.active == (service, generation):
            self.active = None
            return True
        return False


def _module_mock():
    module = mock.Mock()
    module.runtime_callbacks.return_value = object()
    module.callback_guard.return_value = object()
    return module


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
        stream = _module_mock()
        voip_module = _module_mock()
        ai_module = _module_mock()
        call_module = _module_mock()
        sdk_runtime = _FakeSdkRuntime()
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
            sdk_runtime=sdk_runtime,
        )
        runtime.voip = mock.Mock(wraps=runtime.voip)

        runtime.start()

        voip_module.report_profile.assert_called_once_with(
            "https://voip.example.com", "mqtt-token"
        )
        runtime.voip.replace_callers.assert_called_once_with([{"wx_open_id": "openid-1"}])
        stream.start_service.assert_called_once()
        self.assertTrue(sdk_runtime.started)
        runtime.shutdown()
        self.assertTrue(sdk_runtime.stopped)
        runtime.shutdown()
        self.assertEqual(sdk_runtime.stop_calls, 1)

    def test_start_failure_closes_process_runtime_once(self):
        stream = _module_mock()
        voip_module = _module_mock()
        ai_module = _module_mock()
        call_module = _module_mock()
        sdk_runtime = _FakeSdkRuntime()
        voip_module.report_profile.side_effect = RuntimeError(
            "profile unavailable")
        runtime = DeviceRtcRuntime(
            RuntimeConfig(
                device_id="dev-1",
                device_key="key-1",
                client_id="client-1",
                mqtt_token="mqtt-token",
                tirtc_endpoint="http://rtc.example.com",
                voip_server="https://voip.example.com",
                ai_server="https://ai.example.com",
                call_server="https://call.example.com",
                up_audio_file="audio.g711a",
                up_video_file="",
                down_media_dir="./received",
            ),
            stream, voip_module, ai_module, call_module,
            sdk_runtime=sdk_runtime,
        )

        with self.assertRaisesRegex(RuntimeError, "profile unavailable"):
            runtime.start()

        self.assertEqual(sdk_runtime.stop_calls, 1)
        runtime.shutdown()
        self.assertEqual(sdk_runtime.stop_calls, 1)

    def test_shutdown_stops_sdk_even_when_business_stop_fails(self):
        stream = _module_mock()
        voip_module = _module_mock()
        ai_module = _module_mock()
        call_module = _module_mock()
        sdk_runtime = _FakeSdkRuntime()
        voip_module.report_profile.return_value = []
        runtime = DeviceRtcRuntime(
            RuntimeConfig(
                device_id="dev-1",
                device_key="key-1",
                client_id="client-1",
                mqtt_token="mqtt-token",
                tirtc_endpoint="http://rtc.example.com",
                voip_server="https://voip.example.com",
                ai_server="https://ai.example.com",
                call_server="https://call.example.com",
                up_audio_file="audio.g711a",
                up_video_file="",
                down_media_dir="./received",
            ),
            stream, voip_module, ai_module, call_module,
            sdk_runtime=sdk_runtime,
        )
        runtime.start()
        stream.stop_service.side_effect = RuntimeError("stream stop failed")

        with self.assertRaisesRegex(RuntimeError, "stream stop failed"):
            runtime.shutdown()

        self.assertEqual(sdk_runtime.stop_calls, 1)
        self.assertFalse(runtime.arbiter._worker.is_alive())

    def test_stream_media_factory_supports_audio_only_config(self):
        stream = _module_mock()
        voip_module = _module_mock()
        ai_module = _module_mock()
        call_module = _module_mock()
        sdk_runtime = _FakeSdkRuntime()
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
                sdk_runtime=sdk_runtime,
            )

            ai_module.configure_receive_dir.assert_called_once_with(root)
            self.assertFalse(runtime.terminal._video_capable)
            self.assertFalse(runtime.voip._video_capable)
            self.assertFalse(runtime.call._video_capable)
            runtime.start()

            media_factory = stream.start_service.call_args.args[0]
            source = media_factory()
            try:
                self.assertFalse(source.has_video())
                packet = source.next_audio_packet()
                self.assertIsNotNone(packet)
            finally:
                source.close()
            runtime.shutdown()

    def test_real_modules_switch_repeatedly_without_sdk_restart_or_thread_leak(self):
        import rtc_ai
        import rtc_call
        import rtc_stream
        import rtc_voip
        from session_coordinator import SessionKind
        from tirtc_runtime import ServiceKind

        baseline_threads = {
            thread.ident for thread in threading.enumerate()
            if thread.ident is not None
        }
        sdk_runtime = _FakeSdkRuntime()
        runtime = None
        with tempfile.TemporaryDirectory() as root:
            audio_path = os.path.join(root, "audio.g711a")
            with open(audio_path, "wb") as target:
                target.write(b"\xd5" * 320)
            config = RuntimeConfig(
                device_id="dev-1",
                device_key="key-1",
                client_id="client-1",
                mqtt_token="mqtt-token",
                tirtc_endpoint="http://rtc.example.com",
                voip_server="https://voip.example.com",
                ai_server="https://ai.example.com",
                call_server="https://call.example.com",
                up_audio_file=audio_path,
                up_video_file="",
                down_media_dir=root,
                log_level="error",
            )

            try:
                with mock.patch.object(
                    rtc_voip, "report_profile", return_value=[]
                ):
                    runtime = DeviceRtcRuntime(
                        config,
                        rtc_stream,
                        rtc_voip,
                        rtc_ai,
                        rtc_call,
                        sdk_runtime=sdk_runtime,
                    )
                    runtime.start()

                    service_by_session = {
                        SessionKind.VOIP: ServiceKind.VOIP,
                        SessionKind.AI: ServiceKind.AI,
                        SessionKind.CALL: ServiceKind.CALL,
                    }
                    for _ in range(20):
                        for kind, service in service_by_session.items():
                            lease = runtime._begin(kind, lambda: None)
                            self.assertEqual(runtime.coordinator.current, kind)
                            self.assertEqual(
                                sdk_runtime.active[0], service)
                            runtime.arbiter.finish(kind, lease.generation)
                            self.assertEqual(
                                runtime.coordinator.current,
                                SessionKind.STREAM,
                            )
                            self.assertEqual(
                                sdk_runtime.active[0],
                                ServiceKind.STREAM,
                            )

                    self.assertEqual(sdk_runtime.start_calls, 1)
                    self.assertEqual(sdk_runtime.stop_calls, 0)
            finally:
                if runtime is not None:
                    runtime.shutdown()
                rtc_ai.set_session_end_callback(None)
                rtc_call.set_session_end_callback(None)

        self.assertEqual(sdk_runtime.start_calls, 1)
        self.assertEqual(sdk_runtime.stop_calls, 1)
        time.sleep(0.05)
        leaked = [
            thread.name for thread in threading.enumerate()
            if (
                thread.ident is not None
                and thread.ident not in baseline_threads
                and thread.is_alive()
            )
        ]
        self.assertEqual(leaked, [])


if __name__ == "__main__":
    unittest.main()
