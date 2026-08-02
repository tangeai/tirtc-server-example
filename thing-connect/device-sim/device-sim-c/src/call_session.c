/** \file call_session.c
 * \brief Call-server session layer — HTTP endpoints, ring timer, command dispatch.
 *
 * Uses libcurl for HTTP (same pattern as device_flow.c and tirtc_voip.c).
 */

#include "call_session.h"
#define LOG_MODULE "call"
#include "common.h"
#include "http_tls.h"

#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include <curl/curl.h>
#include <cjson/cJSON.h>

#include "tirtc_call.h"

static int _before_call_start(CallState *cs, int consume_pending) {
    if (cs->before_start_ex)
        return cs->before_start_ex(cs->runtime_user, consume_pending);
    if (cs->before_start)
        return cs->before_start(cs->runtime_user);
    return 0;
}

/* ── HTTP helpers ────────────────────────────────────────────────────────── */

static size_t _write_cb(void *ptr, size_t size, size_t nmemb, void *user) {
    StrBuf *sb = (StrBuf *)user;
    size_t total = size * nmemb;
    if (sb->len + total >= sb->cap) return 0;
    memcpy(sb->buf + sb->len, ptr, total);
    sb->len += total;
    sb->buf[sb->len] = '\0';
    return total;
}

static int _http_get(const char *url, const char *bearer,
                     char *body_buf, size_t body_cap, long *http_code) {
    CURL *curl = curl_easy_init();
    if (!curl) return -1;

    StrBuf body;
    sb_init(&body, body_buf, body_cap);

    struct curl_slist *hlist = NULL;
    if (bearer && bearer[0]) {
        char auth[640];
        snprintf(auth, sizeof(auth), "Authorization: Bearer %s", bearer);
        hlist = curl_slist_append(hlist, auth);
    }

    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 10L);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, _write_cb);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &body);
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, hlist);
    http_tls_apply(curl);

    CURLcode res = curl_easy_perform(curl);
    curl_slist_free_all(hlist);
    if (res != CURLE_OK) {
        LOG_E("HTTP GET %s 失败: %s", url, curl_easy_strerror(res));
        curl_easy_cleanup(curl);
        return -1;
    }
    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, http_code);
    curl_easy_cleanup(curl);
    return 0;
}

static int _http_post(const char *url, const char *bearer,
                      const char *json_body,
                      char *body_buf, size_t body_cap, long *http_code) {
    CURL *curl = curl_easy_init();
    if (!curl) return -1;

    StrBuf body;
    sb_init(&body, body_buf, body_cap);

    struct curl_slist *hlist = NULL;
    if (bearer && bearer[0]) {
        char auth[640];
        snprintf(auth, sizeof(auth), "Authorization: Bearer %s", bearer);
        hlist = curl_slist_append(hlist, auth);
    }
    hlist = curl_slist_append(hlist, "Content-Type: application/json");

    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 10L);
    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, json_body ? json_body : "");
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, hlist);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, _write_cb);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &body);
    http_tls_apply(curl);

    CURLcode res = curl_easy_perform(curl);
    curl_slist_free_all(hlist);
    if (res != CURLE_OK) {
        LOG_E("HTTP POST %s 失败: %s", url, curl_easy_strerror(res));
        curl_easy_cleanup(curl);
        return -1;
    }
    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, http_code);
    curl_easy_cleanup(curl);
    return 0;
}

static int _http_put(const char *url, const char *bearer,
                     const char *json_body,
                     char *body_buf, size_t body_cap, long *http_code) {
    CURL *curl = curl_easy_init();
    if (!curl) return -1;

    StrBuf body;
    sb_init(&body, body_buf, body_cap);

    struct curl_slist *hlist = NULL;
    if (bearer && bearer[0]) {
        char auth[640];
        snprintf(auth, sizeof(auth), "Authorization: Bearer %s", bearer);
        hlist = curl_slist_append(hlist, auth);
    }
    hlist = curl_slist_append(hlist, "Content-Type: application/json");

    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 10L);
    curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, "PUT");
    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, json_body ? json_body : "");
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, hlist);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, _write_cb);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &body);
    http_tls_apply(curl);

    CURLcode res = curl_easy_perform(curl);
    curl_slist_free_all(hlist);
    if (res != CURLE_OK) {
        LOG_E("HTTP PUT %s 失败: %s", url, curl_easy_strerror(res));
        curl_easy_cleanup(curl);
        return -1;
    }
    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, http_code);
    curl_easy_cleanup(curl);
    return 0;
}

static int _http_delete_contact(const char *base_url, const char *bearer,
                                const char *peer_id,
                                char *body_buf, size_t body_cap,
                                long *http_code) {
    CURL *curl = curl_easy_init();
    if (!curl) return -1;
    char *escaped = curl_easy_escape(curl, peer_id, 0);
    if (!escaped) {
        curl_easy_cleanup(curl);
        return -1;
    }
    char url[768];
    if (snprintf(url, sizeof(url), "%s?peer_id=%s", base_url, escaped) >=
        (int)sizeof(url)) {
        curl_free(escaped);
        curl_easy_cleanup(curl);
        return -1;
    }
    curl_free(escaped);

    StrBuf body;
    sb_init(&body, body_buf, body_cap);
    struct curl_slist *headers = NULL;
    if (bearer && bearer[0]) {
        char auth[640];
        snprintf(auth, sizeof(auth), "Authorization: Bearer %s", bearer);
        headers = curl_slist_append(headers, auth);
    }
    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 10L);
    curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, "DELETE");
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, _write_cb);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &body);
    http_tls_apply(curl);
    CURLcode result = curl_easy_perform(curl);
    curl_slist_free_all(headers);
    if (result != CURLE_OK) {
        LOG_E("HTTP DELETE %s 失败: %s", url, curl_easy_strerror(result));
        curl_easy_cleanup(curl);
        return -1;
    }
    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, http_code);
    curl_easy_cleanup(curl);
    return 0;
}

