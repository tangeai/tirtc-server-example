#!/usr/bin/env python3
"""
device_sim_main.py — 模拟 RTC 设备完整生命周期

阶段一（验证码流程）：report_device → connect_temp_mqtt → 获取 device_id + device_key
阶段二：get_mqtt_token → 启动进程级 TiRTC runtime → connect_mqtt_blocking

快速入门见 quickstart.py（已绑定设备最简流程）

实时推流是基础状态；VoIP、AI 或设备通话建立时临时抢占，结束后自动恢复。
"""

import argparse
import importlib
import json
import os
import shlex
import signal
import sys
import threading

DEFAULT_SDK_VERSION = "2.2.1"
DEFAULT_AUDIO_FILENAME = "audio.g711a"
EXPERIENCE_PLATFORM_URL = "https://demo-open.tange-ai.com"
MIN_PYTHON_VERSION = (3, 10)
MAX_PYTHON_VERSION = (3, 14)
COMMAND_THREAD_JOIN_TIMEOUT_SEC = 5.0


def _is_supported_python(version_info=None):
    """Return whether the interpreter is in the simulator's supported range."""
    version = tuple(
        (sys.version_info if version_info is None else version_info)[:2]
    )
    return MIN_PYTHON_VERSION <= version <= MAX_PYTHON_VERSION


if not _is_supported_python():
    sys.exit(
        f"[device] 当前 Python {sys.version.split()[0]} 不受支持；"
        "请使用 Python 3.10–3.14。"
    )

# ── 依赖检查 ──────────────────────────────────────────────────────────────────
def _find_missing_deps(requirements):
    missing = []
    for mod, pip_name in requirements:
        try:
            importlib.import_module(mod)
        except ImportError:
            missing.append((mod, pip_name))
    return missing


_REQUIRED_DEPS = [
    ("paho.mqtt.client", "paho-mqtt"),
    ("requests", "requests"),
]
if sys.version_info[:2] >= (3, 13):
    # audioop was removed from the standard library in 3.13. audioop-lts
    # installs a compatible module under the original "audioop" import name.
    _REQUIRED_DEPS.append(("audioop", "audioop-lts"))

_MISSING_DEPS = _find_missing_deps(_REQUIRED_DEPS)
if _MISSING_DEPS:
    print("\033[1;33m[warn] 缺少依赖:\033[0m")
    for _mod, _pip in _MISSING_DEPS:
        print(f"  pip install {_pip}")
    print("")
    _all = " ".join(p for _, p in _MISSING_DEPS)
    print(f"  一键安装: pip install {_all}")
    sys.exit(1)


# ── SDK 版本（必须在 import rtc_* 之前设定：tirtc_sdk 加载动态库时读它）────────
def _early_sdk_version(argv):
    """argparse 之前从 argv 取 --sdk-version，因为 rtc 模块 import 时就要加载库。"""
    it = iter(argv)
    for a in it:
        if a == "--sdk-version":
            try:
                return next(it)
            except StopIteration:
                return None
        if a.startswith("--sdk-version="):
            return a.split("=", 1)[1]
    return None

if not os.environ.get("TIRTC_SDK_VERSION"):
    os.environ["TIRTC_SDK_VERSION"] = _early_sdk_version(sys.argv) or DEFAULT_SDK_VERSION

_OPTIONAL_HW_AUDIO_DEPS = [
    ("numpy", "numpy"),
    ("sounddevice", "sounddevice"),
    ("soxr", "soxr"),
]

import device_flow
from media_formats import (
    AUDIO_FORMATS,
    AUDIO_FORMAT_CHOICES,
    VIDEO_FORMAT_CHOICES,
    normalize_audio_format,
    validate_with_mic_audio_formats,
)
from device_credentials import load_saved_creds as _load_saved_creds
from device_credentials import save_creds as _save_creds
from device_credentials import credentials_are_paired
from device_flow import (
    fetch_services,
    report_device, get_mqtt_token,
    connect_temp_mqtt, connect_mqtt_blocking, set_log_level,
    DeviceResetError,
)
_rtc_stream_available = False
try:
    import rtc_stream as _rtc_stream
    _rtc_stream_available = True
except (ImportError, OSError, SystemExit):
    pass

_rtc_voip_available = False
try:
    import rtc_voip as _rtc_voip
    _rtc_voip_available = True
