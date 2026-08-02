#define LOG_MODULE "arbiter"
#include "common.h"
#include "device_adapter.h"
#include "session_arbiter.h"

static DeviceBusiness _device_business(SessionKind kind) {
    switch (kind) {
    case SESSION_STREAM: return DEVICE_BUSINESS_STREAM;
    case SESSION_VOIP: return DEVICE_BUSINESS_VOIP;
    case SESSION_AI: return DEVICE_BUSINESS_AI;
    case SESSION_CALL: return DEVICE_BUSINESS_CALL;
    default: return DEVICE_BUSINESS_NONE;
    }
}

static void _notify_incoming(SessionKind kind, const char *session_id) {
    DeviceBusiness business = _device_business(kind);
    DeviceProductEvent event = {
        .type = DEVICE_SESSION_INCOMING,
        .business = business,
        .generation = device_adapter_session_generation(business),
    };
    str_copy(event.session_id, sizeof(event.session_id),
             session_id ? session_id : "");
    device_product_notify(&event);
}

static uint32_t _kind_bit(SessionKind kind) {
    return kind > SESSION_NONE && kind <= SESSION_CALL
               ? (1U << (unsigned int)kind) : 0;
}

static int _id_matches(const char *stored, const char *expected) {
    return !expected || strcmp(stored, expected) == 0;
}

static void _clear_pending_locked(SessionArbiter *arbiter) {
    arbiter->pending_mask = 0;
    arbiter->pending_session_id[0] = '\0';
    arbiter->pending_deadline_ms = 0;
}

static void _expire_pending_locked(SessionArbiter *arbiter) {
    if (arbiter->pending_mask != 0 && arbiter->pending_deadline_ms > 0 &&
        now_ms() >= arbiter->pending_deadline_ms) {
        LOG_W("%s 待接票据已超时 session=%s",
              session_kind_name(
                  (SessionKind)__builtin_ctz(arbiter->pending_mask)),
              arbiter->pending_session_id);
        _clear_pending_locked(arbiter);
        arbiter->pending_generation++;
    }
}

static void _finish_coordinator_with_recovery(SessionArbiter *arbiter,
                                               SessionKind kind) {
    if (session_coordinator_finish_checked(arbiter->coordinator, kind) == 0)
        return;
    static const int retry_delays_ms[] = {50, 200, 500};
    for (size_t i = 0;
         i < sizeof(retry_delays_ms) / sizeof(retry_delays_ms[0]); i++) {
        sleep_ms(retry_delays_ms[i]);
        pthread_mutex_lock(&arbiter->state_lock);
        int closed = arbiter->closed;
        pthread_mutex_unlock(&arbiter->state_lock);
        if (closed) return;
        if (session_coordinator_start_stream(arbiter->coordinator) == 0) {
            LOG_I("%s 结束后已重试恢复 H5 实时流",
                  session_kind_name(kind));
            return;
        }
    }
    LOG_E("%s 结束后恢复 H5 实时流失败",
          session_kind_name(kind));
    device_recovery_report(DEVICE_RECOVERY_TIRTC, -1,
                           "会话结束后恢复实时流失败");
}

static void _finish_generation(SessionArbiter *arbiter, SessionKind kind,
                               uint64_t generation) {
    pthread_mutex_lock(&arbiter->transition_lock);
    pthread_mutex_lock(&arbiter->state_lock);
    int current = !arbiter->closed && arbiter->owner == kind &&
                  arbiter->generation == generation;
    if (current) {
        arbiter->owner = SESSION_NONE;
        arbiter->owner_session_id[0] = '\0';
        arbiter->owner_cancelled = 0;
    }
    pthread_mutex_unlock(&arbiter->state_lock);
    if (current)
        _finish_coordinator_with_recovery(arbiter, kind);
    pthread_mutex_unlock(&arbiter->transition_lock);
}

