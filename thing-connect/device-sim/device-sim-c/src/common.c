/** \file common.c
 * \brief Central, thread-safe runtime logger for the C device simulator.
 */

#define LOG_MODULE "log"
#include "common.h"

static pthread_mutex_t s_log_lock = PTHREAD_MUTEX_INITIALIZER;
static log_sink_fn s_log_sink = NULL;
static void *s_log_sink_user = NULL;

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

    /* Board sinks may themselves emit diagnostics; do not invoke one while
     * holding the logger mutex, otherwise a re-entrant log would deadlock. */
    if (sink) sink(level, line, sink_user);
}
