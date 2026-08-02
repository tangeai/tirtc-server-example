/** \file device_adapter.h
 * \brief Versioned product-adapter contract for the Linux C reference core.
 *
 * Install one adapter before starting worker threads. The core copies the V1
 * table, so the table itself may be released after device_adapter_install().
 * Every non-NULL context and the object it references must remain valid until
 * process shutdown. Installation is one-shot; runtime replacement is rejected.
 * Callback-specific ownership and thread rules are documented on each group.
 */
#ifndef DEVICE_ADAPTER_H
#define DEVICE_ADAPTER_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define DEVICE_ADAPTER_ABI_V1 1u
#define DEVICE_ADAPTER_NOT_HANDLED 1

/* V1 field order and semantics are frozen. Incompatible changes require a new
 * ABI version instead of inserting/reordering fields in DeviceAdapterV1. */

typedef enum {
    DEVICE_BUSINESS_NONE = 0,
    DEVICE_BUSINESS_STREAM = 1,
    DEVICE_BUSINESS_VOIP = 2,
    DEVICE_BUSINESS_AI = 3,
    DEVICE_BUSINESS_CALL = 4,
} DeviceBusiness;

typedef enum {
    DEVICE_SESSION_STARTING = 1,
    DEVICE_SESSION_STARTED,
    DEVICE_SESSION_STOPPING,
    DEVICE_SESSION_STOPPED,
    DEVICE_SESSION_FAILED,
    DEVICE_SESSION_INCOMING,
} DeviceSessionEventType;

typedef enum {
    DEVICE_RECOVERY_PLATFORM = 1,
    DEVICE_RECOVERY_IDENTITY,
    DEVICE_RECOVERY_NETWORK,
    DEVICE_RECOVERY_MQTT,
    DEVICE_RECOVERY_TIRTC,
    DEVICE_RECOVERY_MEDIA,
    DEVICE_RECOVERY_RESOURCE,
    DEVICE_RECOVERY_SECURITY,
} DeviceRecoveryDomain;

typedef enum {
    DEVICE_ACTION_NONE = 0,
    DEVICE_ACTION_RAW_COMMAND,
    DEVICE_ACTION_ACCEPT,
    DEVICE_ACTION_REJECT,
    DEVICE_ACTION_START_AI,
    DEVICE_ACTION_HANGUP,
    DEVICE_ACTION_CANCEL,
    DEVICE_ACTION_DIAL_DEVICE,
    DEVICE_ACTION_DIAL_CONTACT_INDEX,
    DEVICE_ACTION_DIAL_WX_INDEX,
    DEVICE_ACTION_EXIT,
} DeviceActionType;

typedef struct {
    DeviceActionType type;
    int index;
    char target[256];
    char call_type[16];
    char reason[64];
    char raw_command[1024];
} DeviceProductAction;

typedef struct {
    DeviceSessionEventType type;
    DeviceBusiness business;
    uint64_t generation;
    int code;
    char session_id[128];
    char detail[160];
} DeviceProductEvent;

typedef struct {
    const char *audio_locator;
    const char *audio_format;
    const char *video_locator;
    const char *video_format;
    int audio_packet_ms;
    DeviceBusiness business;
} DeviceMediaSourceConfig;

typedef struct {
    const unsigned char *data;
    size_t length;
    double duration_ms;
    int key_frame;
} DeviceMediaPacket;

typedef struct {
    DeviceBusiness business;
    uint64_t generation;
    int video;
    uint8_t stream_id;
    uint8_t media;
    uint8_t flags;
    uint32_t timestamp_ms;
    const void *data;
    size_t length;
} DeviceDownlinkFrame;

typedef struct {
    void *context;
    /* Time functions can be called concurrently from any core thread. */
    int64_t (*monotonic_ms)(void *context);
    int64_t (*wall_time_ms)(void *context);
    void (*sleep_ms)(void *context, uint32_t milliseconds);
} DevicePlatformOps;

typedef struct {
    void *context;
    int (*load)(void *context, const char *slot,
                char *device_id, size_t device_id_size,
                char *device_key, size_t device_key_size);
    int (*save)(void *context, const char *slot,
                const char *device_id, const char *device_key);
    int (*clear)(void *context, const char *slot);
} DeviceIdentityOps;

/** Media-source functions run on a business media worker, never an SDK
 * callback. Returned packet data remains owned by the adapter and must stay
 * valid until the next next_* call or close for the same handle. next_*()
 * returns 1 for a packet, 0 for normal end-of-source, or a negative error. */
typedef struct {
    void *context;
    int (*open)(void *context, const DeviceMediaSourceConfig *config,
                void **handle_out);
    int (*has_video)(void *context, void *handle);
    int (*next_audio)(void *context, void *handle,
                      DeviceMediaPacket *packet_out);
    int (*next_video)(void *context, void *handle, int force_key_frame,
                      DeviceMediaPacket *packet_out);
    void (*close)(void *context, void *handle);
} DeviceMediaSourceOps;