static void *_lifecycle_worker(void *opaque) {
    SessionArbiter *arbiter = opaque;
    for (;;) {
        pthread_mutex_lock(&arbiter->state_lock);
        while (!arbiter->worker_stop && arbiter->finish_count == 0)
            pthread_cond_wait(&arbiter->finish_ready, &arbiter->state_lock);
        if (arbiter->worker_stop && arbiter->finish_count == 0) {
            pthread_cond_broadcast(&arbiter->finish_idle);
            pthread_mutex_unlock(&arbiter->state_lock);
            return NULL;
        }
        SessionFinishRequest request =
            arbiter->finish_queue[arbiter->finish_head];
        arbiter->finish_head =
            (arbiter->finish_head + 1U) % SESSION_ARBITER_QUEUE_CAP;
        arbiter->finish_count--;
        arbiter->finish_active = 1;
        pthread_mutex_unlock(&arbiter->state_lock);

        _finish_generation(arbiter, request.kind, request.generation);

        pthread_mutex_lock(&arbiter->state_lock);
        arbiter->finish_active = 0;
        if (arbiter->finish_count == 0)
            pthread_cond_broadcast(&arbiter->finish_idle);
        pthread_mutex_unlock(&arbiter->state_lock);
    }
}

void session_arbiter_init(SessionArbiter *arbiter,
                          SessionCoordinator *coordinator) {
    *arbiter = (SessionArbiter){0};
    pthread_mutex_init(&arbiter->state_lock, NULL);
    pthread_mutex_init(&arbiter->transition_lock, NULL);
    pthread_cond_init(&arbiter->finish_idle, NULL);
    pthread_cond_init(&arbiter->finish_ready, NULL);
    arbiter->coordinator = coordinator;
    if (pthread_create(&arbiter->worker_thread, NULL,
                       _lifecycle_worker, arbiter) == 0) {
        arbiter->worker_started = 1;
    } else {
        LOG_E("无法创建常驻会话生命周期线程");
    }
}

int session_arbiter_ready(SessionArbiter *arbiter) {
    return arbiter && arbiter->worker_started;
}

int session_arbiter_offer_pending_id(SessionArbiter *arbiter, SessionKind kind,
                                     const char *session_id,
                                     int64_t ttl_ms) {
    uint32_t bit = _kind_bit(kind);
    if (!arbiter || !bit || kind == SESSION_STREAM) return -1;
    pthread_mutex_lock(&arbiter->state_lock);
    _expire_pending_locked(arbiter);
    int granted = !arbiter->closed && arbiter->owner == SESSION_NONE &&
                  arbiter->pending_mask == 0;
    if (granted) {
        arbiter->pending_mask = bit;
        arbiter->pending_generation++;
        str_copy(arbiter->pending_session_id,
                 sizeof(arbiter->pending_session_id),
                 session_id ? session_id : "");
        arbiter->pending_deadline_ms =
            ttl_ms > 0 ? now_ms() + ttl_ms : 0;
    }
    pthread_mutex_unlock(&arbiter->state_lock);
    if (!granted)
        LOG_W("会话冲突：%s 来电未获得待接权",
              session_kind_name(kind));
    else
        _notify_incoming(kind, session_id);
    return granted ? 0 : -1;
}

int session_arbiter_offer_pending(SessionArbiter *arbiter, SessionKind kind) {
    return session_arbiter_offer_pending_id(arbiter, kind, "", 0);
}

int session_arbiter_admit_incoming_id(SessionArbiter *arbiter,
                                      SessionKind kind,
                                      const char *session_id,
                                      int64_t ttl_ms) {
    uint32_t bit = _kind_bit(kind);
    if (!arbiter || !bit || kind == SESSION_STREAM) return -1;
    pthread_mutex_lock(&arbiter->state_lock);
    _expire_pending_locked(arbiter);
    int decision = -1;
    if (!arbiter->closed && arbiter->owner == kind) {
        decision = 1;
    } else if (!arbiter->closed && arbiter->owner == SESSION_NONE &&
               arbiter->pending_mask == 0) {
        arbiter->pending_mask = bit;
        arbiter->pending_generation++;
        str_copy(arbiter->pending_session_id,
                 sizeof(arbiter->pending_session_id),
                 session_id ? session_id : "");
        arbiter->pending_deadline_ms =
            ttl_ms > 0 ? now_ms() + ttl_ms : 0;
        decision = 0;
    }
    pthread_mutex_unlock(&arbiter->state_lock);
    if (decision == 0) _notify_incoming(kind, session_id);
    return decision;
}

int session_arbiter_admit_incoming(SessionArbiter *arbiter,
                                   SessionKind kind) {
    return session_arbiter_admit_incoming_id(arbiter, kind, "", 0);
}

