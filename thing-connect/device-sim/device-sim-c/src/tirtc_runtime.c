#include "tirtc_runtime.h"

#include <errno.h>
#include <pthread.h>
#include <string.h>
#include <time.h>

#define LOG_MODULE "rtc-runtime"
#include "common.h"
#include "device_adapter.h"
#include "sdk_callback_guard.h"

extern volatile sig_atomic_t g_stop;

#define RUNTIME_CONNECTION_CAPACITY 32
#define RUNTIME_START_TIMEOUT_SEC 10
#define RUNTIME_STOP_TIMEOUT_SEC 8

typedef struct {
    tirtc_conn_t hconn;
    TirtcService service;
    uint64_t generation;
} RuntimeConnection;

typedef struct {
    pthread_mutex_t lock;
    pthread_cond_t started_cond;
    pthread_cond_t stopped_cond;
    int initialized;
    int start_submitted;
    int started;
    int stopped;
    TirtcService active_service;
    uint64_t active_generation;
    uint64_t next_generation;
    TIRTCCALLBACKS handlers[TIRTC_SERVICE_COUNT];
    SdkCallbackGuard *service_guards[TIRTC_SERVICE_COUNT];
    unsigned char registered[TIRTC_SERVICE_COUNT];
    RuntimeConnection connections[RUNTIME_CONNECTION_CAPACITY];
    TIRTCCALLBACKS sdk_callbacks;
    SdkCallbackGuard callback_guard;
    SdkCallbackGuard sdk_log_guard;
} TirtcRuntime;

static TirtcRuntime s_runtime = {
    .lock = PTHREAD_MUTEX_INITIALIZER,
    .started_cond = PTHREAD_COND_INITIALIZER,
    .stopped_cond = PTHREAD_COND_INITIALIZER,
    .callback_guard = SDK_CALLBACK_GUARD_INITIALIZER,
    .sdk_log_guard = SDK_CALLBACK_GUARD_INITIALIZER,
};

#ifdef DEVICE_SIM_TESTING
typedef struct {
    pthread_mutex_t lock;
    int set_option_rc;
    int init_rc;
    int start_rc;
    int stop_rc;
    int emit_started;
    int emit_stopped;
    TirtcRuntimeTestSdkStats stats;
    TIRTCCALLBACKS callbacks;
} TestSdkRuntime;

static TestSdkRuntime s_test_sdk = {
    .lock = PTHREAD_MUTEX_INITIALIZER,
    .emit_started = 1,
    .emit_stopped = 1,
};

static int _test_sdk_set_option(TIRTCOPTION option, const void *data,
                                uint32_t length) {
    (void)option;
    (void)data;
    (void)length;
    pthread_mutex_lock(&s_test_sdk.lock);
    s_test_sdk.stats.set_option_calls++;
    int rc = s_test_sdk.set_option_rc;
    pthread_mutex_unlock(&s_test_sdk.lock);
    return rc;
}

static int _test_sdk_init(void) {
    pthread_mutex_lock(&s_test_sdk.lock);
    s_test_sdk.stats.init_calls++;
    int rc = s_test_sdk.init_rc;
    pthread_mutex_unlock(&s_test_sdk.lock);
    return rc;
}

static int _test_sdk_start(const char *device_id,
                           const TIRTCCALLBACKS *callbacks) {
    (void)device_id;
    TIRTCCALLBACKS copied = {0};
    pthread_mutex_lock(&s_test_sdk.lock);
    s_test_sdk.stats.start_calls++;
    if (callbacks) {
        s_test_sdk.callbacks = *callbacks;
        copied = *callbacks;
    }
    int rc = s_test_sdk.start_rc;
    int emit_started = s_test_sdk.emit_started;
    pthread_mutex_unlock(&s_test_sdk.lock);
    if (rc == 0 && emit_started && copied.on_event)
        copied.on_event(TIRTC_EVENT_SYS_STARTED, NULL, 0);
    return rc;
}

static int _test_sdk_stop(void) {
    TIRTCCALLBACKS copied;
    pthread_mutex_lock(&s_test_sdk.lock);
    s_test_sdk.stats.stop_calls++;
    copied = s_test_sdk.callbacks;
    int rc = s_test_sdk.stop_rc;
    int emit_stopped = s_test_sdk.emit_stopped;
    pthread_mutex_unlock(&s_test_sdk.lock);
    if (emit_stopped && copied.on_event)
        copied.on_event(TIRTC_EVENT_SYS_STOPPED, NULL, 0);
    return rc;
}

