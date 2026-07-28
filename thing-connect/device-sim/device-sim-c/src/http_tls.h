#ifndef HTTP_TLS_H
#define HTTP_TLS_H

#include <curl/curl.h>

/* Configure libcurl TLS once during startup. Verification is on by default. */
void http_tls_configure(const char *ca_cert, int insecure);
void http_tls_apply(CURL *curl);

#endif
