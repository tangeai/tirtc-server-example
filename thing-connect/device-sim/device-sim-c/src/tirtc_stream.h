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

/** Initialize TiRTC SDK in streaming (passive-server) mode.
 *  device_id and secret_key follow the TiRTC 2.2.1 credential API.
 *  Returns 0 on success, exits on failure. */
int stream_init_sdk(const char *device_id, const char *secret_key, const char *client_id, const char *endpoint,
                    const char *video_path, const char *audio_path);
int stream_init_sdk_ex(const char *device_id, const char *secret_key,
                       const char *client_id, const char *endpoint,
                       const char *video_path, const char *audio_path,
                       const char *audio_format, const char *video_format);

/** Stop TiRTC SDK and clean up. */
void stream_uninit_sdk(void);

/** True while the SDK is running. */
int stream_is_active(void);

/*
 * Legacy H.264/G.711A source API.  Kept so existing embedding code still
 * compiles; new simulator code uses file_media_source.h directly.
 */
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