static void _test_sdk_uninit(void) {
    pthread_mutex_lock(&s_test_sdk.lock);
    s_test_sdk.stats.uninit_calls++;
    pthread_mutex_unlock(&s_test_sdk.lock);
}

static const char *_test_sdk_error_string(int error) {
    (void)error;
    return "test-sdk-error";
}

static const char *_test_sdk_version(void) {
    return "test-sdk";
}

static void _test_sdk_log_config(int output_to_console, const char *path,
                                 uint32_t size) {
    (void)output_to_console;
    (void)path;
    (void)size;
}

static void _test_sdk_log_level(int level) {
    (void)level;
}

static void _test_sdk_log_callback(TIRTCLOGCALLBACK callback) {
    (void)callback;
}

#define TiRtcSetOption      _test_sdk_set_option
#define TiRtcInit           _test_sdk_init
#define TiRtcStart          _test_sdk_start
#define TiRtcStop           _test_sdk_stop
#define TiRtcUninit         _test_sdk_uninit
#define TiRtcGetErrorStr    _test_sdk_error_string
#define TiRtcGetVersion     _test_sdk_version
#define TiRtcLogConfig      _test_sdk_log_config
#define TiRtcLogSetLevel    _test_sdk_log_level
#define TiRtcLogSetCallback _test_sdk_log_callback
#endif

static int _valid_service(TirtcService service) {
    return service > TIRTC_SERVICE_NONE && service < TIRTC_SERVICE_COUNT;
}

const char *tirtc_runtime_service_name(TirtcService service) {
    switch (service) {
    case TIRTC_SERVICE_STREAM: return "stream";
    case TIRTC_SERVICE_VOIP: return "voip";
    case TIRTC_SERVICE_AI: return "ai";
    case TIRTC_SERVICE_CALL: return "device-call";
    default: return "none";
    }
}

static RuntimeConnection *_find_connection_locked(tirtc_conn_t hconn) {
    for (size_t i = 0; i < RUNTIME_CONNECTION_CAPACITY; ++i) {
        if (s_runtime.connections[i].hconn == hconn)
            return &s_runtime.connections[i];
    }
    return NULL;
}

static RuntimeConnection *_connection_slot_locked(void) {
    RuntimeConnection *stale = NULL;
    for (size_t i = 0; i < RUNTIME_CONNECTION_CAPACITY; ++i) {
        RuntimeConnection *entry = &s_runtime.connections[i];
        if (!entry->hconn) return entry;
        if (!stale &&
            (entry->service != s_runtime.active_service ||
             entry->generation != s_runtime.active_generation))
            stale = entry;
    }
    return stale;
}

static int _bind_locked(tirtc_conn_t hconn, TirtcService service,
                        uint64_t generation) {
    if (!hconn || !_valid_service(service) || generation == 0)
        return -1;
    RuntimeConnection *entry = _find_connection_locked(hconn);
    if (!entry) entry = _connection_slot_locked();
    if (!entry) return -1;
    *entry = (RuntimeConnection){hconn, service, generation};
    return 0;
}

static int _current_handler(tirtc_conn_t hconn, TIRTCCALLBACKS *handler,
                            TirtcService *service_out,
                            uint64_t *generation_out,
                            int remove) {
    int current = 0;
    pthread_mutex_lock(&s_runtime.lock);
    RuntimeConnection *entry = _find_connection_locked(hconn);
    if (entry) {
        TirtcService service = entry->service;
        uint64_t generation = entry->generation;
        current = s_runtime.started &&
                  service == s_runtime.active_service &&
                  generation == s_runtime.active_generation &&
                  s_runtime.registered[service];
        if (current && handler) *handler = s_runtime.handlers[service];
        if (service_out) *service_out = service;
        if (generation_out) *generation_out = generation;
        if (remove) memset(entry, 0, sizeof(*entry));
    }
    pthread_mutex_unlock(&s_runtime.lock);
    return current;
}

