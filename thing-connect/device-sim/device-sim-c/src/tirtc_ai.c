/** \file tirtc_ai.c
 * \brief TiRTC AI conversation module — WHIP + JSON-RPC + encoded audio push.
 *
 * Linux reference: demonstrates the AI start_session handshake, caption
 * handling, and encoded file-audio streaming. Received AI audio is recorded
 * outside the SDK callback thread; no capture or speaker hardware is used.
 */

#include "tirtc_ai.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <pthread.h>
#include <unistd.h>
#include <time.h>

#include <curl/curl.h>
#include <cjson/cJSON.h>

#include "tirtc/tiRTC.h"
#define LOG_MODULE "ai"
#include "common.h"
#include "audio_recorder.h"
#include "device_adapter.h"
#include "http_tls.h"
#include "media_format.h"
#include "media_rx_log.h"
#include "sdk_callback_guard.h"
#include "tirtc_runtime.h"

extern volatile sig_atomic_t g_stop;

/* ── Local HTTP helper ───────────────────────────────────────────────────── */

static size_t _ai_write_cb(void *ptr, size_t sz, size_t nmemb, void *user) {
    StrBuf *sb = (StrBuf *)user;
    size_t total = sz * nmemb;
    if (sb->len + total >= sb->cap) return 0;
    memcpy(sb->buf + sb->len, ptr, total);
    sb->len += total;
    sb->buf[sb->len] = '\0';
    return total;
}

static int _ai_http_get(const char *url, const char *bearer,
                        char *body_buf, size_t body_cap, long *http_code) {
    CURL *curl = curl_easy_init();
    if (!curl) return -1;
    StrBuf sb; sb_init(&sb, body_buf, body_cap);

    char auth[600];
    snprintf(auth, sizeof(auth), "Authorization: Bearer %s", bearer);
    struct curl_slist *hdrs = curl_slist_append(NULL, auth);

    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 10L);
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, hdrs);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, _ai_write_cb);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &sb);
    http_tls_apply(curl);

    CURLcode res = curl_easy_perform(curl);
    curl_slist_free_all(hdrs);
    if (res != CURLE_OK) {
        LOG_E("HTTP GET %s 失败: %s", url, curl_easy_strerror(res));
        curl_easy_cleanup(curl);
        return -1;
    }
    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, http_code);
    LOG_D("GET %s -> HTTP %ld", url, *http_code);
    curl_easy_cleanup(curl);
    return 0;
}

/* ── AI state ────────────────────────────────────────────────────────────── */

#define AI_CONNECT_TIMEOUT_MS        10000
#define AI_START_RESPONSE_TIMEOUT_MS 10000

struct AiState {
    char ai_server[256];
    char device_id[64];
    char mqtt_token[512];
    char ai_audio[512];
    char receive_dir[512];
    const AudioFormat *up_audio_format;
    const AudioFormat *down_audio_format;

    pthread_mutex_t lock;
    SessionState    session_state;
    int             active;
    tirtc_conn_t    hconn;
    uint64_t        connect_generation;
    int64_t         connect_deadline_ms;
    int64_t         start_response_deadline_ms;

    /* Encoded audio file push and downlink recording. */
    pthread_t       push_thread;
    int             push_thread_created;
    int             push_running;
    char            push_audio_path[512];

    /* Session params */
    char            role_id[64];

    /* start_session deferred send (polled by cmd loop) */
    int             start_pending;
    int64_t         start_send_at_ms;

    /* push thread start deferred (polled by cmd loop, avoids pthread_create in SDK cb) */
    int             push_needed;
    MediaRxLog      rx_log;
    AudioRecorder   recorder;

    ai_session_end_cb on_session_end;
    void             *on_session_end_user;
};

/* Active AI business state selected by the process runtime. */
static AiState     *s_active_ai = NULL;
static pthread_mutex_t s_ai_mtx = PTHREAD_MUTEX_INITIALIZER;

AiState *ai_create_ex(const char *ai_server, const char *device_id,
                      const char *mqtt_token, const char *ai_audio,
                      const char *up_audio_format,
                      const char *down_audio_format) {
    AiState *as = (AiState *)calloc(1, sizeof(AiState));
    if (!as) return NULL;
    STR_COPY(as->ai_server, ai_server); STR_COPY(as->device_id, device_id);
    STR_COPY(as->mqtt_token, mqtt_token); STR_COPY(as->ai_audio, ai_audio);
    as->up_audio_format = audio_format_find(up_audio_format);
    as->down_audio_format = audio_format_find(down_audio_format);
    if (!as->up_audio_format || !as->down_audio_format ||
        !audio_format_ai_codec(as->up_audio_format) ||
        !audio_format_ai_codec(as->down_audio_format)) {
        free(as);
        return NULL;
    }
    STR_COPY(as->receive_dir, "received");
    pthread_mutex_init(&as->lock, NULL);
    pthread_mutex_init(&as->rx_log.lock, NULL);
    if (audio_recorder_init(&as->recorder) != 0) {
        pthread_mutex_destroy(&as->rx_log.lock);
        pthread_mutex_destroy(&as->lock);
        free(as);
        return NULL;
    }
    as->session_state = SESS_IDLE;
    return as;
}

AiState *ai_create(const char *ai_server, const char *device_id,
                   const char *mqtt_token, const char *ai_audio,
                   const char *down_audio_format) {
    return ai_create_ex(ai_server, device_id, mqtt_token, ai_audio,
                        "alaw_8khz", down_audio_format);
}