/** submit() runs in a TiRTC SDK callback. It must not block or retain frame
 * pointers. A product sink must copy accepted payload into its own bounded
 * queue before returning. Return 0 when accepted, NOT_HANDLED to request the
 * Linux demo fallback, or a negative code when deliberately dropped/failed.
 * is_enabled() is queried before a session allocates the demo fallback; when
 * it returns true, submit() must not later return NOT_HANDLED for that path. */
typedef struct {
    void *context;
    int (*is_enabled)(void *context, DeviceBusiness business, int video);
    int (*submit)(void *context, const DeviceDownlinkFrame *frame);
    void (*flush)(void *context, DeviceBusiness business,
                  uint64_t generation);
} DeviceMediaSinkOps;

/** poll_action() runs on the application control thread. Return 1 with an
 * action, 0 on timeout, or a negative error. notify() can run on an MQTT
 * callback or a serialized session-control path; it must copy the stack event,
 * enqueue without blocking, and not re-enter coordinator/session action APIs. */
typedef struct {
    void *context;
    int (*poll_action)(void *context, DeviceProductAction *action_out,
                       int timeout_ms);
    void (*notify)(void *context, const DeviceProductEvent *event);
} DeviceProductOps;

/** Resource hooks run on a serialized SessionCoordinator path while its
 * internal transition lock is held. They must not re-enter coordinator APIs.
 * acquire() must either acquire the complete product resource set or roll back
 * before returning an error. release() must be idempotent. */
typedef struct {
    void *context;
    int (*acquire)(void *context, DeviceBusiness business);
    void (*release)(void *context, DeviceBusiness business);
} DeviceResourceOps;

typedef struct {
    void *context;
    /** Only enqueue/record a recovery request and return promptly. This may be
     * called by MQTT callbacks as well as application worker threads. */
    void (*report)(void *context, DeviceRecoveryDomain domain, int code,
                   const char *detail);
} DeviceRecoveryOps;

typedef struct {
    void *context;
    int (*random_bytes)(void *context, unsigned char *output, size_t length);
    int (*allow_insecure_transport)(void *context);
} DeviceSecurityOps;

typedef struct {
    uint32_t abi_version;
    size_t struct_size;
    DevicePlatformOps platform;
    DeviceIdentityOps identity;
    DeviceMediaSourceOps media_source;
    DeviceMediaSinkOps media_sink;
    DeviceProductOps product;
    DeviceResourceOps resource;
    DeviceRecoveryOps recovery;
    DeviceSecurityOps security;
} DeviceAdapterV1;

int device_adapter_install(const DeviceAdapterV1 *adapter);
int device_adapter_is_installed(void);

int64_t device_platform_monotonic_ms(void);
int64_t device_platform_wall_time_ms(void);
void device_platform_sleep_ms(uint32_t milliseconds);

int device_identity_load(const char *slot,
                         char *device_id, size_t device_id_size,
                         char *device_key, size_t device_key_size);
int device_identity_save(const char *slot, const char *device_id,
                         const char *device_key);
int device_identity_clear(const char *slot);

typedef struct {
    DeviceMediaSourceOps ops;
    void *handle;
    DeviceBusiness business;
    int opened;
} DeviceMediaSource;

int device_media_source_open(DeviceMediaSource *source,
                             const DeviceMediaSourceConfig *config);
int device_media_source_has_video(DeviceMediaSource *source);
int device_media_source_next_audio(DeviceMediaSource *source,
                                   DeviceMediaPacket *packet_out);
int device_media_source_next_video(DeviceMediaSource *source,
                                   int force_key_frame,
                                   DeviceMediaPacket *packet_out);
void device_media_source_close(DeviceMediaSource *source);

int device_media_sink_submit(DeviceBusiness business, int video,
                             uint8_t stream_id, uint8_t media, uint8_t flags,
                             uint32_t timestamp_ms,
                             const void *data, size_t length);
int device_media_sink_is_enabled(DeviceBusiness business, int video);
void device_media_sink_flush(DeviceBusiness business, uint64_t generation);

int device_product_poll_action(DeviceProductAction *action_out,
                               int timeout_ms);
void device_product_notify(const DeviceProductEvent *event);

int device_resource_acquire(DeviceBusiness business);
void device_resource_release(DeviceBusiness business);
void device_adapter_session_starting(DeviceBusiness business);
void device_adapter_session_started(DeviceBusiness business);
void device_adapter_session_failed(DeviceBusiness business, int code,
                                   const char *detail);
void device_adapter_session_stopped(DeviceBusiness business);
uint64_t device_adapter_session_generation(DeviceBusiness business);

void device_recovery_report(DeviceRecoveryDomain domain, int code,
                            const char *detail);
int device_security_random_bytes(unsigned char *output, size_t length);
int device_security_allow_insecure_transport(void);

#ifdef DEVICE_SIM_TESTING
void device_adapter_reset_for_testing(void);
#endif

#ifdef __cplusplus
}
#endif

#endif /* DEVICE_ADAPTER_H */
