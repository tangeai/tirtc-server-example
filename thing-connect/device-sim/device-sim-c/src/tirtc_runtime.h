/** \file tirtc_runtime.h
 * \brief Process-wide TiRTC SDK lifecycle and callback dispatcher.
 *
 * The device process owns one TiRTC SDK instance.  Business modules register
 * handlers, while the session coordinator activates exactly one service
 * generation at a time.  Connections are bound to that generation so stale
 * callbacks cannot enter a later session.
 */
#ifndef TIRTC_RUNTIME_H
#define TIRTC_RUNTIME_H

#include <stdint.h>

#include "sdk_callback_guard.h"
#include "tirtc/tiRTC.h"

typedef enum {
    TIRTC_SERVICE_NONE = 0,
    TIRTC_SERVICE_STREAM,
    TIRTC_SERVICE_VOIP,
    TIRTC_SERVICE_AI,
    TIRTC_SERVICE_CALL,
    TIRTC_SERVICE_COUNT
} TirtcService;

int tirtc_runtime_register_service(TirtcService service,
                                   const TIRTCCALLBACKS *callbacks,
                                   SdkCallbackGuard *callback_guard);

int tirtc_runtime_start(const char *device_id, const char *secret_key,
                        const char *client_id, const char *endpoint);
void tirtc_runtime_stop(void);
int tirtc_runtime_is_started(void);

uint64_t tirtc_runtime_activate(TirtcService service);
int tirtc_runtime_deactivate(TirtcService service, uint64_t generation);
int tirtc_runtime_is_current(TirtcService service, uint64_t generation);

/** Bind an outgoing connection to the service generation active right now. */
int tirtc_runtime_bind_active_connection(TirtcService service,
                                         tirtc_conn_t hconn);

const char *tirtc_runtime_service_name(TirtcService service);

#ifdef DEVICE_SIM_TESTING
void tirtc_runtime_test_reset(void);
void tirtc_runtime_test_on_conn_accepted(tirtc_conn_t hconn);
void tirtc_runtime_test_on_conn_error(tirtc_conn_t hconn, int error);
void tirtc_runtime_test_on_disconnected(tirtc_conn_t hconn);
void tirtc_runtime_test_on_audio(tirtc_conn_t hconn,
                                 const TIRTCFRAMEINFO *frame, void *data);
void tirtc_runtime_test_on_command(tirtc_conn_t hconn, uint32_t command,
                                   const void *data, uint32_t length);
#endif

#endif
