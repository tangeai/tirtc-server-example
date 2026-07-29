#!/usr/bin/env python3
from __future__ import annotations
"""rtc_call_media.py — 音频格式、文件推流、硬件音频采集/播放

从 rtc_call.py 拆出，负责：
  - 音频格式定义（AUDIO_FORMATS、远端格式检测）
  - 文件模式推流（_media_worker）
  - 硬件模式采集/播放（_mic_worker、_speaker_worker）
  - 接收帧处理（写文件、解码、推扬声器队列）
"""

import ctypes
import os
import sys
import threading
import time
import queue

from audio_recorder import AudioRecorder
from media_postprocess import convert_video_to_mp4

import tirtc_sdk as sdk
from tirtc_sdk import (
    TIRTCFRAMEINFO,
    AUDIO_STREAM_ID, VIDEO_STREAM_ID,
    TIRTC_AUDIO_ALAW, TIRTC_AUDIO_PCM,
    TIRTC_FRAME_FLAG_KEY_FRAME,
    TIRTC_AUDIOSAMPLE_8K16B1C, TIRTC_AUDIOSAMPLE_16K16B1C,
    CONN_FATAL_ERRORS,
)
from media_file_reader import AudioFileReader, VideoFileReader
from media_formats import (
    AUDIO_FORMATS,
    VIDEO_FORMATS,
    audio_packet_bytes,
    normalize_audio_format,
    supports_live_audio_capture,
    video_file_extension,
)
from sdk_callback_guard import join_worker_before_uninit

try:
    from rtc_echo_gate import EchoGate
    _ECHO_GATE_AVAILABLE = True
except ImportError:
    EchoGate = None
    _ECHO_GATE_AVAILABLE = False

# 半双工门控：远端有声时衰减近端麦克风（默认 30dB）
_ECHO_GATE_ACTIVE = True
_ECHO_GATE_ATTEN_DB = 24

try:
    from g711 import alaw_encode, alaw_decode
except ImportError:
    alaw_encode = alaw_decode = None


# ── 日志 ──────────────────────────────────────────────────────────────────────
_LOG_LEVEL = 10

def set_log_level(level: str) -> None:
    global _LOG_LEVEL
    _LOG_LEVEL = {"debug": 10, "info": 20, "warn": 30, "error": 40}.get(level.lower(), 10)

def _log(msg):
    if _LOG_LEVEL <= 10:
        print(f"\033[0;36m[rtc_call]\033[0m {msg}", flush=True)
def _info(msg):
    if _LOG_LEVEL <= 20:
        print(f"\033[0;32m[rtc_call]\033[0m {msg}", flush=True)
def _warn(msg):
    if _LOG_LEVEL <= 30:
        print(f"\033[1;33m[rtc_call]\033[0m {msg}", flush=True)
def _err(msg):
    if _LOG_LEVEL <= 40:
        print(f"\033[0;31m[rtc_call]\033[0m {msg}", file=sys.stderr, flush=True)


AUDIO_PKT_MS   = 20
VIDEO_FRAME_MS = 1000 / 15  # 15fps


# ── 媒体流状态 ────────────────────────────────────────────────────────────────
_hw_audio       = False
_hw_audio_fmt   = "alaw_8khz"
_hw_mic_device: "int | None" = None
_hw_spk_device: "int | None" = None
_hw_spk: "object | None" = None
_play_queue: "queue.Queue | None" = None

_send_audio_path: str = ""
_send_video_path: str = ""
_recv_dir: str = ""
_device_id: str = ""
_send_audio_fmt: str = "alaw_8khz"
_send_video_fmt: str = "h264"
_recv_video_fmt: str = "h264"
_recv_recorder: AudioRecorder | None = None
_recv_vf: "object | None" = None
_recv_video_path: str = ""
_stream_stop = threading.Event()
_stream_thread: "threading.Thread | None" = None
_mic_thread: "threading.Thread | None" = None
_speaker_thread: "threading.Thread | None" = None
_video_thread: "threading.Thread | None" = None

