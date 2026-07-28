/** \file tirtc_call.h
 * \brief TiRTC device-to-device P2P call module — passive listen + TiRtcConnect.
 *
 * Unlike VoIP (TiRtcWhipConnect / WHIP client), this module uses TiRtcConnect
 * for true device↔device P2P. Both sides TiRtcStart as passive listeners.
 *
 * Roles:
 *   - CALLER (initiator): Waits passively. Callee TiRtcConnect's → on_conn_accepted.
 *   - CALLEE (receiver):  Gets token from call-server, TiRtcConnect(retry) → sends 0x2000.
 */

#ifndef TIRTC_CALL_H
#define TIRTC_CALL_H

#include <pthread.h>
#include <stddef.h>
#include <stdint.h>
#include <cjson/cJSON.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct CallState {
    char call_server[256];
    char device_id[64];
    char mqtt_token[512];
    char send_audio[512];
    char send_video[512];
    /* Legacy public field; C simulator now logs and discards downlink media. */
    char recv_dir[512];

    pthread_mutex_t lock;
    pthread_mutex_t reject_lock;
    pthread_cond_t reject_ready;
    pthread_cond_t reject_idle;
    pthread_t reject_thread;
    struct {
        char room_id[128];
        char reason[32];
    } reject_queue[16];
    unsigned int reject_head;
    unsigned int reject_count;
    int reject_active;
    int reject_thread_started;
    int reject_stop;

    /* Pending incoming call */
    int  pending_call;
    char pending_room_id[128];
    char pending_caller_id[64];
    char pending_caller_name[64];
    char pending_call_type[16];  /* "video" | "audio" */
    uint64_t incoming_generation;
    uint64_t pending_generation;
    int64_t pending_deadline_ms;

    /* Current room / role */
    int  active;
    char room_id[128];
    char role[16];     /* "caller" | "callee" */
    char active_call_type[16]; /* "video" | "audio" */
    uint64_t active_generation;

    /* Ring timer (caller-side, 30s timeout) */
    pthread_t ring_timer_thread;
    int       ring_timer_thread_created;
    int       ring_timer_running;

    /* Contact list cache for index-based commands (call N, remark N, etc.) */
    int     contact_count;
    cJSON  *contact_list;     /* cJSON array of contact objects */
    char  **contact_device_ids; /* parallel array of device_id strings */

    int  (*before_start)(void *user);
    int  (*before_start_ex)(void *user, int consume_pending);
    int  (*run_action)(void *user, const char *session_id,
                       int (*action)(void *action_user),
                       void *action_user);
    void (*on_session_end)(void *user);
    void *runtime_user;
} CallState;

/* ── Lifecycle ─────────────────────────────────────────────────── */
CallState *call_create(const char *call_server, const char *device_id,
                       const char *mqtt_token,
                       const char *send_audio, const char *send_video,
                       const char *recv_dir);
CallState *call_create_ex(const char *call_server, const char *device_id,
                          const char *mqtt_token,
                          const char *send_audio, const char *audio_format,
                          const char *send_video, const char *video_format);
void call_destroy(CallState *cs);

/* ── MQTT message handlers (called from device_flow.c) ──────────── */
void call_on_device_call_incoming(void *ctx, const cJSON *payload);
void call_on_device_room_cancel(void *ctx, const cJSON *payload);
void call_on_device_call_reject(void *ctx, const cJSON *payload);
void call_on_device_callers_update(void *ctx);
void call_on_device_callers_update_ex(void *ctx, const cJSON *payload);
void call_on_device_callee_answered(void *ctx, const cJSON *payload);
int call_expire_pending(CallState *cs, char *room_id_out,
                        size_t room_id_size);

/* ── Command input thread (reads stdin) ────────────────────────── */
void call_cmd_loop(CallState *cs);

/* ── SDK lifecycle (global singleton, one per process) ─────────── */
int  call_init_sdk(const char *device_id, const char *secret_key, const char *client_id, const char *endpoint);
void call_uninit_sdk(void);

/* ── P2P call operations ───────────────────────────────────────── */
void call_set_expected_room(CallState *cs, const char *room_id);
void call_clear_expected_room(CallState *cs);
int  call_connect_to(const char *remote_device_id, const char *token,
                     const char *room_id, int max_retries, int timeout_s);
void call_hangup(void);
int  call_is_active(void);
const char *call_get_state_str(void);
void call_configure_media(const char *device_id, const char *send_audio,
                          const char *send_video, const char *recv_dir);
int  call_configure_media_ex(const char *send_audio, const char *audio_format,
                             const char *send_video,
                             const char *video_format);

/* ── Callback types for session layer ──────────────────────────── */
typedef void (*call_p2p_connected_cb)(const char *room_id, void *user);
typedef void (*call_connect_failed_cb)(void *user);
void call_register_p2p_connected_cb(call_p2p_connected_cb cb, void *user);
void call_register_connect_failed_cb(call_connect_failed_cb cb, void *user);
void call_set_runtime_callbacks(CallState *cs,
                                int (*before_start)(void *user),
                                void (*on_session_end)(void *user),
                                void *user);
void call_set_runtime_callbacks_ex(
    CallState *cs,
    int (*before_start)(void *user, int consume_pending),
    void (*on_session_end)(void *user),
    void *user);
void call_set_runtime_action_callback(
    CallState *cs,
    int (*run_action)(void *user, const char *session_id,
                      int (*action)(void *action_user),
                      void *action_user));

#ifdef __cplusplus
}
#endif

#endif /* TIRTC_CALL_H */