static int _json_resp_code(const char *json_str) {
    cJSON *root = cJSON_Parse(json_str);
    if (!root) return -1;
    cJSON *code = cJSON_GetObjectItem(root, "code");
    int val = code && cJSON_IsNumber(code) ? code->valueint : -1;
    cJSON_Delete(root);
    return val;
}

/* ── Ring timer ──────────────────────────────────────────────────────────── */

static void *_ring_timer_thread(void *arg) {
    CallState *cs = (CallState *)arg;
    int elapsed = 0;
    char room_id[128];

    pthread_mutex_lock(&cs->lock);
    STR_COPY(room_id, cs->room_id);
    pthread_mutex_unlock(&cs->lock);

    while (elapsed < 30) {
        sleep_ms(1000);
        elapsed++;
        pthread_mutex_lock(&cs->lock);
        if (!cs->ring_timer_running || !cs->active) {
            pthread_mutex_unlock(&cs->lock);
            return NULL;
        }
        pthread_mutex_unlock(&cs->lock);
    }

    /* Timeout: cancel the call */
    LOG_W("等待超时（30s），自动取消呼叫 room_id=%s", room_id);

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/cancel", cs->call_server);

    cJSON *root = cJSON_CreateObject();
    char *json_str = NULL;
    if (root && cJSON_AddStringToObject(root, "room_id", room_id))
        json_str = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (json_str) {
        char body_buf[4096];
        long http_code = 0;
        _http_post(url, cs->mqtt_token, json_str, body_buf,
                   sizeof(body_buf), &http_code);
        free(json_str);
    } else {
        LOG_W("超时取消 JSON 构造失败，仍清理本地会话");
    }

    call_clear_expected_room(cs);

    pthread_mutex_lock(&cs->lock);
    int ended = cs->active && strcmp(cs->room_id, room_id) == 0;
    if (ended) {
        cs->active = 0;
        cs->room_id[0] = '\0';
        cs->role[0]    = '\0';
        cs->active_call_type[0] = '\0';
    }
    cs->ring_timer_running = 0;
    pthread_mutex_unlock(&cs->lock);

    if (ended && cs->on_session_end)
        cs->on_session_end(cs->runtime_user);
    return NULL;
}

void call_session_start_ring_timer(CallState *cs) {
    call_session_cancel_ring_timer(cs);
    pthread_mutex_lock(&cs->lock);
    cs->ring_timer_running = 1;
    if (pthread_create(&cs->ring_timer_thread, NULL, _ring_timer_thread, cs) == 0) {
        cs->ring_timer_thread_created = 1;
    } else {
        cs->ring_timer_running = 0;
        LOG_E("无法创建呼叫超时线程");
    }
    pthread_mutex_unlock(&cs->lock);
}

void call_session_cancel_ring_timer(CallState *cs) {
    pthread_mutex_lock(&cs->lock);
    cs->ring_timer_running = 0;
    int join_timer = cs->ring_timer_thread_created;
    pthread_t timer = cs->ring_timer_thread;
    cs->ring_timer_thread_created = 0;
    pthread_mutex_unlock(&cs->lock);
    if (join_timer && !pthread_equal(pthread_self(), timer))
        pthread_join(timer, NULL);
}

/* ── P2P event callbacks ─────────────────────────────────────────────────── */

void call_session_on_p2p_connected(const char *room_id, void *user) {
    CallState *cs = (CallState *)user;
    if (!cs) return;
    call_session_cancel_ring_timer(cs);
    LOG_I("P2P 建连成功 room_id=%s，通话中", room_id);
}

void call_session_on_connect_failed(void *user) {
    CallState *cs = (CallState *)user;
    if (!cs) return;
    char room_id[128];
    pthread_mutex_lock(&cs->lock);
    STR_COPY(room_id, cs->room_id);
    pthread_mutex_unlock(&cs->lock);
    LOG_W("连接主叫全部失败，调用挂断 room_id=%s", room_id);
    call_session_do_hangup(cs);
}

/* ── HTTP endpoints ──────────────────────────────────────────────────────── */

int call_session_do_call(CallState *cs, const char *target_id, const char *call_type) {
    if (!cs || !target_id) return -1;
    const char *normalized_type =
        call_type ? call_type : (cs->send_video[0] ? "video" : "audio");
    if (strcmp(normalized_type, "video") != 0 &&
        strcmp(normalized_type, "audio") != 0) {
        LOG_W("设备通话类型必须是 video 或 audio");
        return -1;
    }

    pthread_mutex_lock(&cs->lock);
    if (cs->active || cs->pending_call) {
        if (cs->active)
            LOG_W("已在房间 %s 中，不能发起新呼叫", cs->room_id);
        else
            LOG_W("当前有待接设备来电，请先接听或拒绝");
        pthread_mutex_unlock(&cs->lock);
        return -1;
    }
    if (strcmp(normalized_type, "video") == 0 && !cs->send_video[0]) {
        LOG_W("未配置上行视频文件，不能发起视频设备通话");
        pthread_mutex_unlock(&cs->lock);
        return -1;
    }
    pthread_mutex_unlock(&cs->lock);

    if (_before_call_start(cs, 0) != 0)
        return -1;

    /* Build JSON: {"targets": [target_id], "call_type": call_type} */
    cJSON *root = cJSON_CreateObject();
    if (!root) goto failed;
    cJSON *targets = cJSON_CreateArray();
    cJSON *target = cJSON_CreateString(target_id);
    if (!targets || !target) {
        cJSON_Delete(target);
        cJSON_Delete(targets);
        cJSON_Delete(root);
        goto failed;
    }
    cJSON_AddItemToArray(targets, target); /* targets owns target from here. */
    cJSON_AddItemToObject(root, "targets", targets); /* root owns targets from here. */
    if (!cJSON_AddStringToObject(root, "call_type", normalized_type)) {
        cJSON_Delete(root);
        goto failed;
    }
    char *json_str = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json_str) goto failed;

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/request", cs->call_server);

    char body_buf[4096];
    long http_code = 0;
    int ret = _http_post(url, cs->mqtt_token, json_str, body_buf, sizeof(body_buf), &http_code);
    free(json_str);
    if (ret != 0) goto failed;

    int code = _json_resp_code(body_buf);
    if (code != 200) {
        LOG_W("发起呼叫失败（code=%d，响应体已省略）", code);
        goto failed;
    }

    /* Extract room_id */
    cJSON *resp = cJSON_Parse(body_buf);
    if (!resp) goto failed;
    cJSON *data = cJSON_GetObjectItem(resp, "data");
    if (!data) { cJSON_Delete(resp); goto failed; }
    cJSON *rid = cJSON_GetObjectItem(data, "room_id");
    if (!rid || !cJSON_IsString(rid)) {
        cJSON_Delete(resp);
        goto failed;
    }

    pthread_mutex_lock(&cs->lock);
    cs->active = 1;
    STR_COPY(cs->room_id, rid->valuestring); STR_COPY(cs->role, "caller");
    STR_COPY(cs->active_call_type, normalized_type);
    pthread_mutex_unlock(&cs->lock);

    call_set_expected_room(cs, rid->valuestring);
    LOG_I("已发起呼叫 room_id=%s，等待接听（30s 超时）", rid->valuestring);
    call_session_start_ring_timer(cs);

    cJSON_Delete(resp);
    return 0;

