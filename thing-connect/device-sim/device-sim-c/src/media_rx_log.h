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

#define MEDIA_RX_LOG_INITIALIZER { PTHREAD_MUTEX_INITIALIZER, 0, 0 }

void media_rx_log_reset(MediaRxLog *log);
void media_rx_log_audio(MediaRxLog *log, const char *session,
                        const TIRTCFRAMEINFO *frame);
void media_rx_log_video(MediaRxLog *log, const char *session,
                        const TIRTCFRAMEINFO *frame);

#endif
