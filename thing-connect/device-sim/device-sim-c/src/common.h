/** \file common.h
 * \brief Shared Linux/POSIX types, constants, and utility macros.
 *
 * These helpers belong to the Linux reference implementation.  A product port
 * must replace POSIX time, random, signal, thread, file, and log facilities as
 * required by its target platform.
 */

#ifndef COMMON_H
#define COMMON_H

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <signal.h>
#include <unistd.h>
#include <errno.h>
#include <pthread.h>
#include <stdarg.h>

#ifdef __cplusplus
extern "C" {
#endif

/* ── Log levels ─────────────────────────────────────────────────────────── */

enum { LOG_DEBUG = 10, LOG_INFO = 20, LOG_WARN = 30, LOG_ERROR = 40 };

extern int g_log_level;
extern volatile sig_atomic_t g_stop;

/* A Linux integrator can replace the default stdout/stderr sink with syslog or
 * another application logger. `line` has no trailing newline. */
typedef void (*log_sink_fn)(int level, const char *line, void *user);
void log_set_sink(log_sink_fn sink, void *user);
void log_set_level(int level);
void log_write(int level, const char *module, const char *fmt, ...)
    __attribute__((format(printf, 3, 4)));

/* ── Module tag (override before #include in each .c file) ────────────────── */

#ifndef LOG_MODULE
#  define LOG_MODULE "device"
#endif

/* ── Color macros (compile with -DNO_COLORS to strip ANSI escape codes) ── */

#ifdef NO_COLORS
#  define C_CYAN   ""
#  define C_GREEN  ""
#  define C_YELLOW ""
#  define C_RED    ""
#  define C_GRAY   ""
#  define C_BOLD   ""
#  define C_RESET  ""
#else
#  define C_CYAN   "\033[0;36m"
#  define C_GREEN  "\033[0;32m"
#  define C_YELLOW "\033[1;33m"
#  define C_RED    "\033[0;31m"
#  define C_GRAY   "\033[0;90m"
#  define C_BOLD   "\033[1m"
#  define C_RESET  "\033[0m"
#endif

/* Wall-clock timestamp for log lines: "HH:MM:SS.mmm". */
void log_timestamp(char *buf, size_t size);

/* ── Base log macro ─────────────────────────────────────────────────────── */

#define LOG_TAG(level, color, tag, fmt, ...) \
    do { (void)(color); log_write((level), (tag), (fmt), ##__VA_ARGS__); } while(0)

/* ── Per-level convenience macros (use LOG_MODULE as tag) ───────────────── */

#define LOG_D(fmt, ...) LOG_TAG(LOG_DEBUG, C_CYAN,  LOG_MODULE, fmt, ##__VA_ARGS__)
#define LOG_I(fmt, ...) LOG_TAG(LOG_INFO,  C_GREEN, LOG_MODULE, fmt, ##__VA_ARGS__)
#define LOG_W(fmt, ...) LOG_TAG(LOG_WARN,  C_YELLOW,LOG_MODULE, fmt, ##__VA_ARGS__)
#define LOG_E(fmt, ...) LOG_TAG(LOG_ERROR, C_RED,   LOG_MODULE, fmt, ##__VA_ARGS__)

/* SDK callback output is diagnostic: expose it only at debug level and use
 * the same timestamp/level/module layout as all other runtime logs. */
#define LOG_SDK(data, length) do { \
    if (g_log_level <= LOG_DEBUG && (data) && (length) > 0) { \
        int _n = (int)(length); \
        while (_n > 0 && (((const char *)(data))[_n - 1] == '\n' || ((const char *)(data))[_n - 1] == '\r')) _n--; \
        LOG_TAG(LOG_DEBUG, C_GRAY, "sdk", "%.*s", _n, (const char *)(data)); \
    } \
} while(0)

/* ── Visual prompt helpers (no timestamp — for user-facing UI text) ──────── */

/** Boxed prompt for important user action hints (e.g. verification code). */
#define PROMPT_BOX(fmt, ...) do { \
    printf(C_YELLOW "\n╔══════════════════════════════════════╗\n" C_RESET); \
    printf(C_YELLOW "║ " fmt "\n" C_RESET, ##__VA_ARGS__); \
    printf(C_YELLOW "╚══════════════════════════════════════╝\n\n" C_RESET); \
    fflush(stdout); \
} while(0)

/** Bold key-value line (e.g. "  device_id  = DEV000001"). */
#define PROMPT_KV(fmt, ...) do { \
    printf(C_GREEN "  " fmt C_RESET "\n", ##__VA_ARGS__); \
    fflush(stdout); \
} while(0)

/** Phase / stage section title with bold separator lines. */
#define PHASE_TITLE(fmt, ...) do { \
    printf("\n" C_BOLD "──────────────────────────────────────────────────" C_RESET "\n"); \
    printf(" " fmt "\n", ##__VA_ARGS__); \
    printf(C_BOLD "──────────────────────────────────────────────────" C_RESET "\n"); \
    fflush(stdout); \
} while(0)

/** Single command hint line (arrow bullet). */
#define CMD_HINT(fmt, ...) do { \
    printf(C_YELLOW "  ⮞ " fmt C_RESET "\n", ##__VA_ARGS__); \
    fflush(stdout); \
} while(0)

/** Section separator (thin line). */
#define SEP_LINE() do { \
    printf(C_BOLD "──────────────────────────────────────────────────" C_RESET "\n"); \
    fflush(stdout); \
} while(0)

/* ── Session state ──────────────────────────────────────────────────────── */

typedef enum {
    SESS_IDLE,
    SESS_CONNECTING,
    SESS_IN_CALL,
    SESS_DISCONNECTING
} SessionState;

static inline const char *sess_state_str(SessionState s) {
    switch (s) {
        case SESS_IDLE:       return "IDLE";
        case SESS_CONNECTING: return "CONNECTING";
        case SESS_IN_CALL:    return "IN_CALL";
        case SESS_DISCONNECTING: return "DISCONNECTING";
        default:              return "UNKNOWN";
    }
}

/* ── Audio / video constants ────────────────────────────────────────────── */

#define AUDIO_PKT_SIZE_VOIP  320   /* 40ms G.711 A-law 8kHz 8-bit mono */
#define AUDIO_PKT_MS_VOIP    40
#define AUDIO_PKT_SIZE_AI    640   /* 20ms PCM 16kHz 16bit mono */
#define AUDIO_PKT_MS_AI      20

#define STREAM_ID_AUDIO      10
#define STREAM_ID_VIDEO      11
#define STREAM_ID_AI          1

#define VIDEO_FPS            15.0
#define VIDEO_FRAME_MS       66    /* floor(1000/15) */

/* ── TiRTC command words (platform-reserved) ────────────────────────────── */

#define CMD_VOIP_ACCEPT  0x2000
#define CMD_VOIP_HANGUP  0x2001
#define CMD_AI            0x2100

/* ── G.711 silence ──────────────────────────────────────────────────────── */

#define G711A_SILENCE_BYTE 0xD5
#define PCM_SILENCE_BYTE   0x00

/* ── Utility helpers ────────────────────────────────────────────────────── */

/** Monotonic process clock in milliseconds. */
int64_t now_ms(void);

/** Sleep for at least `ms` milliseconds. */
void sleep_ms(int ms);

/* Bounded string copy for fixed-size buffers.  Unlike strncpy(), it
 * always terminates the destination and reports truncation to the caller. */
static inline int str_copy(char *dst, size_t dst_size, const char *src) {
    if (!dst || dst_size == 0) return -1;
    if (!src) { dst[0] = '\0'; return -1; }
    size_t len = strlen(src); /* public inputs are validated NUL-terminated C strings */
    if (len >= dst_size) { memcpy(dst, src, dst_size - 1); dst[dst_size - 1] = '\0'; return -1; }
    memcpy(dst, src, len + 1);
    return 0;
}

#define STR_COPY(dst, src) str_copy((dst), sizeof(dst), (src))

static inline int path_join(char *dst, size_t dst_size, const char *left, const char *right) {
    if (!dst || !dst_size || !left || !right) return -1;
    size_t left_len = strlen(left);
    size_t right_len = strlen(right);
    if (left_len + 1 + right_len >= dst_size) {
        dst[0] = '\0'; return -1;
    }
    memcpy(dst, left, left_len);
    dst[left_len] = '/';
    memcpy(dst + left_len + 1, right, right_len + 1);
    return 0;
}

/** Generate `len` cryptographically random bytes as lowercase hex.
 * `out` must hold `2 * len + 1` bytes. Returns 0 or -1 without a fallback. */
int rand_hex(char *out, int len);

/** String buffer with fixed capacity and no heap allocation. */
typedef struct {
    char *buf;
    size_t cap;
    size_t len;
} StrBuf;

static inline void sb_init(StrBuf *sb, char *buf, size_t cap) {
    sb->buf = buf;
    sb->cap = cap;
    sb->len = 0;
    if (cap > 0) buf[0] = '\0';
}

#ifdef __cplusplus
}
#endif

#endif /* COMMON_H */