void ai_destroy(AiState *as) {
    if (!as) return;
    ai_stop_session(as);
    pthread_mutex_lock(&s_ai_mtx);
    if (s_active_ai == as) s_active_ai = NULL;
    pthread_mutex_unlock(&s_ai_mtx);
    audio_recorder_destroy(&as->recorder);
    pthread_mutex_destroy(&as->rx_log.lock);
    pthread_mutex_destroy(&as->lock);
    free(as);
}

void ai_configure_receive_dir(AiState *as, const char *receive_dir) {
    if (!as) return;
    pthread_mutex_lock(&as->lock);
    STR_COPY(as->receive_dir,
             receive_dir && receive_dir[0] ? receive_dir : "received");
    pthread_mutex_unlock(&as->lock);
}

void ai_set_session_end_callback(AiState *as, ai_session_end_cb cb, void *user) {
    if (!as) return;
    pthread_mutex_lock(&as->lock);
    as->on_session_end = cb;
    as->on_session_end_user = user;
    pthread_mutex_unlock(&as->lock);
}

/* ── Process-runtime callbacks ───────────────────────────────────────────── */

static SdkCallbackGuard s_ai_callback_guard = SDK_CALLBACK_GUARD_INITIALIZER;

/* Forward declarations */
static void _ai_handle_message(AiState *as, tirtc_conn_t hconn, const char *json_str);
static void _ai_handle_disconnect(AiState *as, tirtc_conn_t expected_hconn);
static void *_ai_push_thread(void *arg);
static void _ai_send_deferred_start(AiState *as);

typedef struct {
    AiState *as;
    uint64_t generation;
} AiTerminalCleanup;

static void _ai_terminal_cleanup_deferred(void *context, const void *data,
                                          size_t length) {
    (void)context;
    if (!data || length != sizeof(AiTerminalCleanup)) return;
    const AiTerminalCleanup *cleanup = data;
    AiState *as = cleanup->as;
    if (!as) return;
    pthread_mutex_lock(&as->lock);
    int current = as->connect_generation == cleanup->generation &&
                  as->session_state == SESS_IDLE;
    pthread_mutex_unlock(&as->lock);
    if (!current) return;

    audio_recorder_close(&as->recorder);
    pthread_mutex_lock(&as->lock);
    ai_session_end_cb callback = as->on_session_end;
    void *user = as->on_session_end_user;
    pthread_mutex_unlock(&as->lock);
    if (callback) callback(user);
}

static AiState *_active_ai(void) {
    pthread_mutex_lock(&s_ai_mtx);
    AiState *as = s_active_ai;
    pthread_mutex_unlock(&s_ai_mtx);
    return as;
}

static int _ai_hconn_matches(AiState *as, tirtc_conn_t hconn) {
    if (!as) return 0;
    pthread_mutex_lock(&as->lock);
    int matched = as->hconn == hconn && as->session_state != SESS_IDLE;
    pthread_mutex_unlock(&as->lock);
    return matched;
}

static void _ai_disconnect_deferred(void *opaque) {
    tirtc_conn_t hconn = (tirtc_conn_t)opaque;
    AiState *as = _active_ai();
    _ai_handle_disconnect(as, hconn);
    TiRtcDisconnect(hconn);
}

static void _ai_finish_disconnect_deferred(void *opaque) {
    tirtc_conn_t hconn = (tirtc_conn_t)opaque;
    AiState *as = _active_ai();
    _ai_handle_disconnect(as, hconn);
}

static void _a_on_conn_error(tirtc_conn_t hconn, int error) {
    sdk_callback_enter(&s_ai_callback_guard);
    LOG_E("on_conn_error: %s", TiRtcGetErrorStr(error));
    if (sdk_defer_action(&s_ai_callback_guard, _ai_disconnect_deferred,
                         hconn) != 0)
        LOG_E("无法延后清理 AI 错误连接");
    sdk_callback_leave(&s_ai_callback_guard);
}

static void _a_on_disconnected(tirtc_conn_t hconn) {
    sdk_callback_enter(&s_ai_callback_guard);
    LOG_D("on_disconnected");
    if (sdk_defer_action(&s_ai_callback_guard,
                         _ai_finish_disconnect_deferred, hconn) != 0)
        LOG_E("无法延后清理 AI 断开连接");
    sdk_callback_leave(&s_ai_callback_guard);
}

static void _ai_audio_notice_emit(void *context, const void *data,
                                  size_t length) {
    (void)context;
    if (!data || length != sizeof(MediaRxNotice)) return;
    const MediaRxNotice *notice = data;
    if (notice->count == 1) {
        LOG_I("收到首帧下行 AI 音频: media=%u flags=%u bytes=%u",
              notice->media, notice->flags, notice->length);
    } else {
        LOG_D("下行 AI 音频累计: frames=%llu last_bytes=%u",
              (unsigned long long)notice->count, notice->length);
    }
}

static void _a_on_audio(tirtc_conn_t hconn, const TIRTCFRAMEINFO *pFi, void *data) {
    sdk_callback_enter(&s_ai_callback_guard);
    AiState *as = _active_ai();
    MediaRxNotice notice;
    if (_ai_hconn_matches(as, hconn)) {
        int sink_result = device_media_sink_submit(
            DEVICE_BUSINESS_AI, 0, pFi->stream_id, pFi->media, pFi->flags,
            pFi->ts, data, pFi->length);
        if (sink_result == DEVICE_ADAPTER_NOT_HANDLED)
            (void)audio_recorder_submit(&as->recorder, pFi, data);
        if (media_rx_log_note_audio(&as->rx_log, "AI", pFi, &notice))
            (void)sdk_defer_copy_action(
                &s_ai_callback_guard, _ai_audio_notice_emit, NULL,
                &notice, sizeof(notice));
    }
    sdk_callback_leave(&s_ai_callback_guard);
}

