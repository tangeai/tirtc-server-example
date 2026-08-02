#include "session_coordinator.h"

#define LOG_MODULE "session"
#include "common.h"
#include "device_adapter.h"

static DeviceBusiness _device_business(SessionKind kind) {
    switch (kind) {
    case SESSION_STREAM: return DEVICE_BUSINESS_STREAM;
    case SESSION_VOIP: return DEVICE_BUSINESS_VOIP;
    case SESSION_AI: return DEVICE_BUSINESS_AI;
    case SESSION_CALL: return DEVICE_BUSINESS_CALL;
    default: return DEVICE_BUSINESS_NONE;
    }
}

static void _notify_session(DeviceSessionEventType type, SessionKind kind,
                            int code) {
    DeviceBusiness business = _device_business(kind);
    uint64_t generation = device_adapter_session_generation(business);
    DeviceProductEvent event = {
        .type = type,
        .business = business,
        .generation = generation,
        .code = code,
    };
    str_copy(event.detail, sizeof(event.detail), session_kind_name(kind));
    device_product_notify(&event);
}

static int _start_locked(SessionCoordinator *sc, SessionKind kind) {
    SessionAdapter *adapter = &sc->adapters[kind];
    DeviceBusiness business = _device_business(kind);
    if (!adapter->start) {
        LOG_E("%s 模块不可用", session_kind_name(kind));
        device_adapter_session_starting(business);
        device_adapter_session_failed(business, -1,
                                      session_kind_name(kind));
        return -1;
    }
    device_adapter_session_starting(business);
    int result = device_resource_acquire(business);
    if (result != 0) {
        LOG_E("%s 产品资源申请失败: %d", session_kind_name(kind), result);
        device_adapter_session_failed(business, result,
                                      session_kind_name(kind));
        device_recovery_report(DEVICE_RECOVERY_RESOURCE, result,
                               session_kind_name(kind));
        return -1;
    }
    result = adapter->start(adapter->ctx);
    if (result != 0) {
        device_resource_release(business);
        device_adapter_session_failed(business, result,
                                      session_kind_name(kind));
        device_recovery_report(DEVICE_RECOVERY_TIRTC, result,
                               session_kind_name(kind));
        return -1;
    }
    sc->current = kind;
    device_adapter_session_started(business);
    return 0;
}

static void _stop_locked(SessionCoordinator *sc) {
    SessionKind current = sc->current;
    sc->current = SESSION_NONE;
    if (current == SESSION_NONE) return;
    DeviceBusiness business = _device_business(current);
    _notify_session(DEVICE_SESSION_STOPPING, current, 0);
    if (sc->adapters[current].stop)
        sc->adapters[current].stop(sc->adapters[current].ctx);
    device_adapter_session_stopped(business);
    device_resource_release(business);
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
