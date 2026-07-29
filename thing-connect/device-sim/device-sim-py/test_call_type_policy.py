import unittest

from call_type_policy import CallTypeError, resolve_call_type


class CallTypePolicyTests(unittest.TestCase):
    def test_omitted_type_follows_video_capability(self):
        self.assertEqual(resolve_call_type(None, True), "video")
        self.assertEqual(resolve_call_type(None, False), "audio")
        self.assertEqual(resolve_call_type("", False), "audio")

    def test_explicit_audio_is_always_allowed(self):
        self.assertEqual(resolve_call_type("AUDIO", True), "audio")
        self.assertEqual(resolve_call_type("audio", False), "audio")

    def test_explicit_video_requires_video_capability(self):
        self.assertEqual(resolve_call_type("VIDEO", True), "video")
        with self.assertRaises(CallTypeError):
            resolve_call_type("video", False)

    def test_invalid_type_is_rejected(self):
        with self.assertRaises(CallTypeError):
            resolve_call_type("voice", True)


if __name__ == "__main__":
    unittest.main()
