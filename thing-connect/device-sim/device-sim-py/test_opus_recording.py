import json
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock
import wave

from audio_recorder import AudioRecorder
from media_file_reader import AudioFileReader
from media_postprocess import convert_audio_to_wav
from opus_recording import OpusRecording


class OpusRecordingTests(unittest.TestCase):
    def test_packet_boundaries_granules_and_eos(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'audio.opus'
            recording = OpusRecording(path, 8000)
            packets = [b'\xf8\xff\xfe', b'\x80' + bytes(254), b'\x98' + bytes(999)]
            for packet in packets:
                recording.write_packet(packet)
            recording.close()
            recording.close()
            reader = AudioFileReader(path, 'opus_8khz', loop=False)
            self.assertEqual([reader.next_packet().payload for _ in packets], packets)
            self.assertIsNone(reader.next_packet())
            data = path.read_bytes()
            pages = []
            while data:
                count = data[26]
                size = 27 + count + sum(data[27:27 + count])
                pages.append(data[:size])
                data = data[size:]
            self.assertEqual([struct.unpack_from('<Q', p, 6)[0] for p in pages],
                             [0, 0, 960, 1080, 2040])
            self.assertEqual(pages[0][5], 2)
            self.assertEqual(pages[-1][5], 4)

    def test_invalid_packet_does_not_replace_pending_packet(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'audio.opus'
            recording = OpusRecording(path, 16000)
            recording.write_packet(b'\xf8\xff\xfe')
            for packet in (b'', b'\xfc\xff\xfe', b'\xfb\x3f', b'\xf8' * 61441):
                with self.assertRaises(ValueError):
                    recording.write_packet(packet)
            recording.close()
            reader = AudioFileReader(path, 'opus_16khz', loop=False)
            self.assertEqual(reader.next_packet().payload, b'\xf8\xff\xfe')
            self.assertIsNone(reader.next_packet())

    def test_legacy_raw_opus_does_not_invoke_ffmpeg(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'old.raw'
            path.write_bytes(b'\xf8\xff\xfe')
            with mock.patch('media_postprocess._ffmpeg_exists', return_value=True), \
                    mock.patch('media_postprocess.subprocess.run') as run:
                self.assertIsNone(convert_audio_to_wav(str(path), {'encoding':'opus'}))
            run.assert_not_called()

    @unittest.skipUnless(shutil.which('ffmpeg'), 'ffmpeg required for real codec/container validation')
    def test_real_received_packets_convert_to_wav_at_both_sample_rates(self):
        for rate in (8000, 16000):
            with self.subTest(rate=rate), tempfile.TemporaryDirectory() as directory:
                original = Path(directory) / 'source.ogg'
                subprocess.run(['ffmpeg', '-v', 'error', '-f', 'lavfi', '-i',
                                'sine=frequency=440:duration=0.2', '-ar', str(rate),
                                '-ac', '1', '-c:a', 'libopus', str(original)],
                               check=True, capture_output=True, timeout=10)
                reader = AudioFileReader(original, f'opus_{rate // 1000}khz', loop=False)
                recorder = AudioRecorder(directory)
                recorder.open()
                packets = []
                milliseconds = 0
                while (packet := reader.next_packet()) is not None:
                    packets.append(packet.payload)
                    milliseconds += packet.duration_ms
                    recorder.write_frame(SimpleNamespace(media=4, flags=rate // 8000 - 1,
                                                         stream_id=1), packet.payload)
                recorder.close()
                self.assertEqual((Path(directory) / 'received_audio.raw').read_bytes(), b''.join(packets))
                with wave.open(str(Path(directory) / 'received_audio.wav')) as wav:
                    self.assertEqual(wav.getframerate(), rate)
                    self.assertEqual(wav.getnchannels(), 1)
                    self.assertEqual(wav.getnframes(), int(milliseconds * rate / 1000))
                metadata = json.loads((Path(directory) / 'received_audio.fmt.json').read_text())
                self.assertEqual(metadata['encoding'], 'opus')
