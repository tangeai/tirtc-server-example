/** \file tirtc_call.c
 * \brief TiRTC device-to-device P2P call — passive listen + TiRtcConnect retry.
 *
 * Embedded-reference: demonstrates TiRtcConnect for device↔device P2P calling.
 * Reads already-encoded media files and discards received media after logging.
 */

#include "tirtc_call.h"
#include "call_session.h"
#define LOG_MODULE "call"
#include "common.h"

#include <assert.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include <cjson/cJSON.h>

#include "tirtc/tiRTC.h"
#include "file_media_source.h"
#include "media_format.h"
#include "media_rx_log.h"
#include "media_subscription_policy.h"
#include "sdk_callback_guard.h"

/* Reference to external stop flag (set by SIGINT handler in main) */
extern volatile sig_atomic_t g_stop;

/* ── Global SDK state ────────────────────────────────────────────────────── */

static int              s_sdk_running   = 0;
static int              s_sdk_started   = 0;
static int              s_sdk_stopped   = 0;
static pthread_mutex_t  s_state_mtx     = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t   s_started_cond  = PTHREAD_COND_INITIALIZER;
static pthread_cond_t   s_stopped_cond  = PTHREAD_COND_INITIALIZER;

static tirtc_conn_t     s_active_conn   = NULL;
static pthread_mutex_t  s_conn_mtx      = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t  s_call_control_mtx = PTHREAD_MUTEX_INITIALIZER;
static SessionState     s_session_state = SESS_IDLE;
static char             s_expected_room_id[128] = "";

/* Media state */
static FileMediaSource s_media_src;
static int              s_media_running  = 0;
static pthread_t        s_media_thread;
static int              s_media_thread_created;
static int              s_media_start_pending;
static int              s_force_key;
static MediaSubscriptionPolicy s_media_policy;
static char             s_pending_connected_room[128];
static char             s_send_audio_path[512];
static char             s_send_video_path[512];
static const AudioFormat *s_send_audio_format;
static const VideoFormat *s_send_video_format;
static MediaRxLog       s_rx_log = MEDIA_RX_LOG_INITIALIZER;
static SdkCallbackGuard s_call_callback_guard = SDK_CALLBACK_GUARD_INITIALIZER;
static CallState       *s_call_state;

/* P2P callbacks to session layer */
static call_p2p_connected_cb  s_on_p2p_connected = NULL;
static void                  *s_p2p_connected_user = NULL;
static call_connect_failed_cb s_on_connect_failed = NULL;
static void                  *s_connect_failed_user = NULL;

/* Connect callback context (synchronous wait via condvar) */
static pthread_mutex_t  s_connect_mtx   = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t   s_connect_cond  = PTHREAD_COND_INITIALIZER;
static int              s_connect_done  = 0;
static int              s_connect_error = 0;
static tirtc_conn_t     s_connect_hconn = NULL;
static uint64_t         s_connect_generation;
static TIRTCLOGCALLBACK s_log_cb = NULL;

/* Audio/video constants */
#define AUDIO_PKT_SIZE  320   /* 40ms G.711 A-law 8kHz */
#define AUDIO_PKT_MS    40
#define VIDEO_FRAME_MS  66    /* ~15fps */

/* ── Forward declarations ────────────────────────────────────────────────── */

static void *_media_worker(void *arg);
static void _start_media_stream(void);
static void _stop_media_stream(void);
static void _prepare_media_policy(void);
static int _is_audio_call(void);
static void _apply_video_downlink_policy(tirtc_conn_t hconn);

/* ── SDK log callback (debug level) ──────────────────────────────────────── */

static void _call_sdk_log_cb(const char *log, uint32_t length) {
    sdk_callback_enter(&s_call_callback_guard);
    LOG_SDK(log, length);
    sdk_callback_leave(&s_call_callback_guard);
}

/* ── SDK callbacks ───────────────────────────────────────────────────────── */

static void _call_on_event(int event, const void *data, int len) {
    sdk_callback_enter(&s_call_callback_guard);
    (void)data; (void)len;
    if (event == TIRTC_EVENT_SYS_STARTED) {
        pthread_mutex_lock(&s_state_mtx);
        s_sdk_started = 1;
        pthread_cond_signal(&s_started_cond);
        pthread_mutex_unlock(&s_state_mtx);
        LOG_I("SYS_STARTED");
    } else if (event == TIRTC_EVENT_SYS_STOPPED) {
        pthread_mutex_lock(&s_state_mtx);
        s_sdk_stopped = 1;
        pthread_cond_signal(&s_stopped_cond);
        pthread_mutex_unlock(&s_state_mtx);
        LOG_I("SYS_STOPPED");
    }
    sdk_callback_leave(&s_call_callback_guard);
}

static void _notify_call_transport_ended(void) {
    CallState *cs = s_call_state;
    if (!cs) return;
    call_session_cancel_ring_timer(cs);
    pthread_mutex_lock(&cs->lock);
    int was_active = cs->active;
    cs->active = 0;
    cs->room_id[0] = '\0';
    cs->role[0] = '\0';
    cs->active_call_type[0] = '\0';
    void (*on_end)(void *) = cs->on_session_end;
    void *user = cs->runtime_user;
    pthread_mutex_unlock(&cs->lock);
    if (was_active && on_end) on_end(user);
}

static void _finish_call_transport(tirtc_conn_t hconn) {
    int matched;
    pthread_mutex_lock(&s_conn_mtx);
    matched = s_active_conn == hconn;
    if (matched) {
        s_active_conn = NULL;
        s_session_state = SESS_DISCONNECTING;
        s_media_start_pending = 0;
    }
    pthread_mutex_unlock(&s_conn_mtx);
    if (!matched) return;
    _stop_media_stream();
    pthread_mutex_lock(&s_conn_mtx);
    s_session_state = SESS_IDLE;
    pthread_mutex_unlock(&s_conn_mtx);
    _notify_call_transport_ended();
}

static void _deferred_disconnect_call(void *opaque) {
    tirtc_conn_t hconn = (tirtc_conn_t)opaque;
    pthread_mutex_lock(&s_call_control_mtx);
    _finish_call_transport(hconn);
    TiRtcDisconnect(hconn);
    pthread_mutex_unlock(&s_call_control_mtx);
}

static void _deferred_finish_call(void *opaque) {
    pthread_mutex_lock(&s_call_control_mtx);
    _finish_call_transport((tirtc_conn_t)opaque);
    pthread_mutex_unlock(&s_call_control_mtx);
}

