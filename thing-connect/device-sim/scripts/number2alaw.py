import subprocess
import audioop
import wave
import struct
import tempfile
import os
import argparse
import numpy as np
import soxr

# 中文数字 1 ~ 100
CHINESE_NUMBERS = [
    '一', '二', '三', '四', '五', '六', '七', '八', '九', '十',
    '十一', '十二', '十三', '十四', '十五', '十六', '十七', '十八', '十九', '二十',
    '二十一', '二十二', '二十三', '二十四', '二十五', '二十六', '二十七', '二十八', '二十九', '三十',
    '三十一', '三十二', '三十三', '三十四', '三十五', '三十六', '三十七', '三十八', '三十九', '四十',
    '四十一', '四十二', '四十三', '四十四', '四十五', '四十六', '四十七', '四十八', '四十九', '五十',
    '五十一', '五十二', '五十三', '五十四', '五十五', '五十六', '五十七', '五十八', '五十九', '六十',
    '六十一', '六十二', '六十三', '六十四', '六十五', '六十六', '六十七', '六十八', '六十九', '七十',
    '七十一', '七十二', '七十三', '七十四', '七十五', '七十六', '七十七', '七十八', '七十九', '八十',
    '八十一', '八十二', '八十三', '八十四', '八十五', '八十六', '八十七', '八十八', '八十九', '九十',
    '九十一', '九十二', '九十三', '九十四', '九十五', '九十六', '九十七', '九十八', '九十九', '一百'
]

# English numbers 1 ~ 100
_EN_ONES = ['', 'one', 'two', 'three', 'four', 'five', 'six', 'seven', 'eight', 'nine']
_EN_TEENS = ['ten', 'eleven', 'twelve', 'thirteen', 'fourteen', 'fifteen',
             'sixteen', 'seventeen', 'eighteen', 'nineteen']
_EN_TENS = ['', '', 'twenty', 'thirty', 'forty', 'fifty', 'sixty', 'seventy', 'eighty', 'ninety']

def _english_number(n):
    if n == 100:
        return 'one hundred'
    if 1 <= n <= 9:
        return _EN_ONES[n]
    if 10 <= n <= 19:
        return _EN_TEENS[n - 10]
    tens, ones = divmod(n, 10)
    if ones == 0:
        return _EN_TENS[tens]
    return f'{_EN_TENS[tens]}-{_EN_ONES[ones]}'

ENGLISH_NUMBERS = [_english_number(i) for i in range(1, 101)]

