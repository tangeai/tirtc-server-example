/** \file tirtc_stream.c
 * \brief TiRTC passive streaming with encoded Linux file media.
 */

#include "tirtc_stream.h"
#define LOG_MODULE "stream"
#include "common.h"

#include <limits.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

#include "device_adapter.h"
#include "file_media_source.h"
#include "media_rx_log.h"
#include "sdk_callback_guard.h"
#include "stream_connection_set.h"
#include "tirtc_runtime.h"
#include "tirtc/tiRTC.h"

extern volatile sig_atomic_t g_stop;

static int s_service_active;

static pthread_mutex_t s_conn_mtx = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t s_handoff_mtx = PTHREAD_MUTEX_INITIALIZER;
static pthread_t s_push_thread;
static int s_push_thread_created;
static int s_push_running;
static int s_force_key;
static StreamConnectionSet s_connections;

static DeviceMediaSource s_media;
static const AudioFormat *s_audio_format;
static const VideoFormat *s_video_format;
static char s_video_path[512];
static char s_audio_path[512];
static MediaRxLog s_rx_log = MEDIA_RX_LOG_INITIALIZER;
static SdkCallbackGuard s_callback_guard = SDK_CALLBACK_GUARD_INITIALIZER;

static void *_push_thread(void *arg);

static void _stop_push_thread(void) {
    pthread_t thread;
    int join_thread = 0;
    pthread_mutex_lock(&s_conn_mtx);
    s_push_running = 0;
    if (s_push_thread_created && !pthread_equal(pthread_self(), s_push_thread)) {
        thread = s_push_thread;
        s_push_thread_created = 0;
        join_thread = 1;
    }
    pthread_mutex_unlock(&s_conn_mtx);
    if (join_thread) pthread_join(thread, NULL);
}

static int _push_should_run(void) {
    pthread_mutex_lock(&s_conn_mtx);
    int running = s_push_running;
    pthread_mutex_unlock(&s_conn_mtx);
    return running;
}

static int _take_force_key(void) {
    pthread_mutex_lock(&s_conn_mtx);
    int force_key = s_force_key;
    s_force_key = 0;
    pthread_mutex_unlock(&s_conn_mtx);
    return force_key;
}

static int _ensure_push_thread(void) {
    pthread_t finished_thread;
    int join_finished = 0;
    pthread_mutex_lock(&s_conn_mtx);
    if (s_push_running) {
        pthread_mutex_unlock(&s_conn_mtx);
        return 1;
    }
    if (s_push_thread_created) {
        finished_thread = s_push_thread;
        s_push_thread_created = 0;
        join_finished = 1;
    }
    pthread_mutex_unlock(&s_conn_mtx);
    if (join_finished) pthread_join(finished_thread, NULL);

    DeviceMediaSourceConfig media_config = {
        .audio_locator = s_audio_path,
        .audio_format = s_audio_format ? s_audio_format->name : NULL,
        .video_locator = s_video_path,
        .video_format = s_video_format ? s_video_format->name : NULL,
        .audio_packet_ms = AUDIO_PKT_MS_VOIP,
        .business = DEVICE_BUSINESS_STREAM,
    };
    if (device_media_source_open(&s_media, &media_config) != 0) {
        LOG_E("无法打开实时流上行媒体源");
        return 0;
    }

    pthread_mutex_lock(&s_conn_mtx);
    if (!s_service_active) {
        pthread_mutex_unlock(&s_conn_mtx);
        device_media_source_close(&s_media);
        return 0;
    }
    s_push_running = 1;
    s_force_key = 1;
    if (pthread_create(&s_push_thread, NULL, _push_thread, NULL) != 0) {
        s_push_running = 0;
        pthread_mutex_unlock(&s_conn_mtx);
        device_media_source_close(&s_media);
        LOG_E("无法启动实时流媒体线程");
        return 0;
    }
    s_push_thread_created = 1;
    pthread_mutex_unlock(&s_conn_mtx);
    return 1;
}

