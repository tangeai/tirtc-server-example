/**
 * @file test_tirtc_signing.c
 * @brief Test harness for TGV1-HMAC-SHA256 signing — validates against test-vectors.json.
 *
 * Build: make
 * Run:   ./test_tirtc_signing
 */

#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif

#include "tirtc_signing.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

/* Expected values extracted from test-vectors.json */
typedef struct {
    const char *description;
    const char *access_key;
    const char *access_secret;
    const char *app_id;
    const char *method;
    const char *uri_path;
    const char *body;
    const char *raw_query;
    const char *expected_sig;
    const char *expected_payload_hash;
    const char *expected_content_length;
    const char *expected_signed_headers;
    int         has_content_type;
} test_vector;

static const test_vector vectors[] = {
    {
        .description = "POST with JSON body",
        .access_key = "test-access-key-123",
        .access_secret = "test-secret-456",
        .app_id = "app-789",
        .method = "POST",
        .uri_path = "/v2/device/info",
        .body = "{\"device_id\":\"TESTDEVICE01\"}",
        .raw_query = "",
        .expected_sig = "2144f990e3b387b300f35c2222162ee10186cc35884e9b61b3194092447f3e2f",
        .expected_payload_hash = "bf5afebe060d14c053ad5ca8ae574b300b305ea382da713600df86a93508f478",
        .expected_content_length = "28",
        .expected_signed_headers = "content-length;content-type;x-tg-algorithm;x-tg-app-id;x-tg-content-sha256;x-tg-date",
        .has_content_type = 1,
    },
    {
        .description = "POST empty body",
        .access_key = "test-access-key-123",
        .access_secret = "test-secret-456",
        .app_id = "app-789",
        .method = "POST",
        .uri_path = "/v2/user/login",
        .body = "",
        .raw_query = "",
        .expected_sig = "b8b9c4f602e9e30bf4eced29ba6bdea6a7c66393f46b03eb6a2d27b3a7c200f7",
        .expected_payload_hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        .expected_content_length = "0",
        .expected_signed_headers = "content-length;content-type;x-tg-algorithm;x-tg-app-id;x-tg-content-sha256;x-tg-date",
        .has_content_type = 1,
    },
    {
        .description = "GET with query params",
        .access_key = "test-access-key-123",
        .access_secret = "test-secret-456",
        .app_id = "app-789",
        .method = "GET",
        .uri_path = "/v2/device/server/connection",
        .body = "",
        .raw_query = "device_id=TESTDEVICE01&platform=web",
        .expected_sig = "b8e12cc829768a466a1e6820099a7b1172357327e6c2a7c692c3f78a1c1a057f",
        .expected_payload_hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        .expected_content_length = NULL,
        .expected_signed_headers = "x-tg-algorithm;x-tg-app-id;x-tg-content-sha256;x-tg-date",
        .has_content_type = 0,
    },
    {
        .description = "GET with plus-in-query",
        .access_key = "test-access-key-123",
        .access_secret = "test-secret-456",
        .app_id = "app-789",
        .method = "GET",
        .uri_path = "/v2/search",
        .body = "",
        .raw_query = "q=hello+world",
        .expected_sig = "faf82b0dec7a2430b99b568192a53d042f66febf49a0f29d2244132c4dae4258",
        .expected_payload_hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        .expected_content_length = NULL,
        .expected_signed_headers = "x-tg-algorithm;x-tg-app-id;x-tg-content-sha256;x-tg-date",
        .has_content_type = 0,
    },
    {
        .description = "PUT with body",
        .access_key = "test-access-key-123",
        .access_secret = "test-secret-456",
        .app_id = "app-789",
        .method = "PUT",
        .uri_path = "/v2/device/attrs",
        .body = "{\"attrs\":{\"wakeup\":\"on\"}}",
        .raw_query = "",
        .expected_sig = "56090deedb1ffc6db164a21d4e75a383e1c3f1a55cd273a852167c0e282e96a1",
        .expected_payload_hash = "e404b959f8173b9a773e13a2792d2b039f26307c1305ab75d2453f20c328153f",
        .expected_content_length = "25",
        .expected_signed_headers = "content-length;content-type;x-tg-algorithm;x-tg-app-id;x-tg-content-sha256;x-tg-date",
        .has_content_type = 1,
    },
    {
        .description = "DELETE without body",
        .access_key = "test-access-key-123",
        .access_secret = "test-secret-456",
        .app_id = "app-789",
        .method = "DELETE",
        .uri_path = "/v2/user/12345",
        .body = "",
        .raw_query = "",
        .expected_sig = "62e5e2d93d5f61b880c7bea4eb5a3931f0ed465b069382ff31fe4eef82677d5a",
        .expected_payload_hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        .expected_content_length = NULL,
        .expected_signed_headers = "x-tg-algorithm;x-tg-app-id;x-tg-content-sha256;x-tg-date",
        .has_content_type = 0,
    },
    {
        .description = "URI trailing slash normalized",
        .access_key = "test-access-key-123",
        .access_secret = "test-secret-456",
        .app_id = "app-789",
        .method = "POST",
        .uri_path = "/v2/device/info/",
        .body = "{\"device_id\":\"TESTDEVICE01\"}",
        .raw_query = "",
        .expected_sig = "2144f990e3b387b300f35c2222162ee10186cc35884e9b61b3194092447f3e2f",
        .expected_payload_hash = "bf5afebe060d14c053ad5ca8ae574b300b305ea382da713600df86a93508f478",
        .expected_content_length = "28",
        .expected_signed_headers = "content-length;content-type;x-tg-algorithm;x-tg-app-id;x-tg-content-sha256;x-tg-date",
        .has_content_type = 1,
    },
    {
        .description = "root path /",
        .access_key = "test-access-key-123",
        .access_secret = "test-secret-456",
        .app_id = "app-789",
        .method = "GET",
        .uri_path = "/",
        .body = "",
        .raw_query = "action=ping",
        .expected_sig = "67a9a490b68813c8df7a743325ae5336fbbd979a3fafa929620a34da7861f218",
        .expected_payload_hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        .expected_content_length = NULL,
        .expected_signed_headers = "x-tg-algorithm;x-tg-app-id;x-tg-content-sha256;x-tg-date",
        .has_content_type = 0,
    },
};

