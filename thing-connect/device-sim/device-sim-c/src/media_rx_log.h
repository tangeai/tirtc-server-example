#ifndef MEDIA_RX_LOG_H
#define MEDIA_RX_LOG_H

#include <pthread.h>
#include <stdint.h>

#include "tirtc/tiRTC.h"

typedef struct {
    pthread_mutex_t lock;
    uint64_t audio_frames;
    uint64_t video_frames;
} MediaRxLog;

typedef struct {
    char session[24];
    uint64_t count;
    uint32_t media;
    uint32_t flags;
    uint32_t length;
    int video;
} MediaRxNotice;

#define MEDIA_RX_LOG_INITIALIZER { PTHREAD_MUTEX_INITIALIZER, 0, 0 }

void media_rx_log_reset(MediaRxLog *log);
int media_rx_log_note_audio(MediaRxLog *log, const char *session,
                            const TIRTCFRAMEINFO *frame,
                            MediaRxNotice *notice);
int media_rx_log_note_video(MediaRxLog *log, const char *session,
                            const TIRTCFRAMEINFO *frame,
                            MediaRxNotice *notice);
void media_rx_log_emit(void *context, const void *data, size_t length);

#endif
