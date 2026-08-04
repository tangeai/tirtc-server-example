/** \file tirtc_voip.c
 * \brief TiRTC VoIP module — WHIP client, file media, and call state machine.
 *
 * Linux reference: demonstrates TiRTC WHIP connections for VoIP, including
 * incoming call handling, outgoing calls, rejection, and encoded file-media
 * streaming. It does not capture, play, or display hardware media.
 */

#include "tirtc_voip.h"

#include <stdio.h>
#include <stdlib.h>
#include <limits.h>
#include <math.h>
#include <string.h>
#include <strings.h>
#include <pthread.h>
#include <unistd.h>
#include <time.h>
#include <sys/select.h>

#include <curl/curl.h>
#include <cjson/cJSON.h>

#include "tirtc/tiRTC.h"
#include "device_flow.h"    /* http_get / http_post helpers need extern... */
#define LOG_MODULE "voip"
#include "common.h"
#include "device_adapter.h"
#include "http_tls.h"
#include "media_format.h"
#include "media_rx_log.h"
#include "sdk_callback_guard.h"
#include "tirtc_runtime.h"

extern volatile sig_atomic_t g_stop;

static int voip_env_bool(const char *name) {
    const char *value = getenv(name);
    if (!value) return 0;
    if (strcmp(value, "1") == 0 || strcasecmp(value, "true") == 0 ||
        strcasecmp(value, "yes") == 0 || strcasecmp(value, "on") == 0)
        return 1;
    if (strcmp(value, "0") != 0 && strcasecmp(value, "false") != 0 &&
        strcasecmp(value, "no") != 0 && strcasecmp(value, "off") != 0)
        LOG_W("%s 必须是 true/false，已回退为 false", name);
    return 0;
}

static int voip_env_positive_int(const char *name, int default_value) {
    const char *value = getenv(name);
    if (!value || value[0] == '\0') return default_value;
    char *end = NULL;
    long parsed = strtol(value, &end, 10);
    if (end != value && *end == '\0' && parsed > 0 && parsed <= INT_MAX)
        return (int)parsed;
    LOG_W("%s 必须是正整数，已回退为 %d", name, default_value);
    return default_value;
}

/* Redeclare HTTP helpers since they're static in device_flow.c.
 * For the VoIP profile/call requests, we use libcurl directly here. */

/* ── VoIP session state ──────────────────────────────────────────────────── */

#define VOIP_CONNECT_TIMEOUT_MS 10000
#define VOIP_ACCEPT_TIMEOUT_MS  10000
#define VOIP_REJECT_QUEUE_CAPACITY 16

struct VoipState {
    char voip_server[256];
    char device_id[64];
    char mqtt_token[512];
    char voip_audio[512];
    char voip_video[512];

    /* Call state */
    pthread_mutex_t lock;
    pthread_mutex_t control_lock;
    int             pending_call;   /* have pending incoming call */
    int             pending_with_video;
    uint64_t        incoming_generation;
    uint64_t        pending_generation;
    int64_t         pending_deadline_ms;
    char            pending_peer_id[1024];  /* peer_id is a long URL; 1024 fits observed ~560B; use 2048 if memory allows */
    char            pending_token[1024];
    char            pending_room_id[128];
    char            pending_openid[64];
    char            pending_app_id[64];
    char            pending_model_id[64];
    char            pending_session_token[256];
    char            pending_payload[512];

    int             outgoing_call;   /* currently dialing out */
    int             outgoing_with_video;
    char            outgoing_openid[64];
    char            outgoing_call_id[64];
    int64_t         outgoing_deadline_ms;
    char            ignored_call_id[64];
    int64_t         ignored_call_until_ms;
    char            active_room_id[128];

    /* Authorized callers list (JSON array) */
    cJSON          *auth_list;
    pthread_t       callers_refresh_thread;
    int             callers_refresh_thread_started;
    int             callers_refresh_stop;
    int             callers_refresh_requested;
    int             callers_refresh_running;
    pthread_cond_t  callers_refresh_cond;

    /* HTTP rejects submitted by MQTT callbacks. */
    pthread_mutex_t reject_lock;
    pthread_cond_t  reject_ready;
    pthread_cond_t  reject_idle;
    pthread_t       reject_thread;
    int             reject_thread_started;
    int             reject_stop;
    int             reject_active;
    size_t          reject_head;
    size_t          reject_count;
    struct {
        char app_id[64];
        char model_id[64];
        char session_token[256];
        char room_id[128];
        char payload[512];
        int reason;
    } reject_queue[VOIP_REJECT_QUEUE_CAPACITY];

    /* SDK session */
    SessionState    session_state;
    int             active_hconn_set;  /* 0 = no active connection */
    tirtc_conn_t    active_hconn;
    uint64_t        connect_generation;
    int             connect_pending;
    int64_t         connect_deadline_ms;
    int64_t         accept_deadline_ms;

    /* Encoded file-media push. Downlink is logged then discarded. */
    pthread_t       push_thread;
    int             push_thread_created;
    int             push_running;
    int             media_start_pending;
    int             force_key;
    int             session_with_video;
    char            audio_path[512];
    const AudioFormat *up_audio_format;
    const VideoFormat *up_video_format;
    MediaRxLog      rx_log;
    voip_session_end_cb on_session_end;
    void            *on_session_end_user;
    int             (*before_recovered_start)(void *user);
    void            *before_recovered_start_user;
};

/* Active VoIP business state selected by the process runtime. */
static VoipState *s_active_vs = NULL;
static pthread_mutex_t s_vs_mtx = PTHREAD_MUTEX_INITIALIZER;

static void *voip_refresh_callers_worker(void *opaque);

static void *_voip_reject_worker(void *opaque) {
    VoipState *vs = opaque;
    pthread_mutex_lock(&vs->reject_lock);
    for (;;) {
        while (!vs->reject_stop && vs->reject_count == 0)
            pthread_cond_wait(&vs->reject_ready, &vs->reject_lock);
        if (vs->reject_stop && vs->reject_count == 0)
            break;

        size_t slot = vs->reject_head;
        char app_id[64], model_id[64], session_token[256];
        char room_id[128], payload[512];
        int reason = vs->reject_queue[slot].reason;
        STR_COPY(app_id, vs->reject_queue[slot].app_id);
        STR_COPY(model_id, vs->reject_queue[slot].model_id);
        STR_COPY(session_token, vs->reject_queue[slot].session_token);
        STR_COPY(room_id, vs->reject_queue[slot].room_id);
        STR_COPY(payload, vs->reject_queue[slot].payload);
        vs->reject_head =
            (vs->reject_head + 1) % VOIP_REJECT_QUEUE_CAPACITY;
        vs->reject_count--;
        vs->reject_active = 1;
        pthread_mutex_unlock(&vs->reject_lock);

        (void)voip_reject_session(app_id, model_id, session_token,
                                  room_id, payload, reason);

        pthread_mutex_lock(&vs->reject_lock);
        vs->reject_active = 0;
        if (vs->reject_count == 0)
            pthread_cond_broadcast(&vs->reject_idle);
    }
    pthread_cond_broadcast(&vs->reject_idle);
    pthread_mutex_unlock(&vs->reject_lock);
    return NULL;
}

VoipState *voip_create(const char *voip_server, const char *device_id,
                       const char *mqtt_token, const char *voip_audio) {
    VoipState *vs = (VoipState *)calloc(1, sizeof(VoipState));
    if (!vs) return NULL;
    STR_COPY(vs->voip_server, voip_server); STR_COPY(vs->device_id, device_id);
    STR_COPY(vs->mqtt_token, mqtt_token); STR_COPY(vs->voip_audio, voip_audio);
    snprintf(vs->voip_video, sizeof(vs->voip_video), "assets/video.h264");
    pthread_mutex_init(&vs->lock, NULL);
    pthread_mutex_init(&vs->control_lock, NULL);
    pthread_cond_init(&vs->callers_refresh_cond, NULL);
    pthread_mutex_init(&vs->reject_lock, NULL);
    pthread_cond_init(&vs->reject_ready, NULL);
    pthread_cond_init(&vs->reject_idle, NULL);
    pthread_mutex_init(&vs->rx_log.lock, NULL);
    vs->session_state = SESS_IDLE;
    vs->up_audio_format = audio_format_find("alaw_8khz");
    vs->up_video_format = video_format_find("h264");
    vs->auth_list = NULL;
    if (pthread_create(&vs->reject_thread, NULL,
                       _voip_reject_worker, vs) != 0) {
        LOG_E("无法创建 VoIP 忙线拒接工作线程");
        pthread_mutex_destroy(&vs->rx_log.lock);
        pthread_cond_destroy(&vs->reject_idle);
        pthread_cond_destroy(&vs->reject_ready);
        pthread_mutex_destroy(&vs->reject_lock);
        pthread_cond_destroy(&vs->callers_refresh_cond);
        pthread_mutex_destroy(&vs->control_lock);
        pthread_mutex_destroy(&vs->lock);
        free(vs);
        return NULL;
    }
    vs->reject_thread_started = 1;
    if (pthread_create(&vs->callers_refresh_thread, NULL,
                       voip_refresh_callers_worker, vs) != 0) {
        LOG_E("无法创建 VoIP 授权列表刷新工作线程");
        pthread_mutex_lock(&vs->reject_lock);
        vs->reject_stop = 1;
        pthread_cond_broadcast(&vs->reject_ready);
        pthread_mutex_unlock(&vs->reject_lock);
        pthread_join(vs->reject_thread, NULL);
        pthread_mutex_destroy(&vs->rx_log.lock);
        pthread_cond_destroy(&vs->reject_idle);
        pthread_cond_destroy(&vs->reject_ready);
        pthread_mutex_destroy(&vs->reject_lock);
        pthread_cond_destroy(&vs->callers_refresh_cond);
        pthread_mutex_destroy(&vs->control_lock);
        pthread_mutex_destroy(&vs->lock);
        free(vs);
        return NULL;
    }
    vs->callers_refresh_thread_started = 1;
    return vs;
}

void voip_configure_video(VoipState *vs, const char *video_path) {
    if (vs && video_path) snprintf(vs->voip_video, sizeof(vs->voip_video), "%s", video_path);
}

int voip_configure_media(VoipState *vs, const char *audio_path,
                         const char *audio_format, const char *video_path,
                         const char *video_format) {
    if (!vs) return -1;
    const AudioFormat *audio = audio_format_find(audio_format);
    const VideoFormat *video = video_format_find(video_format);
    if (!audio || ((video_path && video_path[0]) && !video)) return -1;
    pthread_mutex_lock(&vs->lock);
    STR_COPY(vs->voip_audio, audio_path ? audio_path : "");
    STR_COPY(vs->voip_video, video_path ? video_path : "");
    vs->up_audio_format = audio;
    vs->up_video_format = video;
    pthread_mutex_unlock(&vs->lock);
    return 0;
}