static void _defer_stale_disconnect(tirtc_conn_t hconn) {
    pthread_mutex_lock(&s_runtime.lock);
    int sdk_running = s_runtime.started;
    pthread_mutex_unlock(&s_runtime.lock);
    if (!sdk_running) return;
    if (sdk_defer_disconnect(&s_runtime.callback_guard, hconn) != 0)
        LOG_E("无法延后断开未归属连接 hconn=%p", (void *)hconn);
}

static void _emit_sdk_log(void *context, const void *data, size_t length) {
    (void)context;
    LOG_SDK(data, length);
}

static void _sdk_log_cb(const char *log, uint32_t length) {
    if (!log || length == 0) return;
    size_t copy_length = length;
    if (copy_length > SDK_CALLBACK_COPY_CAPACITY)
        copy_length = SDK_CALLBACK_COPY_CAPACITY;
    sdk_callback_enter(&s_runtime.sdk_log_guard);
    /* Diagnostic logs are lossy by design: never block an SDK callback or
     * compete with connection-control work when the log queue is saturated. */
    (void)sdk_defer_copy_action(&s_runtime.sdk_log_guard, _emit_sdk_log, NULL,
                                log, copy_length);
    sdk_callback_leave(&s_runtime.sdk_log_guard);
}

static void _on_event(int event, const void *data, int len) {
    sdk_callback_enter(&s_runtime.callback_guard);
    (void)data;
    (void)len;
    pthread_mutex_lock(&s_runtime.lock);
    if (event == TIRTC_EVENT_SYS_STARTED) {
        s_runtime.started = 1;
        s_runtime.stopped = 0;
        pthread_cond_broadcast(&s_runtime.started_cond);
    } else if (event == TIRTC_EVENT_SYS_STOPPED) {
        s_runtime.started = 0;
        s_runtime.stopped = 1;
        pthread_cond_broadcast(&s_runtime.stopped_cond);
    }
    pthread_mutex_unlock(&s_runtime.lock);
    if (event == TIRTC_EVENT_SYS_STARTED)
        LOG_I("SDK 已启动，等待业务会话");
    else if (event == TIRTC_EVENT_SYS_STOPPED)
        LOG_I("SDK 已停止");
    else if (event == TIRTC_EVENT_ACCESS_HIJACKING)
        LOG_E("SDK 检测到服务访问重定向");
    sdk_callback_leave(&s_runtime.callback_guard);
}

static void _on_conn_accepted(tirtc_conn_t hconn) {
    sdk_callback_enter(&s_runtime.callback_guard);
    TIRTCCALLBACKS handler = {0};
    TirtcService service;
    uint64_t generation;
    int dispatch = 0;

    pthread_mutex_lock(&s_runtime.lock);
    service = s_runtime.active_service;
    generation = s_runtime.active_generation;
    if (s_runtime.started && _valid_service(service) &&
        s_runtime.registered[service] &&
        s_runtime.handlers[service].on_conn_accepted &&
        _bind_locked(hconn, service, generation) == 0) {
        handler = s_runtime.handlers[service];
        dispatch = 1;
    }
    pthread_mutex_unlock(&s_runtime.lock);

    if (dispatch) {
        LOG_D("入站连接已绑定 service=%s generation=%llu hconn=%p",
              tirtc_runtime_service_name(service),
              (unsigned long long)generation, (void *)hconn);
        handler.on_conn_accepted(hconn);
    } else {
        LOG_W("拒绝无活动业务接收的入站连接 hconn=%p", (void *)hconn);
        _defer_stale_disconnect(hconn);
    }
    sdk_callback_leave(&s_runtime.callback_guard);
}

static void _on_conn_error(tirtc_conn_t hconn, int error) {
    sdk_callback_enter(&s_runtime.callback_guard);
    TIRTCCALLBACKS handler = {0};
    TirtcService service = TIRTC_SERVICE_NONE;
    uint64_t generation = 0;
    if (_current_handler(hconn, &handler, &service, &generation, 0) &&
        handler.on_conn_error) {
        handler.on_conn_error(hconn, error);
    } else {
        LOG_D("丢弃迟到连接错误 service=%s generation=%llu hconn=%p rc=%d",
              tirtc_runtime_service_name(service),
              (unsigned long long)generation, (void *)hconn, error);
        _defer_stale_disconnect(hconn);
    }
    sdk_callback_leave(&s_runtime.callback_guard);
}

