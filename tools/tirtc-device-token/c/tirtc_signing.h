/**
 * @file tirtc_signing.h
 * @brief TGV1-HMAC-SHA256 request signing for tange.ai TiRTC server APIs.
 *
 * Reference: https://tange-ai.feishu.cn/wiki/wikcn6GaETEIVO0jgCP1ASHf3Mg
 *
 * Dependencies: OpenSSL (libcrypto) for SHA256 and HMAC-SHA256.
 *
 * Thread safety: NOT thread-safe. Returned string pointers point to internal
 * static buffers valid only until the next call to tirtc_sign_request().
 */

#ifndef TIRTC_SIGNING_H
#define TIRTC_SIGNING_H

#include <stddef.h> /* size_t */
#include <time.h>   /* time_t */

#ifdef __cplusplus
extern "C" {
#endif

/** Maximum number of header entries returned. */
#define TIRTC_MAX_HEADERS 9
/** Maximum length of a single header value (keys are short, values may be long). */
#define TIRTC_MAX_VALUE_LEN 512

/** A single HTTP header: name and value as null-terminated strings. */
typedef struct {
    const char *name;
    char value[TIRTC_MAX_VALUE_LEN];
} TirtcHeader;

/** Result containing an array of header entries. */
typedef struct {
    TirtcHeader entries[TIRTC_MAX_HEADERS];
    size_t count;
} TirtcHeaders;

/**
 * @brief Build TGV1-HMAC-SHA256 signed HTTP headers.
 *
 * @param access_key    Appears in the Authorization Credential field.
 * @param access_secret Used for HMAC key derivation (keep this secret).
 * @param app_id        TiRTC application ID (X-Tg-App-Id header).
 * @param method        HTTP method (GET, POST, PUT, PATCH, DELETE).
 * @param uri_path      URI path, e.g. "/v2/device/info".
 * @param body          Request body bytes.
 * @param body_len      Length of body in bytes; (size_t)-1 to treat as C string.
 * @param raw_query     Raw query string without leading "?" (NULL for none).
 * @param signing_time  UTC time; pass 0 to use current time.
 * @return TirtcHeaders struct with signed headers.
 *
 * @note NOT reentrant. Internal static buffers are reused across calls.
 */
TirtcHeaders tirtc_sign_request(
    const char *access_key,
    const char *access_secret,
    const char *app_id,
    const char *method,
    const char *uri_path,
    const char *body,
    size_t body_len,
    const char *raw_query,
    time_t signing_time
);

/** @brief Return the TGV1 algorithm identifier string. */
const char *tirtc_algorithm(void);

#ifdef __cplusplus
}
#endif

#endif /* TIRTC_SIGNING_H */
