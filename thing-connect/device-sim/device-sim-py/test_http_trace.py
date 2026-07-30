import unittest
from unittest import mock

import http_trace


class HTTPTraceTests(unittest.TestCase):
    def test_request_logs_exchange_with_credentials_redacted(self):
        response = mock.Mock(status_code=200)
        response.json.return_value = {
            "code": 200,
            "data": {
                "mqtt_token": "response-secret",
                "peer_id": "http://rtc.example/connect?credential=secret",
                "wx_session_key": "wechat-session-secret",
                "device_id": "device-1",
            },
        }

        with mock.patch("http_trace.requests.post", return_value=response) as post:
            with mock.patch("http_trace._emit") as emit:
                got = http_trace.request(
                    "POST",
                    "https://device.example/v1/device/token?code=query-secret",
                    headers={
                        "Authorization": "Bearer request-secret",
                        "X-Signature": "signature-secret",
                    },
                    json={"device_key": "body-secret", "device_id": "device-1"},
                    timeout=10,
                )

        self.assertIs(got, response)
        post.assert_called_once()
        rendered = "\n".join(call.args[0] for call in emit.call_args_list)
        self.assertIn(
            "POST https://device.example/v1/device/token?code=query-secret",
            rendered,
        )
        self.assertIn('"Authorization":"<redacted>"', rendered)
        self.assertIn('"X-Signature":"<redacted>"', rendered)
        self.assertIn('"device_key":"<redacted>"', rendered)
        self.assertIn('"mqtt_token":"<redacted>"', rendered)
        self.assertIn('"peer_id":"<redacted>"', rendered)
        self.assertIn('"wx_session_key":"<redacted>"', rendered)
        self.assertIn('"device_id":"device-1"', rendered)
        self.assertNotIn("request-secret", rendered)
        self.assertNotIn("signature-secret", rendered)
        self.assertNotIn("body-secret", rendered)
        self.assertNotIn("response-secret", rendered)
        self.assertNotIn("wechat-session-secret", rendered)
        self.assertNotIn("credential=secret", rendered)


if __name__ == "__main__":
    unittest.main()
