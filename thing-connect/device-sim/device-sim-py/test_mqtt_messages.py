import json
import threading
import unittest
from types import SimpleNamespace
from unittest import mock
import device_flow


INVALID = [b'\xff', b'[]', b'null', b'1', b'{', b'{"type":[]}',
           b'{"channel":{}}', b'{"payload":[1]}', b'{"payload":false}']


class MqttMessageTests(unittest.TestCase):
    def test_permanent_connection_keeps_dispatching_after_invalid_messages(self):
        client, handler, stop = mock.Mock(), mock.Mock(), threading.Event()
        def deliver():
            for payload in INVALID:
                client.on_message(client, None, SimpleNamespace(topic='device/review/cmd', payload=payload))
            self.assertEqual(client.publish.call_count, len(INVALID))
            raw = json.dumps({'type':'call_incoming', 'channel':'device',
                              'payload':{'room_id':'room-1'}}).encode()
            client.on_message(client, None, SimpleNamespace(topic='device/review/cmd', payload=raw))
            stop.set()
        client.loop_start.side_effect = deliver
        with mock.patch.object(device_flow, '_new_mqtt_client', return_value=client):
            device_flow.connect_mqtt_blocking('localhost', 1883, 'review', 'test', handler, stop, False)
        handler.on_device_call_incoming.assert_called_once_with({'room_id':'room-1'})
        self.assertEqual(client.publish.call_count, len(INVALID) + 1)

    def test_temporary_connection_ignores_bad_envelopes_and_credentials(self):
        client = mock.Mock()
        def deliver():
            bad_credentials = [{'device_id': []}, {'device_id': 'only-one'},
                               {'device_id':1, 'device_key':2}]
            messages = INVALID + [json.dumps({'type':'auth_grant', 'payload':p}).encode()
                                  for p in bad_credentials]
            for payload in messages:
                client.on_message(client, None, SimpleNamespace(topic='device/temp/cmd', payload=payload))
            client.publish.assert_not_called()
            raw = b'{"type":"auth_grant","payload":{"device_id":"test","device_key":"test-key"}}'
            client.on_message(client, None, SimpleNamespace(topic='device/temp/cmd', payload=raw))
        client.loop_start.side_effect = deliver
        with mock.patch.object(device_flow, '_new_mqtt_client', return_value=client), \
                mock.patch.object(device_flow.time, 'sleep'):
            credentials = device_flow.connect_temp_mqtt('localhost', 1883, 'temp', 'test', 1, False, persist_credentials=mock.Mock(return_value=True))
        self.assertEqual(credentials, {'device_id':'test', 'device_key':'test-key'})
        client.publish.assert_called_once()

    def test_empty_binding_grant_remains_valid(self):
        for payload in (b'{"type":"auth_grant"}', b'{"type":"auth_grant","payload":null}'):
            self.assertIsNotNone(device_flow._decode_mqtt_message(payload))