int session_arbiter_cancel_id(SessionArbiter *arbiter, SessionKind kind,
                              const char *session_id) {
    if (!arbiter) return 0;
    int result = 0;
    pthread_mutex_lock(&arbiter->state_lock);
    _expire_pending_locked(arbiter);
    uint32_t bit = _kind_bit(kind);
    if ((arbiter->pending_mask & bit) != 0 &&
        _id_matches(arbiter->pending_session_id, session_id)) {
        _clear_pending_locked(arbiter);
        arbiter->pending_generation++;
        result = 1;
    } else if (arbiter->owner == kind &&
               _id_matches(arbiter->owner_session_id, session_id)) {
        /* Invalidate rollback of the exact pending ticket consumed by begin. */
        arbiter->owner_cancelled = 1;
        arbiter->pending_generation++;
        result = 2;
    }
    pthread_mutex_unlock(&arbiter->state_lock);
    return result;
}

void session_arbiter_clear_pending_id(SessionArbiter *arbiter,
                                      SessionKind kind,
                                      const char *session_id) {
    (void)session_arbiter_cancel_id(arbiter, kind, session_id);
}

void session_arbiter_clear_pending(SessionArbiter *arbiter, SessionKind kind) {
    session_arbiter_clear_pending_id(arbiter, kind, NULL);
}

int session_arbiter_has_pending_id(SessionArbiter *arbiter, SessionKind kind,
                                   const char *session_id) {
    if (!arbiter) return 0;
    pthread_mutex_lock(&arbiter->state_lock);
    _expire_pending_locked(arbiter);
    int pending =
        (arbiter->pending_mask & _kind_bit(kind)) != 0 &&
        _id_matches(arbiter->pending_session_id, session_id);
    pthread_mutex_unlock(&arbiter->state_lock);
    return pending;
}

int session_arbiter_has_pending(SessionArbiter *arbiter, SessionKind kind) {
    return session_arbiter_has_pending_id(arbiter, kind, NULL);
}

int session_arbiter_begin_id_ex(SessionArbiter *arbiter, SessionKind kind,
                                int consume_pending, const char *session_id,
                                SessionLease *lease_out,
                                session_arbiter_lease_ready_fn lease_ready,
                                void *lease_ready_user) {
    if (!arbiter || kind == SESSION_NONE || kind == SESSION_STREAM) return -1;
    uint32_t bit = _kind_bit(kind);
    pthread_mutex_lock(&arbiter->transition_lock);
    pthread_mutex_lock(&arbiter->state_lock);
    _expire_pending_locked(arbiter);
    int pending_ok = consume_pending
                         ? arbiter->pending_mask == bit &&
                               _id_matches(arbiter->pending_session_id,
                                           session_id)
                         : arbiter->pending_mask == 0;
    if (arbiter->closed || arbiter->owner != SESSION_NONE || !pending_ok) {
        SessionKind owner = arbiter->owner;
        uint32_t pending = arbiter->pending_mask;
        pthread_mutex_unlock(&arbiter->state_lock);
        pthread_mutex_unlock(&arbiter->transition_lock);
        LOG_W("会话冲突：%s 未获得 RTC（owner=%s pending=%#x）",
              session_kind_name(kind), session_kind_name(owner), pending);
        return -1;
    }

    char consumed_id[SESSION_ARBITER_ID_CAP] = "";
    int64_t consumed_deadline = 0;
    if (consume_pending) {
        str_copy(consumed_id, sizeof(consumed_id),
                 arbiter->pending_session_id);
        consumed_deadline = arbiter->pending_deadline_ms;
        _clear_pending_locked(arbiter);
    }
    arbiter->owner = kind;
    str_copy(arbiter->owner_session_id, sizeof(arbiter->owner_session_id),
             consume_pending ? consumed_id : (session_id ? session_id : ""));
    arbiter->owner_cancelled = 0;
    arbiter->generation++;
    uint64_t generation = arbiter->generation;
    uint64_t pending_generation = arbiter->pending_generation;
    SessionLease provisional_lease = {kind, generation, ""};
    str_copy(provisional_lease.session_id,
             sizeof(provisional_lease.session_id),
             arbiter->owner_session_id);
    pthread_mutex_unlock(&arbiter->state_lock);

    /*
     * An adapter may synchronously report its terminal event from start().
     * Publish this generation first so the callback never reuses the lease
     * of an earlier same-kind session.
     */
    if (lease_ready)
        lease_ready(lease_ready_user, &provisional_lease);
    int rc = session_coordinator_begin(arbiter->coordinator, kind);
    pthread_mutex_lock(&arbiter->state_lock);
    int cancelled = arbiter->owner == kind &&
                    arbiter->generation == generation &&
                    arbiter->owner_cancelled;
    if (rc != 0 || cancelled) {
        if (arbiter->owner == kind && arbiter->generation == generation) {
            arbiter->owner = SESSION_NONE;
            arbiter->owner_session_id[0] = '\0';
            arbiter->owner_cancelled = 0;
            if (rc != 0 && consume_pending && !cancelled &&
                !arbiter->closed &&
                arbiter->pending_generation == pending_generation &&
                (consumed_deadline == 0 || now_ms() < consumed_deadline)) {
                arbiter->pending_mask = bit;
                str_copy(arbiter->pending_session_id,
                         sizeof(arbiter->pending_session_id), consumed_id);
                arbiter->pending_deadline_ms = consumed_deadline;
            }
        }
    } else if (lease_out) {
        *lease_out = provisional_lease;
    }
    pthread_mutex_unlock(&arbiter->state_lock);
    if (cancelled)
        _finish_coordinator_with_recovery(arbiter, kind);
    pthread_mutex_unlock(&arbiter->transition_lock);
    return rc == 0 && !cancelled ? 0 : -1;
}

