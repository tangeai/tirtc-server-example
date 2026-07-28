"""
Tests for TGV1-HMAC-SHA256 signing — validates against shared test-vectors.json.
"""
import json
import os
import unittest
from datetime import datetime, timezone

from tirtc_signing import sign_request


def load_test_vectors():
    vectors_path = os.path.join(os.path.dirname(__file__), "..", "test-vectors.json")
    with open(vectors_path, "r") as f:
        data = json.load(f)
    return data["vectors"]


class TestSignRequest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.vectors = load_test_vectors()

    def test_deterministic(self):
        """Same inputs produce same output."""
        signing = datetime(2024, 1, 15, 12, 0, 0, tzinfo=timezone.utc)
        h1 = sign_request("access123", "secret", "app456", "POST", "/v1/token/wxvoip", '{"k":"v"}', "", signing)
        h2 = sign_request("access123", "secret", "app456", "POST", "/v1/token/wxvoip", '{"k":"v"}', "", signing)
        self.assertEqual(h1["Authorization"], h2["Authorization"])

    def test_different_secrets_different_sig(self):
        """Different secrets produce different signatures."""
        signing = datetime(2024, 1, 15, 12, 0, 0, tzinfo=timezone.utc)
        h1 = sign_request("access", "secret1", "app", "POST", "/path", "body", "", signing)
        h2 = sign_request("access", "secret2", "app", "POST", "/path", "body", "", signing)
        self.assertNotEqual(h1["Authorization"], h2["Authorization"])

    def test_contains_algorithm(self):
        """Authorization header starts with algorithm name."""
        signing = datetime(2024, 1, 15, 12, 0, 0, tzinfo=timezone.utc)
        h = sign_request("access", "secret", "app", "POST", "/v1/token/wxvoip", "{}", "", signing)
        self.assertTrue(h["Authorization"].startswith("TGV1-HMAC-SHA256"))

    def test_required_headers_post(self):
        """POST request includes all required headers."""
        signing = datetime(2024, 1, 15, 12, 0, 0, tzinfo=timezone.utc)
        h = sign_request("access", "secret", "app", "POST", "/v1/token/wxvoip", "{}", "", signing)
        for name in ["X-Tg-Algorithm", "X-Tg-Date", "X-Tg-App-Id", "X-Tg-Content-Sha256", "Content-Type"]:
            self.assertIn(name, h)
            self.assertNotEqual(h[name], "")

    def test_get_no_content_type(self):
        """GET request should not have Content-Type or Content-Length."""
        signing = datetime(2024, 1, 15, 12, 0, 0, tzinfo=timezone.utc)
        h = sign_request("access", "secret", "app", "GET", "/v1/devices", "", "status=online", signing)
        self.assertNotIn("Content-Type", h)
        self.assertNotIn("Content-Length", h)

    def test_uri_trailing_slash(self):
        """URI trailing slash should be normalized."""
        signing = datetime(2024, 1, 15, 12, 0, 0, tzinfo=timezone.utc)
        h1 = sign_request("access", "secret", "app", "POST", "/path/", "{}", "", signing)
        h2 = sign_request("access", "secret", "app", "POST", "/path", "{}", "", signing)
        self.assertEqual(h1["Authorization"], h2["Authorization"])

    # --- Cross-language test vectors ---

    def _run_vector(self, v):
        signing = datetime(2024, 1, 15, 12, 0, 0, tzinfo=timezone.utc)
        h = sign_request(
            access_key=v["accessKey"],
            access_secret=v["accessSecret"],
            app_id=v["appId"],
            method=v["method"],
            uri_path=v["uriPath"],
            body=v["body"],
            raw_query=v["rawQuery"],
            signing_time=signing,
        )
        for key, expected_val in v["expected"].items():
            actual = h.get(key, "")
            self.assertEqual(
                actual, expected_val,
                f"[{v['description']}] header '{key}' mismatch:\n"
                f"  expected: {expected_val}\n"
                f"  actual:   {actual}"
            )

    def test_vector_0_post_with_body(self):
        self._run_vector(self.vectors[0])

    def test_vector_1_post_empty_body(self):
        self._run_vector(self.vectors[1])

    def test_vector_2_get_with_query(self):
        self._run_vector(self.vectors[2])

    def test_vector_3_get_plus_in_query(self):
        self._run_vector(self.vectors[3])

    def test_vector_4_put_with_body(self):
        self._run_vector(self.vectors[4])

    def test_vector_5_delete_without_body(self):
        self._run_vector(self.vectors[5])

    def test_vector_6_uri_trailing_slash(self):
        self._run_vector(self.vectors[6])

    def test_vector_7_root_path(self):
        self._run_vector(self.vectors[7])


if __name__ == "__main__":
    unittest.main()
