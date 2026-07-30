#!/usr/bin/env python3
"""
tirtc_sdk.py — TiRTC SDK ctypes 绑定层

对应头文件：include/tirtc/tiRTC.h
加载动态库：lib/libTiRTC.so

本文件只做原生 API 绑定，不持有初始化状态。进程级生命周期和统一回调
由 tirtc_runtime.py 管理；rtc_*.py 业务模块只管理会话、连接和媒体。
"""

import ctypes
import os
import platform
import sys

DEFAULT_SDK_VERSION = "2.2.1"

# ── 加载动态库 ────────────────────────────────────────────────────────────
# 目录布局: sdk/<platform>/<version>/{include,lib}/
# 默认固定使用 2.2.1；也允许调用方通过 TIRTC_SDK_VERSION 显式覆盖。
_DIR = os.path.dirname(os.path.abspath(__file__))
# PyInstaller frozen: 库已解压到 sys._MEIPASS/<platform>/<version>/lib
_SDK_ROOT = getattr(sys, "_MEIPASS", None) or os.path.join(_DIR, "..", "sdk")

if sys.platform == "win32":
    _PLATFORM, _LIB_FILE = "windows-x86_64", "libTiRTC.dll"
elif sys.platform == "darwin":
    _PLATFORM, _LIB_FILE = "macos-arm64", "libTiRTC.dylib"
elif sys.platform.startswith("linux"):
    _PLATFORM, _LIB_FILE = "linux-x86_64", "libTiRTC.so"
else:
    sys.exit(f"[tirtc] 不支持的平台: {sys.platform}；当前仓库仅内置 Linux x86_64、macOS arm64 和 Windows x86_64 SDK")

_MACHINE = platform.machine().lower()
_EXPECTED_MACHINES = {
    "linux-x86_64": {"x86_64", "amd64"},
    "macos-arm64": {"arm64", "aarch64"},
    "windows-x86_64": {"amd64", "x86_64"},
}
if _MACHINE not in _EXPECTED_MACHINES[_PLATFORM]:
    sys.exit(
        f"[tirtc] 当前机器架构为 {_MACHINE}，仓库内置的是 {_PLATFORM} SDK。"
        "请下载与目标系统和架构匹配的 TiRTC SDK，并放入 device-sim/sdk/<platform>/<version>/。"
    )

_SDK_VERSION = os.environ.get("TIRTC_SDK_VERSION", DEFAULT_SDK_VERSION).strip()
_LIB_DIR = os.path.join(_SDK_ROOT, _PLATFORM, _SDK_VERSION, "lib")
_LIB_PATH = os.path.join(_LIB_DIR, _LIB_FILE)
if not os.path.isfile(_LIB_PATH):
    sys.exit(
        f"[tirtc] 未找到 SDK 动态库: {_LIB_PATH}。"
        f"请检查 --sdk-version {_SDK_VERSION}，或下载匹配 {_PLATFORM} 的 TiRTC SDK。"
    )
if sys.platform == "win32" and hasattr(os, "add_dll_directory"):
    os.add_dll_directory(_LIB_DIR)

try:
    _lib = ctypes.CDLL(_LIB_PATH)
except OSError as e:
    sys.exit(f"[tirtc] 无法加载 {_LIB_PATH}（版本 {_SDK_VERSION}）: {e}")

# ── 常量 ──────────────────────────────────────────────────────────────────
TIRTC_AUDIO_PCM              = 1
TIRTC_AUDIO_ALAW             = 2
TIRTC_AUDIO_AAC              = 3
TIRTC_AUDIO_OPUS             = 4
TIRTC_AUDIO_AMR              = 5
TIRTC_VIDEO_JPEG             = 65
TIRTC_VIDEO_H264             = 66
TIRTC_VIDEO_H265             = 67
TIRTC_FRAME_FLAG_KEY_FRAME   = 0x01
TIRTC_AUDIOSAMPLE_8K16B1C    = 0
TIRTC_AUDIOSAMPLE_16K16B1C   = 1
AUDIO_STREAM_ID              = 10
VIDEO_STREAM_ID              = 11
TIRTC_EVENT_SYS_STARTED      = 0
TIRTC_EVENT_SYS_STOPPED      = 1

