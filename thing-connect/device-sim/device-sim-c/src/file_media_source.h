#ifndef FILE_MEDIA_SOURCE_H
#define FILE_MEDIA_SOURCE_H

#include <stddef.h>
#include <stdint.h>

#include "media_format.h"

typedef struct {
    size_t offset;
    size_t length;
    double duration_ms;
    int key;
} FileMediaChunk;

typedef struct {
    unsigned char *audio_data;
    size_t audio_size;
    FileMediaChunk *audio_chunks;
    size_t audio_count;
    size_t audio_index;

    unsigned char *video_data;
    size_t video_size;
    FileMediaChunk *video_chunks;
    size_t video_count;
    size_t video_index;
    size_t first_key_index;

    const AudioFormat *audio_format;
    const VideoFormat *video_format;
} FileMediaSource;

/*
 * Open encoded media files for looping playback.  video_path may be empty for
 * audio-only sessions.  The simulator does not transcode media.
 */
int file_media_source_open(FileMediaSource *source,
                           const char *audio_path,
                           const AudioFormat *audio_format,
                           const char *video_path,
                           const VideoFormat *video_format,
                           int audio_packet_ms);

int file_media_source_next_audio(FileMediaSource *source,
                                 const unsigned char **data,
                                 size_t *length,
                                 double *duration_ms);

int file_media_source_next_video(FileMediaSource *source,
                                 const unsigned char **data,
                                 size_t *length,
                                 int *is_key,
                                 int force_key);

int file_media_source_has_video(const FileMediaSource *source);
void file_media_source_close(FileMediaSource *source);

#endif
