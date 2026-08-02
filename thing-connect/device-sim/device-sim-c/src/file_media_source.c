#define LOG_MODULE "media-file"
#include "common.h"
#include "file_media_source.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define MEDIA_FILE_LIMIT (256U * 1024U * 1024U)

typedef struct {
    FileMediaChunk *items;
    size_t count;
    size_t capacity;
} ChunkList;

typedef struct {
    unsigned char *data;
    size_t size;
    size_t capacity;
} ByteBuffer;

typedef struct {
    size_t offset;
    size_t length;
    int type;
} NalUnit;

static int _read_file(const char *path, unsigned char **out, size_t *out_size) {
    FILE *fp = fopen(path, "rb");
    if (!fp) {
        LOG_E("无法打开媒体文件 %s: %s", path, strerror(errno));
        return -1;
    }
    if (fseek(fp, 0, SEEK_END) != 0) {
        LOG_E("无法定位媒体文件 %s", path);
        fclose(fp);
        return -1;
    }
    long signed_size = ftell(fp);
    if (signed_size <= 0 || (unsigned long)signed_size > MEDIA_FILE_LIMIT) {
        LOG_E("媒体文件为空或过大（最大 256 MiB）: %s", path);
        fclose(fp);
        return -1;
    }
    if (fseek(fp, 0, SEEK_SET) != 0) {
        fclose(fp);
        return -1;
    }
    size_t size = (size_t)signed_size;
    unsigned char *data = malloc(size);
    if (!data) {
        fclose(fp);
        return -1;
    }
    if (fread(data, 1, size, fp) != size) {
        LOG_E("读取媒体文件失败: %s", path);
        free(data);
        fclose(fp);
        return -1;
    }
    fclose(fp);
    *out = data;
    *out_size = size;
    return 0;
}

static int _chunks_add(ChunkList *list, size_t offset, size_t length,
                       double duration_ms, int key) {
    if (list->count == list->capacity) {
        size_t next = list->capacity ? list->capacity * 2 : 64;
        FileMediaChunk *items = realloc(list->items, next * sizeof(*items));
        if (!items) return -1;
        list->items = items;
        list->capacity = next;
    }
    list->items[list->count++] = (FileMediaChunk){offset, length, duration_ms, key};
    return 0;
}

static int _buffer_append(ByteBuffer *buffer, const void *data, size_t length,
                          size_t *offset) {
    if (length > SIZE_MAX - buffer->size) return -1;
    size_t required = buffer->size + length;
    if (required > buffer->capacity) {
        size_t next = buffer->capacity ? buffer->capacity : 4096;
        while (next < required) {
            if (next > SIZE_MAX / 2) return -1;
            next *= 2;
        }
        unsigned char *resized = realloc(buffer->data, next);
        if (!resized) return -1;
        buffer->data = resized;
        buffer->capacity = next;
    }
    if (offset) *offset = buffer->size;
    memcpy(buffer->data + buffer->size, data, length);
    buffer->size = required;
    return 0;
}

static int _parse_fixed_audio(FileMediaSource *source, int packet_ms) {
    size_t bytes_per_sample = strcmp(source->audio_format->codec, "pcm") == 0 ? 2U : 1U;
    size_t packet_size = (size_t)source->audio_format->sample_rate *
                         bytes_per_sample * (size_t)packet_ms / 1000U;
    if (!packet_size) return -1;

    size_t padded_size = ((source->audio_size + packet_size - 1U) / packet_size) * packet_size;
    if (padded_size != source->audio_size) {
        unsigned char *data = realloc(source->audio_data, padded_size);
        if (!data) return -1;
        unsigned char padding = bytes_per_sample == 2U ? 0x00 : 0xd5;
        memset(data + source->audio_size, padding, padded_size - source->audio_size);
        source->audio_data = data;
        source->audio_size = padded_size;
    }
    ChunkList list = {0};
    for (size_t offset = 0; offset < source->audio_size; offset += packet_size) {
        if (_chunks_add(&list, offset, packet_size, (double)packet_ms, 0) != 0) {
            free(list.items);
            return -1;
        }
    }
    source->audio_chunks = list.items;
    source->audio_count = list.count;
    return 0;
}