static void _deferred_accept_call(void *opaque) {
    tirtc_conn_t hconn = (tirtc_conn_t)opaque;
    pthread_mutex_lock(&s_call_control_mtx);
    pthread_mutex_lock(&s_conn_mtx);
    tirtc_conn_t old = s_active_conn;
    if (old) s_active_conn = NULL;
    pthread_mutex_unlock(&s_conn_mtx);
    if (old && old != hconn) {
        _stop_media_stream();
        TiRtcDisconnect(old);
    }
    pthread_mutex_lock(&s_conn_mtx);
    if (s_sdk_running) {
        s_active_conn = hconn;
        s_session_state = SESS_CONNECTING;
        media_rx_log_reset(&s_rx_log);
    }
    pthread_mutex_unlock(&s_conn_mtx);
    if (!s_sdk_running) {
        TiRtcDisconnect(hconn);
        pthread_mutex_unlock(&s_call_control_mtx);
        return;
    }
    LOG_I("收到入站 P2P 连接 hconn=%p（等待 0x2000 接通确认）",
          (void *)hconn);
    _apply_video_downlink_policy(hconn);
    pthread_mutex_unlock(&s_call_control_mtx);
}

static void _deferred_start_call_media(void *opaque) {
    tirtc_conn_t hconn = (tirtc_conn_t)opaque;
    pthread_mutex_lock(&s_call_control_mtx);
    char room_id[128];
    pthread_mutex_lock(&s_conn_mtx);
    int matched = s_active_conn == hconn && s_media_start_pending;
    STR_COPY(room_id, s_pending_connected_room);
    if (matched) {
        s_media_start_pending = 0;
        s_session_state = SESS_IN_CALL;
    }
    pthread_mutex_unlock(&s_conn_mtx);
    if (!matched) {
        pthread_mutex_unlock(&s_call_control_mtx);
        return;
    }
    _start_media_stream();
    if (s_on_p2p_connected)
        s_on_p2p_connected(room_id, s_p2p_connected_user);
    pthread_mutex_unlock(&s_call_control_mtx);
}

static void _call_on_conn_accepted(tirtc_conn_t hconn) {
    sdk_callback_enter(&s_call_callback_guard);
    if (sdk_defer_action(&s_call_callback_guard, _deferred_accept_call,
                         hconn) != 0)
        LOG_E("无法延后处理入站 P2P 连接");
    sdk_callback_leave(&s_call_callback_guard);
}

static void _call_on_conn_error(tirtc_conn_t hconn, int error) {
    sdk_callback_enter(&s_call_callback_guard);
    LOG_E("on_conn_error hconn=%p: %s", (void*)hconn, TiRtcGetErrorStr(error));
    if (sdk_defer_action(&s_call_callback_guard, _deferred_disconnect_call,
                         hconn) != 0)
        LOG_E("无法延后清理 P2P 错误连接");
    sdk_callback_leave(&s_call_callback_guard);
}

static void _call_on_disconnected(tirtc_conn_t hconn) {
    sdk_callback_enter(&s_call_callback_guard);
    LOG_I("on_disconnected hconn=%p", (void*)hconn);
    if (sdk_defer_action(&s_call_callback_guard, _deferred_finish_call,
                         hconn) != 0)
        LOG_E("无法延后清理断开的 P2P 连接");
    sdk_callback_leave(&s_call_callback_guard);
}

static void _call_on_audio(tirtc_conn_t hconn, const TIRTCFRAMEINFO *pFi, void *data) {
    sdk_callback_enter(&s_call_callback_guard);
    pthread_mutex_lock(&s_conn_mtx);
    int matched = s_active_conn == hconn;
    pthread_mutex_unlock(&s_conn_mtx);
    (void)data;
    if (matched) media_rx_log_audio(&s_rx_log, "设备通话", pFi);
    sdk_callback_leave(&s_call_callback_guard);
}

static void _call_on_video(tirtc_conn_t hconn, const TIRTCFRAMEINFO *pFi, void *data) {
    sdk_callback_enter(&s_call_callback_guard);
    pthread_mutex_lock(&s_conn_mtx);
    int matched = s_active_conn == hconn;
    pthread_mutex_unlock(&s_conn_mtx);
    (void)data;
    if (matched) media_rx_log_video(&s_rx_log, "设备通话", pFi);
    sdk_callback_leave(&s_call_callback_guard);
}

static void _call_on_message(tirtc_conn_t hconn, const TIRTCFRAMEINFO *pFi, void *data) {
    sdk_callback_enter(&s_call_callback_guard);
    (void)hconn; (void)pFi; (void)data;
    sdk_callback_leave(&s_call_callback_guard);
}

static void _call_on_command(tirtc_conn_t hconn, uint32_t cmdw,
                              const void *data, uint32_t len) {
    sdk_callback_enter(&s_call_callback_guard);
    if (cmdw == 0x2000 && data && len > 0) {
        /* Parse JSON to extract room_id */
        char *json_str = (char *)malloc(len + 1);
        if (!json_str) {
            sdk_callback_leave(&s_call_callback_guard);
            return;
        }
        memcpy(json_str, data, len);
        json_str[len] = '\0';

        cJSON *root = cJSON_Parse(json_str);
        free(json_str);
        if (!root) {
            sdk_callback_leave(&s_call_callback_guard);
            return;
        }

        cJSON *rid = cJSON_GetObjectItem(root, "room_id");
        const char *received_room = (rid && cJSON_IsString(rid)) ? rid->valuestring : "";
        LOG_I("收到 0x2000 room_id=%s", received_room);

        pthread_mutex_lock(&s_conn_mtx);
        char expected[128];
        STR_COPY(expected, s_expected_room_id);
        expected[sizeof(expected) - 1] = '\0';
        pthread_mutex_unlock(&s_conn_mtx);

        if (expected[0] && strcmp(received_room, expected) != 0) {
            LOG_W("0x2000 room_id 不匹配（期望 %s，收到 %s），断开", expected, received_room);
            cJSON_Delete(root);
            if (sdk_defer_action(&s_call_callback_guard,
                                 _deferred_disconnect_call, hconn) != 0)
                LOG_E("无法延后断开 room_id 不匹配的连接");
            sdk_callback_leave(&s_call_callback_guard);
            return;
        }

        char accepted_room[128];
        STR_COPY(accepted_room, received_room);
        cJSON_Delete(root);
        pthread_mutex_lock(&s_conn_mtx);
        if (s_active_conn == hconn) {
            STR_COPY(s_pending_connected_room, accepted_room);
            s_media_start_pending = 1;
        }
        pthread_mutex_unlock(&s_conn_mtx);
        if (sdk_defer_action(&s_call_callback_guard,
                             _deferred_start_call_media, hconn) != 0)
            LOG_E("无法延后启动设备通话媒体");
    }
    sdk_callback_leave(&s_call_callback_guard);
}