static void _on_disconnected(tirtc_conn_t hconn) {
    sdk_callback_enter(&s_runtime.callback_guard);
    TIRTCCALLBACKS handler = {0};
    TirtcService service = TIRTC_SERVICE_NONE;
    uint64_t generation = 0;
    int current = _current_handler(hconn, &handler, &service, &generation, 1);
    if (current && handler.on_disconnected)
        handler.on_disconnected(hconn);
    else
        LOG_D("清理迟到断连 service=%s generation=%llu hconn=%p",
              tirtc_runtime_service_name(service),
              (unsigned long long)generation, (void *)hconn);
    sdk_callback_leave(&s_runtime.callback_guard);
}

#define DISPATCH_FRAME(name, field)                                             \
    static void name(tirtc_conn_t hconn, const TIRTCFRAMEINFO *frame,           \
                     void *data) {                                               \
        sdk_callback_enter(&s_runtime.callback_guard);                           \
        TIRTCCALLBACKS handler = {0};                                            \
        if (_current_handler(hconn, &handler, NULL, NULL, 0) && handler.field)   \
            handler.field(hconn, frame, data);                                   \
        sdk_callback_leave(&s_runtime.callback_guard);                           \
    }

DISPATCH_FRAME(_on_audio, on_audio)
DISPATCH_FRAME(_on_video, on_video)
DISPATCH_FRAME(_on_message, on_message)

static void _on_command(tirtc_conn_t hconn, uint32_t command,
                        const void *data, uint32_t length) {
    sdk_callback_enter(&s_runtime.callback_guard);
    TIRTCCALLBACKS handler = {0};
    if (_current_handler(hconn, &handler, NULL, NULL, 0) &&
        handler.on_command)
        handler.on_command(hconn, command, data, length);
    sdk_callback_leave(&s_runtime.callback_guard);
}

static void _on_request_key_frame(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_runtime.callback_guard);
    TIRTCCALLBACKS handler = {0};
    if (_current_handler(hconn, &handler, NULL, NULL, 0) &&
        handler.on_request_key_frame)
        handler.on_request_key_frame(hconn, stream_id);
    sdk_callback_leave(&s_runtime.callback_guard);
}

static int _on_subscribe_video(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_runtime.callback_guard);
    TIRTCCALLBACKS handler = {0};
    int result = -1;
    if (_current_handler(hconn, &handler, NULL, NULL, 0) &&
        handler.on_subscribe_video)
        result = handler.on_subscribe_video(hconn, stream_id);
    sdk_callback_leave(&s_runtime.callback_guard);
    return result;
}

static int _on_subscribe_audio(tirtc_conn_t hconn, uint8_t stream_id) {
    sdk_callback_enter(&s_runtime.callback_guard);
    TIRTCCALLBACKS handler = {0};
    int result = -1;
    if (_current_handler(hconn, &handler, NULL, NULL, 0) &&
        handler.on_subscribe_audio)
        result = handler.on_subscribe_audio(hconn, stream_id);
    sdk_callback_leave(&s_runtime.callback_guard);
    return result;
}

#define DISPATCH_UNSUB(name, field)                                             \
    static void name(tirtc_conn_t hconn, uint8_t stream_id) {                   \
        sdk_callback_enter(&s_runtime.callback_guard);                           \
        TIRTCCALLBACKS handler = {0};                                            \
        if (_current_handler(hconn, &handler, NULL, NULL, 0) && handler.field)   \
            handler.field(hconn, stream_id);                                     \
        sdk_callback_leave(&s_runtime.callback_guard);                           \
    }

DISPATCH_UNSUB(_on_unsubscribe_video, on_unsubscribe_video)
DISPATCH_UNSUB(_on_unsubscribe_audio, on_unsubscribe_audio)