int session_arbiter_begin_id(SessionArbiter *arbiter, SessionKind kind,
                             int consume_pending, const char *session_id,
                             SessionLease *lease_out) {
    return session_arbiter_begin_id_ex(
        arbiter, kind, consume_pending, session_id, lease_out, NULL, NULL);
}

int session_arbiter_begin(SessionArbiter *arbiter, SessionKind kind,
                          int consume_pending) {
    return session_arbiter_begin_id(
        arbiter, kind, consume_pending, NULL, NULL);
}

int session_arbiter_run_action(SessionArbiter *arbiter,
                               const SessionLease *lease,
                               session_arbiter_action_fn action,
                               void *action_user) {
    if (!arbiter || !lease || !action) return -1;
    pthread_mutex_lock(&arbiter->transition_lock);
    pthread_mutex_lock(&arbiter->state_lock);
    int current = !arbiter->closed &&
                  arbiter->owner == lease->kind &&
                  arbiter->generation == lease->generation &&
                  _id_matches(arbiter->owner_session_id,
                              lease->session_id) &&
                  !arbiter->owner_cancelled;
    pthread_mutex_unlock(&arbiter->state_lock);
    if (!current) {
        pthread_mutex_unlock(&arbiter->transition_lock);
        return -1;
    }

    int rc = action(action_user);
    pthread_mutex_lock(&arbiter->state_lock);
    current = !arbiter->closed &&
              arbiter->owner == lease->kind &&
              arbiter->generation == lease->generation;
    int cancelled = current && arbiter->owner_cancelled;
    if (current && (rc != 0 || cancelled)) {
        arbiter->owner = SESSION_NONE;
        arbiter->owner_session_id[0] = '\0';
        arbiter->owner_cancelled = 0;
    }
    pthread_mutex_unlock(&arbiter->state_lock);
    if (current && (rc != 0 || cancelled))
        _finish_coordinator_with_recovery(arbiter, lease->kind);
    pthread_mutex_unlock(&arbiter->transition_lock);
    return rc == 0 && !cancelled ? 0 : -1;
}

void session_arbiter_finish_lease(SessionArbiter *arbiter,
                                  const SessionLease *lease) {
    if (!arbiter || !lease) return;
    _finish_generation(arbiter, lease->kind, lease->generation);
}

void session_arbiter_finish(SessionArbiter *arbiter, SessionKind kind) {
    if (!arbiter) return;
    pthread_mutex_lock(&arbiter->state_lock);
    SessionLease lease = {kind, arbiter->generation, ""};
    str_copy(lease.session_id, sizeof(lease.session_id),
             arbiter->owner_session_id);
    pthread_mutex_unlock(&arbiter->state_lock);
    session_arbiter_finish_lease(arbiter, &lease);
}