# 当前连接的 handle（由 rtc_call.py 通过 set_hconn 更新）
_hconn: "ctypes.c_void_p | None" = None

# 当前设备通话的视频策略。call_type 决定 capability，SDK 的
# subscribe/unsubscribe 回调只在 capability 范围内动态启停视频。
_video_state_lock = threading.Lock()
_session_video_capable = False
_video_enabled = False
_video_generation = 0

# 回声门控（替代 AEC，远端有声时衰减近端麦克风）
_echo_gate = None


# ── 公共接口 ──────────────────────────────────────────────────────────────────

def set_hconn(hconn_val: "int | None") -> None:
    """rtc_call.py 在每次连接状态变化时调用，更新当前连接句柄。"""
    global _hconn
    _hconn = ctypes.c_void_p(hconn_val) if hconn_val is not None else None


def configure(device_id: str, send_audio: str, send_video: str, recv_dir: str,
              audio_fmt: str = "alaw_8khz", up_video_fmt: str = "h264",
              down_video_fmt: str = "h264") -> None:
    """配置待发送的媒体文件和接收目录。"""
    global _device_id, _send_audio_path, _send_video_path, _recv_dir
    global _send_audio_fmt, _send_video_fmt, _recv_video_fmt
    _device_id = device_id
    _send_audio_path = send_audio
    _send_video_path = send_video
    _recv_dir = recv_dir
    _send_audio_fmt = normalize_audio_format(audio_fmt)
    _send_video_fmt = up_video_fmt
    _recv_video_fmt = down_video_fmt
    # Preserve the standalone rtc_call_media API default: configured video is
    # enabled until the call session explicitly prepares an audio-only call.
    prepare_session(bool(send_video))


def prepare_session(with_video: bool) -> None:
    """Prepare media policy before P2P connection establishment."""
    global _session_video_capable, _video_enabled, _video_generation
    capable = bool(with_video and _send_video_path)
    with _video_state_lock:
        _session_video_capable = capable
        _video_enabled = capable
        _video_generation += 1


def reset_session() -> None:
    global _session_video_capable, _video_enabled, _video_generation
    with _video_state_lock:
        _session_video_capable = False
        _video_enabled = False
        _video_generation += 1


def subscribe_video(stream_id: int) -> bool:
    """Enable video only when this is a configured video call."""
    global _video_enabled, _video_generation
    with _video_state_lock:
        if stream_id != VIDEO_STREAM_ID or not _session_video_capable:
            return False
        _video_enabled = True
        _video_generation += 1
        return True


def unsubscribe_video(stream_id: int) -> bool:
    """Stop video without affecting the audio sender."""
    global _video_enabled, _video_generation
    with _video_state_lock:
        if stream_id != VIDEO_STREAM_ID:
            return False
        _video_enabled = False
        _video_generation += 1
        return True


def request_video_key_frame(stream_id: int) -> bool:
    """Force the next enabled video frame to come from a real key frame."""
    global _video_generation
    with _video_state_lock:
        if (
            stream_id != VIDEO_STREAM_ID
            or not _session_video_capable
            or not _video_enabled
        ):
            return False
        _video_generation += 1
        return True


def _video_state() -> tuple[bool, bool, int]:
    with _video_state_lock:
        return (
            _session_video_capable,
            _video_enabled,
            _video_generation,
        )


