#!/usr/bin/env python3
"""g711.py — ITU-T G.711 A-law 编解码（基于 audioop 兼容模块）"""

import audioop


def alaw_encode(pcm16: bytes) -> bytes:
    """16-bit PCM（little-endian）→ 8-bit A-law（标准 ITU-T G.711）。"""
    return audioop.lin2alaw(pcm16, 2)


def alaw_decode(alaw: bytes) -> bytes:
    """8-bit A-law → 16-bit PCM（little-endian）。"""
    return audioop.alaw2lin(alaw, 2)


if __name__ == "__main__":
    import struct, random

    # 静音
    silence = b'\x00\x00' * 640
    encoded = alaw_encode(silence)
    assert len(encoded) == 640
    decoded = alaw_decode(encoded)
    assert len(decoded) == 1280  # 640 samples × 2 bytes

    # 最大/最小幅值
    max_pcm = struct.pack('<h', 32767) * 640
    enc = alaw_encode(max_pcm)
    dec = alaw_decode(enc)
    assert len(dec) == 1280

    min_pcm = struct.pack('<h', -32768) * 640
    enc = alaw_encode(min_pcm)
    dec = alaw_decode(enc)
    assert len(dec) == 1280

    # 往返: decode(encode(x)) ≈ x
    random.seed(42)
    for _ in range(1000):
        val = random.randint(-32768, 32767)
        pcm = struct.pack('<h', val)
        enc = alaw_encode(pcm)
        dec = struct.unpack('<h', alaw_decode(enc))[0]
        assert abs(val - dec) < 600, f"roundtrip error: {val} → {dec}"

    # 验证与 number2alaw.py（也用 audioop.lin2alaw）兼容
    test_pcm = struct.pack('<h', 10000) * 100
    assert alaw_encode(test_pcm) == audioop.lin2alaw(test_pcm, 2)

    print("g711.py all tests passed (using audioop-compatible module)")
