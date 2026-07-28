#include "session_runtime.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "cJSON.h"
#include "esp_log.h"
#include "esp_random.h"
#include "esp_system.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"
#include "media_runtime.h"
#include "platform_client.h"
#include "runtime_config.h"
#include "tirtc_adapter.h"

#define SESSION_QUEUE_DEPTH 4
#define SESSION_ARGUMENT_MAX 1024
#define SESSION_PAYLOAD_MAX 4096
#define CMD_VOIP_ACCEPT 0x2000U
#define CMD_VOIP_HANGUP 0x2001U
#define CMD_AI 0x2100U

typedef enum {
    EVENT_CONNECTION = 0,
    EVENT_COMMAND,
    EVENT_AI_TOKEN,
    EVENT_AI_PRESS,
    EVENT_AI_RELEASE,
    EVENT_PLATFORM_SIGNAL,
    EVENT_VOIP_CALL_DEFAULT,
    EVENT_VOIP_CALLERS_RESPONSE,
    EVENT_VOIP_DIAL_RESPONSE,
    EVENT_VOIP_CONNECT,
    EVENT_CONTACTS,
    EVENT_CONTACTS_RESPONSE,
    EVENT_CALL_REQUEST_RESPONSE,
    EVENT_ROOM_RESPONSE,
    EVENT_ACCEPT_RESPONSE,
    EVENT_CALL_DEFAULT,
    EVENT_DEVICE_CALL,
    EVENT_ACCEPT,
    EVENT_REJECT,
    EVENT_CANCEL,
    EVENT_HANGUP,
} session_event_type_t;

typedef struct {
    session_event_type_t type;
    bool connected;
    bool incoming;
    uint32_t command;
    uint32_t length;
    char first[SESSION_ARGUMENT_MAX];
    char second[SESSION_ARGUMENT_MAX];
    char payload[SESSION_PAYLOAD_MAX];
} session_event_t;

static const char *TAG = "session_runtime";
static QueueHandle_t s_queue;
static TaskHandle_t s_task;
static volatile device_session_state_t s_state = DEVICE_SESSION_OFFLINE;
static volatile device_service_t s_service = DEVICE_SERVICE_H5;
static int64_t s_ai_start_at_ms;
static char s_ai_role_id[65];
static char s_call_room_id[129];
static char s_call_peer_id[65];
static bool s_call_after_contacts;
static bool s_room_request_pending;
static bool s_ignore_next_disconnect;
static int64_t s_next_room_poll_ms;
static int64_t s_call_timeout_at_ms;
static bool s_voip_profile_submitted;
static char s_voip_peer_id[SESSION_ARGUMENT_MAX];
static char s_voip_token[SESSION_ARGUMENT_MAX];
static char s_voip_room_id[129];
static char s_voip_open_id[129];
static char s_voip_call_id[65];
static char s_voip_app_id[65];
static char s_voip_model_id[65];
static char s_voip_session_token[257];
static char s_voip_payload[513];
static char s_voip_cancelled_open_id[129];
static char s_voip_cancelled_call_id[65];
static int64_t s_voip_cancelled_until_ms;

static int64_t now_ms(void)
{
    return esp_timer_get_time() / 1000;
}

static bool queue_event(session_event_t *event)
{
    return s_queue != NULL && xQueueSend(s_queue, &event, 0) == pdTRUE;
}

static void adapter_connection_changed(bool connected, bool incoming, void *user_data)
{
    (void)user_data;
    session_event_t *event = calloc(1, sizeof(*event));
    if (event == NULL) {
        ESP_LOGW(TAG, "cannot allocate connection event");
        return;
    }
    event->type = EVENT_CONNECTION;
    event->connected = connected;
    event->incoming = incoming;
    if (!queue_event(event)) {
        ESP_LOGW(TAG, "dropping connection event because session queue is full");
        free(event);
    }
}

static void adapter_command(uint32_t command,
                            const void *data,
                            uint32_t length,
                            void *user_data)
{
    (void)user_data;
    session_event_t *event = calloc(1, sizeof(*event));
    if (event == NULL) {
        ESP_LOGW(TAG, "cannot allocate command event");
        return;
    }
    event->type = EVENT_COMMAND;
    event->command = command;
    if (data != NULL && length > 0) {
        event->length = length < sizeof(event->payload) - 1U
                            ? length
                            : sizeof(event->payload) - 1U;
        memcpy(event->payload, data, event->length);
        event->payload[event->length] = '\0';
    }
    if (!queue_event(event)) {
        ESP_LOGW(TAG, "dropping command event because session queue is full");
        free(event);
    }
}

static void platform_signal(const char *json, size_t length, void *user_data)
{
    (void)user_data;
    session_event_t *event = calloc(1, sizeof(*event));
    if (event == NULL) {
        ESP_LOGW(TAG, "cannot allocate platform signal event");
        return;
    }
    event->type = EVENT_PLATFORM_SIGNAL;
    if (json != NULL && length > 0) {
        event->length = length < sizeof(event->payload) - 1U
                            ? (uint32_t)length
                            : sizeof(event->payload) - 1U;
        memcpy(event->payload, json, event->length);
        event->payload[event->length] = '\0';
    }
    if (!queue_event(event)) {
        ESP_LOGW(TAG, "dropping platform signal because session queue is full");
        free(event);
    }
}

static void ai_token_response(const char *body, void *user_data)
{
    (void)user_data;
    session_event_t *event = calloc(1, sizeof(*event));
    if (event == NULL) {
        ESP_LOGW(TAG, "cannot allocate AI token event");
        return;
    }
    event->type = EVENT_AI_TOKEN;
    if (body != NULL) {
        (void)snprintf(event->payload, sizeof(event->payload), "%s", body);
    }
    if (!queue_event(event)) {
        ESP_LOGW(TAG, "dropping AI token response because session queue is full");
        free(event);
    }
}

static void service_event_response(const char *body, void *user_data)
{
    session_event_type_t type = (session_event_type_t)(uintptr_t)user_data;
    if (type == EVENT_ROOM_RESPONSE) {
        s_room_request_pending = false;
    }
    session_event_t *event = calloc(1, sizeof(*event));
    if (event == NULL) {
        ESP_LOGW(TAG, "cannot allocate service response event");
        return;
    }
    event->type = type;
    if (body != NULL) {
        (void)snprintf(event->payload, sizeof(event->payload), "%s", body);
    }
    if (!queue_event(event)) {
        ESP_LOGW(TAG, "dropping service response because session queue is full");
        free(event);
    }
}

static void service_log_response(const char *body, void *user_data)
{
    const char *operation = user_data == NULL ? "operation" : (const char *)user_data;
    if (body == NULL || body[0] == '\0') {
        ESP_LOGW(TAG, "%s returned no response body", operation);
        return;
    }
    cJSON *root = cJSON_Parse(body);
    const cJSON *code = root == NULL ? NULL : cJSON_GetObjectItemCaseSensitive(root, "code");
    if (!cJSON_IsNumber(code) || (code->valueint != 0 && code->valueint != 200)) {
        ESP_LOGW(TAG, "%s was not acknowledged", operation);
    }
    cJSON_Delete(root);
}

static bool response_data(const char *body, cJSON **root_out, const cJSON **data_out)
{
    cJSON *root = body == NULL ? NULL : cJSON_Parse(body);
    const cJSON *code = root == NULL ? NULL : cJSON_GetObjectItemCaseSensitive(root, "code");
    const cJSON *data = root == NULL ? NULL : cJSON_GetObjectItemCaseSensitive(root, "data");
    bool ok = cJSON_IsNumber(code) && (code->valueint == 0 || code->valueint == 200);
    if (!ok) {
        cJSON_Delete(root);
        return false;
    }
    *root_out = root;
    *data_out = data;
    return true;
}

