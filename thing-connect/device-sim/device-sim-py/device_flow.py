#!/usr/bin/env python3
"""
device_flow.py — 设备上线协议层（HTTP + MQTT）

本文件只处理协议逻辑，不涉及 TiRTC SDK。
quickstart.py 和 device_sim_main.py 都通过本文件完成上线流程。

handler 接口（传入 connect_mqtt_blocking）：
  channel=wx（微信 VoIP）:
    handler.on_call_incoming(payload: dict)
    handler.on_callers_update()
    handler.on_call_cancel(payload: dict)
  channel=device（设备间通话，call-server）:
    handler.on_device_call_incoming(payload: dict)
    handler.on_room_cancel(payload: dict)
    handler.on_device_call_reject(payload: dict)
    handler.on_device_callers_update()                     # 旧回调
    handler.on_device_callers_update_payload(payload: dict) # 可选新回调
"""

import base64
import hashlib


class DeviceResetError(Exception):
    """服务端返回 6006：设备已被解绑，需重新走扫码绑定流程。"""
import hmac
import json
import os
import secrets
import signal
import ssl
import sys
import threading
import time

import requests
import paho.mqtt.client as mqtt
import http_trace

SERVICES_URL = "http://ep-open.tangeopen.com/services"

# ── 日志 ──────────────────────────────────────────────────────────────────
_LOG_LEVEL = 10

def set_log_level(level: str) -> None:
    global _LOG_LEVEL
    _LOG_LEVEL = {"debug": 10, "info": 20, "warn": 30, "error": 40}.get(level.lower(), 10)

def _log(msg):
    if _LOG_LEVEL <= 10:
        print(f"\033[0;36m[device]\033[0m {msg}", flush=True)

def _ok(msg):
    if _LOG_LEVEL <= 20:
        print(f"\033[0;32m[device]\033[0m {msg}", flush=True)

def _warn(msg):
    if _LOG_LEVEL <= 30:
        print(f"\033[1;33m[device]\033[0m {msg}", flush=True)

def _err(msg):
    if _LOG_LEVEL <= 40:
        print(f"\033[0;31m[device]\033[0m {msg}", file=sys.stderr, flush=True)


def _log_mqtt_connection_details(label: str, broker_host: str, broker_port: int,
                                 client_id: str, username: str, password: str,
                                 use_tls: bool, topics: list[str] | None = None) -> None:
    details = [
        f"{label}:",
        f"host={broker_host}",
        f"port={broker_port}",
        f"tls={use_tls}",
        f"client_id={client_id}",
        f"username={username}",
        f"password={password}",
    ]
    if topics:
        details.append(f"topics={topics}")
    _log("  ".join(details))


def _require_json(resp, action: str) -> dict:
    """Decode an API response or exit with a concise diagnostic."""
    try:
        data = resp.json()
    except ValueError as e:
        body = (resp.text or "")[:200].replace("\n", " ")
        _err(f"{action}响应非 JSON（HTTP {resp.status_code}）: {e}; body={body!r}")
        sys.exit(1)
    if not isinstance(data, dict):
        _err(f"{action}响应格式错误（HTTP {resp.status_code}）：顶层必须是 JSON 对象")
        sys.exit(1)
    return data


def _mqtt_reason_value(reason_code):
    """Return the numeric paho v2 disconnect reason code."""
    return getattr(reason_code, "value", reason_code)


