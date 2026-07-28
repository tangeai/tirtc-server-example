import os
import tempfile
import unittest
from unittest import mock

from media_postprocess import convert_audio_to_wav, convert_video_to_mp4


class MediaPostprocessTests(unittest.TestCase):
    def test_convert_pcm_audio_builds_wav_command(self):
        with tempfile.TemporaryDirectory() as root:
            raw_path = os.path.join(root, "received_audio.raw")
            wav_path = os.path.join(root, "received_audio.wav")
            with open(raw_path, "wb") as f:
                f.write(b"\x00\x00" * 10)
            with mock.patch("media_postprocess.shutil.which", return_value="/usr/bin/ffmpeg"), \
                    mock.patch("media_postprocess.subprocess.run") as run:
                run.return_value = mock.Mock(returncode=0, stderr="", stdout="")
                with open(wav_path, "wb") as f:
                    f.write(b"RIFF")
                result = convert_audio_to_wav(
                    raw_path, {"encoding": "s16le", "sample_rate": 16000})
            self.assertEqual(result, wav_path)
            self.assertIn("s16le", run.call_args.args[0])

    def test_convert_h264_video_builds_mp4_command(self):
        with tempfile.TemporaryDirectory() as root:
            raw_path = os.path.join(root, "received_video.h264")
            mp4_path = os.path.join(root, "received_video.mp4")
            with open(raw_path, "wb") as f:
                f.write(b"\x00\x00\x00\x01\x67")
            with mock.patch("media_postprocess.shutil.which", return_value="/usr/bin/ffmpeg"), \
                    mock.patch("media_postprocess.subprocess.run") as run:
                run.return_value = mock.Mock(returncode=0, stderr="", stdout="")
                with open(mp4_path, "wb") as f:
                    f.write(b"....")
                result = convert_video_to_mp4(raw_path, "h264")
            self.assertEqual(result, mp4_path)
            self.assertIn("h264", run.call_args.args[0])

    def test_convert_h265_video_builds_mp4_command(self):
        with tempfile.TemporaryDirectory() as root:
            raw_path = os.path.join(root, "received_video.h265")
            mp4_path = os.path.join(root, "received_video.mp4")
            with open(raw_path, "wb") as f:
                f.write(b"\x00\x00\x00\x01\x40")
            with mock.patch("media_postprocess.shutil.which", return_value="/usr/bin/ffmpeg"), \
                    mock.patch("media_postprocess.subprocess.run") as run:
                run.return_value = mock.Mock(returncode=0, stderr="", stdout="")
                with open(mp4_path, "wb") as f:
                    f.write(b"....")
                result = convert_video_to_mp4(raw_path, "h265")
            self.assertEqual(result, mp4_path)
            self.assertIn("hevc", run.call_args.args[0])

    def test_convert_mjpeg_video_builds_mp4_command(self):
        with tempfile.TemporaryDirectory() as root:
            raw_path = os.path.join(root, "received_video.mjpeg")
            mp4_path = os.path.join(root, "received_video.mp4")
            with open(raw_path, "wb") as f:
                f.write(b"\xff\xd8\x11\x22\xff\xd9")
            with mock.patch("media_postprocess.shutil.which", return_value="/usr/bin/ffmpeg"), \
                    mock.patch("media_postprocess.subprocess.run") as run:
                run.return_value = mock.Mock(returncode=0, stderr="", stdout="")
                with open(mp4_path, "wb") as f:
                    f.write(b"....")
                result = convert_video_to_mp4(raw_path, "mjpeg")
            self.assertEqual(result, mp4_path)
            self.assertIn("mjpeg", run.call_args.args[0])

    def test_skip_when_ffmpeg_missing(self):
        with tempfile.TemporaryDirectory() as root:
            raw_path = os.path.join(root, "received_audio.raw")
            with open(raw_path, "wb") as f:
                f.write(b"\x00\x00" * 10)
            warnings = []
            with mock.patch("media_postprocess.shutil.which", return_value=None):
                result = convert_audio_to_wav(
                    raw_path, {"encoding": "s16le", "sample_rate": 8000},
                    warn=warnings.append)
            self.assertIsNone(result)
            self.assertTrue(warnings)


if __name__ == "__main__":
    unittest.main()