void voip_destroy(VoipState *vs) {
    if (!vs) return;
    voip_stop_session(vs);
    pthread_mutex_lock(&s_vs_mtx);
    if (s_active_vs == vs) s_active_vs = NULL;
    pthread_mutex_unlock(&s_vs_mtx);

    pthread_mutex_lock(&vs->lock);
    vs->callers_refresh_stop = 1;
    vs->callers_refresh_requested = 0;
    pthread_cond_broadcast(&vs->callers_refresh_cond);
    int callers_refresh_started = vs->callers_refresh_thread_started;
    pthread_t callers_refresh_thread = vs->callers_refresh_thread;
    pthread_mutex_unlock(&vs->lock);
    if (callers_refresh_started &&
        !pthread_equal(pthread_self(), callers_refresh_thread))
        pthread_join(callers_refresh_thread, NULL);

    pthread_mutex_lock(&vs->lock);
    cJSON *auth_list = vs->auth_list;
    vs->auth_list = NULL;
    pthread_mutex_unlock(&vs->lock);
    if (auth_list) cJSON_Delete(auth_list);

    pthread_mutex_lock(&vs->reject_lock);
    vs->reject_stop = 1;
    pthread_cond_broadcast(&vs->reject_ready);
    while (vs->reject_count != 0 || vs->reject_active)
        pthread_cond_wait(&vs->reject_idle, &vs->reject_lock);
    int reject_started = vs->reject_thread_started;
    pthread_t reject_thread = vs->reject_thread;
    pthread_mutex_unlock(&vs->reject_lock);
    if (reject_started && !pthread_equal(pthread_self(), reject_thread))
        pthread_join(reject_thread, NULL);

    pthread_cond_destroy(&vs->reject_idle);
    pthread_cond_destroy(&vs->reject_ready);
    pthread_mutex_destroy(&vs->reject_lock);
    pthread_cond_destroy(&vs->callers_refresh_cond);
    pthread_mutex_destroy(&vs->rx_log.lock);
    pthread_mutex_destroy(&vs->control_lock);
    pthread_mutex_destroy(&vs->lock);
    free(vs);
}

void voip_set_auth_list(VoipState *vs, cJSON *auth_list) {
    if (!vs) return;
    pthread_mutex_lock(&vs->lock);
    cJSON *old_auth_list = vs->auth_list;
    vs->auth_list = auth_list;
    pthread_mutex_unlock(&vs->lock);
    if (old_auth_list) cJSON_Delete(old_auth_list);
}

void voip_set_session_end_callback(VoipState *vs, voip_session_end_cb cb, void *user) {
    if (!vs) return;
    pthread_mutex_lock(&vs->lock);
    vs->on_session_end = cb;
    vs->on_session_end_user = user;
    pthread_mutex_unlock(&vs->lock);
}

void voip_set_recovered_start_callback(VoipState *vs,
                                       int (*callback)(void *user),
                                       void *user) {
    if (!vs) return;
    pthread_mutex_lock(&vs->lock);
    vs->before_recovered_start = callback;
    vs->before_recovered_start_user = user;
    pthread_mutex_unlock(&vs->lock);
}

/* ── HTTP helpers (local) ────────────────────────────────────────────────── */

static size_t _write_cb_local(void *ptr, size_t sz, size_t nmemb, void *user) {
    StrBuf *sb = (StrBuf *)user;
    size_t total = sz * nmemb;
    if (sb->len + total >= sb->cap) return 0;
    memcpy(sb->buf + sb->len, ptr, total);
    sb->len += total;
    sb->buf[sb->len] = '\0';
    return total;
}

/** Simple HTTP GET with Bearer auth. body_buf and body_cap must be provided. */
static int _http_get_auth(const char *url, const char *bearer,
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
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, _write_cb_local);
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
    curl_easy_cleanup(curl);
    return 0;
}

/** Simple HTTP POST with Bearer auth and JSON body. */
static int _http_post_auth(const char *url, const char *bearer, const char *json_body,
                           char *body_buf, size_t body_cap, long *http_code) {
    CURL *curl = curl_easy_init();
    if (!curl) return -1;

    StrBuf sb; sb_init(&sb, body_buf, body_cap);

    char auth[600], ct[] = "Content-Type: application/json";
    snprintf(auth, sizeof(auth), "Authorization: Bearer %s", bearer);
    struct curl_slist *hdrs = curl_slist_append(NULL, auth);
    hdrs = curl_slist_append(hdrs, ct);

    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 10L);
    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, json_body);
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, hdrs);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, _write_cb_local);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &sb);
    http_tls_apply(curl);

    CURLcode res = curl_easy_perform(curl);
    curl_slist_free_all(hdrs);
    if (res != CURLE_OK) {
        LOG_E("HTTP POST %s 失败: %s", url, curl_easy_strerror(res));
        curl_easy_cleanup(curl);
        return -1;
    }
    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, http_code);
    curl_easy_cleanup(curl);
    return 0;
}

/* ── VoIP profile / callers ──────────────────────────────────────────────── */

static const AudioFormat *s_voip_up_audio_format;
static const AudioFormat *s_voip_down_audio_format;
static const VideoFormat *s_voip_up_video_format;
static const VideoFormat *s_voip_down_video_format;
static int s_voip_has_video = 1;

int voip_configure_down_audio_format(const char *format) {
    const AudioFormat *spec = audio_format_find(format);
    if (!spec) {
        LOG_E("不支持的下行音频格式: %s", format ? format : "(null)");
        return -1;
    }
    s_voip_down_audio_format = spec;
    return 0;
}

int voip_configure_profile(const char *up_audio, const char *down_audio,
                           const char *up_video, const char *down_video,
                           int has_video) {
    const AudioFormat *up_a = audio_format_find(up_audio);
    const AudioFormat *down_a = audio_format_find(down_audio);
    const VideoFormat *up_v = video_format_find(up_video);
    const VideoFormat *down_v = video_format_find(down_video);
    if (!up_a || !down_a || (has_video && (!up_v || !down_v))) return -1;
    s_voip_up_audio_format = up_a;
    s_voip_down_audio_format = down_a;
    s_voip_up_video_format = up_v;
    s_voip_down_video_format = down_v;
    s_voip_has_video = has_video ? 1 : 0;
    return 0;
}

int voip_report_profile(const char *voip_server, const char *mqtt_token,
                        cJSON **auth_list_out) {
    /* POST /v1/voip/device/profile */
    char url[512];
    snprintf(url, sizeof(url), "%s/v1/voip/device/profile", voip_server);

    const AudioFormat *down = s_voip_down_audio_format;
    if (!down) down = audio_format_find("alaw_8khz");
    const AudioFormat *up = s_voip_up_audio_format;
    if (!up) up = audio_format_find("alaw_8khz");
    const VideoFormat *up_video = s_voip_up_video_format;
    if (!up_video) up_video = video_format_find("h264");
    const VideoFormat *down_video = s_voip_down_video_format;
    if (!down_video) down_video = video_format_find("h264");
    int screen_width = voip_env_positive_int("VOIP_SCREEN_WIDTH", 1280);
    int screen_height = voip_env_positive_int("VOIP_SCREEN_HEIGHT", 720);
    int camera_rotation = 0;
    const char *rotation_env = getenv("VOIP_CAMERA_ROTATION");
    if (rotation_env) {
        int parsed = atoi(rotation_env);
        if (parsed == 0 || parsed == 90 || parsed == 180 || parsed == 270)
            camera_rotation = parsed;
        else
            LOG_W("VOIP_CAMERA_ROTATION 仅支持 0/90/180/270，已回退为 0");
    }
    double aspect_ratio = 4.0 / 3.0;
    const char *aspect_ratio_env = getenv("VOIP_ASPECT_RATIO");
    if (aspect_ratio_env) {
        char *end = NULL;
        double parsed = strtod(aspect_ratio_env, &end);
        if (end != aspect_ratio_env && *end == '\0' && isfinite(parsed) && parsed > 0)
            aspect_ratio = parsed;
        else
            LOG_W("VOIP_ASPECT_RATIO 必须是大于 0 的数字，已回退为 4/3");
    }
    int hor_mirror = voip_env_bool("VOIP_HOR_MIRROR");
    int vert_mirror = voip_env_bool("VOIP_VERT_MIRROR");
    char object_fit_field[40] = "";
    const char *object_fit_env = getenv("VOIP_OBJECT_FIT");
    if (object_fit_env && object_fit_env[0] != '\0') {
        if (strcmp(object_fit_env, "fill") == 0 ||
            strcmp(object_fit_env, "contain") == 0) {
            snprintf(object_fit_field, sizeof(object_fit_field),
                     "\"object_fit\":\"%s\",", object_fit_env);
        } else {
            LOG_W("VOIP_OBJECT_FIT 仅支持 fill/contain，已回退为微信默认值");
        }
    }
    if (!s_voip_has_video) {
        screen_width = 1;
        screen_height = 1;
    }
    char profile[512];
    snprintf(profile, sizeof(profile),
             "{\"screen_width\":%d,\"screen_height\":%d,"
             "\"camera_rotation\":%d,\"aspect_ratio\":%.10g,"
             "%s"
             "\"hor_mirror\":%s,\"vert_mirror\":%s,"
             "\"audio_rate\":%d,\"audio_channels\":1,"
             "\"up_video_mt\":\"%s\",\"down_video_mt\":\"%s\","
             "\"down_audio_mt\":\"%s\",\"no_video\":%s,"
             "\"calling_timeout_sec\":30}",
             screen_width, screen_height,
             camera_rotation, aspect_ratio, object_fit_field,
             hor_mirror ? "true" : "false",
             vert_mirror ? "true" : "false",
             down->sample_rate,
             s_voip_has_video ? up_video->codec : "none",
             s_voip_has_video ? down_video->codec : "none",
             down->codec, s_voip_has_video ? "false" : "true");

    char body_buf[4096];
    long http_code = 0;
    if (_http_post_auth(url, mqtt_token, profile, body_buf, sizeof(body_buf), &http_code) == 0) {
        cJSON *r = cJSON_Parse(body_buf);
        cJSON *code = r ? cJSON_GetObjectItem(r, "code") : NULL;
        int cv = (code && cJSON_IsNumber(code)) ? code->valueint : -1;
        LOG_D("POST %s -> HTTP %ld code=%d", url, http_code, cv);
        if (http_code == 200 && cv == 0)
            LOG_I("VoIP profile 上报成功: up_audio=%s video=%s",
                  up->name, s_voip_has_video ? up_video->name : "关闭");
        else LOG_W("VoIP profile 上报失败 (code=%d)", cv);
        cJSON_Delete(r);
    }

    /* GET /v1/voip/device/contacts */
    snprintf(url, sizeof(url), "%s/v1/voip/device/contacts", voip_server);
    body_buf[0] = '\0';
    *auth_list_out = NULL;

    int contacts_ok = 0;
    if (_http_get_auth(url, mqtt_token, body_buf, sizeof(body_buf), &http_code) == 0) {
        cJSON *r = cJSON_Parse(body_buf);
        if (r) {
            cJSON *code = cJSON_GetObjectItem(r, "code");
            if (http_code == 200 && code && cJSON_IsNumber(code) && code->valueint == 0) {
                cJSON *data = cJSON_GetObjectItem(r, "data");
                cJSON *list = data ? cJSON_GetObjectItem(data, "contacts") : NULL;
                if (list && cJSON_IsArray(list)) {
                    *auth_list_out = cJSON_Duplicate(list, 1);
                    if (*auth_list_out) {
                        contacts_ok = 1;
                        LOG_I("授权呼叫方: %d 个", cJSON_GetArraySize(list));
                    }
                }
                LOG_D("GET %s -> HTTP %ld contacts=%d",
                      url, http_code,
                      list && cJSON_IsArray(list) ? cJSON_GetArraySize(list) : 0);
            }
            cJSON_Delete(r);
        }
    }
    return contacts_ok ? 0 : -1;
}

