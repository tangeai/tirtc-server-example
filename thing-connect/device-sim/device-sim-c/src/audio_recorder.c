#define LOG_MODULE "audio-rec"
#include "common.h"
#include "audio_recorder.h"

#include <limits.h>
#include <sys/stat.h>

static int _mkdir_one(const char *path) {
    if (mkdir(path, 0755) == 0 || errno == EEXIST) return 0;
    return -1;
}

static int _mkdir_p(const char *path) {
    if (!path || !path[0]) return -1;
    char copy[1024];
    if (snprintf(copy, sizeof(copy), "%s", path) >= (int)sizeof(copy))
        return -1;

    for (char *p = copy + (copy[0] == '/' ? 1 : 0); *p; ++p) {
        if (*p != '/') continue;
        *p = '\0';
        if (copy[0] && _mkdir_one(copy) != 0) return -1;
        *p = '/';
    }
    return _mkdir_one(copy);
}

static const char *_encoding_name(uint8_t media) {
    switch (media) {
    case TIRTC_AUDIO_PCM:  return "s16le";
    case TIRTC_AUDIO_ALAW: return "alaw";
    case TIRTC_AUDIO_AAC:  return "aac";
    case TIRTC_AUDIO_OPUS: return "opus";
    case TIRTC_AUDIO_AMR:  return "amr";
    default:               return "unknown";
    }
}

static int _sample_rate(uint8_t flags) {
    return flags == TIRTC_AUDIOSAMPLE_16K16B1C ? 16000 : 8000;
}

static int16_t _decode_alaw(uint8_t value) {
    value ^= 0x55;
    int sample = (value & 0x0f) << 4;
    int segment = (value & 0x70) >> 4;
    if (segment == 0) {
        sample += 8;
    } else if (segment == 1) {
        sample += 0x108;
    } else {
        sample += 0x108;
        sample <<= segment - 1;
    }
    return (int16_t)((value & 0x80) ? sample : -sample);
}

static void _put_u16le(unsigned char *target, uint16_t value) {
    target[0] = (unsigned char)(value & 0xff);
    target[1] = (unsigned char)((value >> 8) & 0xff);
}

static void _put_u32le(unsigned char *target, uint32_t value) {
    target[0] = (unsigned char)(value & 0xff);
    target[1] = (unsigned char)((value >> 8) & 0xff);
    target[2] = (unsigned char)((value >> 16) & 0xff);
    target[3] = (unsigned char)((value >> 24) & 0xff);
}

static int _write_wav_header(FILE *file, int sample_rate,
                             uint32_t data_bytes) {
    unsigned char header[44] = {0};
    memcpy(header, "RIFF", 4);
    _put_u32le(header + 4, 36u + data_bytes);
    memcpy(header + 8, "WAVEfmt ", 8);
    _put_u32le(header + 16, 16);
    _put_u16le(header + 20, 1);
    _put_u16le(header + 22, 1);
    _put_u32le(header + 24, (uint32_t)sample_rate);
    _put_u32le(header + 28, (uint32_t)sample_rate * 2u);
    _put_u16le(header + 32, 2);
    _put_u16le(header + 34, 16);
    memcpy(header + 36, "data", 4);
    _put_u32le(header + 40, data_bytes);
    return fwrite(header, 1, sizeof(header), file) == sizeof(header) ? 0 : -1;
}

static void _derive_sidecar_path(char *target, size_t target_size,
                                 const char *raw_path, const char *suffix) {
    size_t base_length = strlen(raw_path);
    const char *slash = strrchr(raw_path, '/');
    const char *dot = strrchr(raw_path, '.');
    if (dot && (!slash || dot > slash))
        base_length = (size_t)(dot - raw_path);
    if (base_length >= target_size ||
        snprintf(target, target_size, "%.*s%s",
                 (int)base_length, raw_path, suffix) >= (int)target_size)
        target[0] = '\0';
}

static void _open_wav_if_supported(AudioRecorder *recorder) {
    if (recorder->wav_file || recorder->wav_path[0]) return;
    if (recorder->media != TIRTC_AUDIO_PCM &&
        recorder->media != TIRTC_AUDIO_ALAW)
        return;

    _derive_sidecar_path(recorder->wav_path, sizeof(recorder->wav_path),
                         recorder->raw_path, ".wav");
    if (!recorder->wav_path[0]) return;
    recorder->wav_file = fopen(recorder->wav_path, "wb+");
    if (!recorder->wav_file ||
        _write_wav_header(recorder->wav_file,
                          _sample_rate(recorder->flags), 0) != 0) {
        if (recorder->wav_file) fclose(recorder->wav_file);
        recorder->wav_file = NULL;
        recorder->wav_path[0] = '\0';
    }
}