def fetch_services(base_url: str = "") -> dict:
    """GET {base_url}/services → 标准化地址字典。

    base_url 默认为 SERVICES_URL，可通过 --endpoint 参数或环境变量覆盖。

    返回格式：
      {
        "device_server":   "https://...",
        "user_server":     "https://..." 或 "",
        "voip_server":     "https://...",
        "ai_server":       "https://...",
        "call_server":     "https://..." 或 ""（services 端点未升级时为空，见下）,
        "mqtt_host":       "...",
        "mqtt_port":       8883,
        "mqtt_tls":        True,
        "tirtc_endpoint":  "https://...",
      }

    失败时打印错误并 sys.exit(1)。
    """
    url = f"{base_url.rstrip('/')}/services" if base_url else SERVICES_URL
    _log(f"服务发现  GET {url}")
    try:
        resp = http_trace.request("GET", url, timeout=10)
    except requests.RequestException as e:
        _err(f"服务发现请求失败: {e}")
        sys.exit(1)

    if resp.status_code != 200:
        _err(f"服务发现返回 HTTP {resp.status_code}: {resp.text}")
        sys.exit(1)

    try:
        raw = resp.json()
    except Exception as e:
        _err(f"服务发现响应非 JSON: {e}")
        sys.exit(1)

    required = ("device-srv", "voip-srv", "ai-srv", "mqtt-srv")
    for key in required:
        if key not in raw:
            _err(f"服务发现响应缺少字段 '{key}'")
            sys.exit(1)

    mqtt_url = raw["mqtt-srv"]   # "mqtt://host:port" 或 "mqtts://host:port"
    try:
        scheme, host_port = mqtt_url.split("://", 1)
        mqtt_host, mqtt_port_str = host_port.rsplit(":", 1)
        mqtt_port = int(mqtt_port_str)
        mqtt_tls  = (scheme == "mqtts")
    except Exception:
        _err(f"服务发现 mqtt-srv 格式无法解析: {mqtt_url!r}，期望 mqtt[s]://host:port")
        sys.exit(1)

    result = {
        "device_server":   raw["device-srv"],
        "user_server":     raw.get("user-srv", ""),
        "voip_server":     raw["voip-srv"],
        "ai_server":       raw["ai-srv"],
        # call-srv is optional (not required) so this keeps working against a
        # call-srv remains optional at discovery parsing time; the full
        # simulator runtime validates it before starting concurrent services.
        "call_server":     raw.get("call-srv", ""),
        "mqtt_host":       mqtt_host,
        "mqtt_port":       mqtt_port,
        "mqtt_tls":        mqtt_tls,
        "tirtc_endpoint":  raw.get("tirtc-srv", ""),
    }
    _ok(f"服务发现成功: device={result['device_server']} mqtt={mqtt_host}:{mqtt_port}")
    return result


# ── 工具：创建 paho Client ────────────────────────────────────────────────
def _new_mqtt_client(client_id: str, username: str, password: str,
                     use_tls: bool = True) -> mqtt.Client:
    client = mqtt.Client(
        client_id=client_id,
        protocol=mqtt.MQTTv311,
        callback_api_version=mqtt.CallbackAPIVersion.VERSION2,
    )
    client.username_pw_set(username, password)
    if use_tls:
        tls_ctx = ssl.create_default_context()
        client.tls_set_context(tls_ctx)
    return client


def report_device(server: str, mac: str, device_id: str = "",
                  device_key: str = "") -> dict:
    """POST /v1/device/report → {"code": "123456", "temp_token": "..."}

    When device_key is provided, sends HMAC signature headers
    (X-Device-Id / X-Timestamp / X-Nonce / X-Signature) for signed report
    (scenario 1). Otherwise sends plain body without any device_id
    (scenario 2).
    """
    url = f"{server}/v1/device/report"
    payload = {"mac": mac}
    headers = {}

    if device_key:
        # Signed report (scenario 1): prove identity via HMAC signature.
        # device_id goes in X-Device-Id header, NOT in body.
        ts    = str(int(time.time()))
        nonce = secrets.token_hex(8)
        raw   = (device_id + ts + nonce).encode()
        sig   = base64.b64encode(
            hmac.new(device_key.encode(), raw, hashlib.sha256).digest()
        ).decode()
        headers = {
            "X-Device-Id": device_id,
            "X-Timestamp": ts,
            "X-Nonce":     nonce,
            "X-Signature": sig,
        }
    elif device_id:
        # Scenario 2 but caller passed device_id without key —
        # don't put it in body or it'll be rejected as scenario 3 (6014).
        _log("步骤 1  device_key not provided; device_id omitted")

    try:
        resp = http_trace.request("POST", url, json=payload, headers=headers, timeout=10)
    except requests.RequestException as e:
        _err(f"HTTP 请求失败: {e}")
        sys.exit(1)
    data = _require_json(resp, "设备上报")
    resp_code = data.get("code")
    _log(f"步骤 1  响应 HTTP:{resp.status_code} code={resp_code} msg={data.get('msg')}")
    if resp_code == 200:
        _ok(f"步骤 1  验证码={data['data']['code']} temp_token={data['data']['temp_token']}")
    if resp.status_code == 429 or data.get("code") == 429:
        retry = resp.headers.get("Retry-After", "10")
        _err(f"限频（429）：请等待 {retry}s 后重试")
        sys.exit(1)
    if data.get("code") == 40901:
        retry = resp.headers.get("Retry-After", "60")
        _err(f"验证已在进行中（40901）：上一次验证码仍有效，请等待 {retry}s")
        sys.exit(1)
    if data.get("code") == 6014:
        _err("上报失败（6014）：设备ID不可信——请在调用 report_device 时提供 device_key 以启用签名上报")
        sys.exit(1)
    if data.get("code") != 200:
        _err(f"上报失败 code={data.get('code')} msg={data.get('msg')}")
        sys.exit(1)
    return data["data"]


