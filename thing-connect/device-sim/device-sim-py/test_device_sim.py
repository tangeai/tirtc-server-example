#!/usr/bin/env python3

import json
import os
import stat
import tempfile
import unittest
from unittest import mock

import device_credentials
import device_flow


class FakeResponse:
    status_code = 502
    text = "<html>bad gateway</html>"

    def json(self):
        raise ValueError("not json")


class DeviceSimulatorTests(unittest.TestCase):
    def test_device_id_and_key_must_be_paired(self):
        self.assertTrue(device_credentials.credentials_are_paired("dev1", "key1"))
        self.assertTrue(device_credentials.credentials_are_paired("", ""))
        self.assertFalse(device_credentials.credentials_are_paired("dev1", ""))
        self.assertFalse(device_credentials.credentials_are_paired("", "key1"))

    def test_credentials_are_atomic_and_owner_only(self):
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "nested", "creds.json")
            self.assertTrue(device_credentials.save_creds("dev1", "key1", path))
            self.assertEqual(stat.S_IMODE(os.stat(path).st_mode), 0o600)
            self.assertEqual(device_credentials.load_saved_creds(path), ("dev1", "key1"))
            with open(path) as f:
                self.assertEqual(json.load(f), {"device_id": "dev1", "device_key": "key1"})
            self.assertEqual([n for n in os.listdir(os.path.dirname(path))
                              if n.startswith(".device-creds-")], [])

    def test_credentials_save_without_fchmod_on_windows(self):
        with tempfile.TemporaryDirectory() as root:
            path = os.path.join(root, "creds.json")
            with mock.patch.object(device_credentials.os, "fchmod", None):
                self.assertTrue(device_credentials.save_creds("dev1", "key1", path))
            self.assertEqual(device_credentials.load_saved_creds(path), ("dev1", "key1"))

    def test_non_json_report_exits_cleanly(self):
        with mock.patch.object(device_flow.requests, "post", return_value=FakeResponse()):
            with self.assertRaises(SystemExit) as raised:
                device_flow.report_device("http://device", "AA:BB:CC:DD:EE:FF")
        self.assertEqual(raised.exception.code, 1)

    def test_paho_v2_reason_code_uses_value(self):
        reason = type("Reason", (), {"value": 0x98})()
        self.assertEqual(device_flow._mqtt_reason_value(reason), 0x98)
        self.assertEqual(device_flow._mqtt_reason_value(7), 7)


if __name__ == "__main__":
    unittest.main()