static int submit_service_event(session_event_type_t response_event,
                                platform_service_t service,
                                const char *path,
                                const char *json_body)
{
    return platform_client_request(service,
                                   path,
                                   json_body,
                                   service_event_response,
                                   (void *)(uintptr_t)response_event);
}

static int submit_room_action_for(const char *path,
                                  const char *room_id,
                                  const char *reason)
{
    if (room_id == NULL || room_id[0] == '\0') {
        return -1;
    }
    cJSON *root = cJSON_CreateObject();
    if (root == NULL || !cJSON_AddStringToObject(root, "room_id", room_id) ||
        (reason != NULL && !cJSON_AddStringToObject(root, "reason", reason))) {
        cJSON_Delete(root);
        return -1;
    }
    char *body = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (body == NULL) {
        return -1;
    }
    int rc = platform_client_request(PLATFORM_SERVICE_CALL,
                                     path,
                                     body,
                                     service_log_response,
                                     (void *)path);
    free(body);
    return rc;
}

static int submit_room_action(const char *path, const char *reason)
{
    return submit_room_action_for(path, s_call_room_id, reason);
}

static void set_state(device_session_state_t state, device_service_t service)
{
    ESP_LOGI(TAG,
             "state %s -> %s service=%s",
             device_session_state_name(s_state),
             device_session_state_name(state),
             device_service_name(service));
    s_state = state;
    s_service = service;
    const device_media_config_t *media = media_runtime_config();
    const bool call_video = media != NULL && media->video.downlink_enabled &&
                            (service == DEVICE_SERVICE_VOIP ||
                             service == DEVICE_SERVICE_CALL);
    tirtc_adapter_set_downlink_video_enabled(call_video);
}

static void finish_session(void)
{
    media_runtime_set_uplink_active(false);
    // disconnect also invalidates a WHIP/P2P connection request whose callback
    // has not arrived yet, preventing a cancelled room from being resurrected.
    (void)tirtc_adapter_disconnect();
    s_ai_start_at_ms = 0;
    s_call_timeout_at_ms = 0;
    s_call_room_id[0] = '\0';
    s_call_peer_id[0] = '\0';
    s_voip_peer_id[0] = '\0';
    s_voip_token[0] = '\0';
    s_voip_room_id[0] = '\0';
    s_voip_open_id[0] = '\0';
    s_voip_call_id[0] = '\0';
    s_voip_app_id[0] = '\0';
    s_voip_model_id[0] = '\0';
    s_voip_session_token[0] = '\0';
    s_voip_payload[0] = '\0';
    set_state(DEVICE_SESSION_IDLE, DEVICE_SERVICE_H5);
}

static bool copy_json_string(const cJSON *object,
                             const char *name,
                             char *destination,
                             size_t destination_size)
{
    const cJSON *item = cJSON_GetObjectItemCaseSensitive(object, name);
    if (!cJSON_IsString(item) || item->valuestring == NULL ||
        item->valuestring[0] == '\0' || strlen(item->valuestring) >= destination_size) {
        return false;
    }
    (void)snprintf(destination, destination_size, "%s", item->valuestring);
    return true;
}

static void copy_optional_json_string(const cJSON *object,
                                      const char *name,
                                      char *destination,
                                      size_t destination_size)
{
    const cJSON *item = cJSON_GetObjectItemCaseSensitive(object, name);
    destination[0] = '\0';
    if (cJSON_IsString(item) && item->valuestring != NULL) {
        (void)snprintf(destination, destination_size, "%.*s",
                       (int)destination_size - 1, item->valuestring);
    }
}

static void reject_voip_signal(int reason)
{
    if (s_voip_app_id[0] == '\0' || s_voip_model_id[0] == '\0' ||
        s_voip_room_id[0] == '\0') {
        return;
    }
    cJSON *root = cJSON_CreateObject();
    bool ok = root != NULL &&
              cJSON_AddStringToObject(root, "wx_app_id", s_voip_app_id) &&
              cJSON_AddStringToObject(root, "wx_model_id", s_voip_model_id) &&
              cJSON_AddStringToObject(root, "wx_session_token", s_voip_session_token) &&
              cJSON_AddStringToObject(root, "wx_room_id", s_voip_room_id) &&
              cJSON_AddStringToObject(root, "wx_payload", s_voip_payload) &&
              cJSON_AddNumberToObject(root, "hangup_reason", reason);
    char *body = ok ? cJSON_PrintUnformatted(root) : NULL;
    cJSON_Delete(root);
    if (body == NULL) {
        ESP_LOGW(TAG, "cannot build VoIP reject request");
        return;
    }
    int rc = tirtc_adapter_service_request("/v1/wxvoip/reject",
                                           body,
                                           NULL,
                                           service_log_response,
                                           (void *)"voip reject");
    free(body);
    if (rc != 0) {
        ESP_LOGW(TAG, "VoIP reject submission failed rc=%d", rc);
    }
}

