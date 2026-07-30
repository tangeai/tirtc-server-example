#define LOG_MODULE "media"
#include "common.h"
#include "media_format.h"

#include <string.h>

#include "tirtc/tiRTC.h"

static const AudioFormat k_audio_formats[] = {
    {"alaw_8khz",       "alaw", TIRTC_AUDIO_ALAW, TIRTC_AUDIOSAMPLE_8K16B1C,  8000},
    {"alaw_16khz",      "alaw", TIRTC_AUDIO_ALAW, TIRTC_AUDIOSAMPLE_16K16B1C, 16000},
    {"amr_nb",           "amr", TIRTC_AUDIO_AMR,  TIRTC_AUDIOSAMPLE_8K16B1C,  8000},
    {"amr_wb",           "amr", TIRTC_AUDIO_AMR,  TIRTC_AUDIOSAMPLE_16K16B1C, 16000},
    {"opus_8khz",       "opus", TIRTC_AUDIO_OPUS, TIRTC_AUDIOSAMPLE_8K16B1C,  8000},
    {"opus_16khz",      "opus", TIRTC_AUDIO_OPUS, TIRTC_AUDIOSAMPLE_16K16B1C, 16000},
    {"pcm_s16le_8khz",   "pcm", TIRTC_AUDIO_PCM,  TIRTC_AUDIOSAMPLE_8K16B1C,  8000},
    {"pcm_s16le_16khz",  "pcm", TIRTC_AUDIO_PCM,  TIRTC_AUDIOSAMPLE_16K16B1C, 16000},
    {"aac_adts_8khz",    "aac", TIRTC_AUDIO_AAC,  TIRTC_AUDIOSAMPLE_8K16B1C,  8000},
    {"aac_adts_16khz",   "aac", TIRTC_AUDIO_AAC,  TIRTC_AUDIOSAMPLE_16K16B1C, 16000},
};

typedef struct {
    const char *alias;
    const char *canonical;
} AudioFormatAlias;

static const AudioFormatAlias k_audio_aliases[] = {
    {"g711a_8k",  "alaw_8khz"},
    {"g711a_16k", "alaw_16khz"},
    {"amr_8k",    "amr_nb"},
    {"amr_16k",   "amr_wb"},
    {"opus_8k",   "opus_8khz"},
    {"opus_16k",  "opus_16khz"},
    {"pcm_8k",    "pcm_s16le_8khz"},
    {"pcm_16k",   "pcm_s16le_16khz"},
    {"aac_8k",    "aac_adts_8khz"},
    {"aac_16k",   "aac_adts_16khz"},
};

static const VideoFormat k_video_formats[] = {
    {"h264",  "h264",  TIRTC_VIDEO_H264},
    {"h265",  "h265",  TIRTC_VIDEO_H265},
    {"mjpeg", "mjpeg", TIRTC_VIDEO_JPEG},
};

const AudioFormat *audio_format_find(const char *name) {
    if (!name) return NULL;
    for (size_t i = 0; i < sizeof(k_audio_formats) / sizeof(k_audio_formats[0]); ++i)
        if (strcmp(name, k_audio_formats[i].name) == 0)
            return &k_audio_formats[i];
    for (size_t i = 0; i < sizeof(k_audio_aliases) / sizeof(k_audio_aliases[0]); ++i)
        if (strcmp(name, k_audio_aliases[i].alias) == 0)
            return audio_format_find(k_audio_aliases[i].canonical);
    return NULL;
}

const char *audio_format_choices(void) {
    return "alaw_8khz|alaw_16khz|amr_nb|amr_wb|opus_8khz|opus_16khz|"
           "pcm_s16le_8khz|pcm_s16le_16khz|aac_adts_8khz|aac_adts_16khz";
}

const char *audio_format_ai_codec(const AudioFormat *format) {
    if (!format || !format->codec) return NULL;
    if (strcmp(format->codec, "alaw") == 0) return "g711a";
    if (strcmp(format->codec, "pcm") == 0 ||
        strcmp(format->codec, "amr") == 0 ||
        strcmp(format->codec, "opus") == 0)
        return format->codec;
    return NULL;
}

const VideoFormat *video_format_find(const char *name) {
    if (!name) return NULL;
    for (size_t i = 0; i < sizeof(k_video_formats) / sizeof(k_video_formats[0]); ++i)
        if (strcmp(name, k_video_formats[i].name) == 0)
            return &k_video_formats[i];
    return NULL;
}

const char *video_format_choices(void) {
    return "h264|h265|mjpeg";
}
