"""编码媒体文件分帧器。

模拟器只读取已经编码好的媒体文件，不负责转码。
"""

from dataclasses import dataclass

from media_formats import AUDIO_FORMATS, VIDEO_FORMATS


@dataclass(frozen=True)
class AudioPacket:
    payload: bytes
    duration_ms: float


class AudioFileReader:
    def __init__(self, path: str, format_name: str, packet_ms: int = 20,
                 loop: bool = True):
        self.path = path
        self.format = AUDIO_FORMATS[format_name]
        self.packet_ms = packet_ms
        self.loop = loop
        with open(path, "rb") as source:
            data = source.read()
        self._packets = self._parse(data)
        if not self._packets:
            raise ValueError(f"音频文件没有可发送帧: {path}")
        self._index = 0

    def next_packet(self) -> "AudioPacket | None":
        if self._index >= len(self._packets):
            if not self.loop:
                return None
            self._index = 0
        packet = self._packets[self._index]
        self._index += 1
        return packet

    def next_frame(self) -> "bytes | None":
        packet = self.next_packet()
        return None if packet is None else packet.payload

    def skip_frames(self, count: int) -> None:
        if not self._packets or count <= 0:
            return
        if self.loop:
            self._index = (self._index + count) % len(self._packets)
            return
        self._index = min(len(self._packets), self._index + count)

    def skip_duration(self, duration_ms: float) -> None:
        if not self._packets or duration_ms <= 0:
            return
        if self.loop:
            total_ms = sum(packet.duration_ms for packet in self._packets)
            if total_ms > 0:
                duration_ms %= total_ms
        skipped_ms = 0.0
        while self._index < len(self._packets):
            packet = self._packets[self._index]
            if skipped_ms + packet.duration_ms > duration_ms:
                break
            skipped_ms += packet.duration_ms
            self._index += 1
            if self.loop and self._index >= len(self._packets):
                self._index = 0
                if skipped_ms >= duration_ms:
                    break

    def seek_duration(self, duration_ms: float) -> None:
        """Move to an absolute media position within the looping file."""
        self._index = 0
        self.skip_duration(duration_ms)

    def _parse(self, data: bytes) -> list[AudioPacket]:
        codec = self.format.codec
        if codec in ("pcm", "alaw"):
            bytes_per_sample = 2 if codec == "pcm" else 1
            size = self.format.sample_rate * bytes_per_sample * self.packet_ms // 1000
            padding = b"\x00" if codec == "pcm" else b"\xd5"
            return [
                AudioPacket(
                    data[pos:pos + size].ljust(size, padding),
                    float(self.packet_ms),
                )
                for pos in range(0, len(data), size)
            ]
        if codec == "amr":
            return _parse_amr(data, self.format.sample_rate)
        if codec == "aac":
            return _parse_adts(data, self.format.sample_rate)
        if codec == "opus":
            return _parse_ogg_packets(data)
        raise ValueError(f"不支持的音频格式: {self.format.name}")


class VideoFileReader:
    def __init__(self, path: str, format_name: str, loop: bool = True):
        self.path = path
        self.format = VIDEO_FORMATS[format_name]
        self.loop = loop
        with open(path, "rb") as source:
            data = source.read()
        self._frames = self._parse(data)
        if not self._frames:
            raise ValueError(f"视频文件没有可发送帧: {path}")
        self._index = 0
        self._first_key_index = next((i for i, (_, is_key) in enumerate(self._frames) if is_key), 0)
        self._index = self._first_key_index
        self._last_frame_index: "int | None" = None

    def next_frame(self, force_key: bool = False) -> "tuple[bytes, bool] | None":
        if force_key and not self._advance_to_key():
            return None
        if self._index >= len(self._frames):
            if not self.loop:
                return None
            self._index = 0
            if force_key:
                self._advance_to_key()
        self._last_frame_index = self._index
        frame = self._frames[self._index]
        self._index += 1
        return frame

    def first_key_index(self) -> int:
        return self._first_key_index

    def current_index(self) -> int:
        return self._index

    def last_frame_index(self) -> "int | None":
        return self._last_frame_index

    def _advance_to_key(self) -> bool:
        checked = 0
        while checked < len(self._frames):
            if self._index >= len(self._frames):
                if not self.loop:
                    return False
                self._index = 0
            if self._frames[self._index][1]:
                return True
            self._index += 1
            checked += 1
        return False

    def _parse(self, data: bytes) -> list[tuple[bytes, bool]]:
        if self.format.name == "mjpeg":
            return _parse_mjpeg(data)
        if self.format.name == "h264":
            return _parse_annexb_access_units(data, codec="h264")
        if self.format.name == "h265":
            return _parse_annexb_access_units(data, codec="h265")
        raise ValueError(f"不支持的视频格式: {self.format.name}")


def _parse_amr(data: bytes, sample_rate: int) -> list[AudioPacket]:
    wideband = sample_rate == 16000
    magic = b"#!AMR-WB\n" if wideband else b"#!AMR\n"
    if not data.startswith(magic):
        raise ValueError(f"AMR 文件缺少 {magic!r} 文件头")
    sizes = ([18, 24, 33, 37, 41, 47, 51, 59, 61, 6]
             if wideband else [13, 14, 16, 18, 20, 21, 27, 32, 6])
    frames, pos = [], len(magic)
    while pos < len(data):
        frame_type = (data[pos] >> 3) & 0x0F
        if frame_type >= len(sizes):
            raise ValueError(f"不支持的 AMR frame type: {frame_type}")
        size = sizes[frame_type]
        if pos + size > len(data):
            raise ValueError("AMR 文件末帧不完整")
        frames.append(AudioPacket(data[pos:pos + size], 20.0))
        pos += size
    return frames