def get_mqtt_token(server: str, device_id: str, device_key: str, mac: str = "") -> str:
    """POST /v1/device/token（HMAC-SHA256 签名）→ mqtt_token

    签名串 = device_id + timestamp + nonce
    Headers: X-Device-Id / X-Timestamp / X-Nonce / X-Mac / X-Signature
    """
    ts    = str(int(time.time()))
    nonce = secrets.token_hex(8)
    raw   = (device_id + ts + nonce).encode()
    sig   = base64.b64encode(
        hmac.new(device_key.encode(), raw, hashlib.sha256).digest()
    ).decode()
    headers = {
        "X-Device-Id": device_id,
        "X-Timestamp": ts,
        "X-Nonce":     nonce,
        "X-Mac":       mac,
        "X-Signature": sig,
    }
    token_url = f"{server}/v1/device/token"
    try:
        resp = http_trace.request("POST", token_url, headers=headers, timeout=10)
    except requests.RequestException as e:
        _err(f"换取 token 失败: {e}")
        sys.exit(1)
    data = _require_json(resp, "换取 token")
    _log(f"步骤 1/4  响应 HTTP:{resp.status_code} code={data.get('code')}")
    if data.get("code") == 200:
        _ok(f"步骤 1/4  mqtt_token={data['data']['mqtt_token']}")
    if data.get("code") == 6006:
        raise DeviceResetError("设备已被解绑，需重新走扫码绑定流程")
    if data.get("code") != 200:
        server_msg = data.get("msg")
        if not isinstance(server_msg, str) or not server_msg:
            server_msg = "服务器未返回错误说明"
        _err(f"换 token 失败: {server_msg}")
        sys.exit(1)
    mqtt_token = data["data"]["mqtt_token"]
    _ok(f"mqtt_token 获取成功（有效期 7 天）")
    return mqtt_token