static void _accept_connection(void *opaque) {
    tirtc_conn_t hconn = (tirtc_conn_t)opaque;
    pthread_mutex_lock(&s_handoff_mtx);
    pthread_mutex_lock(&s_conn_mtx);
    int announced = s_service_active &&
                    stream_connection_is_pending(&s_connections, hconn);
    pthread_mutex_unlock(&s_conn_mtx);
    if (!announced) {
        TiRtcDisconnect(hconn);
        pthread_mutex_unlock(&s_handoff_mtx);
        return;
    }

    if (!_ensure_push_thread()) {
        pthread_mutex_lock(&s_conn_mtx);
        (void)stream_connection_remove(&s_connections, hconn);
        pthread_mutex_unlock(&s_conn_mtx);
        TiRtcDisconnect(hconn);
        pthread_mutex_unlock(&s_handoff_mtx);
        return;
    }

    pthread_mutex_lock(&s_conn_mtx);
    if (!s_service_active ||
        !stream_connection_activate(&s_connections, hconn)) {
        pthread_mutex_unlock(&s_conn_mtx);
        TiRtcDisconnect(hconn);
        pthread_mutex_unlock(&s_handoff_mtx);
        return;
    }
    media_rx_log_reset(&s_rx_log);
    pthread_mutex_unlock(&s_conn_mtx);

    int audio_rc = TiRtcSubscribeAudio(hconn, STREAM_ID_DOWN_AUDIO);
    int video_rc = TiRtcSubscribeVideo(hconn, STREAM_ID_DOWN_VIDEO);

    pthread_mutex_lock(&s_conn_mtx);
    int current = s_service_active &&
                  stream_connection_is_active(&s_connections, hconn);
    if (current) {
        stream_connection_set_down_audio(&s_connections, hconn, audio_rc >= 0);
        stream_connection_set_down_video(&s_connections, hconn, video_rc >= 0);
    }
    size_t active_count = stream_connection_active_count(&s_connections);
    pthread_mutex_unlock(&s_conn_mtx);
    if (!current) {
        if (audio_rc >= 0)
            (void)TiRtcUnsubscribeAudio(hconn, STREAM_ID_DOWN_AUDIO);
        if (video_rc >= 0)
            (void)TiRtcUnsubscribeVideo(hconn, STREAM_ID_DOWN_VIDEO);
        LOG_E("实时流连接在订阅下行媒体时失效 audio_rc=%d video_rc=%d",
              audio_rc, video_rc);
        TiRtcDisconnect(hconn);
    } else {
        LOG_I("客户端已连接（%zu/%d），下行订阅 audio=%s video=%s；上行媒体等待该端订阅",
              active_count, STREAM_MAX_CONNECTIONS,
              audio_rc >= 0 ? "成功" : "失败",
              video_rc >= 0 ? "成功" : "失败");
    }
    pthread_mutex_unlock(&s_handoff_mtx);
}

static void _on_conn_accepted(tirtc_conn_t hconn) {
    sdk_callback_enter(&s_callback_guard);
    pthread_mutex_lock(&s_conn_mtx);
    int duplicate = stream_connection_is_pending(&s_connections, hconn) ||
                    stream_connection_is_active(&s_connections, hconn);
    int added = s_service_active && !duplicate &&
                stream_connection_add_pending(&s_connections, hconn);
    pthread_mutex_unlock(&s_conn_mtx);
    if (duplicate) {
        LOG_D("忽略重复的客户端连接回调 hconn=%p", (void *)hconn);
    } else if (!added ||
               sdk_defer_action(
                   &s_callback_guard, _accept_connection, hconn) != 0) {
        LOG_E("无法接收或延后处理客户端连接");
        pthread_mutex_lock(&s_conn_mtx);
        if (added) (void)stream_connection_remove(&s_connections, hconn);
        pthread_mutex_unlock(&s_conn_mtx);
        sdk_defer_disconnect(&s_callback_guard, hconn);
    }
    sdk_callback_leave(&s_callback_guard);
}