def synthesize_digit(text, lang='cmn', speed=220):
    """合成单个数字，返回 (16-bit PCM数据, 采样率)"""
    with tempfile.NamedTemporaryFile(suffix='.wav', delete=False) as f:
        temp_file = f.name

    cmd = ['espeak-ng', '-v', lang, '-s', str(speed), '-w', temp_file, text]
    subprocess.run(cmd, check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    if not os.path.exists(temp_file) or os.path.getsize(temp_file) == 0:
        raise RuntimeError(f'espeak-ng 生成音频失败：{text}')

    with wave.open(temp_file, 'rb') as wf:
        params = wf.getparams()
        data = wf.readframes(wf.getnframes())
    os.remove(temp_file)

    nchannels, sampwidth, framerate, nframes = params[0], params[1], params[2], params[3]

    if sampwidth != 2:
        data = audioop.lin2lin(data, sampwidth, 2)
        sampwidth = 2
    if nchannels != 1:
        data = audioop.tomono(data, sampwidth, 0.5, 0.5)
        nchannels = 1

    return data, framerate

def append_silence(pcm_data, sample_rate, silence_duration_sec):
    """在 PCM 末尾追加静音（零样本）"""
    silence_samples = int(sample_rate * silence_duration_sec)
    silence_bytes = b'\x00' * (silence_samples * 2)
    return pcm_data + silence_bytes

def synthesize_sequence_texts(texts, lang='zh', target_rate=16000, gap_sec=1.0, speed=220):
    """生成一组文本的 PCM 数据，每段总时长 = gap_sec 秒"""
    espeak_lang = 'en' if lang == 'en' else 'cmn'  # espeak-ng 用 cmn
    label = '英文' if lang == 'en' else '中文'
    all_pcm = b''
    source_rate = None

    for idx, text in enumerate(texts, start=1):
        print(f'合成第 {idx} 段（{label}）：{text}')

        pcm_data, rate = synthesize_digit(text, lang=espeak_lang, speed=speed)
        if source_rate is None:
            source_rate = rate

        # 如果某段采样率不一致，用 soxr 对齐到主采样率
        if rate != source_rate:
            samples = np.frombuffer(pcm_data, dtype=np.int16)
            samples = soxr.resample(samples, rate, source_rate, quality='HQ')
            pcm_data = samples.tobytes()

        duration = len(pcm_data) / (2 * source_rate)
        if duration >= gap_sec:
            print(f'  警告：{text} 发音时长 {duration:.2f}s，超过目标间隔，不添加静音')
            silence_needed = 0.0
        else:
            silence_needed = gap_sec - duration

        if silence_needed > 0:
            pcm_data = append_silence(pcm_data, source_rate, silence_needed)

        all_pcm += pcm_data

    # 用 soxr 做高质量重采样到目标采样率
    if source_rate != target_rate:
        print(f'重采样：{source_rate} Hz → {target_rate} Hz (soxr HQ)')
        samples = np.frombuffer(all_pcm, dtype=np.int16)
        samples = soxr.resample(samples, source_rate, target_rate, quality='HQ')
        all_pcm = samples.tobytes()

    return all_pcm, target_rate


def build_sequence(lang='zh', texts=None, repeat=1):
    if texts:
        return texts * max(repeat, 1)
    return ENGLISH_NUMBERS if lang == 'en' else CHINESE_NUMBERS

def write_wav_alaw(filename, alaw_data, sample_rate):
    """将 A-law 数据写入带头的 WAV 文件（格式 0x0006）"""
    data_size = len(alaw_data)
    total_size = 36 + data_size

    with open(filename, 'wb') as f:
        f.write(b'RIFF')
        f.write(struct.pack('<I', total_size))
        f.write(b'WAVE')

        f.write(b'fmt ')
        f.write(struct.pack('<I', 16))
        f.write(struct.pack('<H', 0x0006))      # WAVE_FORMAT_ALAW
        f.write(struct.pack('<H', 1))           # 单声道
        f.write(struct.pack('<I', sample_rate))
        f.write(struct.pack('<I', sample_rate * 1))
        f.write(struct.pack('<H', 1))
        f.write(struct.pack('<H', 8))

        f.write(b'data')
        f.write(struct.pack('<I', data_size))
        f.write(alaw_data)

def write_wav_pcm(filename, pcm_data, sample_rate):
    """将 16-bit PCM 数据写入 WAV 文件"""
    data_size = len(pcm_data)
    total_size = 36 + data_size
    byte_rate = sample_rate * 2  # 16-bit mono

    with open(filename, 'wb') as f:
        f.write(b'RIFF')
        f.write(struct.pack('<I', total_size))
        f.write(b'WAVE')

        f.write(b'fmt ')
        f.write(struct.pack('<I', 16))
        f.write(struct.pack('<H', 1))           # WAVE_FORMAT_PCM
        f.write(struct.pack('<H', 1))           # 单声道
        f.write(struct.pack('<I', sample_rate))
        f.write(struct.pack('<I', byte_rate))
        f.write(struct.pack('<H', 2))           # block align
        f.write(struct.pack('<H', 16))          # bits per sample

        f.write(b'data')
        f.write(struct.pack('<I', data_size))
        f.write(pcm_data)


def main():
    parser = argparse.ArgumentParser(description='生成 G.711 A-law / PCM 音频素材')
    parser.add_argument('--lang', choices=['zh', 'en'], default='zh',
                        help='语言：zh=中文（默认），en=英文')
    parser.add_argument('--speed', type=int, default=240, help='语速 (default: 240)')
    parser.add_argument('--gap', type=float, default=1.0, help='每段文本总时长秒数 (default: 1.0)')
    parser.add_argument('--rate', type=int, default=8000, help='目标采样率 (default: 8000)')
    parser.add_argument('--format', choices=['alaw', 'pcm'], default='alaw',
                        help='输出格式：alaw=G.711 A-law（默认），pcm=16-bit PCM')
    parser.add_argument('--text', action='append',
                        help='要播报的文本；可重复传入多次。未传时默认生成 1~100')
    parser.add_argument('--repeat', type=int, default=1,
                        help='当指定 --text 时，整组文本重复次数 (default: 1)')
    parser.add_argument('--output-prefix',
                        help='输出文件前缀；未传时沿用默认 numbers_chinese / numbers_english')
    args = parser.parse_args()

    prefix = args.output_prefix or ('numbers_english' if args.lang == 'en' else 'numbers_chinese')
    label = '英文' if args.lang == 'en' else '中文'
    texts = build_sequence(lang=args.lang, texts=args.text, repeat=args.repeat)

    pcm_data, rate = synthesize_sequence_texts(
        texts=texts,
        lang=args.lang,
        target_rate=args.rate,
        gap_sec=args.gap,
        speed=args.speed,
    )

    if args.format == 'pcm':
        wav_file = f'{prefix}_pcm.wav'
        write_wav_pcm(wav_file, pcm_data, rate)
        print(f'✅ WAV 文件：{wav_file} ({label}, PCM {rate} Hz, 单声道)')

        raw_file = f'{prefix}_pcm.raw'
        with open(raw_file, 'wb') as f:
            f.write(pcm_data)
        print(f'✅ 裸流文件：{raw_file} ({label}, 纯 PCM 16-bit {rate} Hz)')
    else:
        alaw_data = audioop.lin2alaw(pcm_data, 2)
        wav_file = f'{prefix}_g711a.wav'
        write_wav_alaw(wav_file, alaw_data, rate)
        print(f'✅ WAV 文件：{wav_file} ({label}, G.711 A-law, {rate} Hz, 单声道)')

        alaw_file = f'{prefix}_g711a.alaw'
        with open(alaw_file, 'wb') as f:
            f.write(alaw_data)
        print(f'✅ 裸流文件：{alaw_file} ({label}, 纯 G.711 A-law 数据)')

if __name__ == '__main__':
    main()