def connect_temp_mqtt(broker_host: str, broker_port: int,
                      temp_client_id: str, temp_token: str, timeout_sec: int,
                      use_tls: bool = True) -> dict:
    """临时身份连接，等待服务端下发 auth_grant。

    MQTT 连接参数：
      ClientID = temp_client_id（服务端 Report 响应返回的 tmp_{8位hex}，全局唯一）
      Username = temp_client_id
      Password = temp_token
    订阅 device/{temp_client_id}/cmd，收到 auth_grant 后回 ACK 并断开。
    """
    client_id  = temp_client_id
    down_topic = f"device/{client_id}/cmd"
    ack_topic  = f"device/{client_id}/ack"
    received      = threading.Event()
    result        = {}
    connect_error = []
    countdown_active = False

    def _render_bind_countdown(remaining_sec: int) -> None:
        print(
            f"\r\033[1;33m[device]\033[0m 等待绑定下发，验证码剩余有效时间: \033[1m{remaining_sec:>3d}s\033[0m",
            end="",
            flush=True,
        )

    def on_connect(client, userdata, flags, rc, props=None):
        if rc == 0:
            _ok("步骤 2  MQTT 临时连接成功")
            client.subscribe(down_topic, qos=1)
            _log(f"已订阅 {down_topic}，等待服务端下发 DeviceID+KEY…")
        else:
            rc_val = rc.value if hasattr(rc, "value") else rc
            _err(f"连接被拒绝 rc={rc_val}（temp_token 已过期？）")
            connect_error.append(rc)
            received.set()

    def on_message(client, userdata, msg):
        raw = msg.payload.decode()
        try:
            data = json.loads(raw)
        except json.JSONDecodeError:
            _err(f"JSON 解析失败: {raw}")
            return
        _log(f"MQTT 收到 {msg.topic}: {raw}")
        if data.get("type") == "auth_grant":
            data = data.get("payload") or {}
        else:
            _err(f"未知消息类型: {data.get('type')}")
            return
        device_id  = data.get("device_id")
        device_key = data.get("device_key")
        if not device_id or not device_key:
            # Pre-burned device: auth_grant with empty payload.
            # Device already has credentials in Flash.
            _ok("收到 auth_grant（预烧设备），使用本地预存凭证上线")
            _log(f"MQTT 发送 {ack_topic}: {{\"ack\":true}}")
            client.publish(ack_topic, json.dumps({"ack": True}), qos=1)
            time.sleep(0.3)
            received.set()
            return
        result["device_id"]  = device_id
        result["device_key"] = device_key
        _log(f"MQTT 发送 {ack_topic}: {{\"ack\":true}}")
        client.publish(ack_topic, json.dumps({"ack": True}), qos=1)
        time.sleep(0.3)
        received.set()

    client = _new_mqtt_client(client_id, temp_client_id, temp_token, use_tls)
    _log_mqtt_connection_details(
        "MQTT 临时连接参数",
        broker_host, broker_port,
        client_id, temp_client_id, temp_token, use_tls,
        [down_topic, ack_topic],
    )
    client.on_connect = on_connect
    client.on_message = on_message
    client.on_disconnect = lambda *a: None
    client.connect(broker_host, broker_port, keepalive=300)
    client.loop_start()
    deadline = time.monotonic() + timeout_sec
    last_remaining = None
    try:
        while not received.is_set():
            now = time.monotonic()
            if now >= deadline:
                break
            remaining = max(0, int(deadline - now + 0.999))
            if remaining != last_remaining:
                _render_bind_countdown(remaining)
                countdown_active = True
                last_remaining = remaining
            time.sleep(0.2)
        ok_flag = received.is_set()
    except KeyboardInterrupt:
        if countdown_active:
            print(flush=True)
        client.loop_stop()
        client.disconnect()
        raise
    if countdown_active:
        print(flush=True)
    client.loop_stop()
    client.disconnect()
    if not ok_flag:
        _err(f"等待超时（{timeout_sec}s），未收到下发消息")
        sys.exit(1)
    if connect_error:
        sys.exit(1)
    return result


