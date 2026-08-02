#define LOG_MODULE "linux-adapter"
#include "common.h"
#include "device_adapter.h"
#include "file_media_source.h"
#include "linux_device_adapter.h"
#include "media_format.h"

#include <cjson/cJSON.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/random.h>
#include <sys/select.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

static int64_t _linux_monotonic_ms(void *context) {
    (void)context;
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0) return -1;
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

static int64_t _linux_wall_time_ms(void *context) {
    (void)context;
    struct timespec ts;
    if (clock_gettime(CLOCK_REALTIME, &ts) != 0) return -1;
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

static void _linux_sleep_ms(void *context, uint32_t milliseconds) {
    (void)context;
    struct timespec requested = {
        (time_t)(milliseconds / 1000u),
        (long)(milliseconds % 1000u) * 1000000L
    };
    while (nanosleep(&requested, &requested) != 0 && errno == EINTR) {}
}

static int _linux_identity_load(void *context, const char *path,
                                char *device_id, size_t id_size,
                                char *device_key, size_t key_size) {
    (void)context;
    if (!path || !path[0] || !device_id || !id_size ||
        !device_key || !key_size)
        return -1;
    FILE *file = fopen(path, "r");
    if (!file) return -1;
    int result = -1;
    if (fseek(file, 0, SEEK_END) != 0) goto done;
    long file_size = ftell(file);
    if (file_size <= 0 || file_size > 8192 ||
        fseek(file, 0, SEEK_SET) != 0)
        goto done;
    char *json = malloc((size_t)file_size + 1);
    if (!json) goto done;
    if (fread(json, 1, (size_t)file_size, file) != (size_t)file_size) {
        free(json);
        goto done;
    }
    json[file_size] = '\0';
    cJSON *root = cJSON_ParseWithOpts(json, NULL, 1);
    free(json);
    if (!root || !cJSON_IsObject(root)) {
        cJSON_Delete(root);
        goto done;
    }
    cJSON *id = cJSON_GetObjectItemCaseSensitive(root, "device_id");
    cJSON *key = cJSON_GetObjectItemCaseSensitive(root, "device_key");
    if (cJSON_IsString(id) && id->valuestring && id->valuestring[0] &&
        cJSON_IsString(key) && key->valuestring && key->valuestring[0] &&
        str_copy(device_id, id_size, id->valuestring) == 0 &&
        str_copy(device_key, key_size, key->valuestring) == 0)
        result = 0;
    cJSON_Delete(root);
done:
    fclose(file);
    return result;
}

static int _write_all(int fd, const char *data, size_t length) {
    while (length) {
        ssize_t written = write(fd, data, length);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) return -1;
        data += written;
        length -= (size_t)written;
    }
    return 0;
}

static int _linux_identity_save(void *context, const char *path,
                                const char *device_id,
                                const char *device_key) {
    (void)context;
    if (!path || !path[0] || !device_id || !device_id[0] ||
        !device_key || !device_key[0])
        return -1;
    cJSON *root = cJSON_CreateObject();
    if (!root) return -1;
    if (!cJSON_AddStringToObject(root, "device_id", device_id) ||
        !cJSON_AddStringToObject(root, "device_key", device_key)) {
        cJSON_Delete(root);
        return -1;
    }
    char *json = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json) return -1;
    char temp_path[1024];
    int path_length = snprintf(temp_path, sizeof(temp_path), "%s.tmp", path);
    if (path_length < 0 || path_length >= (int)sizeof(temp_path)) {
        free(json);
        return -1;
    }
    int fd = open(temp_path, O_WRONLY | O_CREAT | O_TRUNC, 0600);
    if (fd < 0) {
        free(json);
        return -1;
    }
    size_t length = strlen(json);
    int ok = _write_all(fd, json, length) == 0 && fsync(fd) == 0;
    int saved_errno = errno;
    if (close(fd) != 0) ok = 0;
    if (ok && rename(temp_path, path) == 0) {
        free(json);
        return 0;
    }
    if (!ok) errno = saved_errno;
    unlink(temp_path);
    free(json);
    return -1;
}

static int _linux_identity_clear(void *context, const char *path) {
    (void)context;
    if (!path || !path[0]) return -1;
    return unlink(path) == 0 || errno == ENOENT ? 0 : -1;
}

static int _linux_media_open(void *context,
                             const DeviceMediaSourceConfig *config,
                             void **handle_out) {
    (void)context;
    if (!config || !handle_out || !config->audio_locator ||
        !config->audio_format)
        return -1;
    const AudioFormat *audio = audio_format_find(config->audio_format);
    const VideoFormat *video = NULL;
    if (config->video_locator && config->video_locator[0]) {
        video = video_format_find(config->video_format);
        if (!video) return -1;
    }
    if (!audio) return -1;
    FileMediaSource *source = calloc(1, sizeof(*source));
    if (!source) return -1;
    if (file_media_source_open(source, config->audio_locator, audio,
                               config->video_locator ?
                                   config->video_locator : "",
                               video, config->audio_packet_ms) != 0) {
        free(source);
        return -1;
    }
    *handle_out = source;
    return 0;
}