except (ImportError, OSError, SystemExit):
    pass

_rtc_call_available = False
try:
    import rtc_call as _rtc_call
    _rtc_call_available = True
except (ImportError, OSError, SystemExit):
    pass

_rtc_ai_available = False
try:
    import rtc_ai as _rtc_ai
    _rtc_ai_available = True
except (ImportError, OSError, SystemExit):
    pass


def _terminal_link(url: str, label: str = "") -> str:
    if not url:
        return ""
    text = label or url
    return f"\033]8;;{url}\033\\{text}\033]8;;\033\\"


def _print_bind_guide() -> None:
    homepage_url = EXPERIENCE_PLATFORM_URL
    print(f"\033[1;36m[device]\033[0m  注册/登录入口: \033[1;96;4m{_terminal_link(homepage_url, homepage_url)}\033[0m")
    print(f"\033[1;36m[device]\033[0m  操作指引      : 打开首页完成注册/登录后，进入设备绑定并输入上方验证码")


def _print_tts_guide(server: str, code: str) -> None:
    """打印验证码 TTS 下载及播放命令，不把临时凭证写入日志。"""
    tts_url = f"{server.rstrip('/')}/v1/device/tts?code={code}&fmt=wav"
    output_file = "/tmp/device-verify-code.wav"
    curl_command = (
        "curl -fsS "
        f"-H {shlex.quote('Authorization: Bearer <TEMP_TOKEN>')} "
        f"{shlex.quote(tts_url)} -o {output_file}"
    )
    print(f"\033[1;35m[device]\033[0m  播放验证码 TTS（请在有扬声器的电脑终端执行）：")
    print(f"\033[1;35m[device]\033[0m    将 <TEMP_TOKEN> 替换为本次临时凭证；凭证不会写入日志")
    print(f"\033[1;35m[device]\033[0m    下载 : {curl_command}")
    print(f"\033[1;35m[device]\033[0m    macOS: afplay {output_file}")
    print(f"\033[1;35m[device]\033[0m    Linux: ffplay -nodisp -autoexit {output_file}")


def _bind_via_scan(args, server: str, broker_host: str, broker_port: int, broker_tls: bool,
                   device_id: str = "", device_key: str = ""):
    """阶段一：report → 展示验证码 → 临时 MQTT 等待绑定，返回 (device_id, device_key)。

    当 args.device_key 存在时（预烧设备解绑后重绑），使用签名 Report（情况1）；
    否则使用普通 Report（情况2）。
    """
    print(f"\n\033[1m{'─'*50}\033[0m\n 阶段一：未绑定上线 — 获取验证码并等待绑定\n\033[1m{'─'*50}\033[0m")
    # Credentials may have come from --creds-file rather than argparse.  In
    # that case args.device_id/device_key are still empty, so use the current
    # runtime credentials passed by the reset/rebind path.
    report_device_id = device_id or args.device_id
    report_device_key = device_key or args.device_key
    report_data = report_device(server, args.mac, report_device_id, report_device_key)
    code          = report_data["code"]
    temp_token    = report_data["temp_token"]
    temp_client_id = report_data.get("temp_client_id")
    if not temp_client_id:
        _err("服务端未返回 temp_client_id，请升级 device-server")
        sys.exit(1)
    print(f"\033[1;32m[device]\033[0m 验证码      : \033[1;30;103m {code} \033[0m  \033[92m← 设备 TTS 播报此 6 位数字\033[0m")
    print(f"\033[0;32m[device]\033[0m temp_token   : <hidden>")
    print(f"\033[0;32m[device]\033[0m temp_client  : {temp_client_id}")
    print()
    print(f"\033[1;96m[device]\033[0m ╔══════════════════════════════════════╗")
    print(f"\033[1;96m[device]\033[0m   请访问首页完成注册/登录")
    print(f"\033[1;96m[device]\033[0m   然后输入验证码: \033[1;30;103m {code} \033[0m")
    print(f"\033[1;96m[device]\033[0m ╚══════════════════════════════════════╝")
    _print_tts_guide(server, code)
    _print_bind_guide()
    print()
    result = connect_temp_mqtt(
        broker_host=broker_host,
        broker_port=broker_port,
        temp_client_id=temp_client_id,
        temp_token=temp_token,
        timeout_sec=args.timeout,
        use_tls=broker_tls,
    )
    # bind_ok means we already have credentials (pre-burned path);
    # auth_grant means we got new credentials (scan-code path).
    if result:
        device_id  = result["device_id"]
        device_key = result["device_key"]
    else:
        device_id = report_device_id
        device_key = report_device_key
    print()
    print(f"\033[0;32m[device]\033[0m ══════════════════════════════════════")
    print(f"\033[0;32m[device]\033[0m  绑定完成！本地持久化存储（Flash）：")
    print(f"\033[0;32m[device]\033[0m    device_id  = {device_id}")
    print(f"\033[0;32m[device]\033[0m    device_key = <hidden>")
    print(f"\033[0;32m[device]\033[0m    (来源: {'预烧凭证' if not result else 'auth_grant 下发'})")
    print(f"\033[0;32m[device]\033[0m ══════════════════════════════════════")
    return device_id, device_key