/* ── Process-runtime callbacks ───────────────────────────────────────────── */

static SdkCallbackGuard s_voip_callback_guard = SDK_CALLBACK_GUARD_INITIALIZER;

/* Forward declaration of the call state machine callbacks */
static void _voip_handle_disconnect(VoipState *vs);
static void *_voip_push_thread(void *arg);

static VoipState *_active_voip(void) {
    pthread_mutex_lock(&s_vs_mtx);
    VoipState *vs = s_active_vs;
    pthread_mutex_unlock(&s_vs_mtx);
    return vs;
}

static int _voip_hconn_matches(VoipState *vs, tirtc_conn_t hconn) {
    int matched = 0;
    if (vs) {
        pthread_mutex_lock(&vs->lock);
        matched = vs->active_hconn_set && vs->active_hconn == hconn;
        pthread_mutex_unlock(&vs->lock);
    }
    return matched;
}

static void _deferred_disconnect_session(void *opaque) {
    tirtc_conn_t hconn = (tirtc_conn_t)opaque;
    VoipState *vs = _active_voip();
    if (vs) pthread_mutex_lock(&vs->control_lock);
    if (_voip_hconn_matches(vs, hconn)) _voip_handle_disconnect(vs);
    TiRtcDisconnect(hconn);
    if (vs) pthread_mutex_unlock(&vs->control_lock);
}

static void _deferred_finish_disconnect(void *opaque) {
    tirtc_conn_t hconn = (tirtc_conn_t)opaque;
    VoipState *vs = _active_voip();
    if (vs) pthread_mutex_lock(&vs->control_lock);
    if (_voip_hconn_matches(vs, hconn)) _voip_handle_disconnect(vs);
    if (vs) pthread_mutex_unlock(&vs->control_lock);
}

static void _deferred_start_media(void *opaque) {
    tirtc_conn_t hconn = (tirtc_conn_t)opaque;
    VoipState *vs = _active_voip();
    if (!vs) return;
    pthread_mutex_lock(&vs->control_lock);
    int start = 0;
    pthread_mutex_lock(&vs->lock);
    if (vs->active_hconn_set && vs->active_hconn == hconn &&
        vs->media_start_pending && !vs->push_thread_created) {
        vs->media_start_pending = 0;
        STR_COPY(vs->audio_path, vs->voip_audio);
        vs->push_running = 1;
        start = 1;
        if (pthread_create(&vs->push_thread, NULL, _voip_push_thread, vs) == 0)
            vs->push_thread_created = 1;
        else
            vs->push_running = 0;
    }
    int started = vs->push_thread_created;
    pthread_mutex_unlock(&vs->lock);
    pthread_mutex_unlock(&vs->control_lock);
    if (start && !started) {
        LOG_E("无法创建 VoIP 上行媒体线程，断开会话");
        _deferred_disconnect_session(hconn);
    } else if (start) {
        LOG_I("收到 0x2000，开始 VoIP 媒体收发");
    }
}

static void _von_conn_error(tirtc_conn_t hconn, int error) {
    sdk_callback_enter(&s_voip_callback_guard);
    LOG_E("on_conn_error: %s", TiRtcGetErrorStr(error));
    if (sdk_defer_action(&s_voip_callback_guard, _deferred_disconnect_session,
                         hconn) != 0)
        LOG_E("无法延后清理 VoIP 错误连接");
    sdk_callback_leave(&s_voip_callback_guard);
}

static void _von_disconnected(tirtc_conn_t hconn) {
    sdk_callback_enter(&s_voip_callback_guard);
    LOG_D("on_disconnected");
    if (sdk_defer_action(&s_voip_callback_guard, _deferred_finish_disconnect,
                         hconn) != 0)
        LOG_E("无法延后清理 VoIP 断开连接");
    sdk_callback_leave(&s_voip_callback_guard);
}

static void _von_audio(tirtc_conn_t hconn, const TIRTCFRAMEINFO *pFi, void *data) {
    sdk_callback_enter(&s_voip_callback_guard);
    VoipState *vs = _active_voip();
    MediaRxNotice notice;
    int matched = _voip_hconn_matches(vs, hconn);
    if (matched)
        (void)device_media_sink_submit(
            DEVICE_BUSINESS_VOIP, 0, pFi->stream_id, pFi->media,
            pFi->flags, pFi->ts, data, pFi->length);
    if (matched &&
        media_rx_log_note_audio(&vs->rx_log, "VoIP", pFi, &notice))
        (void)sdk_defer_copy_action(
            &s_voip_callback_guard, media_rx_log_emit, NULL,
            &notice, sizeof(notice));
    sdk_callback_leave(&s_voip_callback_guard);
}

static void _von_video(tirtc_conn_t hconn, const TIRTCFRAMEINFO *pFi, void *data) {
    sdk_callback_enter(&s_voip_callback_guard);
    VoipState *vs = _active_voip();
    MediaRxNotice notice;
    int matched = _voip_hconn_matches(vs, hconn);
    if (matched)
        (void)device_media_sink_submit(
            DEVICE_BUSINESS_VOIP, 1, pFi->stream_id, pFi->media,
            pFi->flags, pFi->ts, data, pFi->length);
    if (matched &&
        media_rx_log_note_video(&vs->rx_log, "VoIP", pFi, &notice))
        (void)sdk_defer_copy_action(
            &s_voip_callback_guard, media_rx_log_emit, NULL,
            &notice, sizeof(notice));
    sdk_callback_leave(&s_voip_callback_guard);
}

static void _von_command(tirtc_conn_t hconn, uint32_t cmdw, const void *data, uint32_t len) {
    sdk_callback_enter(&s_voip_callback_guard);
    if (cmdw == CMD_VOIP_ACCEPT) {
        VoipState *vs = _active_voip();
        int matched = 0;
        if (vs) {
            pthread_mutex_lock(&vs->lock);
            matched = vs->active_hconn_set && vs->active_hconn == hconn &&
                      vs->session_state == SESS_CONNECTING;
            if (matched) {
                vs->session_state = SESS_IN_CALL;
                vs->media_start_pending = 1;
                vs->force_key = 1;
                vs->accept_deadline_ms = 0;
            }
            pthread_mutex_unlock(&vs->lock);
        }
        if (matched &&
            sdk_defer_action(&s_voip_callback_guard, _deferred_start_media,
                             hconn) != 0)
            LOG_E("无法延后启动 VoIP 媒体线程");
    } else if (cmdw == CMD_VOIP_HANGUP) {
        LOG_I("收到远端挂断命令 0x2001");
        if (sdk_defer_action(&s_voip_callback_guard,
                             _deferred_disconnect_session, hconn) != 0)
            LOG_E("无法延后断开 VoIP 会话");
    }
    (void)data; (void)len;
    sdk_callback_leave(&s_voip_callback_guard);
}

/* Callbacks for supported media events. */
static void _v_nop_4(tirtc_conn_t h, const TIRTCFRAMEINFO *f, void *d) {
    sdk_callback_enter(&s_voip_callback_guard);
    (void)h;(void)f;(void)d;
    sdk_callback_leave(&s_voip_callback_guard);
}
static void _v_nop_unsub(tirtc_conn_t h, uint8_t s) {
    sdk_callback_enter(&s_voip_callback_guard);
    (void)h;(void)s;
    sdk_callback_leave(&s_voip_callback_guard);
}
static void _von_request_key_frame(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_voip_callback_guard);
    VoipState *vs = _active_voip();
    if (vs && stream_id == STREAM_ID_VIDEO) {
        pthread_mutex_lock(&vs->lock);
        if (vs->active_hconn_set && vs->active_hconn == hconn)
            vs->force_key = 1;
        pthread_mutex_unlock(&vs->lock);
    }
    sdk_callback_leave(&s_voip_callback_guard);
}
static int _v_ret0(tirtc_conn_t h, uint8_t s) {
    sdk_callback_enter(&s_voip_callback_guard);
    (void)h;(void)s;
    sdk_callback_leave(&s_voip_callback_guard);
    return 0;
}

int voip_service_register(void) {
    TIRTCCALLBACKS cbs;
    memset(&cbs, 0, sizeof(cbs));
    cbs.on_conn_error        = _von_conn_error;
    cbs.on_disconnected      = _von_disconnected;
    cbs.on_audio             = _von_audio;
    cbs.on_video             = _von_video;
    cbs.on_message           = _v_nop_4;
    cbs.on_command           = _von_command;
    cbs.on_request_key_frame = _von_request_key_frame;
    cbs.on_subscribe_video   = _v_ret0;
    cbs.on_unsubscribe_video = _v_nop_unsub;
    cbs.on_subscribe_audio   = _v_ret0;
    cbs.on_unsubscribe_audio = _v_nop_unsub;
    return tirtc_runtime_register_service(
        TIRTC_SERVICE_VOIP, &cbs, &s_voip_callback_guard);
}