static void _call_on_request_key_frame(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_call_callback_guard);
    pthread_mutex_lock(&s_conn_mtx);
    if (s_active_conn == hconn && stream_id == STREAM_ID_VIDEO)
        s_force_key = 1;
    pthread_mutex_unlock(&s_conn_mtx);
    sdk_callback_leave(&s_call_callback_guard);
}

static int _call_on_subscribe_video(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_call_callback_guard);
    pthread_mutex_lock(&s_conn_mtx);
    int accepted =
        s_active_conn == hconn &&
        stream_id == STREAM_ID_VIDEO &&
        media_subscription_policy_subscribe_video(&s_media_policy);
    if (accepted) s_force_key = 1;
    pthread_mutex_unlock(&s_conn_mtx);
    LOG_I("设备通话视频订阅 stream=%u %s",
          stream_id, accepted ? "已接受" : "已拒绝");
    sdk_callback_leave(&s_call_callback_guard);
    return accepted ? 0 : -1;
}

static void _call_on_unsubscribe_video(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_call_callback_guard);
    pthread_mutex_lock(&s_conn_mtx);
    int matched = s_active_conn == hconn && stream_id == STREAM_ID_VIDEO;
    if (matched)
        media_subscription_policy_unsubscribe_video(&s_media_policy);
    pthread_mutex_unlock(&s_conn_mtx);
    if (matched)
        LOG_I("对端已退订设备通话视频 stream=%u；音频继续发送", stream_id);
    sdk_callback_leave(&s_call_callback_guard);
}

static int _call_on_subscribe_audio(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_call_callback_guard);
    (void)hconn; (void)stream_id;
    sdk_callback_leave(&s_call_callback_guard);
    return 0;
}

static void _call_on_unsubscribe_audio(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_call_callback_guard);
    (void)hconn; (void)stream_id;
    sdk_callback_leave(&s_call_callback_guard);
}

/* ── SDK lifecycle ───────────────────────────────────────────────────────── */

int call_init_sdk(const char *device_id, const char *secret_key, const char *client_id, const char *endpoint) {
    if (s_sdk_running) {
        LOG_W("SDK 已运行，跳过重复初始化");
        return 0;
    }

    s_sdk_started = 0;
    s_sdk_stopped = 0;

    /* Set send buffer (must be before TiRtcInit) */
    uint32_t buf = 1024 * 1024;
    int rc = TiRtcSetOption(TIRTC_OPT_MAX_SEND_BUFFER, &buf, sizeof(buf));
    if (rc != 0) {
        LOG_E("TiRtcSetOption(MAX_SEND_BUFFER) failed: rc=%d (%s)", rc, TiRtcGetErrorStr(rc));
        return -1;
    }

    rc = TiRtcInit();
    if (rc != 0) {
        LOG_E("TiRtcInit failed: rc=%d (%s)", rc, TiRtcGetErrorStr(rc));
        return -1;
    }

    TiRtcLogConfig(0, NULL, 0);
    TiRtcLogSetLevel(3);
    if (g_log_level <= LOG_DEBUG) {
        s_log_cb = _call_sdk_log_cb;
        TiRtcLogSetCallback(s_log_cb);
        TiRtcLogSetLevel(8);
    }

    if (endpoint && endpoint[0]) {
        rc = TiRtcSetOption(TIRTC_OPT_SERVICE_ENDPOINT, endpoint, (uint32_t)strlen(endpoint));
        if (rc != 0) {
            LOG_E("TiRtcSetOption(ENDPOINT) failed: rc=%d", rc);
            TiRtcUninit();
            return -1;
        }
    }
    rc = TiRtcSetOption(TIRTC_OPT_DEVICE_SECRET_KEY, secret_key, (uint32_t)strlen(secret_key));
    if (rc != 0) { LOG_E("TiRtcSetOption(SECRET_KEY) failed: rc=%d", rc); TiRtcUninit(); return -1; }
    rc = TiRtcSetOption(TIRTC_OPT_CLIENT_ID, client_id, (uint32_t)strlen(client_id));
    if (rc != 0) { LOG_E("TiRtcSetOption(CLIENT_ID) failed: rc=%d", rc); TiRtcUninit(); return -1; }

    /* Build callbacks (static — must outlive call_init_sdk per SDK docs) */
    static TIRTCCALLBACKS cbs;
    memset(&cbs, 0, sizeof(cbs));
    cbs.on_event             = _call_on_event;
    cbs.on_conn_accepted     = _call_on_conn_accepted;
    cbs.on_conn_error        = _call_on_conn_error;
    cbs.on_disconnected      = _call_on_disconnected;
    cbs.on_audio             = _call_on_audio;
    cbs.on_video             = _call_on_video;
    cbs.on_message           = _call_on_message;
    cbs.on_command           = _call_on_command;
    cbs.on_request_key_frame = _call_on_request_key_frame;
    cbs.on_subscribe_video   = _call_on_subscribe_video;
    cbs.on_unsubscribe_video = _call_on_unsubscribe_video;
    cbs.on_subscribe_audio   = _call_on_subscribe_audio;
    cbs.on_unsubscribe_audio = _call_on_unsubscribe_audio;

    rc = TiRtcStart(device_id, &cbs);
    if (rc != 0) {
        LOG_E("TiRtcStart failed: rc=%d (%s)", rc, TiRtcGetErrorStr(rc));
        TiRtcUninit();
        return -1;
    }

    s_sdk_running = 1;
    LOG_I("TiRTC SDK 启动中，等待 SYS_STARTED…");

    /* Wait for SYS_STARTED (10s timeout) */
    struct timespec deadline;
    clock_gettime(CLOCK_REALTIME, &deadline);
    deadline.tv_sec += 10;

    pthread_mutex_lock(&s_state_mtx);
    int timed_out = 0;
    while (!s_sdk_started && !timed_out && !g_stop) {
        if (pthread_cond_timedwait(&s_started_cond, &s_state_mtx, &deadline) == ETIMEDOUT) {
            timed_out = 1;
        }
    }
    pthread_mutex_unlock(&s_state_mtx);

    if (timed_out || !s_sdk_started) {
        LOG_E("TiRTC SDK 启动超时");
        s_sdk_running = 0;
        sdk_callback_wait_all(&s_call_callback_guard);
        TiRtcStop();
        clock_gettime(CLOCK_REALTIME, &deadline);
        deadline.tv_sec += 8;
        pthread_mutex_lock(&s_state_mtx);
        while (!s_sdk_stopped) {
            if (pthread_cond_timedwait(&s_stopped_cond, &s_state_mtx,
                                       &deadline) == ETIMEDOUT)
                break;
        }
        pthread_mutex_unlock(&s_state_mtx);
        sdk_callback_wait_all(&s_call_callback_guard);
        TiRtcUninit();
        return -1;
    }

    LOG_I("TiRTC SDK 已就绪，常驻监听入站连接");
    return 0;
}