static void _finish_async_generation(SessionArbiter *arbiter,
                                     SessionKind kind,
                                     uint64_t generation,
                                     int restore_pending) {
    if (!arbiter) return;
    pthread_mutex_lock(&arbiter->state_lock);
    if (arbiter->closed || arbiter->owner != kind ||
        arbiter->generation != generation) {
        pthread_mutex_unlock(&arbiter->state_lock);
        return;
    }
    if (restore_pending && arbiter->pending_mask == 0) {
        arbiter->pending_mask = _kind_bit(kind);
        str_copy(arbiter->pending_session_id,
                 sizeof(arbiter->pending_session_id),
                 arbiter->owner_session_id);
        arbiter->pending_deadline_ms = now_ms() + 45000;
        arbiter->pending_generation++;
    }

    SessionFinishRequest request = {kind, generation};
    for (unsigned int i = 0; i < arbiter->finish_count; i++) {
        unsigned int index =
            (arbiter->finish_head + i) % SESSION_ARBITER_QUEUE_CAP;
        if (arbiter->finish_queue[index].kind == kind &&
            arbiter->finish_queue[index].generation == generation) {
            pthread_mutex_unlock(&arbiter->state_lock);
            return;
        }
    }
    if (!arbiter->worker_started ||
        arbiter->finish_count == SESSION_ARBITER_QUEUE_CAP) {
        pthread_mutex_unlock(&arbiter->state_lock);
        LOG_E("会话生命周期队列不可用，无法结束 %s",
              session_kind_name(kind));
        return;
    }
    unsigned int tail =
        (arbiter->finish_head + arbiter->finish_count) %
        SESSION_ARBITER_QUEUE_CAP;
    arbiter->finish_queue[tail] = request;
    arbiter->finish_count++;
    pthread_cond_signal(&arbiter->finish_ready);
    pthread_mutex_unlock(&arbiter->state_lock);
}

void session_arbiter_finish_lease_async(SessionArbiter *arbiter,
                                        const SessionLease *lease) {
    if (!lease) return;
    _finish_async_generation(
        arbiter, lease->kind, lease->generation, 0);
}

void session_arbiter_finish_lease_async_restore_pending(
    SessionArbiter *arbiter, const SessionLease *lease) {
    if (!lease) return;
    _finish_async_generation(
        arbiter, lease->kind, lease->generation, 1);
}

void session_arbiter_finish_async(SessionArbiter *arbiter, SessionKind kind) {
    if (!arbiter) return;
    pthread_mutex_lock(&arbiter->state_lock);
    uint64_t generation = arbiter->generation;
    pthread_mutex_unlock(&arbiter->state_lock);
    _finish_async_generation(arbiter, kind, generation, 0);
}

void session_arbiter_finish_async_restore_pending(SessionArbiter *arbiter,
                                                   SessionKind kind) {
    if (!arbiter) return;
    pthread_mutex_lock(&arbiter->state_lock);
    uint64_t generation = arbiter->generation;
    pthread_mutex_unlock(&arbiter->state_lock);
    _finish_async_generation(arbiter, kind, generation, 1);
}

SessionKind session_arbiter_current(SessionArbiter *arbiter) {
    if (!arbiter) return SESSION_NONE;
    pthread_mutex_lock(&arbiter->state_lock);
    SessionKind owner = arbiter->owner;
    pthread_mutex_unlock(&arbiter->state_lock);
    return owner;
}

void session_arbiter_shutdown(SessionArbiter *arbiter) {
    if (!arbiter) return;
    pthread_mutex_lock(&arbiter->transition_lock);
    pthread_mutex_lock(&arbiter->state_lock);
    int first = !arbiter->closed;
    arbiter->closed = 1;
    arbiter->owner = SESSION_NONE;
    arbiter->owner_session_id[0] = '\0';
    arbiter->owner_cancelled = 0;
    _clear_pending_locked(arbiter);
    pthread_mutex_unlock(&arbiter->state_lock);
    if (first) session_coordinator_shutdown(arbiter->coordinator);
    pthread_mutex_unlock(&arbiter->transition_lock);

    pthread_mutex_lock(&arbiter->state_lock);
    arbiter->worker_stop = 1;
    pthread_cond_broadcast(&arbiter->finish_ready);
    while (arbiter->finish_count != 0 || arbiter->finish_active)
        pthread_cond_wait(&arbiter->finish_idle, &arbiter->state_lock);
    pthread_mutex_unlock(&arbiter->state_lock);
    if (arbiter->worker_started &&
        !pthread_equal(pthread_self(), arbiter->worker_thread)) {
        pthread_join(arbiter->worker_thread, NULL);
        arbiter->worker_started = 0;
    }
}

void session_arbiter_destroy(SessionArbiter *arbiter) {
    if (!arbiter) return;
    session_arbiter_shutdown(arbiter);
    pthread_cond_destroy(&arbiter->finish_ready);
    pthread_cond_destroy(&arbiter->finish_idle);
    pthread_mutex_destroy(&arbiter->transition_lock);
    pthread_mutex_destroy(&arbiter->state_lock);
}
