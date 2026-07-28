#ifndef SESSION_ARBITER_H
#define SESSION_ARBITER_H

#include <pthread.h>
#include <stdint.h>

#include "session_coordinator.h"

#define SESSION_ARBITER_ID_CAP 128
#define SESSION_ARBITER_QUEUE_CAP 16

typedef struct {
    SessionKind kind;
    uint64_t generation;
    char session_id[SESSION_ARBITER_ID_CAP];
} SessionLease;

typedef struct {
    SessionKind kind;
    uint64_t generation;
} SessionFinishRequest;

typedef int (*session_arbiter_action_fn)(void *user);
typedef void (*session_arbiter_lease_ready_fn)(void *user,
                                               const SessionLease *lease);

/*
 * Central policy gate for every producer of session events (terminal, MQTT,
 * SDK callbacks).  It owns conflict decisions; SessionCoordinator owns only
 * the TiRTC lifecycle transition selected by this gate.
 */
typedef struct {
    pthread_mutex_t state_lock;
    pthread_mutex_t transition_lock;
    pthread_cond_t finish_idle;
    pthread_cond_t finish_ready;
    SessionCoordinator *coordinator;
    SessionKind owner;
    char owner_session_id[SESSION_ARBITER_ID_CAP];
    int owner_cancelled;
    uint32_t pending_mask;
    char pending_session_id[SESSION_ARBITER_ID_CAP];
    int64_t pending_deadline_ms;
    uint64_t generation;
    uint64_t pending_generation;
    pthread_t worker_thread;
    SessionFinishRequest finish_queue[SESSION_ARBITER_QUEUE_CAP];
    unsigned int finish_head;
    unsigned int finish_count;
    unsigned int finish_active;
    int worker_started;
    int worker_stop;
    int closed;
} SessionArbiter;

void session_arbiter_init(SessionArbiter *arbiter,
                          SessionCoordinator *coordinator);
void session_arbiter_destroy(SessionArbiter *arbiter);

/* Register an incoming call without stopping the idle H5 live stream. */
int session_arbiter_offer_pending(SessionArbiter *arbiter, SessionKind kind);
int session_arbiter_offer_pending_id(SessionArbiter *arbiter, SessionKind kind,
                                     const char *session_id,
                                     int64_t ttl_ms);
/* Returns 1 for the current owner's event, 0 for a newly reserved pending
 * slot, and -1 when another event owns either slot. */
int session_arbiter_admit_incoming(SessionArbiter *arbiter, SessionKind kind);
int session_arbiter_admit_incoming_id(SessionArbiter *arbiter,
                                      SessionKind kind,
                                      const char *session_id,
                                      int64_t ttl_ms);
void session_arbiter_clear_pending(SessionArbiter *arbiter, SessionKind kind);
void session_arbiter_clear_pending_id(SessionArbiter *arbiter,
                                      SessionKind kind,
                                      const char *session_id);
/* Returns 1 when a pending ticket was cleared, 2 when the matching STARTING/
 * ACTIVE owner was cancelled, and 0 for a stale/unrelated cancellation. */
int session_arbiter_cancel_id(SessionArbiter *arbiter, SessionKind kind,
                              const char *session_id);
int session_arbiter_has_pending(SessionArbiter *arbiter, SessionKind kind);
int session_arbiter_has_pending_id(SessionArbiter *arbiter, SessionKind kind,
                                   const char *session_id);

/*
 * Claim the exclusive RTC resource and switch lifecycle modules.  Set
 * consume_pending only for accepting/recovering the already registered
 * incoming call of the same kind.
 */
int session_arbiter_begin(SessionArbiter *arbiter, SessionKind kind,
                          int consume_pending);
int session_arbiter_begin_id(SessionArbiter *arbiter, SessionKind kind,
                             int consume_pending, const char *session_id,
                             SessionLease *lease_out);
/*
 * Extended begin used by lifecycle integrations that can receive a terminal
 * callback synchronously from the coordinator's start adapter.  lease_ready
 * runs after ownership is reserved but before that adapter is entered.
 */
int session_arbiter_begin_id_ex(SessionArbiter *arbiter, SessionKind kind,
                                int consume_pending, const char *session_id,
                                SessionLease *lease_out,
                                session_arbiter_lease_ready_fn lease_ready,
                                void *lease_ready_user);
int session_arbiter_run_action(SessionArbiter *arbiter,
                               const SessionLease *lease,
                               session_arbiter_action_fn action,
                               void *action_user);
void session_arbiter_finish(SessionArbiter *arbiter, SessionKind kind);
void session_arbiter_finish_lease(SessionArbiter *arbiter,
                                  const SessionLease *lease);
void session_arbiter_finish_async(SessionArbiter *arbiter, SessionKind kind);
void session_arbiter_finish_lease_async(SessionArbiter *arbiter,
                                        const SessionLease *lease);
void session_arbiter_finish_lease_async_restore_pending(
    SessionArbiter *arbiter, const SessionLease *lease);
void session_arbiter_finish_async_restore_pending(SessionArbiter *arbiter,
                                                   SessionKind kind);
SessionKind session_arbiter_current(SessionArbiter *arbiter);
int session_arbiter_ready(SessionArbiter *arbiter);

void session_arbiter_shutdown(SessionArbiter *arbiter);

#endif
