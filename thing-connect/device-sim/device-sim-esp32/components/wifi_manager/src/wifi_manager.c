#include "wifi_manager.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "cJSON.h"
#include "esp_event.h"
#include "esp_http_server.h"
#include "esp_log.h"
#include "esp_mac.h"
#include "esp_netif.h"
#include "esp_system.h"
#include "esp_wifi.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "nvs.h"

#define WIFI_NVS_NAMESPACE "wifi_cfg"
#define WIFI_NVS_SSID "ssid"
#define WIFI_NVS_PASSWORD "password"
#define WIFI_CONNECT_RETRIES 5
#define WIFI_SETUP_PASSWORD "tirtc1234"

static const char *TAG = "wifi_manager";
static bool s_started;
static volatile bool s_connected;
static volatile bool s_provisioning;
static int s_retry_count;
static char s_provisioning_ssid[33];
static httpd_handle_t s_http_server;

static const char s_setup_page[] =
    "<!doctype html><html lang=zh-CN><meta charset=utf-8>"
    "<meta name=viewport content='width=device-width,initial-scale=1'>"
    "<title>TiRTC Wi-Fi 配置</title><style>body{font-family:sans-serif;max-width:420px;"
    "margin:40px auto;padding:0 18px}input,button{box-sizing:border-box;width:100%;"
    "padding:12px;margin:7px 0;font-size:16px}#msg{white-space:pre-wrap}</style>"
    "<h2>TiRTC 设备配网</h2><p>填写设备需要连接的 Wi-Fi。</p>"
    "<input id=s placeholder='Wi-Fi 名称' maxlength=32>"
    "<input id=p type=password placeholder='Wi-Fi 密码（开放网络可留空）' maxlength=64>"
    "<button onclick=save()>保存并重启</button><p id=msg></p>"
    "<script>async function save(){let m=document.getElementById('msg');m.textContent='保存中…';"
    "try{let r=await fetch('/api/wifi',{method:'POST',headers:{'Content-Type':'application/json'},"
    "body:JSON.stringify({ssid:s.value,password:p.value})});m.textContent=await r.text()}"
    "catch(e){m.textContent='请求失败：'+e}}</script></html>";

static void set_error(char *error, size_t error_size, const char *message)
{
    if (error != NULL && error_size > 0) {
        (void)snprintf(error, error_size, "%s", message);
    }
}

bool wifi_manager_credentials_valid(const char *ssid,
                                    const char *password,
                                    char *error,
                                    size_t error_size)
{
    if (ssid == NULL || password == NULL) {
        set_error(error, error_size, "SSID/password is null");
        return false;
    }
    size_t ssid_length = strlen(ssid);
    size_t password_length = strlen(password);
    if (ssid_length == 0 || ssid_length > WIFI_MANAGER_SSID_MAX) {
        set_error(error, error_size, "SSID length must be 1..32 bytes");
        return false;
    }
    if (password_length != 0 && (password_length < 8 ||
                                 password_length > WIFI_MANAGER_PASSWORD_MAX)) {
        set_error(error, error_size, "password length must be 0 or 8..64 bytes");
        return false;
    }
    set_error(error, error_size, "");
    return true;
}

esp_err_t wifi_manager_load_credentials(wifi_manager_credentials_t *credentials)
{
    if (credentials == NULL) {
        return ESP_ERR_INVALID_ARG;
    }
    memset(credentials, 0, sizeof(*credentials));
    nvs_handle_t nvs = 0;
    esp_err_t err = nvs_open(WIFI_NVS_NAMESPACE, NVS_READONLY, &nvs);
    if (err != ESP_OK) {
        return err;
    }
    size_t ssid_size = sizeof(credentials->ssid);
    size_t password_size = sizeof(credentials->password);
    err = nvs_get_str(nvs, WIFI_NVS_SSID, credentials->ssid, &ssid_size);
    if (err == ESP_OK) {
        err = nvs_get_str(nvs, WIFI_NVS_PASSWORD, credentials->password, &password_size);
    }
    nvs_close(nvs);
    if (err != ESP_OK) {
        memset(credentials, 0, sizeof(*credentials));
    }
    return err;
}