static void _on_conn_error(tirtc_conn_t hconn, int error) {
    sdk_callback_enter(&s_callback_guard);
    LOG_E("on_conn_error: %s", TiRtcGetErrorStr(error));
    pthread_mutex_lock(&s_conn_mtx);
    (void)stream_connection_remove(&s_connections, hconn);
    pthread_mutex_unlock(&s_conn_mtx);
    if (sdk_defer_disconnect(&s_callback_guard, hconn) != 0)
        LOG_E("无法延后断开错误连接");
    sdk_callback_leave(&s_callback_guard);
}

static void _on_disconnected(tirtc_conn_t hconn) {
    sdk_callback_enter(&s_callback_guard);
    LOG_D("客户端断开");
    pthread_mutex_lock(&s_conn_mtx);
    (void)stream_connection_remove(&s_connections, hconn);
    pthread_mutex_unlock(&s_conn_mtx);
    sdk_callback_leave(&s_callback_guard);
}

static void _on_audio(tirtc_conn_t hconn, const TIRTCFRAMEINFO *frame, void *data) {
    sdk_callback_enter(&s_callback_guard);
    pthread_mutex_lock(&s_conn_mtx);
    int accepted = frame && data &&
                   stream_connection_accepts_down_audio(&s_connections, hconn) &&
                   frame->stream_id == STREAM_ID_DOWN_AUDIO;
    pthread_mutex_unlock(&s_conn_mtx);
    if (accepted)
        (void)device_media_sink_submit(
            DEVICE_BUSINESS_STREAM, 0, frame->stream_id, frame->media,
            frame->flags, frame->ts, data, frame->length);
    MediaRxNotice notice;
    if (accepted && media_rx_log_note_audio(&s_rx_log, "实时流", frame, &notice))
        (void)sdk_defer_copy_action(
            &s_callback_guard, media_rx_log_emit, NULL,
            &notice, sizeof(notice));
    sdk_callback_leave(&s_callback_guard);
}

static void _on_video(tirtc_conn_t hconn, const TIRTCFRAMEINFO *frame, void *data) {
    sdk_callback_enter(&s_callback_guard);
    pthread_mutex_lock(&s_conn_mtx);
    int accepted = frame && data &&
                   stream_connection_accepts_down_video(&s_connections, hconn) &&
                   frame->stream_id == STREAM_ID_DOWN_VIDEO;
    pthread_mutex_unlock(&s_conn_mtx);
    if (accepted)
        (void)device_media_sink_submit(
            DEVICE_BUSINESS_STREAM, 1, frame->stream_id, frame->media,
            frame->flags, frame->ts, data, frame->length);
    MediaRxNotice notice;
    if (accepted && media_rx_log_note_video(&s_rx_log, "实时流", frame, &notice))
        (void)sdk_defer_copy_action(
            &s_callback_guard, media_rx_log_emit, NULL,
            &notice, sizeof(notice));
    sdk_callback_leave(&s_callback_guard);
}

static void _on_message(tirtc_conn_t hconn, const TIRTCFRAMEINFO *frame, void *data) {
    sdk_callback_enter(&s_callback_guard);
    (void)hconn;
    (void)frame;
    (void)data;
    sdk_callback_leave(&s_callback_guard);
}

static void _on_command(tirtc_conn_t hconn, uint32_t cmd, const void *data,
                        uint32_t length) {
    sdk_callback_enter(&s_callback_guard);
    (void)hconn;
    (void)cmd;
    (void)data;
    (void)length;
    sdk_callback_leave(&s_callback_guard);
}

static void _on_request_key_frame(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_callback_guard);
    LOG_D("请求关键帧 stream_id=%u", stream_id);
    pthread_mutex_lock(&s_conn_mtx);
    if (stream_id == STREAM_ID_VIDEO &&
        stream_connection_is_active(&s_connections, hconn))
        s_force_key = 1;
    pthread_mutex_unlock(&s_conn_mtx);
    sdk_callback_leave(&s_callback_guard);
}

static int _on_sub_video(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_callback_guard);
    LOG_D("订阅视频 stream_id=%u", stream_id);
    pthread_mutex_lock(&s_conn_mtx);
    int accepted = stream_id == STREAM_ID_VIDEO &&
                   stream_connection_subscribe_video(&s_connections, hconn);
    if (accepted) {
        /* H5 subscribes after connect completes and may have missed the IDR
         * sent when the connection was accepted. */
        s_force_key = 1;
    }
    pthread_mutex_unlock(&s_conn_mtx);
    sdk_callback_leave(&s_callback_guard);
    return accepted ? 0 : -1;
}

