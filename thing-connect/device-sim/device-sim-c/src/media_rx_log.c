#define LOG_MODULE "media-rx"
#include "common.h"
#include "media_rx_log.h"

static uint64_t _next(MediaRxLog *log, int video) {
    pthread_mutex_lock(&log->lock);
    uint64_t value = video ? ++log->video_frames : ++log->audio_frames;
    pthread_mutex_unlock(&log->lock);
    return value;
}

void media_rx_log_reset(MediaRxLog *log) {
    if (!log) return;
    pthread_mutex_lock(&log->lock);
    log->audio_frames = 0;
    log->video_frames = 0;
    pthread_mutex_unlock(&log->lock);
}

void media_rx_log_audio(MediaRxLog *log, const char *session,
                        const TIRTCFRAMEINFO *frame) {
    if (!log || !frame) return;
    uint64_t count = _next(log, 0);
    if (count == 1) {
        LOG_I("%s 收到下行音频，日志后丢弃: media=%u flags=%u bytes=%u",
              session, frame->media, frame->flags, frame->length);
    } else if (count % 50 == 0) {
        LOG_D("%s 下行音频已丢弃: frames=%llu last_bytes=%u",
              session, (unsigned long long)count, frame->length);
    }
}

void media_rx_log_video(MediaRxLog *log, const char *session,
                        const TIRTCFRAMEINFO *frame) {
    if (!log || !frame) return;
    uint64_t count = _next(log, 1);
    if (count == 1) {
        LOG_I("%s 收到下行视频，日志后丢弃: media=%u key=%d bytes=%u",
              session, frame->media, TIRTC_IS_KEY_FRAME(frame->flags) ? 1 : 0,
              frame->length);
    } else if (count % 50 == 0) {
        LOG_D("%s 下行视频已丢弃: frames=%llu last_bytes=%u",
              session, (unsigned long long)count, frame->length);
    }
}
