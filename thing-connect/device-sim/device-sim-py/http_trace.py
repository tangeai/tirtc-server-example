#!/usr/bin/env python3
"""Uniform HTTP tracing for the Python device simulator."""

from __future__ import annotations

import json
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

import requests


_REDACTED = "<redacted>"
_SENSITIVE_KEY_PARTS = (
    "authorization",
    "device_key",
    "password",
    "secret",
    "session_key",
    "signature",
    "token",
)
_SENSITIVE_KEYS = {
    "temp_client_id",
    "wx_payload",
    "wxa_payload",
}


def _emit(message: str) -> None:
    print(f"\033[0;35m[http]\033[0m {message}", flush=True)


def _is_sensitive_key(key: object) -> bool:
    normalized = str(key).lower().replace("-", "_")
    return (
        normalized in _SENSITIVE_KEYS
        or any(part in normalized for part in _SENSITIVE_KEY_PARTS)
    )


def _redact(value, *, redact_peer_id: bool = False):
    if isinstance(value, dict):
        return {
            key: (
                _REDACTED
                if _is_sensitive_key(key)
                or (
                    redact_peer_id
                    and str(key).lower().replace("-", "_") == "peer_id"
                )
                else _redact(item, redact_peer_id=redact_peer_id)
            )
            for key, item in value.items()
        }
    if isinstance(value, (list, tuple)):
        return [
            _redact(item, redact_peer_id=redact_peer_id)
            for item in value
        ]
    return value


def _display_url(url: str, params=None) -> str:
    prepared = requests.Request("GET", url, params=params).prepare().url or url
    parts = urlsplit(prepared)
    query = [
        (key, _REDACTED if _is_sensitive_key(key) else value)
        for key, value in parse_qsl(parts.query, keep_blank_values=True)
    ]
    return urlunsplit((
        parts.scheme,
        parts.netloc,
        parts.path,
        urlencode(query),
        parts.fragment,
    ))


def _response_body(response) -> str:
    try:
        value = _redact(response.json(), redact_peer_id=True)
        return json.dumps(value, ensure_ascii=False, separators=(",", ":"))
    except (TypeError, ValueError):
        text = getattr(response, "text", "") or ""
        return f"<non-json body omitted; {len(text.encode('utf-8'))} bytes>"


def _request_body(kwargs) -> str:
    if "json" in kwargs:
        value = _redact(kwargs["json"])
    elif "data" in kwargs:
        value = kwargs["data"]
        if isinstance(value, (str, bytes, bytearray)):
            try:
                value = json.loads(value)
            except (TypeError, ValueError, UnicodeDecodeError):
                return f"<opaque body omitted; {len(value)} bytes>"
        value = _redact(value)
    else:
        return "<empty>"
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def request(method: str, url: str, **kwargs):
    """Send one requests call and always print its request/response exchange."""
    normalized_method = method.upper()
    display_url = _display_url(url, kwargs.get("params"))
    _emit(f"请求: {normalized_method} {display_url}")
    _emit("请求头: " + json.dumps(
        _redact(kwargs.get("headers") or {}),
        ensure_ascii=False,
        separators=(",", ":"),
    ))
    _emit("请求体: " + _request_body(kwargs))

    sender = getattr(requests, normalized_method.lower())
    try:
        response = sender(url, **kwargs)
    except requests.RequestException as exc:
        _emit(f"响应: 请求异常 {type(exc).__name__}")
        raise

    _emit(f"响应: HTTP {response.status_code} body={_response_body(response)}")
    return response
