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
#include "tirtc_runtime.h"
#include "tirtc/tiRTC.h"

extern volatile sig_atomic_t g_stop;

static int s_service_active;

static tirtc_conn_t s_active_conn;
static pthread_mutex_t s_conn_mtx = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t s_handoff_mtx = PTHREAD_MUTEX_INITIALIZER;
static pthread_t s_push_thread;
static int s_push_thread_created;
static int s_push_running;
static int s_force_key;

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

static void _accept_connection(void *opaque) {
    tirtc_conn_t hconn = (tirtc_conn_t)opaque;
    pthread_mutex_lock(&s_handoff_mtx);
    if (!s_service_active) {
        TiRtcDisconnect(hconn);
        pthread_mutex_unlock(&s_handoff_mtx);
        return;
    }

    pthread_mutex_lock(&s_conn_mtx);
    tirtc_conn_t old_conn = s_active_conn;
    s_active_conn = NULL;
    pthread_mutex_unlock(&s_conn_mtx);
    if (old_conn) TiRtcDisconnect(old_conn);
    _stop_push_thread();

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
        TiRtcDisconnect(hconn);
        pthread_mutex_unlock(&s_handoff_mtx);
        return;
    }

    pthread_mutex_lock(&s_conn_mtx);
    if (!s_service_active || s_active_conn) {
        pthread_mutex_unlock(&s_conn_mtx);
        device_media_source_close(&s_media);
        TiRtcDisconnect(hconn);
        pthread_mutex_unlock(&s_handoff_mtx);
        return;
    }
    s_active_conn = hconn;
    s_push_running = 1;
    s_force_key = 1;
    media_rx_log_reset(&s_rx_log);
    if (pthread_create(&s_push_thread, NULL, _push_thread, NULL) == 0) {
        s_push_thread_created = 1;
        LOG_I("客户端已连接，上行媒体线程已启动");
    } else {
        s_push_running = 0;
        s_active_conn = NULL;
        device_media_source_close(&s_media);
        LOG_E("无法创建实时推流线程");
        TiRtcDisconnect(hconn);
    }
    pthread_mutex_unlock(&s_conn_mtx);
    pthread_mutex_unlock(&s_handoff_mtx);
}

static void _on_conn_accepted(tirtc_conn_t hconn) {
    sdk_callback_enter(&s_callback_guard);
    if (sdk_defer_action(&s_callback_guard, _accept_connection, hconn) != 0) {
        LOG_E("无法延后处理客户端连接");
        sdk_defer_disconnect(&s_callback_guard, hconn);
    }
    sdk_callback_leave(&s_callback_guard);
}

static void _on_conn_error(tirtc_conn_t hconn, int error) {
    sdk_callback_enter(&s_callback_guard);
    LOG_E("on_conn_error: %s", TiRtcGetErrorStr(error));
    pthread_mutex_lock(&s_conn_mtx);
    if (s_active_conn == hconn) {
        s_active_conn = NULL;
        s_push_running = 0;
    }
    pthread_mutex_unlock(&s_conn_mtx);
    if (sdk_defer_disconnect(&s_callback_guard, hconn) != 0)
        LOG_E("无法延后断开错误连接");
    sdk_callback_leave(&s_callback_guard);
}

static void _on_disconnected(tirtc_conn_t hconn) {
    sdk_callback_enter(&s_callback_guard);
    LOG_D("客户端断开");
    pthread_mutex_lock(&s_conn_mtx);
    if (s_active_conn == hconn) {
        s_active_conn = NULL;
        s_push_running = 0;
    }
    pthread_mutex_unlock(&s_conn_mtx);
    sdk_callback_leave(&s_callback_guard);
}

static void _on_audio(tirtc_conn_t hconn, const TIRTCFRAMEINFO *frame, void *data) {
    sdk_callback_enter(&s_callback_guard);
    (void)hconn;
    (void)device_media_sink_submit(
        DEVICE_BUSINESS_STREAM, 0, frame->stream_id, frame->media,
        frame->flags, frame->ts, data, frame->length);
    MediaRxNotice notice;
    if (media_rx_log_note_audio(&s_rx_log, "实时流", frame, &notice))
        (void)sdk_defer_copy_action(
            &s_callback_guard, media_rx_log_emit, NULL,
            &notice, sizeof(notice));
    sdk_callback_leave(&s_callback_guard);
}

static void _on_video(tirtc_conn_t hconn, const TIRTCFRAMEINFO *frame, void *data) {
    sdk_callback_enter(&s_callback_guard);
    (void)hconn;
    (void)device_media_sink_submit(
        DEVICE_BUSINESS_STREAM, 1, frame->stream_id, frame->media,
        frame->flags, frame->ts, data, frame->length);
    MediaRxNotice notice;
    if (media_rx_log_note_video(&s_rx_log, "实时流", frame, &notice))
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
    (void)hconn;
    LOG_D("请求关键帧 stream_id=%u", stream_id);
    if (stream_id == STREAM_ID_VIDEO) {
        pthread_mutex_lock(&s_conn_mtx);
        s_force_key = 1;
        pthread_mutex_unlock(&s_conn_mtx);
    }
    sdk_callback_leave(&s_callback_guard);
}

