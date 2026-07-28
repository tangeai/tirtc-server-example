/**
 * @file tirtc_signing.c
 * @brief TGV1-HMAC-SHA256 request signing implementation.
 *
 * Dependencies: OpenSSL (libcrypto) — link with -lcrypto.
 */

#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif

#include "tirtc_signing.h"

#include <openssl/evp.h>
#include <openssl/hmac.h>
#include <openssl/sha.h>

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#define TGV1_ALG "TGV1-HMAC-SHA256"
#define CREDENTIAL_SCOPE_SUFFIX "tgv1_request"

/* --- internal static buffers (NOT THREAD SAFE) --- */
static char g_date_buf[17];        /* YYYYMMDD */
static char g_scope[32];           /* YYYYMMDD/tgv1_request */
static char g_tg_date[18];         /* YYYYMMDDTHHmmssZ + NUL */
static char g_signed_headers[256];
static char g_auth[1024];
static char g_hash_hex[65];        /* SHA-256 hex = 64 chars + NUL */

/* --- forward declarations --- */
static void sha256_hex(const unsigned char *data, size_t len, char out[65]);
static void hmac_sha256_bytes(const char *data, const unsigned char *key, size_t key_len, unsigned char *out);
static void hmac_sha256_hex(const char *data, const unsigned char *key, size_t key_len, char out[65]);
static void build_credential_scope(time_t t, char out[32]);
static void canonical_uri_path(const char *path, char *out, size_t out_size);
static const char *canonical_query(const char *method, const char *raw_query);
static int is_body_method(const char *method);
static void format_tg_date(time_t t, char out[17]);
static void format_date_yyyymmdd(time_t t, char out[17]);
static void strip_trailing_newline(char *s);

const char *tirtc_algorithm(void) {
    return TGV1_ALG;
}