int tirtc_runtime_register_service(TirtcService service,
                                   const TIRTCCALLBACKS *callbacks,
                                   SdkCallbackGuard *callback_guard) {
    if (!_valid_service(service) || !callbacks) return -1;
    pthread_mutex_lock(&s_runtime.lock);
    if (s_runtime.start_submitted) {
        pthread_mutex_unlock(&s_runtime.lock);
        LOG_E("SDK 启动后不能修改业务回调 service=%s",
              tirtc_runtime_service_name(service));
        return -1;
    }
    if (callback_guard && sdk_callback_guard_start(callback_guard) != 0) {
        pthread_mutex_unlock(&s_runtime.lock);
        LOG_E("无法启动业务控制队列 service=%s",
              tirtc_runtime_service_name(service));
        return -1;
    }
    s_runtime.handlers[service] = *callbacks;
    s_runtime.handlers[service].on_event = NULL;
    s_runtime.service_guards[service] = callback_guard;
    s_runtime.registered[service] = 1;
    pthread_mutex_unlock(&s_runtime.lock);
    return 0;
}

static void _build_sdk_callbacks(void) {
    TIRTCCALLBACKS *callbacks = &s_runtime.sdk_callbacks;
    memset(callbacks, 0, sizeof(*callbacks));
    callbacks->on_event = _on_event;
    callbacks->on_conn_accepted = _on_conn_accepted;
    callbacks->on_conn_error = _on_conn_error;
    callbacks->on_disconnected = _on_disconnected;
    callbacks->on_audio = _on_audio;
    callbacks->on_video = _on_video;
    callbacks->on_message = _on_message;
    callbacks->on_command = _on_command;
    callbacks->on_request_key_frame = _on_request_key_frame;
    callbacks->on_subscribe_video = _on_subscribe_video;
    callbacks->on_unsubscribe_video = _on_unsubscribe_video;
    callbacks->on_subscribe_audio = _on_subscribe_audio;
    callbacks->on_unsubscribe_audio = _on_unsubscribe_audio;
}

static int _wait_started(void) {
    struct timespec deadline;
    clock_gettime(CLOCK_REALTIME, &deadline);
    deadline.tv_sec += RUNTIME_START_TIMEOUT_SEC;
    pthread_mutex_lock(&s_runtime.lock);
    while (!s_runtime.started && !g_stop) {
        if (pthread_cond_timedwait(&s_runtime.started_cond, &s_runtime.lock,
                                   &deadline) == ETIMEDOUT)
            break;
    }
    int started = s_runtime.started;
    pthread_mutex_unlock(&s_runtime.lock);
    return started ? 0 : -1;
}

static int _wait_stopped(void) {
    struct timespec deadline;
    clock_gettime(CLOCK_REALTIME, &deadline);
    deadline.tv_sec += RUNTIME_STOP_TIMEOUT_SEC;
    pthread_mutex_lock(&s_runtime.lock);
    while (!s_runtime.stopped) {
        if (pthread_cond_timedwait(&s_runtime.stopped_cond, &s_runtime.lock,
                                   &deadline) == ETIMEDOUT)
            break;
    }
    int stopped = s_runtime.stopped;
    pthread_mutex_unlock(&s_runtime.lock);
    return stopped ? 0 : -1;
}

