#include "stream_media_timeline.h"

#include <string.h>

void stream_media_timeline_init(StreamMediaTimeline *timeline) {
    if (timeline) memset(timeline, 0, sizeof(*timeline));
}

int stream_media_timeline_observe(StreamMediaTimeline *timeline,
                                  int has_video, int audio_subscribed,
                                  int video_subscribed, int64_t elapsed_ms,
                                  int *audio_started, int *video_started) {
    if (!timeline) return 0;
    int media_active = audio_subscribed || video_subscribed;
    int audio_enabled = media_active;
    int video_enabled = media_active && has_video;
    if (audio_started) *audio_started = 0;
    if (video_started) *video_started = 0;
    if (audio_enabled && !timeline->audio_was_enabled) {
        if (timeline->audio_pts_ms < elapsed_ms)
            timeline->audio_pts_ms = (double)elapsed_ms;
        if (audio_started) *audio_started = 1;
    }
    if (video_enabled && !timeline->video_was_enabled) {
        if (timeline->video_pts_ms < elapsed_ms)
            timeline->video_pts_ms = (double)elapsed_ms;
        if (video_started) *video_started = 1;
    }
    timeline->audio_was_enabled = audio_enabled;
    timeline->video_was_enabled = video_enabled;
    return media_active;
}

double stream_media_timeline_target(const StreamMediaTimeline *timeline,
                                    int has_video) {
    if (!timeline) return 0.0;
    if (has_video)
        return timeline->audio_pts_ms < timeline->video_pts_ms
                   ? timeline->audio_pts_ms
                   : timeline->video_pts_ms;
    return timeline->audio_pts_ms;
}

int stream_media_timeline_next_is_audio(const StreamMediaTimeline *timeline,
                                        int has_video) {
    return timeline &&
           (!has_video || timeline->audio_pts_ms <= timeline->video_pts_ms);
}

void stream_media_timeline_advance_audio(StreamMediaTimeline *timeline,
                                         double duration_ms) {
    if (timeline) timeline->audio_pts_ms += duration_ms;
}

void stream_media_timeline_advance_video(StreamMediaTimeline *timeline,
                                         double duration_ms) {
    if (timeline) timeline->video_pts_ms += duration_ms;
}
