/** \file call_session.h
 * \brief Call-server session layer — HTTP endpoints + ring timer.
 */

#ifndef CALL_SESSION_H
#define CALL_SESSION_H

#include "tirtc_call.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ── HTTP session operations ──────────────────────────────────── */

int  call_session_do_call(CallState *cs, const char *target_id, const char *call_type);
int  call_session_do_accept(CallState *cs);
int  call_session_do_reject(CallState *cs, const char *reason);
int  call_session_reject_incoming_payload(CallState *cs,
                                          const cJSON *payload,
                                          const char *reason);
int  call_session_reject_incoming_payload_async(CallState *cs,
                                                const cJSON *payload,
                                                const char *reason);
void call_session_shutdown_workers(CallState *cs);
int  call_session_do_hangup(CallState *cs);
int  call_session_do_cancel(CallState *cs);
int  call_session_do_list_contacts(CallState *cs);
int  call_session_do_list_pending_contacts(CallState *cs);
int  call_session_do_add_contact(CallState *cs, const char *target_id);
int  call_session_do_respond_contact(CallState *cs, const char *peer_id, int accept);
int  call_session_do_delete_contact(CallState *cs, const char *peer_id);
int  call_session_do_remark(CallState *cs, const char *peer_id, const char *remark);
int  call_session_do_query_room(CallState *cs);
int  call_session_has_pending(CallState *cs);
int  call_session_has_pending_or_outgoing(CallState *cs);
/** Return an owned duplicate of one cached contact; caller deletes it. */
cJSON *call_session_copy_contact(CallState *cs, int index);
/** Match cached device_id/wx_open_id and return an owned duplicate. */
cJSON *call_session_find_contact(CallState *cs, const char *target);

/* ── Command dispatch (called from tirtc_call.c cmd loop) ─────── */

void call_session_dispatch(CallState *cs, const char *line);

/* ── Ring timer ──────────────────────────────────────────────── */

void call_session_start_ring_timer(CallState *cs);
void call_session_cancel_ring_timer(CallState *cs);

/* ── P2P event callbacks (set from tirtc_call.c) ──────────────── */

void call_session_on_p2p_connected(const char *room_id, void *user);
void call_session_on_connect_failed(void *user);

#ifdef __cplusplus
}
#endif

#endif /* CALL_SESSION_H */
