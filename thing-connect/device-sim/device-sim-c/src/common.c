/** \file common.c
 * \brief Central, thread-safe runtime logger for the C device simulator.
 */

#define LOG_MODULE "log"
#include "common.h"
#include "device_adapter.h"

int g_log_level = LOG_DEBUG;
volatile sig_atomic_t g_stop = 0;

static pthread_mutex_t s_log_lock = PTHREAD_MUTEX_INITIALIZER;
static log_sink_fn s_log_sink = NULL;
static void *s_log_sink_user = NULL;

int64_t now_ms(void) {
    return device_platform_monotonic_ms();
}

void sleep_ms(int ms) {
    if (ms <= 0) return;
    device_platform_sleep_ms((uint32_t)ms);
}

void log_timestamp(char *buf, size_t size) {
    if (!buf || size == 0) return;
    int64_t wall_ms = device_platform_wall_time_ms();
    if (wall_ms < 0) {
        snprintf(buf, size, "--:--:--.---");
        return;
    }
    time_t seconds = (time_t)(wall_ms / 1000);
    struct tm tm_info;
    if (!localtime_r(&seconds, &tm_info)) {
        snprintf(buf, size, "--:--:--.---");
        return;
    }
    snprintf(buf, size, "%02d:%02d:%02d.%03d",
             tm_info.tm_hour, tm_info.tm_min, tm_info.tm_sec,
             (int)(wall_ms % 1000));
}

int rand_hex(char *out, int len) {
    if (!out || len < 0 || len > 32) return -1;
    unsigned char bytes[32];
    if (device_security_random_bytes(bytes, (size_t)len) != 0) {
        if (len >= 0) out[0] = '\0';
        device_recovery_report(DEVICE_RECOVERY_SECURITY, -1,
                               "密码学随机数获取失败");
        return -1;
    }
    static const char digits[] = "0123456789abcdef";
    for (int index = 0; index < len; ++index) {
        out[index * 2] = digits[bytes[index] >> 4];
        out[index * 2 + 1] = digits[bytes[index] & 0x0f];
    }
    out[len * 2] = '\0';
    return 0;
}

static const char *_level_name(int level) {
    switch (level) {
    case LOG_DEBUG: return "DEBUG";
    case LOG_INFO:  return "INFO";
    case LOG_WARN:  return "WARN";
    case LOG_ERROR: return "ERROR";
    default:        return "UNKNOWN";
    }
}

void log_set_sink(log_sink_fn sink, void *user) {
    pthread_mutex_lock(&s_log_lock);
    s_log_sink = sink;
    s_log_sink_user = user;
    pthread_mutex_unlock(&s_log_lock);
}

void log_set_level(int level) {
    g_log_level = level;
}

void log_write(int level, const char *module, const char *fmt, ...) {
    if (level < g_log_level) return;

    char message[1024];
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(message, sizeof(message), fmt, ap);
    va_end(ap);

    char timestamp[16];
    log_timestamp(timestamp, sizeof(timestamp));
    char line[1200];
    snprintf(line, sizeof(line), "%s %-5s [%-7s] %s",
             timestamp, _level_name(level), module ? module : "device", message);

    pthread_mutex_lock(&s_log_lock);
    log_sink_fn sink = s_log_sink;
    void *sink_user = s_log_sink_user;
    if (!sink) {
        FILE *out = level >= LOG_WARN ? stderr : stdout;
        fputs(line, out);
        fputc('\n', out);
        fflush(out);
    }
    pthread_mutex_unlock(&s_log_lock);

    /* Custom sinks may themselves emit diagnostics; do not invoke one while
     * holding the logger mutex, otherwise a re-entrant log would deadlock. */
    if (sink) sink(level, line, sink_user);
}
