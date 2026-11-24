#!/usr/bin/env python3
"""Bounded, sharded HTTP load driver. Standard library only; JSONL on stdout.

Use a dedicated test installation. Credentials never enter the output. Each
scheduled request runs once: hidden retries would invalidate outage measurements.
"""
import argparse
import base64
import concurrent.futures
import csv
import hmac
import json
import math
import secrets
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

BUCKETS = (10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000)


def fixtures(path, shard, shards):
    with open(path, newline="", encoding="utf-8") as f:
        rows = list(csv.DictReader(f))
    if not rows or any(not r.get("device_id") or not r.get("device_key") for r in rows):
        raise ValueError("fixture CSV requires device_id and device_key on every row")
    if len({r["device_id"] for r in rows}) != len(rows):
        raise ValueError("duplicate fixture device IDs")
    selected = rows[shard::shards]
    if not selected:
        raise ValueError("shard has no devices")
    return selected


def signed_headers(device):
    stamp, nonce = str(int(time.time())), secrets.token_hex(16)
    raw = device["device_id"] + stamp + nonce
    signature = base64.b64encode(hmac.digest(device["device_key"].encode(), raw.encode(), "sha256")).decode()
    return {"X-Device-Id": device["device_id"], "X-Timestamp": stamp,
            "X-Nonce": nonce, "X-Signature": signature}


class Stats:
    def __init__(self):
        self.lock = threading.Lock()
        self.completed = self.failed = self.dropped = 0
        self.buckets = [0] * (len(BUCKETS) + 1)
        self.statuses = {}

    def record(self, elapsed, status, success):
        with self.lock:
            self.completed += 1
            self.failed += not success
            index = next((i for i, bound in enumerate(BUCKETS) if elapsed <= bound), len(BUCKETS))
            self.buckets[index] += 1
            self.statuses[status] = self.statuses.get(status, 0) + 1

    def snapshot(self):
        with self.lock:
            return dict(completed=self.completed, failed=self.failed, dropped=self.dropped,
                        latency_bucket_counts=list(self.buckets), outcomes=dict(self.statuses))


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # Never forward device credentials to a different endpoint.


def request_once(url, device, timeout, stats):
    started = time.monotonic()
    outcome, success = "transport_error", False
    try:
        req = urllib.request.Request(url, data=b"", headers=signed_headers(device), method="POST")
        with urllib.request.build_opener(NoRedirect).open(req, timeout=timeout) as resp:
            body = json.loads(resp.read(65537))
            code = body.get("code")
            outcome = f"http_{resp.status}_code_{code if isinstance(code, int) else 'invalid'}"
            success = resp.status == 200 and code == 200 and bool(body.get("data", {}).get("mqtt_token"))
    except urllib.error.HTTPError as err:
        outcome = f"http_{err.code}"
        err.close()
    except (OSError, ValueError, TypeError, AttributeError):
        pass
    finally:
        stats.record((time.monotonic() - started) * 1000, outcome, success)


def quantile(counts, q):
    target = math.ceil(sum(counts) * q)
    if not target:
        return None
    seen = 0
    for i, count in enumerate(counts):
        seen += count
        if seen >= target:
            return BUCKETS[i] if i < len(BUCKETS) else ">30000"