int voip_service_start(VoipState *vs) {
    if (!vs) return -1;
    pthread_mutex_lock(&s_vs_mtx);
    s_active_vs = vs;
    pthread_mutex_unlock(&s_vs_mtx);
    LOG_I("VoIP 业务已就绪");
    return 0;
}

void voip_service_stop(VoipState *vs) {
    if (!vs) return;
    voip_stop_session(vs);
    sdk_callback_wait_all(&s_voip_callback_guard);
    pthread_mutex_lock(&s_vs_mtx);
    if (s_active_vs == vs) s_active_vs = NULL;
    pthread_mutex_unlock(&s_vs_mtx);
    LOG_I("VoIP 业务已停止");
}

/* ── Audio push thread ───────────────────────────────────────────────────── */

static void *_voip_push_thread(void *arg) {
    VoipState *vs = (VoipState *)arg;
    char audio_path[512], video_path[512];
    const AudioFormat *audio_format;
    const VideoFormat *video_format;
    pthread_mutex_lock(&vs->lock);
    STR_COPY(audio_path, vs->audio_path);
    if (vs->session_with_video)
        STR_COPY(video_path, vs->voip_video);
    else
        video_path[0] = '\0';
    audio_format = vs->up_audio_format;
    video_format = vs->up_video_format;
    pthread_mutex_unlock(&vs->lock);

    LOG_D("VoIP 上行媒体线程启动 audio=%s video=%s",
          audio_path, video_path[0] ? video_path : "关闭");
    DeviceMediaSource source;
    DeviceMediaSourceConfig media_config = {
        .audio_locator = audio_path,
        .audio_format = audio_format ? audio_format->name : NULL,
        .video_locator = video_path,
        .video_format = video_format ? video_format->name : NULL,
        .audio_packet_ms = AUDIO_PKT_MS_VOIP,
        .business = DEVICE_BUSINESS_VOIP,
    };
    if (device_media_source_open(&source, &media_config) != 0) {
        LOG_E("无法打开 VoIP 上行媒体源");
        pthread_mutex_lock(&vs->lock);
        tirtc_conn_t hconn = vs->active_hconn;
        pthread_mutex_unlock(&vs->lock);
        if (hconn) TiRtcDisconnect(hconn);
        return NULL;
    }

    double audio_pts_ms = 0.0, video_pts_ms = 0.0;
    int64_t wall_start  = now_ms();
    int force_key = 1;
    int has_video = device_media_source_has_video(&source);
    int consecutive_failures = 0;

    while (!g_stop) {
        double target_pts = has_video && video_pts_ms < audio_pts_ms
                                ? video_pts_ms : audio_pts_ms;
        int64_t elapsed = now_ms() - wall_start;
        int64_t wait_ms = (int64_t)target_pts - elapsed;
        if (wait_ms > 2) {
            sleep_ms((int)(wait_ms > 50 ? 50 : wait_ms));
            continue;
        }

        pthread_mutex_lock(&vs->lock);
        int running = vs->push_running;
        tirtc_conn_t hconn = vs->active_hconn;
        pthread_mutex_unlock(&vs->lock);

        if (!running || !hconn) break;

        int rc;
        int send_audio = !has_video || audio_pts_ms <= video_pts_ms;
        if (send_audio) {
            DeviceMediaPacket packet;
            if (device_media_source_next_audio(&source, &packet) <= 0)
                break;
            TIRTCFRAMEINFO fi = {0};
            fi.stream_id = STREAM_ID_AUDIO;
            fi.media = audio_format->media;
            fi.flags = audio_format->flags;
            fi.ts = (uint32_t)audio_pts_ms;
            fi.length = (uint32_t)packet.length;
            rc = TiRtcSendAudioStream(hconn, &fi, packet.data);
            audio_pts_ms += packet.duration_ms;
        } else {
            pthread_mutex_lock(&vs->lock);
            if (vs->force_key) {
                force_key = 1;
                vs->force_key = 0;
            }
            pthread_mutex_unlock(&vs->lock);
            DeviceMediaPacket packet;
            if (device_media_source_next_video(
                    &source, force_key, &packet) <= 0)
                break;
            force_key = 0;
            TIRTCFRAMEINFO fi = {0};
            fi.stream_id = STREAM_ID_VIDEO;
            fi.media = video_format->media;
            fi.flags = packet.key_frame ? TIRTC_FRAME_FLAG_KEY_FRAME : 0;
            fi.ts = (uint32_t)video_pts_ms;
            fi.length = (uint32_t)packet.length;
            rc = TiRtcSendVideoStream(hconn, &fi, packet.data);
            video_pts_ms += 1000.0 / VIDEO_FPS;
        }
        if (rc >= 0) {
            consecutive_failures = 0;
        } else if (rc == TIRTC_E_CONN_TIMEOUTCLOSE || rc == TIRTC_E_CONN_REMOTECLOSE ||
            rc == TIRTC_E_CONN_OTHER_ERROR) {
            LOG_D("连接已关闭，退出推流");
            break;
        } else if (rc == TIRTC_E_INVALID_HANDLE) {
            sleep_ms(5);
        } else {
            if (!send_audio && rc == TIRTC_E_BUSY) {
                pthread_mutex_lock(&vs->lock);
                vs->force_key = 1;
                pthread_mutex_unlock(&vs->lock);
            }
            LOG_W("发送 VoIP 帧失败 rc=%d: %s", rc,
                  TiRtcGetErrorStr(rc));
            if (++consecutive_failures >= 3) {
                TiRtcDisconnect(hconn);
                break;
            }
        }
    }

    device_media_source_close(&source);
    LOG_D("VoIP 上行媒体线程退出");
    return NULL;
}

/* ── Session management ──────────────────────────────────────────────────── */

static void _voip_handle_disconnect(VoipState *vs) {
    if (!vs) return;
    pthread_mutex_lock(&vs->lock);
    vs->push_running = 0;
    int join_push = vs->push_thread_created;
    pthread_t push_thread = vs->push_thread;
    vs->push_thread_created = 0;
    vs->media_start_pending = 0;
    vs->force_key = 0;
    vs->active_hconn_set = 0;
    vs->active_hconn = NULL;
    vs->session_with_video = 0;
    vs->connect_deadline_ms = 0;
    vs->accept_deadline_ms = 0;
    vs->session_state = SESS_DISCONNECTING;
    vs->active_room_id[0] = '\0';
    pthread_mutex_unlock(&vs->lock);
    if (join_push && !pthread_equal(pthread_self(), push_thread))
        pthread_join(push_thread, NULL);
    pthread_mutex_lock(&vs->lock);
    vs->session_state = SESS_IDLE;
    pthread_mutex_unlock(&vs->lock);
    LOG_I("连接已断开，回到 IDLE");
    if (vs->on_session_end) vs->on_session_end(vs->on_session_end_user);
}

typedef struct {
    VoipState *vs;
    uint64_t generation;
} VoipConnectContext;

/* WHIP connect callback */
static void _voip_connect_cb(int error, tirtc_conn_t hconn, void *user_data) {
    sdk_callback_enter(&s_voip_callback_guard);
    VoipConnectContext *connect_ctx = (VoipConnectContext *)user_data;
    VoipState *vs = connect_ctx->vs;
    uint64_t generation = connect_ctx->generation;
    free(connect_ctx);

    pthread_mutex_lock(&vs->lock);
    int current = vs->connect_pending && vs->connect_generation == generation;
    if (!current) {
        pthread_mutex_unlock(&vs->lock);
        if (error == 0 && hconn) {
            LOG_I("忽略已经取消的过期 WHIP 连接回调，主动断开");
            if (sdk_defer_disconnect(&s_voip_callback_guard, hconn) != 0)
                LOG_E("无法延后断开过期 WHIP 连接");
        }
        sdk_callback_leave(&s_voip_callback_guard);
        return;
    }

    vs->connect_pending = 0;
    vs->connect_deadline_ms = 0;
    if (error != 0 || !hconn) {
        vs->active_hconn_set = 0;
        vs->session_with_video = 0;
        vs->session_state = SESS_IDLE;
        vs->accept_deadline_ms = 0;
        pthread_mutex_unlock(&vs->lock);
        LOG_E("TiRtcWhipConnect 失败: rc=%d %s", error, TiRtcGetErrorStr(error));
        if (vs->on_session_end &&
            sdk_defer_action(&s_voip_callback_guard, vs->on_session_end,
                             vs->on_session_end_user) != 0)
            LOG_E("无法延后通知 VoIP 连接失败");
        sdk_callback_leave(&s_voip_callback_guard);
        return;
    }
    if (tirtc_runtime_bind_active_connection(TIRTC_SERVICE_VOIP, hconn) != 0) {
        vs->active_hconn_set = 0;
        vs->session_with_video = 0;
        vs->session_state = SESS_IDLE;
        vs->accept_deadline_ms = 0;
        pthread_mutex_unlock(&vs->lock);
        LOG_W("WHIP 连接完成时 VoIP 会话已切换，丢弃连接");
        if (sdk_defer_disconnect(&s_voip_callback_guard, hconn) != 0)
            LOG_E("无法延后断开失效 VoIP 连接");
        if (vs->on_session_end &&
            sdk_defer_action(&s_voip_callback_guard, vs->on_session_end,
                             vs->on_session_end_user) != 0)
            LOG_E("无法延后通知 VoIP runtime 代次失效");
        sdk_callback_leave(&s_voip_callback_guard);
        return;
    }
    vs->active_hconn = hconn;
    vs->active_hconn_set = 1;
    vs->session_state = SESS_CONNECTING;
    vs->accept_deadline_ms = now_ms() + VOIP_ACCEPT_TIMEOUT_MS;
    pthread_mutex_unlock(&vs->lock);
    LOG_I("WHIP 连接成功，等待平台下发 cmdw=0x2000");
    sdk_callback_leave(&s_voip_callback_guard);
}

