#ifndef SDK_CALLBACK_GUARD_H
#define SDK_CALLBACK_GUARD_H

#include <pthread.h>

#include "tirtc/tiRTC.h"

/*
 * TiRTC invokes callbacks on SDK-owned threads.  Every registered callback
 * enters/leaves this guard; teardown waits for zero callbacks before Uninit.
 */
typedef struct {
    pthread_mutex_t lock;
    pthread_cond_t idle;
    unsigned int active;
    unsigned int pending;
} SdkCallbackGuard;

#define SDK_CALLBACK_GUARD_INITIALIZER \
    { PTHREAD_MUTEX_INITIALIZER, PTHREAD_COND_INITIALIZER, 0, 0 }

typedef void (*sdk_deferred_action)(void *arg);

void sdk_callback_enter(SdkCallbackGuard *guard);
void sdk_callback_leave(SdkCallbackGuard *guard);
void sdk_callback_wait_idle(SdkCallbackGuard *guard);
void sdk_callback_wait_all(SdkCallbackGuard *guard);

/* Run an SDK-affecting action only after the callback stack has unwound. */
int sdk_defer_action(SdkCallbackGuard *guard, sdk_deferred_action action, void *arg);
int sdk_defer_disconnect(SdkCallbackGuard *guard, tirtc_conn_t hconn);

#endif
