#!/usr/bin/env python3

from fractions import Fraction
from types import SimpleNamespace
import time
import unittest

from camera_video_source import (
    CAMERA_FPS,
    CAMERA_HEIGHT,
    CAMERA_WIDTH,
    CameraVideoSource,
    _as_annexb,
    camera_index_from_uri,
    camera_source_uri,
    describe_video_source,
    is_camera_source,
)


class _FakeImage:
    def __init__(self, width=640, height=480):
        self.shape = (height, width, 3)

    def copy(self):
        return _FakeImage(self.shape[1], self.shape[0])


class _FakeCapture:
    def __init__(self):
        self.opened = True
        self.properties = {}

    def isOpened(self):
        return self.opened

    def set(self, prop, value):
        self.properties[prop] = value
        return True

    def read(self):
        time.sleep(0.002)
        return self.opened, (_FakeImage() if self.opened else None)

    def release(self):
        self.opened = False


class _FakeCv2:
    CAP_DSHOW = 700
    CAP_PROP_FOURCC = 1
    CAP_PROP_FRAME_WIDTH = 2
    CAP_PROP_FRAME_HEIGHT = 3
    CAP_PROP_FPS = 4
    CAP_PROP_BUFFERSIZE = 5
    INTER_LINEAR = 6

    def __init__(self):
        self.capture = _FakeCapture()
        self.resize_calls = []

    def VideoCapture(self, index, backend=None):
        self.open_args = (index, backend)
        return self.capture

    @staticmethod
    def VideoWriter_fourcc(*value):
        return 1234

    def resize(self, image, size, interpolation=None):
        self.resize_calls.append((image.shape, size, interpolation))
        return _FakeImage(*size)


class _FakeFrame:
    def __init__(self, image):
        self.image = image
        self.pts = None
        self.time_base = None
        self.pict_type = None


class _FakePacket:
    def __init__(self, key):
        self.is_keyframe = key

    def __bytes__(self):
        return b"\x00\x00\x00\x01" + (b"\x65" if self.is_keyframe else b"\x41")


class _FakeCodec:
    def __init__(self):
        self.opened = False
        self.closed = False

    def open(self):
        self.opened = True

    def encode(self, frame):
        return [_FakePacket(frame.pict_type == "I" or frame.pts == 0)]

    def close(self):
        self.closed = True


class _FakeCodecContext:
    created = None

    @classmethod
    def create(cls, name, mode):
        cls.create_args = (name, mode)
        cls.created = _FakeCodec()
        return cls.created


class _FakeVideoFrame:
    @staticmethod
    def from_ndarray(image, format):
        _FakeVideoFrame.last_args = (image, format)
        return _FakeFrame(image)


def _fake_av():
    return SimpleNamespace(
        CodecContext=_FakeCodecContext,
        VideoFrame=_FakeVideoFrame,
        video=SimpleNamespace(
            frame=SimpleNamespace(PictureType=SimpleNamespace(I="I"))),
    )


class CameraVideoSourceTests(unittest.TestCase):
    def test_camera_uri_helpers(self):
        uri = camera_source_uri(2)
        self.assertEqual(uri, "camera://2")
        self.assertTrue(is_camera_source(uri))
        self.assertEqual(camera_index_from_uri(uri), 2)
        self.assertIn("1280x720 15fps H.264", describe_video_source(uri))
        with self.assertRaises(ValueError):
            camera_source_uri(-1)

    def test_camera_encodes_resized_annexb_key_frame(self):
        cv2 = _FakeCv2()
        source = CameraVideoSource(1, cv2_module=cv2, av_module=_fake_av())
        codec = _FakeCodecContext.created
        capture_thread = source._capture_thread
        try:
            payload, is_key = source.next_frame(force_key=True)
        finally:
            source.close()

        self.assertTrue(payload.startswith(b"\x00\x00\x00\x01\x65"))
        self.assertTrue(is_key)
        self.assertEqual(cv2.open_args, (1, cv2.CAP_DSHOW))
        self.assertEqual(codec.width, CAMERA_WIDTH)
        self.assertEqual(codec.height, CAMERA_HEIGHT)
        self.assertEqual(codec.framerate, Fraction(CAMERA_FPS, 1))
        self.assertEqual(codec.max_b_frames, 0)
        self.assertIn("repeat-headers=1", codec.options["x264-params"])
        self.assertEqual(cv2.resize_calls[0][1], (CAMERA_WIDTH, CAMERA_HEIGHT))
        self.assertTrue(codec.closed)
        self.assertFalse(capture_thread.is_alive())

    def test_close_supports_pyav_codec_without_close_method(self):
        source = CameraVideoSource(
            0, cv2_module=_FakeCv2(), av_module=_fake_av())
        capture_thread = source._capture_thread
        source._codec.close = None

        source.close()

        self.assertFalse(capture_thread.is_alive())

    def test_length_prefixed_nals_are_converted_to_annexb(self):
        encoded = b"\x00\x00\x00\x02\x67\x01\x00\x00\x00\x02\x65\x02"
        self.assertEqual(
            _as_annexb(encoded),
            b"\x00\x00\x00\x01\x67\x01\x00\x00\x00\x01\x65\x02",
        )
        with self.assertRaises(RuntimeError):
            _as_annexb(b"invalid")


if __name__ == "__main__":
    unittest.main()
