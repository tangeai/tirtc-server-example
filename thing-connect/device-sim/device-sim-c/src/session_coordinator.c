#include "session_coordinator.h"

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

int session_coordinator_init(SessionCoordinator *sc,
                             const SessionAdapter *stream,
                             const SessionAdapter *voip,
                             const SessionAdapter *ai,
                             const SessionAdapter *call) {
    *sc = (SessionCoordinator){0};
    pthread_mutex_init(&sc->lock, NULL);
    sc->adapters[SESSION_STREAM] = *stream;
    sc->adapters[SESSION_VOIP] = *voip;
    sc->adapters[SESSION_AI] = *ai;
    sc->adapters[SESSION_CALL] = *call;
    return 0;
}

void session_coordinator_destroy(SessionCoordinator *sc) {
    session_coordinator_shutdown(sc);
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

void session_coordinator_shutdown(SessionCoordinator *sc) {
    pthread_mutex_lock(&sc->lock);
    if (!sc->closed) {
        sc->closed = 1;
        _stop_locked(sc);
    }
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
