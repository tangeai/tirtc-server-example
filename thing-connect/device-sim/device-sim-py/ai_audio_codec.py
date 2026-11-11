"""Single-owner, bounded mono AI packet codecs (PCM16LE <-> wire packets).

Each worker owns one instance; close it before releasing the session. Native
libraries use their published C ABIs, never the TiRTC SDK's private symbols:
https://opus-codec.org/docs/opus_api-1.5/opus_8h.html
https://github.com/BelledonneCommunications/opencore-amr/tree/master/amrnb
https://github.com/BelledonneCommunications/opencore-amr/blob/master/amrwb/dec_if.h
https://github.com/mstorsjo/vo-amrwbenc/blob/master/enc_if.h
"""
import audioop
import ctypes as C
from ctypes.util import find_library
import os
import sys
from importlib.metadata import distribution, PackageNotFoundError
from pathlib import Path
from contextlib import ExitStack

from media_formats import AUDIO_FORMATS, ai_audio_descriptor

_I16 = C.POINTER(C.c_int16)
_U8 = C.POINTER(C.c_ubyte)


def _library(name):
    directory = os.environ.get('AI_CODEC_LIBRARY_DIR', '')
    if directory:
        root = Path(directory).expanduser().resolve()
        candidates = [root / filename for filename in
                      (f'{name}.dll', f'lib{name}.dll', f'lib{name}-0.dll',
                       f'lib{name}.so', f'lib{name}.so.0', f'lib{name}.dylib')]
    else:
        found = find_library(name)
        candidates = [found] if found else []
        if name == 'opus' and sys.platform == 'win32':
            try:
                package = distribution('opuslib-next-bundled')
            except PackageNotFoundError:
                pass
            else:
                # Load only the packaged C ABI, without importing another binding
                # layer that would share and mutate ctypes function signatures.
                candidates.insert(0, package.locate_file('opuslib_next/_native/opus.dll'))
    for path in candidates:
        try:
            return C.CDLL(str(path))
        except OSError:
            continue
    if name == 'opus' and sys.platform == 'win32' and not directory:
        raise RuntimeError('AI Opus 库不可用；请使用启动模拟器的 Python 执行 '
                           'python -m pip install -r requirements-audio.txt；'
                           '需要 Windows x64 Python，或设置 AI_CODEC_LIBRARY_DIR（见 README）')
    raise RuntimeError(f'AI 音频缺少原生库 {name}；请安装匹配进程架构的库，'
                       '或设置 AI_CODEC_LIBRARY_DIR（见 README）')


def _function(lib, name, result, *args):
    fn = getattr(lib, name)
    fn.restype, fn.argtypes = result, list(args)
    return fn


