#!/usr/bin/env python3

import ctypes
import threading
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
    def test_stop_waits_for_connection_release_before_stopping_sdk(self):
        runtime = TiRtcRuntime()
        runtime._callback_guard.start()
        runtime._initialized = True
        runtime._start_submitted = True
        runtime._started = True
        runtime._connections[0x303] = tirtc_runtime._ConnectionOwner(
            ServiceKind.ROOM, 1)
        drained = threading.Event()
        errors = []

        def stop():
            try:
                runtime.stop()
            except Exception as exc:
                errors.append(exc)

        def stop_sdk():
            runtime._stopped_event.set()
            return 0

        with mock.patch.object(runtime._callback_guard, 'wait_for_all',
                               side_effect=drained.set), \
                mock.patch.object(tirtc_runtime.sdk, 'TiRtcStop',
                                  side_effect=stop_sdk) as native_stop, \
                mock.patch.object(tirtc_runtime.sdk, 'TiRtcUninit') as uninit:
            worker = threading.Thread(target=stop)
            worker.start()
            try:
                self.assertTrue(drained.wait(1))
                worker.join(.05)
                native_stop.assert_not_called()
                uninit.assert_not_called()
            finally:
                runtime._on_disconnected(0x303)
                worker.join(2)
            self.assertFalse(worker.is_alive())
            self.assertEqual(errors, [])
            native_stop.assert_called_once()
            uninit.assert_called_once()

    def test_inbound_during_stop_waits_for_release_before_uninit(self):
        runtime = TiRtcRuntime()
        runtime._callback_guard.start()
        runtime._started = runtime._initialized = runtime._start_submitted = True
        disconnect_started = threading.Event()
        errors = []

        def stop_sdk():
            runtime._on_conn_accepted(0x505)
            runtime._stopped_event.set()
            return 0

        def stop():
            try:
                runtime.stop()
            except Exception as exc:
                errors.append(exc)

        with mock.patch.object(tirtc_runtime.sdk, 'TiRtcStop', side_effect=stop_sdk), \
                mock.patch.object(tirtc_runtime.sdk, 'TiRtcDisconnect',
                                  side_effect=lambda _: disconnect_started.set()), \
                mock.patch.object(tirtc_runtime.sdk, 'TiRtcUninit') as uninit:
            worker = threading.Thread(target=stop)
            worker.start()
            try:
                self.assertTrue(disconnect_started.wait(1))
                worker.join(.05)
                uninit.assert_not_called()
            finally:
                runtime._on_disconnected(0x505)
                worker.join(2)
            self.assertFalse(worker.is_alive())
            self.assertEqual(errors, [])
            uninit.assert_called_once()

    def test_release_timeout_preserves_native_callback_resources(self):
        runtime = TiRtcRuntime()
        runtime._initialized = True
        runtime._start_submitted = True
        callbacks = runtime._build_callbacks()
        runtime._sdk_callbacks = callbacks
        with mock.patch.object(runtime._connections_changed, 'wait_for',
                               return_value=False), \
                mock.patch.object(tirtc_runtime.sdk, 'TiRtcStop') as stop, \
                mock.patch.object(tirtc_runtime.sdk, 'TiRtcUninit') as uninit:
            with self.assertRaisesRegex(RuntimeError, '连接释放超时'):
                runtime.stop()
            stop.assert_not_called()
            uninit.assert_not_called()
            self.assertIs(runtime._sdk_callbacks, callbacks)
            self.assertTrue(runtime._initialized)
        runtime._initialized = runtime._start_submitted = False
        runtime.stop()

    def test_stop_timeout_does_not_uninitialize_or_release_callbacks(self):
        runtime = TiRtcRuntime()
        runtime._initialized = True
        runtime._start_submitted = True
        callbacks = runtime._build_callbacks()
        runtime._sdk_callbacks = callbacks
        with mock.patch.object(runtime._stopped_event, 'wait', return_value=False), \
                mock.patch.object(tirtc_runtime.sdk, 'TiRtcStop', return_value=0), \
                mock.patch.object(tirtc_runtime.sdk, 'TiRtcUninit') as uninit:
            with self.assertRaisesRegex(RuntimeError, 'SYS_STOPPED 超时'):
                runtime.stop()
            uninit.assert_not_called()
            self.assertIs(runtime._sdk_callbacks, callbacks)
        runtime._initialized = runtime._start_submitted = False
        runtime.stop()

    def test_rejected_inbound_is_tracked_until_release_and_closed_once(self):
        runtime = TiRtcRuntime()
        runtime._callback_guard.start()
        runtime._test_mark_started()
        self.addCleanup(runtime.stop)
        with mock.patch.object(tirtc_runtime.sdk, 'TiRtcDisconnect') as disconnect:
            runtime._on_conn_accepted(0x404)
            runtime._on_conn_accepted(0x404)
            runtime._callback_guard.wait_for_all()
            self.assertEqual(runtime._rejected_connections, {0x404})
            disconnect.assert_called_once()
            runtime._on_disconnected(0x404)
            self.assertEqual(runtime._rejected_connections, set())

    def test_late_error_after_adapter_stop_never_disconnects_again(self):
        runtime = TiRtcRuntime()
        runtime._callback_guard.start()
        self.addCleanup(runtime.stop)
        counts = {'accepted': 0, 'disconnected': 0, 'audio': 0, 'command': 0}
        runtime.register_service(ServiceKind.STREAM, _handler(counts, accepts=True),
                                 accepts_inbound=True)
        runtime._test_mark_started()
        generation = runtime.activate(ServiceKind.STREAM)
        connection = ctypes.c_void_p(0x101)
        runtime._on_conn_accepted(connection)
        with mock.patch.object(tirtc_runtime.sdk, 'TiRtcDisconnect') as disconnect:
            # The adapter stops its connection before releasing its generation.
            tirtc_runtime.sdk.TiRtcDisconnect(connection)
            runtime.deactivate(ServiceKind.STREAM, generation)
            runtime.activate(ServiceKind.STREAM)
            runtime._on_conn_error(connection, -9001)
            runtime._callback_guard.wait_for_all()
            runtime._on_disconnected(connection)
            runtime._on_conn_error(connection, -9001)
            runtime._callback_guard.wait_for_all()
            disconnect.assert_called_once_with(connection)

    def test_current_connection_error_is_forwarded_to_owner(self):
        runtime = TiRtcRuntime()
        self.addCleanup(runtime.stop)
        errors = []
        callbacks = TIRTCCALLBACKS()
        callbacks.on_conn_error = tirtc_runtime.OnConnErrCB(
            lambda handle, error: errors.append((handle, error)))
        callbacks._cb_refs = [callbacks.on_conn_error]
        runtime.register_service(ServiceKind.CALL, callbacks, accepts_inbound=False)
        runtime._test_mark_started()
        runtime.activate(ServiceKind.CALL)
        runtime.bind_active_connection(ServiceKind.CALL, 0x202)
        with mock.patch.object(tirtc_runtime.sdk, 'TiRtcDisconnect') as disconnect:
            runtime._on_conn_error(0x202, -9001)
            self.assertEqual(errors, [(0x202, -9001)])
            disconnect.assert_not_called()

    def test_callbacks_match_tirtc_2_5_abi(self):
        self.assertEqual(tirtc_runtime.sdk._SDK_VERSION, "2.5.0")
        self.assertEqual(
            tirtc_runtime.sdk.TiRtcGetVersion(),
            b"v2.5.0",
        )
        self.assertEqual(TIRTCCALLBACKS._fields_[-1][0], "on_sleep_wakeup_info")
        self.assertEqual(
            ctypes.sizeof(TIRTCCALLBACKS),
            15 * ctypes.sizeof(ctypes.c_void_p),
        )
        self.assertFalse(TIRTCCALLBACKS().on_sleep_wakeup_info)

    def test_removed_file_log_api_is_not_called(self):
        self.assertIsNone(tirtc_runtime.sdk._legacy_log_config)
        tirtc_runtime.sdk.disable_file_logging()

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
        runtime._callback_guard.start()
        self.addCleanup(runtime.stop)
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

        configured_options = {}

        def set_option(option, data, length):
            if option == tirtc_runtime.sdk.TIRTC_OPT_MAX_CONNECTIONS:
                self.assertEqual(length, ctypes.sizeof(ctypes.c_int))
                configured_options[option] = ctypes.cast(
                    data, ctypes.POINTER(ctypes.c_int)).contents.value
            return 0

        with mock.patch.object(
                tirtc_runtime.sdk, "TiRtcSetOption",
                side_effect=set_option), \
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
                    tirtc_runtime.sdk, "disable_file_logging"), \
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
        self.assertEqual(
            tirtc_runtime.sdk.TIRTC_REFERENCE_MAX_CONNECTIONS,
            configured_options[tirtc_runtime.sdk.TIRTC_OPT_MAX_CONNECTIONS],
        )
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
                    tirtc_runtime.sdk, "disable_file_logging"), \
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