def connect_mqtt_blocking(broker_host: str, broker_port: int,
                          device_id: str, mqtt_token: str,
                          handler,
                          stop_event: threading.Event = None,
                          use_tls: bool = True) -> None:
    """正式长连接（ClientID=sn_{device_id}），阻塞直到 Ctrl+C。

    MQTT 连接参数：
      ClientID = sn_{device_id}
      Username = device_id
      Password = mqtt_token
    订阅 device/sn_{device_id}/cmd 和 /notify。

    收到消息后路由给 handler：
      handler.on_call_incoming(payload)   # type=call_incoming, channel=wx
      handler.on_callers_update()         # type=callers_update, channel=wx
      handler.on_call_cancel(payload)     # type=call_cancel, channel=wx

    stop_event: 外部传入时直接使用；未传入时内部创建并注册 SIGINT handler。
    """
    client_id    = f"sn_{device_id}"
    cmd_topic    = f"device/{client_id}/cmd"
    notify_topic = f"device/{client_id}/notify"
    hb_topic     = f"device/{client_id}/up"
    _own_stop    = stop_event is None
    if _own_stop:
        stop_event = threading.Event()

    def on_connect(client, userdata, flags, rc, props=None):
        if rc == 0:
            _ok(f"步骤 2/4  MQTT 正式连接成功  ClientID={client_id}")
            client.subscribe(cmd_topic, qos=1)
            client.subscribe(notify_topic, qos=1)
            _ok("步骤 3/4  设备在线，保持长连接（Ctrl+C 退出）…")
        else:
            rc_val = rc.value if hasattr(rc, "value") else rc
            _err(f"正式连接被拒绝 rc={rc_val}")
            stop_event.set()

    def on_disconnect(client, userdata, disconnect_flags, reason_code, properties):
        # paho-mqtt Callback API v2 passes the reason code as the fifth
        # callback argument; DisconnectFlags does not contain it.
        rc = _mqtt_reason_value(reason_code)
        if stop_event.is_set():
            return
        if rc in (0x98, 0x99, 152, 153):
            _warn(f"Token 失效（rc={rc:#x}），需重新签名换取 mqtt_token")
        elif rc != 0:
            _warn(f"连接断开 rc={rc}，将自动重连…")

    def on_message(client, userdata, msg):
        raw = msg.payload.decode()
        _log(f"收到服务端消息 [{msg.topic}]: {raw}")
        if msg.topic.endswith("/cmd"):
            ack_t = msg.topic.replace("/cmd", "/ack")
            _log(f"发送 ACK [{ack_t}]: {{\"ack\":true}}")
            client.publish(ack_t, json.dumps({"ack": True}), qos=1)
        try:
            data = json.loads(raw)
        except json.JSONDecodeError:
            _err(f"JSON 解析失败: {raw}")
            return
        msg_type = data.get("type", "")
        channel  = data.get("channel", "")
        payload  = data.get("payload") or {}
        if msg_type == "unbind":
            _warn("收到服务端解绑通知，断开 MQTT 连接（凭证保留，重新绑定时复用）")
            stop_event.set()   # 主循环退出 → 下次上电视同已绑定设备，6006 后走重新绑定
        elif msg_type == "call_incoming" and channel == "wx":
            handler.on_call_incoming(payload)
        elif msg_type == "callers_update" and channel == "wx":
            handler.on_callers_update()
        elif msg_type == "call_cancel" and channel == "wx":
            handler.on_call_cancel(payload)
        elif msg_type == "call_incoming" and channel == "device":
            handler.on_device_call_incoming(payload)
        elif msg_type == "room_cancel" and channel == "device":
            handler.on_room_cancel(payload)
        elif msg_type == "call_reject" and channel == "device":
            handler.on_device_call_reject(payload)
        elif msg_type == "callers_update" and channel == "device":
            callback = getattr(handler, "on_device_callers_update_payload", None)
            if callback:
                callback(payload)
            else:
                handler.on_device_callers_update()
        elif msg_type == "callee_answered" and channel == "device":
            if hasattr(handler, "on_callee_answered"):
                handler.on_callee_answered(payload)

    def heartbeat_loop():
        seq = 0
        while not stop_event.wait(timeout=30):
            seq += 1
            hb_payload = {"type": "heartbeat", "seq": seq, "ts": int(time.time())}
            _log(f"心跳 #{seq} 发送 [{hb_topic}]: {json.dumps(hb_payload)}")
            client.publish(hb_topic, json.dumps(hb_payload), qos=0)

    if _own_stop:
        def handle_sigint(sig, frame):
            print()
            _warn("接收到 SIGINT，断开连接…")
            stop_event.set()
        signal.signal(signal.SIGINT, handle_sigint)

    client = _new_mqtt_client(client_id, device_id, mqtt_token, use_tls)
    _log_mqtt_connection_details(
        "MQTT 正式连接参数",
        broker_host, broker_port,
        client_id, device_id, mqtt_token, use_tls,
        [cmd_topic, notify_topic, hb_topic],
    )
    client.on_connect    = on_connect
    client.on_disconnect = on_disconnect
    client.on_message    = on_message
    client.connect(broker_host, broker_port, keepalive=60)
    client.loop_start()
    threading.Thread(target=heartbeat_loop, daemon=True).start()
    while not stop_event.wait(timeout=0.5):
        pass
    client.loop_stop()
    client.disconnect()
    _ok("已断开 MQTT 连接")