TirtcHeaders tirtc_sign_request(
    const char *access_key,
    const char *access_secret,
    const char *app_id,
    const char *method,
    const char *uri_path,
    const char *body,
    size_t body_len,
    const char *raw_query,
    time_t signing_time)
{
    TirtcHeaders result;
    memset(&result, 0, sizeof(result));

    /* defaults */
    if (!method) method = "GET";
    if (!uri_path) uri_path = "/";
    if (!body) { body = ""; body_len = 0; }
    if (!raw_query) raw_query = "";
    if (!app_id) app_id = "";
    if (!access_key) access_key = "";
    if (!access_secret) access_secret = "";

    if (body_len == (size_t)-1) body_len = strlen(body);
    if (signing_time == 0) signing_time = time(NULL);

    /* uppercase method (on stack) */
    char method_upper[16];
    size_t mlen = strlen(method);
    if (mlen > sizeof(method_upper) - 1) mlen = sizeof(method_upper) - 1;
    for (size_t i = 0; i < mlen; i++) {
        method_upper[i] = (method[i] >= 'a' && method[i] <= 'z')
            ? (char)(method[i] - 'a' + 'A') : method[i];
    }
    method_upper[mlen] = '\0';

    format_tg_date(signing_time, g_tg_date);
    build_credential_scope(signing_time, g_scope);
    sha256_hex((const unsigned char *)body, body_len, g_hash_hex);

    int has_body = is_body_method(method_upper);

    /* Step 1: build header values map */
    /* We collect names for sorting */
    typedef struct {
        const char *name;
        const char *value;
    } hv_entry;

    hv_entry hv[6];
    int hv_count = 0;

    hv[hv_count].name  = "x-tg-algorithm";
    hv[hv_count].value = TGV1_ALG;
    hv_count++;
    hv[hv_count].name  = "x-tg-date";
    hv[hv_count].value = g_tg_date;
    hv_count++;
    hv[hv_count].name  = "x-tg-app-id";
    /* trim spaces from app_id */
    {
        static char app_id_trimmed[256];
        const char *s = app_id;
        while (*s == ' ' || *s == '\t') s++;
        strncpy(app_id_trimmed, s, sizeof(app_id_trimmed) - 1);
        app_id_trimmed[sizeof(app_id_trimmed) - 1] = '\0';
        char *end = app_id_trimmed + strlen(app_id_trimmed) - 1;
        while (end >= app_id_trimmed && (*end == ' ' || *end == '\t')) { *end = '\0'; end--; }
        hv[hv_count].value = app_id_trimmed;
    }
    hv_count++;
    hv[hv_count].name  = "x-tg-content-sha256";
    hv[hv_count].value = g_hash_hex;
    hv_count++;

    char body_len_str_val[24];
    if (has_body) {
        hv[hv_count].name  = "content-type";
        hv[hv_count].value = "application/json";
        hv_count++;
        snprintf(body_len_str_val, sizeof(body_len_str_val), "%zu", body_len);
        hv[hv_count].name  = "content-length";
        hv[hv_count].value = body_len_str_val;
        hv_count++;
    }

    /* Step 2: sort lowercased header names */
    /* Build lowercased name array for sorting */
    typedef struct {
        int  idx;           /* index into hv[] */
        char lower[64];
    } sort_entry;

    sort_entry sorted[6];
    for (int i = 0; i < hv_count; i++) {
        sorted[i].idx = i;
        size_t nlen = strlen(hv[i].name);
        for (size_t j = 0; j < nlen && j < sizeof(sorted[i].lower) - 1; j++) {
            char c = hv[i].name[j];
            sorted[i].lower[j] = (c >= 'A' && c <= 'Z') ? (char)(c - 'A' + 'a') : c;
        }
        sorted[i].lower[nlen] = '\0';
    }

    /* Bubble sort (small N, simple) */
    for (int i = 0; i < hv_count - 1; i++) {
        for (int j = i + 1; j < hv_count; j++) {
            if (strcmp(sorted[i].lower, sorted[j].lower) > 0) {
                sort_entry tmp = sorted[i];
                sorted[i] = sorted[j];
                sorted[j] = tmp;
            }
        }
    }

    /* Build signedHeaders string */
    g_signed_headers[0] = '\0';
    for (int i = 0; i < hv_count; i++) {
        if (i > 0) strcat(g_signed_headers, ";");
        strcat(g_signed_headers, sorted[i].lower);
    }

    /* Step 3: canonical header string */
    char h_canon[2048];
    h_canon[0] = '\0';
    for (int i = 0; i < hv_count; i++) {
        if (i > 0) strcat(h_canon, "\n");
        strcat(h_canon, sorted[i].lower);
        strcat(h_canon, ":");
        /* trim value */
        {
            const char *v = hv[sorted[i].idx].value;
            while (*v == ' ' || *v == '\t') v++;
            strcat(h_canon, v);
            /* strip trailing spaces from the just-appended part */
            strip_trailing_newline(h_canon);
        }
    }

    /* Step 4: canonical URIPath */
    char uri_p[512];
    canonical_uri_path(uri_path, uri_p, sizeof(uri_p));

    /* canonical query */
    const char *q_canon = canonical_query(method_upper, raw_query);

    /* payload hash */
    char payload_hash[65];
    strncpy(payload_hash, g_hash_hex, sizeof(payload_hash) - 1);
    payload_hash[sizeof(payload_hash) - 1] = '\0';

    /* canonical request */
    char canonical_req[4096];
    snprintf(canonical_req, sizeof(canonical_req), "%s\n%s\n%s\n%s\n%s\n%s",
             method_upper, uri_p, q_canon, h_canon, g_signed_headers, payload_hash);

    /* Step 5: string-to-sign */
    char hash_canon[65];
    sha256_hex((const unsigned char *)canonical_req, strlen(canonical_req), hash_canon);

    char str_to_sign[2048];
    snprintf(str_to_sign, sizeof(str_to_sign), "%s\n%s\n%s\n%s",
             TGV1_ALG, g_tg_date, g_scope, hash_canon);

    /* Step 6: derive signing key */
    format_date_yyyymmdd(signing_time, g_date_buf);

    char secret_prefixed[512];
    snprintf(secret_prefixed, sizeof(secret_prefixed), "TGV1%s", access_secret);

    unsigned char k[32];     /* SHA-256 output = 32 bytes */
    unsigned char tmp[32];   /* temp for in-place HMAC */

    /* k1 = HMAC(date, "TGV1" + secret) */
    hmac_sha256_bytes(g_date_buf, (const unsigned char *)secret_prefixed, strlen(secret_prefixed), k);
    /* k2 = HMAC(uri_p, k1) */
    hmac_sha256_bytes(uri_p, k, 32, tmp);
    memcpy(k, tmp, 32);
    /* k3 = HMAC("tgv1_request", k2) */
    hmac_sha256_bytes(CREDENTIAL_SCOPE_SUFFIX, k, 32, k);
    (void)tmp;

    /* Step 7: compute signature */
    char sig_hex[65];
    hmac_sha256_hex(str_to_sign, k, 32, sig_hex);

    /* Step 8: build Authorization header */
    snprintf(g_auth, sizeof(g_auth),
             "%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
             TGV1_ALG, access_key, g_scope, g_signed_headers, sig_hex);

    /* Build output */
    result.count = 0;

#define ADD_HEADER(n, v) do { \
    result.entries[result.count].name = (n); \
    strncpy(result.entries[result.count].value, (v), TIRTC_MAX_VALUE_LEN - 1); \
    result.entries[result.count].value[TIRTC_MAX_VALUE_LEN - 1] = '\0'; \
    result.count++; \
} while(0)

    ADD_HEADER("X-Tg-Algorithm", TGV1_ALG);
    ADD_HEADER("X-Tg-Date", g_tg_date);
    ADD_HEADER("X-Tg-App-Id", hv[2].value); /* index 2 is always x-tg-app-id */
    ADD_HEADER("X-Tg-Content-Sha256", g_hash_hex);
    ADD_HEADER("X-Tg-Signed-Headers", g_signed_headers);
    if (has_body) {
        ADD_HEADER("Content-Type", "application/json");
        ADD_HEADER("Content-Length", body_len_str_val);
    }
    ADD_HEADER("Authorization", g_auth);