class AudioCodec:
    """20 ms encoder; decoder accepts bounded complete wire packets.

    AMR packets contain storage-format frames (TOC + speech), without a file
    header. Opus packets contain raw Opus, without Ogg or length prefixes.
    PCM input/output is mono signed 16-bit little endian at spec.sample_rate.
    """
    def __init__(self, format_name, *, encode):
        self.spec = AUDIO_FORMATS[format_name]
        ai_audio_descriptor(self.spec)  # Reject unsupported codecs, e.g. AAC.
        self.encoding = encode
        self.samples = self.spec.sample_rate // 50
        self._state = None
        self._closed = False
        self._destroy = None
        if self.spec.codec in ('pcm', 'alaw'):
            return
        if self.spec.codec == 'opus':
            self._lib = _library('opus')
            prefix = 'opus_encoder' if encode else 'opus_decoder'
            self._destroy = _function(self._lib, prefix + '_destroy', None, C.c_void_p)
            error = C.c_int()
            if encode:
                create = _function(self._lib, prefix + '_create', C.c_void_p,
                                   C.c_int32, C.c_int, C.c_int, C.POINTER(C.c_int))
                self._process = _function(self._lib, 'opus_encode', C.c_int32,
                    C.c_void_p, _I16, C.c_int, _U8, C.c_int32)
                self._state = create(self.spec.sample_rate, 1, 2048, C.byref(error))
            else:
                create = _function(self._lib, prefix + '_create', C.c_void_p,
                                   C.c_int32, C.c_int, C.POINTER(C.c_int))
                self._process = _function(self._lib, 'opus_decode', C.c_int,
                    C.c_void_p, _U8, C.c_int32, _I16, C.c_int, C.c_int)
                self._state = create(self.spec.sample_rate, 1, C.byref(error))
            if error.value != 0 or not self._state:
                self.close()
                raise RuntimeError(f'Opus 初始化失败: {error.value}')
        else:
            wide = self.spec.sample_rate == 16000
            self._lib = _library('vo-amrwbenc' if wide and encode else
                                 'opencore-amrwb' if wide else 'opencore-amrnb')
            prefix = ('E_IF' if encode else 'D_IF') if wide else (
                'Encoder_Interface' if encode else 'Decoder_Interface')
            self._destroy = _function(self._lib, prefix + '_exit', None, C.c_void_p)
            create = _function(self._lib, prefix + '_init', C.c_void_p,
                               *([C.c_int] if encode and not wide else []))
            # Resolve all symbols before allocating native state.
            operation = ('_encode' if encode else '_decode') if wide else (
                '_Encode' if encode else '_Decode')
            self._process = _function(self._lib, prefix + operation,
                C.c_int if encode else None, C.c_void_p,
                *([C.c_int, _I16, _U8, C.c_int] if encode else [_U8, _I16, C.c_int]))
            self._state = create(0) if encode and not wide else create()
            if not self._state:
                raise RuntimeError('AMR 初始化失败')

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()

    def close(self):
        if self._state:
            self._destroy(self._state)
            self._state = None
        self._closed = True

    def _check(self, encoding):
        if self._closed or self.encoding != encoding:
            raise RuntimeError('音频 codec 已关闭或方向错误')

    def encode(self, pcm):
        self._check(True)
        if len(pcm) != self.samples * 2:
            raise ValueError('AI 编码输入必须为完整的 20ms PCM 帧')
        if self.spec.codec == 'pcm':
            return pcm
        if self.spec.codec == 'alaw':
            return audioop.lin2alaw(pcm, 2)
        samples = (C.c_int16 * self.samples).from_buffer_copy(pcm)
        packet = (C.c_ubyte * 1275)()
        if self.spec.codec == 'opus':
            length = self._process(self._state, samples, self.samples, packet, len(packet))
        else:
            # NB MR122 (12.2 kbps), WB mode 8 (23.85 kbps), DTX disabled.
            length = self._process(self._state, 8 if self.spec.sample_rate == 16000 else 7,
                                   samples, packet, 0)
        if not 0 < length <= len(packet):
            raise RuntimeError(f'AI 音频编码失败: {length}')
        return bytes(packet[:length])

    def decode(self, packet):
        self._check(False)
        if not packet or len(packet) > 61440:
            raise ValueError('AI 音频包为空或过大')
        codec = self.spec.codec
        if codec in ('pcm', 'alaw'):
            pcm = audioop.alaw2lin(packet, 2) if codec == 'alaw' else packet
            if len(pcm) % 2 or len(pcm) > self.spec.sample_rate * 2 * 120 // 1000:
                raise ValueError('PCM 包长度无效或超过 120ms')
            return pcm
        if codec == 'opus':
            pcm = (C.c_int16 * (self.spec.sample_rate * 120 // 1000))()
            raw = (C.c_ubyte * len(packet)).from_buffer_copy(packet)
            samples = self._process(self._state, raw, len(packet), pcm, len(pcm), 0)
            if samples <= 0:
                raise ValueError(f'Opus 解码失败: {samples}')
            return bytes(pcm)[:samples * 2]
        # Validate every AMR frame before passing any untrusted buffer to C.
        sizes = ([18, 24, 33, 37, 41, 47, 51, 59, 61, 6, 0, 0, 0, 0, 1, 1]
                 if self.spec.sample_rate == 16000 else
                 [13, 14, 16, 18, 20, 21, 27, 32, 6, 0, 0, 0, 0, 0, 0, 1])
        frames, pos = [], 0
        while pos < len(packet):
            toc = packet[pos]
            size = sizes[(toc >> 3) & 15]
            if toc & 0x83 or not size or pos + size > len(packet) or len(frames) >= 6:
                raise ValueError('AMR 包帧边界无效或超过 120ms')
            frames.append(packet[pos:pos + size])
            pos += size
        output = bytearray()
        for frame in frames:
            # Padding protects native decoders on SID/no-data frames.
            raw = (C.c_ubyte * 64)()
            raw[:len(frame)] = frame
            pcm = (C.c_int16 * self.samples)()
            self._process(self._state, raw, pcm, int(not (frame[0] & 4)))
            output.extend(bytes(pcm))
        return bytes(output)


def validate_live_codecs(up, down):
    """Fail before connecting; load only the two requested codec directions."""
    with ExitStack() as stack:
        stack.enter_context(AudioCodec(up, encode=True))
        stack.enter_context(AudioCodec(down, encode=False))
