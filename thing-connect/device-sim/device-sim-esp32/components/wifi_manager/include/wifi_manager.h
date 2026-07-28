#ifndef WIFI_MANAGER_H
#define WIFI_MANAGER_H

#include <stdbool.h>
#include <stddef.h>

#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

#define WIFI_MANAGER_SSID_MAX 32
#define WIFI_MANAGER_PASSWORD_MAX 64

typedef struct {
    char ssid[WIFI_MANAGER_SSID_MAX + 1];
    char password[WIFI_MANAGER_PASSWORD_MAX + 1];
} wifi_manager_credentials_t;

esp_err_t wifi_manager_start(void);
bool wifi_manager_connected(void);
bool wifi_manager_provisioning(void);
const char *wifi_manager_provisioning_ssid(void);

esp_err_t wifi_manager_load_credentials(wifi_manager_credentials_t *credentials);
esp_err_t wifi_manager_save_credentials(const char *ssid, const char *password);
esp_err_t wifi_manager_forget_credentials(void);
bool wifi_manager_credentials_valid(const char *ssid,
                                    const char *password,
                                    char *error,
                                    size_t error_size);

#ifdef __cplusplus
}
#endif

#endif
