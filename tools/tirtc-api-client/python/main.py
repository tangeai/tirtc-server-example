#!/usr/bin/env python3
"""tirtc-api-client: 服务端调用探鸽云 OpenAPI 示例

Usage:
    export TIRTC_APP_ID=xxx
    export TIRTC_ACCESS_KEY=xxx
    export TIRTC_SECRET_KEY=xxx

    python main.py wxvoip
    python main.py aichat
    python main.py login
    python main.py plans
"""

import json
import os
import sys
from datetime import datetime, timezone
from urllib.request import Request, urlopen

from tirtc_signing import sign_request

ENDPOINT_TOKEN = "https://api-tirtc.tange365.com"
ENDPOINT_OPENAPI = "https://openapi-cn01.tange365.com"


def main():
    if len(sys.argv) < 2:
        print("用法: python main.py <api>", file=sys.stderr)
        print("  wxvoip  — POST /v1/token/wxvoip", file=sys.stderr)
        print("  aichat  — POST /v1/token/aichat", file=sys.stderr)
        print("  login   — POST /v2/user/login/user-id", file=sys.stderr)
        print("  plans   — GET  /v2/cloud-service/plans", file=sys.stderr)
        sys.exit(1)

    app_id = _require_env("TIRTC_APP_ID")
    access_key = _require_env("TIRTC_ACCESS_KEY")
    secret_key = _require_env("TIRTC_SECRET_KEY")

    api = sys.argv[1]
    if api == "wxvoip":
        run_wxvoip(app_id, access_key, secret_key)
    elif api == "aichat":
        run_aichat(app_id, access_key, secret_key)
    elif api == "login":
        run_login(app_id, access_key, secret_key)
    elif api == "plans":
        run_plans(app_id, access_key, secret_key)
    else:
        print(f"未知 API: {api}", file=sys.stderr)
        sys.exit(1)


def run_wxvoip(app_id, access_key, secret_key):
    body = {
        "device_id": "TESTDEVICE01",
        "wx_session_key": "test-session-key",
        "wx_room_id": "test-room-001",
        "wx_session_token": "test-server-token",
        "wx_app_id": "wx0123456789abcdef",
        "wx_model_id": "model-001",
        "audio_rate": 8000,
        "audio_channels": 1,
    }
    print("=== POST /v1/token/wxvoip (微信 VoIP) ===")
    print(f"device_id: {body['device_id']}\n")
    code, resp = do_post(app_id, access_key, secret_key, ENDPOINT_TOKEN, "/v1/token/wxvoip", body)
    print_result(code, resp)


def run_aichat(app_id, access_key, secret_key):
    body = {"device_id": "TESTDEVICE01", "role_id": "your-role-id"}
    print("=== POST /v1/token/aichat (AI 语音对话) ===")
    print(f"device_id: {body['device_id']}")
    print(f"role_id:   {body['role_id']}\n")
    code, resp = do_post(app_id, access_key, secret_key, ENDPOINT_TOKEN, "/v1/token/aichat", body)
    print_result(code, resp)


def run_login(app_id, access_key, secret_key):
    body = {"user_id": "test-user-001"}
    print("=== POST /v2/user/login/user-id (用户登录) ===")
    print(f"user_id: {body['user_id']}\n")
    code, resp = do_post(app_id, access_key, secret_key, ENDPOINT_OPENAPI, "/v2/user/login/user-id", body)
    print_result(code, resp)


def run_plans(app_id, access_key, secret_key):
    print("=== GET /v2/cloud-service/plans (套餐列表) ===\n")
    code, resp = do_get(app_id, access_key, secret_key, ENDPOINT_OPENAPI, "/v2/cloud-service/plans")
    print_result(code, resp)


def do_post(app_id, access_key, secret_key, endpoint, uri_path, body):
    body_str = json.dumps(body)
    return do_request(app_id, access_key, secret_key, endpoint, "POST", uri_path, "", body_str)


def do_get(app_id, access_key, secret_key, endpoint, uri_path, raw_query=""):
    return do_request(app_id, access_key, secret_key, endpoint, "GET", uri_path, raw_query, None)


def do_request(app_id, access_key, secret_key, endpoint, method, uri_path, raw_query, body_str):
    headers = sign_request(access_key, secret_key, app_id, method, uri_path, raw_query, body_str)

    endpoint = os.environ.get("TIRTC_ENDPOINT", endpoint)
    full_url = endpoint + uri_path
    if raw_query:
        full_url += "?" + raw_query

    print(f"→ {method} {full_url}")

    data = body_str.encode("utf-8") if body_str else None
    req = Request(full_url, data=data, method=method)
    for k, v in headers.items():
        req.add_header(k, v)

    try:
        with urlopen(req, timeout=15) as resp:
            return resp.status, resp.read().decode("utf-8")
    except Exception as e:
        print(f"❌ 请求失败: {e}")
        return 0, ""


def print_result(status_code, body):
    print(f"HTTP {status_code}")
    if body:
        try:
            parsed = json.loads(body)
            print(json.dumps(parsed, indent=2, ensure_ascii=False))
            code = parsed.get("code", -1)
            if code == 0 or code == 200:
                print("✅ 成功")
            elif code == 401 or code == 40105:
                print("❌ 签名验证失败")
            else:
                print(f"⚠️  code={code}, msg={parsed.get('msg', '')}")
        except json.JSONDecodeError:
            print(body)


def _require_env(key):
    v = os.environ.get(key)
    if not v:
        print(f"缺少环境变量 {key}", file=sys.stderr)
        sys.exit(1)
    return v


if __name__ == "__main__":
    main()
