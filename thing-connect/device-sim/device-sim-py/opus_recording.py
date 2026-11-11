"""Bounded mono Ogg/Opus recording of SDK packets (RFC 7845 / RFC 3533).

The transport provides complete Opus packets, not an Ogg stream. Keep packet
boundaries and a 48 kHz granule clock here, independent of capture sample rate.
No encoder pre-skip is known for a received live stream, so it is zero. Gaps in
network reception are omitted, matching the simulator's other raw recordings.
"""
import secrets
import struct


def opus_packet_duration_ms(packet: bytes) -> float:
    if not packet:
        raise ValueError("Opus packet 为空")
    toc = packet[0]
    config = toc >> 3
    code = toc & 0x03
    frame_duration_ms = _opus_frame_duration_ms(config)
    if code == 0:
        frame_count = 1
    elif code in (1, 2):
        frame_count = 2
    else:
        if len(packet) < 2:
            raise ValueError("Opus packet 缺少帧数头")
        frame_count = packet[1] & 0x3F
        if frame_count <= 0:
            raise ValueError("Opus packet 帧数无效")
    duration = frame_duration_ms * frame_count
    if duration > 120:
        raise ValueError("Opus packet 超过 120ms")
    return duration


def _opus_frame_duration_ms(config: int) -> float:
    if config < 12:
        return (10.0, 20.0, 40.0, 60.0)[config & 0x03]
    if config < 16:
        return (10.0, 20.0)[config & 0x01]
    return (2.5, 5.0, 10.0, 20.0)[config & 0x03]


def _crc_table():
    table = []
    for value in range(256):
        crc = value << 24
        for _ in range(8):
            crc = ((crc << 1) ^ (0x04C11DB7 if crc & 0x80000000 else 0)) & 0xffffffff
        table.append(crc)
    return tuple(table)


_CRC_TABLE = _crc_table()


class OpusRecording:
    """Own a seek-free Ogg file, buffering at most one bounded audio packet.

    The last packet is delayed until close so its page can carry EOS. Call only
    from the recording worker, never the SDK callback. close() is idempotent.
    """
    def __init__(self, path: str, sample_rate: int):
        if sample_rate not in (8000, 16000):
            raise ValueError("Opus 录音仅支持 8/16 kHz 单声道")
        self._serial = secrets.randbits(32)
        self._sequence = 0
        self._granule = 0
        self._pending = None
        self._file = open(path, "wb")
        try:
            self._page(b"OpusHead" + struct.pack("<BBHIhB", 1, 1, 0, sample_rate, 0, 0), 2)
            vendor = b"ThingConnect"
            self._page(b"OpusTags" + struct.pack("<I", len(vendor)) + vendor + struct.pack("<I", 0))
        except BaseException:
            self._file.close()
            self._file = None
            raise

    def write_packet(self, packet: bytes) -> None:
        if self._file is None:
            raise ValueError("Opus 录音已关闭")
        # A single page has at most 255 lacing entries. Leave room for the
        # terminating entry, including packets whose length is a multiple of 255.
        if not packet or len(packet) > 61440 or packet[0] & 4:
            raise ValueError("Opus packet 长度或单声道格式无效")
        samples = int(opus_packet_duration_ms(packet) * 48)
        if self._pending is not None:
            self._page(self._pending)
        self._granule += samples
        self._pending = bytes(packet)

    def close(self) -> None:
        if self._file is None:
            return
        try:
            if self._pending is not None:
                self._page(self._pending, 4)
                self._pending = None
        finally:
            self._file.close()
            self._file = None

    def _page(self, packet: bytes, flags: int = 0) -> None:
        lacing = bytes([255] * (len(packet) // 255) + [len(packet) % 255])
        page = bytearray(struct.pack("<4sBBQIIIB", b"OggS", 0, flags,
                                     self._granule, self._serial, self._sequence, 0,
                                     len(lacing)) + lacing + packet)
        crc = 0
        for value in page:
            crc = ((crc << 8) ^ _CRC_TABLE[((crc >> 24) ^ value) & 255]) & 0xffffffff
        struct.pack_into("<I", page, 22, crc)
        self._file.write(page)
        self._sequence += 1