static void handle_platform_signal(const session_event_t *event)
{
    cJSON *root = cJSON_ParseWithLength(event->payload, event->length);
    const cJSON *type = root == NULL
                            ? NULL
                            : cJSON_GetObjectItemCaseSensitive(root, "type");
    const cJSON *channel = root == NULL
                               ? NULL
                               : cJSON_GetObjectItemCaseSensitive(root, "channel");
    const cJSON *payload = root == NULL
                               ? NULL
                               : cJSON_GetObjectItemCaseSensitive(root, "payload");
    const char *type_name = cJSON_IsString(type) ? type->valuestring : "";
    const char *channel_name = cJSON_IsString(channel) ? channel->valuestring : "";
    if (strcmp(type_name, "unbind") == 0) {
        ESP_LOGW(TAG,
                 "device unbound; clearing NVS credentials and restarting verification binding");
        finish_session();
        cJSON_Delete(root);
        esp_err_t err = runtime_config_clear_tirtc();
        if (err != ESP_OK) {
            ESP_LOGE(TAG, "cannot clear binding credentials: %s", esp_err_to_name(err));
            return;
        }
        vTaskDelay(pdMS_TO_TICKS(500));
        esp_restart();
        return;
    }
    if (!cJSON_IsObject(payload)) {
        cJSON_Delete(root);
        return;
    }

    if (strcmp(channel_name, "device") == 0 &&
        strcmp(type_name, "call_incoming") == 0) {
        char room_id[sizeof(s_call_room_id)] = {0};
        char caller_id[sizeof(s_call_peer_id)] = {0};
        bool valid = copy_json_string(payload, "room_id", room_id, sizeof(room_id)) &&
                     copy_json_string(payload, "caller_id", caller_id,
                                      sizeof(caller_id));
        if (valid && (s_state == DEVICE_SESSION_IDLE ||
                      s_state == DEVICE_SESSION_H5_STREAMING)) {
            if (tirtc_adapter_has_connection()) {
                media_runtime_set_uplink_active(false);
                s_ignore_next_disconnect = true;
                (void)tirtc_adapter_disconnect();
            }
            (void)snprintf(s_call_room_id, sizeof(s_call_room_id), "%s", room_id);
            (void)snprintf(s_call_peer_id, sizeof(s_call_peer_id), "%s", caller_id);
            set_state(DEVICE_SESSION_RINGING, DEVICE_SERVICE_CALL);
            ESP_LOGI(TAG, "incoming device call from=%s room=%s; use accept or reject",
                     caller_id, room_id);
        } else if (valid) {
            ESP_LOGW(TAG, "device call arrived while busy; room=%s", room_id);
            (void)submit_room_action_for("/v1/call/reject", room_id, "busy");
        }
    } else if (strcmp(channel_name, "device") == 0 &&
               (strcmp(type_name, "room_cancel") == 0 ||
                strcmp(type_name, "call_reject") == 0)) {
        char room_id[sizeof(s_call_room_id)] = {0};
        (void)copy_json_string(payload, "room_id", room_id, sizeof(room_id));
        if (s_service == DEVICE_SERVICE_CALL && s_call_room_id[0] != '\0' &&
            strcmp(room_id, s_call_room_id) == 0) {
            ESP_LOGI(TAG, "device call ended by platform signal room=%s", room_id);
            finish_session();
        }
    } else if (strcmp(channel_name, "wx") == 0 &&
               strcmp(type_name, "call_incoming") == 0) {
        char peer_id[sizeof(s_voip_peer_id)] = {0};
        char token[sizeof(s_voip_token)] = {0};
        char open_id[sizeof(s_voip_open_id)] = {0};
        char call_id[sizeof(s_voip_call_id)] = {0};
        char wx_from[65] = {0};
        bool valid = copy_json_string(payload, "peer_id", peer_id, sizeof(peer_id)) &&
                     copy_json_string(payload, "token", token, sizeof(token));
        copy_optional_json_string(payload, "wx_user_openid", open_id, sizeof(open_id));
        copy_optional_json_string(payload, "wx_call_id", call_id, sizeof(call_id));
        copy_optional_json_string(payload, "wx_from", wx_from, sizeof(wx_from));

        bool cancelled = now_ms() < s_voip_cancelled_until_ms &&
                         (s_voip_cancelled_call_id[0] != '\0'
                              ? (call_id[0] != '\0' &&
                                 strcmp(call_id, s_voip_cancelled_call_id) == 0)
                              : (s_voip_cancelled_open_id[0] != '\0' &&
                                 strcmp(open_id, s_voip_cancelled_open_id) == 0));
        bool open_id_matches = open_id[0] == '\0' ||
                               strcmp(open_id, s_voip_open_id) == 0;
        runtime_tirtc_config_t runtime = {0};
        bool from_this_device =
            wx_from[0] != '\0' &&
            runtime_config_load_tirtc(&runtime) == ESP_OK &&
            strcmp(wx_from, runtime.device_id) == 0;
        bool outgoing = s_service == DEVICE_SERVICE_VOIP &&
                        s_state == DEVICE_SESSION_CALLING &&
                        (s_voip_call_id[0] != '\0'
                             ? (call_id[0] != '\0' &&
                                strcmp(call_id, s_voip_call_id) == 0 &&
                                open_id_matches)
                             : (call_id[0] != '\0'
                                    ? (from_this_device && open_id_matches)
                                    : open_id_matches));
        bool recover_outgoing =
            (s_state == DEVICE_SESSION_IDLE ||
             s_state == DEVICE_SESSION_H5_STREAMING) &&
            call_id[0] != '\0' && from_this_device;
        (void)snprintf(s_voip_peer_id, sizeof(s_voip_peer_id), "%s", peer_id);
        (void)snprintf(s_voip_token, sizeof(s_voip_token), "%s", token);
        (void)snprintf(s_voip_open_id, sizeof(s_voip_open_id), "%s", open_id);
        copy_optional_json_string(payload, "wx_room_id", s_voip_room_id,
                                  sizeof(s_voip_room_id));
        copy_optional_json_string(payload, "wx_app_id", s_voip_app_id,
                                  sizeof(s_voip_app_id));
        copy_optional_json_string(payload, "wx_model_id", s_voip_model_id,
                                  sizeof(s_voip_model_id));
        copy_optional_json_string(payload, "wx_server_token", s_voip_session_token,
                                  sizeof(s_voip_session_token));
        copy_optional_json_string(payload, "wx_payload", s_voip_payload,
                                  sizeof(s_voip_payload));

        if (!valid || cancelled) {
            if (cancelled) {
                ESP_LOGI(TAG, "rejecting callback for a locally cancelled VoIP call");
                reject_voip_signal(7);
            }
        } else if (outgoing || recover_outgoing) {
            if (recover_outgoing && tirtc_adapter_has_connection()) {
                media_runtime_set_uplink_active(false);
                s_ignore_next_disconnect = true;
                (void)tirtc_adapter_disconnect();
            }
            if (recover_outgoing) {
                set_state(DEVICE_SESSION_CALLING, DEVICE_SERVICE_VOIP);
                ESP_LOGI(TAG,
                         "recovering VoIP outgoing call after HTTP response loss");
            } else {
                ESP_LOGI(TAG, "VoIP callee answered; establishing WHIP connection");
            }
            if (tirtc_adapter_whip_connect(s_voip_peer_id, s_voip_token) != 0) {
                finish_session();
            }
        } else if (s_state == DEVICE_SESSION_IDLE ||
                   s_state == DEVICE_SESSION_H5_STREAMING) {
            if (tirtc_adapter_has_connection()) {
                media_runtime_set_uplink_active(false);
                s_ignore_next_disconnect = true;
                (void)tirtc_adapter_disconnect();
            }
            set_state(DEVICE_SESSION_RINGING, DEVICE_SERVICE_VOIP);
            ESP_LOGI(TAG, "incoming VoIP call room=%s; use accept or reject",
                     s_voip_room_id);
        } else {
            ESP_LOGW(TAG, "VoIP call arrived while busy; rejecting");
            reject_voip_signal(5);
        }
    } else if (strcmp(channel_name, "wx") == 0 &&
               strcmp(type_name, "call_cancel") == 0 &&
               s_service == DEVICE_SERVICE_VOIP) {
        char room_id[sizeof(s_voip_room_id)] = {0};
        copy_optional_json_string(payload, "wx_room_id", room_id, sizeof(room_id));
        if (room_id[0] != '\0' && s_voip_room_id[0] != '\0' &&
            strcmp(room_id, s_voip_room_id) == 0) {
            ESP_LOGI(TAG, "VoIP call cancelled by remote room=%s", room_id);
            finish_session();
        } else {
            ESP_LOGW(TAG,
                     "ignoring stale VoIP cancel room=%s active=%s",
                     room_id,
                     s_voip_room_id);
        }
    }
    cJSON_Delete(root);
}

static void request_contacts(bool call_first)
{
    if (tirtc_adapter_state() != TIRTC_ADAPTER_RUNNING) {
        ESP_LOGW(TAG, "contacts unavailable before TiRTC is running");
        return;
    }
    s_call_after_contacts = call_first;
    if (submit_service_event(EVENT_CONTACTS_RESPONSE,
                             PLATFORM_SERVICE_CALL,
                             "/v1/call/device/contacts",
                             NULL) != 0) {
        s_call_after_contacts = false;
        ESP_LOGE(TAG, "contacts request submission failed");
    }
}