static void _a_on_video(tirtc_conn_t hconn, const TIRTCFRAMEINFO *pFi, void *data) {
    sdk_callback_enter(&s_ai_callback_guard);
    AiState *as = _active_ai();
    MediaRxNotice notice;
    if (_ai_hconn_matches(as, hconn)) {
        (void)device_media_sink_submit(
            DEVICE_BUSINESS_AI, 1, pFi->stream_id, pFi->media, pFi->flags,
            pFi->ts, data, pFi->length);
        if (media_rx_log_note_video(&as->rx_log, "AI", pFi, &notice))
            (void)sdk_defer_copy_action(
                &s_ai_callback_guard, media_rx_log_emit, NULL,
                &notice, sizeof(notice));
    }
    sdk_callback_leave(&s_ai_callback_guard);
}

static void _ai_command_deferred(void *context, const void *data,
                                 size_t length) {
    tirtc_conn_t hconn = (tirtc_conn_t)context;
    if (!data || length == 0 ||
        length > SDK_CALLBACK_COPY_CAPACITY)
        return;

    char json_str[SDK_CALLBACK_COPY_CAPACITY + 1];
    memcpy(json_str, data, length);
    json_str[length] = '\0';

    AiState *as = _active_ai();
    if (_ai_hconn_matches(as, hconn))
        _ai_handle_message(as, hconn, json_str);
}

static void _a_on_command(tirtc_conn_t hconn, uint32_t cmdw, const void *data, uint32_t len) {
    sdk_callback_enter(&s_ai_callback_guard);
    if (cmdw != CMD_AI || !data || len == 0) {
        sdk_callback_leave(&s_ai_callback_guard);
        return;
    }
    if (sdk_defer_copy_action(&s_ai_callback_guard,
                              _ai_command_deferred, hconn,
                              data, len) != 0)
        LOG_E("AI 命令超出控制队列容量或队列已满，已丢弃 len=%u", len);
    sdk_callback_leave(&s_ai_callback_guard);
}

static void _a_nop_4(tirtc_conn_t h, const TIRTCFRAMEINFO *f, void *d) {
    sdk_callback_enter(&s_ai_callback_guard);
    (void)h;(void)f;(void)d;
    sdk_callback_leave(&s_ai_callback_guard);
}
static void _a_nop_unsub(tirtc_conn_t h, uint8_t s) {
    sdk_callback_enter(&s_ai_callback_guard);
    (void)h;(void)s;
    sdk_callback_leave(&s_ai_callback_guard);
}
static int _a_ret0(tirtc_conn_t h, uint8_t s) {
    sdk_callback_enter(&s_ai_callback_guard);
    (void)h;(void)s;
    sdk_callback_leave(&s_ai_callback_guard);
    return 0;
}

int ai_service_register(void) {
    TIRTCCALLBACKS cbs;
    memset(&cbs, 0, sizeof(cbs));
    cbs.on_conn_error        = _a_on_conn_error;
    cbs.on_disconnected      = _a_on_disconnected;
    cbs.on_audio             = _a_on_audio;
    cbs.on_video             = _a_on_video;
    cbs.on_message           = _a_nop_4;
    cbs.on_command           = _a_on_command;
    cbs.on_request_key_frame = _a_nop_unsub;
    cbs.on_subscribe_video   = _a_ret0;
    cbs.on_unsubscribe_video = _a_nop_unsub;
    cbs.on_subscribe_audio   = _a_ret0;
    cbs.on_unsubscribe_audio = _a_nop_unsub;
    return tirtc_runtime_register_service(
        TIRTC_SERVICE_AI, &cbs, &s_ai_callback_guard);
}

int ai_service_start(AiState *as) {
    if (!as) return -1;
    pthread_mutex_lock(&s_ai_mtx);
    s_active_ai = as;
    pthread_mutex_unlock(&s_ai_mtx);
    LOG_I("AI 业务已就绪");
    return 0;
}

void ai_service_stop(AiState *as) {
    if (!as) return;
    ai_stop_session(as);
    sdk_callback_wait_all(&s_ai_callback_guard);
    pthread_mutex_lock(&s_ai_mtx);
    if (s_active_ai == as) s_active_ai = NULL;
    pthread_mutex_unlock(&s_ai_mtx);
    LOG_I("AI 业务已停止");
}

/* ── AI token ────────────────────────────────────────────────────────────── */

