/* Minimal config for using mbedTLS HMAC-SHA256 + Base64 from libTiRTC.so.
 * Symbols are provided by libTiRTC.so (mbedTLS 3.2.1 built-in).
 * Only enable what device_flow.c actually uses. */
#define MBEDTLS_MD_C
#define MBEDTLS_SHA256_C
#define MBEDTLS_BASE64_C
#define MBEDTLS_CONFIG_VERSION 0x03020100