#undef ADD_HEADER

    return result;
}

/* --- static helpers --- */

static void sha256_hex(const unsigned char *data, size_t len, char out[65]) {
    unsigned char digest[SHA256_DIGEST_LENGTH];
    SHA256(data, len, digest);
    for (int i = 0; i < SHA256_DIGEST_LENGTH; i++) {
        sprintf(out + i * 2, "%02x", digest[i]);
    }
    out[64] = '\0';
}

static void hmac_sha256_bytes(const char *data, const unsigned char *key, size_t key_len, unsigned char *out) {
    unsigned int out_len = 0;
    HMAC(EVP_sha256(), key, (int)key_len,
         (const unsigned char *)data, strlen(data), out, &out_len);
}

static void hmac_sha256_hex(const char *data, const unsigned char *key, size_t key_len, char out[65]) {
    unsigned char digest[SHA256_DIGEST_LENGTH];
    hmac_sha256_bytes(data, key, key_len, digest);
    for (int i = 0; i < SHA256_DIGEST_LENGTH; i++) {
        sprintf(out + i * 2, "%02x", digest[i]);
    }
    out[64] = '\0';
}

static void build_credential_scope(time_t t, char out[32]) {
    /* t + 7 days in YYYYMMDD */
    time_t future = t + 7 * 86400;
    struct tm tm_buf;
    gmtime_r(&future, &tm_buf);
    strftime(out, 16, "%Y%m%d", &tm_buf);
    strcat(out, "/");
    strcat(out, CREDENTIAL_SCOPE_SUFFIX);
}

static void format_tg_date(time_t t, char out[17]) {
    struct tm tm_buf;
    gmtime_r(&t, &tm_buf);
    strftime(out, 17, "%Y%m%dT%H%M%SZ", &tm_buf);
}

static void format_date_yyyymmdd(time_t t, char out[17]) {
    struct tm tm_buf;
    gmtime_r(&t, &tm_buf);
    strftime(out, 16, "%Y%m%d", &tm_buf);
    out[16] = '\0';
}

static void canonical_uri_path(const char *path, char *out, size_t out_size) {
    /* trim leading spaces */
    while (*path == ' ' || *path == '\t') path++;
    size_t len = strlen(path);
    /* trim trailing spaces */
    while (len > 0 && (path[len - 1] == ' ' || path[len - 1] == '\t')) len--;

    if (len >= out_size) len = out_size - 1;
    memcpy(out, path, len);
    out[len] = '\0';

    /* strip trailing slash unless root */
    if (len > 1 && out[len - 1] == '/') {
        out[len - 1] = '\0';
    }
}

static const char *canonical_query(const char *method, const char *raw_query) {
    static char q_buf[2048];

    if (is_body_method(method)) {
        return "";
    }

    if (!raw_query) raw_query = "";

    /* strip leading '?' */
    const char *s = raw_query;
    if (*s == '?') s++;

    /* replace '+' with "%20" */
    size_t i = 0;
    while (*s && i < sizeof(q_buf) - 3) {
        if (*s == '+') {
            q_buf[i++] = '%';
            q_buf[i++] = '2';
            q_buf[i++] = '0';
        } else {
            q_buf[i++] = *s;
        }
        s++;
    }
    q_buf[i] = '\0';
    return q_buf;
}

static int is_body_method(const char *method) {
    return (strcmp(method, "POST") == 0 ||
            strcmp(method, "PUT") == 0 ||
            strcmp(method, "PATCH") == 0);
}

static void strip_trailing_newline(char *s) {
    size_t len = strlen(s);
    while (len > 0 && (s[len - 1] == ' ' || s[len - 1] == '\t')) {
        s[len - 1] = '\0';
        len--;
    }
}
