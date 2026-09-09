#!/usr/bin/env python3
from __future__ import annotations
"""audio_device.py — 音频设备抽象（Windows: WASAPI/MME；Linux/macOS: PortAudio 默认）

依赖：sounddevice（requirements-audio.txt）、numpy/soxr（requirements.txt）
"""

try:
    import sounddevice as sd
    HAS_SD = True
except ImportError:
    sd = None
    HAS_SD = False

try:
    import soxr
    HAS_SOXR = True
except ImportError:
    soxr = None
    HAS_SOXR = False

import queue
import sys
import threading
import time

import numpy as np

SAMPLE_RATE = 16000
CHANNELS = 1
DTYPE = "int16"
AUDIO_PKT_MS = 40
AUDIO_PKT_BYTES = SAMPLE_RATE * 2 * CHANNELS * AUDIO_PKT_MS // 1000  # 1280 bytes PCM

# ── 设备选择关键词 ────────────────────────────────────────────────────────
_MIC_PREFER = [
    "array", "阵列", "airpods", "hands-free",
    "headset", "headphone", "耳机",
]
_SPK_PREFER = [
    "airpods", "headphone", "耳机",
    "speakers", "扬声器", "realtek",
]


def open_input_stream(device: "int | None", target_rate: int = 16000,
                      channels: int = 1, pkt_ms: int = 20) -> "tuple[object, int]":
    """打开麦克风输入流：target_rate → MME target_rate → 原生率+soxr

    返回 (RawInputStream, actual_rate)。多平台通用（Win/Mac/Linux）。
    """
    if not HAS_SD:
        raise RuntimeError("sounddevice 未安装")

    def _try(dev, rate):
        try:
            return sd.RawInputStream(
                samplerate=rate, channels=channels, dtype="int16",
                blocksize=rate * pkt_ms // 1000,
                device=dev, latency="low",
            )
        except sd.PortAudioError:
            return None

    s = _try(device, target_rate)
    if s:
        return s, target_rate

    # MME fallback (Windows only, safe to try everywhere)
    dev_info = sd.query_devices(device) if device is not None else None
    if dev_info is not None:
        mme_api = next((i for i, h in enumerate(sd.query_hostapis())
                        if "MME" in h["name"]), None)
        if mme_api is not None:
            for i, d in enumerate(sd.query_devices()):
                if (d["hostapi"] == mme_api and d["name"] == dev_info["name"]
                        and d["max_input_channels"] > 0):
                    s = _try(i, target_rate)
                    if s:
                        return s, target_rate
                    break

    # 兜底：原生采样率 + 调用方用 soxr 重采样
    native = int(sd.query_devices(device)["default_samplerate"])
    s = sd.RawInputStream(
        samplerate=native, channels=channels, dtype="int16",
        blocksize=native * pkt_ms // 1000,
        device=device, latency="low",
    )
    return s, native


def open_output_stream(device: "int | None", target_rate: int = 16000,
                       channels: int = 1, pkt_ms: int = 20) -> "tuple[object, int]":
    """打开扬声器输出流：target_rate → MME target_rate → 原生率+soxr

    返回 (RawOutputStream, actual_rate)。多平台通用（Win/Mac/Linux）。
    """
    if not HAS_SD:
        raise RuntimeError("sounddevice 未安装")

    def _try(dev, rate):
        try:
            return sd.RawOutputStream(
                samplerate=rate, channels=channels, dtype="int16",
                blocksize=rate * pkt_ms // 1000,
                device=dev, latency="low",
            )
        except sd.PortAudioError:
            return None

    s = _try(device, target_rate)
    if s:
        return s, target_rate

    dev_info = sd.query_devices(device) if device is not None else None
    if dev_info is not None:
        mme_api = next((i for i, h in enumerate(sd.query_hostapis())
                        if "MME" in h["name"]), None)
        if mme_api is not None:
            for i, d in enumerate(sd.query_devices()):
                if (d["hostapi"] == mme_api and d["name"] == dev_info["name"]
                        and d["max_output_channels"] > 0):
                    s = _try(i, target_rate)
                    if s:
                        return s, target_rate
                    break

    native = int(sd.query_devices(device)["default_samplerate"])
    s = sd.RawOutputStream(
        samplerate=native, channels=channels, dtype="int16",
        blocksize=native * pkt_ms // 1000,
        device=device, latency="low",
    )
    return s, native


def _find_mme_peer(device_idx: int, want_input: bool) -> "int | None":
    """找同名硬件的 MME 设备 index。"""
    if not HAS_SD:
        return None
    devices = sd.query_devices()
    hostapis = sd.query_hostapis()
    mme_api = next((i for i, h in enumerate(hostapis) if "MME" in h["name"]), None)
    if mme_api is None:
        return None
    target = devices[device_idx]["name"]
    ch_key = "max_input_channels" if want_input else "max_output_channels"
    for i, d in enumerate(devices):
        if d["hostapi"] == mme_api and d["name"] == target and d[ch_key] > 0:
            return i
    return None