def main():
    parser = argparse.ArgumentParser(
        description="模拟 RTC 设备完整上线流程（未绑定→绑定→长连接）",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
示例:
  # 全流程（未绑定 → 等待 H5 输验证码 → 换 token → 长连接）
  python3 device_sim_main.py --mac AA:BB:CC:DD:EE:FF

  # 已绑定设备重启：直接换 token 并长连
  python3 device_sim_main.py --device-id DEV000001 --device-key key-test-000001

启动后默认实时推流；VoIP、AI 或设备通话建立时自动暂停，结束后恢复。
        """,
    )
    parser.add_argument("--mac",        default=os.getenv("DEVICE_MAC", "AA:BB:CC:DD:EE:FF"),
                        help="设备 MAC 地址")
    parser.add_argument("--device-id",  default=os.getenv("DEVICE_ID", ""),
                        dest="device_id", help="已知设备 ID（出厂内置凭证场景）")
    parser.add_argument("--device-key", default=os.getenv("DEVICE_KEY", ""),
                        dest="device_key", help="已知设备 KEY（出厂内置凭证场景）")
    parser.add_argument("--creds-file", default=os.getenv("CREDS_FILE", ""),
                        dest="creds_file", help="设备凭证文件完整路径（默认当前脚本所在目录下的 device_creds.json）")
    parser.add_argument("--timeout",    default=190, type=int,
                        help="等待绑定超时秒数（默认 190）")
    parser.add_argument("--with-mic", action="store_true", dest="with_mic",
                        help="Windows 使用本机麦克风/扬声器；上下行必须使用相同的 "
                             "alaw_8khz 或 alaw_16khz")
    _base = getattr(sys, "_MEIPASS", None) or os.path.join(os.path.dirname(__file__), "..")
    _assets = os.path.join(_base, "assets")
    _default_audio_file = os.path.join(_assets, DEFAULT_AUDIO_FILENAME)
    _default_video_file = os.path.join(_assets, "video.h264")
    media = parser.add_argument_group("通用媒体参数")
    media.add_argument("--up-audio-format", default=os.getenv("UP_AUDIO_FORMAT", "alaw_8khz"),
                       type=normalize_audio_format, choices=AUDIO_FORMAT_CHOICES,
                       help="上行音频格式（默认 alaw_8khz）")
    media.add_argument("--down-audio-format", default=os.getenv("DOWN_AUDIO_FORMAT", "alaw_8khz"),
                       type=normalize_audio_format, choices=AUDIO_FORMAT_CHOICES,
                       help="下行音频格式（默认 alaw_8khz）")
    media.add_argument("--up-audio-file", default=os.getenv("UP_AUDIO_FILE", _default_audio_file),
                       help="上行音频文件路径（默认 ../assets/audio.g711a）")
    media.add_argument("--up-video-file", default=os.getenv("UP_VIDEO_FILE", _default_video_file),
                       help="上行视频文件路径（默认 ../assets/video.h264）；空值表示纯音频")
    media.add_argument("--up-video-format", default=os.getenv("UP_VIDEO_FORMAT", "h264"),
                       choices=VIDEO_FORMAT_CHOICES,
                       help="上行视频格式（默认 h264）")
    media.add_argument("--down-video-format", default=os.getenv("DOWN_VIDEO_FORMAT", "h264"),
                       choices=VIDEO_FORMAT_CHOICES,
                       help="下行视频格式（默认 h264）")
    media.add_argument("--down-media-dir", default=os.getenv("DOWN_MEDIA_DIR",
                       os.path.join(os.path.dirname(__file__), "received")),
                       help="下行音视频保存目录（默认 ./received）")
    parser.add_argument("--endpoint",   default=os.getenv("SERVICES_BASE_URL", "http://ep-open.tangeopen.com"),
                        help="服务发现入口地址（GET {endpoint}/services）")
    parser.add_argument("--log-level",  default=os.getenv("LOG_LEVEL", "debug"),
                        dest="log_level", choices=["debug", "info", "warn", "error"],
                        help="日志级别（默认 debug）")
    parser.add_argument("--sdk-version", default=os.getenv("TIRTC_SDK_VERSION", DEFAULT_SDK_VERSION),
                        dest="sdk_version",
                        help="TiRTC SDK 版本（对应 sdk/<platform>/<version>/ 目录；默认 2.2.1，"
                             "也可用 TIRTC_SDK_VERSION 环境变量覆盖）")
    args = parser.parse_args()

    if args.with_mic and not sys.platform.startswith("win"):
        parser.error("--with-mic 仅支持 Windows；其他平台请使用媒体文件模式")
    if args.with_mic:
        try:
            validate_with_mic_audio_formats(
                args.up_audio_format,
                args.down_audio_format,
            )
        except ValueError as exc:
            parser.error(str(exc))
    if args.with_mic:
        missing_hw_audio_deps = _find_missing_deps(_OPTIONAL_HW_AUDIO_DEPS)
        if missing_hw_audio_deps:
            missing = " ".join(
                pip_name for _, pip_name in missing_hw_audio_deps
            )
            parser.error(
                "--with-mic 缺少硬件音频依赖，请先执行: "
                "python -m pip install -r requirements-audio.txt "
                f"（缺少: {missing}）"
            )
        try:
            import rtc_ai_hw as _rtc_ai_hw
        except (ImportError, OSError, SystemExit) as exc:
            parser.error(
                "--with-mic 无法加载硬件音频模块；请确认已执行 "
                "python -m pip install -r requirements-audio.txt："
                f"{exc}"
            )
    if AUDIO_FORMATS[args.down_audio_format].codec not in ("alaw", "amr", "opus"):
        parser.error("--down-audio-format 必须使用 VoIP 支持的 alaw/amr/opus 编码")

    if not credentials_are_paired(args.device_id, args.device_key):
        parser.error("--device-id 和 --device-key 必须成对提供（环境变量 DEVICE_ID/DEVICE_KEY 同样如此）")

    for label, path in (("上行音频", args.up_audio_file), ("上行视频", args.up_video_file)):
        if path and (not os.path.isfile(path) or os.path.getsize(path) == 0):
            parser.error(
                f"{label}素材不存在或为空: {path}。"
                "请先在 device-sim/device-sim-py 目录执行: bash ../scripts/gen_assets.sh"
            )
    try:
        os.makedirs(args.down_media_dir, exist_ok=True)
    except OSError as exc:
        parser.error(f"无法创建下行媒体目录 {args.down_media_dir}: {exc}")

    set_log_level(args.log_level)

    svc = fetch_services(base_url=args.endpoint)
    _server         = svc["device_server"]
    _broker_host    = svc["mqtt_host"]
    _broker_port    = svc["mqtt_port"]
    _broker_tls     = svc["mqtt_tls"]
    _voip_server    = svc["voip_server"]
    _ai_server      = svc["ai_server"]
    _call_server    = svc["call_server"]
    _tirtc_endpoint = os.getenv("TIRTC_ENDPOINT") or svc["tirtc_endpoint"]
    print(f"[device] TiRTC endpoint: {_tirtc_endpoint}")

    if _rtc_voip_available:
        _rtc_voip.set_log_level(args.log_level)
    if _rtc_stream_available:
        _rtc_stream.set_log_level(args.log_level)
    if _rtc_call_available:
        _rtc_call.set_log_level(args.log_level)

    if not all((_rtc_stream_available, _rtc_voip_available,
                _rtc_ai_available, _rtc_call_available)):
        print("\033[0;31m[device]\033[0m TiRTC Stream/VoIP/AI/Call 模块加载不完整，退出",
              file=sys.stderr, flush=True)
        sys.exit(1)
    print(f"\n\033[1m{'─'*50}\033[0m\n 模拟 RTC 设备上线流程\n\033[1m{'─'*50}\033[0m")

    device_id  = args.device_id
    device_key = args.device_key

    # ── 阶段一（验证码流程，仅在没有预存 ID+KEY 时执行）──────────────────────
    if not (device_id and device_key):
        device_id, device_key = _load_saved_creds(args.creds_file)
    if not (device_id and device_key):
        device_id, device_key = _bind_via_scan(
            args, _server, _broker_host, _broker_port, _broker_tls,
            device_id=device_id, device_key=device_key,
        )
        _save_creds(device_id, device_key, args.creds_file)
    else:
        print(f"\033[0;32m[device]\033[0m 使用预存凭证 device_id={device_id}，直接进入阶段二")
        _save_creds(device_id, device_key, args.creds_file)

    # ── 阶段二：换取 mqtt_token ────────────────────────────────────────────
    print(f"\n\033[1m{'─'*50}\033[0m\n 阶段二：已绑定上线\n\033[1m{'─'*50}\033[0m")
    try:
        mqtt_token = get_mqtt_token(_server, device_id, device_key, args.mac)
    except DeviceResetError as e:
        # 设备被解绑（6006）：user_id=0。上报 device_id 获取验证码，
        # 用户在 H5 扫码后，服务端直接绑这台设备（不分配新 ID），
        # 通过 MQTT auth_grant 下发原凭证。
        print(f"\033[1;33m[device]\033[0m {e}")
        print(f"\033[1;33m[device]\033[0m 重新进入验证码绑定流程（保留原 device_id={device_id}）")
        device_id, device_key = _bind_via_scan(
            args, _server, _broker_host, _broker_port, _broker_tls,
            device_id=device_id, device_key=device_key,
        )
        _save_creds(device_id, device_key, args.creds_file)
        mqtt_token = get_mqtt_token(_server, device_id, device_key, args.mac)

    # 主干：创建统一 RTC 运行时并启动实时流。通话切换由运行时内部协调。
    from device_rtc_runtime import DeviceRtcRuntime, RuntimeConfig
    _ai_mod = _rtc_ai_hw if args.with_mic else _rtc_ai
    import rtc_ai_session as _rtc_ai_session_mod
    _rtc_ai_session_mod.rtc_ai = _ai_mod
    runtime = DeviceRtcRuntime(
        RuntimeConfig(
            device_id=device_id, device_key=device_key, client_id=args.mac,
            mqtt_token=mqtt_token, tirtc_endpoint=_tirtc_endpoint,
            voip_server=_voip_server, ai_server=_ai_server, call_server=_call_server,
            up_audio_file=args.up_audio_file, up_video_file=args.up_video_file,
            down_media_dir=args.down_media_dir,
            up_audio_format=args.up_audio_format,
            down_audio_format=args.down_audio_format,
            up_video_format=args.up_video_format,
            down_video_format=args.down_video_format,
            hardware_audio=args.with_mic, log_level=args.log_level,
        ),
        _rtc_stream, _rtc_voip, _ai_mod, _rtc_call,
    )

    stop_event = threading.Event()

    def handle_sigint(sig, frame):
        del sig, frame
        print()
        stop_event.set()

    signal.signal(signal.SIGINT, handle_sigint)

    command_thread = None
    try:
        runtime.start()
        command_thread = threading.Thread(
            target=runtime.run_cmd_loop,
            args=(stop_event,),
            daemon=True,
            name="cmd-loop",
        )
        command_thread.start()
        connect_mqtt_blocking(
            _broker_host, _broker_port, device_id, mqtt_token,
            runtime.message_handler,
            stop_event,
            use_tls=_broker_tls,
        )
    finally:
        stop_event.set()
        if (command_thread is not None
                and command_thread is not threading.current_thread()):
            command_thread.join(timeout=COMMAND_THREAD_JOIN_TIMEOUT_SEC)
            if command_thread.is_alive():
                print(
                    "[device] 终端命令仍在结束中，继续关闭 RTC 运行时",
                    flush=True,
                )
        runtime.shutdown()


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
