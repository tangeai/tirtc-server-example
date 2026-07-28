import unittest
from unittest import mock

import http_trace


class HTTPTraceTests(unittest.TestCase):
    def test_request_logs_full_exchange_without_redaction(self):
        response = mock.Mock(status_code=200)
        response.json.return_value = {
            "code": 200,
            "data": {
                "mqtt_token": "response-secret",
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
        self.assertIn('"Authorization":"Bearer request-secret"', rendered)
        self.assertIn('"X-Signature":"signature-secret"', rendered)
        self.assertIn('"device_key":"body-secret"', rendered)
        self.assertIn('"mqtt_token":"response-secret"', rendered)
        self.assertIn('"device_id":"device-1"', rendered)


if __name__ == "__main__":
    unittest.main()