static int _on_sub_audio(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_callback_guard);
    LOG_D("订阅音频 stream_id=%u", stream_id);
    pthread_mutex_lock(&s_conn_mtx);
    int accepted = stream_id == STREAM_ID_AUDIO &&
                   stream_connection_subscribe_audio(&s_connections, hconn);
    pthread_mutex_unlock(&s_conn_mtx);
    sdk_callback_leave(&s_callback_guard);
    return accepted ? 0 : -1;
}

static void _on_unsubscribe_video(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_callback_guard);
    pthread_mutex_lock(&s_conn_mtx);
    if (stream_id == STREAM_ID_VIDEO)
        stream_connection_unsubscribe_video(&s_connections, hconn);
    pthread_mutex_unlock(&s_conn_mtx);
    sdk_callback_leave(&s_callback_guard);
}

static void _on_unsubscribe_audio(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_callback_guard);
    pthread_mutex_lock(&s_conn_mtx);
    if (stream_id == STREAM_ID_AUDIO)
        stream_connection_unsubscribe_audio(&s_connections, hconn);
    pthread_mutex_unlock(&s_conn_mtx);
    sdk_callback_leave(&s_callback_guard);
}

static void *_push_thread(void *arg) {
    (void)arg;
    int has_video = device_media_source_has_video(&s_media);
    double audio_pts_ms = 0.0;
    double video_pts_ms = 0.0;
    int64_t wall_start_ms = now_ms();
    int audio_was_enabled = 0;
    int video_was_enabled = 0;

    while (_push_should_run() && !g_stop) {
        tirtc_conn_t audio_targets[STREAM_MAX_CONNECTIONS];
        tirtc_conn_t video_targets[STREAM_MAX_CONNECTIONS];
        pthread_mutex_lock(&s_conn_mtx);
        size_t audio_count = stream_connection_snapshot_audio(
            &s_connections, audio_targets, STREAM_MAX_CONNECTIONS);
        size_t video_count = has_video ? stream_connection_snapshot_video(
            &s_connections, video_targets, STREAM_MAX_CONNECTIONS) : 0;
        pthread_mutex_unlock(&s_conn_mtx);
        int audio_enabled = audio_count > 0;
        int video_enabled = video_count > 0;
        if (!audio_enabled && !video_enabled) {
            sleep_ms(10);
            continue;
        }
        int64_t elapsed = now_ms() - wall_start_ms;
        if (audio_enabled && !audio_was_enabled && audio_pts_ms < elapsed)
            audio_pts_ms = (double)elapsed;
        if (video_enabled && !video_was_enabled && video_pts_ms < elapsed) {
            video_pts_ms = (double)elapsed;
            pthread_mutex_lock(&s_conn_mtx);
            s_force_key = 1;
            pthread_mutex_unlock(&s_conn_mtx);
        }
        audio_was_enabled = audio_enabled;
        video_was_enabled = video_enabled;
        double target_pts = audio_enabled && video_enabled
                                ? (video_pts_ms < audio_pts_ms
                                       ? video_pts_ms : audio_pts_ms)
                                : (audio_enabled ? audio_pts_ms : video_pts_ms);
        int64_t wait_ms = (int64_t)target_pts - elapsed;
        if (wait_ms > 2) {
            sleep_ms((int)(wait_ms > 50 ? 50 : wait_ms));
            continue;
        }

        int send_audio = audio_enabled &&
                         (!video_enabled || audio_pts_ms <= video_pts_ms);
        TIRTCFRAMEINFO frame;
        memset(&frame, 0, sizeof(frame));
        DeviceMediaPacket packet;
        if (send_audio) {
            if (device_media_source_next_audio(&s_media, &packet) <= 0)
                break;
            frame.stream_id = STREAM_ID_AUDIO;
            frame.media = s_audio_format->media;
            frame.flags = s_audio_format->flags;
            frame.ts = (uint32_t)audio_pts_ms;
            frame.length = (uint32_t)packet.length;
            audio_pts_ms += packet.duration_ms;
        } else {
            if (device_media_source_next_video(
                    &s_media, _take_force_key(), &packet) <= 0)
                break;
            frame.stream_id = STREAM_ID_VIDEO;
            frame.media = s_video_format->media;
            frame.flags = packet.key_frame ? TIRTC_FRAME_FLAG_KEY_FRAME : 0;
            frame.ts = (uint32_t)video_pts_ms;
            frame.length = (uint32_t)packet.length;
            video_pts_ms += 1000.0 / VIDEO_FPS;
        }

        tirtc_conn_t *targets = send_audio ? audio_targets : video_targets;
        size_t target_count = send_audio ? audio_count : video_count;
        for (size_t i = 0; i < target_count; ++i) {
            tirtc_conn_t hconn = targets[i];
            int rc = send_audio
                         ? TiRtcSendAudioStream(hconn, &frame, packet.data)
                         : TiRtcSendVideoStream(hconn, &frame, packet.data);
            int disconnect = 0;
            if (rc >= 0) {
                pthread_mutex_lock(&s_conn_mtx);
                (void)stream_connection_note_send_result(
                    &s_connections, hconn, 1);
                pthread_mutex_unlock(&s_conn_mtx);
                continue;
            }
            if (rc == TIRTC_E_CONN_TIMEOUTCLOSE ||
                rc == TIRTC_E_CONN_REMOTECLOSE ||
                rc == TIRTC_E_CONN_OTHER_ERROR) {
                disconnect = 1;
            } else if (rc == TIRTC_E_BUSY) {
                if (!send_audio) {
                    pthread_mutex_lock(&s_conn_mtx);
                    s_force_key = 1;
                    pthread_mutex_unlock(&s_conn_mtx);
                }
            } else if (rc != TIRTC_E_INVALID_HANDLE) {
                pthread_mutex_lock(&s_conn_mtx);
                unsigned int failures = stream_connection_note_send_result(
                    &s_connections, hconn, 0);
                pthread_mutex_unlock(&s_conn_mtx);
                LOG_E("发送%s失败 rc=%d: %s", send_audio ? "音频" : "视频",
                      rc, TiRtcGetErrorStr(rc));
                disconnect = failures >= 3;
            }
            if (disconnect) {
                LOG_W("单个实时流连接发送失败，断开该连接");
                pthread_mutex_lock(&s_conn_mtx);
                (void)stream_connection_remove(&s_connections, hconn);
                pthread_mutex_unlock(&s_conn_mtx);
                TiRtcDisconnect(hconn);
            }
        }
    }
    device_media_source_close(&s_media);
    pthread_mutex_lock(&s_conn_mtx);
    s_push_running = 0;
    pthread_mutex_unlock(&s_conn_mtx);
    LOG_I("推流线程退出");
    return NULL;
}

