import contextlib
import http.server
import io
import json
import os
import tempfile
import threading
import unittest

import scale_load as load


class LoadTests(unittest.TestCase):
    def test_shards_do_not_overlap_or_drop_devices(self):
        with tempfile.NamedTemporaryFile(mode="w", delete=False) as f:
            f.write("device_id,device_key\n" + "".join(f"d{i},k{i}\n" for i in range(11)))
        try:
            groups = [load.fixtures(f.name, i, 3) for i in range(3)]
            self.assertEqual(11, sum(map(len, groups)))
            self.assertEqual(11, len({d["device_id"] for group in groups for d in group}))
        finally:
            os.unlink(f.name)

    def test_http_200_business_failure_is_not_success_and_secrets_stay_private(self):
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"code":6006,"msg":"SECRET"}')

            def log_message(self, *args):
                pass

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            stats, output = load.Stats(), io.StringIO()
            with contextlib.redirect_stdout(output):
                load.request_once(f"http://127.0.0.1:{server.server_port}",
                                  {"device_id": "test", "device_key": "SECRET"}, 1, stats)
            result = stats.snapshot()
            self.assertEqual(1, result["failed"])
            self.assertNotIn("SECRET", json.dumps(result) + output.getvalue())
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_merge_rejects_missing_shards(self):
        with tempfile.NamedTemporaryFile(mode="w", delete=False) as f:
            json.dump(dict(kind="final", run_id="r", shard=0, shards=2, start_at=1,
                           latency_bucket_upper_ms=list(load.BUCKETS)), f)
            f.write("\n")
        try:
            with self.assertRaisesRegex(ValueError, "missing shard"):
                load.merge([f.name])
        finally:
            os.unlink(f.name)

    def test_histogram_has_bounded_memory_and_preserves_tail(self):
        stats = load.Stats()
        for value in [1, 2, 60000]:
            stats.record(value, "ok", True)
        counts = stats.snapshot()["latency_bucket_counts"]
        self.assertEqual(12, len(counts))
        self.assertEqual(10, load.quantile(counts, .5))
        self.assertEqual(">30000", load.quantile(counts, .99))

    def test_interrupted_run_cannot_pass_merge(self):
        with tempfile.NamedTemporaryFile(mode="w", delete=False) as f:
            json.dump(dict(kind="final", run_id="r", shard=0, shards=1, start_at=1,
                           duration=60, interrupted=True, completed=1, failed=0, dropped=0,
                           latency_bucket_upper_ms=list(load.BUCKETS),
                           latency_bucket_counts=[1] + [0] * len(load.BUCKETS)), f)
            f.write("\n")
        try:
            self.assertFalse(load.merge([f.name])["success"])
        finally:
            os.unlink(f.name)


if __name__ == "__main__":
    unittest.main()
