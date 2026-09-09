#define LOG_MODULE "room"
#include "tirtc_room.h"
#include "common.h"
#include "device_adapter.h"
#include "http_tls.h"
#include "media_format.h"
#include "tirtc_runtime.h"
#include <cjson/cJSON.h>
#include <curl/curl.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define ROOM_CMD 0x2200u
#define ROOM_QUEUE 32
#define ROOM_JSON_CAP 32768

typedef struct {
    int type, error;
    uint64_t generation;
    tirtc_conn_t conn;
    char text[ROOM_JSON_CAP];
} RoomEvent;
typedef struct {
    uint64_t generation;
    tirtc_conn_t conn;
    TIRTCFRAMEINFO frame;
    unsigned char data[8192];
} RoomAudio;
struct RoomState {
    pthread_mutex_t lock, queue_lock, media_lock;
    pthread_t worker, media_worker;
    int started, media_started, closed, wake, active, ptt, joined;
    char server[512], device[65], bearer[2048], audio[512];
    char room_id[65], room_code[7], session_id[65], wire_room_id[256];
    int64_t version, deadline, next_heartbeat, lease_deadline;
    int heartbeat_seconds, lease_seconds;
    _Atomic uint64_t generation;
    uint64_t rtc_generation;
    tirtc_conn_t conn;
    const AudioFormat *up, *down;
    SessionArbiter *arbiter;
    SessionLease lease;
    RoomEvent queue[ROOM_QUEUE];
    unsigned head, count;
    RoomAudio audio_queue[ROOM_QUEUE];
    unsigned audio_head, audio_count;
    _Atomic int closing;
    cJSON *members;
    DeviceMediaSource source;
    int source_open;
};
static RoomState *s_room;
static TIRTCCALLBACKS s_callbacks;
static SdkCallbackGuard s_guard = SDK_CALLBACK_GUARD_INITIALIZER;