def _parse_adts(data: bytes, sample_rate: int) -> list[AudioPacket]:
    frames, pos = [], 0
    while pos < len(data):
        if pos + 7 > len(data) or data[pos] != 0xFF or data[pos + 1] & 0xF6 != 0xF0:
            raise ValueError(f"AAC 文件不是有效的 ADTS 流（offset={pos}）")
        size = ((data[pos + 3] & 0x03) << 11) | (data[pos + 4] << 3) | (data[pos + 5] >> 5)
        if size < 7 or pos + size > len(data):
            raise ValueError("AAC ADTS 帧长度无效")
        raw_blocks = (data[pos + 6] & 0x03) + 1
        duration_ms = (1024 * raw_blocks * 1000.0) / sample_rate
        frames.append(AudioPacket(data[pos:pos + size], duration_ms))
        pos += size
    return frames


def _parse_ogg_packets(data: bytes) -> list[AudioPacket]:
    packets, current, pos = [], bytearray(), 0
    while pos < len(data):
        if data[pos:pos + 4] != b"OggS" or pos + 27 > len(data):
            raise ValueError(f"Opus 文件不是有效的 Ogg 流（offset={pos}）")
        segment_count = data[pos + 26]
        table_end = pos + 27 + segment_count
        if table_end > len(data):
            raise ValueError("Ogg segment table 不完整")
        lacing = data[pos + 27:table_end]
        payload_end = table_end + sum(lacing)
        if payload_end > len(data):
            raise ValueError("Ogg page payload 不完整")
        cursor = table_end
        for size in lacing:
            current.extend(data[cursor:cursor + size])
            cursor += size
            if size < 255:
                packet = bytes(current)
                current.clear()
                if not packet.startswith((b"OpusHead", b"OpusTags")):
                    packets.append(AudioPacket(packet, _opus_packet_duration_ms(packet)))
        pos = payload_end
    if current:
        raise ValueError("Ogg 文件末尾存在未完成 packet")
    return packets


def _opus_packet_duration_ms(packet: bytes) -> float:
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
    return frame_duration_ms * frame_count


def _opus_frame_duration_ms(config: int) -> float:
    if config < 12:
        return (10.0, 20.0, 40.0, 60.0)[config & 0x03]
    if config < 16:
        return (10.0, 20.0)[config & 0x01]
    return (2.5, 5.0, 10.0, 20.0)[config & 0x03]


def _parse_annexb_access_units(data: bytes, codec: str) -> list[tuple[bytes, bool]]:
    nals = _split_annexb_nals(data)
    if not nals:
        raise ValueError("Annex-B 文件不包含 NALU")

    frames: list[tuple[bytes, bool]] = []
    current: list[bytes] = []
    current_types: list[int] = []

    for index, nal in enumerate(nals):
        nal_type = _nal_type(nal, codec)
        current.append(nal)
        current_types.append(nal_type)
        next_type = _nal_type(nals[index + 1], codec) if index + 1 < len(nals) else None
        if _starts_new_access_unit(next_type, current_types, codec):
            frame = b"".join(current)
            frames.append((frame, _is_key_frame(current_types, codec)))
            current = []
            current_types = []

    if current:
        frames.append((b"".join(current), _is_key_frame(current_types, codec)))
    return frames


def _split_annexb_nals(data: bytes) -> list[bytes]:
    starts: list[tuple[int, int]] = []
    pos = 0
    while True:
        offset, length = _find_start_code(data, pos)
        if length == 0:
            break
        starts.append((offset, length))
        pos = offset + length
    nals = []
    for index, (offset, _) in enumerate(starts):
        next_offset = starts[index + 1][0] if index + 1 < len(starts) else len(data)
        nals.append(data[offset:next_offset])
    return nals


def _find_start_code(data: bytes, pos: int) -> tuple[int, int]:
    three = data.find(b"\x00\x00\x01", pos)
    four = data.find(b"\x00\x00\x00\x01", pos)
    if four >= 0 and (three < 0 or four <= three):
        return four, 4
    if three >= 0:
        return three, 3
    return len(data), 0


def _nal_type(nal: bytes, codec: str) -> int:
    payload = nal[4:] if nal.startswith(b"\x00\x00\x00\x01") else nal[3:]
    if not payload:
        return -1
    if codec == "h264":
        return payload[0] & 0x1F
    return (payload[0] >> 1) & 0x3F


def _starts_new_access_unit(next_type: "int | None", current_types: list[int], codec: str) -> bool:
    if next_type is None:
        return True
    if codec == "h264":
        slice_types = {1, 2, 3, 4, 5}
        has_slice = any(nal_type in slice_types for nal_type in current_types)
        return next_type == 9 or (next_type in slice_types and has_slice) or (next_type == 7 and has_slice)
    has_vcl = any(0 <= nal_type <= 31 for nal_type in current_types)
    return next_type == 35 or ((0 <= next_type <= 31) and has_vcl) or (next_type in (32, 33, 34) and has_vcl)


def _is_key_frame(nal_types: list[int], codec: str) -> bool:
    if codec == "h264":
        return 5 in nal_types
    return any(16 <= nal_type <= 21 for nal_type in nal_types)


def _parse_mjpeg(data: bytes) -> list[tuple[bytes, bool]]:
    frames = []
    pos = 0
    while True:
        start = data.find(b"\xff\xd8", pos)
        if start < 0:
            break
        end = data.find(b"\xff\xd9", start + 2)
        if end < 0:
            raise ValueError("MJPEG 文件末尾缺少 JPEG EOI 标记")
        frames.append((data[start:end + 2], True))
        pos = end + 2
    if not frames:
        raise ValueError("MJPEG 文件没有 JPEG 帧")
    return frames