int voip_start_session(VoipState *vs, const char *peer_id, const char *token,
                       const char *audio_file) {
    pthread_mutex_lock(&vs->control_lock);
    pthread_mutex_lock(&vs->lock);
    if (vs->active_hconn_set || vs->connect_pending) {
        LOG_E("已在通话中，无法发起新会话");
        pthread_mutex_unlock(&vs->lock);
        pthread_mutex_unlock(&vs->control_lock);
        return -1;
    }

    STR_COPY(vs->voip_audio, audio_file);
    vs->connect_generation++;
    uint64_t generation = vs->connect_generation;
    vs->connect_pending = 1;
    vs->connect_deadline_ms = 0;
    vs->accept_deadline_ms = 0;
    vs->session_state = SESS_CONNECTING;
    vs->media_start_pending = 0;
    media_rx_log_reset(&vs->rx_log);
    pthread_mutex_unlock(&vs->lock);

    VoipConnectContext *connect_ctx = calloc(1, sizeof(*connect_ctx));
    if (!connect_ctx) {
        pthread_mutex_lock(&vs->lock);
        if (vs->connect_generation == generation) {
            vs->connect_pending = 0;
            vs->session_state = SESS_IDLE;
        }
        pthread_mutex_unlock(&vs->lock);
        LOG_E("WHIP 连接上下文分配失败");
        pthread_mutex_unlock(&vs->control_lock);
        return -1;
    }
    connect_ctx->vs = vs;
    connect_ctx->generation = generation;

    LOG_I("下行 VoIP 音视频提交 media sink（默认仅记录日志）");
    LOG_I("正在 WHIP 连接…");

    pthread_mutex_lock(&s_vs_mtx);
    s_active_vs = vs;
    pthread_mutex_unlock(&s_vs_mtx);

    int rc = TiRtcWhipConnect(peer_id, token, _voip_connect_cb, connect_ctx);
    if (rc != 0) {
        LOG_E("TiRtcWhipConnect 调用失败: rc=%d %s", rc, TiRtcGetErrorStr(rc));
        pthread_mutex_lock(&vs->lock);
        if (vs->connect_generation == generation) vs->connect_pending = 0;
        vs->session_state = SESS_IDLE;
        pthread_mutex_unlock(&vs->lock);
        free(connect_ctx);
        pthread_mutex_unlock(&vs->control_lock);
        return -1;
    }
    pthread_mutex_lock(&vs->lock);
    if (vs->connect_generation == generation && vs->connect_pending)
        vs->connect_deadline_ms = now_ms() + VOIP_CONNECT_TIMEOUT_MS;
    pthread_mutex_unlock(&vs->lock);
    LOG_D("TiRtcWhipConnect rc=%d", rc);
    pthread_mutex_unlock(&vs->control_lock);
    return 0;
}

void voip_stop_session(VoipState *vs) {
    if (!vs) return;
    pthread_mutex_lock(&vs->control_lock);
    pthread_mutex_lock(&vs->lock);
    int had_session = vs->active_hconn_set || vs->connect_pending ||
                      vs->pending_call || vs->outgoing_call;
    vs->connect_generation++;
    vs->connect_pending = 0;
    vs->connect_deadline_ms = 0;
    vs->accept_deadline_ms = 0;
    vs->push_running = 0;
    vs->media_start_pending = 0;
    vs->session_state = SESS_DISCONNECTING;
    int join_push = vs->push_thread_created;
    pthread_t push_thread = vs->push_thread;
    vs->push_thread_created = 0;

    tirtc_conn_t h = vs->active_hconn_set ? vs->active_hconn : NULL;
    vs->active_hconn = NULL;
    vs->active_hconn_set = 0;
    vs->pending_call = 0;
    vs->pending_with_video = 0;
    vs->outgoing_call = 0;
    vs->outgoing_with_video = 0;
    vs->outgoing_openid[0] = '\0';
    vs->outgoing_call_id[0] = '\0';
    vs->outgoing_deadline_ms = 0;
    vs->active_room_id[0] = '\0';
    vs->session_with_video = 0;
    pthread_mutex_unlock(&vs->lock);
    if (h) {
        LOG_I("发送 hangup 命令 0x2001");
        const char *hangup = "{\"reason\":0}";
        TiRtcSendCommand(h, CMD_VOIP_HANGUP, hangup,
                         (uint32_t)strlen(hangup));
        TiRtcDisconnect(h);
    }
    if (join_push && !pthread_equal(pthread_self(), push_thread))
        pthread_join(push_thread, NULL);
    pthread_mutex_lock(&vs->lock);
    vs->session_state = SESS_IDLE;
    pthread_mutex_unlock(&vs->lock);
    LOG_I("会话已停止");
    if (had_session && vs->on_session_end) vs->on_session_end(vs->on_session_end_user);
    pthread_mutex_unlock(&vs->control_lock);
}

int voip_is_active(const VoipState *vs) {
    if (!vs) return 0;
    VoipState *mutable_vs = (VoipState *)vs;
    pthread_mutex_lock(&mutable_vs->lock);
    int active = mutable_vs->active_hconn_set || mutable_vs->connect_pending ||
                 mutable_vs->pending_call || mutable_vs->outgoing_call;
    pthread_mutex_unlock(&mutable_vs->lock);
    return active;
}

int voip_has_pending(VoipState *vs) {
    if (!vs) return 0;
    pthread_mutex_lock(&vs->lock);
    int pending = vs->pending_call;
    pthread_mutex_unlock(&vs->lock);
    return pending;
}

int voip_matches_active_room(VoipState *vs, const char *room_id) {
    if (!vs || !room_id || !room_id[0]) return 0;
    pthread_mutex_lock(&vs->lock);
    int matches = vs->active_room_id[0] &&
                  strcmp(vs->active_room_id, room_id) == 0;
    pthread_mutex_unlock(&vs->lock);
    return matches;
}

int voip_has_pending_or_outgoing(VoipState *vs) {
    if (!vs) return 0;
    pthread_mutex_lock(&vs->lock);
    int waiting = vs->pending_call || vs->outgoing_call;
    pthread_mutex_unlock(&vs->lock);
    return waiting;
}

int voip_copy_pending_room(VoipState *vs, char *room_id_out,
                           size_t room_id_size) {
    if (!vs || !room_id_out || room_id_size == 0) return -1;
    pthread_mutex_lock(&vs->lock);
    int pending = vs->pending_call;
    if (pending)
        str_copy(room_id_out, room_id_size, vs->pending_room_id);
    else
        room_id_out[0] = '\0';
    pthread_mutex_unlock(&vs->lock);
    return pending ? 0 : -1;
}

int voip_expire_pending(VoipState *vs, char *room_id_out,
                        size_t room_id_size) {
    if (!vs || !room_id_out || room_id_size == 0) return 0;
    pthread_mutex_lock(&vs->lock);
    int expired = vs->pending_call && vs->pending_deadline_ms > 0 &&
                  now_ms() >= vs->pending_deadline_ms;
    if (expired) {
        str_copy(room_id_out, room_id_size, vs->pending_room_id);
        vs->pending_call = 0;
        vs->pending_with_video = 0;
        vs->pending_deadline_ms = 0;
        vs->incoming_generation++;
    }
    pthread_mutex_unlock(&vs->lock);
    if (expired)
        LOG_W("VoIP 来电等待接听超时，已清理 room=%s", room_id_out);
    return expired;
}

int voip_expire_outgoing(VoipState *vs) {
    if (!vs) return 0;
    pthread_mutex_lock(&vs->lock);
    int expired = vs->outgoing_call && vs->outgoing_deadline_ms > 0 &&
                  now_ms() >= vs->outgoing_deadline_ms;
    if (expired) {
        if (vs->outgoing_call_id[0]) {
            STR_COPY(vs->ignored_call_id, vs->outgoing_call_id);
            vs->ignored_call_until_ms = now_ms() + 60000;
        }
        vs->outgoing_call = 0;
        vs->outgoing_with_video = 0;
        vs->outgoing_openid[0] = '\0';
        vs->outgoing_call_id[0] = '\0';
        vs->outgoing_deadline_ms = 0;
    }
    voip_session_end_cb callback = vs->on_session_end;
    void *callback_user = vs->on_session_end_user;
    pthread_mutex_unlock(&vs->lock);
    if (expired) {
        LOG_W("VoIP 外呼等待超时（30s），已清理本地状态");
        if (callback) callback(callback_user);
    }
    return expired;
}

int voip_expire_connection(VoipState *vs) {
    if (!vs) return 0;
    pthread_mutex_lock(&vs->lock);
    int64_t now = now_ms();
    int connect_expired =
        vs->connect_pending && vs->connect_deadline_ms > 0 &&
        now >= vs->connect_deadline_ms;
    int accept_expired =
        vs->active_hconn_set && vs->session_state == SESS_CONNECTING &&
        vs->accept_deadline_ms > 0 && now >= vs->accept_deadline_ms;
    int expired = connect_expired || accept_expired;
    if (expired) {
        /* Timeout wins atomically.  Late callbacks observe a stale generation
         * or DISCONNECTING and cannot reactivate the session. */
        vs->connect_generation++;
        vs->session_state = SESS_DISCONNECTING;
        vs->connect_deadline_ms = 0;
        vs->accept_deadline_ms = 0;
    }
    pthread_mutex_unlock(&vs->lock);
    if (expired) {
        LOG_W("%s超时，结束 VoIP 会话",
              connect_expired ? "等待 WHIP 连接回调" :
                                "等待 0x2000 建连确认");
        voip_stop_session(vs);
    }
    return expired;
}

#ifdef DEVICE_SIM_TESTING
void voip_test_force_outgoing_timeout(VoipState *vs) {
    if (!vs) return;
    pthread_mutex_lock(&vs->lock);
    vs->outgoing_call = 1;
    vs->outgoing_deadline_ms = now_ms() - 1;
    pthread_mutex_unlock(&vs->lock);
}

void voip_test_force_connect_timeout(VoipState *vs) {
    if (!vs) return;
    pthread_mutex_lock(&vs->lock);
    vs->session_state = SESS_CONNECTING;
    vs->connect_pending = 1;
    vs->connect_deadline_ms = now_ms() - 1;
    pthread_mutex_unlock(&vs->lock);
}
#endif

void voip_clear_pending_local(VoipState *vs) {
    if (!vs) return;
    pthread_mutex_lock(&vs->lock);
    if (vs->pending_call) {
        vs->pending_call = 0;
        vs->pending_with_video = 0;
        vs->pending_deadline_ms = 0;
        vs->incoming_generation++;
    }
    pthread_mutex_unlock(&vs->lock);
}

int voip_accept_pending(VoipState *vs) {
    if (!vs) return -1;
    char peer_id[1024] = "", token[1024] = "", room_id[128] = "";
    pthread_mutex_lock(&vs->lock);
    int pending = vs->pending_call;
    if (pending) {
        snprintf(peer_id, sizeof(peer_id), "%s", vs->pending_peer_id);
        snprintf(token, sizeof(token), "%s", vs->pending_token);
        snprintf(room_id, sizeof(room_id), "%s", vs->pending_room_id);
        vs->session_with_video = vs->pending_with_video;
        vs->pending_call = 0;
        vs->pending_with_video = 0;
        vs->pending_deadline_ms = 0;
        snprintf(vs->active_room_id, sizeof(vs->active_room_id), "%s", room_id);
    }
    pthread_mutex_unlock(&vs->lock);
    if (!pending) { LOG_W("没有待处理的来电"); return -1; }
    return voip_start_session(vs, peer_id, token, vs->voip_audio);
}