int stream_service_register(void) {
    TIRTCCALLBACKS callbacks;
    memset(&callbacks, 0, sizeof(callbacks));
    callbacks.on_conn_accepted = _on_conn_accepted;
    callbacks.on_conn_error = _on_conn_error;
    callbacks.on_disconnected = _on_disconnected;
    callbacks.on_audio = _on_audio;
    callbacks.on_video = _on_video;
    callbacks.on_message = _on_message;
    callbacks.on_command = _on_command;
    callbacks.on_request_key_frame = _on_request_key_frame;
    callbacks.on_subscribe_video = _on_sub_video;
    callbacks.on_unsubscribe_video = _on_unsubscribe_video;
    callbacks.on_subscribe_audio = _on_sub_audio;
    callbacks.on_unsubscribe_audio = _on_unsubscribe_audio;
    return tirtc_runtime_register_service(
        TIRTC_SERVICE_STREAM, &callbacks, &s_callback_guard);
}

int stream_service_start(const char *video_path, const char *audio_path,
                         const char *audio_format, const char *video_format) {
    s_audio_format = audio_format_find(audio_format);
    s_video_format = video_format_find(video_format);
    if (!s_audio_format || ((video_path && video_path[0]) && !s_video_format)) {
        LOG_E("上行媒体格式无效: audio=%s video=%s",
              audio_format ? audio_format : "", video_format ? video_format : "");
        return -1;
    }
    STR_COPY(s_video_path, video_path ? video_path : "");
    STR_COPY(s_audio_path, audio_path ? audio_path : "");
    pthread_mutex_lock(&s_conn_mtx);
    stream_connection_set_init(&s_connections, s_video_path[0] != '\0');
    s_service_active = 1;
    pthread_mutex_unlock(&s_conn_mtx);
    LOG_I("实时流业务已就绪（上行 audio=%s video=%s；下行提交 media sink）",
          s_audio_format->name, s_video_path[0] ? s_video_format->name : "关闭");
    return 0;
}