def configure_hardware_audio(enable: bool, fmt: str = "alaw_8khz",
                              mic_device=None, spk_device=None) -> None:
    """启用硬件麦克风/扬声器。"""
    global _hw_audio, _hw_audio_fmt, _hw_mic_device, _hw_spk_device, _hw_spk
    if not enable:
        _hw_audio = False
        return
    fmt = normalize_audio_format(fmt)
    if fmt not in AUDIO_FORMATS:
        _err(f"不支持的音频格式: {fmt}，可选: {list(AUDIO_FORMATS.keys())}")
        return
    if not supports_live_audio_capture(fmt):
        _err(f"实时麦克风不支持 {fmt}，仅支持 pcm/g711a")
        return
    _hw_audio = True
    _hw_audio_fmt = fmt

    try:
        from audio_device import SpeakerPlayback, select_mic, select_speaker
    except (ImportError, RuntimeError) as e:
        _err(f"无法加载音频设备: {e}")
        _err("请安装依赖: pip install sounddevice numpy soxr")
        _hw_audio = False
        return

    mic_dev = mic_device if mic_device is not None else select_mic()
    spk_dev = spk_device if spk_device is not None else select_speaker()
    _info(f"硬件音频: fmt={fmt} mic_dev={mic_dev} spk_dev={spk_dev}")
    _warn("⚠ 建议将电脑音量调至 15% 左右，防止扬声器回声")
    _hw_mic_device = mic_dev
    _hw_spk_device = spk_dev

    try:
        _hw_spk = SpeakerPlayback(spk_dev)
    except Exception as e:
        _err(f"打开扬声器设备失败: {e}")
        _hw_audio = False
        _hw_spk = None


def is_hw_audio() -> bool:
    return _hw_audio


def _has_running_media_threads() -> bool:
    return any(
        thread is not None and thread.is_alive()
        for thread in (_stream_thread, _mic_thread, _speaker_thread, _video_thread)
    )


def _close_receive_outputs(convert_video: bool = False) -> None:
    global _recv_recorder, _recv_vf, _recv_video_path

    if _recv_vf is not None:
        try:
            _recv_vf.close()
        except Exception:
            pass
        _recv_vf = None

    video_path, _recv_video_path = _recv_video_path, ""
    if convert_video and video_path:
        convert_video_to_mp4(video_path, _recv_video_fmt, _info, _warn)

    if _recv_recorder is not None:
        _recv_recorder.close()
        _recv_recorder = None


def start() -> None:
    """建连成功后启动媒体收发。"""
    global _recv_recorder, _recv_vf, _stream_thread, _stream_stop
    global _mic_thread, _speaker_thread, _video_thread
    global _play_queue

    if _has_running_media_threads():
        _warn("媒体流已在运行，忽略重复 start()")
        return
    # 音频设备下标为 None 表示使用 sounddevice 的系统默认设备，并不表示
    # 硬件不可用。扬声器对象创建成功即可进入硬件音频路径。
    hardware_audio_ready = _hw_audio and _hw_spk is not None
    if not hardware_audio_ready and not _send_audio_path:
        _warn("未配置发送音频文件，跳过媒体流")
        return

    out_dir = os.path.join(_recv_dir, _device_id)
    os.makedirs(out_dir, exist_ok=True)

    global _echo_gate
    global _recv_video_path
    try:
        _recv_recorder = AudioRecorder(_recv_dir, _device_id, info=_info, warn=_warn)
        _recv_recorder.open()
        video_capable, _, _ = _video_state()
        if video_capable:
            _recv_video_path = os.path.join(
                out_dir, f"received_video.{video_file_extension(_recv_video_fmt)}")
            _recv_vf = open(_recv_video_path, "wb")
        else:
            _recv_video_path = ""
            _recv_vf = None
        _info(f"接收目录: {out_dir}")

        _stream_stop.clear()

        # 硬件模式下创建回声门控（替代 AEC）
        if hardware_audio_ready and _ECHO_GATE_AVAILABLE and _ECHO_GATE_ACTIVE:
            fmt_cfg = AUDIO_FORMATS[_hw_audio_fmt]
            gate_rate = fmt_cfg.sample_rate
            _echo_gate = EchoGate.create(sample_rate=gate_rate, frame_ms=20, attenuation_db=_ECHO_GATE_ATTEN_DB)
            _info(f"回声门控已启用 ({gate_rate}Hz, 衰减 {_ECHO_GATE_ATTEN_DB}dB)")
        else:
            _echo_gate = None
    except Exception as e:
        if _echo_gate is not None:
            _echo_gate.close()
            _echo_gate = None
        _close_receive_outputs(convert_video=False)
        _err(f"媒体流初始化失败: {e}")
        return

    if hardware_audio_ready:
        _play_queue = queue.Queue(maxsize=20)
        fmt_cfg = AUDIO_FORMATS[_hw_audio_fmt]

        _mic_thread = threading.Thread(
            target=_mic_worker, args=(fmt_cfg,), daemon=True, name="call-mic")
        _speaker_thread = threading.Thread(
            target=_speaker_worker, daemon=True, name="call-speaker")
        _mic_thread.start()
        _speaker_thread.start()
        if video_capable:
            _video_thread = threading.Thread(
                target=_video_only_worker, daemon=True, name="call-video")
            _video_thread.start()
            _info("媒体流已启动（硬件音频 + 文件视频）")
        else:
            _video_thread = None
            _info("媒体流已启动（硬件音频，无视频）")
    else:
        _mic_thread = None
        _speaker_thread = None
        _video_thread = None

        hconn_val = _hconn.value if _hconn else 0
        _stream_thread = threading.Thread(
            target=_media_worker, args=(hconn_val,), daemon=True, name="call-media",
        )
        _stream_thread.start()
        if video_capable:
            _info("媒体流已启动（文件音频 + 文件视频）")
        else:
            _info("媒体流已启动（文件音频，无视频）")