int tirtc_runtime_start(const char *device_id, const char *secret_key,
                        const char *client_id, const char *endpoint) {
    if (!device_id || !device_id[0] || !secret_key || !secret_key[0] ||
        !client_id || !client_id[0])
        return -1;

    pthread_mutex_lock(&s_runtime.lock);
    if (s_runtime.started || s_runtime.start_submitted) {
        int ready = s_runtime.started;
        pthread_mutex_unlock(&s_runtime.lock);
        return ready ? 0 : -1;
    }
    s_runtime.started = 0;
    s_runtime.stopped = 0;
    pthread_mutex_unlock(&s_runtime.lock);

    if (sdk_callback_guard_start(&s_runtime.callback_guard) != 0) {
        LOG_E("无法启动 TiRTC runtime 控制队列");
        device_recovery_report(DEVICE_RECOVERY_TIRTC, -1,
                               "TiRTC runtime 控制队列启动失败");
        return -1;
    }
    if (sdk_callback_guard_start(&s_runtime.sdk_log_guard) != 0) {
        LOG_E("无法启动 TiRTC SDK 日志队列");
        tirtc_runtime_stop();
        return -1;
    }
    for (int service = TIRTC_SERVICE_NONE + 1;
         service < TIRTC_SERVICE_COUNT; ++service) {
        SdkCallbackGuard *guard = s_runtime.service_guards[service];
        if (guard && sdk_callback_guard_start(guard) != 0) {
            LOG_E("无法启动业务控制队列 service=%s",
                  tirtc_runtime_service_name((TirtcService)service));
            tirtc_runtime_stop();
            return -1;
        }
    }

    uint32_t buffer_size = 1024 * 1024;
    int rc = TiRtcSetOption(TIRTC_OPT_MAX_SEND_BUFFER, &buffer_size,
                            sizeof(buffer_size));
    if (rc != 0) {
        LOG_E("设置发送缓冲区失败 rc=%d (%s)", rc, TiRtcGetErrorStr(rc));
        device_recovery_report(DEVICE_RECOVERY_TIRTC, rc,
                               "TiRTC 发送缓冲区配置失败");
        tirtc_runtime_stop();
        return -1;
    }
    rc = TiRtcInit();
    if (rc != 0) {
        LOG_E("TiRtcInit 失败 rc=%d (%s)", rc, TiRtcGetErrorStr(rc));
        device_recovery_report(DEVICE_RECOVERY_TIRTC, rc,
                               "TiRtcInit 失败");
        tirtc_runtime_stop();
        return -1;
    }
    pthread_mutex_lock(&s_runtime.lock);
    s_runtime.initialized = 1;
    pthread_mutex_unlock(&s_runtime.lock);

    TiRtcLogConfig(0, NULL, 0);
    TiRtcLogSetLevel(3);
    if (g_log_level <= LOG_DEBUG) {
        TiRtcLogSetCallback(_sdk_log_cb);
        TiRtcLogSetLevel(8);
    }

    if (endpoint && endpoint[0]) {
        rc = TiRtcSetOption(TIRTC_OPT_SERVICE_ENDPOINT, endpoint,
                            (uint32_t)strlen(endpoint));
        if (rc != 0)
            LOG_E("设置 TiRTC 服务入口失败 rc=%d (%s)",
                  rc, TiRtcGetErrorStr(rc));
    }
    if (rc == 0)
        rc = TiRtcSetOption(TIRTC_OPT_DEVICE_SECRET_KEY, secret_key,
                            (uint32_t)strlen(secret_key));
    if (rc == 0)
        rc = TiRtcSetOption(TIRTC_OPT_CLIENT_ID, client_id,
                            (uint32_t)strlen(client_id));
    if (rc != 0) {
        LOG_E("设置 TiRTC 启动参数失败 rc=%d (%s)",
              rc, TiRtcGetErrorStr(rc));
        device_recovery_report(DEVICE_RECOVERY_TIRTC, rc,
                               "TiRTC 启动参数配置失败");
        tirtc_runtime_stop();
        return -1;
    }

    _build_sdk_callbacks();
    rc = TiRtcStart(device_id, &s_runtime.sdk_callbacks);
    if (rc != 0) {
        LOG_E("TiRtcStart 失败 rc=%d (%s)", rc, TiRtcGetErrorStr(rc));
        device_recovery_report(DEVICE_RECOVERY_TIRTC, rc,
                               "TiRtcStart 失败");
        tirtc_runtime_stop();
        return -1;
    }
    pthread_mutex_lock(&s_runtime.lock);
    s_runtime.start_submitted = 1;
    pthread_mutex_unlock(&s_runtime.lock);

    LOG_I("TiRTC %s 启动中（进程级单实例）", TiRtcGetVersion());
    if (_wait_started() == 0) return 0;

    LOG_E("等待 SDK 启动%s", g_stop ? "已取消" : "超时");
    device_recovery_report(DEVICE_RECOVERY_TIRTC, -1,
                           g_stop ? "TiRTC 启动已取消" :
                                    "TiRTC 启动等待超时");
    tirtc_runtime_stop();
    return -1;
}