static int _parse_amr(FileMediaSource *source) {
    static const unsigned char nb_sizes[] = {13, 14, 16, 18, 20, 21, 27, 32, 6};
    static const unsigned char wb_sizes[] = {18, 24, 33, 37, 41, 47, 51, 59, 61, 6};
    const unsigned char *magic;
    size_t magic_size;
    const unsigned char *sizes;
    size_t sizes_count;
    if (source->audio_format->sample_rate == 16000) {
        magic = (const unsigned char *)"#!AMR-WB\n";
        magic_size = 9;
        sizes = wb_sizes;
        sizes_count = sizeof(wb_sizes);
    } else {
        magic = (const unsigned char *)"#!AMR\n";
        magic_size = 6;
        sizes = nb_sizes;
        sizes_count = sizeof(nb_sizes);
    }
    if (source->audio_size < magic_size ||
        memcmp(source->audio_data, magic, magic_size) != 0) {
        LOG_E("AMR 文件头与所选格式不匹配");
        return -1;
    }
    ChunkList list = {0};
    size_t pos = magic_size;
    while (pos < source->audio_size) {
        unsigned int frame_type = (source->audio_data[pos] >> 3) & 0x0fU;
        if (frame_type >= sizes_count || pos + sizes[frame_type] > source->audio_size) {
            LOG_E("AMR 帧无效（offset=%zu type=%u）", pos, frame_type);
            free(list.items);
            return -1;
        }
        if (_chunks_add(&list, pos, sizes[frame_type], 20.0, 0) != 0) {
            free(list.items);
            return -1;
        }
        pos += sizes[frame_type];
    }
    source->audio_chunks = list.items;
    source->audio_count = list.count;
    return 0;
}

static int _parse_adts(FileMediaSource *source) {
    ChunkList list = {0};
    size_t pos = 0;
    while (pos < source->audio_size) {
        const unsigned char *data = source->audio_data;
        if (pos + 7 > source->audio_size || data[pos] != 0xff ||
            (data[pos + 1] & 0xf6U) != 0xf0U) {
            LOG_E("AAC 文件不是有效 ADTS 流（offset=%zu）", pos);
            free(list.items);
            return -1;
        }
        size_t size = ((size_t)(data[pos + 3] & 0x03U) << 11) |
                      ((size_t)data[pos + 4] << 3) |
                      ((size_t)data[pos + 5] >> 5);
        if (size < 7 || pos + size > source->audio_size) {
            LOG_E("AAC ADTS 帧长度无效（offset=%zu）", pos);
            free(list.items);
            return -1;
        }
        unsigned int raw_blocks = (data[pos + 6] & 0x03U) + 1U;
        double duration = 1024.0 * raw_blocks * 1000.0 /
                          source->audio_format->sample_rate;
        if (_chunks_add(&list, pos, size, duration, 0) != 0) {
            free(list.items);
            return -1;
        }
        pos += size;
    }
    source->audio_chunks = list.items;
    source->audio_count = list.count;
    return 0;
}

static double _opus_duration_ms(const unsigned char *packet, size_t length) {
    if (!length) return 0.0;
    unsigned int config = packet[0] >> 3;
    unsigned int code = packet[0] & 0x03U;
    double frame_duration;
    if (config < 12)
        frame_duration = (double[]){10.0, 20.0, 40.0, 60.0}[config & 0x03U];
    else if (config < 16)
        frame_duration = (double[]){10.0, 20.0}[config & 0x01U];
    else
        frame_duration = (double[]){2.5, 5.0, 10.0, 20.0}[config & 0x03U];
    unsigned int count;
    if (code == 0) count = 1;
    else if (code == 1 || code == 2) count = 2;
    else if (length >= 2) count = packet[1] & 0x3fU;
    else return 0.0;
    return count ? frame_duration * count : 0.0;
}

