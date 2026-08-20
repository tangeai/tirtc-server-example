#!/usr/bin/env python3

import os
import tempfile
import unittest
from unittest import mock

os.environ.setdefault("TIRTC_SDK_VERSION", "2.2.1")

from media_file_reader import AudioFileReader, VideoFileReader
import media_source
from media_source import FileMediaSource


def _write_temp(root: str, name: str, data: bytes) -> str:
    path = os.path.join(root, name)
    with open(path, "wb") as target:
        target.write(data)
    return path


def _ogg_page(payloads: list[bytes], sequence: int) -> bytes:
    lacing = bytes(len(payload) for payload in payloads)
    header = bytearray(b"OggS")
    header.extend(b"\x00")
    header.extend(b"\x00")
    header.extend((0).to_bytes(8, "little"))
    header.extend((1).to_bytes(4, "little"))
    header.extend(sequence.to_bytes(4, "little"))
    header.extend((0).to_bytes(4, "little"))
    header.append(len(payloads))
    header.extend(lacing)
    return bytes(header) + b"".join(payloads)


class MediaFileReaderTests(unittest.TestCase):
    def test_pcm_reader_splits_and_loops(self):
        with tempfile.TemporaryDirectory() as root:
            path = _write_temp(root, "audio.pcm", b"\x01\x02" * 100)
            reader = AudioFileReader(path, "pcm_s16le_8khz", packet_ms=20)
            first_packet = reader.next_packet()
            second_packet = reader.next_packet()
            first = first_packet.payload
            second = second_packet.payload
            self.assertEqual(len(first), 320)
            self.assertEqual(len(second), 320)
            self.assertEqual(first_packet.duration_ms, 20.0)
            self.assertEqual(second_packet.duration_ms, 20.0)
            self.assertEqual(reader.next_frame(), first)

    def test_amr_reader_parses_nb_frames(self):
        with tempfile.TemporaryDirectory() as root:
            frame = bytes([0x04]) + b"\x11" * 12
            path = _write_temp(root, "audio.amr", b"#!AMR\n" + frame + frame)
            reader = AudioFileReader(path, "amr_nb", packet_ms=20, loop=False)
            first = reader.next_packet()
            second = reader.next_packet()
            self.assertEqual(first.payload, frame)
            self.assertEqual(second.payload, frame)
            self.assertEqual(first.duration_ms, 20.0)
            self.assertEqual(second.duration_ms, 20.0)
            self.assertIsNone(reader.next_frame())

    def test_aac_reader_parses_adts_frames(self):
        with tempfile.TemporaryDirectory() as root:
            frame = b"\xff\xf1\x50\x80\x00\xff\xfc"
            path = _write_temp(root, "audio.aac", frame + frame)
            reader = AudioFileReader(path, "aac_adts_16khz", loop=False)
            first = reader.next_packet()
            second = reader.next_packet()
            self.assertEqual(first.payload, frame)
            self.assertEqual(second.payload, frame)
            self.assertEqual(first.duration_ms, 64.0)
            self.assertEqual(second.duration_ms, 64.0)
            self.assertIsNone(reader.next_frame())

    def test_opus_reader_parses_ogg_packets(self):
        with tempfile.TemporaryDirectory() as root:
            data = (
                _ogg_page([b"OpusHead" + b"\x00" * 11, b"OpusTags" + b"\x00" * 4], 0) +
                _ogg_page([b"\x78\x00", b"\x79\x00"], 1)
            )
            path = _write_temp(root, "audio.ogg", data)
            reader = AudioFileReader(path, "opus_16khz", loop=False)
            first = reader.next_packet()
            second = reader.next_packet()
            self.assertEqual(first.payload, b"\x78\x00")
            self.assertEqual(second.payload, b"\x79\x00")
            self.assertEqual(first.duration_ms, 20.0)
            self.assertEqual(second.duration_ms, 40.0)
            self.assertIsNone(reader.next_frame())

    def test_legacy_audio_format_alias_still_works(self):
        with tempfile.TemporaryDirectory() as root:
            path = _write_temp(root, "audio.pcm", b"\x01\x02" * 100)
            reader = AudioFileReader(path, "pcm_8k", packet_ms=20)
            self.assertEqual(len(reader.next_frame()), 320)

    def test_skip_duration_uses_real_packet_timing(self):
        with tempfile.TemporaryDirectory() as root:
            data = (
                _ogg_page([b"OpusHead" + b"\x00" * 11, b"OpusTags" + b"\x00" * 4], 0) +
                _ogg_page([b"\x78\x00", b"\x78\x00", b"\x79\x00"], 1)
            )
            path = _write_temp(root, "audio.ogg", data)
            reader = AudioFileReader(path, "opus_16khz", loop=True)
            reader.skip_duration(35.0)
            packet = reader.next_packet()
            self.assertEqual(packet.payload, b"\x78\x00")
            reader.skip_duration(20.0)
            packet = reader.next_packet()
            self.assertEqual(packet.payload, b"\x79\x00")

    def test_h264_reader_groups_access_units(self):
        with tempfile.TemporaryDirectory() as root:
            data = (
                b"\x00\x00\x00\x01\x67\x64\x00\x1f"
                b"\x00\x00\x00\x01\x68\xeb\xe3\xcb"
                b"\x00\x00\x00\x01\x65\x88\x84"
                b"\x00\x00\x00\x01\x41\x9a"
            )
            path = _write_temp(root, "video.h264", data)
            reader = VideoFileReader(path, "h264", loop=False)
            first = reader.next_frame()
            second = reader.next_frame()
            self.assertTrue(first[1])
            self.assertFalse(second[1])
            self.assertIsNone(reader.next_frame())

    def test_forced_key_frame_realigns_file_audio(self):
        with tempfile.TemporaryDirectory() as root:
            audio = b"".join(bytes([index]) * 320 for index in range(8))
            audio_path = _write_temp(root, "audio.g711a", audio)
            video = (
                b"\x00\x00\x00\x01\x67\x64\x00\x1f"
                b"\x00\x00\x00\x01\x65\x88\x84"
                b"\x00\x00\x00\x01\x41\x9a"
                b"\x00\x00\x00\x01\x41\x9b"
                b"\x00\x00\x00\x01\x67\x64\x00\x1f"
                b"\x00\x00\x00\x01\x65\x88\x85"
            )
            video_path = _write_temp(root, "video.h264", video)
            source = FileMediaSource(video_path, audio_path)

            first_audio, _ = source.next_audio_packet()
            first_video, first_is_key = source.next_video()
            forced_video, forced_is_key = source.next_video(force_key=True)
            aligned_audio, _ = source.next_audio_packet()

            self.assertEqual(first_audio, bytes([0]) * 320)
            self.assertTrue(first_is_key)
            self.assertTrue(forced_is_key)
            self.assertNotEqual(first_video, forced_video)
            # The forced IDR is video frame 3: 3 * 1000/15 = 200 ms,
            # which maps to audio packet 5 at 40 ms per packet.
            self.assertEqual(aligned_audio, bytes([5]) * 320)

    def test_camera_media_source_does_not_use_file_seek_contract(self):
        with tempfile.TemporaryDirectory() as root:
            audio_path = _write_temp(root, "audio.g711a", b"\xd5" * 320)
            camera = mock.Mock()
            camera.next_frame.return_value = (b"\x00\x00\x00\x01\x65", True)
            with mock.patch.object(
                    media_source, "open_video_source", return_value=camera):
                source = FileMediaSource("camera://0", audio_path)
                try:
                    self.assertTrue(source.has_video())
                    self.assertEqual(
                        source.next_video(force_key=True),
                        (b"\x00\x00\x00\x01\x65", True),
                    )
                finally:
                    source.close()

            camera.next_frame.assert_called_once_with(force_key=True)
            camera.first_key_index.assert_not_called()
            camera.close.assert_called_once_with()

    def test_h265_reader_groups_access_units(self):
        with tempfile.TemporaryDirectory() as root:
            data = (
                b"\x00\x00\x00\x01\x40\x01"
                b"\x00\x00\x00\x01\x42\x01"
                b"\x00\x00\x00\x01\x44\x01"
                b"\x00\x00\x00\x01\x28\x01\xaa"
                b"\x00\x00\x00\x01\x02\x01\xbb"
            )
            path = _write_temp(root, "video.h265", data)
            reader = VideoFileReader(path, "h265", loop=False)
            first = reader.next_frame()
            second = reader.next_frame()
            self.assertTrue(first[1])
            self.assertFalse(second[1])
            self.assertIsNone(reader.next_frame())

    def test_mjpeg_reader_parses_frames(self):
        with tempfile.TemporaryDirectory() as root:
            data = b"\xff\xd8\x11\x22\xff\xd9\xff\xd8\x33\x44\xff\xd9"
            path = _write_temp(root, "video.mjpeg", data)
            reader = VideoFileReader(path, "mjpeg", loop=False)
            first = reader.next_frame()
            second = reader.next_frame()
            self.assertEqual(first, (b"\xff\xd8\x11\x22\xff\xd9", True))
            self.assertEqual(second, (b"\xff\xd8\x33\x44\xff\xd9", True))
            self.assertIsNone(reader.next_frame())


if __name__ == "__main__":
    unittest.main()