int voip_reject_pending(VoipState *vs) {
    if (!vs) return -1;
    char app_id[64] = "", model_id[64] = "", session_token[256] = "", room_id[128] = "", payload[512] = "";
    pthread_mutex_lock(&vs->lock);
    int pending = vs->pending_call;
    if (pending) {
        snprintf(app_id, sizeof(app_id), "%s", vs->pending_app_id);
        snprintf(model_id, sizeof(model_id), "%s", vs->pending_model_id);
        snprintf(session_token, sizeof(session_token), "%s", vs->pending_session_token);
        snprintf(room_id, sizeof(room_id), "%s", vs->pending_room_id);
        snprintf(payload, sizeof(payload), "%s", vs->pending_payload);
        vs->pending_call = 0;
        vs->pending_with_video = 0;
        vs->pending_deadline_ms = 0;
        vs->incoming_generation++;
    }
    pthread_mutex_unlock(&vs->lock);
    if (!pending) { LOG_W("没有待处理的来电"); return -1; }
    return (app_id[0] && model_id[0]) ? voip_reject_session(app_id, model_id, session_token, room_id, payload, 7) : 0;
}

int voip_dial_authorized_ex(VoipState *vs, int index,
                            const char *call_type) {
    if (!vs) {
        LOG_W("无效的授权用户下标: %d", index);
        return -1;
    }
    pthread_mutex_lock(&vs->lock);
    cJSON *caller = NULL;
    if (vs->auth_list && index >= 0 && index < cJSON_GetArraySize(vs->auth_list))
        caller = cJSON_Duplicate(cJSON_GetArrayItem(vs->auth_list, index), 1);
    pthread_mutex_unlock(&vs->lock);
    if (!caller) {
        LOG_W("无效的授权用户下标: %d", index);
        return -1;
    }
    int rc = voip_do_outgoing_call_ex(vs, caller, call_type);
    cJSON_Delete(caller);
    return rc;
}

int voip_dial_authorized(VoipState *vs, int index) {
    return voip_dial_authorized_ex(vs, index, NULL);
}

int voip_list_authorized(VoipState *vs) {
    if (!vs) return 0;
    pthread_mutex_lock(&vs->lock);
    cJSON *snapshot = vs->auth_list ? cJSON_Duplicate(vs->auth_list, 1) : NULL;
    pthread_mutex_unlock(&vs->lock);
    int count = snapshot ? cJSON_GetArraySize(snapshot) : 0;
    if (!count) {
        LOG_I("微信联系人列表为空");
        cJSON_Delete(snapshot);
        return 0;
    }
    for (int i = 0; i < count; ++i) {
        cJSON *item = cJSON_GetArrayItem(snapshot, i);
        cJSON *openid = cJSON_GetObjectItem(item, "wx_open_id");
        cJSON *remark = cJSON_GetObjectItem(item, "remark");
        LOG_I("  " C_YELLOW "[%d]" C_RESET " remark=%s wx_open_id=%s",
              i,
              cJSON_IsString(remark) && remark->valuestring[0]
                  ? remark->valuestring : "未命名",
              cJSON_IsString(openid) ? openid->valuestring : "-");
    }
    cJSON_Delete(snapshot);
    return count;
}

cJSON *voip_find_authorized(VoipState *vs, const char *wx_open_id) {
    if (!vs || !wx_open_id || !wx_open_id[0]) return NULL;
    pthread_mutex_lock(&vs->lock);
    cJSON *copy = NULL;
    cJSON *item = NULL;
    cJSON_ArrayForEach(item, vs->auth_list) {
        cJSON *openid = cJSON_GetObjectItem(item, "wx_open_id");
        if (cJSON_IsString(openid) &&
            strcmp(openid->valuestring, wx_open_id) == 0) {
            copy = cJSON_Duplicate(item, 1);
            break;
        }
    }
    pthread_mutex_unlock(&vs->lock);
    return copy;
}

/* ── Reject session ──────────────────────────────────────────────────────── */

static void _reject_cb(const char *body, void *user_data) {
    LOG_D("Reject 响应已收到%s", body ? "" : "（无响应体）");
    (void)user_data;
}

int voip_reject_session(const char *wx_app_id, const char *wx_model_id,
                        const char *wx_session_token, const char *wx_room_id,
                        const char *wx_payload, int hangup_reason) {
    cJSON *root = cJSON_CreateObject();
    if (!root || !cJSON_AddStringToObject(root, "wx_app_id", wx_app_id) ||
        !cJSON_AddStringToObject(root, "wx_model_id", wx_model_id) ||
        !cJSON_AddStringToObject(root, "wx_session_token", wx_session_token) ||
        !cJSON_AddStringToObject(root, "wx_room_id", wx_room_id) ||
        !cJSON_AddStringToObject(root, "wx_payload", wx_payload ? wx_payload : "") ||
        !cJSON_AddNumberToObject(root, "hangup_reason", hangup_reason)) {
        cJSON_Delete(root);
        LOG_W("拒接会话 JSON 分配失败");
        return -1;
    }
    char *json_str = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json_str) {
        LOG_W("拒接会话 JSON 序列化失败");
        return -1;
    }

    LOG_I("拒接会话 room=%s reason=%d", wx_room_id, hangup_reason);

    int rc = TiRtcServiceRequest("/v1/wxvoip/reject", json_str, NULL, _reject_cb, NULL);
    if (rc != 0)
        LOG_E("TiRtcServiceRequest(reject) 返回 %d", rc);
    free(json_str);
    return rc;
}

/* ── Outgoing call ───────────────────────────────────────────────────────── */

int voip_do_outgoing_call_ex(VoipState *vs, const cJSON *caller,
                             const char *call_type) {
    cJSON *app_id   = cJSON_GetObjectItem(caller, "wx_app_id");
    cJSON *model_id = cJSON_GetObjectItem(caller, "wx_model_id");
    cJSON *openid   = cJSON_GetObjectItem(caller, "wx_open_id");
    cJSON *remark   = cJSON_GetObjectItem(caller, "remark");

    if (!app_id || !model_id || !openid ||
        !cJSON_IsString(app_id) || !cJSON_IsString(model_id) || !cJSON_IsString(openid)) {
        LOG_W("呼叫方记录缺少 wx_app_id/wx_model_id/wx_open_id");
        return -1;
    }

    if (call_type && strcmp(call_type, "video") != 0 &&
        strcmp(call_type, "audio") != 0) {
        LOG_W("微信通话类型必须是 video 或 audio");
        return -1;
    }
    int video_call = call_type
                         ? strcmp(call_type, "video") == 0
                         : (vs->voip_video[0] && vs->up_video_format);
    if (video_call && (!vs->voip_video[0] || !vs->up_video_format)) {
        LOG_W("未配置上行视频源，不能发起视频微信通话");
        return -1;
    }
    cJSON *body = cJSON_CreateObject();
    if (!body || !cJSON_AddStringToObject(body, "device_id", vs->device_id) ||
        !cJSON_AddStringToObject(body, "wx_app_id", app_id->valuestring) ||
        !cJSON_AddStringToObject(body, "wx_user_openid", openid->valuestring) ||
        !cJSON_AddStringToObject(body, "wx_model_id", model_id->valuestring) ||
        !cJSON_AddStringToObject(body, "wx_room_type",
                                 video_call ? "video" : "voice") ||
        !cJSON_AddNumberToObject(body, "wx_version_type", 2)) {
        cJSON_Delete(body);
        LOG_W("微信 VoIP 外呼 JSON 分配失败");
        return -1;
    }
    char *json_str = cJSON_PrintUnformatted(body);
    cJSON_Delete(body);
    if (!json_str) {
        LOG_W("微信 VoIP 外呼 JSON 序列化失败");
        return -1;
    }

    char url[512];
    snprintf(url, sizeof(url), "%s/v1/voip/device/call", vs->voip_server);

    /*
     * Reserve the local outgoing state before issuing HTTP.  The MQTT room
     * callback can arrive before the HTTP response; setting this afterwards
     * would misclassify our own room as a new incoming call and then re-arm
     * the outgoing state after the room was already connected.
     */
    pthread_mutex_lock(&vs->lock);
    if (vs->outgoing_call || vs->active_hconn_set || vs->connect_pending ||
        vs->pending_call) {
        pthread_mutex_unlock(&vs->lock);
        free(json_str);
        LOG_W("已有 VoIP 会话或来电等待，不能重复外呼");
        return -1;
    }
    vs->outgoing_call = 1;
    vs->outgoing_with_video = video_call;
    STR_COPY(vs->outgoing_openid, openid->valuestring);
    vs->outgoing_call_id[0] = '\0';
    vs->outgoing_deadline_ms = 0;
    pthread_mutex_unlock(&vs->lock);

    char resp_buf[4096];
    long http_code = 0;
    int ret = _http_post_auth(url, vs->mqtt_token, json_str,
                              resp_buf, sizeof(resp_buf), &http_code);
    free(json_str);

    if (ret == 0) {
        cJSON *r = cJSON_Parse(resp_buf);
        cJSON *code = r ? cJSON_GetObjectItem(r, "code") : NULL;
        int c = code && cJSON_IsNumber(code) ? code->valueint : -1;
        if (http_code == 200 && c == 0) {
            cJSON *data = cJSON_GetObjectItem(r, "data");
            cJSON *call_id = data ? cJSON_GetObjectItem(data, "call_id") : NULL;
            LOG_I("外呼已发起 -> 联系人=%s openid=%s",
                  (remark && cJSON_IsString(remark) && remark->valuestring[0])
                      ? remark->valuestring : "未命名",
                  openid->valuestring);
            pthread_mutex_lock(&vs->lock);
            if (vs->outgoing_call &&
                strcmp(vs->outgoing_openid, openid->valuestring) == 0) {
                if (call_id && cJSON_IsString(call_id))
                    STR_COPY(vs->outgoing_call_id, call_id->valuestring);
                vs->outgoing_deadline_ms = now_ms() + 30000;
            }
            pthread_mutex_unlock(&vs->lock);
        } else {
            if (http_code == 401) {
                LOG_W("设备登录凭证无效或已过期，请重新获取 mqtt_token");
            } else if (c == 40205) {
                LOG_W("微信授权已失效，请让用户打开小程序重新授权 (code=%d)", c);
                voip_on_callers_update(vs);
            } else if (c == 6006) {
                LOG_W("设备已解绑，请重新完成设备绑定 (code=%d)", c);
            } else {
                LOG_W("外呼失败 code=%d", c);
            }
            ret = -1;
        }
        cJSON_Delete(r);
    }
    if (ret != 0) {
        pthread_mutex_lock(&vs->lock);
        if (vs->outgoing_call &&
            strcmp(vs->outgoing_openid, openid->valuestring) == 0) {
            vs->outgoing_call = 0;
            vs->outgoing_with_video = 0;
            vs->outgoing_openid[0] = '\0';
            vs->outgoing_call_id[0] = '\0';
            vs->outgoing_deadline_ms = 0;
        }
        pthread_mutex_unlock(&vs->lock);
    }
    return ret;
}

