#!/usr/bin/env python3

import ctypes
import unittest
from unittest import mock

import tirtc_runtime
from tirtc_runtime import ServiceKind, TiRtcRuntime
from tirtc_sdk import (
    OnAudioCB,
    OnCmdCB,
    OnConnAcceptCB,
    OnDisconnCB,
    TIRTCCALLBACKS,
    TIRTCFRAMEINFO,
)


def _handler(counts, *, accepts=False):
    callbacks = TIRTCCALLBACKS()

    if accepts:
        def accepted(_hconn):
            counts["accepted"] += 1
        callbacks.on_conn_accepted = OnConnAcceptCB(accepted)

    def disconnected(_hconn):
        counts["disconnected"] += 1

    def audio(_hconn, _frame, _data):
        counts["audio"] += 1

    def command(_hconn, _command, _data, _length):
        counts["command"] += 1

    callbacks.on_disconnected = OnDisconnCB(disconnected)
    callbacks.on_audio = OnAudioCB(audio)
    callbacks.on_command = OnCmdCB(command)
    callbacks._cb_refs = [
        callbacks.on_conn_accepted,
        callbacks.on_disconnected,
        callbacks.on_audio,
        callbacks.on_command,
    ]
    return callbacks


class TiRtcRuntimeTests(unittest.TestCase):
    def test_binding_keeps_sdk_version_compatibility_internal(self):
        with mock.patch.object(
                tirtc_runtime.sdk, "HAS_CLIENT_ID_OPT", False), \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcSetOption") as set_option:
            self.assertEqual(
                0, tirtc_runtime.sdk.set_client_id(b"client-1"))
            self.assertEqual(
                b"device-1,secret",
                tirtc_runtime.sdk.device_id_for_start(
                    "device-1", "secret"),
            )
        set_option.assert_not_called()

    def test_generation_dispatch_drops_every_stale_callback(self):
        runtime = TiRtcRuntime()
        stream_counts = {
            "accepted": 0, "disconnected": 0, "audio": 0, "command": 0}
        voip_counts = {
            "accepted": 0, "disconnected": 0, "audio": 0, "command": 0}
        runtime.register_service(
            ServiceKind.STREAM,
            _handler(stream_counts, accepts=True),
            accepts_inbound=True,
        )
        runtime.register_service(
            ServiceKind.VOIP,
            _handler(voip_counts),
            accepts_inbound=False,
        )
        runtime._test_mark_started()

        frame = TIRTCFRAMEINFO()
        payload = ctypes.c_uint8(0xD5)
        stream_conn = ctypes.c_void_p(0x101)
        stream_generation = runtime.activate(ServiceKind.STREAM)
        runtime._on_conn_accepted(stream_conn)
        runtime._on_audio(
            stream_conn, ctypes.byref(frame), ctypes.byref(payload))
        runtime._on_command(
            stream_conn, 0x2000, ctypes.byref(payload), 1)
        self.assertEqual(stream_counts["accepted"], 1)
        self.assertEqual(stream_counts["audio"], 1)
        self.assertEqual(stream_counts["command"], 1)

        self.assertTrue(runtime.deactivate(
            ServiceKind.STREAM, stream_generation))
        runtime._on_audio(
            stream_conn, ctypes.byref(frame), ctypes.byref(payload))
        runtime._on_command(
            stream_conn, 0x2000, ctypes.byref(payload), 1)
        runtime._on_disconnected(stream_conn)
        self.assertEqual(stream_counts["audio"], 1)
        self.assertEqual(stream_counts["command"], 1)
        self.assertEqual(stream_counts["disconnected"], 0)

        voip_generation = runtime.activate(ServiceKind.VOIP)
        voip_conn = ctypes.c_void_p(0x202)
        self.assertFalse(runtime.bind_active_connection(
            ServiceKind.STREAM, voip_conn))
        self.assertTrue(runtime.bind_active_connection(
            ServiceKind.VOIP, voip_conn))
        runtime._on_audio(
            voip_conn, ctypes.byref(frame), ctypes.byref(payload))
        self.assertEqual(voip_counts["audio"], 1)
        self.assertTrue(runtime.deactivate(
            ServiceKind.VOIP, voip_generation))

        next_generation = runtime.activate(ServiceKind.VOIP)
        self.assertGreater(next_generation, voip_generation)
        runtime._on_audio(
            voip_conn, ctypes.byref(frame), ctypes.byref(payload))
        self.assertEqual(voip_counts["audio"], 1)
        self.assertTrue(runtime.deactivate(
            ServiceKind.VOIP, next_generation))

    def test_many_business_switches_use_one_sdk_lifecycle(self):
        runtime = TiRtcRuntime()
        runtime.register_service(
            ServiceKind.STREAM,
            _handler({}, accepts=True),
            accepts_inbound=True,
        )
        runtime.register_service(
            ServiceKind.AI,
            _handler({}),
            accepts_inbound=False,
        )

        def start_sdk(_device_id, _callbacks):
            runtime._on_event(
                tirtc_runtime.sdk.TIRTC_EVENT_SYS_STARTED, None, 0)
            return 0

        def stop_sdk():
            runtime._on_event(
                tirtc_runtime.sdk.TIRTC_EVENT_SYS_STOPPED, None, 0)
            return 0

        with mock.patch.object(
                tirtc_runtime.sdk, "TiRtcSetOption", return_value=0), \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcInit", return_value=0) as init, \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcStart",
                    side_effect=start_sdk) as start, \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcStop",
                    side_effect=stop_sdk) as stop, \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcUninit") as uninit, \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcLogConfig"), \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcLogSetLevel"):
            runtime.set_log_level("error")
            runtime.start("device-1", "secret", "client-1")
            for _ in range(100):
                stream_generation = runtime.activate(ServiceKind.STREAM)
                self.assertTrue(runtime.deactivate(
                    ServiceKind.STREAM, stream_generation))
                ai_generation = runtime.activate(ServiceKind.AI)
                self.assertTrue(runtime.deactivate(
                    ServiceKind.AI, ai_generation))
            runtime.stop()

        init.assert_called_once_with()
        start.assert_called_once()
        stop.assert_called_once_with()
        uninit.assert_called_once_with()

    def test_failed_start_cleans_up_and_runtime_can_start_again(self):
        runtime = TiRtcRuntime()
        runtime.register_service(
            ServiceKind.STREAM,
            _handler({}, accepts=True),
            accepts_inbound=True,
        )

        start_attempt = 0

        def start_sdk(_device_id, _callbacks):
            nonlocal start_attempt
            start_attempt += 1
            if start_attempt == 1:
                return -40003
            runtime._on_event(
                tirtc_runtime.sdk.TIRTC_EVENT_SYS_STARTED, None, 0)
            return 0

        def stop_sdk():
            runtime._on_event(
                tirtc_runtime.sdk.TIRTC_EVENT_SYS_STOPPED, None, 0)
            return 0

        with mock.patch.object(
                tirtc_runtime.sdk, "TiRtcSetOption", return_value=0), \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcInit", return_value=0), \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcStart",
                    side_effect=start_sdk), \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcStop",
                    side_effect=stop_sdk) as stop, \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcUninit") as uninit, \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcLogConfig"), \
                mock.patch.object(
                    tirtc_runtime.sdk, "TiRtcLogSetLevel"):
            runtime.set_log_level("error")
            with self.assertRaises(RuntimeError):
                runtime.start("device-1", "secret", "client-1")
            self.assertFalse(runtime.started)

            runtime.start("device-1", "secret", "client-1")
            runtime.stop()

        stop.assert_called_once_with()
        self.assertEqual(2, uninit.call_count)


if __name__ == "__main__":
    unittest.main()
