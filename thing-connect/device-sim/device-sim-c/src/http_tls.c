#include "http_tls.h"

#include <stdio.h>

static char s_ca_cert[512];
static int s_insecure;

void http_tls_configure(const char *ca_cert, int insecure) {
    snprintf(s_ca_cert, sizeof(s_ca_cert), "%s", ca_cert ? ca_cert : "");
    s_insecure = insecure ? 1 : 0;
}

void http_tls_apply(CURL *curl) {
    if (!curl) return;
    curl_easy_setopt(curl, CURLOPT_SSL_VERIFYPEER, s_insecure ? 0L : 1L);
    curl_easy_setopt(curl, CURLOPT_SSL_VERIFYHOST, s_insecure ? 0L : 2L);
    if (!s_insecure && s_ca_cert[0])
        curl_easy_setopt(curl, CURLOPT_CAINFO, s_ca_cert);
}