static void request_device_call(const char *target_id)
{
    const device_media_config_t *media = media_runtime_config();
    const bool video = media != NULL &&
                       (media->video.uplink_enabled || media->video.downlink_enabled);
    cJSON *root = cJSON_CreateObject();
    cJSON *targets = cJSON_CreateArray();
    cJSON *target = cJSON_CreateString(target_id);
    if (root == NULL || targets == NULL || target == NULL) {
        cJSON_Delete(target);
        cJSON_Delete(targets);
        cJSON_Delete(root);
        finish_session();
        return;
    }
    cJSON_AddItemToArray(targets, target);
    cJSON_AddItemToObject(root, "targets", targets);
    bool ok = cJSON_AddStringToObject(root, "call_type", video ? "video" : "audio");
    char *body = ok ? cJSON_PrintUnformatted(root) : NULL;
    cJSON_Delete(root);
    if (body == NULL) {
        finish_session();
        return;
    }

    if (tirtc_adapter_has_connection()) {
        media_runtime_set_uplink_active(false);
        s_ignore_next_disconnect = true;
        (void)tirtc_adapter_disconnect();
    }
    (void)snprintf(s_call_peer_id, sizeof(s_call_peer_id), "%s", target_id);
    set_state(DEVICE_SESSION_CALLING, DEVICE_SERVICE_CALL);
    int rc = submit_service_event(EVENT_CALL_REQUEST_RESPONSE,
                                  PLATFORM_SERVICE_CALL,
                                  "/v1/call/request",
                                  body);
    free(body);
    if (rc != 0) {
        ESP_LOGE(TAG, "device call request submission failed rc=%d", rc);
        finish_session();
    }
}

static void handle_contacts_response(const char *body)
{
    const bool call_first = s_call_after_contacts;
    s_call_after_contacts = false;
    cJSON *root = NULL;
    const cJSON *data = NULL;
    if (!response_data(body, &root, &data)) {
        ESP_LOGE(TAG, "contacts response is invalid");
        return;
    }
    const cJSON *contacts = cJSON_IsObject(data)
                                ? cJSON_GetObjectItemCaseSensitive(data, "contacts")
                                : NULL;
    if (!cJSON_IsArray(contacts) || cJSON_GetArraySize(contacts) == 0) {
        ESP_LOGW(TAG, "contact list is empty");
        cJSON_Delete(root);
        return;
    }

    char first_id[sizeof(s_call_peer_id)] = {0};
    const cJSON *contact = NULL;
    int index = 0;
    cJSON_ArrayForEach(contact, contacts) {
        const cJSON *id = cJSON_GetObjectItemCaseSensitive(contact, "device_id");
        const cJSON *remark = cJSON_GetObjectItemCaseSensitive(contact, "remark");
        const cJSON *online = cJSON_GetObjectItemCaseSensitive(contact, "online");
        if (!cJSON_IsString(id) || id->valuestring == NULL) {
            continue;
        }
        ESP_LOGI(TAG, "contact[%d] id=%s remark=%s online=%s",
                 index++,
                 id->valuestring,
                 cJSON_IsString(remark) ? remark->valuestring : "-",
                 cJSON_IsTrue(online) ? "yes" : "no");
        if (first_id[0] == '\0' && strlen(id->valuestring) < sizeof(first_id)) {
            (void)snprintf(first_id, sizeof(first_id), "%s", id->valuestring);
        }
    }
    cJSON_Delete(root);
    if (call_first) {
        if (first_id[0] == '\0') {
            ESP_LOGW(TAG, "no usable contact to call");
        } else {
            ESP_LOGI(TAG, "calling first contact: %s", first_id);
            request_device_call(first_id);
        }
    }
}

static void handle_call_request_response(const char *body)
{
    if (s_state != DEVICE_SESSION_CALLING || s_service != DEVICE_SERVICE_CALL) {
        return;
    }
    cJSON *root = NULL;
    const cJSON *data = NULL;
    bool ok = response_data(body, &root, &data) && cJSON_IsObject(data) &&
              copy_json_string(data,
                               "room_id",
                               s_call_room_id,
                               sizeof(s_call_room_id));
    cJSON_Delete(root);
    if (!ok) {
        ESP_LOGE(TAG, "device call request was rejected");
        finish_session();
        return;
    }
    s_call_timeout_at_ms = now_ms() + 30000;
    s_next_room_poll_ms = now_ms();
    ESP_LOGI(TAG, "calling room=%s; cancel automatically after 30 seconds",
             s_call_room_id);
}

static void handle_room_response(const char *body)
{
    cJSON *root = NULL;
    const cJSON *data = NULL;
    if (!response_data(body, &root, &data)) {
        ESP_LOGW(TAG, "room query response is invalid");
        return;
    }
    if (data == NULL || cJSON_IsNull(data)) {
        cJSON_Delete(root);
        if (s_service == DEVICE_SERVICE_CALL &&
            (s_state == DEVICE_SESSION_RINGING || s_state == DEVICE_SESSION_IN_CALL ||
             (s_state == DEVICE_SESSION_CALLING && s_call_room_id[0] != '\0'))) {
            ESP_LOGI(TAG, "call room closed by remote");
            finish_session();
        }
        return;
    }

    char room_id[sizeof(s_call_room_id)] = {0};
    char caller_id[sizeof(s_call_peer_id)] = {0};
    char role[16] = {0};
    char status[24] = {0};
    bool ok = cJSON_IsObject(data) &&
              copy_json_string(data, "room_id", room_id, sizeof(room_id)) &&
              copy_json_string(data, "role", role, sizeof(role));
    (void)copy_json_string(data, "caller", caller_id, sizeof(caller_id));
    (void)copy_json_string(data, "status", status, sizeof(status));
    if (!ok) {
        cJSON_Delete(root);
        ESP_LOGW(TAG, "room response has no usable room or role");
        return;
    }

    if (strcmp(role, "callee") == 0 &&
        (s_state == DEVICE_SESSION_IDLE || s_state == DEVICE_SESSION_H5_STREAMING)) {
        if (tirtc_adapter_has_connection()) {
            media_runtime_set_uplink_active(false);
            s_ignore_next_disconnect = true;
            (void)tirtc_adapter_disconnect();
        }
        (void)snprintf(s_call_room_id, sizeof(s_call_room_id), "%s", room_id);
        (void)snprintf(s_call_peer_id, sizeof(s_call_peer_id), "%s", caller_id);
        set_state(DEVICE_SESSION_RINGING, DEVICE_SERVICE_CALL);
        ESP_LOGI(TAG, "incoming device call from=%s room=%s; use accept or reject",
                 s_call_peer_id,
                 s_call_room_id);
    }
    cJSON_Delete(root);
}

static void accept_device_call(void)
{
    cJSON *root = cJSON_CreateObject();
    bool ok = root != NULL && s_call_peer_id[0] != '\0' && s_call_room_id[0] != '\0' &&
              cJSON_AddStringToObject(root, "device_id", s_call_peer_id) &&
              cJSON_AddStringToObject(root, "room_id", s_call_room_id) &&
              cJSON_AddStringToObject(root, "purpose", "call");
    char *body = ok ? cJSON_PrintUnformatted(root) : NULL;
    cJSON_Delete(root);
    if (body == NULL) {
        ESP_LOGE(TAG, "cannot build device accept request");
        finish_session();
        return;
    }
    set_state(DEVICE_SESSION_CALLING, DEVICE_SERVICE_CALL);
    int rc = submit_service_event(EVENT_ACCEPT_RESPONSE,
                                  PLATFORM_SERVICE_CALL,
                                  "/v1/call/device/info",
                                  body);
    free(body);
    if (rc != 0) {
        ESP_LOGE(TAG, "device accept request submission failed rc=%d", rc);
        finish_session();
    }
}

static void handle_accept_response(const char *body)
{
    cJSON *root = NULL;
    const cJSON *data = NULL;
    char token[SESSION_ARGUMENT_MAX];
    bool ok = response_data(body, &root, &data) && cJSON_IsObject(data) &&
              copy_json_string(data, "token", token, sizeof(token));
    cJSON_Delete(root);
    if (!ok || s_call_peer_id[0] == '\0') {
        ESP_LOGE(TAG, "device accept response has no connection token");
        finish_session();
        return;
    }
    int rc = tirtc_adapter_connect(s_call_peer_id, token);
    if (rc != 0) {
        ESP_LOGE(TAG, "device call P2P connection submission failed rc=%d", rc);
        finish_session();
    }
}

