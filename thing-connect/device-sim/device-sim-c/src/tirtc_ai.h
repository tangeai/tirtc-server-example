/** \file tirtc_ai.h
 * \brief TiRTC AI conversation module — WHIP client, JSON-RPC, and file audio.
 */

#ifndef TIRTC_AI_H
#define TIRTC_AI_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* ── AI session state (opaque) ────────────────────────────────────────── */

typedef struct AiState AiState;

AiState *ai_create(const char *ai_server, const char *device_id,
                   const char *mqtt_token, const char *ai_audio,
                   const char *down_audio_format);
AiState *ai_create_ex(const char *ai_server, const char *device_id,
                      const char *mqtt_token, const char *ai_audio,
                      const char *up_audio_format,
                      const char *down_audio_format);
void ai_configure_receive_dir(AiState *as, const char *receive_dir);
void ai_destroy(AiState *as);

typedef void (*ai_session_end_cb)(void *user);
void ai_set_session_end_callback(AiState *as, ai_session_end_cb cb, void *user);

/* Command input thread (aicall / hangup) */
void ai_cmd_loop(AiState *as);

/* ── Process-runtime integration ──────────────────────────────────────── */

int  ai_service_register(void);
int  ai_service_start(AiState *as);
void ai_service_stop(AiState *as);

/* ── AI operations ────────────────────────────────────────────────────── */

int  ai_get_token(const char *ai_server, const char *mqtt_token,
                  const char *device_id,
                  char *peer_id_out, size_t pid_size,
                  char *token_out, size_t tok_size,
                  char *role_id_out, size_t rid_size);
int  ai_start_session(AiState *as, const char *peer_id, const char *token,
                      const char *audio_path, const char *device_id,
                      const char *role_id);
void ai_stop_session(AiState *as);
int  ai_is_active(const AiState *as);
/** Drive deferred SDK work from the unified terminal/runtime loop. */
void ai_poll(AiState *as);

#ifdef DEVICE_SIM_TESTING
void ai_test_force_connect_timeout(AiState *as);
char *ai_test_build_start_session_json(AiState *as, const char *request_id);
#endif

#ifdef __cplusplus
}
#endif

#endif /* TIRTC_AI_H */