static const char *string_field(const cJSON *root, const char *key) {
    const cJSON *item = cJSON_GetObjectItemCaseSensitive(root, key);
    return cJSON_IsString(item) ? item->valuestring : "";
}
static int64_t number_field(const cJSON *root, const char *key) {
    const cJSON *item = cJSON_GetObjectItemCaseSensitive(root, key);
    return cJSON_IsNumber(item) ? (int64_t)item->valuedouble : 0;
}
static int random_id(char *out) {
    unsigned char bytes[16];
    if (device_security_random_bytes(bytes, sizeof(bytes)) != 0)
        return -1;
    for (size_t i = 0; i < sizeof(bytes); i++)
        snprintf(out + i * 2, 3, "%02x", bytes[i]);
    return 0;
}
static int enqueue(RoomState *r, int type, tirtc_conn_t conn, int error,
                   uint64_t generation, const char *text) {
    pthread_mutex_lock(&r->queue_lock);
    if (r->closing || r->count == ROOM_QUEUE) {
        pthread_mutex_unlock(&r->queue_lock);
        return -1;
    }
    RoomEvent *event = &r->queue[(r->head + r->count) % ROOM_QUEUE];
    *event =
        (RoomEvent){.type = type, .conn = conn, .error = error, .generation = generation};
    if (text)
        str_copy(event->text, sizeof(event->text), text);
    r->count++;
    pthread_mutex_unlock(&r->queue_lock);
    return 0;
}
static size_t write_response(void *ptr, size_t size, size_t n, void *user) {
    StrBuf *b = user;
    size_t bytes = size * n;
    if (bytes >= b->cap - b->len)
        return 0;
    memcpy(b->buf + b->len, ptr, bytes);
    b->len += bytes;
    b->buf[b->len] = 0;
    return bytes;
}
static cJSON *request(RoomState *r, const char *path, cJSON *body) {
    CURL *curl = curl_easy_init();
    if (!curl)
        return NULL;
    char url[768], auth[2200], buffer[16384];
    snprintf(url, sizeof(url), "%s/v1/call/group/device/%s", r->server, path);
    snprintf(auth, sizeof(auth), "Authorization: Bearer %s", r->bearer);
    struct curl_slist *headers = curl_slist_append(NULL, auth);
    headers = curl_slist_append(headers, "Content-Type: application/json");
    StrBuf response;
    sb_init(&response, buffer, sizeof(buffer));
    char *raw = body ? cJSON_PrintUnformatted(body) : NULL;
    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 5L);
    curl_easy_setopt(curl, CURLOPT_CONNECTTIMEOUT, 3L);
    curl_easy_setopt(curl, CURLOPT_NOSIGNAL, 1L);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, write_response);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &response);
    http_tls_apply(curl);
    if (body) {
        if (!raw) {
            curl_slist_free_all(headers);
            curl_easy_cleanup(curl);
            return NULL;
        }
        curl_easy_setopt(curl, CURLOPT_POSTFIELDS, raw);
    }
    CURLcode rc = curl_easy_perform(curl);
    long status = 0;
    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &status);
    free(raw);
    curl_slist_free_all(headers);
    curl_easy_cleanup(curl);
    if (rc != CURLE_OK || status != 200)
        return NULL;
    cJSON *root = cJSON_Parse(buffer);
    int code = (int)number_field(root, "code");
    if (code != 200) {
        LOG_W("房间请求失败 code=%d", code);
        cJSON_Delete(root);
        return NULL;
    }
    cJSON *data = cJSON_DetachItemFromObjectCaseSensitive(root, "data");
    cJSON_Delete(root);
    return data ? data : cJSON_CreateObject();
}
static int send_json(tirtc_conn_t conn, cJSON *message) {
    int rc = -1;
    char *raw = cJSON_PrintUnformatted(message);
    if (raw) {
        rc = TiRtcSendCommand(conn, ROOM_CMD, raw, (uint32_t)strlen(raw));
        free(raw);
    }
    cJSON_Delete(message);
    return rc;
}
static cJSON *rpc(const char *method) {
    cJSON *root = cJSON_CreateObject();
    cJSON_AddStringToObject(root, "jsonrpc", "2.0");
    cJSON_AddStringToObject(root, "method", method);
    return root;
}
static cJSON *audio_descriptor(const AudioFormat *format) {
    cJSON *o = cJSON_CreateObject();
    cJSON_AddStringToObject(o, "codec",
                            strcmp(format->codec, "alaw") == 0 ? "g711a" : format->codec);
    cJSON_AddNumberToObject(o, "sample_rate", format->sample_rate);
    cJSON_AddNumberToObject(o, "channels", 1);
    return o;
}
static int matches_format(const cJSON *o, const AudioFormat *f) {
    const char *codec = strcmp(f->codec, "alaw") == 0 ? "g711a" : f->codec;
    return strcmp(string_field(o, "codec"), codec) == 0 &&
           number_field(o, "sample_rate") == f->sample_rate &&
           number_field(o, "channels") == 1;
}
static cJSON *presence_locked(RoomState *r, const char *state) {
    cJSON *body = cJSON_CreateObject();
    cJSON_AddStringToObject(body, "room_id", r->room_id);
    cJSON_AddNumberToObject(body, "assignment_version", (double)r->version);
    cJSON_AddStringToObject(body, "session_id", r->session_id);
    cJSON_AddStringToObject(body, "state", state);
    return body;
}
static void report(RoomState *r, cJSON *body) {
    cJSON *data = request(r, "presence", body);
    cJSON_Delete(data);
    cJSON_Delete(body);
}
static int start_service(void *ctx) {
    RoomState *r = ctx;
    pthread_mutex_lock(&r->lock);
    r->rtc_generation = tirtc_runtime_activate(TIRTC_SERVICE_ROOM);
    r->active = r->rtc_generation != 0;
    int ok = r->active;
    pthread_mutex_unlock(&r->lock);
    return ok ? 0 : -1;
}
static void stop_service(void *ctx) {
    RoomState *r = ctx;
    pthread_mutex_lock(&r->lock);
    r->generation++;
    r->active = 0;
    r->ptt = 0;
    r->joined = 0;
    r->wire_room_id[0] = 0;
    tirtc_conn_t conn = r->conn;
    r->conn = NULL;
    uint64_t rtc_generation = r->rtc_generation;
    r->rtc_generation = 0;
    cJSON_Delete(r->members);
    r->members = NULL;
    cJSON *p = presence_locked(r, "suspended");
    char *raw = cJSON_PrintUnformatted(p);
    cJSON_Delete(p);
    r->wake = 1;
    pthread_mutex_unlock(&r->lock);
    if (conn) {
        send_json(conn, rpc("leave_room"));
        TiRtcDisconnect(conn);
    }
    if (rtc_generation)
        tirtc_runtime_deactivate(TIRTC_SERVICE_ROOM, rtc_generation);
    pthread_mutex_lock(&r->media_lock);
    if (r->source_open) {
        device_media_source_close(&r->source);
        r->source_open = 0;
    }
    device_media_sink_flush(DEVICE_BUSINESS_ROOM,
                            device_adapter_session_generation(DEVICE_BUSINESS_ROOM));
    pthread_mutex_unlock(&r->media_lock);
    if (raw) {
        enqueue(r, 4, NULL, 0, 0, raw);
        free(raw);
    }
}
static void finish(RoomState *r) {
    pthread_mutex_lock(&r->lock);
    SessionLease lease = r->lease;
    pthread_mutex_unlock(&r->lock);
    if (lease.generation)
        session_arbiter_finish_lease(r->arbiter, &lease);
}
static void store_lease(void *ctx, const SessionLease *lease) {
    RoomState *r = ctx;
    pthread_mutex_lock(&r->lock);
    r->lease = *lease;
    pthread_mutex_unlock(&r->lock);
}
static void stale_disconnect(void *context) { TiRtcDisconnect(context); }
static void connected(int error, tirtc_conn_t conn, void *ctx) {
    sdk_callback_enter(&s_guard);
    RoomState *r = s_room;
    if ((!r || enqueue(r, 1, conn, error, (uint64_t)(uintptr_t)ctx, NULL) != 0) && conn)
        sdk_defer_action(&s_guard, stale_disconnect, conn);
    sdk_callback_leave(&s_guard);
}
static void disconnected(tirtc_conn_t conn) {
    RoomState *r = s_room;
    if (!r)
        return;
    uint64_t gen = r->generation;
    enqueue(r, 2, conn, 0, gen, NULL);
}
static void conn_error(tirtc_conn_t conn, int error) {
    (void)error;
    disconnected(conn);
}
static void command_cb(tirtc_conn_t conn, uint32_t command, const void *data,
                       uint32_t length) {
    RoomState *r = s_room;
    if (!r || (command & 0xfffeu) != ROOM_CMD || !data || !length || length >= ROOM_JSON_CAP)
        return;
    char text[ROOM_JSON_CAP];
    memcpy(text, data, length);
    text[length] = 0;
    uint64_t gen = r->generation;
    enqueue(r, 3, conn, 0, gen, text);
}
static void audio_cb(tirtc_conn_t conn, const TIRTCFRAMEINFO *frame, void *data) {
    RoomState *r = s_room;
    if (!r || !frame || !data || !frame->length || frame->length > 8192 ||
        frame->stream_id != 1 || frame->media != r->down->media ||
        frame->flags != r->down->flags)
        return;
    pthread_mutex_lock(&r->queue_lock);
    if (!r->closing && r->audio_count < ROOM_QUEUE) {
        RoomAudio *audio = &r->audio_queue[(r->audio_head + r->audio_count) % ROOM_QUEUE];
        audio->generation = r->generation;
        audio->conn = conn;
        audio->frame = *frame;
        memcpy(audio->data, data, frame->length);
        r->audio_count++;
    }
    pthread_mutex_unlock(&r->queue_lock);
}
static int subscribe_audio(tirtc_conn_t conn, uint8_t stream) {
    (void)conn;
    return stream == 1 ? 0 : -1;
}
static void request_members_locked(RoomState *r) {
    if (!r->joined || !r->conn)
        return;
    int rc = send_json(r->conn, rpc("get_room_snapshot"));
    if (rc > 0)
        LOG_I("正在查询房间成员…");
    else
        LOG_W("成员查询发送失败 code=%d，请输入 room status 重试", rc);
}
static void print_status_locked(RoomState *r) {
    LOG_I("%s | 房间号：%s | 房间 ID：%s", r->joined ? "已加入" : "未连接",
          r->room_code[0] ? r->room_code : "—", r->room_id[0] ? r->room_id : "—");
    if (!r->joined || !r->members) {
        LOG_I("在线成员：等待房间同步");
        return;
    }
    LOG_I("在线成员（%d）：", cJSON_GetArraySize(r->members));
    for (int i = 0; i < cJSON_GetArraySize(r->members); i++) {
        const cJSON *member = cJSON_GetArrayItem(r->members, i);
        const char *device = string_field(member, "device_id");
        const char *id = device[0] ? device : string_field(member, "participant_id");
        LOG_I("  %d. %.128s%s · %s", i + 1, id,
              device[0] && strcmp(device, r->device) == 0 ? "（本机）" : "",
              strcmp(string_field(member, "mic_state"), "speaking") == 0 ? "正在说话" : "收听中");
    }
}
static int matches_room(RoomState *r, const char *id) {
    if (!id[0] || strcmp(id, r->room_id) == 0)
        return 1;
    if (r->wire_room_id[0])
        return strcmp(id, r->wire_room_id) == 0;
    const char *separator = strchr(id, ':');
    if (separator && separator != id && strlen(id) < sizeof(r->wire_room_id) &&
        strcmp(separator + 1, r->room_id) == 0) {
        str_copy(r->wire_room_id, sizeof(r->wire_room_id), id);
        return 1;
    }
    return 0;
}