static const char *platform_audio_codec(device_audio_codec_t codec)
{
    switch (codec) {
    case DEVICE_AUDIO_CODEC_G711A: return "alaw";
    case DEVICE_AUDIO_CODEC_AMR_NB:
    case DEVICE_AUDIO_CODEC_AMR_WB: return "amr";
    case DEVICE_AUDIO_CODEC_OPUS: return "opus";
    default: return "alaw";
    }
}

static void submit_voip_profile(void)
{
    const device_media_config_t *media = media_runtime_config();
    if (media == NULL || !platform_client_ready()) {
        return;
    }
    cJSON *root = cJSON_CreateObject();
    bool no_video = !media->video.uplink_enabled && !media->video.downlink_enabled;
    bool ok = root != NULL &&
              cJSON_AddNumberToObject(root, "screen_width", media->video.width) &&
              cJSON_AddNumberToObject(root, "screen_height", media->video.height) &&
              cJSON_AddNumberToObject(root, "camera_rotation",
                                     media->video.camera_rotation) &&
              cJSON_AddNumberToObject(root, "aspect_ratio",
                                     media->video.aspect_ratio) &&
              (media->video.object_fit[0] == '\0' ||
               cJSON_AddStringToObject(root, "object_fit",
                                      media->video.object_fit)) &&
              cJSON_AddBoolToObject(root, "hor_mirror",
                                   media->video.hor_mirror) &&
              cJSON_AddBoolToObject(root, "vert_mirror",
                                   media->video.vert_mirror) &&
              cJSON_AddNumberToObject(root, "audio_rate", media->audio.sample_rate_hz) &&
              cJSON_AddNumberToObject(root, "audio_channels", media->audio.channels) &&
              cJSON_AddStringToObject(root, "up_video_mt",
                                     device_video_codec_name(media->video.codec)) &&
              cJSON_AddStringToObject(root, "down_video_mt",
                                     device_video_codec_name(media->video.codec)) &&
              cJSON_AddStringToObject(root, "down_audio_mt",
                                     platform_audio_codec(media->audio.codec)) &&
              cJSON_AddBoolToObject(root, "no_video", no_video) &&
              cJSON_AddNumberToObject(root, "calling_timeout_sec", 30);
    char *body = ok ? cJSON_PrintUnformatted(root) : NULL;
    cJSON_Delete(root);
    if (body == NULL) {
        ESP_LOGW(TAG, "cannot build VoIP device profile");
        return;
    }
    esp_err_t err = platform_client_request(PLATFORM_SERVICE_VOIP,
                                            "/v1/voip/device/profile",
                                            body,
                                            service_log_response,
                                            (void *)"voip profile");
    free(body);
    if (err == ESP_OK) {
        s_voip_profile_submitted = true;
    } else {
        ESP_LOGW(TAG, "VoIP profile submission failed: %s", esp_err_to_name(err));
    }
}

static void request_voip_callers(void)
{
    if (!s_voip_profile_submitted) {
        submit_voip_profile();
    }
    if (submit_service_event(EVENT_VOIP_CALLERS_RESPONSE,
                             PLATFORM_SERVICE_VOIP,
                             "/v1/voip/device/contacts",
                             NULL) != ESP_OK) {
        ESP_LOGE(TAG, "VoIP caller list request submission failed");
        finish_session();
    }
}

static void handle_voip_callers_response(const char *body)
{
    if (s_state != DEVICE_SESSION_CALLING || s_service != DEVICE_SERVICE_VOIP) {
        return;
    }
    cJSON *root = NULL;
    const cJSON *data = NULL;
    if (!response_data(body, &root, &data) || !cJSON_IsObject(data)) {
        ESP_LOGE(TAG, "VoIP caller list response is invalid");
        finish_session();
        return;
    }
    const cJSON *list = cJSON_GetObjectItemCaseSensitive(data, "contacts");
    const cJSON *caller = cJSON_IsArray(list) ? cJSON_GetArrayItem(list, 0) : NULL;
    char app_id[65];
    char model_id[65];
    char open_id[129];
    bool ok = cJSON_IsObject(caller) &&
              copy_json_string(caller, "wx_app_id", app_id, sizeof(app_id)) &&
              copy_json_string(caller, "wx_model_id", model_id, sizeof(model_id)) &&
              copy_json_string(caller, "wx_open_id", open_id, sizeof(open_id));
    cJSON_Delete(root);
    if (!ok) {
        ESP_LOGW(TAG, "there is no usable authorized VoIP contact");
        finish_session();
        return;
    }

    runtime_tirtc_config_t runtime;
    if (runtime_config_load_tirtc(&runtime) != ESP_OK) {
        finish_session();
        return;
    }
    cJSON *request = cJSON_CreateObject();
    ok = request != NULL &&
         cJSON_AddStringToObject(request, "device_id", runtime.device_id) &&
         cJSON_AddStringToObject(request, "wx_app_id", app_id) &&
         cJSON_AddStringToObject(request, "wx_user_openid", open_id) &&
         cJSON_AddStringToObject(request, "wx_model_id", model_id) &&
         cJSON_AddStringToObject(request, "wx_room_type", "voice") &&
         cJSON_AddNumberToObject(request, "wx_version_type", 2);
    char *request_body = ok ? cJSON_PrintUnformatted(request) : NULL;
    cJSON_Delete(request);
    if (request_body == NULL) {
        finish_session();
        return;
    }
    s_voip_call_id[0] = '\0';
    s_voip_cancelled_open_id[0] = '\0';
    s_voip_cancelled_call_id[0] = '\0';
    s_voip_cancelled_until_ms = 0;
    (void)snprintf(s_voip_open_id, sizeof(s_voip_open_id), "%s", open_id);
    esp_err_t err = platform_client_request(PLATFORM_SERVICE_VOIP,
                                            "/v1/voip/device/call",
                                            request_body,
                                            service_event_response,
                                            (void *)(uintptr_t)EVENT_VOIP_DIAL_RESPONSE);
    free(request_body);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "VoIP dial request submission failed: %s", esp_err_to_name(err));
        finish_session();
    }
}

static void handle_voip_dial_response(const char *body)
{
    if (s_state != DEVICE_SESSION_CALLING || s_service != DEVICE_SERVICE_VOIP) {
        return;
    }
    cJSON *root = NULL;
    const cJSON *data = NULL;
    bool ok = response_data(body, &root, &data);
    if (ok && cJSON_IsObject(data)) {
        copy_optional_json_string(data,
                                  "call_id",
                                  s_voip_call_id,
                                  sizeof(s_voip_call_id));
    }
    cJSON_Delete(root);
    if (!ok) {
        cJSON *error_root = body == NULL ? NULL : cJSON_Parse(body);
        const cJSON *code = error_root == NULL ? NULL :
            cJSON_GetObjectItemCaseSensitive(error_root, "code");
        if (cJSON_IsNumber(code) && code->valueint == 40205) {
            ESP_LOGW(TAG, "微信 VoIP 授权已失效，请让用户重新授权");
        } else if (cJSON_IsNumber(code) && code->valueint == 6006) {
            ESP_LOGW(TAG, "设备已解绑，请重新完成设备绑定");
        } else if (cJSON_IsNumber(code) && code->valueint == 401) {
            ESP_LOGW(TAG, "设备登录凭证无效或已过期，请重新获取 mqtt_token");
        } else {
            ESP_LOGE(TAG, "VoIP dial request was rejected");
        }
        cJSON_Delete(error_root);
        finish_session();
        return;
    }
    s_call_timeout_at_ms = now_ms() + 30000;
    ESP_LOGI(TAG, "VoIP calling first authorized contact; cancel after 30 seconds if unanswered");
}