failed:
    if (cs->on_session_end) cs->on_session_end(cs->runtime_user);
    return -1;
}

typedef struct {
    CallState *cs;
    uint64_t ticket_generation;
    char caller_id[64];
    char room_id[128];
    char call_type[16];
    char token[512];
} AcceptConnectAction;

static int _accept_connect_action(void *opaque) {
    AcceptConnectAction *action = opaque;
    CallState *cs = action->cs;
    pthread_mutex_lock(&cs->lock);
    int still_pending =
        cs->pending_call &&
        cs->pending_generation == action->ticket_generation &&
        strcmp(cs->pending_room_id, action->room_id) == 0;
    if (!still_pending) {
        pthread_mutex_unlock(&cs->lock);
        LOG_W("来电在启动期间已取消 room_id=%s", action->room_id);
        return -1;
    }
    cs->active = 1;
    cs->pending_call = 0;
    cs->pending_deadline_ms = 0;
    cs->active_generation = action->ticket_generation;
    STR_COPY(cs->room_id, action->room_id);
    STR_COPY(cs->role, "callee");
    STR_COPY(cs->active_call_type,
             strcmp(action->call_type, "audio") == 0 ? "audio" : "video");
    pthread_mutex_unlock(&cs->lock);

    LOG_I("接听成功，正在建立 P2P 连接 room_id=%s", action->room_id);
    call_register_p2p_connected_cb(call_session_on_p2p_connected, cs);
    call_register_connect_failed_cb(call_session_on_connect_failed, cs);
    return call_connect_to(action->caller_id, action->token,
                           action->room_id, 3, 10);
}

int call_session_do_accept(CallState *cs) {
    if (!cs) return -1;

    pthread_mutex_lock(&cs->lock);
    if (!cs->pending_call) {
        LOG_W("当前没有待接听的来电");
        pthread_mutex_unlock(&cs->lock);
        return -1;
    }
    char caller_id[64], room_id[128], call_type[16];
    uint64_t ticket_generation = cs->pending_generation;
    STR_COPY(caller_id, cs->pending_caller_id); STR_COPY(room_id, cs->pending_room_id);
    STR_COPY(call_type, cs->pending_call_type);
    pthread_mutex_unlock(&cs->lock);

    /* POST /v1/call/device/info */
    cJSON *root = cJSON_CreateObject();
    if (!root) return -1;
    if (!cJSON_AddStringToObject(root, "device_id", caller_id) ||
        !cJSON_AddStringToObject(root, "room_id", room_id) ||
        !cJSON_AddStringToObject(root, "purpose", "call")) {
        cJSON_Delete(root);
        return -1;
    }
    char *json_str = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json_str) return -1;

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/device/info", cs->call_server);

    char body_buf[4096];
    long http_code = 0;
    int ret = _http_post(url, cs->mqtt_token, json_str, body_buf, sizeof(body_buf), &http_code);
    free(json_str);
    if (ret != 0) return -1;

    int code = _json_resp_code(body_buf);
    if (code != 200) {
        LOG_W("接听失败（code=%d，响应体已省略）", code);
        return -1;
    }

    /* Extract token */
    cJSON *resp = cJSON_Parse(body_buf);
    if (!resp) return -1;
    cJSON *data = cJSON_GetObjectItem(resp, "data");
    if (!data) { cJSON_Delete(resp); return -1; }
    cJSON *token_json = cJSON_GetObjectItem(data, "token");
    if (!token_json || !cJSON_IsString(token_json)) {
        cJSON_Delete(resp);
        return -1;
    }

    char token[512];
    STR_COPY(token, token_json->valuestring);
    cJSON_Delete(resp);

    /* The token request may take seconds.  Revalidate the exact ticket before
     * claiming RTC so a concurrent room_cancel cannot be resurrected. */
    pthread_mutex_lock(&cs->lock);
    int still_pending =
        cs->pending_call &&
        cs->pending_generation == ticket_generation &&
        strcmp(cs->pending_room_id, room_id) == 0;
    pthread_mutex_unlock(&cs->lock);
    if (!still_pending) {
        LOG_W("来电已在获取 token 期间取消 room_id=%s", room_id);
        return -1;
    }

    if (_before_call_start(cs, 1) != 0)
        return -1;

    AcceptConnectAction action = {
        .cs = cs,
        .ticket_generation = ticket_generation,
    };
    STR_COPY(action.caller_id, caller_id);
    STR_COPY(action.room_id, room_id);
    STR_COPY(action.call_type, call_type);
    STR_COPY(action.token, token);
    if (cs->run_action)
        return cs->run_action(cs->runtime_user, room_id,
                              _accept_connect_action, &action);
    return _accept_connect_action(&action);
}