# Configured once by the launcher before any media worker starts.
_selected_devices: "tuple[int, int] | None" = None


def _supported_api(hostapi):
    return sys.platform != "win32" or any(
        name in hostapi["name"] for name in ("WASAPI", "MME", "Windows Multi"))


def _resolve_device(index, want_input):
    if not HAS_SD:
        raise RuntimeError("请先安装硬件音频依赖：python -m pip install -r requirements-audio.txt")
    devices = sd.query_devices()
    apis = sd.query_hostapis()
    label = "麦克风" if want_input else "扬声器"
    key = "max_input_channels" if want_input else "max_output_channels"
    is_default = index is None
    if is_default:
        index = sd.default.device[0 if want_input else 1]
        # WASAPI exposes the Windows default endpoint without the MME mapper alias.
        if sys.platform == "win32":
            default_key = "default_input_device" if want_input else "default_output_device"
            for api in apis:
                candidate = api.get(default_key, -1)
                if "WASAPI" in api["name"] and candidate >= 0:
                    index = candidate
                    break
    if (not isinstance(index, int) or index < 0 or index >= len(devices)
            or devices[index][key] <= 0
            or not _supported_api(apis[devices[index]["hostapi"]])):
        source = "系统默认" if is_default else f"指定的 [{index}]"
        raise ValueError(f"{source}{label}不可用，请用 --list-audio-devices 查看并指定设备")
    return index


def configure_audio_devices(mic=None, speaker=None):
    """Validate both endpoints atomically; all media services share this selection."""
    global _selected_devices
    selected = (_resolve_device(mic, True), _resolve_device(speaker, False))
    devices = sd.query_devices()
    apis = sd.query_hostapis()
    _selected_devices = selected
    for label, index in zip(("麦克风", "扬声器"), selected):
        info = devices[index]
        print(f"[audio_device] {label}: [{index}] {info['name']} / {apis[info['hostapi']]['name']}")


def print_audio_devices():
    if not HAS_SD:
        raise RuntimeError("请先安装硬件音频依赖：python -m pip install -r requirements-audio.txt")
    devices = sd.query_devices()
    apis = sd.query_hostapis()
    defaults = []
    for want_input in (True, False):
        try:
            defaults.append(_resolve_device(None, want_input))
        except ValueError:
            defaults.append(None)
    print("[audio_device] 音频设备（编号用于 --mic-device / --speaker-device）")
    if not devices:
        print("[audio_device] 未发现音频设备，请连接设备后重启模拟器")
    for index, info in enumerate(devices):
        api = apis[info["hostapi"]]
        labels = []
        if index == defaults[0]:
            labels.append("默认输入")
        if index == defaults[1]:
            labels.append("默认输出")
        if not _supported_api(api):
            labels.append("不支持此音频接口")
        print(f"[{index}] {info['name']} / {api['name']} / "
              f"输入 {info['max_input_channels']} 声道 / 输出 {info['max_output_channels']} 声道 "
              + "、".join(labels))


def select_mic() -> "int | None":
    if _selected_devices is not None:
        return _selected_devices[0]
    if sys.platform == "win32":
        return _resolve_device(None, True)
    return _pick_device(_MIC_PREFER, want_input=True)


def select_speaker() -> "int | None":
    if _selected_devices is not None:
        return _selected_devices[1]
    if sys.platform == "win32":
        return _resolve_device(None, False)
    return _pick_device(_SPK_PREFER, want_input=False)


def _pick_device(keywords: list, want_input: bool) -> "int | None":
    if not HAS_SD:
        return None

    devices = sd.query_devices()
    ch_key = "max_input_channels" if want_input else "max_output_channels"

    # macOS / Linux: 所有 host API 都可用（CoreAudio / ALSA / PulseAudio）
    candidates = [
        (i, d) for i, d in enumerate(devices)
        if d[ch_key] > 0
    ]

    name_lower = [(i, d["name"].lower()) for i, d in candidates]
    for kw in keywords:
        for i, name in name_lower:
            if kw in name:
                return i

    if candidates:
        return candidates[0][0]
    return None


# ── MicCapture ─────────────────────────────────────────────────────────────