static void handle_ai_token(const char *body)
{
    if (s_state != DEVICE_SESSION_AI_CONNECTING) {
        return;
    }
    if (body == NULL || body[0] == '\0') {
        ESP_LOGE(TAG, "AI token request returned no response; ending session");
        finish_session();
        return;
    }
    cJSON *root = cJSON_Parse(body);
    const cJSON *code = root == NULL ? NULL : cJSON_GetObjectItemCaseSensitive(root, "code");
    const cJSON *data = root == NULL ? NULL : cJSON_GetObjectItemCaseSensitive(root, "data");
    char peer_id[SESSION_ARGUMENT_MAX];
    char token[SESSION_ARGUMENT_MAX];
    bool code_ok = cJSON_IsNumber(code) && (code->valueint == 0 || code->valueint == 200);
    bool ok = code_ok && cJSON_IsObject(data) &&
              copy_json_string(data, "peer_id", peer_id, sizeof(peer_id)) &&
              copy_json_string(data, "token", token, sizeof(token));
    if (ok) {
        const cJSON *role = cJSON_GetObjectItemCaseSensitive(data, "role_id");
        if (cJSON_IsString(role) && role->valuestring != NULL) {
            (void)snprintf(s_ai_role_id, sizeof(s_ai_role_id), "%.64s", role->valuestring);
        } else {
            s_ai_role_id[0] = '\0';
        }
    }
    cJSON_Delete(root);

    if (!ok) {
        ESP_LOGE(TAG, "AI token response is invalid");
        finish_session();
        return;
    }
    int rc = tirtc_adapter_whip_connect(peer_id, token);
    if (rc != 0) {
        ESP_LOGE(TAG, "AI WHIP connect submission failed rc=%d", rc);
        finish_session();
    }
}

static void send_ai_start(void)
{
    runtime_tirtc_config_t runtime;
    const device_media_config_t *media = media_runtime_config();
    if (runtime_config_load_tirtc(&runtime) != ESP_OK || media == NULL ||
        s_state != DEVICE_SESSION_AI_CONNECTING || !tirtc_adapter_has_connection()) {
        finish_session();
        return;
    }

    char request_id[17];
    (void)snprintf(request_id, sizeof(request_id), "%08lx%08lx",
                   (unsigned long)esp_random(), (unsigned long)esp_random());
    cJSON *root = cJSON_CreateObject();
    cJSON *params = cJSON_CreateObject();
    cJSON *input_audio = cJSON_CreateObject();
    cJSON *output_audio = cJSON_CreateObject();
    bool ok = root != NULL && params != NULL && input_audio != NULL && output_audio != NULL &&
              cJSON_AddStringToObject(root, "jsonrpc", "2.0") &&
              cJSON_AddStringToObject(root, "id", request_id) &&
              cJSON_AddStringToObject(root, "method", "start_session") &&
              cJSON_AddStringToObject(params, "device_id", runtime.device_id) &&
              cJSON_AddStringToObject(params, "role_id", s_ai_role_id) &&
              cJSON_AddNumberToObject(input_audio, "sample_rate", media->audio.sample_rate_hz) &&
              cJSON_AddNumberToObject(input_audio, "channels", media->audio.channels) &&
              cJSON_AddNumberToObject(output_audio, "sample_rate", media->audio.sample_rate_hz) &&
              cJSON_AddNumberToObject(output_audio, "channels", media->audio.channels);
    if (ok) {
        cJSON_AddItemToObject(params, "input_audio", input_audio);
        input_audio = NULL;
        cJSON_AddItemToObject(params, "output_audio", output_audio);
        output_audio = NULL;
        cJSON_AddItemToObject(root, "params", params);
        params = NULL;
    }
    char *json = ok ? cJSON_PrintUnformatted(root) : NULL;
    cJSON_Delete(input_audio);
    cJSON_Delete(output_audio);
    cJSON_Delete(params);
    cJSON_Delete(root);
    if (json == NULL) {
        ESP_LOGE(TAG, "cannot build AI start_session command");
        finish_session();
        return;
    }
    int rc = tirtc_adapter_send_command(CMD_AI, json, strlen(json));
    free(json);
    s_ai_start_at_ms = 0;
    if (rc < 0) {
        ESP_LOGE(TAG, "AI start_session send failed rc=%d", rc);
        finish_session();
    } else {
        ESP_LOGI(TAG, "AI start_session sent; waiting for response");
    }
}

static void handle_connection(bool connected, bool incoming)
{
    if (!connected) {
        media_runtime_set_uplink_active(false);
        if (s_ignore_next_disconnect) {
            s_ignore_next_disconnect = false;
            return;
        }
        if (s_service == DEVICE_SERVICE_CALL && s_call_room_id[0] != '\0' &&
            (s_state == DEVICE_SESSION_CALLING || s_state == DEVICE_SESSION_IN_CALL)) {
            (void)submit_room_action("/v1/call/hangup", "connection_error");
        }
        if (s_state != DEVICE_SESSION_IDLE && s_state != DEVICE_SESSION_OFFLINE) {
            finish_session();
        }
        return;
    }

    if (incoming) {
        if (s_state == DEVICE_SESSION_IDLE) {
            set_state(DEVICE_SESSION_H5_STREAMING, DEVICE_SERVICE_H5);
            media_runtime_set_uplink_active(true);
        } else if (s_state == DEVICE_SESSION_RINGING) {
            set_state(DEVICE_SESSION_IN_CALL, s_service);
            media_runtime_set_uplink_active(true);
        } else if (s_state == DEVICE_SESSION_CALLING &&
                   s_service == DEVICE_SERVICE_CALL) {
            ESP_LOGI(TAG, "callee P2P connected; waiting for room confirmation command");
        } else {
            ESP_LOGW(TAG, "unexpected incoming connection while %s; closing",
                     device_session_state_name(s_state));
            (void)tirtc_adapter_disconnect();
        }
        return;
    }

    if (s_service == DEVICE_SERVICE_AI && s_state == DEVICE_SESSION_AI_CONNECTING) {
        s_ai_start_at_ms = esp_timer_get_time() / 1000 + 300;
    } else if ((s_service == DEVICE_SERVICE_VOIP || s_service == DEVICE_SERVICE_CALL) &&
               s_state == DEVICE_SESSION_CALLING) {
        set_state(DEVICE_SESSION_IN_CALL, s_service);
        s_call_timeout_at_ms = 0;
        media_runtime_set_uplink_active(true);
        if (s_service == DEVICE_SERVICE_CALL) {
            char room[192];
            int length = snprintf(room,
                                  sizeof(room),
                                  "{\"room_id\":\"%s\"}",
                                  s_call_room_id[0] == '\0' ? "direct-demo" : s_call_room_id);
            if (length > 0 && (size_t)length < sizeof(room)) {
                (void)tirtc_adapter_send_command(CMD_VOIP_ACCEPT,
                                                 room,
                                                 (uint32_t)length);
            }
        }
    } else {
        ESP_LOGW(TAG, "outgoing connection completed after its session ended; closing");
        (void)tirtc_adapter_disconnect();
    }
}