int call_session_do_reject(CallState *cs, const char *reason) {
    if (!cs) return -1;

    pthread_mutex_lock(&cs->lock);
    if (!cs->pending_call) {
        LOG_W("当前没有待接听的来电");
        pthread_mutex_unlock(&cs->lock);
        return -1;
    }
    char room_id[128];
    STR_COPY(room_id, cs->pending_room_id);
    cs->pending_call = 0;
    cs->pending_deadline_ms = 0;
    cs->incoming_generation++;
    pthread_mutex_unlock(&cs->lock);

    cJSON *root = cJSON_CreateObject();
    if (!root) return -1;
    if (!cJSON_AddStringToObject(root, "room_id", room_id) ||
        !cJSON_AddStringToObject(root, "reason", reason ? reason : "decline")) {
        cJSON_Delete(root);
        LOG_W("拒接请求 JSON 分配失败");
        return -1;
    }
    char *json_str = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json_str) return -1;

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/reject", cs->call_server);

    char body_buf[4096];
    long http_code = 0;
    int ret = _http_post(url, cs->mqtt_token, json_str, body_buf, sizeof(body_buf), &http_code);
    free(json_str);

    if (ret == 0) LOG_I("已拒接");
    return ret;
}

static int _reject_incoming_room(CallState *cs, const char *room_id,
                                 const char *reason) {
    if (!cs || !room_id || !room_id[0]) return -1;
    cJSON *root = cJSON_CreateObject();
    if (!root ||
        !cJSON_AddStringToObject(root, "room_id", room_id) ||
        !cJSON_AddStringToObject(root, "reason",
                                 reason ? reason : "busy")) {
        cJSON_Delete(root);
        return -1;
    }
    char *json = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json) return -1;
    char url[512], body[4096];
    long http_code = 0;
    snprintf(url, sizeof(url), "%s/v1/call/reject", cs->call_server);
    int result = _http_post(url, cs->mqtt_token, json, body, sizeof(body),
                            &http_code);
    free(json);
    if (result == 0)
        LOG_I("忙线拒接设备来电 room=%s", room_id);
    return result;
}

int call_session_reject_incoming_payload(CallState *cs,
                                         const cJSON *payload,
                                         const char *reason) {
    if (!cs || !payload) return -1;
    const cJSON *room = cJSON_GetObjectItemCaseSensitive(payload, "room_id");
    if (!cJSON_IsString(room) || !room->valuestring[0]) return -1;
    return _reject_incoming_room(cs, room->valuestring, reason);
}

static void *_reject_worker(void *opaque) {
    CallState *cs = opaque;
    for (;;) {
        pthread_mutex_lock(&cs->reject_lock);
        while (!cs->reject_stop && cs->reject_count == 0)
            pthread_cond_wait(&cs->reject_ready, &cs->reject_lock);
        if (cs->reject_stop && cs->reject_count == 0) {
            pthread_cond_broadcast(&cs->reject_idle);
            pthread_mutex_unlock(&cs->reject_lock);
            return NULL;
        }
        char room_id[128], reason[32];
        STR_COPY(room_id, cs->reject_queue[cs->reject_head].room_id);
        STR_COPY(reason, cs->reject_queue[cs->reject_head].reason);
        cs->reject_head = (cs->reject_head + 1U) % 16U;
        cs->reject_count--;
        cs->reject_active = 1;
        pthread_mutex_unlock(&cs->reject_lock);

        _reject_incoming_room(cs, room_id, reason);

        pthread_mutex_lock(&cs->reject_lock);
        cs->reject_active = 0;
        if (cs->reject_count == 0)
            pthread_cond_broadcast(&cs->reject_idle);
        pthread_mutex_unlock(&cs->reject_lock);
    }
}

int call_session_reject_incoming_payload_async(CallState *cs,
                                               const cJSON *payload,
                                               const char *reason) {
    if (!cs || !payload) return -1;
    const cJSON *room = cJSON_GetObjectItemCaseSensitive(payload, "room_id");
    if (!cJSON_IsString(room) || !room->valuestring[0]) return -1;
    pthread_mutex_lock(&cs->reject_lock);
    if (!cs->reject_thread_started) {
        if (pthread_create(&cs->reject_thread, NULL, _reject_worker, cs) != 0) {
            pthread_mutex_unlock(&cs->reject_lock);
            LOG_E("无法创建设备忙线拒接工作线程");
            return -1;
        }
        cs->reject_thread_started = 1;
    }
    if (cs->reject_count == 16U) {
        pthread_mutex_unlock(&cs->reject_lock);
        LOG_W("设备忙线拒接队列已满，丢弃 room=%s", room->valuestring);
        return -1;
    }
    unsigned int tail = (cs->reject_head + cs->reject_count) % 16U;
    STR_COPY(cs->reject_queue[tail].room_id, room->valuestring);
    STR_COPY(cs->reject_queue[tail].reason, reason ? reason : "busy");
    cs->reject_count++;
    pthread_cond_signal(&cs->reject_ready);
    pthread_mutex_unlock(&cs->reject_lock);
    return 0;
}

void call_session_shutdown_workers(CallState *cs) {
    if (!cs) return;
    pthread_mutex_lock(&cs->reject_lock);
    cs->reject_stop = 1;
    pthread_cond_broadcast(&cs->reject_ready);
    while (cs->reject_count != 0 || cs->reject_active)
        pthread_cond_wait(&cs->reject_idle, &cs->reject_lock);
    int started = cs->reject_thread_started;
    pthread_t thread = cs->reject_thread;
    pthread_mutex_unlock(&cs->reject_lock);
    if (started && !pthread_equal(pthread_self(), thread))
        pthread_join(thread, NULL);
}

