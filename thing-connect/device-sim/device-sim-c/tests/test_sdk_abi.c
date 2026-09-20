#include <stddef.h>
#include <stdio.h>
#include <string.h>

#include <tirtc/tiRTC.h>

#ifndef EXPECTED_SDK_VERSION
#error "EXPECTED_SDK_VERSION must be supplied by the Makefile"
#endif

_Static_assert(TIRTC_VERSION_MAJOR == EXPECTED_SDK_VERSION_MAJOR,
               "TiRTC header major version does not match SDK_VERSION");
_Static_assert(TIRTC_VERSION_MINOR == EXPECTED_SDK_VERSION_MINOR,
               "TiRTC header minor version does not match SDK_VERSION");
_Static_assert(TIRTC_VERSION_PATCH == EXPECTED_SDK_VERSION_PATCH,
               "TiRTC header patch version does not match SDK_VERSION");
_Static_assert(offsetof(TIRTCCALLBACKS, on_update_bitrate) > 0,
               "TiRTC callback ABI is missing on_update_bitrate");
#if EXPECTED_SDK_VERSION_MAJOR == 2 && EXPECTED_SDK_VERSION_MINOR >= 5
_Static_assert(offsetof(TIRTCCALLBACKS, on_sleep_wakeup_info) ==
                   14 * sizeof(void *),
               "TiRTC 2.5 callback ABI is missing on_sleep_wakeup_info");
_Static_assert(sizeof(TIRTCCALLBACKS) == 15 * sizeof(void *),
               "TiRTC 2.5 callback ABI has unexpected size");
#endif

int main(void) {
    const char *runtime_version = TiRtcGetVersion();
    const char *build_info = TiRtcGetBuildInfo();
    const char expected_version[] = "v" EXPECTED_SDK_VERSION;
    TIRTCCALLBACKS callbacks = {0};

    if (!runtime_version || strcmp(runtime_version, expected_version) != 0) {
        fprintf(stderr, "TiRTC SDK mismatch: expected %s, loaded %s\n",
                expected_version,
                runtime_version ? runtime_version : "<null>");
        return 1;
    }
    if (!build_info || build_info[0] == '\0') {
        fprintf(stderr, "TiRTC SDK build information is empty\n");
        return 1;
    }
    if (callbacks.on_update_bitrate != NULL) {
        fprintf(stderr, "zero-initialized bitrate callback is not NULL\n");
        return 1;
    }
#if EXPECTED_SDK_VERSION_MAJOR == 2 && EXPECTED_SDK_VERSION_MINOR >= 5
    if (callbacks.on_sleep_wakeup_info != NULL) {
        fprintf(stderr, "zero-initialized sleep callback is not NULL\n");
        return 1;
    }
#endif

    printf("TiRTC SDK ABI OK: %s\n", runtime_version);
    return 0;
}