void stream_service_stop(void) {
    pthread_mutex_lock(&s_conn_mtx);
    if (!s_service_active) {
        pthread_mutex_unlock(&s_conn_mtx);
        return;
    }
    s_service_active = 0;
    tirtc_conn_t handles[STREAM_MAX_CONNECTIONS];
    size_t count = stream_connection_snapshot_all(
        &s_connections, handles, STREAM_MAX_CONNECTIONS);
    stream_connection_set_init(&s_connections, s_video_path[0] != '\0');
    pthread_mutex_unlock(&s_conn_mtx);

    _stop_push_thread();
    for (size_t i = 0; i < count; ++i) TiRtcDisconnect(handles[i]);

    sdk_callback_wait_all(&s_callback_guard);
    LOG_I("实时流业务已停止");
}

int stream_is_active(void) {
    return s_service_active;
}

int h264_source_open(H264FileSource *src, const char *video_path,
                     const char *audio_path) {
    if (!src || !video_path || !audio_path) return -1;
    memset(src, 0, sizeof(*src));
    FileMediaSource *source =
        (FileMediaSource *)calloc(1, sizeof(FileMediaSource));
    if (!source) return -1;
    if (file_media_source_open(source, audio_path,
                               audio_format_find("alaw_8khz"),
                               video_path, video_format_find("h264"),
                               AUDIO_PKT_MS_VOIP) != 0) {
        free(source);
        return -1;
    }
    /* The public helper stores its private source pointer in first_pending. */
    src->first_pending = (char *)source;
    return 0;
}

int h264_source_next_audio(H264FileSource *src, unsigned char *pkt,
                           int pkt_size) {
    if (!src || !src->first_pending || !pkt || pkt_size <= 0) return 0;
    const unsigned char *data = NULL;
    size_t length = 0;
    double duration_ms = 0.0;
    if (file_media_source_next_audio(
            (FileMediaSource *)src->first_pending, &data, &length,
            &duration_ms) != 0)
        return 0;
    (void)duration_ms;
    size_t copy = length < (size_t)pkt_size ? length : (size_t)pkt_size;
    memcpy(pkt, data, copy);
    if (copy < (size_t)pkt_size)
        memset(pkt + copy, ALAW_SILENCE_BYTE, (size_t)pkt_size - copy);
    return pkt_size;
}

int h264_source_next_video(H264FileSource *src, unsigned char **out_data,
                           int *is_key, int force_key) {
    if (!src || !src->first_pending || !out_data || !is_key) return 0;
    const unsigned char *data = NULL;
    size_t length = 0;
    if (file_media_source_next_video(
            (FileMediaSource *)src->first_pending, &data, &length, is_key,
            force_key) != 0 || length > (size_t)INT_MAX)
        return 0;
    unsigned char *copy = (unsigned char *)malloc(length);
    if (!copy) return 0;
    memcpy(copy, data, length);
    *out_data = copy;
    return (int)length;
}

void h264_source_close(H264FileSource *src) {
    if (!src) return;
    FileMediaSource *source = (FileMediaSource *)src->first_pending;
    if (source) {
        file_media_source_close(source);
        free(source);
    }
    memset(src, 0, sizeof(*src));
}
