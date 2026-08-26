#!/usr/bin/env python3

import os
import unittest

os.environ.setdefault("TIRTC_SDK_VERSION", "2.3.0")

from media_formats import (
    AUDIO_FORMATS,
    VIDEO_FORMATS,
    ai_audio_descriptor,
    validate_with_mic_audio_formats,
)


class MediaFormatsTests(unittest.TestCase):
    def test_required_audio_formats_cover_codecs_and_sample_rates(self):
        expected = {
            "pcm_s16le_8khz": ("pcm", 8000),
            "pcm_s16le_16khz": ("pcm", 16000),
            "alaw_8khz": ("alaw", 8000),
            "alaw_16khz": ("alaw", 16000),
            "amr_nb": ("amr", 8000),
            "amr_wb": ("amr", 16000),
            "opus_8khz": ("opus", 8000),
            "opus_16khz": ("opus", 16000),
        }
        for name, (codec, sample_rate) in expected.items():
            with self.subTest(name=name):
                spec = AUDIO_FORMATS[name]
                self.assertEqual(spec.codec, codec)
                self.assertEqual(spec.sample_rate, sample_rate)

    def test_required_video_formats_are_registered(self):
        expected = {
            "h264": "h264",
            "h265": "h265",
            "mjpeg": "mjpeg",
        }
        for name, codec in expected.items():
            with self.subTest(name=name):
                spec = VIDEO_FORMATS[name]
                self.assertEqual(spec.codec, codec)

    def test_ai_alaw_descriptor_uses_public_codec_name(self):
        self.assertEqual(
            ai_audio_descriptor("alaw_8khz"),
            {"codec": "alaw", "sample_rate": 8000, "channels": 1},
        )

    def test_ai_rejects_unsupported_aac_descriptor(self):
        with self.assertRaisesRegex(ValueError, "AI 对讲不支持"):
            ai_audio_descriptor("aac_adts_16khz")

    def test_with_mic_accepts_matching_alaw_8khz_or_16khz(self):
        validate_with_mic_audio_formats("alaw_8khz", "g711a_8k")
        validate_with_mic_audio_formats("g711a_16k", "alaw_16khz")
        validate_with_mic_audio_formats("g711a_8khz", "alaw_8khz")
        validate_with_mic_audio_formats("alaw_16khz", "g711a_16khz")

        for up_format, down_format in (
                ("pcm_s16le_16khz", "alaw_8khz"),
                ("alaw_8khz", "alaw_16khz"),
                ("alaw_8khz", "amr_nb"),
                ("opus_8khz", "alaw_8khz")):
            with self.subTest(up=up_format, down=down_format):
                with self.assertRaisesRegex(
                        ValueError, "alaw_8khz 或 alaw_16khz"):
                    validate_with_mic_audio_formats(up_format, down_format)


if __name__ == "__main__":
    unittest.main()