void call_uninit_sdk(void) {
    if (!s_sdk_running) return;
    s_sdk_running = 0;

    sdk_callback_wait_all(&s_call_callback_guard);
    TiRtcStop();

    struct timespec deadline;
    clock_gettime(CLOCK_REALTIME, &deadline);
    deadline.tv_sec += 8;

    pthread_mutex_lock(&s_state_mtx);
    int timed_out = 0;
    while (!s_sdk_stopped && !timed_out) {
        if (pthread_cond_timedwait(&s_stopped_cond, &s_state_mtx, &deadline) == ETIMEDOUT)
            timed_out = 1;
    }
    pthread_mutex_unlock(&s_state_mtx);

    sdk_callback_wait_all(&s_call_callback_guard);
    TiRtcUninit();
    LOG_I("TiRTC SDK 已停止");
}

/* ── Media stream ────────────────────────────────────────────────────────── */

static int _is_audio_call(void) {
    int audio_call = 0;
    if (s_call_state) {
        pthread_mutex_lock(&s_call_state->lock);
        audio_call =
            strcmp(s_call_state->active_call_type, "audio") == 0;
        pthread_mutex_unlock(&s_call_state->lock);
    }
    return audio_call;
}

static void _prepare_media_policy(void) {
    int video_capable = s_send_video_path[0] != '\0' && !_is_audio_call();
    pthread_mutex_lock(&s_conn_mtx);
    media_subscription_policy_prepare(&s_media_policy, video_capable);
    s_force_key = video_capable;
    pthread_mutex_unlock(&s_conn_mtx);
}

static void _apply_video_downlink_policy(tirtc_conn_t hconn) {
    if (!hconn || !_is_audio_call()) return;
    int rc = TiRtcUnsubscribeVideo(hconn, STREAM_ID_VIDEO);
    if (rc == 0)
        LOG_I("纯音频设备通话已退订下行视频 stream=%u",
              STREAM_ID_VIDEO);
    else
        LOG_W("退订下行视频失败 stream=%u rc=%d (%s)",
              STREAM_ID_VIDEO, rc, TiRtcGetErrorStr(rc));
}

static void _start_media_stream(void) {
    pthread_mutex_lock(&s_conn_mtx);
    int already_running = s_media_running;
    pthread_mutex_unlock(&s_conn_mtx);
    if (already_running) return;
    if (!s_send_audio_path[0] || !s_send_audio_format) {
        LOG_W("未配置上行音频文件，跳过媒体流");
        return;
    }
    int with_video = s_send_video_path[0] != '\0' && !_is_audio_call();
    pthread_mutex_lock(&s_conn_mtx);
    if (!s_media_policy.initialized)
        media_subscription_policy_prepare(&s_media_policy, with_video);
    pthread_mutex_unlock(&s_conn_mtx);
    const char *video_path = with_video ? s_send_video_path : "";
    if (file_media_source_open(&s_media_src, s_send_audio_path,
                               s_send_audio_format, video_path,
                               s_send_video_format, AUDIO_PKT_MS) != 0) {
        LOG_E("无法打开发送媒体文件: video=%s audio=%s",
              s_send_video_path, s_send_audio_path);
        return;
    }

    pthread_mutex_lock(&s_conn_mtx);
    s_media_running = 1;
    s_force_key = 1;
    pthread_mutex_unlock(&s_conn_mtx);
    if (pthread_create(&s_media_thread, NULL, _media_worker, NULL) != 0) {
        pthread_mutex_lock(&s_conn_mtx);
        s_media_running = 0;
        pthread_mutex_unlock(&s_conn_mtx);
        file_media_source_close(&s_media_src);
        LOG_E("无法创建设备互呼媒体线程");
        return;
    }
    pthread_mutex_lock(&s_conn_mtx);
    s_media_thread_created = 1;
    pthread_mutex_unlock(&s_conn_mtx);
    LOG_I("上行文件媒体已启动；下行音视频记录日志后丢弃");
}

static void _stop_media_stream(void) {
    pthread_t thread;
    int join_thread = 0;
    pthread_mutex_lock(&s_conn_mtx);
    s_media_running = 0;
    if (s_media_thread_created && !pthread_equal(pthread_self(), s_media_thread)) {
        thread = s_media_thread;
        s_media_thread_created = 0;
        join_thread = 1;
    }
    pthread_mutex_unlock(&s_conn_mtx);
    if (join_thread) {
        pthread_join(thread, NULL);
        file_media_source_close(&s_media_src);
        LOG_I("媒体流已停止");
    }
    pthread_mutex_lock(&s_conn_mtx);
    media_subscription_policy_reset(&s_media_policy);
    pthread_mutex_unlock(&s_conn_mtx);
}

