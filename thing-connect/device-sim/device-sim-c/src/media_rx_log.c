#define LOG_MODULE "media-rx"
#include "common.h"
#include "media_rx_log.h"

static int _note(MediaRxLog *log, const char *session,
                 const TIRTCFRAMEINFO *frame, int video,
                 MediaRxNotice *notice) {
    if (!log || !frame || !notice) return 0;
    pthread_mutex_lock(&log->lock);
    uint64_t value = video ? ++log->video_frames : ++log->audio_frames;
    pthread_mutex_unlock(&log->lock);
    if (value != 1 && value % 50 != 0) return 0;

    memset(notice, 0, sizeof(*notice));
    snprintf(notice->session, sizeof(notice->session), "%s",
             session ? session : "");
    notice->count = value;
    notice->media = frame->media;
    notice->flags = frame->flags;
    notice->length = frame->length;
    notice->video = video;
    return 1;
}

void media_rx_log_reset(MediaRxLog *log) {
    if (!log) return;
    pthread_mutex_lock(&log->lock);
    log->audio_frames = 0;
    log->video_frames = 0;
    pthread_mutex_unlock(&log->lock);
}

int media_rx_log_note_audio(MediaRxLog *log, const char *session,
                            const TIRTCFRAMEINFO *frame,
                            MediaRxNotice *notice) {
    return _note(log, session, frame, 0, notice);
}

int media_rx_log_note_video(MediaRxLog *log, const char *session,
                            const TIRTCFRAMEINFO *frame,
                            MediaRxNotice *notice) {
    return _note(log, session, frame, 1, notice);
}

void media_rx_log_emit(void *context, const void *data, size_t length) {
    (void)context;
    if (!data || length != sizeof(MediaRxNotice)) return;
    const MediaRxNotice *notice = data;
    if (notice->video) {
        if (notice->count == 1) {
            LOG_I("%s 收到下行视频，日志后丢弃: media=%u key=%d bytes=%u",
                  notice->session, notice->media,
                  TIRTC_IS_KEY_FRAME(notice->flags) ? 1 : 0,
                  notice->length);
        } else {
            LOG_D("%s 下行视频已丢弃: frames=%llu last_bytes=%u",
                  notice->session, (unsigned long long)notice->count,
                  notice->length);
        }
    } else if (notice->count == 1) {
        LOG_I("%s 收到下行音频，日志后丢弃: media=%u flags=%u bytes=%u",
              notice->session, notice->media, notice->flags,
              notice->length);
    } else {
        LOG_D("%s 下行音频已丢弃: frames=%llu last_bytes=%u",
              notice->session, (unsigned long long)notice->count,
              notice->length);
    }
}
