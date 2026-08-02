#include "device_adapter.h"

#include <errno.h>
#include <pthread.h>
#include <stdio.h>
#include <string.h>
#include <time.h>

static pthread_rwlock_t s_adapter_lock = PTHREAD_RWLOCK_INITIALIZER;
static DeviceAdapterV1 s_adapter;
static int s_installed;
static uint64_t s_generations[DEVICE_BUSINESS_CALL + 1];

static DeviceAdapterV1 _adapter_snapshot(void) {
    DeviceAdapterV1 snapshot;
    pthread_rwlock_rdlock(&s_adapter_lock);
    snapshot = s_adapter;
    pthread_rwlock_unlock(&s_adapter_lock);
    return snapshot;
}

int device_adapter_install(const DeviceAdapterV1 *adapter) {
    if (!adapter || adapter->abi_version != DEVICE_ADAPTER_ABI_V1 ||
        adapter->struct_size < sizeof(DeviceAdapterV1))
        return -1;
    pthread_rwlock_wrlock(&s_adapter_lock);
    if (s_installed) {
        pthread_rwlock_unlock(&s_adapter_lock);
        return -1;
    }
    s_adapter = *adapter;
    memset(s_generations, 0, sizeof(s_generations));
    s_installed = 1;
    pthread_rwlock_unlock(&s_adapter_lock);
    return 0;
}

int device_adapter_is_installed(void) {
    pthread_rwlock_rdlock(&s_adapter_lock);
    int installed = s_installed;
    pthread_rwlock_unlock(&s_adapter_lock);
    return installed;
}

int64_t device_platform_monotonic_ms(void) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (adapter.platform.monotonic_ms)
        return adapter.platform.monotonic_ms(adapter.platform.context);
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

int64_t device_platform_wall_time_ms(void) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (adapter.platform.wall_time_ms)
        return adapter.platform.wall_time_ms(adapter.platform.context);
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

void device_platform_sleep_ms(uint32_t milliseconds) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (adapter.platform.sleep_ms) {
        adapter.platform.sleep_ms(adapter.platform.context, milliseconds);
        return;
    }
    struct timespec requested = {
        (time_t)(milliseconds / 1000u),
        (long)(milliseconds % 1000u) * 1000000L
    };
    while (nanosleep(&requested, &requested) != 0 && errno == EINTR) {}
}

int device_identity_load(const char *slot,
                         char *device_id, size_t device_id_size,
                         char *device_key, size_t device_key_size) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (!adapter.identity.load) return -1;
    return adapter.identity.load(adapter.identity.context, slot,
                                 device_id, device_id_size,
                                 device_key, device_key_size);
}

int device_identity_save(const char *slot, const char *device_id,
                         const char *device_key) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (!adapter.identity.save) return -1;
    return adapter.identity.save(adapter.identity.context, slot,
                                 device_id, device_key);
}

int device_identity_clear(const char *slot) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (!adapter.identity.clear) return -1;
    return adapter.identity.clear(adapter.identity.context, slot);
}

int device_media_source_open(DeviceMediaSource *source,
                             const DeviceMediaSourceConfig *config) {
    if (!source || !config) return -1;
    memset(source, 0, sizeof(*source));
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (!adapter.media_source.open || !adapter.media_source.close ||
        !adapter.media_source.next_audio)
        return -1;
    source->ops = adapter.media_source;
    if (source->ops.open(source->ops.context, config, &source->handle) != 0 ||
        !source->handle) {
        memset(source, 0, sizeof(*source));
        device_recovery_report(DEVICE_RECOVERY_MEDIA, -1,
                               "上行媒体源打开失败");
        return -1;
    }
    source->business = config->business;
    source->opened = 1;
    return 0;
}

int device_media_source_has_video(DeviceMediaSource *source) {
    if (!source || !source->opened || !source->ops.has_video) return 0;
    return source->ops.has_video(source->ops.context, source->handle);
}

int device_media_source_next_audio(DeviceMediaSource *source,
                                   DeviceMediaPacket *packet_out) {
    if (!source || !source->opened || !packet_out ||
        !source->ops.next_audio)
        return 0;
    memset(packet_out, 0, sizeof(*packet_out));
    int result = source->ops.next_audio(source->ops.context, source->handle,
                                        packet_out);
    if (result < 0) {
        device_recovery_report(DEVICE_RECOVERY_MEDIA, result,
                               "上行音频源读取失败");
        return result;
    }
    if (result == 0) return 0;
    if (!packet_out->data || packet_out->length == 0 ||
        packet_out->length > UINT32_MAX || packet_out->duration_ms <= 0.0) {
        memset(packet_out, 0, sizeof(*packet_out));
        device_recovery_report(DEVICE_RECOVERY_MEDIA, -1,
                               "上行音频源返回无效帧");
        return -1;
    }
    return 1;
}

int device_media_source_next_video(DeviceMediaSource *source,
                                   int force_key_frame,
                                   DeviceMediaPacket *packet_out) {
    if (!source || !source->opened || !packet_out ||
        !source->ops.next_video)
        return 0;
    memset(packet_out, 0, sizeof(*packet_out));
    int result = source->ops.next_video(source->ops.context, source->handle,
                                        force_key_frame, packet_out);
    if (result < 0) {
        device_recovery_report(DEVICE_RECOVERY_MEDIA, result,
                               "上行视频源读取失败");
        return result;
    }
    if (result == 0) return 0;
    if (!packet_out->data || packet_out->length == 0 ||
        packet_out->length > UINT32_MAX) {
        memset(packet_out, 0, sizeof(*packet_out));
        device_recovery_report(DEVICE_RECOVERY_MEDIA, -1,
                               "上行视频源返回无效帧");
        return -1;
    }
    packet_out->key_frame = packet_out->key_frame ? 1 : 0;
    return 1;
}