static int signal_locked(RoomState *r, const cJSON *msg) {
    if (strcmp(string_field(msg, "jsonrpc"), "2.0") != 0)
        return 0;
    if (number_field(msg, "id") == 1 && !r->joined) {
        const cJSON *result = cJSON_GetObjectItemCaseSensitive(msg, "result");
        if (!string_field(result, "session_id")[0] ||
            !matches_format(cJSON_GetObjectItemCaseSensitive(result, "input_audio"),
                            r->up) ||
            !matches_format(cJSON_GetObjectItemCaseSensitive(result, "output_audio"),
                            r->down))
            return -1;
        r->joined = 1;
        print_status_locked(r);
        r->ptt = 0;
        r->lease_deadline = now_ms() + r->lease_seconds * 1000;
        r->next_heartbeat = 0;
        cJSON *off = rpc("set_mic_state"),
              *params = cJSON_AddObjectToObject(off, "params");
        cJSON_AddStringToObject(params, "mic_state", "off");
        send_json(r->conn, off);
        request_members_locked(r);
    }
    const char *method = string_field(msg, "method");
    const cJSON *params = cJSON_GetObjectItemCaseSensitive(msg, "params");
    const char *room_id = string_field(params, "room_id");
    if (!matches_room(r, room_id))
        return 0;
    if (strcmp(method, "room_closed") == 0)
        return -1;
    if (strcmp(method, "room_snapshot") == 0) {
        const cJSON *members = cJSON_GetObjectItemCaseSensitive(params, "participants");
        if (!cJSON_IsArray(members) || cJSON_GetArraySize(members) > 100)
            return -1;
        cJSON_Delete(r->members);
        r->members = cJSON_Duplicate(members, 1);
        print_status_locked(r);
    } else if (strcmp(method, "participant_joined") == 0) {
        const cJSON *p = cJSON_GetObjectItemCaseSensitive(params, "participant");
        if (r->members && cJSON_IsObject(p) && string_field(p, "participant_id")[0]) {
            int found = 0;
            for (int i = 0; i < cJSON_GetArraySize(r->members); i++) {
                if (strcmp(
                        string_field(cJSON_GetArrayItem(r->members, i), "participant_id"),
                        string_field(p, "participant_id")) == 0) {
                    cJSON_ReplaceItemInArray(r->members, i, cJSON_Duplicate(p, 1));
                    found = 1;
                    break;
                }
            }
            if (!found && cJSON_GetArraySize(r->members) < 100) {
                cJSON_AddItemToArray(r->members, cJSON_Duplicate(p, 1));
                print_status_locked(r);
            }
        }
    } else if (r->members && (strcmp(method, "participant_left") == 0 ||
                              strcmp(method, "participant_mic_state_changed") == 0)) {
        for (int i = cJSON_GetArraySize(r->members) - 1; i >= 0; i--) {
            cJSON *p = cJSON_GetArrayItem(r->members, i);
            if (strcmp(string_field(p, "participant_id"),
                       string_field(params, "participant_id")) == 0) {
                if (strcmp(method, "participant_left") == 0) {
                    cJSON_DeleteItemFromArray(r->members, i);
                    print_status_locked(r);
                }
                else
                    cJSON_ReplaceItemInObjectCaseSensitive(
                        p, "mic_state",
                        cJSON_CreateString(string_field(params, "mic_state")));
            }
        }
    }
    return 0;
}
static int process_event(RoomState *r, RoomEvent *event) {
    if (event->type == 4) {
        cJSON *p = cJSON_Parse(event->text);
        if (p)
            report(r, p);
        return 0;
    }
    if (event->type == 5) {
        char kind[16] = "", code[16] = "", password[16] = "";
        sscanf(event->text, "room %15s %15s %15s", kind, code, password);
        cJSON *body = cJSON_CreateObject();
        if (strcmp(kind, "create") == 0)
            cJSON_AddStringToObject(body, "password", code);
        else if (strcmp(kind, "join") == 0) {
            cJSON_AddStringToObject(body, "room_code", code);
            cJSON_AddStringToObject(body, "password", password);
        } else if (strcmp(kind, "leave") != 0) {
            cJSON_Delete(body);
            return 0;
        }
        cJSON *data = request(r, kind, body);
        cJSON_Delete(body);
        cJSON_Delete(data);
        room_sync(r);
        return 0;
    }
    pthread_mutex_lock(&r->lock);
    if (event->generation != r->generation ||
        (event->type != 1 && event->conn != r->conn)) {
        pthread_mutex_unlock(&r->lock);
        if (event->type == 1 && event->conn)
            TiRtcDisconnect(event->conn);
        return 0;
    }
    int rc = 0;
    if (event->type == 1) {
        if (event->error || !event->conn || !r->active) {
            rc = -1;
            if (event->conn)
                TiRtcDisconnect(event->conn);
        } else if (tirtc_runtime_bind_active_connection(TIRTC_SERVICE_ROOM,
                                                        event->conn) != 0) {
            TiRtcDisconnect(event->conn);
            rc = -1;
        } else {
            r->conn = event->conn;
            r->deadline = now_ms() + 8000;
            cJSON *join = rpc("join_room");
            cJSON_AddNumberToObject(join, "id", 1);
            cJSON *p = cJSON_AddObjectToObject(join, "params");
            cJSON_AddStringToObject(p, "room_id", r->room_id);
            cJSON_AddStringToObject(p, "device_id", r->device);
            cJSON_AddItemToObject(p, "input_audio", audio_descriptor(r->up));
            cJSON_AddItemToObject(p, "output_audio", audio_descriptor(r->down));
            send_json(r->conn, join);
        }
    } else if (event->type == 2)
        rc = -1;
    else if (event->type == 3) {
        cJSON *msg = cJSON_Parse(event->text);
        if (msg)
            rc = signal_locked(r, msg);
        cJSON_Delete(msg);
    }
    pthread_mutex_unlock(&r->lock);
    return rc;
}
typedef struct {
    RoomState *room;
    const char *peer, *token;
} RoomConnect;
static int connect_action(void *ctx) {
    RoomConnect *connect = ctx;
    RoomState *r = connect->room;
    pthread_mutex_lock(&r->lock);
    uint64_t gen = ++r->generation;
    r->deadline = now_ms() + 10000;
    pthread_mutex_unlock(&r->lock);
    return TiRtcWhipConnect(connect->peer, connect->token, connected,
                            (void *)(uintptr_t)gen);
}
static int reconcile(RoomState *r) {
    cJSON *a = request(r, "assignment", NULL);
    if (!a)
        return -1;
    const char *id = string_field(a, "room_id"), *code = string_field(a, "room_code");
    int64_t version = number_field(a, "assignment_version");
    int desired = strcmp(string_field(a, "desired_state"), "joined") == 0;
    if (strlen(id) > 64 || strlen(code) > 6 || version < 0) {
        cJSON_Delete(a);
        return -1;
    }
    pthread_mutex_lock(&r->lock);
    int changed = version != r->version;
    pthread_mutex_unlock(&r->lock);
    if (changed || !desired)
        finish(r);
    if (changed) {
        pthread_mutex_lock(&r->lock);
        r->session_id[0] = 0;
        pthread_mutex_unlock(&r->lock);
    }
    pthread_mutex_lock(&r->lock);
    str_copy(r->room_id, sizeof(r->room_id), id);
    str_copy(r->room_code, sizeof(r->room_code), code);
    r->version = version;
    int active = r->active;
    pthread_mutex_unlock(&r->lock);
    cJSON_Delete(a);
    if (!desired || active)
        return 0;
    if (!audio_format_ai_codec(r->up) || !audio_format_ai_codec(r->down))
        return -1;
    SessionKind current = session_arbiter_current(r->arbiter);
    pthread_mutex_lock(&r->lock);
    if ((current == SESSION_NONE || !r->session_id[0]) && random_id(r->session_id) != 0) {
        pthread_mutex_unlock(&r->lock);
        return -1;
    }
    cJSON *p = presence_locked(r, current == SESSION_NONE ? "connecting" : "suspended");
    pthread_mutex_unlock(&r->lock);
    if (current != SESSION_NONE) {
        report(r, p);
        return 0;
    }
    cJSON *token = request(r, "connect-token", p);
    cJSON_Delete(p);
    if (!token)
        return -1;
    const char *peer = string_field(token, "peer_id"),
               *secret = string_field(token, "token");
    if (!peer[0] || strlen(peer) > 255 || !secret[0] || strlen(secret) > 8192) {
        cJSON_Delete(token);
        return -1;
    }
    if (session_arbiter_begin_id_ex(r->arbiter, SESSION_ROOM, 0, r->session_id, NULL,
                                    store_lease, r) != 0) {
        cJSON_Delete(token);
        pthread_mutex_lock(&r->lock);
        p = presence_locked(r, "connect_failed");
        pthread_mutex_unlock(&r->lock);
        report(r, p);
        return -1;
    }
    pthread_mutex_lock(&r->lock);
    r->generation++;
    r->deadline = now_ms() + 10000;
    r->heartbeat_seconds = (int)number_field(token, "heartbeat_seconds");
    r->lease_seconds = (int)number_field(token, "lease_seconds");
    if (r->heartbeat_seconds < 1)
        r->heartbeat_seconds = 15;
    if (r->lease_seconds < 3)
        r->lease_seconds = 45;
    SessionLease lease = r->lease;
    pthread_mutex_unlock(&r->lock);
    RoomConnect connect = {r, peer, secret};
    int rc = session_arbiter_run_action(r->arbiter, &lease, connect_action, &connect);
    cJSON_Delete(token);
    return rc == 0 ? 0 : -1;
}
static void *media_worker(void *ctx) {
    RoomState *r = ctx;
    while (!r->closing) {
        int delay = 10;
        RoomAudio audio;
        pthread_mutex_lock(&r->queue_lock);
        int have_audio = r->audio_count > 0;
        if (have_audio) {
            audio = r->audio_queue[r->audio_head];
            r->audio_head = (r->audio_head + 1) % ROOM_QUEUE;
            r->audio_count--;
        }
        pthread_mutex_unlock(&r->queue_lock);
        pthread_mutex_lock(&r->media_lock);
        pthread_mutex_lock(&r->lock);
        int current = have_audio && audio.generation == r->generation &&
                      audio.conn == r->conn && r->joined;
        int speaking = r->active && r->joined && r->ptt;
        uint64_t generation = r->generation;
        pthread_mutex_unlock(&r->lock);
        if (current)
            device_media_sink_submit(DEVICE_BUSINESS_ROOM, 0, audio.frame.stream_id,
                                     audio.frame.media, audio.frame.flags, audio.frame.ts,
                                     audio.data, audio.frame.length);
        if (speaking) {
            if (!r->source_open) {
                DeviceMediaSourceConfig cfg = {.audio_locator = r->audio,
                                               .audio_format = r->up->name,
                                               .audio_packet_ms = 20,
                                               .business = DEVICE_BUSINESS_ROOM};
                r->source_open = device_media_source_open(&r->source, &cfg) == 0;
            }
            if (r->source_open) {
                DeviceMediaPacket packet;
                int available = device_media_source_next_audio(&r->source, &packet);
                pthread_mutex_lock(&r->lock);
                if (available > 0 && packet.length <= 8192 &&
                    generation == r->generation && r->active && r->joined && r->ptt) {
                    TIRTCFRAMEINFO frame = {0};
                    frame.stream_id = 1;
                    frame.media = r->up->media;
                    frame.flags = r->up->flags;
                    frame.ts = (uint32_t)now_ms();
                    frame.length = (uint32_t)packet.length;
                    int sent = TiRtcSendAudioStream(r->conn, &frame, packet.data);
                    if (sent == TIRTC_E_CONN_TIMEOUTCLOSE ||
                        sent == TIRTC_E_CONN_REMOTECLOSE ||
                        sent == TIRTC_E_CONN_OTHER_ERROR) {
                        r->ptt = 0;
                        enqueue(r, 2, r->conn, sent, generation, NULL);
                    }
                    delay = (int)packet.duration_ms;
                    if (delay < 1 || delay > 100)
                        delay = 20;
                }
                pthread_mutex_unlock(&r->lock);
            }
        } else if (r->source_open) {
            device_media_source_close(&r->source);
            r->source_open = 0;
        }
        pthread_mutex_unlock(&r->media_lock);
        sleep_ms(delay);
    }
    return NULL;
}
static void *control_worker(void *ctx) {
    RoomState *r = ctx;
    int64_t next_sync = 0;
    int retry = 1;
    RoomEvent storage;
    RoomEvent *event = &storage;
    for (;;) {
        pthread_mutex_lock(&r->lock);
        int closed = r->closed, wake = r->wake;
        r->wake = 0;
        pthread_mutex_lock(&r->queue_lock);
        int has_event = r->count > 0;
        if (has_event) {
            *event = r->queue[r->head];
            r->head = (r->head + 1) % ROOM_QUEUE;
            r->count--;
        }
        pthread_mutex_unlock(&r->queue_lock);
        int64_t now = now_ms();
        int expired = r->active && ((!r->joined && now >= r->deadline) ||
                                    (r->joined && now >= r->lease_deadline));
        int heartbeat = r->joined && now >= r->next_heartbeat;
        cJSON *p = heartbeat ? presence_locked(r, "joined") : NULL;
        pthread_mutex_unlock(&r->lock);
        if (closed) {
            cJSON_Delete(p);
            break;
        }
        if (has_event)
            sdk_callback_wait_idle(&s_guard);
        int rc = has_event ? process_event(r, event) : 0;
        if (expired)
            rc = -1;
        if (p) {
            cJSON *data = request(r, "presence", p);
            cJSON_Delete(p);
            if (!data)
                rc = -1;
            else {
                pthread_mutex_lock(&r->lock);
                r->next_heartbeat = now_ms() + r->heartbeat_seconds * 1000;
                r->lease_deadline = now_ms() + r->lease_seconds * 1000;
                pthread_mutex_unlock(&r->lock);
            }
            cJSON_Delete(data);
        }
        if (rc == 0 && (wake || now >= next_sync)) {
            rc = reconcile(r);
            next_sync = now_ms() + 3000;
        }
        if (rc != 0) {
            finish(r);
            LOG_W("房间连接待恢复");
            next_sync = now_ms() + retry * 1000;
            if (retry < 30)
                retry *= 2;
            for (int i = 0; i < retry * 10; i++) {
                pthread_mutex_lock(&r->lock);
                closed = r->closed;
                pthread_mutex_unlock(&r->lock);
                if (closed)
                    break;
                sleep_ms(100);
            }
        } else {
            pthread_mutex_lock(&r->lock);
            if (r->joined)
                retry = 1;
            pthread_mutex_unlock(&r->lock);
        }
        sleep_ms(10);
    }
    return NULL;
}
RoomState *room_create(const char *server, const char *device, const char *bearer,
                       const char *audio, const char *up, const char *down) {
    if (!server || strlen(server) >= 512 || !device || strlen(device) > 64 || !bearer ||
        strlen(bearer) >= 2048 || !audio || strlen(audio) >= 512)
        return NULL;
    RoomState *r = calloc(1, sizeof(*r));
    if (!r)
        return NULL;
    r->up = audio_format_find(up);
    r->down = audio_format_find(down);
    if (!r->up || !r->down) {
        free(r);
        return NULL;
    }
    pthread_mutex_init(&r->lock, NULL);
    pthread_mutex_init(&r->queue_lock, NULL);
    pthread_mutex_init(&r->media_lock, NULL);
    str_copy(r->server, sizeof(r->server), server);
    str_copy(r->device, sizeof(r->device), device);
    str_copy(r->bearer, sizeof(r->bearer), bearer);
    str_copy(r->audio, sizeof(r->audio), audio);
    return r;
}
int room_register(RoomState *r) {
    s_room = r;
    memset(&s_callbacks, 0, sizeof(s_callbacks));
    s_callbacks.on_conn_error = conn_error;
    s_callbacks.on_disconnected = disconnected;
    s_callbacks.on_command = command_cb;
    s_callbacks.on_audio = audio_cb;
    s_callbacks.on_subscribe_audio = subscribe_audio;
    return tirtc_runtime_register_service(TIRTC_SERVICE_ROOM, &s_callbacks, &s_guard);
}
int room_start(RoomState *r, SessionArbiter *arbiter, SessionCoordinator *coordinator) {
    r->arbiter = arbiter;
    coordinator->adapters[SESSION_ROOM] =
        (SessionAdapter){start_service, stop_service, r};
    if (pthread_create(&r->worker, NULL, control_worker, r) != 0)
        return -1;
    r->started = 1;
    if (pthread_create(&r->media_worker, NULL, media_worker, r) != 0) {
        room_shutdown(r);
        return -1;
    }
    r->media_started = 1;
    return 0;
}
void room_sync(RoomState *r) {
    if (!r)
        return;
    pthread_mutex_lock(&r->lock);
    r->wake = 1;
    pthread_mutex_unlock(&r->lock);
}
void room_ptt(RoomState *r, int pressed) {
    if (!r)
        return;
    pthread_mutex_lock(&r->lock);
    r->ptt = pressed && r->joined;
    if (r->conn) {
        cJSON *msg = rpc("set_mic_state");
        cJSON *p = cJSON_AddObjectToObject(msg, "params");
        cJSON_AddStringToObject(p, "mic_state", r->ptt ? "speaking" : "off");
        send_json(r->conn, msg);
    }
    pthread_mutex_unlock(&r->lock);
}
int room_command(RoomState *r, const char *line) {
    if (strncmp(line, "room ", 5) != 0)
        return 0;
    if (strcmp(line, "room ptt down") == 0)
        room_ptt(r, 1);
    else if (strcmp(line, "room ptt up") == 0)
        room_ptt(r, 0);
    else if (strcmp(line, "room status") == 0) {
        pthread_mutex_lock(&r->lock);
        print_status_locked(r);
        request_members_locked(r);
        pthread_mutex_unlock(&r->lock);
    } else
        enqueue(r, 5, NULL, 0, 0, line);
    return 1;
}
void room_shutdown(RoomState *r) {
    if (!r)
        return;
    pthread_mutex_lock(&r->lock);
    r->closed = 1;
    r->closing = 1;
    r->ptt = 0;
    pthread_mutex_unlock(&r->lock);
    if (r->started) {
        pthread_join(r->worker, NULL);
        r->started = 0;
    }
    if (r->media_started) {
        pthread_join(r->media_worker, NULL);
        r->media_started = 0;
    }
    if (r->arbiter)
        finish(r);
    /* The control worker is stopped; release directly outside adapter locks. */
    pthread_mutex_lock(&r->lock);
    cJSON *final_presence = r->session_id[0] && r->room_id[0] && r->version > 0
                                ? presence_locked(r, "suspended") : NULL;
    pthread_mutex_unlock(&r->lock);
    if (final_presence)
        report(r, final_presence);
}
void room_destroy(RoomState *r) {
    if (!r)
        return;
    room_shutdown(r);
    if (s_room == r)
        s_room = NULL;
    cJSON_Delete(r->members);
    pthread_mutex_destroy(&r->lock);
    pthread_mutex_destroy(&r->queue_lock);
    pthread_mutex_destroy(&r->media_lock);
    memset(r, 0, sizeof(*r));
    free(r);
}
