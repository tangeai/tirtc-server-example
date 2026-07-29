#ifndef PLATFORM_CLIENT_H
#define PLATFORM_CLIENT_H

#include <stdbool.h>
#include <stddef.h>

#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef enum {
    PLATFORM_SERVICE_DEVICE = 0,
    PLATFORM_SERVICE_VOIP,
    PLATFORM_SERVICE_AI,
    PLATFORM_SERVICE_CALL,
} platform_service_t;

typedef struct {
    const char *device_id;
    const char *device_secret;
    const char *client_id;
    const char *mac_address;
    const char *discovery_url;
} platform_client_config_t;

typedef struct {
    const char *mac_address;
    const char *existing_device_id;
    const char *existing_device_secret;
    const char *discovery_url;
    unsigned timeout_seconds;
} platform_provision_config_t;

typedef struct {
    char device_id[65];
    char device_secret[257];
} platform_provision_result_t;

typedef void (*platform_response_callback_t)(const char *body, void *user_data);
typedef void (*platform_signal_callback_t)(const char *json,
                                           size_t length,
                                           void *user_data);

/* Starts service discovery, signed device login, the request worker and MQTT.
 * This function performs network I/O and must run outside app_main. */
esp_err_t platform_client_start(const platform_client_config_t *config);

/* First-boot / rebind flow: report the device fingerprint, print the
 * verification code, wait for auth_grant over temporary MQTT, ACK it, and
 * return the credentials that the caller must persist. Existing credentials
 * are optional and enable a signed report after server-side unbind. */
esp_err_t platform_client_provision(const platform_provision_config_t *config,
                                    platform_provision_result_t *result);
bool platform_client_ready(void);
bool platform_client_mqtt_connected(void);
bool platform_client_provisioning(void);
const char *platform_client_verification_code(void);
const char *platform_client_tirtc_endpoint(void);

/* GET when json_body is NULL, POST otherwise. The callback runs in the
 * platform request task; response text is only valid during the callback. */
esp_err_t platform_client_request(platform_service_t service,
                                  const char *path,
                                  const char *json_body,
                                  platform_response_callback_t callback,
                                  void *user_data);
esp_err_t platform_client_request_timeout(platform_service_t service,
                                          const char *path,
                                          const char *json_body,
                                          unsigned timeout_ms,
                                          platform_response_callback_t callback,
                                          void *user_data);

void platform_client_set_signal_handler(platform_signal_callback_t callback,
                                        void *user_data);

#ifdef __cplusplus
}
#endif

#endif
