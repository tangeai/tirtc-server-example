/**
 * Product adapter starting point for the Linux C reference executable.
 *
 * This file deliberately keeps every stock Linux demo operation active. Copy
 * it into the product tree and replace one complete operation group at a time.
 * Never publish a build while a required product TODO still falls through to
 * JSON credentials, file media, stdin interaction, or the demo security rule.
 */
#include "device_adapter.h"
#include "linux_device_adapter.h"

/* The reference main() provides a weak function with this name. Linking this
 * object supplies the strong product implementation without editing main.c. */
int device_product_adapter_install(void) {
    DeviceAdapterV1 adapter;
    if (linux_device_adapter_build(&adapter) != 0) return -1;

    /* TODO 1 — platform adaptation
     * Replace adapter.platform when product time/sleep services differ from
     * POSIX clock_gettime/nanosleep. Network readiness, daemon supervision and
     * logging remain application integrations outside this small table. */

    /* TODO 2 — device identity
     * Replace adapter.identity as one group. load/save/clear must implement
     * atomic persistence, power-loss behavior, unbind and factory-reset policy.
     * Keep the context object alive until process shutdown. */

    /* TODO 3 and 4 — real uplink audio/video
     * Replace adapter.media_source as one group. open receives the business and
     * negotiated format names. next_audio/next_video return encoded complete
     * frames and must obey the packet lifetime documented in device_adapter.h. */

    /* TODO 5 and 6 — downlink playback/video display
     * Set adapter.media_sink.is_enabled, submit and flush. submit runs on an SDK callback:
     * copy into a bounded product queue and return immediately. The playback or
     * display worker owns decoding and hardware I/O. flush removes the stopped
     * generation so an old call cannot play after a new call starts. */

    /* TODO 7 — product interaction
     * Replace adapter.product. poll_action converts keys/UI/application events
     * to DeviceProductAction; notify reflects incoming/starting/started/
     * stopping/stopped/failed states back to the UI. */

    /* TODO 8 — resource arbitration
     * Replace adapter.resource. acquire atomically reserves the full microphone,
     * speaker, camera, codec, display and memory set or rolls it all back.
     * release must be idempotent. */

    /* TODO 9 — exception recovery
     * Set adapter.recovery.report and route each domain into the product's
     * bounded-retry state machine, watchdog and observability path. Reporting
     * an error is not itself a retry policy. */

    /* TODO 10 — security and production
     * Replace adapter.security.random_bytes with the product-approved CSPRNG and
     * make allow_insecure_transport return false in production. Also replace
     * the stock identity store above and complete signing, OTA, privilege,
     * secret-redaction and production-test requirements outside this table. */

    return device_adapter_install(&adapter);
}
