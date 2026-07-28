#!/usr/bin/env python3
from __future__ import annotations
"""rtc_echo_gate.py — 能量门控回声抑制模块

远端有声时衰减近端麦克风，远端安静时正常透传。半双工逻辑，不扭曲音频，
替代效果不佳的 AEC 算法。

接口与 AecProcessor 兼容，调用方无需修改。

用法:
    gate = EchoGate.create(sample_rate=16000, frame_ms=20)
    gate.feed_far_end(decoded_pcm, source_rate=8000)
    cleaned = gate.process(mic_pcm)
"""

import numpy as np


class EchoGate:
    """能量门控回声抑制器。

    远端有声时对近端信号施加 attenuation_db 衰减，远端安静时透传。
    起效/释放时间用指数平滑，避免咔嗒声。
    """

    def __init__(self, sample_rate: int, frame_bytes: int,
                 attenuation_db: float = 20.0,
                 attack_ms: float = 5.0, release_ms: float = 50.0):
        self.sample_rate = sample_rate
        self.frame_bytes = frame_bytes
        self._frame_samples = frame_bytes // 2
        self._attenuation_mult = 10.0 ** (-attenuation_db / 20.0)

        # 平滑系数：每帧更新一次
        fps = 1000.0 / 20.0  # frames per second (20ms frames)
        self._attack_coeff = np.exp(-1.0 / (attack_ms / 1000.0 * fps))
        self._release_coeff = np.exp(-1.0 / (release_ms / 1000.0 * fps))

        # 状态
        self._far_energy = 0.0       # 远端能量（平滑后）
        self._gate = 1.0             # 当前门控增益（1.0=透传, mult=衰减）

        # 远端重采样（延迟创建，按需）
        self._far_resampler = None
        self._far_resampler_rates: "tuple[int,int] | None" = None

    @staticmethod
    def create(sample_rate: int = 16000, frame_ms: int = 20,
               attenuation_db: float = 20.0) -> "EchoGate":
        """工厂方法：创建 EchoGate 实例。"""
        frame_size = sample_rate * frame_ms // 1000
        return EchoGate(sample_rate, frame_size * 2, attenuation_db)

    # ── 公共接口（与 AecProcessor 兼容） ──────────────────────────────────

    def feed_far_end(self, pcm: bytes, source_rate: int = 0) -> None:
        """喂入远端参考信号，检测远端是否在说话。"""
        if source_rate > 0 and source_rate != self.sample_rate:
            pcm = self._resample_far(pcm, source_rate, self.sample_rate)

        pcm = pcm[:self.frame_bytes]
        if len(pcm) < self.frame_bytes:
            pcm = pcm + b'\x00' * (self.frame_bytes - len(pcm))

        samples = np.frombuffer(pcm, dtype=np.int16)
        if len(samples) == 0:
            return

        energy = float(np.sqrt(np.mean(samples.astype(np.float64) ** 2)))

        # 指数平滑远端能量
        self._far_energy = (self._attack_coeff * self._far_energy +
                            (1.0 - self._attack_coeff) * energy)

        # 滞回阈值：避免边缘抖动
        # 远端 RMS > 500 → 有人在说话，衰减近端
        # 远端 RMS < 200 → 远端静音，透传近端
        gate_active = self._gate < 0.5  # 当前是否已在衰减状态
        if gate_active:
            threshold = 200.0   # 退出衰减需要更低能量
        else:
            threshold = 500.0   # 进入衰减需要更高能量

        if self._far_energy > threshold:
            target = self._attenuation_mult
        else:
            target = 1.0

        # 攻击快、释放慢
        coeff = (self._attack_coeff if target < self._gate
                 else self._release_coeff)
        self._gate = coeff * self._gate + (1.0 - coeff) * target

    def process(self, near_pcm: bytes) -> bytes:
        """处理近端信号。远端活跃时施加衰减，远端安静时透传。"""
        pcm = near_pcm[:self.frame_bytes]
        if len(pcm) < self.frame_bytes:
            pcm = pcm + b'\x00' * (self.frame_bytes - len(pcm))

        if self._gate >= 0.999:
            return pcm

        samples = np.frombuffer(pcm, dtype=np.int16)
        attenuated = (samples.astype(np.float64) * self._gate)
        return attenuated.astype(np.int16).tobytes()

    def close(self) -> None:
        self._far_resampler = None
        self._far_resampler_rates = None

    # ── 内部 ────────────────────────────────────────────────────────────

    def _resample_far(self, pcm: bytes, from_rate: int, to_rate: int) -> bytes:
        """流式重采样远端 PCM 以匹配门控采样率。"""
        import soxr

        rates = (from_rate, to_rate)
        if self._far_resampler is None or self._far_resampler_rates != rates:
            self._far_resampler = soxr.ResampleStream(
                from_rate, to_rate, 1, dtype='int16', quality='HQ',
            )
            self._far_resampler_rates = rates

        samples = np.frombuffer(pcm, dtype=np.int16)
        if len(samples) == 0:
            return b'\x00' * self.frame_bytes
        resampled = self._far_resampler.resample_chunk(samples)
        if len(resampled) == 0:
            return b'\x00' * self.frame_bytes
        return resampled.astype(np.int16).tobytes()