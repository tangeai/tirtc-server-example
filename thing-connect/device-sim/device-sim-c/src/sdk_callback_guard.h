#ifndef SDK_CALLBACK_GUARD_H
#define SDK_CALLBACK_GUARD_H

#include <stddef.h>
#include <pthread.h>

#include "tirtc/tiRTC.h"

typedef void (*sdk_deferred_action)(void *arg);
typedef void (*sdk_deferred_copy_action)(void *context,
                                         const void *data, size_t length);

#define SDK_CALLBACK_QUEUE_CAPACITY 32
#define SDK_CALLBACK_COPY_CAPACITY 2048

typedef struct {
    sdk_deferred_action action;
    sdk_deferred_copy_action copy_action;
    void *arg;
    size_t length;
    unsigned char data[SDK_CALLBACK_COPY_CAPACITY];
} SdkDeferredTask;

/*
 * TiRTC invokes callbacks on SDK-owned threads.  Every registered callback
 * enters/leaves this guard.  Deferred control work runs on one bounded,
 * process-lifetime worker instead of creating threads from SDK callbacks.
 */
typedef struct {
    pthread_mutex_t lock;
    pthread_cond_t idle;
    unsigned int active;
    unsigned int pending;
    pthread_t worker;
    unsigned int worker_started;
    unsigned int stopping;
    size_t queue_head;
    size_t queue_tail;
    size_t queue_count;
    SdkDeferredTask queue[SDK_CALLBACK_QUEUE_CAPACITY];
} SdkCallbackGuard;

#define SDK_CALLBACK_GUARD_INITIALIZER \
    { PTHREAD_MUTEX_INITIALIZER, PTHREAD_COND_INITIALIZER, 0, 0, \
      (pthread_t)0, 0, 0, 0, 0, 0, {{0}} }

int sdk_callback_guard_start(SdkCallbackGuard *guard);
void sdk_callback_guard_stop(SdkCallbackGuard *guard);
void sdk_callback_enter(SdkCallbackGuard *guard);
void sdk_callback_leave(SdkCallbackGuard *guard);
void sdk_callback_wait_idle(SdkCallbackGuard *guard);
void sdk_callback_wait_all(SdkCallbackGuard *guard);

/* Enqueue an SDK-affecting action to run after the callback stack unwinds. */
int sdk_defer_action(SdkCallbackGuard *guard, sdk_deferred_action action, void *arg);
int sdk_defer_copy_action(SdkCallbackGuard *guard,
                          sdk_deferred_copy_action action, void *context,
                          const void *data, size_t length);
int sdk_defer_disconnect(SdkCallbackGuard *guard, tirtc_conn_t hconn);

#endif