static int _write_wav_frame(AudioRecorder *recorder,
                            const AudioRecorderFrame *frame) {
    if (!recorder->wav_file) return 0;
    if (recorder->media == TIRTC_AUDIO_PCM) {
        if (fwrite(frame->data, 1, frame->length, recorder->wav_file) !=
            frame->length)
            return -1;
        recorder->wav_data_bytes += frame->length;
        return 0;
    }

    unsigned char decoded[1024];
    size_t offset = 0;
    while (offset < frame->length) {
        size_t count = frame->length - offset;
        if (count > sizeof(decoded) / 2)
            count = sizeof(decoded) / 2;
        for (size_t i = 0; i < count; ++i) {
            int16_t sample = _decode_alaw(frame->data[offset + i]);
            _put_u16le(decoded + i * 2, (uint16_t)sample);
        }
        size_t output_bytes = count * 2;
        if (fwrite(decoded, 1, output_bytes, recorder->wav_file) !=
            output_bytes)
            return -1;
        recorder->wav_data_bytes += output_bytes;
        offset += count;
    }
    return 0;
}

static void *_audio_recorder_worker(void *opaque) {
    AudioRecorder *recorder = opaque;
    AudioRecorderFrame frame;

    for (;;) {
        pthread_mutex_lock(&recorder->lock);
        while (!recorder->stopping && recorder->queue_count == 0)
            pthread_cond_wait(&recorder->wake, &recorder->lock);
        if (recorder->stopping && recorder->queue_count == 0) {
            pthread_mutex_unlock(&recorder->lock);
            break;
        }
        frame = recorder->queue[recorder->queue_head];
        recorder->queue_head =
            (recorder->queue_head + 1) % AUDIO_RECORDER_QUEUE_CAPACITY;
        recorder->queue_count--;
        pthread_mutex_unlock(&recorder->lock);

        if (!recorder->format_set) {
            recorder->stream_id = frame.stream_id;
            recorder->media = frame.media;
            recorder->flags = frame.flags;
            recorder->format_set = 1;
            _open_wav_if_supported(recorder);
        }
        if (fwrite(frame.data, 1, frame.length, recorder->raw_file) !=
            frame.length ||
            _write_wav_frame(recorder, &frame) != 0) {
            recorder->io_error = 1;
        } else {
            recorder->frame_count++;
        }
    }
    return NULL;
}

int audio_recorder_init(AudioRecorder *recorder) {
    if (!recorder) return -1;
    memset(recorder, 0, sizeof(*recorder));
    if (pthread_mutex_init(&recorder->lock, NULL) != 0) return -1;
    if (pthread_cond_init(&recorder->wake, NULL) != 0) {
        pthread_mutex_destroy(&recorder->lock);
        return -1;
    }
    recorder->initialized = 1;
    return 0;
}

void audio_recorder_destroy(AudioRecorder *recorder) {
    if (!recorder || !recorder->initialized) return;
    audio_recorder_close(recorder);
    pthread_cond_destroy(&recorder->wake);
    pthread_mutex_destroy(&recorder->lock);
    recorder->initialized = 0;
}

int audio_recorder_open(AudioRecorder *recorder, const char *root_dir,
                        const char *device_id, const char *filename) {
    if (!recorder || !recorder->initialized || !root_dir || !root_dir[0] ||
        !filename || !filename[0])
        return -1;
    audio_recorder_close(recorder);

    char output_dir[1024];
    if (device_id && device_id[0]) {
        if (snprintf(output_dir, sizeof(output_dir), "%s/%s",
                     root_dir, device_id) >= (int)sizeof(output_dir))
            return -1;
    } else if (snprintf(output_dir, sizeof(output_dir), "%s",
                        root_dir) >= (int)sizeof(output_dir)) {
        return -1;
    }
    if (_mkdir_p(output_dir) != 0) return -1;
    if (snprintf(recorder->raw_path, sizeof(recorder->raw_path), "%s/%s",
                 output_dir, filename) >= (int)sizeof(recorder->raw_path)) {
        recorder->raw_path[0] = '\0';
        return -1;
    }

    recorder->raw_file = fopen(recorder->raw_path, "wb");
    if (!recorder->raw_file) {
        recorder->raw_path[0] = '\0';
        return -1;
    }
    recorder->wav_path[0] = '\0';
    recorder->fmt_path[0] = '\0';
    recorder->queue_head = 0;
    recorder->queue_tail = 0;
    recorder->queue_count = 0;
    recorder->format_set = 0;
    recorder->frame_count = 0;
    recorder->dropped_frames = 0;
    recorder->wav_data_bytes = 0;
    recorder->io_error = 0;
    recorder->stopping = 0;
    recorder->accepting = 1;
    int rc = pthread_create(&recorder->worker, NULL,
                            _audio_recorder_worker, recorder);
    if (rc != 0) {
        recorder->accepting = 0;
        fclose(recorder->raw_file);
        recorder->raw_file = NULL;
        return -1;
    }
    recorder->worker_started = 1;
    return 0;
}