static int _on_sub_video(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_callback_guard);
    (void)hconn;
    LOG_D("订阅视频 stream_id=%u", stream_id);
    if (stream_id == STREAM_ID_VIDEO) {
        /* H5 subscribes after connect completes and may have missed the IDR
         * sent when the connection was accepted. */
        pthread_mutex_lock(&s_conn_mtx);
        s_force_key = 1;
        pthread_mutex_unlock(&s_conn_mtx);
    }
    sdk_callback_leave(&s_callback_guard);
    return 0;
}

static int _on_sub_audio(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_callback_guard);
    (void)hconn;
    LOG_D("订阅音频 stream_id=%u", stream_id);
    sdk_callback_leave(&s_callback_guard);
    return 0;
}

static void _on_unsubscribe(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_callback_guard);
    (void)hconn;
    (void)stream_id;
    sdk_callback_leave(&s_callback_guard);
}

static void *_push_thread(void *arg) {
    (void)arg;
    pthread_mutex_lock(&s_conn_mtx);
    tirtc_conn_t hconn = s_active_conn;
    pthread_mutex_unlock(&s_conn_mtx);
    if (!hconn) return NULL;

    int has_video = device_media_source_has_video(&s_media);
    double audio_pts_ms = 0.0;
    double video_pts_ms = 0.0;
    int64_t wall_start_ms = now_ms();
    int consecutive_failures = 0;

    while (_push_should_run() && !g_stop) {
        if (consecutive_failures >= 3) {
            LOG_E("连续 3 次发送失败，断开连接");
            TiRtcDisconnect(hconn);
            break;
        }
        double target_pts = has_video && video_pts_ms < audio_pts_ms
                                ? video_pts_ms : audio_pts_ms;
        int64_t wait_ms = (int64_t)target_pts - (now_ms() - wall_start_ms);
        if (wait_ms > 2) {
            sleep_ms((int)(wait_ms > 50 ? 50 : wait_ms));
            continue;
        }

        int send_audio = !has_video || audio_pts_ms <= video_pts_ms;
        TIRTCFRAMEINFO frame;
        memset(&frame, 0, sizeof(frame));
        int rc;
        if (send_audio) {
            DeviceMediaPacket packet;
            if (device_media_source_next_audio(&s_media, &packet) <= 0)
                break;
            frame.stream_id = STREAM_ID_AUDIO;
            frame.media = s_audio_format->media;
            frame.flags = s_audio_format->flags;
            frame.ts = (uint32_t)audio_pts_ms;
            frame.length = (uint32_t)packet.length;
            rc = TiRtcSendAudioStream(hconn, &frame, packet.data);
            audio_pts_ms += packet.duration_ms;
        } else {
            DeviceMediaPacket packet;
            if (device_media_source_next_video(
                    &s_media, _take_force_key(), &packet) <= 0)
                break;
            frame.stream_id = STREAM_ID_VIDEO;
            frame.media = s_video_format->media;
            frame.flags = packet.key_frame ? TIRTC_FRAME_FLAG_KEY_FRAME : 0;
            frame.ts = (uint32_t)video_pts_ms;
            frame.length = (uint32_t)packet.length;
            rc = TiRtcSendVideoStream(hconn, &frame, packet.data);
            video_pts_ms += 1000.0 / VIDEO_FPS;
        }

        if (rc >= 0) {
            consecutive_failures = 0;
        } else if (rc == TIRTC_E_CONN_TIMEOUTCLOSE ||
                   rc == TIRTC_E_CONN_REMOTECLOSE ||
                   rc == TIRTC_E_CONN_OTHER_ERROR) {
            LOG_D("连接已关闭，退出推流");
            break;
        } else if (rc == TIRTC_E_INVALID_HANDLE) {
            sleep_ms(5);
        } else {
            if (!send_audio && rc == TIRTC_E_BUSY) {
                pthread_mutex_lock(&s_conn_mtx);
                s_force_key = 1;
                pthread_mutex_unlock(&s_conn_mtx);
            }
            LOG_E("发送%s失败 rc=%d: %s", send_audio ? "音频" : "视频",
                  rc, TiRtcGetErrorStr(rc));
            consecutive_failures++;
        }
    }
    device_media_source_close(&s_media);
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
    callbacks.on_unsubscribe_video = _on_unsubscribe;
    callbacks.on_subscribe_audio = _on_sub_audio;
    callbacks.on_unsubscribe_audio = _on_unsubscribe;
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
    s_service_active = 1;
    LOG_I("实时流业务已就绪（上行 audio=%s video=%s；下行提交 media sink）",
          s_audio_format->name, s_video_path[0] ? s_video_format->name : "关闭");
    return 0;
}

void stream_service_stop(void) {
    if (!s_service_active) return;
    s_service_active = 0;
    _stop_push_thread();

    pthread_mutex_lock(&s_conn_mtx);
    tirtc_conn_t hconn = s_active_conn;
    s_active_conn = NULL;
    pthread_mutex_unlock(&s_conn_mtx);
    if (hconn) TiRtcDisconnect(hconn);

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