static void handle_command(const session_event_t *event)
{
    if (event->command == CMD_AI && s_service == DEVICE_SERVICE_AI) {
        cJSON *root = cJSON_ParseWithLength(event->payload, event->length);
        bool error = root != NULL && cJSON_GetObjectItemCaseSensitive(root, "error") != NULL;
        bool response = root != NULL &&
                        cJSON_GetObjectItemCaseSensitive(root, "result") != NULL;
        cJSON_Delete(root);
        if (error) {
            ESP_LOGE(TAG, "AI start_session was rejected");
            finish_session();
        } else if (response && s_state == DEVICE_SESSION_AI_CONNECTING) {
            set_state(DEVICE_SESSION_AI_ACTIVE, DEVICE_SERVICE_AI);
            media_runtime_set_uplink_active(true);
        }
    } else if (event->command == CMD_VOIP_HANGUP) {
        ESP_LOGI(TAG, "remote hangup received");
        finish_session();
    } else if (event->command == CMD_VOIP_ACCEPT &&
               (s_service == DEVICE_SERVICE_VOIP || s_service == DEVICE_SERVICE_CALL)) {
        if (s_service == DEVICE_SERVICE_CALL && s_call_room_id[0] != '\0') {
            cJSON *root = cJSON_ParseWithLength(event->payload, event->length);
            const cJSON *room = root == NULL
                                    ? NULL
                                    : cJSON_GetObjectItemCaseSensitive(root, "room_id");
            bool matches = cJSON_IsString(room) && room->valuestring != NULL &&
                           strcmp(room->valuestring, s_call_room_id) == 0;
            cJSON_Delete(root);
            if (!matches) {
                ESP_LOGW(TAG, "ignoring call confirmation for a different room");
                (void)tirtc_adapter_disconnect();
                return;
            }
        }
        s_call_timeout_at_ms = 0;
        set_state(DEVICE_SESSION_IN_CALL, s_service);
        media_runtime_set_uplink_active(true);
    }
}

static void handle_event(const session_event_t *event)
{
    switch (event->type) {
    case EVENT_CONNECTION:
        handle_connection(event->connected, event->incoming);
        break;
    case EVENT_COMMAND:
        handle_command(event);
        break;
    case EVENT_PLATFORM_SIGNAL:
        handle_platform_signal(event);
        break;
    case EVENT_VOIP_CALL_DEFAULT:
        if (s_state != DEVICE_SESSION_IDLE && s_state != DEVICE_SESSION_H5_STREAMING) {
            ESP_LOGW(TAG, "VoIP call ignored while %s", device_session_state_name(s_state));
            break;
        }
        if (!platform_client_ready()) {
            ESP_LOGW(TAG, "VoIP call unavailable while platform signaling is offline");
            break;
        }
        if (tirtc_adapter_has_connection()) {
            media_runtime_set_uplink_active(false);
            s_ignore_next_disconnect = true;
            (void)tirtc_adapter_disconnect();
        }
        set_state(DEVICE_SESSION_CALLING, DEVICE_SERVICE_VOIP);
        request_voip_callers();
        break;
    case EVENT_VOIP_CALLERS_RESPONSE:
        handle_voip_callers_response(event->payload);
        break;
    case EVENT_VOIP_DIAL_RESPONSE:
        handle_voip_dial_response(event->payload);
        break;
    case EVENT_AI_TOKEN:
        handle_ai_token(event->payload);
        break;
    case EVENT_CONTACTS:
        request_contacts(false);
        break;
    case EVENT_CONTACTS_RESPONSE:
        handle_contacts_response(event->payload);
        break;
    case EVENT_CALL_REQUEST_RESPONSE:
        handle_call_request_response(event->payload);
        break;
    case EVENT_ROOM_RESPONSE:
        handle_room_response(event->payload);
        break;
    case EVENT_ACCEPT_RESPONSE:
        handle_accept_response(event->payload);
        break;
    case EVENT_AI_PRESS:
        if (s_state != DEVICE_SESSION_IDLE && s_state != DEVICE_SESSION_H5_STREAMING) {
            ESP_LOGW(TAG, "AI press ignored while %s", device_session_state_name(s_state));
            break;
        }
        if (tirtc_adapter_has_connection()) {
            media_runtime_set_uplink_active(false);
            s_ignore_next_disconnect = true;
            (void)tirtc_adapter_disconnect();
        }
        set_state(DEVICE_SESSION_AI_CONNECTING, DEVICE_SERVICE_AI);
        if (platform_client_request(PLATFORM_SERVICE_AI,
                                    "/v1/ai/token",
                                    NULL,
                                    ai_token_response,
                                    NULL) != ESP_OK) {
            ESP_LOGE(TAG, "AI token request submission failed");
            finish_session();
        }
        break;
    case EVENT_AI_RELEASE:
        if (s_service == DEVICE_SERVICE_AI &&
            (s_state == DEVICE_SESSION_AI_CONNECTING ||
             s_state == DEVICE_SESSION_AI_ACTIVE)) {
            if (tirtc_adapter_has_connection()) {
                const char end[] = "{\"jsonrpc\":\"2.0\",\"method\":\"end_session\"}";
                (void)tirtc_adapter_send_command(CMD_AI, end, sizeof(end) - 1U);
            }
            finish_session();
        }
        break;
    case EVENT_VOIP_CONNECT:
    case EVENT_DEVICE_CALL:
        if (s_state != DEVICE_SESSION_IDLE && s_state != DEVICE_SESSION_H5_STREAMING) {
            ESP_LOGW(TAG, "call ignored while %s", device_session_state_name(s_state));
            break;
        }
        if (tirtc_adapter_has_connection()) {
            media_runtime_set_uplink_active(false);
            s_ignore_next_disconnect = true;
            (void)tirtc_adapter_disconnect();
        }
        s_service = event->type == EVENT_VOIP_CONNECT
                        ? DEVICE_SERVICE_VOIP
                        : DEVICE_SERVICE_CALL;
        set_state(DEVICE_SESSION_CALLING, s_service);
        int rc = event->type == EVENT_VOIP_CONNECT
                     ? tirtc_adapter_whip_connect(event->first, event->second)
                     : tirtc_adapter_connect(event->first, event->second);
        if (rc != 0) {
            ESP_LOGE(TAG, "call connection submission failed rc=%d", rc);
            finish_session();
        }
        break;
    case EVENT_CALL_DEFAULT:
        if (s_state != DEVICE_SESSION_IDLE && s_state != DEVICE_SESSION_H5_STREAMING) {
            ESP_LOGW(TAG, "call ignored while %s", device_session_state_name(s_state));
            break;
        }
        request_contacts(true);
        break;
    case EVENT_CANCEL:
        if (s_state == DEVICE_SESSION_CALLING || s_state == DEVICE_SESSION_AI_CONNECTING) {
            if (s_service == DEVICE_SERVICE_CALL && s_call_room_id[0] != '\0') {
                (void)submit_room_action("/v1/call/cancel", NULL);
            } else if (s_service == DEVICE_SERVICE_VOIP && s_voip_open_id[0] != '\0') {
                (void)snprintf(s_voip_cancelled_open_id,
                               sizeof(s_voip_cancelled_open_id),
                               "%s",
                               s_voip_open_id);
                (void)snprintf(s_voip_cancelled_call_id,
                               sizeof(s_voip_cancelled_call_id),
                               "%s",
                               s_voip_call_id);
                s_voip_cancelled_until_ms = now_ms() + 60000;
            }
            finish_session();
        }
        break;
    case EVENT_HANGUP:
        if (s_state == DEVICE_SESSION_IN_CALL || s_state == DEVICE_SESSION_AI_ACTIVE ||
            s_state == DEVICE_SESSION_H5_STREAMING) {
            if (s_service == DEVICE_SERVICE_AI) {
                const char end[] = "{\"jsonrpc\":\"2.0\",\"method\":\"end_session\"}";
                (void)tirtc_adapter_send_command(CMD_AI, end, sizeof(end) - 1U);
            } else if (s_service == DEVICE_SERVICE_VOIP) {
                const char hangup[] = "{\"reason\":0}";
                (void)tirtc_adapter_send_command(CMD_VOIP_HANGUP,
                                                  hangup,
                                                  sizeof(hangup) - 1U);
            } else if (s_service == DEVICE_SERVICE_CALL && s_call_room_id[0] != '\0') {
                (void)submit_room_action("/v1/call/hangup", "hangup");
            }
            finish_session();
        }
        break;
    case EVENT_ACCEPT:
        if (s_state == DEVICE_SESSION_RINGING) {
            if (s_service == DEVICE_SERVICE_CALL) {
                accept_device_call();
            } else if (s_service == DEVICE_SERVICE_VOIP) {
                set_state(DEVICE_SESSION_CALLING, DEVICE_SERVICE_VOIP);
                if (tirtc_adapter_whip_connect(s_voip_peer_id, s_voip_token) != 0) {
                    finish_session();
                }
            } else {
                set_state(DEVICE_SESSION_CALLING, s_service);
            }
        }
        break;
    case EVENT_REJECT:
        if (s_state == DEVICE_SESSION_RINGING) {
            if (s_service == DEVICE_SERVICE_CALL && s_call_room_id[0] != '\0') {
                (void)submit_room_action("/v1/call/reject", "decline");
            } else if (s_service == DEVICE_SERVICE_VOIP) {
                reject_voip_signal(7);
            }
            finish_session();
        }
        break;
    default:
        break;
    }
}