class MicCapture:
    """麦克风采集：16kHz mono int16 PCM"""

    def __init__(self, device: "int | None" = None, pkt_ms: int = AUDIO_PKT_MS):
        if not HAS_SD:
            raise RuntimeError("sounddevice 未安装，无法使用麦克风")
        if pkt_ms not in (20, 40):
            raise ValueError("麦克风帧长必须为 20 或 40ms")
        self._pkt_ms = pkt_ms
        self._packet_bytes = SAMPLE_RATE * 2 * pkt_ms // 1000
        self._device = device
        self._stream = None
        self._rate = SAMPLE_RATE
        self._resampler = None
        self._pcm_pending = bytearray()
        self._open()

    def _open(self):
        dev = self._device if self._device is not None else sd.default.device[0]
        frames = SAMPLE_RATE * self._pkt_ms // 1000
        self._resampler = None
        self._rate = SAMPLE_RATE

        # 三级策略：直开 → MME → 原生率+重采样
        s = self._try_open_input(dev, SAMPLE_RATE, self._pkt_ms)
        if s:
            self._stream = s
            self._rate = SAMPLE_RATE
            return

        mme = _find_mme_peer(dev, want_input=True)
        if mme is not None:
            s = self._try_open_input(mme, SAMPLE_RATE, self._pkt_ms)
            if s:
                self._stream = s
                self._rate = SAMPLE_RATE
                return

        if HAS_SOXR:
            native = int(sd.query_devices(dev)["default_samplerate"])
            frames = native * self._pkt_ms // 1000
            stream = sd.RawInputStream(
                samplerate=native, channels=CHANNELS, dtype=DTYPE,
                blocksize=frames, device=dev, latency="low",
            )
            try:
                stream.start()
            except Exception:
                stream.close()
                raise
            self._stream = stream
            self._resampler = soxr.ResampleStream(
                native, SAMPLE_RATE, CHANNELS,
                dtype='int16', quality='HQ',
            )
            self._rate = native
            print(f"[audio_device] 麦克风: 原生 {native}Hz → soxr 重采样到 16kHz")
        else:
            raise RuntimeError("无法以 16kHz 打开麦克风，且 soxr 未安装")

    @staticmethod
    def _try_open_input(device: int, rate: int, pkt_ms: int = AUDIO_PKT_MS) -> "sd.RawInputStream | None":
        frames = rate * pkt_ms // 1000
        stream = None
        try:
            stream = sd.RawInputStream(
                samplerate=rate, channels=CHANNELS, dtype=DTYPE,
                blocksize=frames, device=device, latency="low",
            )
            stream.start()
            return stream
        except sd.PortAudioError:
            if stream is not None:
                try:
                    stream.close()
                except Exception:
                    pass
            return None

    def read(self) -> bytes:
        """返回连续的指定帧长 PCM；重采样器输出按样本缓存，不补零或截断。"""
        while len(self._pcm_pending) < self._packet_bytes:
            for attempt in range(2):
                frames = self._rate * self._pkt_ms // 1000
                try:
                    pkt, overflowed = self._stream.read(frames)
                    if overflowed:
                        print("[audio_device] WARNING: 麦克风输入溢出", file=sys.stderr, flush=True)
                    break
                except sd.PortAudioError:
                    if attempt == 0:
                        print("[audio_device] 麦克风流已停止，尝试重新打开...", file=sys.stderr, flush=True)
                        self.close()
                        time.sleep(0.5)
                        self._open()
                    else:
                        raise
            pcm = np.frombuffer(bytes(pkt), dtype=np.int16)
            if self._resampler is not None:
                pcm = self._resampler.resample_chunk(pcm)
            self._pcm_pending.extend(pcm.tobytes())
        raw = bytes(self._pcm_pending[:self._packet_bytes])
        del self._pcm_pending[:self._packet_bytes]
        return raw

    def close(self):
        self._pcm_pending.clear()
        if self._stream:
            try:
                self._stream.stop()
                self._stream.close()
            except Exception:
                pass
            self._stream = None


# ── SpeakerPlayback ────────────────────────────────────────────────────────