def run(args):
    parsed = urllib.parse.urlsplit(args.url)
    if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.username or parsed.query or parsed.fragment:
        raise ValueError("use an HTTP(S) token endpoint without credentials, query or fragment")
    devices = fixtures(args.fixtures, args.shard, args.shards)
    stats, slots = Stats(), threading.BoundedSemaphore(args.workers)
    # Epoch-based start makes independent hosts share the same timeline.
    start_epoch = args.start_at or time.time()
    if args.start_at is not None and (not math.isfinite(start_epoch) or start_epoch < time.time()):
        raise ValueError("--start-at must be in the future")
    while time.time() < start_epoch:
        time.sleep(max(0, min(.2, start_epoch - time.time())))
    start = time.monotonic()
    deadline, next_sample, sequence = start + args.duration, start, 0
    planned = math.ceil(args.duration * args.rate)
    metadata = dict(run_id=args.run_id, shard=args.shard, shards=args.shards,
                    start_at=start_epoch, latency_bucket_upper_ms=list(BUCKETS),
                    duration=args.duration, rate_per_shard=args.rate, workers=args.workers,
                    planned=planned)
    interrupted = False

    def emit(kind):
        print(json.dumps(dict(metadata, kind=kind, at=time.time(), interrupted=interrupted, **stats.snapshot())), flush=True)

    def invoke(device):
        try:
            request_once(args.url, device, args.timeout, stats)
        finally:
            slots.release()

    emit("start")
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        try:
            while time.monotonic() < deadline:
                # Missed schedule slots are generator drops, not a catch-up burst.
                current_slot = min(planned, int((time.monotonic() - start) * args.rate))
                if current_slot > sequence:
                    with stats.lock:
                        stats.dropped += current_slot - sequence
                    sequence = current_slot
                due = start + sequence / args.rate
                remaining = due - time.monotonic()
                if remaining > 0:
                    time.sleep(min(remaining, max(0, deadline-time.monotonic())))
                if sequence >= planned or time.monotonic() >= deadline:
                    break
                if slots.acquire(blocking=False):
                    pool.submit(invoke, devices[sequence % len(devices)])
                else:
                    with stats.lock:
                        stats.dropped += 1
                sequence += 1
                if time.monotonic() >= next_sample:
                    emit("sample")
                    next_sample = time.monotonic() + 5
        except KeyboardInterrupt:
            interrupted = True
        finally:
            # A stall in output, sleep or scheduling can cross the end of the
            # run without another loop iteration. Account for the entire tail.
            with stats.lock:
                stats.dropped += max(0, planned - sequence)
    emit("final")
    snapshot = stats.snapshot()
    return 1 if interrupted or not snapshot["completed"] or snapshot["failed"] or snapshot["dropped"] else 0


def merge(paths):
    finals = {}
    for path in paths:
        with open(path, encoding="utf-8") as f:
            for line in f:
                item = json.loads(line)
                if item.get("kind") == "final":
                    key = (item["run_id"], item["shard"])
                    if key in finals:
                        raise ValueError("duplicate shard final result")
                    finals[key] = item
    if not finals:
        raise ValueError("no final samples; incomplete run")
    values = list(finals.values())
    first = values[0]
    if any(v["run_id"] != first["run_id"] or v["shards"] != first["shards"] or
           v["start_at"] != first["start_at"] or v.get("duration") != first.get("duration") or v["latency_bucket_upper_ms"] != list(BUCKETS) for v in values):
        raise ValueError("incompatible run metadata")
    if {v["shard"] for v in values} != set(range(first["shards"])):
        raise ValueError("missing shard results")
    counts = [sum(v["latency_bucket_counts"][i] for v in values) for i in range(len(BUCKETS)+1)]
    totals = {k: sum(v[k] for v in values) for k in ("completed", "failed", "dropped")}
    return dict(run_id=first["run_id"], **totals, p50_upper_ms=quantile(counts,.5),
                p95_upper_ms=quantile(counts,.95), p99_upper_ms=quantile(counts,.99),
                success=bool(totals["completed"]) and not (totals["failed"] or totals["dropped"] or any(v.get("interrupted", False) for v in values)))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--merge", nargs="+")
    p.add_argument("--url")
    p.add_argument("--fixtures")
    p.add_argument("--run-id", default="baseline")
    p.add_argument("--shards", type=int, default=1)
    p.add_argument("--shard", type=int, default=0)
    p.add_argument("--start-at", type=float, help="shared Unix epoch seconds; required for multiple shards")
    p.add_argument("--duration", type=float, default=60)
    p.add_argument("--rate", type=float, default=10, help="requests/second PER SHARD")
    p.add_argument("--workers", type=int, default=16)
    p.add_argument("--timeout", type=float, default=10)
    args = p.parse_args()
    try:
        if args.merge:
            result = merge(args.merge)
            print(json.dumps(result))
            return 0 if result["success"] else 1
        if not args.url or not args.fixtures or not 0 <= args.shard < args.shards or not 0 < args.rate <= 100000 or not 0 < args.workers <= 4096 or not 0 < args.timeout <= 180 or not 0 < args.duration <= 7*86400:
            p.error("provide endpoint/fixtures and valid positive limits")
        if args.shards > 1 and args.start_at is None:
            p.error("distributed runs require a shared --start-at")
        return run(args)
    except (ValueError, OSError) as err:
        p.error(str(err))


if __name__ == "__main__":
    raise SystemExit(main())