esp_err_t wifi_manager_save_credentials(const char *ssid, const char *password)
{
    char validation_error[80];
    if (!wifi_manager_credentials_valid(ssid,
                                        password,
                                        validation_error,
                                        sizeof(validation_error))) {
        ESP_LOGE(TAG, "invalid Wi-Fi credentials: %s", validation_error);
        return ESP_ERR_INVALID_ARG;
    }

    nvs_handle_t nvs = 0;
    esp_err_t err = nvs_open(WIFI_NVS_NAMESPACE, NVS_READWRITE, &nvs);
    if (err == ESP_OK) {
        err = nvs_set_str(nvs, WIFI_NVS_SSID, ssid);
    }
    if (err == ESP_OK) {
        err = nvs_set_str(nvs, WIFI_NVS_PASSWORD, password);
    }
    if (err == ESP_OK) {
        err = nvs_commit(nvs);
    }
    if (nvs != 0) {
        nvs_close(nvs);
    }
    return err;
}

esp_err_t wifi_manager_forget_credentials(void)
{
    nvs_handle_t nvs = 0;
    esp_err_t err = nvs_open(WIFI_NVS_NAMESPACE, NVS_READWRITE, &nvs);
    if (err == ESP_OK) {
        err = nvs_erase_all(nvs);
    }
    if (err == ESP_OK) {
        err = nvs_commit(nvs);
    }
    if (nvs != 0) {
        nvs_close(nvs);
    }
    return err;
}

static void restart_task(void *argument)
{
    (void)argument;
    vTaskDelay(pdMS_TO_TICKS(1000));
    esp_restart();
}

static esp_err_t setup_page_get(httpd_req_t *request)
{
    httpd_resp_set_type(request, "text/html; charset=utf-8");
    return httpd_resp_send(request, s_setup_page, HTTPD_RESP_USE_STRLEN);
}

static esp_err_t wifi_config_post(httpd_req_t *request)
{
    if (request->content_len <= 0 || request->content_len > 512) {
        return httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "invalid request size");
    }
    char body[513];
    int received = httpd_req_recv(request, body, request->content_len);
    if (received <= 0) {
        return httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "cannot read request");
    }
    body[received] = '\0';

    cJSON *root = cJSON_ParseWithLength(body, (size_t)received);
    const cJSON *ssid = root == NULL ? NULL :
        cJSON_GetObjectItemCaseSensitive(root, "ssid");
    const cJSON *password = root == NULL ? NULL :
        cJSON_GetObjectItemCaseSensitive(root, "password");
    if (!cJSON_IsString(ssid) || !cJSON_IsString(password)) {
        cJSON_Delete(root);
        return httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "ssid/password required");
    }

    char validation_error[80];
    if (!wifi_manager_credentials_valid(ssid->valuestring,
                                        password->valuestring,
                                        validation_error,
                                        sizeof(validation_error))) {
        cJSON_Delete(root);
        return httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, validation_error);
    }
    esp_err_t err = wifi_manager_save_credentials(ssid->valuestring, password->valuestring);
    cJSON_Delete(root);
    if (err != ESP_OK) {
        return httpd_resp_send_err(request, HTTPD_500_INTERNAL_SERVER_ERROR, "NVS save failed");
    }

    httpd_resp_set_type(request, "text/plain; charset=utf-8");
    esp_err_t response = httpd_resp_sendstr(request, "保存成功，设备将在 1 秒后重启。");
    if (response == ESP_OK) {
        (void)xTaskCreate(restart_task, "wifi_restart", 2048, NULL, 3, NULL);
    }
    return response;
}

static void start_http_server(void)
{
    if (s_http_server != NULL) {
        return;
    }
    httpd_config_t config = HTTPD_DEFAULT_CONFIG();
    config.max_uri_handlers = 4;
    if (httpd_start(&s_http_server, &config) != ESP_OK) {
        s_http_server = NULL;
        ESP_LOGE(TAG, "cannot start provisioning HTTP server");
        return;
    }
    const httpd_uri_t page = {
        .uri = "/",
        .method = HTTP_GET,
        .handler = setup_page_get,
    };
    const httpd_uri_t api = {
        .uri = "/api/wifi",
        .method = HTTP_POST,
        .handler = wifi_config_post,
    };
    (void)httpd_register_uri_handler(s_http_server, &page);
    (void)httpd_register_uri_handler(s_http_server, &api);
}