static int _finish_ogg_packet(ByteBuffer *payload, ChunkList *chunks,
                              ByteBuffer *packet) {
    if (!packet->size) return -1;
    if ((packet->size >= 8 && memcmp(packet->data, "OpusHead", 8) == 0) ||
        (packet->size >= 8 && memcmp(packet->data, "OpusTags", 8) == 0)) {
        packet->size = 0;
        return 0;
    }
    double duration = _opus_duration_ms(packet->data, packet->size);
    if (duration <= 0.0) return -1;
    size_t offset;
    if (_buffer_append(payload, packet->data, packet->size, &offset) != 0 ||
        _chunks_add(chunks, offset, packet->size, duration, 0) != 0)
        return -1;
    packet->size = 0;
    return 0;
}

static int _parse_ogg(FileMediaSource *source) {
    ByteBuffer payload = {0};
    ByteBuffer packet = {0};
    ChunkList chunks = {0};
    size_t pos = 0;
    int status = -1;
    while (pos < source->audio_size) {
        const unsigned char *data = source->audio_data;
        if (pos + 27 > source->audio_size || memcmp(data + pos, "OggS", 4) != 0) {
            LOG_E("Opus 文件不是有效 Ogg 流（offset=%zu）", pos);
            goto done;
        }
        size_t segments = data[pos + 26];
        size_t table_end = pos + 27 + segments;
        if (table_end > source->audio_size) goto done;
        size_t payload_size = 0;
        for (size_t i = 0; i < segments; ++i) payload_size += data[pos + 27 + i];
        size_t payload_end = table_end + payload_size;
        if (payload_end > source->audio_size) goto done;
        size_t cursor = table_end;
        for (size_t i = 0; i < segments; ++i) {
            size_t segment_size = data[pos + 27 + i];
            if (_buffer_append(&packet, data + cursor, segment_size, NULL) != 0)
                goto done;
            cursor += segment_size;
            if (segment_size < 255 && _finish_ogg_packet(&payload, &chunks, &packet) != 0)
                goto done;
        }
        pos = payload_end;
    }
    if (packet.size || !chunks.count) goto done;
    free(source->audio_data);
    source->audio_data = payload.data;
    source->audio_size = payload.size;
    source->audio_chunks = chunks.items;
    source->audio_count = chunks.count;
    payload.data = NULL;
    chunks.items = NULL;
    status = 0;
done:
    free(payload.data);
    free(packet.data);
    free(chunks.items);
    return status;
}

static int _parse_audio(FileMediaSource *source, int packet_ms) {
    const char *codec = source->audio_format->codec;
    if (strcmp(codec, "pcm") == 0 || strcmp(codec, "alaw") == 0)
        return _parse_fixed_audio(source, packet_ms);
    if (strcmp(codec, "amr") == 0) return _parse_amr(source);
    if (strcmp(codec, "aac") == 0) return _parse_adts(source);
    if (strcmp(codec, "opus") == 0) return _parse_ogg(source);
    return -1;
}

static int _find_start_code(const unsigned char *data, size_t size, size_t pos,
                            size_t *offset, size_t *code_size) {
    while (pos + 3 <= size) {
        if (data[pos] == 0 && data[pos + 1] == 0 && data[pos + 2] == 1) {
            *offset = pos;
            *code_size = 3;
            return 1;
        }
        if (pos + 4 <= size && data[pos] == 0 && data[pos + 1] == 0 &&
            data[pos + 2] == 0 && data[pos + 3] == 1) {
            *offset = pos;
            *code_size = 4;
            return 1;
        }
        ++pos;
    }
    return 0;
}

static int _nal_type(const unsigned char *data, size_t offset, size_t length,
                     size_t code_size, int h265) {
    if (length <= code_size) return -1;
    return h265 ? (data[offset + code_size] >> 1) & 0x3f
                : data[offset + code_size] & 0x1f;
}

