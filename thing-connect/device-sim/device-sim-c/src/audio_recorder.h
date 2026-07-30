#ifndef AUDIO_RECORDER_H
#define AUDIO_RECORDER_H

#include <pthread.h>
#include <stdint.h>
#include <stdio.h>

#include "tirtc/tiRTC.h"

#define AUDIO_RECORDER_QUEUE_CAPACITY 64
#define AUDIO_RECORDER_MAX_FRAME_BYTES 4096

typedef struct {
    uint8_t stream_id;
    uint8_t media;
    uint8_t flags;
    uint32_t length;
    unsigned char data[AUDIO_RECORDER_MAX_FRAME_BYTES];
} AudioRecorderFrame;

typedef struct {
    pthread_mutex_t lock;
    pthread_cond_t wake;
    pthread_t worker;
    int initialized;
    int worker_started;
    int accepting;
    int stopping;
    int closing;
    size_t queue_head;
    size_t queue_tail;
    size_t queue_count;
    AudioRecorderFrame queue[AUDIO_RECORDER_QUEUE_CAPACITY];

    FILE *raw_file;
    FILE *wav_file;
    char raw_path[1024];
    char wav_path[1024];
    char fmt_path[1024];
    uint8_t stream_id;
    uint8_t media;
    uint8_t flags;
    int format_set;
    uint64_t frame_count;
    uint64_t dropped_frames;
    uint64_t wav_data_bytes;
    int io_error;
} AudioRecorder;

int audio_recorder_init(AudioRecorder *recorder);
void audio_recorder_destroy(AudioRecorder *recorder);

int audio_recorder_open(AudioRecorder *recorder, const char *root_dir,
                        const char *device_id, const char *filename);
int audio_recorder_submit(AudioRecorder *recorder,
                          const TIRTCFRAMEINFO *frame, const void *data);
void audio_recorder_close(AudioRecorder *recorder);

const char *audio_recorder_raw_path(const AudioRecorder *recorder);
const char *audio_recorder_wav_path(const AudioRecorder *recorder);
uint64_t audio_recorder_frame_count(const AudioRecorder *recorder);
uint64_t audio_recorder_dropped_frames(const AudioRecorder *recorder);

#endif