static void *_media_worker(void *arg) {
    (void)arg;
    double audio_pts_ms  = 0.0;
    double video_pts_ms  = 0.0;
    int     first_video   = 1;
    int64_t wall_start_ms = now_ms();
    int     consec_fail   = 0;
    int has_video = file_media_source_has_video(&s_media_src);
    int video_was_enabled = 0;

    while (!g_stop) {
        pthread_mutex_lock(&s_conn_mtx);
        int running = s_media_running;
        tirtc_conn_t conn = s_active_conn;
        int video_enabled =
            has_video &&
            media_subscription_policy_video_enabled(&s_media_policy);
        pthread_mutex_unlock(&s_conn_mtx);
        if (!running || !conn) break;
        if (video_enabled && !video_was_enabled) {
            video_pts_ms = audio_pts_ms;
            first_video = 1;
        }
        video_was_enabled = video_enabled;
        double target_pts = video_enabled && video_pts_ms < audio_pts_ms
                                ? video_pts_ms : audio_pts_ms;
        int64_t elapsed    = now_ms() - wall_start_ms;
        int64_t wait_ms    = (int64_t)target_pts - elapsed;
        if (wait_ms > 2) {
            sleep_ms((int)(wait_ms > 50 ? 50 : wait_ms));
            continue;
        }

        int rc;
        int send_audio = !video_enabled || audio_pts_ms <= video_pts_ms;
        if (send_audio) {
            const unsigned char *payload;
            size_t length;
            double duration_ms;
            if (!file_media_source_next_audio(&s_media_src, &payload, &length,
                                              &duration_ms))
                break;
            TIRTCFRAMEINFO fi;
            memset(&fi, 0, sizeof(fi));
            fi.stream_id = STREAM_ID_AUDIO;
            fi.media = s_send_audio_format->media;
            fi.flags = s_send_audio_format->flags;
            fi.ts = (uint32_t)audio_pts_ms;
            fi.length = (uint32_t)length;
            rc = TiRtcSendAudioStream(conn, &fi, payload);
            audio_pts_ms += duration_ms;
        } else {
            const unsigned char *payload;
            size_t length;
            int is_key = 0;
            pthread_mutex_lock(&s_conn_mtx);
            int force_key = s_force_key;
            s_force_key = 0;
            pthread_mutex_unlock(&s_conn_mtx);
            if (!file_media_source_next_video(&s_media_src, &payload, &length,
                                              &is_key, first_video || force_key))
                break;
            TIRTCFRAMEINFO fi;
            memset(&fi, 0, sizeof(fi));
            fi.stream_id = STREAM_ID_VIDEO;
            fi.media = s_send_video_format->media;
            fi.flags     = is_key ? TIRTC_FRAME_FLAG_KEY_FRAME : 0;
            fi.ts = (uint32_t)video_pts_ms;
            fi.length = (uint32_t)length;
            rc = TiRtcSendVideoStream(conn, &fi, payload);
            video_pts_ms += 1000.0 / VIDEO_FPS;
        }
        if (rc < 0) {
            if (rc == TIRTC_E_INVALID_HANDLE ||
                rc == TIRTC_E_CONN_TIMEOUTCLOSE ||
                rc == TIRTC_E_CONN_REMOTECLOSE ||
                rc == TIRTC_E_CONN_OTHER_ERROR) {
                LOG_W("媒体发送失败，连接已断开 rc=%d", rc);
                break;
            }
            if (++consec_fail >= 3) {
                LOG_E("连续 3 次发送失败，断开");
                TiRtcDisconnect(conn);
                break;
            }
            if (!send_audio && rc == TIRTC_E_BUSY) {
                pthread_mutex_lock(&s_conn_mtx);
                s_force_key = 1;
                pthread_mutex_unlock(&s_conn_mtx);
            }
        } else {
            first_video = 0;
            consec_fail = 0;
        }
    }

    LOG_I("媒体推流线程退出");
    return NULL;
}

/* ── TiRtcConnect callback (synchronous wait) ────────────────────────────── */

typedef struct {
    uint64_t generation;
} CallConnectContext;

static void _connect_cb(int error, tirtc_conn_t hconn, void *user_data) {
    sdk_callback_enter(&s_call_callback_guard);
    CallConnectContext *context = user_data;
    uint64_t generation = context->generation;
    free(context);
    pthread_mutex_lock(&s_connect_mtx);
    int current = generation == s_connect_generation;
    if (current) {
        s_connect_error = error;
        s_connect_hconn = hconn;
        s_connect_done  = 1;
        pthread_cond_signal(&s_connect_cond);
    }
    pthread_mutex_unlock(&s_connect_mtx);
    if (!current && error == 0 && hconn &&
        sdk_defer_disconnect(&s_call_callback_guard, hconn) != 0)
        LOG_E("无法延后断开过期 P2P 连接");
    sdk_callback_leave(&s_call_callback_guard);
}

/* ── P2P connect (callee side) ──────────────────────────────────────────── */

int call_connect_to(const char *remote_device_id, const char *token,
                     const char *room_id, int max_retries, int timeout_s) {
    _prepare_media_policy();
    pthread_mutex_lock(&s_conn_mtx);
    if (s_session_state != SESS_IDLE && s_session_state != SESS_CONNECTING) {
        LOG_E("call_connect_to: 当前状态 %s，不能发起新连接",
              sess_state_str(s_session_state));
        pthread_mutex_unlock(&s_conn_mtx);
        return -1;
    }
    s_session_state = SESS_CONNECTING;
    pthread_mutex_unlock(&s_conn_mtx);

    for (int attempt = 1; attempt <= max_retries; attempt++) {
        LOG_I("call_connect_to 尝试 %d/%d remote=%s", attempt, max_retries, remote_device_id);

        pthread_mutex_lock(&s_connect_mtx);
        uint64_t generation = ++s_connect_generation;
        s_connect_done  = 0;
        s_connect_error = 0;
        s_connect_hconn = NULL;
        pthread_mutex_unlock(&s_connect_mtx);
        CallConnectContext *context = calloc(1, sizeof(*context));
        if (!context) break;
        context->generation = generation;

        /* token is one-shot (SDK docs: "token 是一次性的").
         * First attempt uses the fresh token; retries pass NULL to
         * reuse the connection params cached by the SDK on success. */
        const char *tk = (attempt == 1) ? token : NULL;
        int rc = TiRtcConnect(remote_device_id, tk, _connect_cb, context);
        if (rc == TIRTC_E_CACHE_EXPIRED) {
            free(context);
            LOG_E("TiRtcConnect 缓存已过期，停止重试");
            break;
        }
        if (rc != 0) {
            free(context);
            LOG_E("TiRtcConnect 调用失败: rc=%d (%s)", rc, TiRtcGetErrorStr(rc));
            continue;
        }

        /* Wait for callback (sync) */
        struct timespec deadline;
        clock_gettime(CLOCK_REALTIME, &deadline);
        deadline.tv_sec += timeout_s;

        pthread_mutex_lock(&s_connect_mtx);
        int timed_out = 0;
        while (!s_connect_done && !timed_out && !g_stop &&
               s_connect_generation == generation) {
            if (pthread_cond_timedwait(&s_connect_cond, &s_connect_mtx, &deadline) == ETIMEDOUT)
                timed_out = 1;
        }
        int cancelled = s_connect_generation != generation;
        int error   = s_connect_error;
        int done    = s_connect_done;
        tirtc_conn_t hconn = s_connect_hconn;
        if (timed_out || !done) {
            if (s_connect_generation == generation)
                s_connect_generation++;
        }
        pthread_mutex_unlock(&s_connect_mtx);

        if (cancelled) {
            LOG_W("call_connect_to 已被取消 room_id=%s", room_id);
            pthread_mutex_lock(&s_conn_mtx);
            s_session_state = SESS_IDLE;
            s_active_conn = NULL;
            pthread_mutex_unlock(&s_conn_mtx);
            return -1;
        }
        if (timed_out || !done) {
            LOG_E("call_connect_to 超时 (%ds)，尝试 %d/%d", timeout_s, attempt, max_retries);
            continue;
        }

        if (error != 0) {
            LOG_E("call_connect_to 回调失败: rc=%d (%s)", error, TiRtcGetErrorStr(error));
            continue;
        }

        /* Success */
        pthread_mutex_lock(&s_conn_mtx);
        s_active_conn = hconn;
        s_session_state = SESS_IN_CALL;
        media_rx_log_reset(&s_rx_log);
        pthread_mutex_unlock(&s_conn_mtx);

        LOG_I("P2P 连接成功 hconn=%p，发送 0x2000 room_id=%s", (void*)hconn, room_id);
        _apply_video_downlink_policy(hconn);

        /* Build 0x2000 payload */
        cJSON *cmd_root = cJSON_CreateObject();
        char *cmd_str = NULL;
        if (cmd_root && cJSON_AddStringToObject(cmd_root, "room_id", room_id))
            cmd_str = cJSON_PrintUnformatted(cmd_root);
        cJSON_Delete(cmd_root);
        if (cmd_str) {
            TiRtcSendCommand(hconn, 0x2000, cmd_str, (uint32_t)strlen(cmd_str));
            free(cmd_str);
        } else {
            LOG_W("建立 P2P 后的 room_id 命令构造失败");
        }

        /* Start media stream (callee side) */
        pthread_mutex_lock(&s_call_control_mtx);
        pthread_mutex_lock(&s_conn_mtx);
        int still_active = s_active_conn == hconn &&
                           s_session_state == SESS_IN_CALL;
        pthread_mutex_unlock(&s_conn_mtx);
        if (still_active) {
            _start_media_stream();
            if (s_on_p2p_connected)
                s_on_p2p_connected(room_id, s_p2p_connected_user);
        }
        pthread_mutex_unlock(&s_call_control_mtx);
        if (!still_active) {
            TiRtcDisconnect(hconn);
            return -1;
        }

        return 0;
    }

    /* All retries failed */
    LOG_E("call_connect_to 全部 %d 次失败", max_retries);
    pthread_mutex_lock(&s_conn_mtx);
    s_session_state = SESS_IDLE;
    s_active_conn = NULL;
    pthread_mutex_unlock(&s_conn_mtx);

    if (s_on_connect_failed)
        s_on_connect_failed(s_connect_failed_user);

    return -1;
}