int call_session_do_hangup(CallState *cs) {
    if (!cs) return -1;

    pthread_mutex_lock(&cs->lock);
    if (!cs->active) {
        LOG_W("当前不在通话中");
        pthread_mutex_unlock(&cs->lock);
        return -1;
    }
    char room_id[128];
    STR_COPY(room_id, cs->room_id);
    pthread_mutex_unlock(&cs->lock);

    call_session_cancel_ring_timer(cs);

    cJSON *root = cJSON_CreateObject();
    if (!root) { LOG_W("挂断请求 JSON 分配失败"); goto local_hangup; }
    if (!cJSON_AddStringToObject(root, "room_id", room_id) ||
        !cJSON_AddStringToObject(root, "reason", "hangup")) {
        cJSON_Delete(root);
        LOG_W("挂断请求 JSON 字段分配失败");
        goto local_hangup;
    }
    char *json_str = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json_str) { LOG_W("挂断请求 JSON 序列化失败"); goto local_hangup; }

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/hangup", cs->call_server);

    char body_buf[4096];
    long http_code = 0;
    int ret = _http_post(url, cs->mqtt_token, json_str, body_buf, sizeof(body_buf), &http_code);
    free(json_str);
    if (ret != 0) LOG_W("挂断 HTTP 请求失败");

local_hangup:
    call_hangup();
    call_clear_expected_room(cs);

    pthread_mutex_lock(&cs->lock);
    cs->active = 0;
    cs->room_id[0] = '\0';
    cs->role[0]    = '\0';
    cs->active_call_type[0] = '\0';
    pthread_mutex_unlock(&cs->lock);

    LOG_I("挂断完成");
    if (cs->on_session_end) cs->on_session_end(cs->runtime_user);
    return 0;
}

int call_session_do_cancel(CallState *cs) {
    if (!cs) return -1;

    pthread_mutex_lock(&cs->lock);
    if (!cs->active || strcmp(cs->role, "caller") != 0) {
        LOG_W("当前没有可取消的外呼");
        pthread_mutex_unlock(&cs->lock);
        return -1;
    }
    char room_id[128];
    STR_COPY(room_id, cs->room_id);
    pthread_mutex_unlock(&cs->lock);

    call_session_cancel_ring_timer(cs);
    call_clear_expected_room(cs);
    int ret = -1;

    cJSON *root = cJSON_CreateObject();
    if (!root) { LOG_W("取消呼叫 JSON 分配失败"); goto local_cancel; }
    if (!cJSON_AddStringToObject(root, "room_id", room_id)) {
        cJSON_Delete(root);
        LOG_W("取消呼叫 JSON 字段分配失败");
        goto local_cancel;
    }
    char *json_str = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json_str) { LOG_W("取消呼叫 JSON 序列化失败"); goto local_cancel; }

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/cancel", cs->call_server);

    char body_buf[4096];
    long http_code = 0;
    ret = _http_post(url, cs->mqtt_token, json_str, body_buf, sizeof(body_buf), &http_code);
    free(json_str);
    if (ret != 0) LOG_W("取消呼叫 HTTP 请求失败");

local_cancel:
    pthread_mutex_lock(&cs->lock);
    cs->active = 0;
    cs->room_id[0] = '\0';
    cs->role[0]    = '\0';
    cs->active_call_type[0] = '\0';
    pthread_mutex_unlock(&cs->lock);

    if (ret == 0) LOG_I("已取消呼叫");
    if (cs->on_session_end) cs->on_session_end(cs->runtime_user);
    return ret;
}

int call_session_do_list_contacts(CallState *cs) {
    if (!cs) return -1;

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/device/contacts", cs->call_server);

    char body_buf[8192];
    long http_code = 0;
    int ret = _http_get(url, cs->mqtt_token, body_buf, sizeof(body_buf), &http_code);
    if (ret != 0) return -1;

    int code = _json_resp_code(body_buf);
    if (code != 200) {
        LOG_W("拉取联系人失败（响应体已省略）");
        return -1;
    }

    cJSON *resp = cJSON_Parse(body_buf);
    if (!resp) return -1;
    cJSON *data  = cJSON_GetObjectItem(resp, "data");
    cJSON *contacts = data ? cJSON_GetObjectItem(data, "contacts") : NULL;
    if (!contacts || !cJSON_IsArray(contacts)) {
        LOG_I("联系人列表为空");
        /* Clear cached contact list */
        pthread_mutex_lock(&cs->lock);
        cJSON *old_list = cs->contact_list;
        char **old_ids = cs->contact_device_ids;
        int old_count = cs->contact_count;
        cs->contact_list = NULL;
        cs->contact_device_ids = NULL;
        cs->contact_count = 0;
        pthread_mutex_unlock(&cs->lock);
        cJSON_Delete(old_list);
        if (old_ids) {
            for (int i = 0; i < old_count; ++i) free(old_ids[i]);
            free(old_ids);
        }
        cJSON_Delete(resp);
        return 0;
    }

    /* Cache contacts for index-based commands */
    int count = cJSON_GetArraySize(contacts);
    cJSON *new_list = cJSON_Duplicate(contacts, 1);
    if (!new_list) {
        LOG_E("联系人缓存内存不足");
        cJSON_Delete(resp);
        return -1;
    }
    char **new_ids = NULL;
    if (count > 0) {
        new_ids = calloc((size_t)count, sizeof(*new_ids));
        if (!new_ids) {
            LOG_E("联系人 ID 缓存内存不足");
            cJSON_Delete(new_list);
            cJSON_Delete(resp);
            return -1;
        }
    }

    for (int i = 0; i < count; i++) {
        cJSON *c = cJSON_GetArrayItem(contacts, i);
        if (!c) continue;
        cJSON *did    = cJSON_GetObjectItem(c, "device_id");
        cJSON *type   = cJSON_GetObjectItem(c, "type");
        cJSON *online = cJSON_GetObjectItem(c, "online");
        cJSON *remark = cJSON_GetObjectItem(c, "remark");
        const char *d = did    && cJSON_IsString(did)    ? did->valuestring    : "?";
        const char *t = type   && cJSON_IsString(type)   ? type->valuestring   : "device";
        const char *o = online ? (cJSON_IsTrue(online) ? "online" : "offline") : "-";
        const char *rm = remark && cJSON_IsString(remark) ? remark->valuestring : "-";

        /* Cache device_id for index lookup */
        if (did && cJSON_IsString(did) && did->valuestring[0]) {
            new_ids[i] = strdup(did->valuestring);
            if (!new_ids[i])
                LOG_W("联系人 ID 缓存内存不足: index=%d", i);
        }

        LOG_I("  " C_YELLOW "[%d]" C_RESET " %s  [%s]  online=%s  remark=%s",
              i, d, t, o, rm);
    }

    pthread_mutex_lock(&cs->lock);
    cJSON *old_list = cs->contact_list;
    char **old_ids = cs->contact_device_ids;
    int old_count = cs->contact_count;
    cs->contact_list = new_list;
    cs->contact_device_ids = new_ids;
    cs->contact_count = count;
    pthread_mutex_unlock(&cs->lock);
    cJSON_Delete(old_list);
    if (old_ids) {
        for (int i = 0; i < old_count; ++i) free(old_ids[i]);
        free(old_ids);
    }
    cJSON_Delete(resp);
    return 0;
}