int ai_get_token(const char *ai_server, const char *mqtt_token,
                 const char *device_id,
                 char *peer_id_out, size_t pid_size,
                 char *token_out, size_t tok_size,
                 char *role_id_out, size_t rid_size) {
    (void)device_id;  /* used only for context, token endpoint doesn't require it as param */
    char url[512];
    snprintf(url, sizeof(url), "%s/v1/ai/token", ai_server);

    char body_buf[4096];
    long http_code = 0;
    if (_ai_http_get(url, mqtt_token, body_buf, sizeof(body_buf), &http_code) != 0) {
        LOG_D("GET %s -> HTTP %ld", url, http_code);
        LOG_E("AI token 请求失败");
        return -1;
    }

    cJSON *root = cJSON_Parse(body_buf);
    if (!root) { LOG_E("AI token 响应非有效 JSON"); return -1; }

    cJSON *code = cJSON_GetObjectItem(root, "code");
    if (!code || !cJSON_IsNumber(code) || code->valueint != 200) {
        cJSON *msg = cJSON_GetObjectItem(root, "msg");
        LOG_D("GET %s -> HTTP %ld", url, http_code);
        LOG_E("AI token 获取失败 code=%d msg=%s",
              code ? code->valueint : -1,
              (msg && cJSON_IsString(msg)) ? msg->valuestring : "?");
        cJSON_Delete(root);
        return -1;
    }

    cJSON *data = cJSON_GetObjectItem(root, "data");
    if (data) {
        cJSON *pid  = cJSON_GetObjectItem(data, "peer_id");
        cJSON *tok  = cJSON_GetObjectItem(data, "token");
        cJSON *rid  = cJSON_GetObjectItem(data, "role_id");
        if (pid && cJSON_IsString(pid)) str_copy(peer_id_out, pid_size, pid->valuestring);
        if (tok && cJSON_IsString(tok)) str_copy(token_out, tok_size, tok->valuestring);
        if (rid && cJSON_IsString(rid)) str_copy(role_id_out, rid_size, rid->valuestring);
    }
    LOG_D("GET %s -> HTTP %ld", url, http_code);
    cJSON_Delete(root);
    LOG_I("AI token 获取成功");
    return 0;
}

/* ── AI message handling ─────────────────────────────────────────────────── */

static void _ai_handle_message(AiState *as, tirtc_conn_t hconn, const char *json_str) {
    cJSON *msg = cJSON_Parse(json_str);
    if (!msg) { LOG_E("JSON parse failed: %.100s", json_str); return; }

    /* Check if response (has "result" or "error" without "method") */
    cJSON *method  = cJSON_GetObjectItem(msg, "method");
    cJSON *result  = cJSON_GetObjectItem(msg, "result");
    cJSON *error   = cJSON_GetObjectItem(msg, "error");
    int    is_resp = (result || error) && !method;

    if (is_resp) {
        pthread_mutex_lock(&as->lock);
        as->start_response_deadline_ms = 0;
        pthread_mutex_unlock(&as->lock);
        if (error) {
            cJSON *message = cJSON_IsObject(error)
                                 ? cJSON_GetObjectItem(error, "message")
                                 : NULL;
            const char *error_message = "?";
            if (message != NULL && cJSON_IsString(message) &&
                message->valuestring != NULL)
                error_message = message->valuestring;
            LOG_E("start_session failed: %s", error_message);
            cJSON_Delete(msg);
            if (sdk_defer_action(&s_ai_callback_guard,
                                 _ai_disconnect_deferred, hconn) != 0)
                LOG_E("无法延后清理 start_session 失败会话");
            return;
        }
        cJSON *res = result ? cJSON_GetObjectItem(result, "session_id") : NULL;
        LOG_I("start_session OK session_id=%s",
              (res && cJSON_IsString(res)) ? res->valuestring : "?");

        /* Defer push thread start to cmd loop (pthread_create unsafe in SDK callback) */
        pthread_mutex_lock(&as->lock);
        as->active = 1;
        as->session_state = SESS_IN_CALL;
        as->push_running = 1;
        STR_COPY(as->push_audio_path, as->ai_audio);
        as->push_needed = 1;
        pthread_mutex_unlock(&as->lock);
        LOG_I("start_session OK, push thread will start from cmd loop...");
        cJSON_Delete(msg);
        return;
    }

    /* Notification / Request */
    const char *m = method && cJSON_IsString(method) ? method->valuestring : "";
    cJSON *params = cJSON_GetObjectItem(msg, "params");
    cJSON *msg_id = cJSON_GetObjectItem(msg, "id");

    if (strcmp(m, "caption") == 0 && params) {
        int ct = cJSON_GetObjectItem(params, "caption_type") ?
            cJSON_GetObjectItem(params, "caption_type")->valueint : 0;
        const char *text = cJSON_GetObjectItem(params, "text") ?
            (cJSON_GetObjectItem(params, "text")->valuestring ? cJSON_GetObjectItem(params, "text")->valuestring : "") : "";
        int is_final = cJSON_GetObjectItem(params, "is_final") ?
            cJSON_GetObjectItem(params, "is_final")->valueint : 0;
        LOG_I("[%s] %s%s", ct == 0 ? "ASR" : "TTS", text, is_final ? " [final]" : "");
    }
    else if (strcmp(m, "round_start") == 0) {
        LOG_I("---- Round started ----");
    }
    else if (strcmp(m, "round_end") == 0) {
        LOG_I("---- User speech ended, waiting for AI reply ----");
    }
    else if (strcmp(m, "end_session") == 0) {
        LOG_I("AI server ended session");
        if (sdk_defer_action(&s_ai_callback_guard, _ai_disconnect_deferred,
                             hconn) != 0)
            LOG_E("无法延后结束 AI 会话");
    }
    else if (strcmp(m, "device_action") == 0 && msg_id) {
        /* Auto-respond with success */
        cJSON *reply = cJSON_CreateObject();
        cJSON *reply_id = cJSON_Duplicate(msg_id, 1);
        cJSON *result = cJSON_CreateObject();
        char *reply_str = NULL;
        if (reply && reply_id && result && cJSON_AddStringToObject(reply, "jsonrpc", "2.0")) {
            cJSON_AddItemToObject(reply, "id", reply_id);
            cJSON_AddItemToObject(reply, "result", result);
            reply_id = NULL;
            result = NULL;
            reply_str = cJSON_PrintUnformatted(reply);
        }
        cJSON_Delete(reply_id);
        cJSON_Delete(result);
        if (reply_str) {
            TiRtcSendCommand(hconn, CMD_AI, reply_str, (uint32_t)strlen(reply_str));
            free(reply_str);
        } else {
            LOG_W("AI device_action 响应构造失败");
        }
        cJSON_Delete(reply);
    }

    cJSON_Delete(msg);
}

