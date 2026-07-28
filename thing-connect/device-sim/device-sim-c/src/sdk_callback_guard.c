#include "sdk_callback_guard.h"

#include <stdlib.h>

typedef struct {
    SdkCallbackGuard *guard;
    sdk_deferred_action action;
    void *arg;
} DeferredTask;

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

static void *_deferred_worker(void *opaque) {
    DeferredTask *task = opaque;
    sdk_callback_wait_idle(task->guard);
    task->action(task->arg);
    pthread_mutex_lock(&task->guard->lock);
    if (task->guard->pending > 0) task->guard->pending--;
    if (task->guard->active == 0 && task->guard->pending == 0)
        pthread_cond_broadcast(&task->guard->idle);
    pthread_mutex_unlock(&task->guard->lock);
    free(task);
    return NULL;
}

int sdk_defer_action(SdkCallbackGuard *guard, sdk_deferred_action action, void *arg) {
    if (!guard || !action) return -1;
    DeferredTask *task = malloc(sizeof(*task));
    if (!task) return -1;
    task->guard = guard;
    task->action = action;
    task->arg = arg;

    pthread_mutex_lock(&guard->lock);
    guard->pending++;
    pthread_mutex_unlock(&guard->lock);

    pthread_t thread;
    if (pthread_create(&thread, NULL, _deferred_worker, task) != 0) {
        pthread_mutex_lock(&guard->lock);
        guard->pending--;
        if (guard->active == 0 && guard->pending == 0)
            pthread_cond_broadcast(&guard->idle);
        pthread_mutex_unlock(&guard->lock);
        free(task);
        return -1;
    }
    pthread_detach(thread);
    return 0;
}

static void _disconnect(void *arg) {
    TiRtcDisconnect((tirtc_conn_t)arg);
}

int sdk_defer_disconnect(SdkCallbackGuard *guard, tirtc_conn_t hconn) {
    return hconn ? sdk_defer_action(guard, _disconnect, hconn) : 0;
}