TIRTC_OPT_SERVICE_ENDPOINT   = 1
TIRTC_OPT_DEVICE_SECRET_KEY  = 2
TIRTC_OPT_CLIENT_ID          = 11
TIRTC_OPT_MAX_SEND_BUFFER    = 8

TIRTC_E_BUSY                 = -40006
TIRTC_E_INVALID_HANDLE       = -40002
TIRTC_E_CONN_TIMEOUTCLOSE    = -40007
TIRTC_E_CONN_REMOTECLOSE     = -40008
TIRTC_E_CONN_OTHER_ERROR     = -40009
TIRTC_E_CONN_CLOSED          = -1
CONN_FATAL_ERRORS = (TIRTC_E_CONN_TIMEOUTCLOSE, TIRTC_E_CONN_REMOTECLOSE, TIRTC_E_CONN_OTHER_ERROR)

# ── 结构体 ────────────────────────────────────────────────────────────────
class TIRTCFRAMEINFO(ctypes.Structure):
    _pack_ = 1
    _fields_ = [
        ("stream_id", ctypes.c_uint8),
        ("media",     ctypes.c_uint8),
        ("flags",     ctypes.c_uint8),
        ("reserved",  ctypes.c_uint8),
        ("ts",        ctypes.c_uint32),
        ("length",    ctypes.c_uint32),
    ]

# ── 回调类型 ──────────────────────────────────────────────────────────────
OnEventCB       = ctypes.CFUNCTYPE(None, ctypes.c_int, ctypes.c_void_p, ctypes.c_int)
OnConnAcceptCB  = ctypes.CFUNCTYPE(None, ctypes.c_void_p)
OnConnErrCB     = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_int)
OnDisconnCB     = ctypes.CFUNCTYPE(None, ctypes.c_void_p)
OnAudioCB       = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p)
OnVideoCB       = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p)
OnMsgCB         = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_void_p, ctypes.c_void_p)
OnCmdCB         = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_uint32, ctypes.c_void_p, ctypes.c_uint32)
OnKeyFrameCB    = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_uint8)
OnSubVideoCB    = ctypes.CFUNCTYPE(ctypes.c_int, ctypes.c_void_p, ctypes.c_uint8)
OnUnsubVideoCB  = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_uint8)
OnSubAudioCB    = ctypes.CFUNCTYPE(ctypes.c_int, ctypes.c_void_p, ctypes.c_uint8)
OnUnsubAudioCB  = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_uint8)
ConnectCB       = ctypes.CFUNCTYPE(None, ctypes.c_int, ctypes.c_void_p, ctypes.c_void_p)
ServiceReqCB    = ctypes.CFUNCTYPE(None, ctypes.c_char_p, ctypes.c_void_p)

class TIRTCCALLBACKS(ctypes.Structure):
    _fields_ = [
        ("on_event",             OnEventCB),
        ("on_conn_accepted",     OnConnAcceptCB),
        ("on_conn_error",        OnConnErrCB),
        ("on_disconnected",      OnDisconnCB),
        ("on_audio",             OnAudioCB),
        ("on_video",             OnVideoCB),
        ("on_message",           OnMsgCB),
        ("on_command",           OnCmdCB),
        ("on_request_key_frame", OnKeyFrameCB),
        ("on_subscribe_video",   OnSubVideoCB),
        ("on_unsubscribe_video", OnUnsubVideoCB),
        ("on_subscribe_audio",   OnSubAudioCB),
        ("on_unsubscribe_audio", OnUnsubAudioCB),
    ]

# ── SDK 函数绑定 ──────────────────────────────────────────────────────────
def _bind(name, restype, *argtypes):
    fn = getattr(_lib, name)
    fn.restype  = restype
    fn.argtypes = list(argtypes)
    return fn

TiRtcGetVersion      = _bind("TiRtcGetVersion",    ctypes.c_char_p)
TiRtcGetErrorStr     = _bind("TiRtcGetErrorStr",   ctypes.c_char_p, ctypes.c_int)
TiRtcInit            = _bind("TiRtcInit",          ctypes.c_int)
TiRtcUninit          = _bind("TiRtcUninit",        None)
TiRtcSetOption       = _bind("TiRtcSetOption",     ctypes.c_int,
                               ctypes.c_int, ctypes.c_void_p, ctypes.c_uint32)

