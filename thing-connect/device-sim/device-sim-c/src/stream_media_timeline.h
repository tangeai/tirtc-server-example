#ifndef STREAM_MEDIA_TIMELINE_H
#define STREAM_MEDIA_TIMELINE_H

#include <stdint.h>

typedef struct {
    double audio_pts_ms;
    double video_pts_ms;
    int audio_was_enabled;
    int video_was_enabled;
} StreamMediaTimeline;

void stream_media_timeline_init(StreamMediaTimeline *timeline);
int stream_media_timeline_observe(StreamMediaTimeline *timeline,
                                  int has_video, int audio_subscribed,
                                  int video_subscribed, int64_t elapsed_ms,
                                  int *audio_started, int *video_started);
double stream_media_timeline_target(const StreamMediaTimeline *timeline,
                                    int has_video);
int stream_media_timeline_next_is_audio(const StreamMediaTimeline *timeline,
                                        int has_video);
void stream_media_timeline_advance_audio(StreamMediaTimeline *timeline,
                                         double duration_ms);
void stream_media_timeline_advance_video(StreamMediaTimeline *timeline,
                                         double duration_ms);

#endif
