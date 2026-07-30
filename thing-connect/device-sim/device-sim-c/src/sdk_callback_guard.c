#include "sdk_callback_guard.h"

#include <string.h>

static void *_deferred_worker(void *opaque) {
    SdkCallbackGuard *guard = opaque;

    pthread_mutex_lock(&guard->lock);
    for (;;) {
        while (!guard->stopping &&
               (guard->queue_count == 0 || guard->active != 0))
            pthread_cond_wait(&guard->idle, &guard->lock);

        if (guard->stopping && guard->queue_count == 0) break;
        if (guard->active != 0) continue;

        SdkDeferredTask task = guard->queue[guard->queue_head];
        guard->queue_head =
            (guard->queue_head + 1) % SDK_CALLBACK_QUEUE_CAPACITY;
        guard->queue_count--;
        pthread_mutex_unlock(&guard->lock);

        if (task.copy_action)
            task.copy_action(task.arg, task.data, task.length);
        else
            task.action(task.arg);

        pthread_mutex_lock(&guard->lock);
        if (guard->pending > 0) guard->pending--;
        if (guard->active == 0 && guard->pending == 0)
            pthread_cond_broadcast(&guard->idle);
    }
    pthread_mutex_unlock(&guard->lock);
    return NULL;
}

int sdk_callback_guard_start(SdkCallbackGuard *guard) {
    if (!guard) return -1;
    pthread_mutex_lock(&guard->lock);
    if (guard->worker_started) {
        int ready = !guard->stopping;
        pthread_mutex_unlock(&guard->lock);
        return ready ? 0 : -1;
    }
    guard->stopping = 0;
    guard->queue_head = 0;
    guard->queue_tail = 0;
    guard->queue_count = 0;
    guard->pending = 0;
    int rc = pthread_create(&guard->worker, NULL, _deferred_worker, guard);
    if (rc == 0) guard->worker_started = 1;
    pthread_mutex_unlock(&guard->lock);
    return rc == 0 ? 0 : -1;
}

void sdk_callback_guard_stop(SdkCallbackGuard *guard) {
    if (!guard) return;
    sdk_callback_wait_all(guard);

    pthread_mutex_lock(&guard->lock);
    if (!guard->worker_started) {
        pthread_mutex_unlock(&guard->lock);
        return;
    }
    guard->stopping = 1;
    pthread_t worker = guard->worker;
    pthread_cond_broadcast(&guard->idle);
    pthread_mutex_unlock(&guard->lock);

    if (!pthread_equal(pthread_self(), worker)) pthread_join(worker, NULL);

    pthread_mutex_lock(&guard->lock);
    guard->worker_started = 0;
    guard->stopping = 0;
    guard->queue_head = 0;
    guard->queue_tail = 0;
    guard->queue_count = 0;
    pthread_mutex_unlock(&guard->lock);
}

void sdk_callback_enter(SdkCallbackGuard *guard) {
    pthread_mutex_lock(&guard->lock);
    guard->active++;
    pthread_mutex_unlock(&guard->lock);
}

void sdk_callback_leave(SdkCallbackGuard *guard) {
    pthread_mutex_lock(&guard->lock);
    if (guard->active > 0) guard->active--;
    if (guard->active == 0) pthread_cond_broadcast(&guard->idle);
    pthread_mutex_unlock(&guard->lock);
}

void sdk_callback_wait_idle(SdkCallbackGuard *guard) {
    pthread_mutex_lock(&guard->lock);
    while (guard->active != 0)
        pthread_cond_wait(&guard->idle, &guard->lock);
    pthread_mutex_unlock(&guard->lock);
}

void sdk_callback_wait_all(SdkCallbackGuard *guard) {
    pthread_mutex_lock(&guard->lock);
    while (guard->active != 0 || guard->pending != 0)
        pthread_cond_wait(&guard->idle, &guard->lock);
    pthread_mutex_unlock(&guard->lock);
}

int sdk_defer_action(SdkCallbackGuard *guard, sdk_deferred_action action, void *arg) {
    if (!guard || !action) return -1;
    pthread_mutex_lock(&guard->lock);
    if (!guard->worker_started || guard->stopping ||
        guard->queue_count == SDK_CALLBACK_QUEUE_CAPACITY) {
        pthread_mutex_unlock(&guard->lock);
        return -1;
    }
    guard->queue[guard->queue_tail] = (SdkDeferredTask){
        .action = action,
        .arg = arg,
    };
    guard->queue_tail =
        (guard->queue_tail + 1) % SDK_CALLBACK_QUEUE_CAPACITY;
    guard->queue_count++;
    guard->pending++;
    pthread_cond_broadcast(&guard->idle);
    pthread_mutex_unlock(&guard->lock);
    return 0;
}

int sdk_defer_copy_action(SdkCallbackGuard *guard,
                          sdk_deferred_copy_action action, void *context,
                          const void *data, size_t length) {
    if (!guard || !action || (!data && length) ||
        length > SDK_CALLBACK_COPY_CAPACITY)
        return -1;

    pthread_mutex_lock(&guard->lock);
    if (!guard->worker_started || guard->stopping ||
        guard->queue_count == SDK_CALLBACK_QUEUE_CAPACITY) {
        pthread_mutex_unlock(&guard->lock);
        return -1;
    }
    SdkDeferredTask *task = &guard->queue[guard->queue_tail];
    task->action = NULL;
    task->copy_action = action;
    task->arg = context;
    task->length = length;
    if (length) memcpy(task->data, data, length);
    guard->queue_tail =
        (guard->queue_tail + 1) % SDK_CALLBACK_QUEUE_CAPACITY;
    guard->queue_count++;
    guard->pending++;
    pthread_cond_broadcast(&guard->idle);
    pthread_mutex_unlock(&guard->lock);
    return 0;
}

static void _disconnect(void *arg) {
    TiRtcDisconnect((tirtc_conn_t)arg);
}

int sdk_defer_disconnect(SdkCallbackGuard *guard, tirtc_conn_t hconn) {
    return hconn ? sdk_defer_action(guard, _disconnect, hconn) : 0;
}