def stop() -> None:
    """停止媒体推流线程并关闭文件。"""
    global _recv_recorder, _recv_vf, _recv_video_path, _stream_thread
    global _mic_thread, _speaker_thread, _video_thread
    global _play_queue, _echo_gate

    if _echo_gate is not None:
        _echo_gate.close()
        _echo_gate = None

    _stream_stop.set()

    if _play_queue is not None:
        try:
            _play_queue.put_nowait(None)
        except queue.Full:
            pass

    if _stream_thread and _stream_thread.is_alive():
        join_worker_before_uninit(_stream_thread, _warn, "设备通话媒体")
    _stream_thread = None
    for attr in ("_mic_thread", "_speaker_thread", "_video_thread"):
        thread = globals()[attr]
        if thread is not None and thread.is_alive():
            join_worker_before_uninit(thread, _warn, f"设备通话 {attr}")
        globals()[attr] = None

    _play_queue = None
    _close_receive_outputs(convert_video=True)
    reset_session()
    _log("媒体流已停止")


def shutdown() -> None:
    """Release process-lifetime hardware audio resources."""
    global _hw_audio, _hw_spk, _hw_mic_device, _hw_spk_device
    stop()
    if _hw_spk is not None:
        _hw_spk.close()
        _hw_spk = None
    _hw_audio = False
    _hw_mic_device = None
    _hw_spk_device = None


def on_audio_frame(fi: TIRTCFRAMEINFO, buf: bytes) -> None:
    """接收音频帧：写文件 + 硬件模式下解码推入播放队列。"""
    global _recv_recorder

    if _recv_recorder is not None:
        _recv_recorder.write_frame(fi, buf)

    if _hw_audio and _play_queue is not None and _hw_spk is not None:
        if _recv_recorder is not None and _recv_recorder.frame_count == 1:
            from audio_recorder import MEDIA_TO_FFMPEG_FMT
            _recv_fmt = MEDIA_TO_FFMPEG_FMT.get(fi.media, str(fi.media))
            _recv_khz = 16 if fi.flags == TIRTC_AUDIOSAMPLE_16K16B1C else 8
            _info(f"收到远端音频格式: {_recv_fmt} {_recv_khz}kHz {fi.length}bytes/帧")

        if fi.media == TIRTC_AUDIO_ALAW:
            if alaw_decode is None:
                return
            pcm = alaw_decode(buf)
        elif fi.media == TIRTC_AUDIO_PCM:
            pcm = buf
        else:
            return

        source_rate = 16000 if fi.flags == TIRTC_AUDIOSAMPLE_16K16B1C else 8000

        # 喂入回声门控远端参考（必须在推扬声器之前）
        # 远端采样率可能与门控不同（如 8kHz G.711a → 16kHz），
        # feed_far_end 内部会自动重采样对齐
        if _echo_gate is not None:
            _echo_gate.feed_far_end(pcm, source_rate=source_rate)

        try:
            _play_queue.put_nowait((pcm, source_rate))
        except queue.Full:
            try:
                _play_queue.get_nowait()
            except queue.Empty:
                pass
            try:
                _play_queue.put_nowait((pcm, source_rate))
            except queue.Full:
                pass