int voip_do_outgoing_call(VoipState *vs, const cJSON *caller) {
    return voip_do_outgoing_call_ex(vs, caller, NULL);
}

/* ── MQTT message handlers ───────────────────────────────────────────────── */

static const char *json_string_or_empty(const cJSON *object, const char *key) {
    const cJSON *item = cJSON_GetObjectItemCaseSensitive(object, key);
    return cJSON_IsString(item) && item->valuestring ? item->valuestring : "";
}

static void voip_incoming_remark(VoipState *vs, const cJSON *payload,
                                 const char *openid, char *out, size_t out_cap) {
    if (!out || out_cap == 0) return;
    out[0] = '\0';
    const char *keys[] = {"wx_user_remark", "remark", "wx_user_nickname"};
    for (size_t i = 0; i < sizeof(keys) / sizeof(keys[0]); i++) {
        cJSON *value = cJSON_GetObjectItem(payload, keys[i]);
        if (value && cJSON_IsString(value) && value->valuestring[0]) {
            snprintf(out, out_cap, "%s", value->valuestring);
            return;
        }
    }
    if (!openid || !openid[0]) return;
    pthread_mutex_lock(&vs->lock);
    cJSON *contact = NULL;
    cJSON_ArrayForEach(contact, vs->auth_list) {
        cJSON *contact_openid = cJSON_GetObjectItem(contact, "wx_open_id");
        cJSON *contact_remark = cJSON_GetObjectItem(contact, "remark");
        if (contact_openid && cJSON_IsString(contact_openid) &&
            strcmp(contact_openid->valuestring, openid) == 0 &&
            contact_remark && cJSON_IsString(contact_remark)) {
            snprintf(out, out_cap, "%s", contact_remark->valuestring);
            break;
        }
    }
    pthread_mutex_unlock(&vs->lock);
}

void voip_on_call_incoming(void *ctx, const struct cJSON *payload) {
    VoipState *vs = (VoipState *)ctx;
    if (!vs || !payload) return;

    const char *peer_id  = json_string_or_empty(payload, "peer_id");
    const char *token    = json_string_or_empty(payload, "token");
    const char *room_id  = json_string_or_empty(payload, "wx_room_id");
    const char *openid   = json_string_or_empty(payload, "wx_user_openid");
    const char *app_id   = json_string_or_empty(payload, "wx_app_id");
    const char *model_id = json_string_or_empty(payload, "wx_model_id");
    const char *sess_tok = json_string_or_empty(payload, "wx_server_token");
    const char *wx_pay   = json_string_or_empty(payload, "wx_payload");
    const char *call_id  = json_string_or_empty(payload, "wx_call_id");
    const char *wx_from  = json_string_or_empty(payload, "wx_from");
    const char *room_type = json_string_or_empty(payload, "wx_room_type");
    char remark[256];
    voip_incoming_remark(vs, payload, openid, remark, sizeof(remark));

    pthread_mutex_lock(&vs->lock);
    int is_outgoing     = vs->outgoing_call;
    int outgoing_with_video = vs->outgoing_with_video;
    int has_pending     = vs->pending_call;
    int is_active       = vs->active_hconn_set || vs->connect_pending;
    int video_configured = vs->voip_video[0] && vs->up_video_format;
    char out_openid[64];
    char out_call_id[64];
    char ignored_call_id[64];
    STR_COPY(out_openid, vs->outgoing_openid);
    STR_COPY(out_call_id, vs->outgoing_call_id);
    STR_COPY(ignored_call_id, vs->ignored_call_id);
    int ignored = call_id[0] && ignored_call_id[0] &&
                  strcmp(call_id, ignored_call_id) == 0 &&
                  now_ms() < vs->ignored_call_until_ms;
    pthread_mutex_unlock(&vs->lock);

    int room_with_video = is_outgoing
                              ? outgoing_with_video
                              : (!room_type[0] ||
                                 strcmp(room_type, "voice") != 0);
    room_with_video = room_with_video && video_configured;

    if (ignored) {
        LOG_W("忽略已经取消或超时的外呼回铃 call_id=%s room=%s", call_id, room_id);
        if (app_id[0] && model_id[0])
            (void)voip_reject_incoming_payload_async(vs, payload, 7);
        return;
    }

    int openid_matches = !openid[0] || strcmp(openid, out_openid) == 0;
    int matches_outgoing = is_outgoing &&
        (out_call_id[0]
             ? (call_id[0] && strcmp(call_id, out_call_id) == 0 && openid_matches)
             : (call_id[0]
                    ? (strcmp(wx_from, vs->device_id) == 0 && openid_matches)
                    : openid_matches));

    /* Conflict: an unrelated room arrived while an outbound call is pending. */
    if (is_outgoing && !matches_outgoing) {
        LOG_W("外呼进行中，自动拒接不匹配来电 room=%s call_id=%s", room_id, call_id);
        if (app_id[0] && model_id[0])
            (void)voip_reject_incoming_payload_async(vs, payload, 5);
        return;
    }

    /* Already in call or have pending -> auto reject */
    if (is_active || has_pending) {
        LOG_W("%s，自动拒接", is_active ? "已在通话中" : "有待处理来电");
        if (app_id[0] && model_id[0])
            (void)voip_reject_incoming_payload_async(vs, payload, 7);
        return;
    }

    /* Outgoing call callback — auto answer */
    int own_outgoing_recovery = !is_outgoing && call_id[0] &&
                                strcmp(wx_from, vs->device_id) == 0;
    if (matches_outgoing || own_outgoing_recovery) {
        if (own_outgoing_recovery && vs->before_recovered_start &&
            vs->before_recovered_start(vs->before_recovered_start_user) != 0) {
            LOG_W("恢复本设备外呼时协调器忙，拒绝 room=%s", room_id);
            if (app_id[0] && model_id[0])
                (void)voip_reject_incoming_payload_async(vs, payload, 5);
            return;
        }
        if (!peer_id[0] || !token[0]) {
            LOG_W("外呼回铃缺少 peer_id/token，无法连接 room=%s", room_id);
            pthread_mutex_lock(&vs->lock);
            if (matches_outgoing) {
                vs->outgoing_call = 0;
                vs->outgoing_with_video = 0;
                vs->outgoing_openid[0] = '\0';
                vs->outgoing_call_id[0] = '\0';
                vs->outgoing_deadline_ms = 0;
            }
            pthread_mutex_unlock(&vs->lock);
            if (vs->on_session_end)
                vs->on_session_end(vs->on_session_end_user);
            return;
        }
        LOG_I("外呼已接听 room=%s，自动连接", room_id);
        pthread_mutex_lock(&vs->lock);
        if (matches_outgoing) {
            vs->outgoing_call = 0;
            vs->outgoing_with_video = 0;
            vs->outgoing_openid[0] = '\0';
            vs->outgoing_call_id[0] = '\0';
            vs->outgoing_deadline_ms = 0;
        }
        STR_COPY(vs->active_room_id, room_id);
        vs->session_with_video = room_with_video;
        pthread_mutex_unlock(&vs->lock);
        if (voip_start_session(vs, peer_id, token, vs->voip_audio) != 0) {
            pthread_mutex_lock(&vs->lock);
            vs->active_room_id[0] = '\0';
            pthread_mutex_unlock(&vs->lock);
            if (vs->on_session_end) vs->on_session_end(vs->on_session_end_user);
        }
        return;
    }

    /* New incoming call */
    pthread_mutex_lock(&vs->lock);
    vs->incoming_generation++;
    vs->pending_call = 1;
    vs->pending_with_video = room_with_video;
    vs->pending_generation = vs->incoming_generation;
    vs->pending_deadline_ms = now_ms() + 45000;
    STR_COPY(vs->pending_peer_id, peer_id); STR_COPY(vs->pending_token, token);
    STR_COPY(vs->pending_room_id, room_id); STR_COPY(vs->pending_openid, openid);
    STR_COPY(vs->pending_app_id, app_id); STR_COPY(vs->pending_model_id, model_id);
    STR_COPY(vs->pending_session_token, sess_tok); STR_COPY(vs->pending_payload, wx_pay);
    pthread_mutex_unlock(&vs->lock);

    printf(C_YELLOW "\n╔══════════════════════════════════════╗\n" C_RESET);
    printf(C_YELLOW "║ \033[1m微信来电!\033[0m 联系人=%s\n" C_RESET,
           remark[0] ? remark : "未命名");
    printf(C_YELLOW "║ openid=%s\n" C_RESET, openid);
    printf(C_YELLOW "║ room=%s\n" C_RESET, room_id);
    printf(C_YELLOW "║ 输入 'yes' 接听, 'no' 拒接\n" C_RESET);
    printf(C_YELLOW "╚══════════════════════════════════════╝\n\n" C_RESET);
    fflush(stdout);
}

int voip_reject_incoming_payload(const cJSON *payload, int reason) {
    if (!payload) return -1;
    const char *app_id = json_string_or_empty(payload, "wx_app_id");
    const char *model_id = json_string_or_empty(payload, "wx_model_id");
    const char *session_token =
        json_string_or_empty(payload, "wx_server_token");
    const char *room_id = json_string_or_empty(payload, "wx_room_id");
    const char *wx_payload = json_string_or_empty(payload, "wx_payload");
    if (!app_id[0] || !model_id[0]) return -1;
    return voip_reject_session(app_id, model_id, session_token, room_id,
                               wx_payload, reason);
}