cJSON *call_session_copy_contact(CallState *cs, int index) {
    if (!cs) return NULL;
    pthread_mutex_lock(&cs->lock);
    cJSON *copy = NULL;
    if (cs->contact_list && index >= 0 &&
        index < cJSON_GetArraySize(cs->contact_list))
        copy = cJSON_Duplicate(cJSON_GetArrayItem(cs->contact_list, index), 1);
    pthread_mutex_unlock(&cs->lock);
    return copy;
}

cJSON *call_session_find_contact(CallState *cs, const char *target) {
    if (!cs || !target || !target[0]) return NULL;
    pthread_mutex_lock(&cs->lock);
    cJSON *copy = NULL;
    cJSON *item = NULL;
    cJSON_ArrayForEach(item, cs->contact_list) {
        cJSON *device_id = cJSON_GetObjectItem(item, "device_id");
        cJSON *openid = cJSON_GetObjectItem(item, "wx_open_id");
        if ((cJSON_IsString(device_id) &&
             strcmp(device_id->valuestring, target) == 0) ||
            (cJSON_IsString(openid) &&
             strcmp(openid->valuestring, target) == 0)) {
            copy = cJSON_Duplicate(item, 1);
            break;
        }
    }
    pthread_mutex_unlock(&cs->lock);
    return copy;
}

int call_session_do_list_pending_contacts(CallState *cs) {
    if (!cs) return -1;

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/device/contacts/pending", cs->call_server);

    char body_buf[8192];
    long http_code = 0;
    int ret = _http_get(url, cs->mqtt_token, body_buf, sizeof(body_buf), &http_code);
    if (ret != 0) return -1;
    if (_json_resp_code(body_buf) != 200) {
        LOG_W("拉取待审批联系人申请失败（响应体已省略）");
        return -1;
    }

    cJSON *resp = cJSON_Parse(body_buf);
    cJSON *data = resp ? cJSON_GetObjectItem(resp, "data") : NULL;
    cJSON *pending = data ? cJSON_GetObjectItem(data, "pending") : NULL;
    if (!pending || !cJSON_IsArray(pending) || cJSON_GetArraySize(pending) == 0) {
        LOG_I("没有待审批的联系人申请");
        cJSON_Delete(resp);
        return 0;
    }
    cJSON *item = NULL;
    cJSON_ArrayForEach(item, pending) {
        cJSON *peer = cJSON_GetObjectItem(item, "peer_device_id");
        cJSON *created = cJSON_GetObjectItem(item, "created_at");
        LOG_I("  peer_device_id=%s  created_at=%s",
              peer && cJSON_IsString(peer) ? peer->valuestring : "-",
              created && cJSON_IsString(created) ? created->valuestring : "-");
    }
    cJSON_Delete(resp);
    return 0;
}

int call_session_do_add_contact(CallState *cs, const char *target_id) {
    if (!cs || !target_id) return -1;

    cJSON *root = cJSON_CreateObject();
    if (!root || !cJSON_AddStringToObject(root, "target_device_id", target_id)) {
        cJSON_Delete(root);
        LOG_W("联系人申请 JSON 分配失败");
        return -1;
    }
    char *json_str = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json_str) {
        LOG_W("联系人申请 JSON 序列化失败");
        return -1;
    }

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/device/contacts/request", cs->call_server);

    char body_buf[4096];
    long http_code = 0;
    int ret = _http_post(url, cs->mqtt_token, json_str, body_buf, sizeof(body_buf), &http_code);
    free(json_str);

    if (ret == 0) {
        int code = _json_resp_code(body_buf);
        if (code == 200) LOG_I("申请已发送");
        else LOG_W("发起申请失败（code=%d）", code);
    }
    return ret;
}

int call_session_do_respond_contact(CallState *cs, const char *peer_id, int accept) {
    if (!cs || !peer_id) return -1;

    cJSON *root = cJSON_CreateObject();
    if (!root || !cJSON_AddStringToObject(root, "peer_device_id", peer_id) ||
        !cJSON_AddStringToObject(root, "action", accept ? "accept" : "reject")) {
        cJSON_Delete(root);
        LOG_W("联系人响应 JSON 分配失败");
        return -1;
    }
    char *json_str = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json_str) {
        LOG_W("联系人响应 JSON 序列化失败");
        return -1;
    }

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/device/contacts/respond", cs->call_server);

    char body_buf[4096];
    long http_code = 0;
    int ret = _http_post(url, cs->mqtt_token, json_str, body_buf, sizeof(body_buf), &http_code);
    free(json_str);

    if (ret == 0) LOG_I("已%s", accept ? "同意" : "拒绝");
    return ret;
}

