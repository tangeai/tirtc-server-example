#!/usr/bin/env python3
"""
quickstart.py — 已绑定设备上线 + VoIP 等待来电（最简流程）

完整流程（验证码绑定、出厂内置凭证）参考 device_sim_main.py

用法：
  DEVICE_ID=DEV000001 DEVICE_KEY=your-key python3 quickstart.py
"""

import os
import rtc_voip
from device_flow import fetch_services, get_mqtt_token, connect_mqtt_blocking
from rtc_voip_session import VoipCallState

DEVICE_ID   = os.getenv("DEVICE_ID",     "DEV000001")
DEVICE_KEY  = os.getenv("DEVICE_KEY",    "your-key")

# 步骤 0：从服务发现接口获取各服务地址
#   GET {SERVICES_BASE_URL}/services
SERVICES_BASE_URL = os.getenv("SERVICES_BASE_URL", "http://ep-open.tangeopen.com")
_svc        = fetch_services(base_url=SERVICES_BASE_URL)
SERVER      = _svc["device_server"]
BROKER      = _svc["mqtt_host"]
BROKER_PORT = _svc["mqtt_port"]
BROKER_TLS  = _svc["mqtt_tls"]
VOIP_SERVER = _svc["voip_server"]
ENDPOINT    = os.getenv("TIRTC_ENDPOINT") or _svc.get("tirtc_endpoint") or "http://ep-tirtc.tange365.com"

VOIP_AUDIO  = os.getenv("VOIP_AUDIO",   os.path.join(os.path.dirname(__file__), "assets", "number.g711a"))

# 步骤 1：用 HMAC-SHA256 签名换取 mqtt_token
#   签名串 = device_id + timestamp + nonce
#   POST /v1/device/token，Headers: X-Device-Id / X-Timestamp / X-Nonce / X-Signature
mqtt_token = get_mqtt_token(SERVER, DEVICE_ID, DEVICE_KEY)

# 步骤 2：初始化 TiRTC SDK
#   TiRtcInit() → TiRtcSetOption(TIRTC_OPT_DEVICE_SECRET_KEY, device_key, ...)
#   → TiRtcStart(device_id, &callbacks)
rtc_voip.init_sdk(DEVICE_ID, DEVICE_KEY, ENDPOINT or None)

# 步骤 3：上报 VoIP profile，拉取授权用户列表
#   POST /v1/voip/device/profile   Authorization: Bearer {mqtt_token}
#   GET  /v1/voip/device/contacts  Authorization: Bearer {mqtt_token}
auth_list = rtc_voip.report_profile(VOIP_SERVER, mqtt_token)

# 步骤 4：建立 MQTT 长连接，监听来电（阻塞直到 Ctrl+C）
#   ClientID = sn_{device_id}，Username = device_id，Password = mqtt_token
#   订阅 device/sn_{device_id}/cmd 和 /notify
#   收到 call_incoming → TiRtcWhipConnect(peer_id, token, cb)
#   接听后收到命令字 0x2000，挂断发送 0x2001
handler = VoipCallState(VOIP_SERVER, DEVICE_ID, mqtt_token, VOIP_AUDIO, auth_list)
connect_mqtt_blocking(BROKER, BROKER_PORT, DEVICE_ID, mqtt_token, handler, use_tls=BROKER_TLS)
