"""Windows recognition audio using the same sound-card adapters as calls."""
import sys
import io
import struct
import time


def _close(stream):
    if stream is not None:
        try:
            stream.stop()
        except Exception:
            pass  # A failed start may leave the stream already stopped.
        try:
            stream.close()
        except Exception:
            print("[recognition] 声卡释放失败，请退出模拟器后检查设备", flush=True)


def _resample(data, source_rate, target_rate, channels):
    if source_rate == target_rate:
        return data
    import numpy as np
    import soxr
    samples = np.frombuffer(data, dtype='int16')
    if channels > 1:
        samples = samples.reshape(-1, channels)
    return soxr.resample(samples, source_rate, target_rate, quality='HQ').astype('int16').tobytes()


def capture_pcm(cancelled, device=None):
    if sys.platform != "win32":
        raise ValueError("recognize record 仅支持 Windows 麦克风采集；其他平台请上传音频文件")
    from audio_device import open_input_stream
    if cancelled():
        raise ValueError("录音已取消，未上传")
    stream = None
    try:
        stream, rate = open_input_stream(device, target_rate=16000, channels=1, pkt_ms=20)
        if not 8000 <= rate <= 192000:
            raise ValueError("麦克风采样率不受支持")
        stream.start()
        print(f"[recognition] 开始录音10秒：麦克风设备={device}，声卡采样率={rate}Hz，上传PCM=16000Hz", flush=True)
        deadline = time.monotonic() + 15
        remaining = rate * 10
        data = bytearray()
        while remaining:
            if cancelled():
                raise ValueError("录音已取消，未上传")
            if time.monotonic() >= deadline:
                raise ValueError("麦克风采集超时，未上传")
            count = min(rate // 50, remaining)
            chunk, overflow = stream.read(count)
            if overflow or len(chunk) != count * 2:
                raise ValueError("麦克风采集丢帧，未上传，请检查设备后重新触发")
            data.extend(chunk)
            remaining -= count
        if cancelled():
            raise ValueError("录音已取消，未上传")
        result = _resample(bytes(data), rate, 16000, 1)
        if len(result) != 320000:
            raise ValueError("录音长度不正确，未上传")
        return result
    except ValueError:
        raise
    except Exception as exc:
        raise ValueError(f"麦克风采集失败（设备={device}）：{str(exc)[:240]}") from None
    finally:
        _close(stream)


def _playback_wav_bytes(filename):
    # Some completed TTS downloads retain streaming length placeholders.
    # Normalize only known placeholder pairs; ordinary truncation remains an error.
    with open(filename, 'rb') as source:
        data = bytearray(source.read((10 << 20) + 1))
    if len(data) > 10 << 20 or len(data) < 44:
        raise ValueError('语音文件大小无效')
    if data[:4] != b'RIFF' or data[8:12] != b'WAVE':
        raise ValueError('语音文件不是 WAV')
    riff_size = struct.unpack_from('<I', data, 4)[0]
    offset = 12
    block_align = 0
    while offset + 8 <= len(data):
        size = struct.unpack_from('<I', data, offset + 4)[0]
        start = offset + 8
        if data[offset:offset + 4] == b'fmt ' and size >= 16 and start + 16 <= len(data):
            block_align = struct.unpack_from('<H', data, start + 12)[0]
        if data[offset:offset + 4] == b'data':
            if (riff_size, size) in ((2147483583, 2147483547), (0xffffffff, 0xffffffff)):
                struct.pack_into('<I', data, 4, len(data) - 8)
                struct.pack_into('<I', data, offset + 4, len(data) - start)
            elif riff_size != len(data) - 8 or size > len(data) - start:
                raise ValueError('语音文件不完整')
            actual_size = struct.unpack_from('<I', data, offset + 4)[0]
            if block_align not in (2, 4) or actual_size % block_align:
                raise ValueError('语音采样数据不完整')
            return io.BytesIO(data)
        if size > len(data) - start:
            raise ValueError('语音文件不完整')
        offset = start + size + size % 2
    raise ValueError('语音文件缺少音频数据')


def play_wav(filename, cancelled, device=None):
    import wave
    if sys.platform != 'win32':
        raise ValueError('自动播放仅支持 Windows')
    from audio_device import open_output_stream
    stream = None
    try:
        with wave.open(_playback_wav_bytes(filename), 'rb') as source:
            if source.getsampwidth() != 2 or source.getcomptype() != 'NONE' or source.getnchannels() not in (1, 2):
                raise ValueError('播放需要单/双声道 PCM16 WAV')
            rate, channels = source.getframerate(), source.getnchannels()
            frames = source.getnframes()
            if frames <= 0:
                raise ValueError('语音文件没有可播放的采样')
            if not 8000 <= rate <= 48000 or frames > rate * 180:
                raise ValueError('返回语音超过播放规格限制')
            data = source.readframes(frames)
            if len(data) != frames * channels * 2:
                raise ValueError('语音文件不完整')
        if cancelled():
            raise ValueError('播放已取消')
        stream, actual = open_output_stream(device, target_rate=rate, channels=channels, pkt_ms=20)
        data = _resample(data, rate, actual, channels)
        print(f'[recognition] 播放语音：扬声器设备={device}，音频={rate}Hz，声卡={actual}Hz', flush=True)
        stream.start()
        step = max(1, actual // 50) * channels * 2
        deadline = time.monotonic() + frames / rate + 10
        for offset in range(0, len(data), step):
            if cancelled():
                raise ValueError('播放已取消')
            if time.monotonic() >= deadline:
                raise ValueError('播放超时')
            stream.write(data[offset:offset + step])
        if cancelled():
            raise ValueError('播放已取消')
    except ValueError:
        raise
    except Exception as exc:
        raise ValueError(f'语音播放失败（设备={device}）：{str(exc)[:240]}') from None
    finally:
        _close(stream)