static void _ai_handle_disconnect(AiState *as, tirtc_conn_t expected_hconn) {
    if (!as) return;
    pthread_mutex_lock(&as->lock);
    if (!expected_hconn || as->hconn != expected_hconn ||
        as->session_state == SESS_IDLE ||
        as->session_state == SESS_DISCONNECTING) {
        pthread_mutex_unlock(&as->lock);
        return;
    }
    as->push_running = 0;
    int join_push = as->push_thread_created;
    pthread_t push_thread = as->push_thread;
    as->push_thread_created = 0;
    as->active = 0;
    as->hconn = NULL;
    as->start_pending = 0;
    as->push_needed = 0;
    as->connect_deadline_ms = 0;
    as->start_response_deadline_ms = 0;
    as->session_state = SESS_DISCONNECTING;
    pthread_mutex_unlock(&as->lock);
    if (join_push && !pthread_equal(pthread_self(), push_thread))
        pthread_join(push_thread, NULL);
    audio_recorder_close(&as->recorder);
    pthread_mutex_lock(&as->lock);
    as->session_state = SESS_IDLE;
    pthread_mutex_unlock(&as->lock);
    LOG_I("连接已断开，回到 IDLE");
    if (as->on_session_end) as->on_session_end(as->on_session_end_user);
}

/* ── Encoded file-audio push thread ─────────────────────────────────────── */

static void *_ai_push_thread(void *arg) {
    AiState *as = (AiState *)arg;
    char audio_path[512];
    const AudioFormat *audio_format;
    pthread_mutex_lock(&as->lock);
    STR_COPY(audio_path, as->push_audio_path);
    audio_format = as->up_audio_format;
    pthread_mutex_unlock(&as->lock);

    LOG_D("AI 上行音频线程启动: %s (%s)", audio_path, audio_format->name);
    DeviceMediaSource source;
    DeviceMediaSourceConfig media_config = {
        .audio_locator = audio_path,
        .audio_format = audio_format->name,
        .video_locator = "",
        .video_format = "",
        .audio_packet_ms = AUDIO_PKT_MS_AI,
        .business = DEVICE_BUSINESS_AI,
    };
    if (device_media_source_open(&source, &media_config) != 0) {
        LOG_E("无法打开 AI 上行音频源: %s", audio_path);
        pthread_mutex_lock(&as->lock);
        tirtc_conn_t hconn = as->hconn;
        pthread_mutex_unlock(&as->lock);
        if (hconn) TiRtcDisconnect(hconn);
        return NULL;
    }

    double pts_ms = 0.0;
    int64_t wall_start = now_ms();

    while (!g_stop) {
        pthread_mutex_lock(&as->lock);
        int running = as->push_running;
        tirtc_conn_t hconn = as->hconn;
        pthread_mutex_unlock(&as->lock);
        if (!running || !hconn) break;
        DeviceMediaPacket packet;
        int source_result = device_media_source_next_audio(&source, &packet);
        if (source_result <= 0) {
            if (source_result == 0)
                LOG_I("AI 上行音频源发送完毕，等待服务端 VAD");
            else
                LOG_E("AI 上行音频源读取失败: %d", source_result);
            break;
        }

        /* Pacing */
        int64_t elapsed = now_ms() - wall_start;
        int64_t wait_ms = (int64_t)pts_ms - elapsed;
        if (wait_ms > 2) {
            sleep_ms((int)(wait_ms > 50 ? 50 : wait_ms));
            pthread_mutex_lock(&as->lock);
            running = as->push_running;
            pthread_mutex_unlock(&as->lock);
            if (!running || g_stop) break;
        }

        TIRTCFRAMEINFO fi;
        memset(&fi, 0, sizeof(fi));
        fi.stream_id = STREAM_ID_AI;
        fi.media = audio_format->media;
        fi.flags = audio_format->flags;
        fi.ts = (uint32_t)pts_ms;
        fi.length = (uint32_t)packet.length;

        int rc = TiRtcSendAudioStream(hconn, &fi, packet.data);
        if (rc == TIRTC_E_CONN_TIMEOUTCLOSE || rc == TIRTC_E_CONN_REMOTECLOSE ||
            rc == TIRTC_E_CONN_OTHER_ERROR) {
            LOG_D("连接已关闭，退出推流");
            break;
        } else if (rc == TIRTC_E_INVALID_HANDLE) {
            sleep_ms(5);
        }
        pts_ms += packet.duration_ms;
    }

    device_media_source_close(&source);
    LOG_D("音频推流线程退出");
    return NULL;
}

/* ── Session management ──────────────────────────────────────────────────── */

typedef struct {
    AiState *as;
    uint64_t generation;
} AiConnectContext;

