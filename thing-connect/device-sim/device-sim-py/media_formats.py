"""TiRTC 媒体格式的唯一事实来源。"""

from dataclasses import dataclass

import tirtc_sdk as sdk


@dataclass(frozen=True)
class AudioFormat:
    name: str
    media: int
    flags: int
    sample_rate: int
    codec: str


@dataclass(frozen=True)
class VideoFormat:
    name: str
    media: int
    codec: str


def _audio(name: str, media: int, rate: int, codec: str) -> AudioFormat:
    flags = (sdk.TIRTC_AUDIOSAMPLE_8K16B1C if rate == 8000
             else sdk.TIRTC_AUDIOSAMPLE_16K16B1C)
    return AudioFormat(name, media, flags, rate, codec)


_CANONICAL_AUDIO_FORMATS = {
    spec.name: spec for spec in (
        _audio("alaw_8khz", sdk.TIRTC_AUDIO_ALAW, 8000, "alaw"),
        _audio("alaw_16khz", sdk.TIRTC_AUDIO_ALAW, 16000, "alaw"),
        _audio("amr_nb", sdk.TIRTC_AUDIO_AMR, 8000, "amr"),
        _audio("amr_wb", sdk.TIRTC_AUDIO_AMR, 16000, "amr"),
        _audio("opus_8khz", sdk.TIRTC_AUDIO_OPUS, 8000, "opus"),
        _audio("opus_16khz", sdk.TIRTC_AUDIO_OPUS, 16000, "opus"),
        _audio("pcm_s16le_8khz", sdk.TIRTC_AUDIO_PCM, 8000, "pcm"),
        _audio("pcm_s16le_16khz", sdk.TIRTC_AUDIO_PCM, 16000, "pcm"),
        _audio("aac_adts_8khz", sdk.TIRTC_AUDIO_AAC, 8000, "aac"),
        _audio("aac_adts_16khz", sdk.TIRTC_AUDIO_AAC, 16000, "aac"),
    )
}

_AUDIO_FORMAT_ALIASES = {
    "g711a_8k": "alaw_8khz",
    "g711a_16k": "alaw_16khz",
    "amr_8k": "amr_nb",
    "amr_16k": "amr_wb",
    "opus_8k": "opus_8khz",
    "opus_16k": "opus_16khz",
    "pcm_8k": "pcm_s16le_8khz",
    "pcm_16k": "pcm_s16le_16khz",
    "aac_8k": "aac_adts_8khz",
    "aac_16k": "aac_adts_16khz",
}

AUDIO_FORMATS = dict(_CANONICAL_AUDIO_FORMATS)
for alias, canonical in _AUDIO_FORMAT_ALIASES.items():
    AUDIO_FORMATS[alias] = _CANONICAL_AUDIO_FORMATS[canonical]

VIDEO_FORMATS = {
    spec.name: spec for spec in (
        VideoFormat("h264", sdk.TIRTC_VIDEO_H264, "h264"),
        VideoFormat("h265", sdk.TIRTC_VIDEO_H265, "h265"),
        VideoFormat("mjpeg", sdk.TIRTC_VIDEO_JPEG, "mjpeg"),
    )
}

_AI_CODEC_NAMES = {
    "pcm": "pcm",
    "alaw": "g711a",
    "amr": "amr",
    "opus": "opus",
}

CANONICAL_AUDIO_FORMAT_CHOICES = tuple(_CANONICAL_AUDIO_FORMATS)
AUDIO_FORMAT_CHOICES = CANONICAL_AUDIO_FORMAT_CHOICES
VIDEO_FORMAT_CHOICES = tuple(VIDEO_FORMATS)
WITH_MIC_AUDIO_FORMATS = ("alaw_8khz", "alaw_16khz")


def normalize_audio_format(name: str) -> str:
    if name in _CANONICAL_AUDIO_FORMATS:
        return name
    canonical = _AUDIO_FORMAT_ALIASES.get(name)
    if canonical:
        return canonical
    raise ValueError(f"不支持的音频格式: {name}")


def audio_packet_bytes(spec_or_name: "AudioFormat | str", packet_ms: int) -> int:
    spec = AUDIO_FORMATS[spec_or_name] if isinstance(spec_or_name, str) else spec_or_name
    if spec.codec == "pcm":
        return spec.sample_rate * 2 * packet_ms // 1000
    if spec.codec == "alaw":
        return spec.sample_rate * packet_ms // 1000
    return 0


def supports_live_audio_capture(spec_or_name: "AudioFormat | str") -> bool:
    spec = AUDIO_FORMATS[spec_or_name] if isinstance(spec_or_name, str) else spec_or_name
    return spec.codec in ("pcm", "alaw")


def validate_with_mic_audio_formats(up_format: str, down_format: str) -> None:
    """Keep the Windows sound-card path on its fully supported wire formats."""
    up = normalize_audio_format(up_format)
    down = normalize_audio_format(down_format)
    if up != down or up not in WITH_MIC_AUDIO_FORMATS:
        raise ValueError(
            "--with-mic 上下行必须使用相同的 alaw_8khz 或 alaw_16khz"
            "（G.711A、单声道）；PCM/AMR/Opus 请去掉 --with-mic 后使用预编码文件模式"
        )


def ai_audio_descriptor(spec_or_name: "AudioFormat | str") -> dict:
    """Build the codec descriptor required by AI start_session."""
    spec = AUDIO_FORMATS[spec_or_name] if isinstance(spec_or_name, str) else spec_or_name
    codec = _AI_CODEC_NAMES.get(spec.codec)
    if codec is None:
        raise ValueError(f"AI 对讲不支持音频编码: {spec.codec}")
    return {
        "codec": codec,
        "sample_rate": spec.sample_rate,
        "channels": 1,
    }


def video_file_extension(spec_or_name: "VideoFormat | str") -> str:
    spec = VIDEO_FORMATS[spec_or_name] if isinstance(spec_or_name, str) else spec_or_name
    return {
        "h264": "h264",
        "h265": "h265",
        "mjpeg": "mjpeg",
    }[spec.name]