static void session_task(void *argument)
{
    (void)argument;
    set_state(DEVICE_SESSION_IDLE, DEVICE_SERVICE_H5);
    s_next_room_poll_ms = now_ms() + 1000;
    for (;;) {
        session_event_t *event = NULL;
        if (xQueueReceive(s_queue, &event, pdMS_TO_TICKS(50)) == pdTRUE &&
            event != NULL) {
            handle_event(event);
            free(event);
        }
        const int64_t current_ms = now_ms();
        if (s_ai_start_at_ms != 0 && current_ms >= s_ai_start_at_ms) {
            send_ai_start();
        }
        if (s_call_timeout_at_ms != 0 && current_ms >= s_call_timeout_at_ms &&
            s_state == DEVICE_SESSION_CALLING) {
            if (s_service == DEVICE_SERVICE_CALL) {
                ESP_LOGW(TAG, "device call timed out; cancelling room=%s", s_call_room_id);
                (void)submit_room_action("/v1/call/cancel", NULL);
            } else if (s_service == DEVICE_SERVICE_VOIP) {
                ESP_LOGW(TAG, "VoIP call timed out; cancelling locally");
                (void)snprintf(s_voip_cancelled_open_id,
                               sizeof(s_voip_cancelled_open_id),
                               "%s",
                               s_voip_open_id);
                (void)snprintf(s_voip_cancelled_call_id,
                               sizeof(s_voip_cancelled_call_id),
                               "%s",
                               s_voip_call_id);
                s_voip_cancelled_until_ms = current_ms + 60000;
            }
            finish_session();
        }
        if (!s_voip_profile_submitted && platform_client_ready()) {
            submit_voip_profile();
        }
        const bool room_poll_allowed =
            platform_client_ready() &&
            tirtc_adapter_state() == TIRTC_ADAPTER_RUNNING &&
            s_service != DEVICE_SERVICE_AI && s_service != DEVICE_SERVICE_VOIP;
        if (room_poll_allowed && !s_room_request_pending &&
            current_ms >= s_next_room_poll_ms) {
            s_room_request_pending = true;
            s_next_room_poll_ms = current_ms + 2000;
            if (submit_service_event(EVENT_ROOM_RESPONSE,
                                     PLATFORM_SERVICE_CALL,
                                     "/v1/call/room",
                                     NULL) != ESP_OK) {
                s_room_request_pending = false;
                ESP_LOGW(TAG, "room status request submission failed");
            }
        }
    }
}

static esp_err_t enqueue_simple(session_event_type_t type)
{
    session_event_t *event = calloc(1, sizeof(*event));
    if (event == NULL) {
        return ESP_ERR_NO_MEM;
    }
    event->type = type;
    bool queued = queue_event(event);
    if (!queued) {
        free(event);
    }
    return queued ? ESP_OK : ESP_ERR_TIMEOUT;
}

static esp_err_t enqueue_pair(session_event_type_t type, const char *first, const char *second)
{
    if (first == NULL || first[0] == '\0' || second == NULL || second[0] == '\0' ||
        strlen(first) >= SESSION_ARGUMENT_MAX || strlen(second) >= SESSION_ARGUMENT_MAX) {
        return ESP_ERR_INVALID_ARG;
    }
    session_event_t *event = calloc(1, sizeof(*event));
    if (event == NULL) {
        return ESP_ERR_NO_MEM;
    }
    event->type = type;
    (void)snprintf(event->first, sizeof(event->first), "%s", first);
    (void)snprintf(event->second, sizeof(event->second), "%s", second);
    bool queued = queue_event(event);
    if (!queued) {
        free(event);
    }
    return queued ? ESP_OK : ESP_ERR_TIMEOUT;
}

esp_err_t session_runtime_start(void)
{
    if (s_task != NULL) {
        return ESP_OK;
    }
    s_queue = xQueueCreate(SESSION_QUEUE_DEPTH, sizeof(session_event_t *));
    if (s_queue == NULL) {
        return ESP_ERR_NO_MEM;
    }
    const tirtc_adapter_event_handlers_t handlers = {
        .on_connection_changed = adapter_connection_changed,
        .on_command = adapter_command,
    };
    tirtc_adapter_set_event_handlers(&handlers);
    platform_client_set_signal_handler(platform_signal, NULL);
    if (xTaskCreate(session_task, "session", 24576, NULL, 6, &s_task) != pdPASS) {
        vQueueDelete(s_queue);
        s_queue = NULL;
        return ESP_ERR_NO_MEM;
    }
    return ESP_OK;
}

device_session_state_t session_runtime_state(void)
{
    return s_state;
}

device_service_t session_runtime_service(void)
{
    return s_service;
}

esp_err_t session_runtime_ai_press(void) { return enqueue_simple(EVENT_AI_PRESS); }
esp_err_t session_runtime_ai_release(void) { return enqueue_simple(EVENT_AI_RELEASE); }
esp_err_t session_runtime_voip_call_default(void)
{
    return enqueue_simple(EVENT_VOIP_CALL_DEFAULT);
}
esp_err_t session_runtime_voip_connect(const char *peer, const char *token)
{
    return enqueue_pair(EVENT_VOIP_CONNECT, peer, token);
}
esp_err_t session_runtime_contacts(void) { return enqueue_simple(EVENT_CONTACTS); }
esp_err_t session_runtime_device_call_default(void)
{
    return enqueue_simple(EVENT_CALL_DEFAULT);
}
esp_err_t session_runtime_device_call(const char *remote, const char *token)
{
    return enqueue_pair(EVENT_DEVICE_CALL, remote, token);
}
esp_err_t session_runtime_accept(void) { return enqueue_simple(EVENT_ACCEPT); }
esp_err_t session_runtime_reject(void) { return enqueue_simple(EVENT_REJECT); }
esp_err_t session_runtime_cancel(void) { return enqueue_simple(EVENT_CANCEL); }
esp_err_t session_runtime_hangup(void) { return enqueue_simple(EVENT_HANGUP); }
