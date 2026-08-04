/** \file tirtc_voip.h
 * \brief TiRTC VoIP module — WHIP client, file media, and state machine.
 */

#ifndef TIRTC_VOIP_H
#define TIRTC_VOIP_H

#include <stddef.h>
#include <cjson/cJSON.h>

#ifdef __cplusplus
extern "C" {
#endif

/* ── MQTT handler (for device_flow integration) ───────────────────────── */

typedef struct VoipState VoipState;

VoipState *voip_create(const char *voip_server, const char *device_id,
                       const char *mqtt_token, const char *voip_audio);
void voip_destroy(VoipState *vs);
void voip_set_auth_list(VoipState *vs, cJSON *auth_list);
typedef void (*voip_session_end_cb)(void *user);
void voip_set_session_end_callback(VoipState *vs, voip_session_end_cb cb, void *user);
void voip_set_recovered_start_callback(VoipState *vs,
                                       int (*callback)(void *user),
                                       void *user);
void voip_configure_video(VoipState *vs, const char *video_path);
int  voip_configure_media(VoipState *vs, const char *audio_path,
                          const char *audio_format, const char *video_path,
                          const char *video_format);
int  voip_configure_down_audio_format(const char *format);
int  voip_configure_profile(const char *up_audio, const char *down_audio,
                            const char *up_video, const char *down_video,
                            int has_video);

/* MQTT message handlers (called from device_flow) */
void voip_on_call_incoming(void *ctx, const cJSON *payload);
void voip_on_callers_update(void *ctx);
void voip_on_call_cancel(void *ctx, const cJSON *payload);
int  voip_reject_incoming_payload(const cJSON *payload, int reason);
int  voip_reject_incoming_payload_async(VoipState *vs,
                                        const cJSON *payload, int reason);

/* Command input thread (reads stdin, dispatches yes/no/wxcall/hangup/cancel) */
void voip_cmd_loop(VoipState *vs);

/* ── Process-runtime integration ───────────────────────────────────────── */

int  voip_service_register(void);
int  voip_service_start(VoipState *vs);
void voip_service_stop(VoipState *vs);

/* ── VoIP operations ───────────────────────────────────────────────────── */

int  voip_report_profile(const char *voip_server, const char *mqtt_token,
                         cJSON **auth_list_out);
int  voip_start_session(VoipState *vs, const char *peer_id, const char *token,
                        const char *audio_file);
void voip_stop_session(VoipState *vs);
int  voip_is_active(const VoipState *vs);
int  voip_matches_active_room(VoipState *vs, const char *room_id);
int  voip_reject_session(const char *wx_app_id, const char *wx_model_id,
                         const char *wx_session_token, const char *wx_room_id,
                         const char *wx_payload, int hangup_reason);
int  voip_do_outgoing_call(VoipState *vs, const cJSON *caller);
int  voip_do_outgoing_call_ex(VoipState *vs, const cJSON *caller,
                              const char *call_type);
int  voip_accept_pending(VoipState *vs);
int  voip_reject_pending(VoipState *vs);
int  voip_dial_authorized(VoipState *vs, int index);
int  voip_dial_authorized_ex(VoipState *vs, int index,
                             const char *call_type);
int  voip_list_authorized(VoipState *vs);
cJSON *voip_find_authorized(VoipState *vs, const char *wx_open_id);
int  voip_has_pending(VoipState *vs);
int  voip_has_pending_or_outgoing(VoipState *vs);
int  voip_copy_pending_room(VoipState *vs, char *room_id_out,
                            size_t room_id_size);
int  voip_expire_pending(VoipState *vs, char *room_id_out,
                         size_t room_id_size);
/** Expire a 30s outbound ring wait and notify the runtime to restore STREAM. */
int  voip_expire_outgoing(VoipState *vs);
/** Expire a missing WHIP callback or missing 0x2000 connect acknowledgement. */
int  voip_expire_connection(VoipState *vs);
void voip_clear_pending_local(VoipState *vs);

#ifdef DEVICE_SIM_TESTING
void voip_test_force_outgoing_timeout(VoipState *vs);
void voip_test_force_connect_timeout(VoipState *vs);
#endif

#ifdef __cplusplus
}
#endif

#endif /* TIRTC_VOIP_H */