static void _ai_connect_cb(int error, tirtc_conn_t hconn, void *user_data) {
    sdk_callback_enter(&s_ai_callback_guard);
    AiConnectContext *context = user_data;
    AiState *as = context->as;
    uint64_t generation = context->generation;
    free(context);

    pthread_mutex_lock(&as->lock);
    int current = as->connect_generation == generation &&
                  as->session_state == SESS_CONNECTING;
    if (!current) {
        pthread_mutex_unlock(&as->lock);
        if (error == 0 && hconn &&
            sdk_defer_disconnect(&s_ai_callback_guard, hconn) != 0)
            LOG_E("无法延后断开过期 AI 连接");
        sdk_callback_leave(&s_ai_callback_guard);
        return;
    }
    if (error != 0 || !hconn) {
        LOG_E("TiRtcWhipConnect 失败: rc=%d %s", error, TiRtcGetErrorStr(error));
        as->hconn = NULL;
        as->start_pending = 0;
        as->connect_deadline_ms = 0;
        as->start_response_deadline_ms = 0;
        as->session_state = SESS_IDLE;
        pthread_mutex_unlock(&as->lock);
        AiTerminalCleanup cleanup = {as, generation};
        if (sdk_defer_copy_action(
                &s_ai_callback_guard, _ai_terminal_cleanup_deferred, NULL,
                &cleanup, sizeof(cleanup)) != 0)
            LOG_E("无法延后通知 AI 连接失败");
        sdk_callback_leave(&s_ai_callback_guard);
        return;
    }

    if (tirtc_runtime_bind_active_connection(TIRTC_SERVICE_AI, hconn) != 0) {
        as->hconn = NULL;
        as->start_pending = 0;
        as->connect_deadline_ms = 0;
        as->start_response_deadline_ms = 0;
        as->session_state = SESS_IDLE;
        pthread_mutex_unlock(&as->lock);
        LOG_W("WHIP 连接完成时 AI 会话已切换，丢弃连接");
        if (sdk_defer_disconnect(&s_ai_callback_guard, hconn) != 0)
            LOG_E("无法延后断开失效 AI 连接");
        AiTerminalCleanup cleanup = {as, generation};
        if (sdk_defer_copy_action(
                &s_ai_callback_guard, _ai_terminal_cleanup_deferred, NULL,
                &cleanup, sizeof(cleanup)) != 0)
            LOG_E("无法延后通知 AI runtime 代次失效");
        sdk_callback_leave(&s_ai_callback_guard);
        return;
    }
    LOG_I("WHIP 连接成功");
    as->hconn = hconn;
    as->connect_deadline_ms = 0;
    as->start_response_deadline_ms = 0;
    as->start_pending = 1;
    as->start_send_at_ms = now_ms() + 300;  /* KCP handshake delay */
    pthread_mutex_unlock(&as->lock);
    sdk_callback_leave(&s_ai_callback_guard);
}

int ai_start_session(AiState *as, const char *peer_id, const char *token,
                     const char *audio_path, const char *device_id,
                     const char *role_id) {
    char receive_dir[512];
    pthread_mutex_lock(&as->lock);
    if (as->session_state != SESS_IDLE) {
        LOG_E("Already in a session");
        pthread_mutex_unlock(&as->lock);
        return -1;
    }

    STR_COPY(as->ai_audio, audio_path); STR_COPY(as->device_id, device_id);
    STR_COPY(as->role_id, role_id);
    as->hconn = NULL;
    as->start_pending = 0;
    as->push_needed = 0;
    as->connect_deadline_ms = 0;
    as->start_response_deadline_ms = 0;
    as->connect_generation++;
    uint64_t generation = as->connect_generation;
    as->session_state = SESS_CONNECTING;
    STR_COPY(receive_dir, as->receive_dir);
    pthread_mutex_unlock(&as->lock);
    media_rx_log_reset(&as->rx_log);

    if (!device_media_sink_is_enabled(DEVICE_BUSINESS_AI, 0)) {
        char receive_name[96];
        snprintf(receive_name, sizeof(receive_name), "ai_%lld.raw",
                 (long long)time(NULL));
        if (audio_recorder_open(&as->recorder, receive_dir, device_id,
                                receive_name) != 0) {
            pthread_mutex_lock(&as->lock);
            if (as->connect_generation == generation)
                as->session_state = SESS_IDLE;
            pthread_mutex_unlock(&as->lock);
            LOG_E("无法创建 AI 接收音频文件: dir=%s device=%s",
                  receive_dir, device_id);
            return -1;
        }
        LOG_I("接收 AI 音频 -> %s",
              audio_recorder_raw_path(&as->recorder));
    } else {
        LOG_I("下行 AI 音频交给产品 media sink");
    }
    LOG_I("下行 AI 视频：交给已启用的产品 sink，否则仅记录日志");
    LOG_I("Starting AI session device_id=%s role_id=%s", device_id, role_id);
    pthread_mutex_lock(&s_ai_mtx);
    s_active_ai = as;
    pthread_mutex_unlock(&s_ai_mtx);

    AiConnectContext *context = calloc(1, sizeof(*context));
    if (!context) {
        pthread_mutex_lock(&as->lock);
        as->session_state = SESS_IDLE;
        pthread_mutex_unlock(&as->lock);
        audio_recorder_close(&as->recorder);
        return -1;
    }
    context->as = as;
    context->generation = generation;
    int rc = TiRtcWhipConnect(peer_id, token, _ai_connect_cb, context);
    if (rc != 0) {
        LOG_E("TiRtcWhipConnect 调用失败: rc=%d %s", rc, TiRtcGetErrorStr(rc));
        free(context);
        pthread_mutex_lock(&as->lock);
        if (as->connect_generation == generation)
            as->session_state = SESS_IDLE;
        pthread_mutex_unlock(&as->lock);
        audio_recorder_close(&as->recorder);
        if (as->on_session_end) as->on_session_end(as->on_session_end_user);
        return -1;
    }
    pthread_mutex_lock(&as->lock);
    if (as->connect_generation == generation &&
        as->session_state == SESS_CONNECTING && !as->hconn)
        as->connect_deadline_ms = now_ms() + AI_CONNECT_TIMEOUT_MS;
    pthread_mutex_unlock(&as->lock);

    /* start_session will be sent from ai_cmd_loop when start_pending fires.
     * See _ai_connect_cb and _ai_poll_start_session below. */
    return 0;
}