void tirtc_runtime_stop(void) {
    pthread_mutex_lock(&s_runtime.lock);
    int submitted = s_runtime.start_submitted;
    int initialized = s_runtime.initialized;
    s_runtime.active_service = TIRTC_SERVICE_NONE;
    s_runtime.active_generation = ++s_runtime.next_generation;
    pthread_mutex_unlock(&s_runtime.lock);

    if (submitted) {
        /*
         * Stop accepting business callbacks first, then drain callbacks that
         * may already have selected the old generation.  Those callbacks can
         * enqueue service work; service work can in turn cause one final
         * callback through the unified table.  All three barriers must finish
         * before TiRtcStop.
         */
        sdk_callback_wait_all(&s_runtime.callback_guard);
        for (int service = TIRTC_SERVICE_NONE + 1;
             service < TIRTC_SERVICE_COUNT; ++service) {
            SdkCallbackGuard *guard = s_runtime.service_guards[service];
            if (guard) sdk_callback_wait_all(guard);
        }
        sdk_callback_wait_all(&s_runtime.callback_guard);
        int rc = TiRtcStop();
        if (rc != 0)
            LOG_W("TiRtcStop 返回 rc=%d (%s)", rc, TiRtcGetErrorStr(rc));
        if (_wait_stopped() != 0)
            LOG_W("等待 TiRTC SYS_STOPPED 超时，继续执行反初始化");
        sdk_callback_wait_all(&s_runtime.callback_guard);
        for (int service = TIRTC_SERVICE_NONE + 1;
             service < TIRTC_SERVICE_COUNT; ++service) {
            SdkCallbackGuard *guard = s_runtime.service_guards[service];
            if (guard) sdk_callback_wait_all(guard);
        }
        /* A final deferred service Disconnect can produce one last callback
         * through the unified runtime table. */
        sdk_callback_wait_all(&s_runtime.callback_guard);
    }
    if (initialized) {
        TiRtcUninit();
        sdk_callback_wait_all(&s_runtime.callback_guard);
    }
    sdk_callback_wait_all(&s_runtime.sdk_log_guard);

    for (int service = TIRTC_SERVICE_NONE + 1;
         service < TIRTC_SERVICE_COUNT; ++service) {
        SdkCallbackGuard *guard = s_runtime.service_guards[service];
        if (guard) sdk_callback_guard_stop(guard);
    }
    sdk_callback_guard_stop(&s_runtime.callback_guard);
    sdk_callback_guard_stop(&s_runtime.sdk_log_guard);

    pthread_mutex_lock(&s_runtime.lock);
    s_runtime.initialized = 0;
    s_runtime.start_submitted = 0;
    s_runtime.started = 0;
    memset(s_runtime.connections, 0, sizeof(s_runtime.connections));
    pthread_mutex_unlock(&s_runtime.lock);
}

int tirtc_runtime_is_started(void) {
    pthread_mutex_lock(&s_runtime.lock);
    int started = s_runtime.started;
    pthread_mutex_unlock(&s_runtime.lock);
    return started;
}

uint64_t tirtc_runtime_activate(TirtcService service) {
    if (!_valid_service(service)) return 0;
    pthread_mutex_lock(&s_runtime.lock);
    if (!s_runtime.started || !s_runtime.registered[service] ||
        s_runtime.active_service != TIRTC_SERVICE_NONE) {
        pthread_mutex_unlock(&s_runtime.lock);
        return 0;
    }
    uint64_t generation = ++s_runtime.next_generation;
    if (generation == 0) generation = ++s_runtime.next_generation;
    s_runtime.active_service = service;
    s_runtime.active_generation = generation;
    pthread_mutex_unlock(&s_runtime.lock);
    LOG_I("业务已激活 service=%s generation=%llu",
          tirtc_runtime_service_name(service),
          (unsigned long long)generation);
    return generation;
}

int tirtc_runtime_deactivate(TirtcService service, uint64_t generation) {
    pthread_mutex_lock(&s_runtime.lock);
    int matched = s_runtime.active_service == service &&
                  s_runtime.active_generation == generation;
    if (matched) {
        s_runtime.active_service = TIRTC_SERVICE_NONE;
        s_runtime.active_generation = ++s_runtime.next_generation;
    }
    pthread_mutex_unlock(&s_runtime.lock);
    if (matched)
        LOG_I("业务已停用 service=%s generation=%llu",
              tirtc_runtime_service_name(service),
              (unsigned long long)generation);
    return matched ? 0 : -1;
}

int tirtc_runtime_is_current(TirtcService service, uint64_t generation) {
    pthread_mutex_lock(&s_runtime.lock);
    int current = s_runtime.started &&
                  s_runtime.active_service == service &&
                  s_runtime.active_generation == generation;
    pthread_mutex_unlock(&s_runtime.lock);
    return current;
}