def on_video_frame(buf: bytes) -> None:
    """接收视频帧：写文件。"""
    if _recv_vf is not None:
        _recv_vf.write(buf)


# ── 硬件：麦克风采集 ──────────────────────────────────────────────────────────
# _open_mic_input 已收敛到 audio_device.open_input_stream

def _mic_worker(fmt_cfg) -> None:
    """麦克风采集 → 编码 → TiRtcSendAudioStream"""
    import numpy as np
    import sounddevice as _sd

    target_rate = fmt_cfg.sample_rate
    encode_mode = fmt_cfg.codec
    pkt_bytes = audio_packet_bytes(fmt_cfg, AUDIO_PKT_MS)
    pad_byte = b'\xd5' if encode_mode == "alaw" else b'\x00'
    device = _hw_mic_device if _hw_mic_device is not None else _sd.default.device[0]

    try:
        from audio_device import open_input_stream as _open_mic
        mic, actual_rate = _open_mic(device, target_rate)
        dev_name = _sd.query_devices(device)["name"]
        rate_note = (f"{actual_rate}Hz"
                     if actual_rate == target_rate
                     else f"{actual_rate}Hz → soxr → {target_rate}Hz")
        _info(f"麦克风采集启动 device=[{device}] {dev_name} {rate_note}")

        # 发送格式
        _media_names = {getattr(sdk, a): a for a in dir(sdk) if a.startswith("TIRTC_AUDIO_")}
        _media_label = _media_names.get(fmt_cfg.media, str(fmt_cfg.media))
        _flag_label = "16K16B1C" if fmt_cfg.flags == getattr(sdk, "TIRTC_AUDIOSAMPLE_16K16B1C", -1) else "8K16B1C"
        _info(f"发送音频格式: {_media_label} {_flag_label} {target_rate}Hz encode={encode_mode} pkt={pkt_bytes}bytes")

        import soxr as _soxr
        resampler = (_soxr.ResampleStream(actual_rate, target_rate, 1,
                                          dtype='int16', quality='HQ')
                     if actual_rate != target_rate else None)

        audio_ts_ms = 0
        frames = actual_rate * AUDIO_PKT_MS // 1000
        with mic:
            while not _stream_stop.is_set():
                try:
                    pkt_data, overflowed = mic.read(frames)
                except _sd.PortAudioError:
                    _err("麦克风流意外停止")
                    break
                if overflowed:
                    _warn("麦克风输入溢出")

                pcm = np.frombuffer(bytes(pkt_data), dtype=np.int16)
                if resampler is not None:
                    pcm = resampler.resample_chunk(pcm)
                    if len(pcm) == 0:
                        continue

                # 回声门控（替代 AEC）
                if _echo_gate is not None:
                    pcm_bytes = pcm.tobytes()
                    fb = _echo_gate.frame_bytes
                    if len(pcm_bytes) < fb:
                        pcm_bytes = pcm_bytes + b'\x00' * (fb - len(pcm_bytes))
                    elif len(pcm_bytes) > fb:
                        pcm_bytes = pcm_bytes[:fb]
                    cleaned = _echo_gate.process(pcm_bytes)
                    pcm = np.frombuffer(cleaned, dtype=np.int16)

                if encode_mode == "alaw":
                    if alaw_encode is None:
                        break
                    raw = alaw_encode(pcm.tobytes())
                else:
                    raw = pcm.tobytes()

                if len(raw) < pkt_bytes:
                    raw = raw + pad_byte * (pkt_bytes - len(raw))
                elif len(raw) > pkt_bytes:
                    raw = raw[:pkt_bytes]

                fi = TIRTCFRAMEINFO()
                fi.stream_id = AUDIO_STREAM_ID
                fi.media     = fmt_cfg.media
                fi.flags     = fmt_cfg.flags
                fi.reserved  = 0
                fi.ts        = int(audio_ts_ms) & 0xFFFFFFFF
                fi.length    = len(raw)

                if _hconn is None:
                    break
                buf = (ctypes.c_uint8 * len(raw)).from_buffer_copy(raw)
                rc = sdk.TiRtcSendAudioStream(_hconn, ctypes.byref(fi), buf)
                if rc in CONN_FATAL_ERRORS:
                    _log("连接已关闭，退出麦克风采集")
                    break
                audio_ts_ms += AUDIO_PKT_MS

    except _sd.PortAudioError as e:
        _err(f"麦克风打开失败: {e}")
    except Exception as e:
        _err(f"麦克风线程异常: {e}")
        import traceback
        traceback.print_exc()
    finally:
        _log("麦克风采集线程退出")