int voip_reject_incoming_payload_async(VoipState *vs,
                                       const cJSON *payload, int reason) {
    if (!vs || !payload) return -1;
    const char *app_id = json_string_or_empty(payload, "wx_app_id");
    const char *model_id = json_string_or_empty(payload, "wx_model_id");
    if (!app_id[0] || !model_id[0]) return -1;

    pthread_mutex_lock(&vs->reject_lock);
    if (vs->reject_stop ||
        vs->reject_count == VOIP_REJECT_QUEUE_CAPACITY) {
        pthread_mutex_unlock(&vs->reject_lock);
        LOG_W("VoIP 忙线拒接队列已停止或已满");
        return -1;
    }
    size_t tail =
        (vs->reject_head + vs->reject_count) % VOIP_REJECT_QUEUE_CAPACITY;
    STR_COPY(vs->reject_queue[tail].app_id, app_id);
    STR_COPY(vs->reject_queue[tail].model_id, model_id);
    STR_COPY(vs->reject_queue[tail].session_token,
             json_string_or_empty(payload, "wx_server_token"));
    STR_COPY(vs->reject_queue[tail].room_id,
             json_string_or_empty(payload, "wx_room_id"));
    STR_COPY(vs->reject_queue[tail].payload,
             json_string_or_empty(payload, "wx_payload"));
    vs->reject_queue[tail].reason = reason;
    vs->reject_count++;
    pthread_cond_signal(&vs->reject_ready);
    pthread_mutex_unlock(&vs->reject_lock);
    return 0;
}

static void *voip_refresh_callers_worker(void *opaque) {
    VoipState *vs = opaque;
    pthread_mutex_lock(&vs->lock);
    for (;;) {
        while (!vs->callers_refresh_stop &&
               !vs->callers_refresh_requested)
            pthread_cond_wait(&vs->callers_refresh_cond, &vs->lock);
        if (vs->callers_refresh_stop)
            break;

        vs->callers_refresh_requested = 0;
        vs->callers_refresh_running = 1;
        pthread_mutex_unlock(&vs->lock);

        cJSON *new_auth_list = NULL;
        if (voip_report_profile(vs->voip_server, vs->mqtt_token,
                                &new_auth_list) == 0) {
            voip_set_auth_list(vs, new_auth_list);
        } else {
            cJSON_Delete(new_auth_list);
            LOG_W("授权列表刷新失败，保留上一次联系人列表");
        }

        pthread_mutex_lock(&vs->lock);
        vs->callers_refresh_running = 0;
        pthread_cond_broadcast(&vs->callers_refresh_cond);
    }
    vs->callers_refresh_running = 0;
    pthread_cond_broadcast(&vs->callers_refresh_cond);
    pthread_mutex_unlock(&vs->lock);
    return NULL;
}

void voip_on_callers_update(void *ctx) {
    VoipState *vs = (VoipState *)ctx;
    if (!vs) return;
    pthread_mutex_lock(&vs->lock);
    if (vs->callers_refresh_stop) {
        pthread_mutex_unlock(&vs->lock);
        return;
    }
    if (vs->callers_refresh_running || vs->callers_refresh_requested) {
        vs->callers_refresh_requested = 1;
        pthread_cond_signal(&vs->callers_refresh_cond);
        pthread_mutex_unlock(&vs->lock);
        LOG_D("授权列表正在刷新，合并本次 callers_update");
        return;
    }
    vs->callers_refresh_requested = 1;
    pthread_cond_signal(&vs->callers_refresh_cond);
    pthread_mutex_unlock(&vs->lock);
    LOG_D("呼叫方列表已更新，后台重新获取…");
}

void voip_on_call_cancel(void *ctx, const struct cJSON *payload) {
    VoipState *vs = (VoipState *)ctx;
    if (!vs) return;
    const char *room_id = json_string_or_empty(payload, "wx_room_id");

    LOG_W("远端取消/拒接通话 room_id=%s", room_id);

    pthread_mutex_lock(&vs->lock);
    if (vs->pending_call && strcmp(vs->pending_room_id, room_id) == 0) {
        vs->pending_call = 0;
        vs->pending_with_video = 0;
        vs->pending_deadline_ms = 0;
        vs->incoming_generation++;
    }
    int active_match = vs->active_room_id[0] &&
                       strcmp(vs->active_room_id, room_id) == 0;
    if (active_match) {
        int should_stop = vs->active_hconn_set || vs->connect_pending;
        vs->active_room_id[0] = '\0';
        pthread_mutex_unlock(&vs->lock);
        if (should_stop)
            voip_stop_session(vs);
        return;
    }
    pthread_mutex_unlock(&vs->lock);
}

/* ── Command input loop ──────────────────────────────────────────────────── */

void voip_cmd_loop(VoipState *vs) {
    PHASE_TITLE("VoIP 命令已就绪");
    CMD_HINT("wxcall  - 拨打授权用户");
    CMD_HINT("yes     - 接听来电");
    CMD_HINT("no      - 拒接来电");
    CMD_HINT("cancel  - 取消外呼");
    CMD_HINT("hangup  - 挂断");
    CMD_HINT("exit    - 退出程序");

    char *line = NULL;
    size_t linecap = 0;

    while (!g_stop) {
        voip_expire_outgoing(vs);
        voip_expire_connection(vs);

        fd_set fds;
        FD_ZERO(&fds);
        FD_SET(STDIN_FILENO, &fds);
        struct timeval tv = { 0, 100000 };  /* 100ms */
        if (select(STDIN_FILENO + 1, &fds, NULL, NULL, &tv) <= 0)
            continue;

        errno = 0;
        ssize_t n = getline(&line, &linecap, stdin);
        if (n <= 0) { sleep_ms(100); continue; }

        /* Strip newline */
        if (n > 0 && line[n-1] == '\n') line[--n] = '\0';
        if (n == 0) continue;

        LOG_D("cmd: %s", line);

        if (strcmp(line, "yes") == 0) {
            pthread_mutex_lock(&vs->lock);
            int has = vs->pending_call;
            char pid[1024], tok[1024], rid[128];
            STR_COPY(pid, vs->pending_peer_id); STR_COPY(tok, vs->pending_token);
            STR_COPY(rid, vs->pending_room_id);
            if (has) {
                vs->session_with_video = vs->pending_with_video;
                vs->pending_call = 0;
                vs->pending_with_video = 0;
                STR_COPY(vs->active_room_id, rid);
            }
            pthread_mutex_unlock(&vs->lock);
            if (!has) { LOG_W("没有待处理的来电"); continue; }
            voip_start_session(vs, pid, tok, vs->voip_audio);
        }
        else if (strcmp(line, "no") == 0) {
            pthread_mutex_lock(&vs->lock);
            int has = vs->pending_call;
            char aid[64], mid[64], stok[256], rid[128], pay[512];
            STR_COPY(aid, vs->pending_app_id); STR_COPY(mid, vs->pending_model_id);
            STR_COPY(stok, vs->pending_session_token); STR_COPY(rid, vs->pending_room_id);
            STR_COPY(pay, vs->pending_payload);
            if (has) {
                vs->pending_call = 0;
                vs->pending_with_video = 0;
            }
            pthread_mutex_unlock(&vs->lock);
            if (!has) { LOG_W("没有待处理的来电"); continue; }
            if (aid[0] && mid[0])
                voip_reject_session(aid, mid, stok, rid, pay, 7);
        }
        else if (strcmp(line, "wxcall") == 0) {
            pthread_mutex_lock(&vs->lock);
            cJSON *auth_snapshot = vs->auth_list
                ? cJSON_Duplicate(vs->auth_list, 1)
                : NULL;
            pthread_mutex_unlock(&vs->lock);
            if (!auth_snapshot || cJSON_GetArraySize(auth_snapshot) == 0) {
                cJSON_Delete(auth_snapshot);
                LOG_W("没有授权的呼叫方");
                continue;
            }
            int count = cJSON_GetArraySize(auth_snapshot);
            for (int i = 0; i < count; i++) {
                cJSON *item = cJSON_GetArrayItem(auth_snapshot, i);
                cJSON *oid  = cJSON_GetObjectItem(item, "wx_open_id");
                cJSON *remark = cJSON_GetObjectItem(item, "remark");
                LOG_I("  [%d] %s  openid=%.24s...", i,
                      (remark && cJSON_IsString(remark) && remark->valuestring[0]) ? remark->valuestring : "未命名",
                      (oid && cJSON_IsString(oid)) ? oid->valuestring : "?");
            }
            printf(C_YELLOW "输入序号 (Enter 取消): " C_RESET); fflush(stdout);
            char idx_buf[16];
            if (!fgets(idx_buf, sizeof(idx_buf), stdin)) {
                cJSON_Delete(auth_snapshot);
                continue;
            }
            int idx = atoi(idx_buf);
            if (idx < 0 || idx >= count) {
                LOG_W("无效索引: %d", idx);
                cJSON_Delete(auth_snapshot);
                continue;
            }
            cJSON *item = cJSON_GetArrayItem(auth_snapshot, idx);
            voip_do_outgoing_call_ex(
                vs, item, vs->voip_video[0] ? "video" : "audio");
            cJSON_Delete(auth_snapshot);
        }
        else if (strcmp(line, "cancel") == 0) {
            pthread_mutex_lock(&vs->lock);
            int active = vs->active_hconn_set || vs->connect_pending;
            int outgo  = vs->outgoing_call;
            pthread_mutex_unlock(&vs->lock);
            if (active) {
                voip_stop_session(vs);
                LOG_I("通话已结束");
            } else if (outgo) {
                pthread_mutex_lock(&vs->lock);
                if (vs->outgoing_call_id[0]) {
                    STR_COPY(vs->ignored_call_id, vs->outgoing_call_id);
                    vs->ignored_call_until_ms = now_ms() + 60000;
                }
                vs->outgoing_call = 0;
                vs->outgoing_with_video = 0;
                vs->outgoing_openid[0] = '\0';
                vs->outgoing_call_id[0] = '\0';
                vs->outgoing_deadline_ms = 0;
                pthread_mutex_unlock(&vs->lock);
                LOG_W("外呼已取消 (本地)");
            } else {
                LOG_W("没有可取消的通话");
            }
        }
        else if (strcmp(line, "hangup") == 0) {
            if (!vs->active_hconn_set && !vs->connect_pending && !vs->pending_call) {
                LOG_W("没有进行中的通话");
                continue;
            }
            pthread_mutex_lock(&vs->lock);
            vs->active_room_id[0] = '\0';
            pthread_mutex_unlock(&vs->lock);
            voip_stop_session(vs);
            LOG_I("已挂断");
        }
        else if (strcmp(line, "exit") == 0) {
            LOG_I("正在退出…");
            if (vs->active_hconn_set) voip_stop_session(vs);
            g_stop = 1;
            break;
        }
        else {
            LOG_W("未知命令: %s (可用: wxcall/yes/no/cancel/hangup/exit)", line);
        }
    }
    free(line);
}
