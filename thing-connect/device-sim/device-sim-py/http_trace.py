#!/usr/bin/env python3
"""Uniform HTTP tracing for the Python device simulator."""

from __future__ import annotations

import json

import requests


_MAX_TEXT_BODY = 4096


def _emit(message: str) -> None:
    print(f"\033[0;35m[http]\033[0m {message}", flush=True)


def _display_url(url: str, params=None) -> str:
    return requests.Request("GET", url, params=params).prepare().url or url


def _response_body(response) -> str:
    try:
        return json.dumps(response.json(), ensure_ascii=False, separators=(",", ":"))
    except (TypeError, ValueError):
        text = getattr(response, "text", "") or ""
        text = text.replace("\n", "\\n")
        if len(text) > _MAX_TEXT_BODY:
            return text[:_MAX_TEXT_BODY] + "…"
        return text


def request(method: str, url: str, **kwargs):
    """Send one requests call and always print its request/response exchange."""
    normalized_method = method.upper()
    display_url = _display_url(url, kwargs.get("params"))
    _emit(f"请求: {normalized_method} {display_url}")
    _emit("请求头: " + json.dumps(
        kwargs.get("headers") or {},
        ensure_ascii=False,
        separators=(",", ":"),
    ))
    if "json" in kwargs:
        _emit("请求体: " + json.dumps(
            kwargs["json"],
            ensure_ascii=False,
            separators=(",", ":"),
        ))
    elif "data" in kwargs:
        _emit(f"请求体: {kwargs['data']}")
    else:
        _emit("请求体: <empty>")

    sender = getattr(requests, normalized_method.lower())
    try:
        response = sender(url, **kwargs)
    except requests.RequestException as exc:
        _emit(f"响应: 请求异常 {exc}")
        raise

    _emit(f"响应: HTTP {response.status_code} body={_response_body(response)}")
    return response