static int _parse_annexb(FileMediaSource *source, int h265) {
    NalUnit *nals = NULL;
    size_t nal_count = 0, nal_capacity = 0;
    size_t pos = 0, offset, code_size;
    while (_find_start_code(source->video_data, source->video_size, pos,
                            &offset, &code_size)) {
        size_t next_offset, next_code_size;
        int found_next = _find_start_code(source->video_data, source->video_size,
                                          offset + code_size, &next_offset,
                                          &next_code_size);
        size_t end = found_next ? next_offset : source->video_size;
        if (nal_count == nal_capacity) {
            size_t next = nal_capacity ? nal_capacity * 2 : 64;
            NalUnit *resized = realloc(nals, next * sizeof(*nals));
            if (!resized) {
                free(nals);
                return -1;
            }
            nals = resized;
            nal_capacity = next;
        }
        nals[nal_count++] = (NalUnit){
            offset, end - offset,
            _nal_type(source->video_data, offset, end - offset, code_size, h265)
        };
        if (!found_next) break;
        pos = next_offset;
        (void)next_code_size;
    }
    if (!nal_count) {
        LOG_E("Annex-B 文件不包含 NALU");
        free(nals);
        return -1;
    }

    ChunkList frames = {0};
    size_t first = 0;
    int has_vcl = 0;
    int key = 0;
    for (size_t i = 0; i < nal_count; ++i) {
        int type = nals[i].type;
        int is_vcl = h265 ? type >= 0 && type <= 31
                          : type >= 1 && type <= 5;
        if (h265 ? (type >= 16 && type <= 21) : type == 5) key = 1;
        if (is_vcl) has_vcl = 1;

        int boundary = i + 1 == nal_count;
        if (!boundary) {
            int next = nals[i + 1].type;
            if (h265)
                boundary = next == 35 || ((next >= 0 && next <= 31) && has_vcl) ||
                           ((next == 32 || next == 33 || next == 34) && has_vcl);
            else
                boundary = next == 9 || ((next >= 1 && next <= 5) && has_vcl) ||
                           (next == 7 && has_vcl);
        }
        if (boundary) {
            size_t end = nals[i].offset + nals[i].length;
            if (_chunks_add(&frames, nals[first].offset,
                            end - nals[first].offset, 1000.0 / 15.0, key) != 0) {
                free(nals);
                free(frames.items);
                return -1;
            }
            first = i + 1;
            has_vcl = 0;
            key = 0;
        }
    }
    free(nals);
    source->video_chunks = frames.items;
    source->video_count = frames.count;
    return frames.count ? 0 : -1;
}

static int _parse_mjpeg(FileMediaSource *source) {
    ChunkList frames = {0};
    size_t pos = 0;
    while (pos + 2 <= source->video_size) {
        size_t start = pos;
        while (start + 2 <= source->video_size &&
               !(source->video_data[start] == 0xff &&
                 source->video_data[start + 1] == 0xd8))
            ++start;
        if (start + 2 > source->video_size) break;
        size_t end = start + 2;
        while (end + 2 <= source->video_size &&
               !(source->video_data[end] == 0xff &&
                 source->video_data[end + 1] == 0xd9))
            ++end;
        if (end + 2 > source->video_size) {
            LOG_E("MJPEG 文件末尾缺少 JPEG EOI");
            free(frames.items);
            return -1;
        }
        end += 2;
        if (_chunks_add(&frames, start, end - start, 1000.0 / 15.0, 1) != 0) {
            free(frames.items);
            return -1;
        }
        pos = end;
    }
    source->video_chunks = frames.items;
    source->video_count = frames.count;
    return frames.count ? 0 : -1;
}

static void _align_audio_to_video_index(FileMediaSource *source,
                                        size_t video_index) {
    source->audio_index = 0;
    double skip_ms = video_index * (1000.0 / 15.0);
    double skipped_ms = 0.0;
    while (source->audio_index < source->audio_count) {
        double duration = source->audio_chunks[source->audio_index].duration_ms;
        if (skipped_ms + duration > skip_ms) break;
        skipped_ms += duration;
        source->audio_index++;
    }
    if (source->audio_index >= source->audio_count) source->audio_index = 0;
}