int call_session_do_delete_contact(CallState *cs, const char *peer_id) {
    if (!cs || !peer_id || !peer_id[0]) return -1;
    char url[512], body[4096];
    long http_code = 0;
    snprintf(url, sizeof(url), "%s/v1/call/device/contacts", cs->call_server);
    int result = _http_delete_contact(url, cs->mqtt_token, peer_id,
                                      body, sizeof(body), &http_code);
    if (result != 0) return -1;
    int code = _json_resp_code(body);
    if (http_code != 200 || code != 200) {
        LOG_W("删除联系人失败（HTTP=%ld code=%d）: %s",
              http_code, code, body);
        return -1;
    }
    LOG_I("联系人已删除 peer_id=%s", peer_id);
    return call_session_do_list_contacts(cs);
}

int call_session_do_remark(CallState *cs, const char *peer_id, const char *remark) {
    if (!cs || !peer_id) return -1;

    cJSON *root = cJSON_CreateObject();
    if (!root || !cJSON_AddStringToObject(root, "peer_id", peer_id) ||
        !cJSON_AddStringToObject(root, "remark", remark ? remark : "")) {
        cJSON_Delete(root);
        LOG_W("联系人备注 JSON 分配失败");
        return -1;
    }
    char *json_str = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json_str) {
        LOG_W("联系人备注 JSON 序列化失败");
        return -1;
    }

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/device/contacts/remark", cs->call_server);

    char body_buf[4096];
    long http_code = 0;
    int ret = _http_put(url, cs->mqtt_token, json_str, body_buf, sizeof(body_buf), &http_code);
    free(json_str);

    if (ret == 0) LOG_I("备注已更新");
    return ret;
}

int call_session_do_query_room(CallState *cs) {
    if (!cs) return -1;

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/call/room", cs->call_server);

    char body_buf[4096];
    long http_code = 0;
    int ret = _http_get(url, cs->mqtt_token, body_buf, sizeof(body_buf), &http_code);
    if (ret != 0) return -1;

    int code = _json_resp_code(body_buf);
    if (code != 200) {
        LOG_W("查询房间失败（响应体已省略）");
        return -1;
    }

    cJSON *resp = cJSON_Parse(body_buf);
    if (!resp) return -1;
    cJSON *data = cJSON_GetObjectItem(resp, "data");
    if (!data || cJSON_IsNull(data)) {
        pthread_mutex_lock(&cs->lock);
        cs->active = 0;
        cs->room_id[0] = '\0';
        cs->role[0]    = '\0';
        cs->active_call_type[0] = '\0';
        pthread_mutex_unlock(&cs->lock);
        LOG_I("当前不在任何房间");
        cJSON_Delete(resp);
        return 0;
    }

    cJSON *rid    = cJSON_GetObjectItem(data, "room_id");
    cJSON *status = cJSON_GetObjectItem(data, "status");
    cJSON *role   = cJSON_GetObjectItem(data, "role");
    cJSON *caller = cJSON_GetObjectItem(data, "caller");
    cJSON *ct     = cJSON_GetObjectItem(data, "call_type");

    const char *r_id = rid    && cJSON_IsString(rid)    ? rid->valuestring    : "?";
    const char *st   = status && cJSON_IsString(status) ? status->valuestring : "?";
    const char *rl   = role   && cJSON_IsString(role)   ? role->valuestring   : "?";
    const char *cl   = caller && cJSON_IsString(caller) ? caller->valuestring : "?";
    const char *cct  = ct     && cJSON_IsString(ct)     ? ct->valuestring     : "?";
    const char *selected_type =
        strcmp(cct, "audio") == 0 || !cs->send_video[0]
            ? "audio" : "video";

    /* Sync local state */
    pthread_mutex_lock(&cs->lock);
    if (!cs->active || strcmp(cs->room_id, r_id) != 0) {
        LOG_I("同步房间状态: room_id=%s role=%s", r_id, rl);
    }
    cs->active = 1;
    STR_COPY(cs->room_id, r_id); STR_COPY(cs->role, rl);
    STR_COPY(cs->active_call_type, selected_type);
    pthread_mutex_unlock(&cs->lock);

    LOG_I("当前房间: room_id=%s status=%s role=%s caller=%s type=%s",
          r_id, st, rl, cl, selected_type);

    cJSON_Delete(resp);
    return 0;
}

int call_session_has_pending(CallState *cs) {
    if (!cs) return 0;
    pthread_mutex_lock(&cs->lock);
    int pending = cs->pending_call;
    pthread_mutex_unlock(&cs->lock);
    return pending;
}

int call_session_has_pending_or_outgoing(CallState *cs) {
    if (!cs) return 0;
    pthread_mutex_lock(&cs->lock);
    int busy = cs->pending_call ||
               (cs->active && strcmp(cs->role, "caller") == 0);
    pthread_mutex_unlock(&cs->lock);
    return busy;
}

int call_expire_pending(CallState *cs, char *room_id_out,
                        size_t room_id_size) {
    if (!cs || !room_id_out || room_id_size == 0) return 0;
    pthread_mutex_lock(&cs->lock);
    int expired = cs->pending_call && cs->pending_deadline_ms > 0 &&
                  now_ms() >= cs->pending_deadline_ms;
    if (expired) {
        str_copy(room_id_out, room_id_size, cs->pending_room_id);
        cs->pending_call = 0;
        cs->pending_deadline_ms = 0;
        cs->incoming_generation++;
    }
    pthread_mutex_unlock(&cs->lock);
    if (expired)
        LOG_W("设备来电等待接听超时，已清理 room=%s", room_id_out);
    return expired;
}

/* ── Command dispatch ────────────────────────────────────────────────────── */

