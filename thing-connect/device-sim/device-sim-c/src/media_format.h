#ifndef MEDIA_FORMAT_H
#define MEDIA_FORMAT_H

#include <stdint.h>

/* Media formats exposed by the TiRTC C SDK and used by the simulator's
 * configuration layer.  Hardware media adapters should use the negotiated
 * media/flags values when constructing or consuming TIRTCFRAMEINFO frames. */
typedef struct {
    const char *name;
    const char *codec;
    uint8_t media;
    uint8_t flags;
    int sample_rate;
} AudioFormat;

typedef struct {
    const char *name;
    const char *codec;
    uint8_t media;
} VideoFormat;

const AudioFormat *audio_format_find(const char *name);
const char *audio_format_choices(void);
const VideoFormat *video_format_find(const char *name);
const char *video_format_choices(void);

#endif
