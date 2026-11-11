import os
import math
import struct
import sys
import unittest
from unittest import mock

from ai_audio_codec import AudioCodec, validate_live_codecs, _library
from importlib.metadata import PackageNotFoundError
from media_formats import AI_AUDIO_FORMAT_CHOICES


class PackagedOpusTests(unittest.TestCase):
    @unittest.skipUnless(sys.platform == 'win32', 'Windows bundled DLL integration')
    def test_windows_opus_round_trip_without_system_library(self):
        # Missing AMR libraries must not skip this Windows Opus regression.
        with mock.patch.dict(os.environ, {'AI_CODEC_LIBRARY_DIR': ''}), \
                mock.patch('ai_audio_codec.find_library', return_value=None), \
                AudioCodec('opus_16khz', encode=True) as encoder, \
                AudioCodec('opus_16khz', encode=False) as decoder:
            values = []
            for frame in range(10):
                pcm = struct.pack('<320h', *[
                    int(8000 * math.sin(2 * math.pi * 440 * (frame * 320 + n) / 16000))
                    for n in range(320)])
                output = decoder.decode(encoder.encode(pcm))
                self.assertEqual(len(output), 640)
                values.extend(struct.unpack('<320h', output))
            self.assertGreater(max(map(abs, values)), 1000)


class NativeCodecTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        try:
            for name in AI_AUDIO_FORMAT_CHOICES:
                validate_live_codecs(name, name)
        except RuntimeError as exc:
            if os.environ.get('REQUIRE_AI_CODECS') == '1':
                raise
            raise unittest.SkipTest(str(exc))

    def test_real_codecs_encode_decode_continuous_audio_at_both_rates(self):
        for name in AI_AUDIO_FORMAT_CHOICES:
            with self.subTest(format=name):
                try:
                    encoder = AudioCodec(name, encode=True)
                except RuntimeError as exc:
                    if '缺少原生库' in str(exc):
                        self.skipTest(str(exc))
                    raise
                with encoder, AudioCodec(name, encode=False) as decoder:
                    decoded = bytearray()
                    for index in range(8):
                        pcm = struct.pack('<' + 'h' * encoder.samples, *[
                            int(9000 * math.sin(2 * math.pi * 440 * (index * encoder.samples + n)
                                               / encoder.spec.sample_rate))
                            for n in range(encoder.samples)])
                        packet = encoder.encode(pcm)
                        output = decoder.decode(packet)
                        self.assertEqual(len(output), len(pcm))
                        if encoder.spec.codec == 'pcm':
                            self.assertEqual(output, pcm)
                        decoded.extend(output)
                    values = struct.unpack('<' + 'h' * (len(decoded) // 2), decoded)
                    self.assertGreater(max(abs(value) for value in values), 1000)
                encoder.close()
                with self.assertRaises(RuntimeError):
                    encoder.encode(pcm)

    def test_independent_direction_matrix(self):
        for up in AI_AUDIO_FORMAT_CHOICES:
            for down in AI_AUDIO_FORMAT_CHOICES:
                with self.subTest(up=up, down=down):
                    validate_live_codecs(up, down)

    def test_amr_concatenated_frames_and_truncation(self):
        for name in ('amr_nb', 'amr_wb'):
            with AudioCodec(name, encode=True) as encoder, AudioCodec(name, encode=False) as decoder:
                packet = encoder.encode(bytes(encoder.samples * 2))
                self.assertEqual(len(decoder.decode(packet * 2)), encoder.samples * 4)
                for invalid in (packet[:-1], b'\x50', b'\xff', packet * 7):
                    with self.assertRaises(ValueError):
                        decoder.decode(invalid)

    def test_empty_oversized_invalid_frames_and_wrong_direction(self):
        for name in AI_AUDIO_FORMAT_CHOICES:
            with AudioCodec(name, encode=True) as encoder, AudioCodec(name, encode=False) as decoder:
                for invalid in (b'', b'\x00', bytes(encoder.samples * 4)):
                    with self.assertRaises(ValueError):
                        encoder.encode(invalid)
                for invalid in (b'', bytes(61441)):
                    with self.assertRaises(ValueError):
                        decoder.decode(invalid)
                with self.assertRaises(RuntimeError):
                    encoder.decode(b'123')
        with AudioCodec('pcm_8k', encode=False) as decoder:
            with self.assertRaises(ValueError):
                decoder.decode(b'123')



class CodecLifecycleTests(unittest.TestCase):
    def test_windows_opus_uses_packaged_dll_without_importing_bindings(self):
        package = mock.Mock()
        package.locate_file.return_value = 'installed/opus.dll'
        with mock.patch.dict(os.environ, {}, clear=True), \
                mock.patch('ai_audio_codec.sys.platform', 'win32'), \
                mock.patch('ai_audio_codec.distribution', return_value=package), \
                mock.patch('ai_audio_codec.find_library', return_value=None), \
                mock.patch('ai_audio_codec.C.CDLL') as load:
            self.assertIs(_library('opus'), load.return_value)
        load.assert_called_once_with('installed/opus.dll')
        package.locate_file.assert_called_once_with('opuslib_next/_native/opus.dll')

    def test_explicit_codec_directory_does_not_fall_back_to_package(self):
        with mock.patch.dict(os.environ, {'AI_CODEC_LIBRARY_DIR': '/explicit'}), \
                mock.patch('ai_audio_codec.distribution') as package, \
                mock.patch('ai_audio_codec.C.CDLL', side_effect=OSError):
            with self.assertRaisesRegex(RuntimeError, 'AI_CODEC_LIBRARY_DIR'):
                _library('opus')
        package.assert_not_called()

    def test_missing_windows_package_explains_install_command(self):
        with mock.patch.dict(os.environ, {}, clear=True), \
                mock.patch('ai_audio_codec.sys.platform', 'win32'), \
                mock.patch('ai_audio_codec.find_library', return_value=None), \
                mock.patch('ai_audio_codec.distribution', side_effect=PackageNotFoundError):
            with self.assertRaisesRegex(RuntimeError, 'pip install -r requirements-audio.txt'):
                _library('opus')

    def test_opus_8k_is_rejected_before_loading_native_libraries(self):
        for name in ('opus_8khz', 'opus_8k'):
            for encode in (True, False):
                with self.subTest(format=name, encode=encode), \
                        mock.patch('ai_audio_codec._library') as load:
                    with self.assertRaisesRegex(ValueError, 'Opus.*16K'):
                        AudioCodec(name, encode=encode)
                    load.assert_not_called()

    def test_missing_library_is_actionable_and_only_required_directions_load(self):
        with mock.patch('ai_audio_codec._library', side_effect=RuntimeError('missing')) as load:
            validate_live_codecs('alaw_8khz', 'pcm_16k')
            load.assert_not_called()
            with self.assertRaisesRegex(RuntimeError, 'missing'):
                validate_live_codecs('opus_16k', 'pcm_16k')

    def test_preflight_closes_encoder_when_decoder_fails(self):
        encoder = mock.Mock()
        encoder.__enter__ = mock.Mock(return_value=encoder)
        encoder.__exit__ = mock.Mock(return_value=False)
        with mock.patch('ai_audio_codec.AudioCodec', side_effect=[encoder, RuntimeError('decoder')]):
            with self.assertRaises(RuntimeError):
                validate_live_codecs('opus_16k', 'amr_wb')
        encoder.__exit__.assert_called_once()

    def test_negotiation_validates_both_directions_and_alias(self):
        from media_formats import ai_audio_descriptor, validate_ai_audio_response
        up, down = ai_audio_descriptor('opus_16k'), ai_audio_descriptor('alaw_16khz')
        down['codec'] = 'g711a'
        response = {'input_audio': up, 'output_audio': down}
        validate_ai_audio_response(response, 'opus_16k', 'alaw_16khz')
        for bad in ({}, {'input_audio': up}, {'input_audio': up, 'output_audio': []},
                    {'input_audio': dict(up, sample_rate=8000), 'output_audio': down},
                    {'input_audio': up, 'output_audio': dict(down, channels=True)}):
            with self.assertRaises(ValueError):
                validate_ai_audio_response(bad, 'opus_16k', 'alaw_16khz')