static void start_provisioning(void)
{
    if (s_provisioning) {
        return;
    }
    uint8_t mac[6];
    esp_read_mac(mac, ESP_MAC_WIFI_STA);
    (void)snprintf(s_provisioning_ssid,
                   sizeof(s_provisioning_ssid),
                   "TiRTC-Setup-%02X%02X",
                   mac[4],
                   mac[5]);

    wifi_config_t ap = {0};
    size_t ap_ssid_length = strlen(s_provisioning_ssid);
    size_t ap_password_length = strlen(WIFI_SETUP_PASSWORD);
    memcpy(ap.ap.ssid, s_provisioning_ssid, ap_ssid_length);
    memcpy(ap.ap.password, WIFI_SETUP_PASSWORD, ap_password_length);
    ap.ap.ssid_len = ap_ssid_length;
    ap.ap.channel = 1;
    ap.ap.max_connection = 4;
    ap.ap.authmode = WIFI_AUTH_WPA2_PSK;

    ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_APSTA));
    ESP_ERROR_CHECK(esp_wifi_set_config(WIFI_IF_AP, &ap));
    s_provisioning = true;
    start_http_server();
    ESP_LOGW(TAG,
             "provisioning active: connect SSID=%s password=%s, then open http://192.168.4.1",
             s_provisioning_ssid,
             WIFI_SETUP_PASSWORD);
}

static void wifi_event(void *argument,
                       esp_event_base_t event_base,
                       int32_t event_id,
                       void *event_data)
{
    (void)argument;
    (void)event_data;
    if (event_base == WIFI_EVENT && event_id == WIFI_EVENT_STA_START) {
        wifi_manager_credentials_t credentials;
        if (wifi_manager_load_credentials(&credentials) == ESP_OK) {
            (void)esp_wifi_connect();
        } else {
            start_provisioning();
        }
    } else if (event_base == WIFI_EVENT && event_id == WIFI_EVENT_STA_DISCONNECTED) {
        s_connected = false;
        if (!s_provisioning && s_retry_count++ < WIFI_CONNECT_RETRIES) {
            ESP_LOGW(TAG, "Wi-Fi disconnected, retry %d/%d",
                     s_retry_count, WIFI_CONNECT_RETRIES);
            (void)esp_wifi_connect();
        } else {
            start_provisioning();
        }
    } else if (event_base == IP_EVENT && event_id == IP_EVENT_STA_GOT_IP) {
        const ip_event_got_ip_t *got_ip = event_data;
        s_connected = true;
        s_retry_count = 0;
        ESP_LOGI(TAG, "Wi-Fi connected, IP=" IPSTR, IP2STR(&got_ip->ip_info.ip));
    }
}

esp_err_t wifi_manager_start(void)
{
    if (s_started) {
        return ESP_OK;
    }
    ESP_ERROR_CHECK(esp_netif_init());
    esp_err_t err = esp_event_loop_create_default();
    if (err != ESP_OK && err != ESP_ERR_INVALID_STATE) {
        return err;
    }
    (void)esp_netif_create_default_wifi_sta();
    (void)esp_netif_create_default_wifi_ap();

    wifi_init_config_t init = WIFI_INIT_CONFIG_DEFAULT();
    err = esp_wifi_init(&init);
    if (err != ESP_OK) {
        return err;
    }
    ESP_ERROR_CHECK(esp_event_handler_register(WIFI_EVENT, ESP_EVENT_ANY_ID, wifi_event, NULL));
    ESP_ERROR_CHECK(esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP, wifi_event, NULL));

    wifi_manager_credentials_t credentials;
    if (wifi_manager_load_credentials(&credentials) == ESP_OK) {
        wifi_config_t station = {0};
        memcpy(station.sta.ssid, credentials.ssid, strlen(credentials.ssid));
        memcpy(station.sta.password, credentials.password, strlen(credentials.password));
        station.sta.threshold.authmode = credentials.password[0] == '\0'
                                             ? WIFI_AUTH_OPEN
                                             : WIFI_AUTH_WPA2_PSK;
        ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_STA));
        ESP_ERROR_CHECK(esp_wifi_set_config(WIFI_IF_STA, &station));
        ESP_LOGI(TAG, "connecting to configured SSID=%s", credentials.ssid);
    } else {
        ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_APSTA));
    }
    err = esp_wifi_start();
    if (err != ESP_OK) {
        return err;
    }
    err = esp_wifi_set_ps(WIFI_PS_NONE);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "cannot disable Wi-Fi power save: %s",
                 esp_err_to_name(err));
        return err;
    }
    ESP_LOGI(TAG, "Wi-Fi power save disabled for real-time media");
    s_started = true;
    return ESP_OK;
}

bool wifi_manager_connected(void)
{
    return s_connected;
}

bool wifi_manager_provisioning(void)
{
    return s_provisioning;
}

const char *wifi_manager_provisioning_ssid(void)
{
    return s_provisioning ? s_provisioning_ssid : "";
}