static int _linux_media_has_video(void *context, void *handle) {
    (void)context;
    return file_media_source_has_video(handle);
}

static int _linux_media_next_audio(void *context, void *handle,
                                   DeviceMediaPacket *packet) {
    (void)context;
    const unsigned char *data;
    size_t length;
    double duration;
    if (!file_media_source_next_audio(handle, &data, &length, &duration))
        return 0;
    *packet = (DeviceMediaPacket){
        .data = data,
        .length = length,
        .duration_ms = duration,
    };
    return 1;
}

static int _linux_media_next_video(void *context, void *handle,
                                   int force_key_frame,
                                   DeviceMediaPacket *packet) {
    (void)context;
    const unsigned char *data;
    size_t length;
    int key_frame;
    if (!file_media_source_next_video(handle, &data, &length, &key_frame,
                                      force_key_frame))
        return 0;
    *packet = (DeviceMediaPacket){
        .data = data,
        .length = length,
        .key_frame = key_frame,
    };
    return 1;
}

static void _linux_media_close(void *context, void *handle) {
    (void)context;
    if (!handle) return;
    file_media_source_close(handle);
    free(handle);
}

static int _linux_poll_action(void *context, DeviceProductAction *action,
                              int timeout_ms) {
    (void)context;
    fd_set descriptors;
    FD_ZERO(&descriptors);
    FD_SET(STDIN_FILENO, &descriptors);
    if (timeout_ms < 0) timeout_ms = 0;
    struct timeval timeout = {
        .tv_sec = timeout_ms / 1000,
        .tv_usec = (timeout_ms % 1000) * 1000,
    };
    int selected = select(STDIN_FILENO + 1, &descriptors, NULL, NULL,
                          &timeout);
    if (selected < 0) return errno == EINTR ? 0 : -1;
    if (selected == 0) return 0;
    if (!fgets(action->raw_command, sizeof(action->raw_command), stdin))
        return feof(stdin) ? 0 : -1;
    action->raw_command[strcspn(action->raw_command, "\r\n")] = '\0';
    if (!action->raw_command[0]) return 0;
    action->type = DEVICE_ACTION_RAW_COMMAND;
    return 1;
}

static int _linux_random_bytes(void *context, unsigned char *output,
                               size_t length) {
    (void)context;
    size_t offset = 0;
    while (offset < length) {
        ssize_t count = getrandom(output + offset, length - offset, 0);
        if (count > 0) {
            offset += (size_t)count;
            continue;
        }
        if (count < 0 && errno == EINTR) continue;
        break;
    }
    if (offset == length) return 0;

    int fd = open("/dev/urandom", O_RDONLY | O_CLOEXEC);
    if (fd < 0) return -1;
    while (offset < length) {
        ssize_t count = read(fd, output + offset, length - offset);
        if (count > 0) offset += (size_t)count;
        else if (count < 0 && errno == EINTR) continue;
        else break;
    }
    close(fd);
    return offset == length ? 0 : -1;
}

static int _linux_allow_insecure(void *context) {
    (void)context;
    return 1;
}

int linux_device_adapter_build(DeviceAdapterV1 *adapter) {
    if (!adapter) return -1;
    memset(adapter, 0, sizeof(*adapter));
    adapter->abi_version = DEVICE_ADAPTER_ABI_V1;
    adapter->struct_size = sizeof(*adapter);
    adapter->platform.monotonic_ms = _linux_monotonic_ms;
    adapter->platform.wall_time_ms = _linux_wall_time_ms;
    adapter->platform.sleep_ms = _linux_sleep_ms;
    adapter->identity.load = _linux_identity_load;
    adapter->identity.save = _linux_identity_save;
    adapter->identity.clear = _linux_identity_clear;
    adapter->media_source.open = _linux_media_open;
    adapter->media_source.has_video = _linux_media_has_video;
    adapter->media_source.next_audio = _linux_media_next_audio;
    adapter->media_source.next_video = _linux_media_next_video;
    adapter->media_source.close = _linux_media_close;
    adapter->product.poll_action = _linux_poll_action;
    adapter->security.random_bytes = _linux_random_bytes;
    adapter->security.allow_insecure_transport = _linux_allow_insecure;
    return 0;
}

int linux_device_adapter_install_default(void) {
    if (device_adapter_is_installed()) return 0;
    DeviceAdapterV1 adapter;
    if (linux_device_adapter_build(&adapter) != 0) return -1;
    return device_adapter_install(&adapter);
}
