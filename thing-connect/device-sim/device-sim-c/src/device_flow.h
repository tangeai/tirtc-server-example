/** \file device_flow.h
 * \brief Device provisioning protocol layer — HTTP + MQTT + HMAC signing.
 *
 * Embedded-reference: shows how a device talks to the server over
 * libcurl (HTTP) + libmosquitto (MQTT) + SDK bundled mbedTLS (HMAC-SHA256 / Base64).
 */

#ifndef DEVICE_FLOW_H
#define DEVICE_FLOW_H

#include <stddef.h>
#include <signal.h>
#include <cjson/cJSON.h>
/* NOTE: Linux uses the mbedTLS headers shipped with SDK 0.1.6, verified
 * compatible with the SDK 2.2.1 exported one-shot HMAC/Base64 symbols.
 * On embedded platforms (ESP-IDF/FreeRTOS), use the platform mbedTLS:
 *   mbedtls_md_context_t / mbedtls_md_hmac_starts/update/finish + base64 */

#ifdef __cplusplus
extern "C" {
#endif

/* ── Service descriptor and result types ──────────────────────────────── */

typedef struct {
    char device_server[256];
    char voip_server[256];
    char ai_server[256];
    char call_server[256];   /* call-server (device-to-device P2P) */
    char mqtt_host[128];
    int  mqtt_port;
    int  mqtt_tls;          /* 1 if scheme is mqtts://, 0 if mqtt:// */
    char tirtc_endpoint[256]; /* TiRTC SDK WHIP/WHEP endpoint */
} DeviceServices;

typedef struct {
    char code[16];
    char temp_token[512];
    char temp_client_id[64];
} ReportResult;

/* ── MQTT message handler (implemented by voip / ai / stream modules) ──── */

typedef struct {
    void (*on_call_incoming)(void *ctx, const cJSON *payload);
    void (*on_callers_update)(void *ctx);
    void (*on_call_cancel)(void *ctx, const cJSON *payload);
    void (*on_unbind)(void *ctx);

    /* channel=device (device-to-device P2P call) */
    void (*on_device_call_incoming)(void *ctx, const cJSON *payload);
    void (*on_device_room_cancel)(void *ctx, const cJSON *payload);
    void (*on_device_call_reject)(void *ctx, const cJSON *payload);
    /* Legacy callback remains source-compatible; _ex receives the new payload. */
    void (*on_device_callers_update)(void *ctx);
    void (*on_device_callers_update_ex)(void *ctx, const cJSON *payload);
    void (*on_device_callee_answered)(void *ctx, const cJSON *payload);
} MqttMsgHandler;

/* ── CA certificate path ─────────────────────────────────────────────── */

/** Set the CA certificate file path for MQTT TLS connections.
 *  Must be called before connect_temp_mqtt() or connect_mqtt_blocking(). */
void set_mqtt_ca_cert(const char *path);
void set_mqtt_insecure(int insecure);

/* ── Service discovery ──────────────────────────────────────────────────── */

/** Fetch service endpoints from the registry.
 *  base_url: service discovery entry point (e.g. "https://ep-open.tangeopen.com").
 *  If empty, uses the built-in default.
 *  Returns 0 on success, -1 on failure. */
int fetch_services(DeviceServices *svc, const char *base_url);

/* ── Device report (phase 1 – unbound flow) ────────────────────────────── */

/** POST /v1/device/report — register fingerprint, get temp credentials.
 *  When device_key is provided, sends HMAC signature headers (scenario 1).
 *  Otherwise sends plain body (scenario 2).
 *  Returns 0 on success, -1 on failure. */
int report_device(const char *server,
                  const char *mac, const char *device_id,
                  const char *device_key,
                  ReportResult *result);

/* ── Token exchange (phase 2 – bound device) ────────────────────────────── */

/** POST /v1/device/token with HMAC-SHA256 signature.
 *  Writes mqtt_token into token_out (caller-supplied buffer).
 *  Returns 0 on success, -1 on error (caller may retry or exit). */
int get_mqtt_token(const char *server,
                   const char *device_id, const char *device_key,
                   const char *mac,
                   char *token_out, size_t token_size);

/* ── Temporary MQTT (wait for auth_grant) ──────────────────────────────── */

/** Connect with temp credentials, wait for auth_grant.
 *  If device_id_out is filled, credentials came from auth_grant payload.
 *  If device_id_out stays empty after success, it's a pre-burned device.
 *  Returns 0 on auth_grant received, -1 on timeout/error. */
int connect_temp_mqtt(const char *host, int port,
                      const char *temp_client_id, const char *temp_token,
                      int timeout_sec, int use_tls,
                      char *device_id_out, size_t id_size,
                      char *device_key_out, size_t key_size);

/* ── Permanent MQTT (long-lived connection) ────────────────────────────── */

/** Establish permanent MQTT connection (ClientID=sn_{device_id}).
 *  Blocks until stop_flag becomes non-zero.
 *  Routes incoming messages to handler callbacks.
 *  Runs heartbeat in a background thread. */
int connect_mqtt_blocking(const char *host, int port,
                          const char *device_id, const char *mqtt_token,
                          const MqttMsgHandler *handler, void *ctx,
                          volatile sig_atomic_t *stop_flag, int use_tls);

/* ── Crypto helpers ─────────────────────────────────────────────────────── */

/** HMAC-SHA256(key, data) → Base64.
 *  out must hold at least 45 bytes (32 bytes HMAC → 44 chars Base64 + NUL). */
int hmac_sha256_b64(const char *key, const char *data,
                    char *out, size_t out_size);

#ifdef __cplusplus
}
#endif

#endif /* DEVICE_FLOW_H */