int tirtc_runtime_bind_active_connection(TirtcService service,
                                         tirtc_conn_t hconn) {
    pthread_mutex_lock(&s_runtime.lock);
    uint64_t generation = s_runtime.active_generation;
    int current = s_runtime.started && s_runtime.active_service == service;
    int rc = current ? _bind_locked(hconn, service, generation) : -1;
    pthread_mutex_unlock(&s_runtime.lock);
    if (rc == 0)
        LOG_D("出站连接已绑定 service=%s generation=%llu hconn=%p",
              tirtc_runtime_service_name(service),
              (unsigned long long)generation, (void *)hconn);
    else
        LOG_W("拒绝绑定非当前业务连接 service=%s hconn=%p",
              tirtc_runtime_service_name(service), (void *)hconn);
    return rc;
}

#ifdef DEVICE_SIM_TESTING
void tirtc_runtime_test_sdk_configure(int set_option_rc, int init_rc,
                                      int start_rc, int stop_rc,
                                      int emit_started, int emit_stopped) {
    pthread_mutex_lock(&s_test_sdk.lock);
    s_test_sdk.set_option_rc = set_option_rc;
    s_test_sdk.init_rc = init_rc;
    s_test_sdk.start_rc = start_rc;
    s_test_sdk.stop_rc = stop_rc;
    s_test_sdk.emit_started = emit_started;
    s_test_sdk.emit_stopped = emit_stopped;
    pthread_mutex_unlock(&s_test_sdk.lock);
}

void tirtc_runtime_test_sdk_get_stats(TirtcRuntimeTestSdkStats *stats) {
    if (!stats) return;
    pthread_mutex_lock(&s_test_sdk.lock);
    *stats = s_test_sdk.stats;
    pthread_mutex_unlock(&s_test_sdk.lock);
}

void tirtc_runtime_test_prepare_lifecycle(void) {
    tirtc_runtime_stop();
    pthread_mutex_lock(&s_runtime.lock);
    memset(s_runtime.handlers, 0, sizeof(s_runtime.handlers));
    memset(s_runtime.service_guards, 0, sizeof(s_runtime.service_guards));
    memset(s_runtime.registered, 0, sizeof(s_runtime.registered));
    memset(s_runtime.connections, 0, sizeof(s_runtime.connections));
    memset(&s_runtime.sdk_callbacks, 0, sizeof(s_runtime.sdk_callbacks));
    s_runtime.initialized = 0;
    s_runtime.start_submitted = 0;
    s_runtime.started = 0;
    s_runtime.stopped = 0;
    s_runtime.active_service = TIRTC_SERVICE_NONE;
    s_runtime.active_generation = 0;
    s_runtime.next_generation = 0;
    pthread_mutex_unlock(&s_runtime.lock);

    pthread_mutex_lock(&s_test_sdk.lock);
    memset(&s_test_sdk.stats, 0, sizeof(s_test_sdk.stats));
    memset(&s_test_sdk.callbacks, 0, sizeof(s_test_sdk.callbacks));
    s_test_sdk.set_option_rc = 0;
    s_test_sdk.init_rc = 0;
    s_test_sdk.start_rc = 0;
    s_test_sdk.stop_rc = 0;
    s_test_sdk.emit_started = 1;
    s_test_sdk.emit_stopped = 1;
    pthread_mutex_unlock(&s_test_sdk.lock);
}

void tirtc_runtime_test_reset(void) {
    tirtc_runtime_test_prepare_lifecycle();
    pthread_mutex_lock(&s_runtime.lock);
    s_runtime.started = 1;
    pthread_mutex_unlock(&s_runtime.lock);
}

void tirtc_runtime_test_on_conn_accepted(tirtc_conn_t hconn) {
    _on_conn_accepted(hconn);
}
void tirtc_runtime_test_on_conn_error(tirtc_conn_t hconn, int error) {
    _on_conn_error(hconn, error);
}
void tirtc_runtime_test_on_disconnected(tirtc_conn_t hconn) {
    _on_disconnected(hconn);
}
void tirtc_runtime_test_on_audio(tirtc_conn_t hconn,
                                 const TIRTCFRAMEINFO *frame, void *data) {
    _on_audio(hconn, frame, data);
}
void tirtc_runtime_test_on_command(tirtc_conn_t hconn, uint32_t command,
                                   const void *data, uint32_t length) {
    _on_command(hconn, command, data, length);
}
#endif