void ai_stop_session(AiState *as) {
    if (!as) return;
    pthread_mutex_lock(&as->lock);
    int had_session = as->session_state != SESS_IDLE;
    as->connect_generation++;
    as->push_running = 0;
    int join_push = as->push_thread_created;
    pthread_t push_thread = as->push_thread;
    as->push_thread_created = 0;
    tirtc_conn_t h = as->hconn;
    as->hconn = NULL;
    as->active = 0;
    as->session_state = had_session ? SESS_DISCONNECTING : SESS_IDLE;
    if (h) {
        LOG_I("发送 end_session");
    }
    as->start_pending = 0;
    as->push_needed   = 0;
    as->connect_deadline_ms = 0;
    as->start_response_deadline_ms = 0;
    pthread_mutex_unlock(&as->lock);
    if (h) {
        const char *end_msg = "{\"jsonrpc\":\"2.0\",\"method\":\"end_session\"}";
        TiRtcSendCommand(h, CMD_AI, end_msg, (uint32_t)strlen(end_msg));
        TiRtcDisconnect(h);
    }
    if (join_push && !pthread_equal(pthread_self(), push_thread))
        pthread_join(push_thread, NULL);
    audio_recorder_close(&as->recorder);
    pthread_mutex_lock(&as->lock);
    if (as->session_state == SESS_DISCONNECTING)
        as->session_state = SESS_IDLE;
    pthread_mutex_unlock(&as->lock);
    LOG_I("会话已停止");
    if (had_session && as->on_session_end) as->on_session_end(as->on_session_end_user);
}

int ai_is_active(const AiState *as) {
    if (!as) return 0;
    AiState *mutable_as = (AiState *)as;
    pthread_mutex_lock(&mutable_as->lock);
    int active = mutable_as->session_state != SESS_IDLE;
    pthread_mutex_unlock(&mutable_as->lock);
    return active;
}

void ai_poll(AiState *as) {
    if (!as) return;
    pthread_mutex_lock(&as->lock);
    int64_t now = now_ms();
    int connect_expired =
        as->session_state == SESS_CONNECTING && !as->hconn &&
        as->connect_deadline_ms > 0 && now >= as->connect_deadline_ms;
    int response_expired =
        as->session_state == SESS_CONNECTING && as->hconn &&
        !as->start_pending && as->start_response_deadline_ms > 0 &&
        now >= as->start_response_deadline_ms;
    int send_start = as->session_state == SESS_CONNECTING &&
                     as->start_pending &&
                     now >= as->start_send_at_ms;
    pthread_mutex_unlock(&as->lock);
    if (connect_expired || response_expired) {
        LOG_W("%s超时，结束 AI 会话",
              connect_expired ? "等待 WHIP 连接回调" :
                                "等待 start_session 响应");
        ai_stop_session(as);
        return;
    }
    if (send_start)
        _ai_send_deferred_start(as);
    pthread_mutex_lock(&as->lock);
    int start_push = as->session_state == SESS_IN_CALL &&
                     as->push_needed;
    int create_rc = 0;
    if (start_push) {
        as->push_needed = 0;
        create_rc = pthread_create(&as->push_thread, NULL,
                                   _ai_push_thread, as);
        if (create_rc == 0)
            as->push_thread_created = 1;
    }
    pthread_mutex_unlock(&as->lock);
    if (start_push && create_rc == 0)
        LOG_I("AI 会话建立，开始推流");
    else if (start_push) {
        LOG_E("无法创建 AI 音频推流线程");
        ai_stop_session(as);
    }
}

#ifdef DEVICE_SIM_TESTING
void ai_test_force_connect_timeout(AiState *as) {
    if (!as) return;
    pthread_mutex_lock(&as->lock);
    as->session_state = SESS_CONNECTING;
    as->hconn = NULL;
    as->connect_deadline_ms = now_ms() - 1;
    as->start_response_deadline_ms = 0;
    pthread_mutex_unlock(&as->lock);
}
#endif

/* ── Deferred start_session sender ──────────────────────────────────────── */