void call_hangup(void) {
    pthread_mutex_lock(&s_call_control_mtx);
    _stop_media_stream();

    pthread_mutex_lock(&s_conn_mtx);
    tirtc_conn_t conn = s_active_conn;
    s_active_conn = NULL;
    s_session_state = conn ? SESS_DISCONNECTING : SESS_IDLE;
    s_media_start_pending = 0;
    s_expected_room_id[0] = '\0';
    pthread_mutex_unlock(&s_conn_mtx);
    pthread_mutex_lock(&s_connect_mtx);
    s_connect_generation++;
    pthread_cond_broadcast(&s_connect_cond);
    pthread_mutex_unlock(&s_connect_mtx);

    if (conn) {
        TiRtcDisconnect(conn);
    }
    pthread_mutex_lock(&s_conn_mtx);
    s_session_state = SESS_IDLE;
    pthread_mutex_unlock(&s_conn_mtx);
    pthread_mutex_unlock(&s_call_control_mtx);
    LOG_I("hangup 完成");
}

int call_is_active(void) {
    pthread_mutex_lock(&s_conn_mtx);
    int active = s_session_state == SESS_IN_CALL ||
                 s_session_state == SESS_CONNECTING ||
                 s_session_state == SESS_DISCONNECTING;
    pthread_mutex_unlock(&s_conn_mtx);
    return active;
}

const char *call_get_state_str(void) {
    pthread_mutex_lock(&s_conn_mtx);
    const char *state = sess_state_str(s_session_state);
    pthread_mutex_unlock(&s_conn_mtx);
    return state;
}

void call_set_expected_room(CallState *cs, const char *room_id) {
    (void)cs;
    _prepare_media_policy();
    pthread_mutex_lock(&s_conn_mtx);
    STR_COPY(s_expected_room_id, room_id);
    s_expected_room_id[sizeof(s_expected_room_id) - 1] = '\0';
    pthread_mutex_unlock(&s_conn_mtx);
}

void call_clear_expected_room(CallState *cs) {
    (void)cs;
    pthread_mutex_lock(&s_conn_mtx);
    s_expected_room_id[0] = '\0';
    pthread_mutex_unlock(&s_conn_mtx);
}

int call_configure_media_ex(const char *send_audio, const char *audio_format,
                            const char *send_video,
                            const char *video_format) {
    const AudioFormat *audio = audio_format_find(audio_format);
    const VideoFormat *video = video_format_find(video_format);
    if (!audio || ((send_video && send_video[0]) && !video)) return -1;
    STR_COPY(s_send_audio_path, send_audio ? send_audio : "");
    STR_COPY(s_send_video_path, send_video ? send_video : "");
    s_send_audio_format = audio;
    s_send_video_format = video;
    return 0;
}

void call_configure_media(const char *device_id, const char *send_audio,
                          const char *send_video, const char *recv_dir) {
    (void)device_id;
    (void)recv_dir;
    (void)call_configure_media_ex(send_audio, "alaw_8khz",
                                  send_video, "h264");
}

void call_register_p2p_connected_cb(call_p2p_connected_cb cb, void *user) {
    s_on_p2p_connected = cb;
    s_p2p_connected_user = user;
}

void call_register_connect_failed_cb(call_connect_failed_cb cb, void *user) {
    s_on_connect_failed = cb;
    s_connect_failed_user = user;
}

void call_set_runtime_callbacks(CallState *cs,
                                int (*before_start)(void *user),
                                void (*on_session_end)(void *user),
                                void *user) {
    if (!cs) return;
    pthread_mutex_lock(&cs->lock);
    cs->before_start = before_start;
    cs->before_start_ex = NULL;
    cs->on_session_end = on_session_end;
    cs->runtime_user = user;
    pthread_mutex_unlock(&cs->lock);
}

