#include "session_coordinator.h"

#include <stdlib.h>

#define LOG_MODULE "session"
#include "common.h"

static int _start_locked(SessionCoordinator *sc, SessionKind kind) {
    SessionAdapter *adapter = &sc->adapters[kind];
    if (!adapter->start) {
        LOG_E("%s 模块不可用", session_kind_name(kind));
        return -1;
    }
    if (adapter->start(adapter->ctx) != 0) return -1;
    sc->current = kind;
    return 0;
}

static void _stop_locked(SessionCoordinator *sc) {
    SessionKind current = sc->current;
    sc->current = SESSION_NONE;
    if (current != SESSION_NONE && sc->adapters[current].stop)
        sc->adapters[current].stop(sc->adapters[current].ctx);
}

void session_coordinator_init(SessionCoordinator *sc,
                              const SessionAdapter *stream,
                              const SessionAdapter *voip,
                              const SessionAdapter *ai,
                              const SessionAdapter *call) {
    *sc = (SessionCoordinator){0};
    /*
     * An adapter stop can synchronously report its terminal event.  That
     * callback enters finish_async() again on the same thread, so the
     * coordinator lock must permit this bounded re-entry.
     */
    pthread_mutexattr_t attr;
    pthread_mutexattr_init(&attr);
    pthread_mutexattr_settype(&attr, PTHREAD_MUTEX_RECURSIVE);
    pthread_mutex_init(&sc->lock, &attr);
    pthread_mutexattr_destroy(&attr);
    pthread_cond_init(&sc->finish_idle, NULL);
    sc->adapters[SESSION_STREAM] = *stream;
    sc->adapters[SESSION_VOIP] = *voip;
    sc->adapters[SESSION_AI] = *ai;
    sc->adapters[SESSION_CALL] = *call;
}

void session_coordinator_destroy(SessionCoordinator *sc) {
    session_coordinator_shutdown(sc);
    pthread_cond_destroy(&sc->finish_idle);
    pthread_mutex_destroy(&sc->lock);
}

int session_coordinator_start_stream(SessionCoordinator *sc) {
    pthread_mutex_lock(&sc->lock);
    if (sc->closed || sc->current == SESSION_STREAM) {
        pthread_mutex_unlock(&sc->lock);
        return sc->closed ? -1 : 0;
    }
    _stop_locked(sc);
    int rc = _start_locked(sc, SESSION_STREAM);
    pthread_mutex_unlock(&sc->lock);
    return rc;
}

int session_coordinator_begin(SessionCoordinator *sc, SessionKind kind) {
    if (kind == SESSION_NONE || kind == SESSION_STREAM) return -1;
    pthread_mutex_lock(&sc->lock);
    if (sc->closed) {
        pthread_mutex_unlock(&sc->lock);
        return -1;
    }
    if (sc->current != SESSION_NONE && sc->current != SESSION_STREAM && sc->current != kind) {
        LOG_W("%s 会话正在进行中", session_kind_name(sc->current));
        pthread_mutex_unlock(&sc->lock);
        return -1;
    }
    if (sc->current == kind) {
        LOG_W("%s 会话已在进行中", session_kind_name(kind));
        pthread_mutex_unlock(&sc->lock);
        return -1;
    }
    _stop_locked(sc);
    int rc = _start_locked(sc, kind);
    if (rc != 0 && !sc->closed) _start_locked(sc, SESSION_STREAM);
    pthread_mutex_unlock(&sc->lock);
    return rc;
}

int session_coordinator_finish_checked(SessionCoordinator *sc,
                                       SessionKind kind) {
    int rc = 0;
    pthread_mutex_lock(&sc->lock);
    if (!sc->closed && sc->current == kind) {
        _stop_locked(sc);
        rc = _start_locked(sc, SESSION_STREAM);
    }
    pthread_mutex_unlock(&sc->lock);
    return rc;
}

void session_coordinator_finish(SessionCoordinator *sc, SessionKind kind) {
    (void)session_coordinator_finish_checked(sc, kind);
}

typedef struct { SessionCoordinator *sc; SessionKind kind; } FinishRequest;
static void *_finish_thread(void *arg) {
    FinishRequest *req = arg;
    session_coordinator_finish(req->sc, req->kind);
    pthread_mutex_lock(&req->sc->lock);
    if (req->sc->finish_threads > 0) req->sc->finish_threads--;
    if (req->sc->finish_threads == 0)
        pthread_cond_broadcast(&req->sc->finish_idle);
    pthread_mutex_unlock(&req->sc->lock);
    free(req);
    return NULL;
}

void session_coordinator_finish_async(SessionCoordinator *sc, SessionKind kind) {
    pthread_mutex_lock(&sc->lock);
    if (sc->closed || sc->current != kind) {
        pthread_mutex_unlock(&sc->lock);
        return;
    }
    FinishRequest *req = malloc(sizeof(*req));
    if (!req) {
        pthread_mutex_unlock(&sc->lock);
        LOG_E("无法分配会话结束任务");
        return;
    }
    *req = (FinishRequest){sc, kind};
    pthread_t thread;
    sc->finish_threads++;
    if (pthread_create(&thread, NULL, _finish_thread, req) != 0) {
        free(req);
        sc->finish_threads--;
        if (sc->finish_threads == 0)
            pthread_cond_broadcast(&sc->finish_idle);
        pthread_mutex_unlock(&sc->lock);
        LOG_E("无法创建会话结束线程");
        return;
    }
    pthread_detach(thread);
    pthread_mutex_unlock(&sc->lock);
}

void session_coordinator_shutdown(SessionCoordinator *sc) {
    pthread_mutex_lock(&sc->lock);
    if (!sc->closed) {
        sc->closed = 1;
        _stop_locked(sc);
    }
    while (sc->finish_threads != 0)
        pthread_cond_wait(&sc->finish_idle, &sc->lock);
    pthread_mutex_unlock(&sc->lock);
}

SessionKind session_coordinator_current(SessionCoordinator *sc) {
    pthread_mutex_lock(&sc->lock);
    SessionKind current = sc->current;
    pthread_mutex_unlock(&sc->lock);
    return current;
}

const char *session_kind_name(SessionKind kind) {
    switch (kind) {
    case SESSION_STREAM: return "实时推流";
    case SESSION_VOIP: return "VoIP";
    case SESSION_AI: return "AI";
    case SESSION_CALL: return "设备互呼";
    default: return "无";
    }
}