# Keep old SDK handling inside the binding layer.  Business modules and
# current-facing documentation use the current API without version branches.
def _ver_tuple(value):
    try:
        return tuple(int(part) for part in value.split("."))
    except (AttributeError, ValueError):
        return (0,)


HAS_CLIENT_ID_OPT = _ver_tuple(_SDK_VERSION) >= (2, 2, 1)


def set_client_id(cid):
    """Set CLIENT_ID when the loaded SDK exposes that option."""
    if not HAS_CLIENT_ID_OPT:
        return 0
    return TiRtcSetOption(
        TIRTC_OPT_CLIENT_ID, ctypes.c_char_p(cid), len(cid))


def device_id_for_start(device_id, secret_key):
    """Build the TiRtcStart identity expected by the loaded SDK."""
    if HAS_CLIENT_ID_OPT:
        return device_id.encode()
    return f"{device_id},{secret_key}".encode()
TiRtcStart           = _bind("TiRtcStart",         ctypes.c_int,
                               ctypes.c_char_p, ctypes.POINTER(TIRTCCALLBACKS))
TiRtcStop            = _bind("TiRtcStop",          ctypes.c_int)
TiRtcLogConfig       = _bind("TiRtcLogConfig",     None,
                               ctypes.c_int, ctypes.c_char_p, ctypes.c_uint32)
TiRtcLogSetLevel     = _bind("TiRtcLogSetLevel",   None, ctypes.c_int)
TiRtcConnSetUserData = _bind("TiRtcConnSetUserData", ctypes.c_int,
                               ctypes.c_void_p, ctypes.c_void_p)
TiRtcConnGetUserData = _bind("TiRtcConnGetUserData", ctypes.c_void_p,
                               ctypes.c_void_p)
TiRtcDisconnect      = _bind("TiRtcDisconnect",    ctypes.c_int, ctypes.c_void_p)
TiRtcSendVideoStream = _bind("TiRtcSendVideoStream", ctypes.c_int,
                               ctypes.c_void_p,
                               ctypes.POINTER(TIRTCFRAMEINFO),
                               ctypes.c_void_p)
TiRtcSendAudioStream = _bind("TiRtcSendAudioStream", ctypes.c_int,
                               ctypes.c_void_p,
                               ctypes.POINTER(TIRTCFRAMEINFO),
                               ctypes.c_void_p)
TiRtcWhipConnect     = _bind("TiRtcWhipConnect",   ctypes.c_int,
                               ctypes.c_char_p, ctypes.c_char_p,
                               ConnectCB, ctypes.c_void_p)
# 设备间 P2P 连接（区别于 TiRtcWhipConnect 的 WHIP client 模式）：
# remote_id = 目标设备 device_id，token = call-server /v1/call/device/info 签发的连接凭证。
TiRtcConnect         = _bind("TiRtcConnect",       ctypes.c_int,
                               ctypes.c_char_p, ctypes.c_char_p,
                               ConnectCB, ctypes.c_void_p)
TiRtcSendCommand     = _bind("TiRtcSendCommand",   ctypes.c_int,
                               ctypes.c_void_p, ctypes.c_uint32,
                               ctypes.c_void_p, ctypes.c_uint32)
TiRtcSubscribeVideo  = _bind("TiRtcSubscribeVideo", ctypes.c_int,
                               ctypes.c_void_p, ctypes.c_uint8)
TiRtcUnsubscribeVideo = _bind("TiRtcUnsubscribeVideo", ctypes.c_int,
                                ctypes.c_void_p, ctypes.c_uint8)
if HAS_CLIENT_ID_OPT:
    LogCB = ctypes.CFUNCTYPE(None, ctypes.c_char_p, ctypes.c_uint32)
else:
    LogCB = ctypes.CFUNCTYPE(None, ctypes.c_char_p)
TiRtcLogSetCallback  = _bind("TiRtcLogSetCallback", None, LogCB)
TiRtcServiceRequest  = _bind("TiRtcServiceRequest", ctypes.c_int,
                               ctypes.c_char_p, ctypes.c_char_p,
                               ctypes.c_char_p, ServiceReqCB, ctypes.c_void_p)