void call_set_runtime_callbacks_ex(
    CallState *cs,
    int (*before_start)(void *user, int consume_pending),
    void (*on_session_end)(void *user),
    void *user) {
    if (!cs) return;
    pthread_mutex_lock(&cs->lock);
    cs->before_start = NULL;
    cs->before_start_ex = before_start;
    cs->on_session_end = on_session_end;
    cs->runtime_user = user;
    pthread_mutex_unlock(&cs->lock);
}

void call_set_runtime_action_callback(
    CallState *cs,
    int (*run_action)(void *user, const char *session_id,
                      int (*action)(void *action_user),
                      void *action_user)) {
    if (!cs) return;
    pthread_mutex_lock(&cs->lock);
    cs->run_action = run_action;
    pthread_mutex_unlock(&cs->lock);
}

/* ── CallState lifecycle ─────────────────────────────────────────────────── */

CallState *call_create_ex(const char *call_server, const char *device_id,
                          const char *mqtt_token,
                          const char *send_audio, const char *audio_format,
                          const char *send_video,
                          const char *video_format) {
    CallState *cs = (CallState *)calloc(1, sizeof(CallState));
    if (!cs) return NULL;

    STR_COPY(cs->call_server, call_server); STR_COPY(cs->device_id, device_id);
    STR_COPY(cs->mqtt_token, mqtt_token);
    if (send_audio) STR_COPY(cs->send_audio, send_audio);
    if (send_video) STR_COPY(cs->send_video, send_video);

    pthread_mutex_init(&cs->lock, NULL);
    pthread_mutex_init(&cs->reject_lock, NULL);
    pthread_cond_init(&cs->reject_ready, NULL);
    pthread_cond_init(&cs->reject_idle, NULL);
    cs->ring_timer_running = 0;

    if (send_audio && send_audio[0]) {
        if (call_configure_media_ex(send_audio, audio_format,
                                    send_video, video_format) != 0) {
            pthread_cond_destroy(&cs->reject_idle);
            pthread_cond_destroy(&cs->reject_ready);
            pthread_mutex_destroy(&cs->reject_lock);
            pthread_mutex_destroy(&cs->lock);
            free(cs);
            return NULL;
        }
    }
    s_call_state = cs;
    call_register_p2p_connected_cb(call_session_on_p2p_connected, cs);
    call_register_connect_failed_cb(call_session_on_connect_failed, cs);

    return cs;
}

CallState *call_create(const char *call_server, const char *device_id,
                       const char *mqtt_token,
                       const char *send_audio, const char *send_video,
                       const char *recv_dir) {
    CallState *cs = call_create_ex(call_server, device_id, mqtt_token,
                                   send_audio, "alaw_8khz",
                                   send_video, "h264");
    if (cs) STR_COPY(cs->recv_dir, recv_dir ? recv_dir : "");
    return cs;
}

void call_destroy(CallState *cs) {
    if (!cs) return;
    if (s_call_state == cs) s_call_state = NULL;
    call_session_shutdown_workers(cs);
    call_session_cancel_ring_timer(cs);
    if (cs->contact_list) cJSON_Delete(cs->contact_list);
    if (cs->contact_device_ids) {
        for (int i = 0; i < cs->contact_count; i++) {
            free(cs->contact_device_ids[i]);
        }
        free(cs->contact_device_ids);
    }
    pthread_cond_destroy(&cs->reject_idle);
    pthread_cond_destroy(&cs->reject_ready);
    pthread_mutex_destroy(&cs->reject_lock);
    pthread_mutex_destroy(&cs->lock);
    free(cs);
}

/* ── MQTT message handlers ───────────────────────────────────────────────── */

void call_on_device_call_incoming(void *ctx, const cJSON *payload) {
    CallState *cs = (CallState *)ctx;
    if (!cs || !payload) return;

    cJSON *room_id     = cJSON_GetObjectItem(payload, "room_id");
    cJSON *caller_id   = cJSON_GetObjectItem(payload, "caller_id");
    cJSON *caller_name = cJSON_GetObjectItem(payload, "caller_name");
    cJSON *call_type   = cJSON_GetObjectItem(payload, "call_type");

    const char *rid = room_id   && cJSON_IsString(room_id)   ? room_id->valuestring   : "";
    const char *cid = caller_id && cJSON_IsString(caller_id) ? caller_id->valuestring : "";
    const char *cname = caller_name && cJSON_IsString(caller_name) ? caller_name->valuestring : cid;
    const char *ct   = call_type && cJSON_IsString(call_type) ? call_type->valuestring : "video";
    if (!rid[0] || !cid[0]) {
        LOG_W("忽略缺少 room_id/caller_id 的设备来电");
        return;
    }

    pthread_mutex_lock(&cs->lock);
    cs->incoming_generation++;
    cs->pending_call = 1;
    cs->pending_generation = cs->incoming_generation;
    cs->pending_deadline_ms = now_ms() + 45000;
    STR_COPY(cs->pending_room_id, rid); STR_COPY(cs->pending_caller_id, cid);
    STR_COPY(cs->pending_caller_name, cname); STR_COPY(cs->pending_call_type, ct);
    if (cs->active) {
        pthread_mutex_unlock(&cs->lock);
        LOG_W("通话中有新来电！%s(%s) room=%s", cname, cid, rid);
        LOG_W("已暂存，hangup 挂断当前通话后可 accept 接听");
        return;
    }
    pthread_mutex_unlock(&cs->lock);

    LOG_W("来电！%s(%s) room=%s type=%s", cname, cid, rid, ct);
    LOG_W("输入 accept 接听，reject 拒接");
}

void call_on_device_room_cancel(void *ctx, const cJSON *payload) {
    CallState *cs = (CallState *)ctx;
    if (!cs || !payload) return;

    cJSON *room_id = cJSON_GetObjectItem(payload, "room_id");
    cJSON *reason  = cJSON_GetObjectItem(payload, "reason");
    const char *rid = room_id && cJSON_IsString(room_id) ? room_id->valuestring : "";
    const char *r   = reason  && cJSON_IsString(reason)  ? reason->valuestring  : "";

    pthread_mutex_lock(&cs->lock);
    if (cs->pending_call && strcmp(cs->pending_room_id, rid) == 0) {
        cs->pending_call = 0;
        cs->pending_deadline_ms = 0;
        cs->incoming_generation++;
        pthread_mutex_unlock(&cs->lock);
        LOG_W("来电已取消 room_id=%s reason=%s", rid, r);
        return;
    }
    int is_current = cs->active && strcmp(cs->room_id, rid) == 0;
    if (is_current) {
        cs->active = 0;
        cs->incoming_generation++;
        cs->room_id[0] = '\0';
        cs->role[0]    = '\0';
        cs->active_call_type[0] = '\0';
        cs->active_generation = 0;
    }
    pthread_mutex_unlock(&cs->lock);

    if (is_current) {
        LOG_W("通话已结束 room_id=%s reason=%s", rid, r);
        call_session_cancel_ring_timer(cs);
        if (call_is_active()) call_hangup();
        if (cs->on_session_end) cs->on_session_end(cs->runtime_user);
    }
}