void device_media_source_close(DeviceMediaSource *source) {
    if (!source || !source->opened) return;
    source->ops.close(source->ops.context, source->handle);
    memset(source, 0, sizeof(*source));
}

uint64_t device_adapter_session_generation(DeviceBusiness business) {
    if (business <= DEVICE_BUSINESS_NONE || business > DEVICE_BUSINESS_CALL)
        return 0;
    pthread_rwlock_rdlock(&s_adapter_lock);
    uint64_t generation = s_generations[business];
    pthread_rwlock_unlock(&s_adapter_lock);
    return generation;
}

int device_media_sink_submit(DeviceBusiness business, int video,
                             uint8_t stream_id, uint8_t media, uint8_t flags,
                             uint32_t timestamp_ms,
                             const void *data, size_t length) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (!adapter.media_sink.submit) return DEVICE_ADAPTER_NOT_HANDLED;
    if (!data || length == 0) return -1;
    DeviceDownlinkFrame frame = {
        .business = business,
        .generation = device_adapter_session_generation(business),
        .video = video ? 1 : 0,
        .stream_id = stream_id,
        .media = media,
        .flags = flags,
        .timestamp_ms = timestamp_ms,
        .data = data,
        .length = length,
    };
    return adapter.media_sink.submit(adapter.media_sink.context, &frame);
}

int device_media_sink_is_enabled(DeviceBusiness business, int video) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (!adapter.media_sink.submit) return 0;
    if (!adapter.media_sink.is_enabled) return 1;
    return adapter.media_sink.is_enabled(adapter.media_sink.context, business,
                                         video ? 1 : 0) ? 1 : 0;
}

void device_media_sink_flush(DeviceBusiness business, uint64_t generation) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (adapter.media_sink.flush)
        adapter.media_sink.flush(adapter.media_sink.context, business,
                                 generation);
}

int device_product_poll_action(DeviceProductAction *action_out,
                               int timeout_ms) {
    if (!action_out) return -1;
    memset(action_out, 0, sizeof(*action_out));
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (!adapter.product.poll_action) {
        if (timeout_ms > 0) device_platform_sleep_ms((uint32_t)timeout_ms);
        return 0;
    }
    return adapter.product.poll_action(adapter.product.context, action_out,
                                       timeout_ms);
}

void device_product_notify(const DeviceProductEvent *event) {
    if (!event) return;
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (adapter.product.notify)
        adapter.product.notify(adapter.product.context, event);
}

int device_resource_acquire(DeviceBusiness business) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (!adapter.resource.acquire) return 0;
    return adapter.resource.acquire(adapter.resource.context, business);
}

void device_resource_release(DeviceBusiness business) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (adapter.resource.release)
        adapter.resource.release(adapter.resource.context, business);
}

void device_adapter_session_starting(DeviceBusiness business) {
    if (business <= DEVICE_BUSINESS_NONE || business > DEVICE_BUSINESS_CALL)
        return;
    pthread_rwlock_wrlock(&s_adapter_lock);
    uint64_t generation = ++s_generations[business];
    pthread_rwlock_unlock(&s_adapter_lock);
    DeviceProductEvent event = {
        .type = DEVICE_SESSION_STARTING,
        .business = business,
        .generation = generation,
    };
    device_product_notify(&event);
}

void device_adapter_session_started(DeviceBusiness business) {
    DeviceProductEvent event = {
        .type = DEVICE_SESSION_STARTED,
        .business = business,
        .generation = device_adapter_session_generation(business),
    };
    device_product_notify(&event);
}

void device_adapter_session_failed(DeviceBusiness business, int code,
                                   const char *detail) {
    uint64_t generation = device_adapter_session_generation(business);
    device_media_sink_flush(business, generation);
    DeviceProductEvent event = {
        .type = DEVICE_SESSION_FAILED,
        .business = business,
        .generation = generation,
        .code = code,
    };
    if (detail)
        snprintf(event.detail, sizeof(event.detail), "%s", detail);
    device_product_notify(&event);
}

void device_adapter_session_stopped(DeviceBusiness business) {
    uint64_t generation = device_adapter_session_generation(business);
    device_media_sink_flush(business, generation);
    DeviceProductEvent event = {
        .type = DEVICE_SESSION_STOPPED,
        .business = business,
        .generation = generation,
    };
    device_product_notify(&event);
}

void device_recovery_report(DeviceRecoveryDomain domain, int code,
                            const char *detail) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (adapter.recovery.report)
        adapter.recovery.report(adapter.recovery.context, domain, code,
                                detail ? detail : "");
}

int device_security_random_bytes(unsigned char *output, size_t length) {
    if (!output && length) return -1;
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (!adapter.security.random_bytes) return -1;
    return adapter.security.random_bytes(adapter.security.context, output,
                                         length);
}

int device_security_allow_insecure_transport(void) {
    DeviceAdapterV1 adapter = _adapter_snapshot();
    if (!adapter.security.allow_insecure_transport) return 0;
    return adapter.security.allow_insecure_transport(
        adapter.security.context) ? 1 : 0;
}

#ifdef DEVICE_SIM_TESTING
void device_adapter_reset_for_testing(void) {
    pthread_rwlock_wrlock(&s_adapter_lock);
    memset(&s_adapter, 0, sizeof(s_adapter));
    memset(s_generations, 0, sizeof(s_generations));
    s_installed = 0;
    pthread_rwlock_unlock(&s_adapter_lock);
}
#endif