static char *_ai_build_start_session_json(AiState *as,
                                          const char *request_id) {
    if (!as || !request_id) return NULL;
    const char *input_codec =
        audio_format_ai_codec(as->up_audio_format);
    const char *output_codec =
        audio_format_ai_codec(as->down_audio_format);
    if (!input_codec || !output_codec) return NULL;

    cJSON *params = cJSON_CreateObject();
    cJSON *in_audio = cJSON_CreateObject();
    cJSON *out_audio = cJSON_CreateObject();
    cJSON *envelope = cJSON_CreateObject();
    if (!params || !in_audio || !out_audio || !envelope ||
        !cJSON_AddStringToObject(params, "device_id", as->device_id) ||
        !cJSON_AddStringToObject(params, "role_id", as->role_id) ||
        !cJSON_AddStringToObject(in_audio, "codec", input_codec) ||
        !cJSON_AddNumberToObject(in_audio, "sample_rate",
                                 as->up_audio_format->sample_rate) ||
        !cJSON_AddNumberToObject(in_audio, "channels", 1) ||
        !cJSON_AddStringToObject(out_audio, "codec", output_codec) ||
        !cJSON_AddNumberToObject(out_audio, "sample_rate",
                                 as->down_audio_format->sample_rate) ||
        !cJSON_AddNumberToObject(out_audio, "channels", 1) ||
        !cJSON_AddStringToObject(envelope, "jsonrpc", "2.0") ||
        !cJSON_AddStringToObject(envelope, "id", request_id) ||
        !cJSON_AddStringToObject(envelope, "method", "start_session")) {
        cJSON_Delete(in_audio);
        cJSON_Delete(out_audio);
        cJSON_Delete(params);
        cJSON_Delete(envelope);
        return NULL;
    }
    cJSON_AddItemToObject(params, "input_audio", in_audio);
    cJSON_AddItemToObject(params, "output_audio", out_audio);
    cJSON_AddItemToObject(envelope, "params", params);

    char *json = cJSON_PrintUnformatted(envelope);
    cJSON_Delete(envelope);
    return json;
}

/* Called from ai_cmd_loop when start_pending fires and KCP delay elapsed.
 * Runs in the cmd thread context — the only context that can call TiRtcSendCommand. */
static void _ai_send_deferred_start(AiState *as) {
    tirtc_conn_t hconn;

    pthread_mutex_lock(&as->lock);
    hconn = as->hconn;
    as->start_pending = 0;
    pthread_mutex_unlock(&as->lock);

    if (!hconn) return;

    char req_id[33];
    if (rand_hex(req_id, 16) != 0) {
        LOG_E("无法生成 AI request_id");
        ai_stop_session(as);
        return;
    }

    char *json_str = _ai_build_start_session_json(as, req_id);
    if (!json_str) {
        LOG_W("AI start_session JSON 构造失败");
        ai_stop_session(as);
        return;
    }
    LOG_D("Sending start_session: %s", json_str);
    int rc = TiRtcSendCommand(hconn, CMD_AI, json_str,
                              (uint32_t)strlen(json_str));
    free(json_str);

    if (rc < 0) {
        LOG_E("发送 start_session 失败: %s", TiRtcGetErrorStr(rc));
        ai_stop_session(as);
        return;
    }

    pthread_mutex_lock(&as->lock);
    if (as->hconn == hconn && as->session_state == SESS_CONNECTING)
        as->start_response_deadline_ms =
            now_ms() + AI_START_RESPONSE_TIMEOUT_MS;
    pthread_mutex_unlock(&as->lock);
    LOG_I("等待 start_session 响应后开始推流…");
}

#ifdef DEVICE_SIM_TESTING
char *ai_test_build_start_session_json(AiState *as,
                                       const char *request_id) {
    return _ai_build_start_session_json(as, request_id);
}
#endif

/* ── Command input loop ──────────────────────────────────────────────────── */

void ai_cmd_loop(AiState *as) {
    PHASE_TITLE("AI 命令已就绪");
    CMD_HINT("aicall  - 发起 AI 对话");
    CMD_HINT("hangup  - 结束对话");
    CMD_HINT("exit    - 退出程序");

    char *line = NULL;
    size_t linecap = 0;

    while (!g_stop) {
        /* Check deferred start_session (set by WHIP callback) */
        ai_poll(as);

        /* Use select() to poll stdin every 100ms instead of blocking in getline */
        fd_set fds;
        FD_ZERO(&fds);
        FD_SET(STDIN_FILENO, &fds);
        struct timeval tv = { 0, 100000 };  /* 100ms */
        if (select(STDIN_FILENO + 1, &fds, NULL, NULL, &tv) <= 0)
            continue;

        errno = 0;
        ssize_t n = getline(&line, &linecap, stdin);
        if (n <= 0) { sleep_ms(100); continue; }
        if (n > 0 && line[n-1] == '\n') line[--n] = '\0';
        if (n == 0) continue;

        if (strcmp(line, "aicall") == 0) {
            if (ai_is_active(as)) {
                LOG_W("Already in a conversation");
                continue;
            }

            LOG_D("获取 AI token  GET %s/v1/ai/token", as->ai_server);
            char peer_id[512] = "", token[1024] = "", role_id[64] = "";
            if (ai_get_token(as->ai_server, as->mqtt_token, as->device_id,
                             peer_id, sizeof(peer_id),
                             token, sizeof(token),
                             role_id, sizeof(role_id)) != 0) {
                LOG_W("获取 AI token 失败");
                continue;
            }
            LOG_I("正在连接 AI 服务…");
            ai_start_session(as, peer_id, token, as->ai_audio, as->device_id, role_id);
        }
        else if (strcmp(line, "hangup") == 0) {
            if (!ai_is_active(as)) {
                LOG_W("没有进行中的对话");
                continue;
            }
            ai_stop_session(as);
            LOG_I("对话已结束");
        }
        else if (strcmp(line, "exit") == 0) {
            LOG_I("正在退出…");
            if (ai_is_active(as)) ai_stop_session(as);
            g_stop = 1;
            break;
        }
        else {
            LOG_W("未知命令: %s (可用: aicall / hangup / exit)", line);
        }
    }
    free(line);
}