void call_on_device_call_reject(void *ctx, const cJSON *payload) {
    CallState *cs = (CallState *)ctx;
    if (!cs || !payload) return;
    cJSON *room_id = cJSON_GetObjectItem(payload, "room_id");
    cJSON *reason  = cJSON_GetObjectItem(payload, "reason");
    const char *rid = room_id && cJSON_IsString(room_id) ? room_id->valuestring : "";
    const char *r   = reason  && cJSON_IsString(reason)  ? reason->valuestring  : "";
    LOG_W("对方拒接 room_id=%s reason=%s", rid, r);

    pthread_mutex_lock(&cs->lock);
    int is_current = cs->active && strcmp(cs->room_id, rid) == 0;
    if (is_current) {
        cs->active = 0;
        cs->room_id[0] = '\0';
        cs->role[0] = '\0';
        cs->active_call_type[0] = '\0';
    }
    pthread_mutex_unlock(&cs->lock);
    if (is_current) {
        call_session_cancel_ring_timer(cs);
        call_clear_expected_room(cs);
        if (cs->on_session_end) cs->on_session_end(cs->runtime_user);
    }
}

void call_on_device_callers_update(void *ctx) {
    (void)ctx;
    LOG_I("联系人数据已变更，可执行 contacts / pending 命令刷新");
}

void call_on_device_callers_update_ex(void *ctx, const cJSON *payload) {
    (void)ctx;
    cJSON *action_item = payload ? cJSON_GetObjectItem(payload, "action") : NULL;
    cJSON *type_item = payload ? cJSON_GetObjectItem(payload, "contact_type") : NULL;
    cJSON *peer_item = payload ? cJSON_GetObjectItem(payload, "peer_id") : NULL;
    const char *action = action_item && cJSON_IsString(action_item) ? action_item->valuestring : "";
    const char *contact_type = type_item && cJSON_IsString(type_item) ? type_item->valuestring : "";
    const char *peer = peer_item && cJSON_IsString(peer_item) ? peer_item->valuestring : "";
    if (strcmp(action, "request") == 0) {
        LOG_I("收到联系人申请 peer=%s，请执行 pending 查看并处理", peer);
    } else {
        LOG_I("联系人变更 action=%s type=%s peer=%s，可执行 contacts / pending 命令刷新",
              action[0] ? action : "update", contact_type, peer);
    }
}

void call_on_device_callee_answered(void *ctx, const cJSON *payload) {
    CallState *cs = (CallState *)ctx;
    if (!cs || !payload) return;

    cJSON *room_id   = cJSON_GetObjectItem(payload, "room_id");
    cJSON *callee_id = cJSON_GetObjectItem(payload, "callee_id");
    const char *rid = room_id   && cJSON_IsString(room_id)   ? room_id->valuestring   : "";
    const char *cid = callee_id && cJSON_IsString(callee_id) ? callee_id->valuestring : "";

    pthread_mutex_lock(&cs->lock);
    int match = cs->active && strcmp(cs->room_id, rid) == 0;
    pthread_mutex_unlock(&cs->lock);

    if (match) {
        LOG_I("对方正在连接中 callee=%s room=%s（等待 P2P 建连）", cid, rid);
    }
}

/* ── Command input loop ──────────────────────────────────────────────────── */

#include <sys/select.h>

void call_cmd_loop(CallState *cs) {
    const char *Y = C_YELLOW;
    const char *R = C_RESET;

    printf("%s[call] ╔══════════════════════════════════════════════════╗%s\n", Y, R);
    printf("%s[call]   终端命令就绪：%s\n", Y, R);
    printf("%s[call]     call                  — 列出联系人并选择拨打%s\n", Y, R);
    printf("%s[call]     call <N> [video|audio] — 按联系人下标呼叫%s\n", Y, R);
    printf("%s[call]     call <device_id> [video|audio] — 按设备 ID 呼叫%s\n", Y, R);
    printf("%s[call]     accept                — 接听来电%s\n", Y, R);
    printf("%s[call]     reject [busy|decline] — 拒接来电%s\n", Y, R);
    printf("%s[call]     hangup                — 挂断通话%s\n", Y, R);
    printf("%s[call]     cancel                — 主叫取消%s\n", Y, R);
    printf("%s[call]     contacts              — 列出联系人（带下标）%s\n", Y, R);
    printf("%s[call]     addcontact <device_id> — 发起联系人申请%s\n", Y, R);
    printf("%s[call]     respond <N> accept|reject — 审批联系人申请（N 为 contacts 下标）%s\n", Y, R);
    printf("%s[call]     room                  — 查询当前所在房间%s\n", Y, R);
    printf("%s[call]     remark <N> <备注文本>   — 修改联系人备注（N 为 contacts 下标）%s\n", Y, R);
    printf("%s[call]     ct list|pending|add|accept|reject|del|remark — 联系人维护%s\n", Y, R);
    printf("%s[call]     exit                  — 退出程序%s\n", Y, R);
    printf("%s[call] ╚══════════════════════════════════════════════════╝%s\n", Y, R);
    fflush(stdout);

    char line[1024];
    while (!g_stop) {
        fd_set fds;
        FD_ZERO(&fds);
        FD_SET(STDIN_FILENO, &fds);
        struct timeval tv = {1, 0};  /* 1s poll to check g_stop */
        int ret = select(STDIN_FILENO + 1, &fds, NULL, NULL, &tv);
        if (ret < 0) {
            if (errno == EINTR) continue;
            break;
        }
        if (ret == 0) continue;  /* timeout */
        if (!FD_ISSET(STDIN_FILENO, &fds)) continue;

        if (!fgets(line, sizeof(line), stdin)) break;

        /* Strip trailing newline */
        size_t len = strlen(line);
        while (len > 0 && (line[len - 1] == '\n' || line[len - 1] == '\r'))
            line[--len] = '\0';
        if (len == 0) continue;

        /* Forward to session layer — handles all commands including exit */
        extern void call_session_dispatch(CallState *cs, const char *line);
        call_session_dispatch(cs, line);
    }
}