static const int num_vectors = sizeof(vectors) / sizeof(vectors[0]);

static int find_header(const TirtcHeaders *h, const char *name) {
    for (size_t i = 0; i < h->count; i++) {
        if (strcasecmp(h->entries[i].name, name) == 0) return (int)i;
    }
    return -1;
}

static const char *get_header(const TirtcHeaders *h, const char *name) {
    int idx = find_header(h, name);
    return idx >= 0 ? h->entries[idx].value : NULL;
}

static const char *extract_signature(const char *auth) {
    const char *p = strstr(auth, "Signature=");
    return p ? p + 10 : "";
}

int main(void) {
    /* Fixed signing time: 2024-01-15T12:00:00Z = 1705320000 */
    struct tm tm_sign = {0};
    tm_sign.tm_year = 2024 - 1900;
    tm_sign.tm_mon = 0; /* January */
    tm_sign.tm_mday = 15;
    tm_sign.tm_hour = 12;
    tm_sign.tm_min = 0;
    tm_sign.tm_sec = 0;
    time_t signing = timegm(&tm_sign);

    int passed = 0;
    int failed = 0;

    /* Basic tests */
    {
        TirtcHeaders h1 = tirtc_sign_request("access123", "secret", "app456", "POST",
            "/v1/token/wxvoip", "{\"k\":\"v\"}", (size_t)-1, "", signing);
        TirtcHeaders h2 = tirtc_sign_request("access123", "secret", "app456", "POST",
            "/v1/token/wxvoip", "{\"k\":\"v\"}", (size_t)-1, "", signing);

        const char *a1 = get_header(&h1, "Authorization");
        const char *a2 = get_header(&h2, "Authorization");
        if (a1 && a2 && strcmp(a1, a2) == 0) { passed++; }
        else { printf("FAIL: Deterministic\n"); failed++; }
    }

    {
        TirtcHeaders h1 = tirtc_sign_request("access", "secret1", "app", "POST",
            "/path", "body", (size_t)-1, "", signing);
        TirtcHeaders h2 = tirtc_sign_request("access", "secret2", "app", "POST",
            "/path", "body", (size_t)-1, "", signing);
        const char *a1 = get_header(&h1, "Authorization");
        const char *a2 = get_header(&h2, "Authorization");
        if (a1 && a2 && strcmp(a1, a2) != 0) { passed++; }
        else { printf("FAIL: Different secrets\n"); failed++; }
    }

    {
        TirtcHeaders h = tirtc_sign_request("access", "secret", "app", "POST",
            "/v1/token/wxvoip", "{}", (size_t)-1, "", signing);
        const char *a = get_header(&h, "Authorization");
        if (a && strncmp(a, tirtc_algorithm(), strlen(tirtc_algorithm())) == 0) { passed++; }
        else { printf("FAIL: Contains algorithm\n"); failed++; }
    }

    {
        TirtcHeaders h = tirtc_sign_request("access", "secret", "app", "GET",
            "/v1/devices", "", 0, "status=online", signing);
        if (find_header(&h, "Content-Type") < 0 && find_header(&h, "Content-Length") < 0) {
            passed++;
        } else {
            printf("FAIL: GET no content type\n"); failed++;
        }
    }

    {
        TirtcHeaders h1 = tirtc_sign_request("access", "secret", "app", "POST",
            "/path/", "{}", (size_t)-1, "", signing);
        TirtcHeaders h2 = tirtc_sign_request("access", "secret", "app", "POST",
            "/path", "{}", (size_t)-1, "", signing);
        const char *a1 = get_header(&h1, "Authorization");
        const char *a2 = get_header(&h2, "Authorization");
        if (a1 && a2 && strcmp(a1, a2) == 0) { passed++; }
        else { printf("FAIL: URI trailing slash\n"); failed++; }
    }

    /* Cross-language test vectors */
    for (int i = 0; i < num_vectors; i++) {
        const test_vector *v = &vectors[i];
        TirtcHeaders h = tirtc_sign_request(
            v->access_key, v->access_secret, v->app_id, v->method,
            v->uri_path, v->body, (size_t)-1, v->raw_query, signing);

        const char *sig = extract_signature(get_header(&h, "Authorization"));
        if (!sig || strcmp(sig, v->expected_sig) != 0) {
            printf("FAIL: [%s] Signature mismatch\n  expected: %s\n  actual:   %s\n",
                   v->description, v->expected_sig, sig ? sig : "(null)");
            failed++; continue;
        }

        const char *hash = get_header(&h, "X-Tg-Content-Sha256");
        if (!hash || strcmp(hash, v->expected_payload_hash) != 0) {
            printf("FAIL: [%s] Payload hash mismatch\n  expected: %s\n  actual:   %s\n",
                   v->description, v->expected_payload_hash, hash ? hash : "(null)");
            failed++; continue;
        }

        const char *sh = get_header(&h, "X-Tg-Signed-Headers");
        if (!sh || strcmp(sh, v->expected_signed_headers) != 0) {
            printf("FAIL: [%s] SignedHeaders mismatch\n  expected: %s\n  actual:   %s\n",
                   v->description, v->expected_signed_headers, sh ? sh : "(null)");
            failed++; continue;
        }

        if (v->has_content_type) {
            const char *cl = get_header(&h, "Content-Length");
            if (!cl || strcmp(cl, v->expected_content_length) != 0) {
                printf("FAIL: [%s] Content-Length mismatch\n  expected: %s\n  actual:   %s\n",
                       v->description, v->expected_content_length, cl ? cl : "(null)");
                failed++; continue;
            }
        } else {
            if (find_header(&h, "Content-Type") >= 0) {
                printf("FAIL: [%s] Should not have Content-Type\n", v->description);
                failed++; continue;
            }
        }

        printf("OK: %s\n", v->description);
        passed++;
    }

    printf("\n=== %d passed, %d failed ===\n", passed, failed);
    return failed > 0 ? 1 : 0;
}