# ── 硬件：扬声器播放 ──────────────────────────────────────────────────────────

def _speaker_worker() -> None:
    """从 _play_queue 取帧 → 扬声器播放"""
    try:
        while not _stream_stop.is_set():
            try:
                item = _play_queue.get(timeout=0.5)
            except queue.Empty:
                continue
            if item is None:
                break
            data, source_rate = item
            _hw_spk.play(data, source_rate=source_rate)
    except Exception as e:
        _err(f"扬声器线程异常: {e}")
    finally:
        _log("扬声器播放线程退出")


# ── 硬件模式：纯视频发送 ──────────────────────────────────────────────────────

def _video_only_worker() -> None:
    """文件视频 → TiRtcSendVideoStream"""
    video_pts_ms = 0
    first_video = True
    wall_start_ms = _now_ms()
    seen_generation = -1

    try:
        reader = VideoFileReader(_send_video_path, _send_video_fmt)
        while not _stream_stop.is_set():
            _, video_enabled, generation = _video_state()
            if not video_enabled:
                time.sleep(0.02)
                continue
            if generation != seen_generation:
                video_pts_ms = max(0, _now_ms() - wall_start_ms)
                first_video = True
                seen_generation = generation
            result = reader.next_frame(force_key=first_video)
            if result is None:
                break
            frame_data, is_key = result

            fi = TIRTCFRAMEINFO()
            fi.stream_id = VIDEO_STREAM_ID
            fi.media     = VIDEO_FORMATS[_send_video_fmt].media
            fi.flags     = TIRTC_FRAME_FLAG_KEY_FRAME if (is_key or first_video) else 0
            fi.reserved  = 0
            fi.ts        = int(video_pts_ms) & 0xFFFFFFFF
            fi.length    = len(frame_data)

            if _hconn is None:
                break
            buf = (ctypes.c_uint8 * len(frame_data)).from_buffer_copy(frame_data)
            rc = sdk.TiRtcSendVideoStream(_hconn, ctypes.byref(fi), buf)
            if rc in CONN_FATAL_ERRORS:
                break
            first_video = False
            video_pts_ms += VIDEO_FRAME_MS

            elapsed = _now_ms() - wall_start_ms
            if video_pts_ms > elapsed:
                time.sleep((video_pts_ms - elapsed) / 1000.0)
    except Exception as e:
        _err(f"视频推流线程异常: {e}")
    finally:
        _log("视频推流线程退出")


