#ifndef RUNTIME_CONFIG_H
#define RUNTIME_CONFIG_H

#include <stdbool.h>
#include <stddef.h>

#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
    char device_id[65];
    char device_secret[257];
    char client_id[65];
} runtime_tirtc_config_t;

esp_err_t runtime_config_load_tirtc(runtime_tirtc_config_t *config);
esp_err_t runtime_config_save_tirtc(const runtime_tirtc_config_t *config);
esp_err_t runtime_config_clear_tirtc(void);
bool runtime_config_tirtc_valid(const runtime_tirtc_config_t *config,
                                char *error,
                                size_t error_size);

#ifdef __cplusplus
}
#endif

#endif