/* Resolve arg to device_id: if numeric, look up in contact list; otherwise use as-is. */
static const char *_resolve_peer(CallState *cs, const char *arg) {
    if (!arg || !arg[0]) return NULL;
    /* Check if arg is all digits */
    int is_digit = 1;
    for (const char *p = arg; *p; p++) {
        if (*p < '0' || *p > '9') { is_digit = 0; break; }
    }
    if (!is_digit) return arg; /* use as device_id directly */

    int idx = atoi(arg);
    /* Auto-refresh contact list if empty */
    if (cs->contact_count == 0) {
        call_session_do_list_contacts(cs);
    }
    if (idx < 0 || idx >= cs->contact_count) {
        LOG_W("下标超出范围 [0-%d]，先执行 contacts 刷新列表", cs->contact_count - 1);
        return NULL;
    }
    return cs->contact_device_ids[idx];
}

void call_session_dispatch(CallState *cs, const char *line) {
    if (!cs || !line) return;

    char cmd[32] = "", arg1[256] = "", arg2[256] = "", rest[512] = "";
    sscanf(line, "%31s %255s %255s %511[^\n]", cmd, arg1, arg2, rest);

    if (strcmp(cmd, "exit") == 0) {
        LOG_I("正在退出…");
        pthread_mutex_lock(&cs->lock);
        if (cs->active) {
            pthread_mutex_unlock(&cs->lock);
            call_session_do_hangup(cs);
        } else {
            pthread_mutex_unlock(&cs->lock);
        }
        extern volatile sig_atomic_t g_stop;
        g_stop = 1;
    } else if (strcmp(cmd, "call") == 0) {
        if (!arg1[0]) {
            /* No args: list contacts and prompt for index */
            call_session_do_list_contacts(cs);
            if (cs->contact_count > 0) {
                printf(C_YELLOW "[call] 选择下标 [0-%d]: " C_RESET, cs->contact_count - 1);
                fflush(stdout);
                char idx_buf[16];
                if (fgets(idx_buf, sizeof(idx_buf), stdin)) {
                    /* Strip newline */
                    size_t len = strlen(idx_buf);
                    if (len > 0 && idx_buf[len-1] == '\n') idx_buf[len-1] = '\0';
                    int idx = atoi(idx_buf);
                    if (idx >= 0 && idx < cs->contact_count) {
                        const char *did = cs->contact_device_ids[idx];
                        if (did) call_session_do_call(cs, did, NULL);
                    } else {
                        LOG_W("下标超出范围 [0-%d]", cs->contact_count - 1);
                    }
                }
            }
        } else {
            /* arg1 may be numeric index or device_id */
            const char *target = _resolve_peer(cs, arg1);
            if (target) {
                const char *ct = arg2[0] ? arg2 : NULL;
                call_session_do_call(cs, target, ct);
            }
        }
    } else if (strcmp(cmd, "accept") == 0) {
        call_session_do_accept(cs);
    } else if (strcmp(cmd, "reject") == 0) {
        const char *reason = arg1[0] ? arg1 : "decline";
        call_session_do_reject(cs, reason);
    } else if (strcmp(cmd, "hangup") == 0) {
        call_session_do_hangup(cs);
    } else if (strcmp(cmd, "cancel") == 0) {
        call_session_do_cancel(cs);
    } else if (strcmp(cmd, "contacts") == 0) {
        call_session_do_list_contacts(cs);
    } else if (strcmp(cmd, "pending") == 0) {
        call_session_do_list_pending_contacts(cs);
    } else if (strcmp(cmd, "addcontact") == 0 && arg1[0]) {
        call_session_do_add_contact(cs, arg1);
    } else if ((strcmp(cmd, "delcontact") == 0 ||
                strcmp(cmd, "deletecontact") == 0) && arg1[0]) {
        const char *peer = _resolve_peer(cs, arg1);
        if (peer) call_session_do_delete_contact(cs, peer);
    } else if (strcmp(cmd, "respond") == 0 && arg1[0] && arg2[0]) {
        int accept = (strcmp(arg2, "accept") == 0);
        call_session_do_respond_contact(cs, arg1, accept);
    } else if (strcmp(cmd, "room") == 0) {
        call_session_do_query_room(cs);
    } else if (strcmp(cmd, "remark") == 0 && arg1[0]) {
        const char *peer = _resolve_peer(cs, arg1);
        if (peer) {
            const char *rm;
            if (rest[0]) {
                rm = rest;
                while (*rm == ' ') rm++;
            } else {
                rm = arg2[0] ? arg2 : "";
            }
            call_session_do_remark(cs, peer, rm);
        }
    } else if (strcmp(cmd, "ct") == 0) {
        if (strcmp(arg1, "list") == 0)
            call_session_do_list_contacts(cs);
        else if (strcmp(arg1, "pending") == 0)
            call_session_do_list_pending_contacts(cs);
        else if (strcmp(arg1, "add") == 0 && arg2[0])
            call_session_do_add_contact(cs, arg2);
        else if ((strcmp(arg1, "accept") == 0 ||
                  strcmp(arg1, "reject") == 0) && arg2[0])
            call_session_do_respond_contact(cs, arg2,
                                            strcmp(arg1, "accept") == 0);
        else if ((strcmp(arg1, "del") == 0 ||
                  strcmp(arg1, "delete") == 0) && arg2[0]) {
            const char *peer = _resolve_peer(cs, arg2);
            if (peer) call_session_do_delete_contact(cs, peer);
        } else if (strcmp(arg1, "remark") == 0 && arg2[0]) {
            const char *peer = _resolve_peer(cs, arg2);
            if (peer) call_session_do_remark(cs, peer, rest);
        } else {
            LOG_W("用法: ct list|pending|add|accept|reject|del|remark");
        }
    } else if (cmd[0]) {
        LOG_W("未知命令: %s（可用：call / accept / reject / hangup / cancel / contacts / ct / room / exit）", line);
    }
}