int audio_recorder_submit(AudioRecorder *recorder,
                          const TIRTCFRAMEINFO *frame, const void *data) {
    if (!recorder || !frame || !data || frame->length == 0 ||
        frame->length > AUDIO_RECORDER_MAX_FRAME_BYTES)
        return -1;

    pthread_mutex_lock(&recorder->lock);
    if (!recorder->accepting || !recorder->raw_file ||
        recorder->queue_count == AUDIO_RECORDER_QUEUE_CAPACITY) {
        recorder->dropped_frames++;
        pthread_mutex_unlock(&recorder->lock);
        return -1;
    }
    AudioRecorderFrame *slot = &recorder->queue[recorder->queue_tail];
    slot->stream_id = frame->stream_id;
    slot->media = frame->media;
    slot->flags = frame->flags;
    slot->length = frame->length;
    memcpy(slot->data, data, frame->length);
    recorder->queue_tail =
        (recorder->queue_tail + 1) % AUDIO_RECORDER_QUEUE_CAPACITY;
    recorder->queue_count++;
    pthread_cond_signal(&recorder->wake);
    pthread_mutex_unlock(&recorder->lock);
    return 0;
}

static void _write_format_metadata(AudioRecorder *recorder) {
    if (!recorder->format_set || !recorder->raw_path[0]) return;
    _derive_sidecar_path(recorder->fmt_path, sizeof(recorder->fmt_path),
                         recorder->raw_path, ".fmt.json");
    if (!recorder->fmt_path[0]) return;
    FILE *file = fopen(recorder->fmt_path, "w");
    if (!file) return;
    fprintf(
        file,
        "{\"stream_id\":%u,\"media\":%u,\"flags\":%u,"
        "\"encoding\":\"%s\",\"sample_rate\":%d,"
        "\"frames\":%llu,\"dropped_frames\":%llu}\n",
        recorder->stream_id, recorder->media, recorder->flags,
        _encoding_name(recorder->media), _sample_rate(recorder->flags),
        (unsigned long long)recorder->frame_count,
        (unsigned long long)recorder->dropped_frames);
    fclose(file);
}

void audio_recorder_close(AudioRecorder *recorder) {
    if (!recorder || !recorder->initialized) return;
    pthread_mutex_lock(&recorder->lock);
    while (recorder->closing)
        pthread_cond_wait(&recorder->wake, &recorder->lock);
    if (!recorder->worker_started && !recorder->raw_file &&
        !recorder->wav_file) {
        pthread_mutex_unlock(&recorder->lock);
        return;
    }
    recorder->closing = 1;
    int worker_started = recorder->worker_started;
    pthread_t worker = recorder->worker;
    recorder->accepting = 0;
    recorder->stopping = 1;
    pthread_cond_broadcast(&recorder->wake);
    pthread_mutex_unlock(&recorder->lock);

    if (worker_started && !pthread_equal(pthread_self(), worker))
        pthread_join(worker, NULL);

    if (recorder->wav_file) {
        uint32_t wav_bytes = recorder->wav_data_bytes > UINT32_MAX
                                 ? UINT32_MAX
                                 : (uint32_t)recorder->wav_data_bytes;
        if (fseek(recorder->wav_file, 0, SEEK_SET) != 0 ||
            _write_wav_header(recorder->wav_file,
                              _sample_rate(recorder->flags),
                              wav_bytes) != 0)
            recorder->io_error = 1;
        fclose(recorder->wav_file);
        recorder->wav_file = NULL;
    }
    if (recorder->raw_file) {
        if (fclose(recorder->raw_file) != 0) recorder->io_error = 1;
        recorder->raw_file = NULL;
    }
    _write_format_metadata(recorder);

    if (recorder->raw_path[0]) {
        LOG_I("接收音频已保存: raw=%s frames=%llu%s%s",
              recorder->raw_path,
              (unsigned long long)recorder->frame_count,
              recorder->wav_path[0] ? " wav=" : "",
              recorder->wav_path[0] ? recorder->wav_path : "");
    }
    if (recorder->dropped_frames)
        LOG_W("接收音频丢帧: %llu",
              (unsigned long long)recorder->dropped_frames);
    if (recorder->io_error)
        LOG_W("接收音频保存过程中发生 I/O 错误");

    pthread_mutex_lock(&recorder->lock);
    recorder->worker_started = 0;
    recorder->closing = 0;
    pthread_cond_broadcast(&recorder->wake);
    pthread_mutex_unlock(&recorder->lock);
}

const char *audio_recorder_raw_path(const AudioRecorder *recorder) {
    return recorder ? recorder->raw_path : "";
}

const char *audio_recorder_wav_path(const AudioRecorder *recorder) {
    return recorder ? recorder->wav_path : "";
}

uint64_t audio_recorder_frame_count(const AudioRecorder *recorder) {
    return recorder ? recorder->frame_count : 0;
}

uint64_t audio_recorder_dropped_frames(const AudioRecorder *recorder) {
    return recorder ? recorder->dropped_frames : 0;
}
