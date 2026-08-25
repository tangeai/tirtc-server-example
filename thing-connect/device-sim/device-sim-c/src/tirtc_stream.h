/** \file tirtc_stream.h
 * \brief TiRTC streaming module — passive server mode (on_conn_accepted).
 *
 * Pushes encoded audio and optional encoded video files to connecting clients.
 * This is the default mode of the device simulator.
 */

#ifndef TIRTC_STREAM_H
#define TIRTC_STREAM_H

#include <stdio.h>

#ifdef __cplusplus
extern "C" {
#endif

/** Register stream handlers with the process-wide TiRTC runtime. */
int stream_service_register(void);

/** Configure and activate stream service resources. */
int stream_service_start(const char *video_path, const char *audio_path,
                         const char *audio_format, const char *video_format);

/** Stop stream media and disconnect its active connection. */
void stream_service_stop(void);

/** True while the stream service owns the current session. */
int stream_is_active(void);

/* H.264/A-law file-source helper API. */
typedef struct {
    FILE *vf;
    FILE *af;
    char *first_pending;
    int first_len;
    int is_first_key;
} H264FileSource;

int h264_source_open(H264FileSource *src, const char *video_path,
                     const char *audio_path);
int h264_source_next_audio(H264FileSource *src, unsigned char *pkt,
                           int pkt_size);
int h264_source_next_video(H264FileSource *src, unsigned char **out_data,
                           int *is_key, int force_key);
void h264_source_close(H264FileSource *src);

#ifdef __cplusplus
}
#endif

#endif /* TIRTC_STREAM_H */