static void _align_to_first_key(FileMediaSource *source) {
    source->first_key_index = 0;
    for (size_t i = 0; i < source->video_count; ++i) {
        if (source->video_chunks[i].key) {
            source->first_key_index = i;
            break;
        }
    }
    source->video_index = source->first_key_index;
    _align_audio_to_video_index(source, source->first_key_index);
}

int file_media_source_open(FileMediaSource *source,
                           const char *audio_path,
                           const AudioFormat *audio_format,
                           const char *video_path,
                           const VideoFormat *video_format,
                           int audio_packet_ms) {
    if (!source || !audio_path || !audio_path[0] || !audio_format ||
        audio_packet_ms <= 0 || ((video_path && video_path[0]) && !video_format))
        return -1;
    memset(source, 0, sizeof(*source));
    source->audio_format = audio_format;
    source->video_format = video_format;
    if (_read_file(audio_path, &source->audio_data, &source->audio_size) != 0 ||
        _parse_audio(source, audio_packet_ms) != 0 || !source->audio_count) {
        LOG_E("音频文件内容与格式 %s 不匹配: %s", audio_format->name, audio_path);
        file_media_source_close(source);
        return -1;
    }
    if (video_path && video_path[0]) {
        if (_read_file(video_path, &source->video_data, &source->video_size) != 0) {
            file_media_source_close(source);
            return -1;
        }
        int parsed;
        if (strcmp(video_format->name, "mjpeg") == 0)
            parsed = _parse_mjpeg(source);
        else
            parsed = _parse_annexb(source, strcmp(video_format->name, "h265") == 0);
        if (parsed != 0) {
            LOG_E("视频文件内容与格式 %s 不匹配: %s", video_format->name, video_path);
            file_media_source_close(source);
            return -1;
        }
        _align_to_first_key(source);
    }
    LOG_I("文件媒体已加载: audio=%s(%zu 帧), video=%s(%zu 帧)",
          audio_format->name, source->audio_count,
          source->video_count ? video_format->name : "关闭", source->video_count);
    return 0;
}

int file_media_source_next_audio(FileMediaSource *source,
                                 const unsigned char **data,
                                 size_t *length,
                                 double *duration_ms) {
    if (!source || !data || !length || !duration_ms || !source->audio_count)
        return 0;
    if (source->audio_index >= source->audio_count) source->audio_index = 0;
    const FileMediaChunk *chunk = &source->audio_chunks[source->audio_index++];
    *data = source->audio_data + chunk->offset;
    *length = chunk->length;
    *duration_ms = chunk->duration_ms;
    return 1;
}

int file_media_source_next_video(FileMediaSource *source,
                                 const unsigned char **data,
                                 size_t *length,
                                 int *is_key,
                                 int force_key) {
    if (!source || !data || !length || !is_key || !source->video_count)
        return 0;
    size_t requested_index = source->video_index;
    if (force_key) {
        size_t checked = 0;
        while (checked < source->video_count) {
            if (source->video_index >= source->video_count) source->video_index = 0;
            if (source->video_chunks[source->video_index].key) break;
            source->video_index++;
            checked++;
        }
        if (checked == source->video_count) return 0;
    }
    if (source->video_index >= source->video_count) source->video_index = 0;
    if (force_key && source->video_index != requested_index)
        _align_audio_to_video_index(source, source->video_index);
    const FileMediaChunk *chunk = &source->video_chunks[source->video_index++];
    *data = source->video_data + chunk->offset;
    *length = chunk->length;
    *is_key = chunk->key;
    return 1;
}

int file_media_source_has_video(const FileMediaSource *source) {
    return source && source->video_count > 0;
}

void file_media_source_close(FileMediaSource *source) {
    if (!source) return;
    free(source->audio_data);
    free(source->audio_chunks);
    free(source->video_data);
    free(source->video_chunks);
    memset(source, 0, sizeof(*source));
}
