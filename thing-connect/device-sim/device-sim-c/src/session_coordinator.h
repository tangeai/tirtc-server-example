/** \file session_coordinator.h
 * \brief Serializes the one TiRTC SDK instance shared by all device flows.
 *
 * The device remains a passive live-stream server when idle.  A VoIP, AI or
 * device-call session temporarily owns the SDK; when it ends, live streaming
 * is started again.  This mirrors device-sim-py's SessionCoordinator.
 */
#ifndef SESSION_COORDINATOR_H
#define SESSION_COORDINATOR_H

#include <pthread.h>

typedef enum {
    SESSION_NONE = 0,
    SESSION_STREAM,
    SESSION_VOIP,
    SESSION_AI,
    SESSION_CALL,
} SessionKind;

typedef int  (*session_start_fn)(void *ctx);
typedef void (*session_stop_fn)(void *ctx);

typedef struct {
    session_start_fn start;
    session_stop_fn  stop;
    void             *ctx;
} SessionAdapter;

typedef struct {
    pthread_mutex_t lock;
    SessionAdapter  adapters[SESSION_CALL + 1];
    SessionKind     current;
    int             closed;
    pthread_cond_t  finish_idle;
    unsigned int    finish_threads;
} SessionCoordinator;

void session_coordinator_init(SessionCoordinator *sc,
                              const SessionAdapter *stream,
                              const SessionAdapter *voip,
                              const SessionAdapter *ai,
                              const SessionAdapter *call);
void session_coordinator_destroy(SessionCoordinator *sc);
int  session_coordinator_start_stream(SessionCoordinator *sc);
int  session_coordinator_begin(SessionCoordinator *sc, SessionKind kind);
void session_coordinator_finish(SessionCoordinator *sc, SessionKind kind);
int  session_coordinator_finish_checked(SessionCoordinator *sc,
                                        SessionKind kind);
void session_coordinator_finish_async(SessionCoordinator *sc, SessionKind kind);
void session_coordinator_shutdown(SessionCoordinator *sc);
SessionKind session_coordinator_current(SessionCoordinator *sc);
const char *session_kind_name(SessionKind kind);

#endif