class SpeakerPlayback:
    """扬声器播放：接收 PCM int16，自动重采样到扬声器原生率"""

    def __init__(self, device: "int | None" = None, diagnostic=False):
        if not HAS_SD:
            raise RuntimeError("sounddevice 未安装，无法使用扬声器")
        self._diagnostic = diagnostic
        self._written_frames = 0
        self._written_samples = 0
        self._playback_dropped = 0
        self._next_diagnostic = 0
        self._device = device
        self._stream = None
        self._native_rate = SAMPLE_RATE
        self._current_source_rate: "int | None" = None
        self._queue: "queue.Queue[tuple[bytes, int] | None]" = queue.Queue(maxsize=20)
        self._stop_event = threading.Event()
        self._resampler = None
        self._opened = threading.Event()
        self._thread = threading.Thread(
            target=self._worker, daemon=True, name="spk-worker",
        )
        self._thread.start()
        self._opened.wait(timeout=5.0)

    def _open_output(self, device: int, rate: int) -> "sd.RawOutputStream | None":
        frames = rate * AUDIO_PKT_MS // 1000
        try:
            return sd.RawOutputStream(
                samplerate=rate, channels=CHANNELS, dtype=DTYPE,
                blocksize=frames, device=device, latency="low",
            )
        except sd.PortAudioError:
            return None

    def _worker(self):
        import numpy as np
        dev = self._device if self._device is not None else sd.default.device[1]

        # 三级打开策略
        s = self._open_output(dev, SAMPLE_RATE)
        if not s:
            mme = _find_mme_peer(dev, want_input=False)
            if mme is not None:
                s = self._open_output(mme, SAMPLE_RATE)
            if not s and HAS_SOXR:
                native = int(sd.query_devices(dev)["default_samplerate"])
                self._native_rate = native
                frames = native * AUDIO_PKT_MS // 1000
                s = sd.RawOutputStream(
                    samplerate=native, channels=CHANNELS, dtype=DTYPE,
                    blocksize=frames, device=dev, latency="low",
                )
                self._resampler = soxr.ResampleStream(
                    SAMPLE_RATE, native, CHANNELS, dtype='int16', quality='HQ',
                )
                self._current_source_rate = SAMPLE_RATE
            elif not s:
                raise RuntimeError(f"无法打开扬声器设备 [{dev}]")

        self._stream = s
        native_frames = self._native_rate * AUDIO_PKT_MS // 1000
        silence = b'\x00' * native_frames * 2

        with self._stream:
            self._opened.set()
            while not self._stop_event.is_set():
                try:
                    item = self._queue.get(timeout=0.5)
                except queue.Empty:
                    self._stream.write(silence)
                    continue
                if item is None:
                    break
                data, source_rate = item

                # 动态重采样：当 source_rate 变化时重建 ResampleStream
                if source_rate != self._native_rate:
                    if (self._resampler is None
                            or self._current_source_rate != source_rate):
                        if HAS_SOXR:
                            self._resampler = soxr.ResampleStream(
                                source_rate, self._native_rate, CHANNELS,
                                dtype='int16', quality='HQ',
                            )
                            self._current_source_rate = source_rate
                        else:
                            # 没有 soxr 就无法重采样，跳过
                            continue
                    pcm = np.frombuffer(data, dtype=np.int16)
                    pcm = self._resampler.resample_chunk(pcm)
                    if len(pcm) == 0:
                        continue
                    data = pcm.tobytes()
                else:
                    self._resampler = None
                    self._current_source_rate = None
                try:
                    self._stream.write(data)
                    self._written_samples += len(data) // 2
                    self._written_frames += 1
                    if self._diagnostic and time.monotonic() >= self._next_diagnostic:
                        self._next_diagnostic = time.monotonic() + 2
                        print(f'[room-audio] 扬声器声卡写入 frames={self._written_frames} '
                              f'rate={self._native_rate} audio_ms={self._written_samples*1000//self._native_rate} '
                              f'queue={self._queue.qsize()} dropped={self._playback_dropped}', flush=True)
                except sd.PortAudioError:
                    if self._diagnostic:
                        print('[room-audio] 扬声器声卡写入失败，播放线程停止', flush=True)
                    break

    def play(self, data: bytes, source_rate: int = 16000):
        """推入播放队列。source_rate: 输入数据的采样率（默认 16kHz）。"""
        if not self._opened.is_set() or not self._thread.is_alive():
            if self._diagnostic and time.monotonic() >= self._next_diagnostic:
                self._next_diagnostic = time.monotonic() + 2
                print('[room-audio] 扬声器未就绪或播放线程已停止，音频未入队', flush=True)
            return
        try:
            self._queue.put_nowait((data, source_rate))
        except queue.Full:
            self._playback_dropped += 1
            try:
                self._queue.get_nowait()
            except queue.Empty:
                pass
            try:
                self._queue.put_nowait((data, source_rate))
            except queue.Full:
                pass

    def close(self):
        self._stop_event.set()
        try:
            self._queue.put_nowait(None)
        except queue.Full:
            pass
        if self._thread and self._thread.is_alive():
            self._thread.join(timeout=5.0)
        if self._stream:
            try:
                self._stream.stop()
                self._stream.close()
            except Exception:
                pass
            self._stream = None
        # Release nanobind-backed soxr objects before interpreter shutdown.
        self._resampler = None
        self._current_source_rate = None
        self._thread = None


# ── 自测 ───────────────────────────────────────────────────────────────────
if __name__ == "__main__":
    if not HAS_SD:
        print("sounddevice not installed, skipping audio_device test")
        sys.exit(0)

    mic_idx = select_mic()
    spk_idx = select_speaker()
    print(f"Selected mic: {mic_idx}, speaker: {spk_idx}")
    print("audio_device.py basic check passed")