# ── 工具 ──────────────────────────────────────────────────────────────────────

def _now_ms() -> int:
    return int(time.monotonic() * 1000)


# ── 文件模式：媒体推流 worker ─────────────────────────────────────────────────

def _media_worker(hconn_val: int) -> None:
    """媒体推流线程：循环读取音视频文件发送。"""
    hconn = ctypes.c_void_p(hconn_val)
    audio_spec = AUDIO_FORMATS[_send_audio_fmt]
    has_video, _, _ = _video_state()
    video_spec = VIDEO_FORMATS[_send_video_fmt] if has_video else None
    audio_reader = AudioFileReader(_send_audio_path, _send_audio_fmt, AUDIO_PKT_MS)
    video_reader = VideoFileReader(_send_video_path, _send_video_fmt) if has_video else None

    audio_pts_ms  = 0
    video_pts_ms  = 0.0 if has_video else float("inf")
    first_video   = True
    wall_start_ms = _now_ms()
    seen_video_generation = -1

    def _send_audio_pkt() -> bool:
        nonlocal audio_pts_ms
        packet = audio_reader.next_packet()
        if packet is None:
            return False
        pkt, duration_ms = packet.payload, packet.duration_ms

        fi = TIRTCFRAMEINFO()
        fi.stream_id = AUDIO_STREAM_ID
        fi.media     = audio_spec.media
        fi.flags     = audio_spec.flags
        fi.reserved  = 0
        fi.ts        = int(audio_pts_ms) & 0xFFFFFFFF
        fi.length    = len(pkt)

        buf = (ctypes.c_uint8 * len(pkt)).from_buffer_copy(pkt)
        rc = sdk.TiRtcSendAudioStream(hconn, ctypes.byref(fi), buf)
        if rc in CONN_FATAL_ERRORS:
            return False
        audio_pts_ms += duration_ms
        return True

    def _send_video_frame() -> bool:
        nonlocal video_pts_ms, first_video
        if video_reader is None or video_spec is None:
            return False
        result = video_reader.next_frame(force_key=first_video)
        if result is None:
            return False
        frame_data, is_key = result

        fi = TIRTCFRAMEINFO()
        fi.stream_id = VIDEO_STREAM_ID
        fi.media     = video_spec.media
        fi.flags     = TIRTC_FRAME_FLAG_KEY_FRAME if (is_key or first_video) else 0
        fi.reserved  = 0
        fi.ts        = int(video_pts_ms) & 0xFFFFFFFF
        fi.length    = len(frame_data)

        buf = (ctypes.c_uint8 * len(frame_data)).from_buffer_copy(frame_data)
        rc = sdk.TiRtcSendVideoStream(hconn, ctypes.byref(fi), buf)
        if rc in CONN_FATAL_ERRORS:
            return False
        first_video = False
        video_pts_ms += VIDEO_FRAME_MS
        return True

    try:
        while not _stream_stop.is_set():
            _, video_enabled, video_generation = _video_state()
            video_enabled = bool(has_video and video_enabled)
            if video_enabled and video_generation != seen_video_generation:
                video_pts_ms = float(audio_pts_ms)
                first_video = True
                seen_video_generation = video_generation
            target_pts = (
                audio_pts_ms
                if not video_enabled
                else min(audio_pts_ms, video_pts_ms)
            )
            elapsed    = _now_ms() - wall_start_ms
            wait_ms    = target_pts - elapsed
            if wait_ms > 2:
                time.sleep(wait_ms / 1000.0)
                continue

            if not video_enabled or audio_pts_ms <= video_pts_ms:
                if not _send_audio_pkt():
                    break
            else:
                if not _send_video_frame():
                    break
    except Exception as e:
        _err(f"媒体推流线程异常: {e}")
        import traceback
        traceback.print_exc()
    finally:
        _log("媒体推流线程退出")
