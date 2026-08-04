/** \file main.c
 * \brief RTC Device Simulator — CLI entry point.
 *
 * One process-wide TiRTC SDK instance serves streaming, VoIP, AI and device
 * calls.  The coordinator switches business sessions without restarting the
 * SDK, and restores streaming after each foreground session.
 *
 * Usage:
 *   ./device-sim [OPTIONS]                          # default: stream mode
 *   ./device-sim                                    # unified runtime
 *   ./device-sim --mac AA:BB:CC:DD:EE:FF            # unbound flow
 */

#include <getopt.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <pthread.h>
#include <unistd.h>

#include <curl/curl.h>
#include <cjson/cJSON.h>

#include "common.h"
#include "device_adapter.h"
#include "device_reference.h"
#include "device_flow.h"
#include "http_tls.h"
#include "linux_device_adapter.h"
#include "media_format.h"
#include "tirtc_stream.h"
#include "tirtc_voip.h"
#include "tirtc_ai.h"
#include "tirtc_call.h"
#include "tirtc_runtime.h"
#include "call_session.h"
#include "session_arbiter.h"
#include "session_coordinator.h"

#define DEFAULT_AUDIO_PATH "../assets/audio.g711a"
#define EXPERIENCE_PLATFORM_URL "https://demo-open.tange-ai.com"

/* ── Signal handler ──────────────────────────────────────────────────────── */

static void _on_sigint(int sig) {
    (void)sig;
    g_stop = 1;
}

/* ── Default (dummy) MQTT handler for stream mode ────────────────────────── */

typedef struct {
    SessionCoordinator coordinator;
    SessionArbiter arbiter;
    const char *device_id;
    const char *secret_key;
    const char *client_id;
    const char *endpoint;
    const char *voip_server;
    const char *ai_server;
    const char *mqtt_token;
    const char *ai_audio;
    VoipState *voip;
    AiState *ai;
    CallState *call;
    const char *up_audio_format;
    const char *up_video_format;
    char video[512];
    char audio[512];
    int pending_selection; /* 0=none, 1=wxcall, 2=device/mixed contact */
    char pending_call_type[16];
    pthread_mutex_t lease_lock;
    SessionLease leases[SESSION_CALL + 1];
    uint64_t rtc_generations[SESSION_CALL + 1];
} DeviceRuntime;

static int _activate_runtime(DeviceRuntime *rt, SessionKind kind,
                             TirtcService service) {
    uint64_t generation = tirtc_runtime_activate(service);
    if (!generation) {
        LOG_E("无法激活 TiRTC 业务 service=%s",
              tirtc_runtime_service_name(service));
        return -1;
    }
    rt->rtc_generations[kind] = generation;
    return 0;
}

static void _deactivate_runtime(DeviceRuntime *rt, SessionKind kind,
                                TirtcService service) {
    uint64_t generation = rt->rtc_generations[kind];
    rt->rtc_generations[kind] = 0;
    if (generation)
        (void)tirtc_runtime_deactivate(service, generation);
}

static int _start_stream(void *ctx) {
    DeviceRuntime *rt = ctx;
    if (_activate_runtime(rt, SESSION_STREAM, TIRTC_SERVICE_STREAM) != 0)
        return -1;
    int rc = stream_service_start(rt->video, rt->audio,
                                  rt->up_audio_format,
                                  rt->up_video_format);
    if (rc != 0)
        _deactivate_runtime(rt, SESSION_STREAM, TIRTC_SERVICE_STREAM);
    return rc;
}
static void _stop_stream(void *ctx) {
    DeviceRuntime *rt = ctx;
    _deactivate_runtime(rt, SESSION_STREAM, TIRTC_SERVICE_STREAM);
    stream_service_stop();
}
static int _start_voip(void *ctx) {
    DeviceRuntime *rt = ctx;
    if (_activate_runtime(rt, SESSION_VOIP, TIRTC_SERVICE_VOIP) != 0)
        return -1;
    int rc = voip_service_start(rt->voip);
    if (rc != 0)
        _deactivate_runtime(rt, SESSION_VOIP, TIRTC_SERVICE_VOIP);
    return rc;
}
static void _stop_voip(void *ctx) {
    DeviceRuntime *rt = ctx;
    _deactivate_runtime(rt, SESSION_VOIP, TIRTC_SERVICE_VOIP);
    voip_service_stop(rt->voip);
}
static int _start_ai(void *ctx) {
    DeviceRuntime *rt = ctx;
    if (_activate_runtime(rt, SESSION_AI, TIRTC_SERVICE_AI) != 0)
        return -1;
    int rc = ai_service_start(rt->ai);
    if (rc != 0)
        _deactivate_runtime(rt, SESSION_AI, TIRTC_SERVICE_AI);
    return rc;
}
static void _stop_ai(void *ctx) {
    DeviceRuntime *rt = ctx;
    _deactivate_runtime(rt, SESSION_AI, TIRTC_SERVICE_AI);
    ai_service_stop(rt->ai);
}
static int _start_call(void *ctx) {
    DeviceRuntime *rt = ctx;
    if (_activate_runtime(rt, SESSION_CALL, TIRTC_SERVICE_CALL) != 0)
        return -1;
    int rc = call_service_start();
    if (rc != 0)
        _deactivate_runtime(rt, SESSION_CALL, TIRTC_SERVICE_CALL);
    return rc;
}
static void _stop_call(void *ctx) {
    DeviceRuntime *rt = ctx;
    _deactivate_runtime(rt, SESSION_CALL, TIRTC_SERVICE_CALL);
    call_service_stop();
}

static void _store_lease(DeviceRuntime *rt, const SessionLease *lease) {
    if (!rt || !lease || lease->kind <= SESSION_NONE ||
        lease->kind > SESSION_CALL) return;
    pthread_mutex_lock(&rt->lease_lock);
    rt->leases[lease->kind] = *lease;
    pthread_mutex_unlock(&rt->lease_lock);
}

static SessionLease _copy_lease(DeviceRuntime *rt, SessionKind kind) {
    SessionLease lease = {kind, 0, ""};
    pthread_mutex_lock(&rt->lease_lock);
    if (kind > SESSION_NONE && kind <= SESSION_CALL)
        lease = rt->leases[kind];
    pthread_mutex_unlock(&rt->lease_lock);
    return lease;
}

static void _publish_lease(void *ctx, const SessionLease *lease) {
    _store_lease(ctx, lease);
}

static int _begin_session(DeviceRuntime *rt, SessionKind kind,
                          int consume_pending, const char *session_id) {
    SessionLease lease;
    int rc = session_arbiter_begin_id_ex(
        &rt->arbiter, kind, consume_pending, session_id, &lease,
        _publish_lease, rt);
    if (rc == 0) _store_lease(rt, &lease);
    return rc;
}

static void _finish_kind(void *ctx, SessionKind kind) {
    DeviceRuntime *rt = ctx;
    SessionLease lease = _copy_lease(rt, kind);
    if (lease.generation)
        session_arbiter_finish_lease_async(&rt->arbiter, &lease);
    else
        session_arbiter_finish_async(&rt->arbiter, kind);
}

static void _finish_voip(void *ctx) { _finish_kind(ctx, SESSION_VOIP); }
static void _finish_ai(void *ctx) { _finish_kind(ctx, SESSION_AI); }
static int _recover_voip(void *ctx) {
    DeviceRuntime *rt = ctx;
    SessionKind current = session_arbiter_current(&rt->arbiter);
    if (current == SESSION_VOIP) return 0;
    return _begin_session(rt, SESSION_VOIP, 1, NULL);
}
static int _begin_call(void *ctx, int consume_pending) {
    DeviceRuntime *rt = ctx;
    char room_id[128] = "";
    if (consume_pending) {
        pthread_mutex_lock(&rt->call->lock);
        if (rt->call->pending_call)
            STR_COPY(room_id, rt->call->pending_room_id);
        pthread_mutex_unlock(&rt->call->lock);
        if (!room_id[0]) return -1;
    }
    return _begin_session(rt, SESSION_CALL, consume_pending, room_id);
}

static int _run_call_action(void *ctx, const char *session_id,
                            int (*action)(void *action_user),
                            void *action_user) {
    DeviceRuntime *rt = ctx;
    SessionLease lease = _copy_lease(rt, SESSION_CALL);
    if (!lease.generation ||
        (session_id && session_id[0] &&
         strcmp(lease.session_id, session_id) != 0))
        return -1;
    return session_arbiter_run_action(
        &rt->arbiter, &lease, action, action_user);
}
static void _finish_call(void *ctx) {
    DeviceRuntime *rt = ctx;
    SessionLease lease = _copy_lease(rt, SESSION_CALL);
    if (lease.generation) {
        if (call_session_has_pending(rt->call))
            session_arbiter_finish_lease_async_restore_pending(
                &rt->arbiter, &lease);
        else
            session_arbiter_finish_lease_async(&rt->arbiter, &lease);
    } else if (call_session_has_pending(rt->call)) {
        session_arbiter_finish_async_restore_pending(
            &rt->arbiter, SESSION_CALL);
    } else {
        session_arbiter_finish_async(&rt->arbiter, SESSION_CALL);
    }
}

static const char *_payload_string(const cJSON *payload, const char *key) {
    const cJSON *item =
        payload ? cJSON_GetObjectItemCaseSensitive(payload, key) : NULL;
    return cJSON_IsString(item) && item->valuestring ? item->valuestring : "";
}

static void _mqtt_voip_incoming(void *ctx, const cJSON *p) {
    DeviceRuntime *rt = ctx;
    const char *room_id = _payload_string(p, "wx_room_id");
    if (!room_id[0]) {
        LOG_W("忽略缺少 wx_room_id 的 VoIP 来电");
        (void)voip_reject_incoming_payload_async(rt->voip, p, 7);
        return;
    }
    SessionIncomingDecision decision = session_arbiter_admit_incoming_id(
        &rt->arbiter, SESSION_VOIP, room_id, 45000);
    if (decision == SESSION_INCOMING_DUPLICATE)
        return;
    if (decision == SESSION_INCOMING_CURRENT) {
        /* The callback may be the answer/ringback for our own VoIP call. */
        if (voip_matches_active_room(rt->voip, room_id))
            return;
        if (voip_is_active(rt->voip))
            voip_on_call_incoming(rt->voip, p);
        else {
            LOG_W("VoIP 正在切换生命周期，拒绝非当前回铃来电");
            (void)voip_reject_incoming_payload_async(rt->voip, p, 5);
        }
        return;
    }
    if (decision == SESSION_INCOMING_BUSY) {
        LOG_W("当前会话忙，直接拒绝微信来电");
        (void)voip_reject_incoming_payload_async(rt->voip, p, 5);
        return;
    }
    /* The arbiter may have lazily expired an old ticket milliseconds before
     * the command-loop TTL cleanup.  A fresh grant is authoritative. */
    voip_clear_pending_local(rt->voip);
    voip_on_call_incoming(rt->voip, p);
    if (!voip_has_pending(rt->voip) &&
        session_arbiter_current(&rt->arbiter) != SESSION_VOIP)
        session_arbiter_clear_pending_id(&rt->arbiter, SESSION_VOIP,
                                         room_id);
}
static void _mqtt_voip_callers(void *ctx) { DeviceRuntime *rt = ctx; voip_on_callers_update(rt->voip); }
static void _mqtt_voip_cancel(void *ctx, const cJSON *p) {
    DeviceRuntime *rt = ctx;
    const char *room_id = _payload_string(p, "wx_room_id");
    voip_on_call_cancel(rt->voip, p);
    if (!voip_has_pending(rt->voip)) {
        int cancelled = session_arbiter_cancel_id(
            &rt->arbiter, SESSION_VOIP, room_id);
        if (cancelled == 2)
            _finish_kind(rt, SESSION_VOIP);
    }
}
static void _mqtt_call_incoming(void *ctx, const cJSON *p) {
    DeviceRuntime *rt = ctx;
    const char *room_id = _payload_string(p, "room_id");
    if (!room_id[0]) {
        LOG_W("忽略缺少 room_id 的设备来电");
        return;
    }
    if (session_arbiter_offer_pending_id(
            &rt->arbiter, SESSION_CALL, room_id, 45000) != 0) {
        LOG_W("当前会话忙，直接拒绝设备来电");
        call_session_reject_incoming_payload_async(rt->call, p, "busy");
        return;
    }
    call_on_device_call_incoming(rt->call, p);
    if (!call_session_has_pending(rt->call))
        session_arbiter_clear_pending_id(&rt->arbiter, SESSION_CALL,
                                         room_id);
}
static void _mqtt_call_cancel(void *ctx, const cJSON *p) {
    DeviceRuntime *rt = ctx;
    const char *room_id = _payload_string(p, "room_id");
    call_on_device_room_cancel(rt->call, p);
    if (!call_session_has_pending(rt->call)) {
        int cancelled = session_arbiter_cancel_id(
            &rt->arbiter, SESSION_CALL, room_id);
        if (cancelled == 2)
            _finish_kind(rt, SESSION_CALL);
    }
}
static void _mqtt_call_reject(void *ctx, const cJSON *p) {
    DeviceRuntime *rt = ctx;
    const char *room_id = _payload_string(p, "room_id");
    call_on_device_call_reject(rt->call, p);
    if (!call_session_has_pending(rt->call)) {
        int cancelled = session_arbiter_cancel_id(
            &rt->arbiter, SESSION_CALL, room_id);
        if (cancelled == 2)
            _finish_kind(rt, SESSION_CALL);
    }
}
static void _mqtt_callers(void *ctx, const cJSON *p) { DeviceRuntime *rt = ctx; call_on_device_callers_update_ex(rt->call, p); }
static void _mqtt_callee_answered(void *ctx, const cJSON *p) { DeviceRuntime *rt = ctx; call_on_device_callee_answered(rt->call, p); }

static void _print_commands(void) {
    printf("[terminal] wxcall [N] [video|audio] | call [N|device_id] [video|audio] | aicall\n");
    printf("[terminal] accept/reject [reason] | cancel | hangup | ct list|pending|add|accept|reject|del|remark\n");
    printf("[terminal] room | help | exit（缩写: w/a/r/h/e）\n");
}

static int _parse_index(const char *text, int *index) {
    if (!text || !text[0]) return 0;
    char *end = NULL;
    long parsed = strtol(text, &end, 10);
    if (!end || *end || parsed < 0 || parsed > INT_MAX) return 0;
    *index = (int)parsed;
    return 1;
}

static int _valid_call_type(const char *call_type) {
    return call_type && (strcmp(call_type, "video") == 0 ||
                         strcmp(call_type, "audio") == 0);
}

static const char *_default_call_type(const DeviceRuntime *rt) {
    return rt->video[0] ? "video" : "audio";
}

static int _dial_wx_index(DeviceRuntime *rt, int index,
                          const char *call_type) {
    if (_begin_session(rt, SESSION_VOIP, 0, "") != 0)
        return -1;
    int rc = voip_dial_authorized_ex(rt->voip, index, call_type);
    if (rc != 0)
        session_arbiter_finish(&rt->arbiter, SESSION_VOIP);
    return rc;
}

static int _dial_contact(DeviceRuntime *rt, const cJSON *contact,
                         const char *call_type) {
    if (!contact) return -1;
    cJSON *type = cJSON_GetObjectItem(contact, "type");
    cJSON *openid = cJSON_GetObjectItem(contact, "wx_open_id");
    int is_voip = (cJSON_IsString(type) &&
                   strcmp(type->valuestring, "voip") == 0) ||
                  (cJSON_IsString(openid) && openid->valuestring[0]);
    if (is_voip) {
        if (_begin_session(rt, SESSION_VOIP, 0, "") != 0)
            return -1;
        int rc = voip_do_outgoing_call_ex(rt->voip, contact, call_type);
        if (rc != 0)
            session_arbiter_finish(&rt->arbiter, SESSION_VOIP);
        return rc;
    }

    cJSON *device_id = cJSON_GetObjectItem(contact, "device_id");
    if (!cJSON_IsString(device_id) || !device_id->valuestring[0]) {
        LOG_W("联系人缺少 device_id，无法呼叫");
        return -1;
    }
    return call_session_do_call(rt->call, device_id->valuestring, call_type);
}

static int _dial_contact_index(DeviceRuntime *rt, int index,
                               const char *call_type) {
    cJSON *contact = call_session_copy_contact(rt->call, index);
    if (!contact) {
        if (call_session_do_list_contacts(rt->call) == 0)
            contact = call_session_copy_contact(rt->call, index);
    }
    if (!contact) {
        LOG_W("无效的联系人下标: %d", index);
        return -1;
    }
    int rc = _dial_contact(rt, contact, call_type);
    cJSON_Delete(contact);
    return rc;
}

static int _accept_voip_action(void *opaque) {
    DeviceRuntime *rt = opaque;
    return voip_accept_pending(rt->voip);
}

static void _handle_command(DeviceRuntime *rt, const char *line) {
    char command[32] = "", arg1[256] = "", arg2[32] = "";
    sscanf(line, "%31s %255s %31s", command, arg1, arg2);

    int selected_index;
    if (rt->pending_selection && _parse_index(command, &selected_index)) {
        const char *call_type = arg1[0] ? arg1 : rt->pending_call_type;
        int selection = rt->pending_selection;
        rt->pending_selection = 0;
        if (!_valid_call_type(call_type)) {
            LOG_W("通话类型仅支持 video/audio");
            return;
        }
        if (selection == 1)
            _dial_wx_index(rt, selected_index, call_type);
        else
            _dial_contact_index(rt, selected_index, call_type);
        return;
    }
    rt->pending_selection = 0;

    if (strcmp(command, "help") == 0) { _print_commands(); return; }
    if (strcmp(command, "yes") == 0 || strcmp(command, "accept") == 0 ||
        strcmp(command, "a") == 0) {
        if (voip_has_pending(rt->voip)) {
            char room_id[128] = "";
            if (voip_copy_pending_room(rt->voip, room_id,
                                       sizeof(room_id)) == 0 &&
                _begin_session(rt, SESSION_VOIP, 1, room_id) == 0) {
                SessionLease lease = _copy_lease(rt, SESSION_VOIP);
                session_arbiter_run_action(
                    &rt->arbiter, &lease, _accept_voip_action, rt);
            }
        } else {
            call_session_do_accept(rt->call);
        }
        return;
    }
    if (strcmp(command, "no") == 0 || strcmp(command, "reject") == 0 ||
        strcmp(command, "r") == 0) {
        if (voip_has_pending(rt->voip)) {
            char room_id[128] = "";
            voip_copy_pending_room(rt->voip, room_id, sizeof(room_id));
            voip_reject_pending(rt->voip);
            session_arbiter_clear_pending_id(&rt->arbiter, SESSION_VOIP,
                                             room_id);
        } else {
            char room_id[128] = "";
            pthread_mutex_lock(&rt->call->lock);
            if (rt->call->pending_call)
                STR_COPY(room_id, rt->call->pending_room_id);
            pthread_mutex_unlock(&rt->call->lock);
            call_session_do_reject(rt->call, arg1[0] ? arg1 : "decline");
            if (!call_session_has_pending(rt->call))
                session_arbiter_clear_pending_id(&rt->arbiter, SESSION_CALL,
                                                 room_id);
        }
        return;
    }
    if (strcmp(command, "wxcall") == 0 || strcmp(command, "w") == 0) {
        const char *call_type = _default_call_type(rt);
        int index;
        if (!arg1[0] || _valid_call_type(arg1)) {
            if (_valid_call_type(arg1)) call_type = arg1;
            int count = voip_list_authorized(rt->voip);
            if (count > 0) {
                rt->pending_selection = 1;
                STR_COPY(rt->pending_call_type, call_type);
                LOG_I("请输入联系人序号发起%s呼叫，输入其他命令取消选择",
                      strcmp(call_type, "video") == 0 ? "视频" : "语音");
            }
            return;
        }
        if (!_parse_index(arg1, &index)) {
            LOG_W("无效的 VoIP 联系人下标: %s", arg1);
            return;
        }
        if (arg2[0]) call_type = arg2;
        if (!_valid_call_type(call_type)) {
            LOG_W("微信通话类型仅支持 video/audio");
            return;
        }
        _dial_wx_index(rt, index, call_type);
        return;
    }
    if (strcmp(command, "call") == 0) {
        const char *call_type = _default_call_type(rt);
        int index;
        if (!arg1[0] || _valid_call_type(arg1)) {
            if (_valid_call_type(arg1)) call_type = arg1;
            if (call_session_do_list_contacts(rt->call) == 0) {
                cJSON *first = call_session_copy_contact(rt->call, 0);
                if (first) {
                    cJSON_Delete(first);
                    rt->pending_selection = 2;
                    STR_COPY(rt->pending_call_type, call_type);
                    LOG_I("请输入联系人序号发起%s呼叫，输入其他命令取消选择",
                          strcmp(call_type, "video") == 0 ? "视频" : "语音");
                }
            }
            return;
        }
        if (arg2[0]) call_type = arg2;
        if (!_valid_call_type(call_type)) {
            LOG_W("设备通话类型仅支持 video/audio");
            return;
        }
        if (_parse_index(arg1, &index)) {
            _dial_contact_index(rt, index, call_type);
            return;
        }
        cJSON *contact = call_session_find_contact(rt->call, arg1);
        if (contact) {
            _dial_contact(rt, contact, call_type);
            cJSON_Delete(contact);
            return;
        }
        contact = voip_find_authorized(rt->voip, arg1);
        if (contact) {
            _dial_contact(rt, contact, call_type);
            cJSON_Delete(contact);
        } else {
            call_session_do_call(rt->call, arg1, call_type);
        }
        return;
    }
    if (strcmp(command, "aicall") == 0) {
        if (_begin_session(rt, SESSION_AI, 0, "") != 0)
            return;
        char peer[512] = "", token[1024] = "", role[64] = "";
        if (ai_get_token(rt->ai_server, rt->mqtt_token, rt->device_id, peer, sizeof(peer), token, sizeof(token), role, sizeof(role)) != 0 ||
            ai_start_session(rt->ai, peer, token, rt->ai_audio, rt->device_id, role) != 0)
            session_arbiter_finish(&rt->arbiter, SESSION_AI);
        return;
    }
    if (strcmp(command, "hangup") == 0 || strcmp(command, "h") == 0 ||
        strcmp(command, "cancel") == 0) {
        SessionKind current = session_arbiter_current(&rt->arbiter);
        if (current == SESSION_VOIP) session_arbiter_finish(&rt->arbiter, SESSION_VOIP);
        else if (current == SESSION_AI) session_arbiter_finish(&rt->arbiter, SESSION_AI);
        else if (current == SESSION_CALL) { if (strcmp(command, "cancel") == 0) call_session_do_cancel(rt->call); else call_session_do_hangup(rt->call); session_arbiter_finish(&rt->arbiter, SESSION_CALL); }
        else {
            pthread_mutex_lock(&rt->call->lock);
            int recovered_call = rt->call->active;
            pthread_mutex_unlock(&rt->call->lock);
            if (recovered_call)
                call_session_do_hangup(rt->call);
            else if (voip_is_active(rt->voip))
                voip_stop_session(rt->voip);
            else
                LOG_W("没有进行中的通话");
        }
        return;
    }
    if (strcmp(command, "exit") == 0 || strcmp(command, "e") == 0) {
        g_stop = 1;
        return;
    }
    call_session_dispatch(rt->call, line);
}

static void _handle_product_action(DeviceRuntime *rt,
                                   const DeviceProductAction *action) {
    if (!rt || !action) return;
    char command[1024];
    command[0] = '\0';
    switch (action->type) {
    case DEVICE_ACTION_RAW_COMMAND:
        STR_COPY(command, action->raw_command);
        break;
    case DEVICE_ACTION_ACCEPT:
        STR_COPY(command, "accept");
        break;
    case DEVICE_ACTION_REJECT:
        snprintf(command, sizeof(command), "reject %s",
                 action->reason[0] ? action->reason : "decline");
        break;
    case DEVICE_ACTION_START_AI:
        STR_COPY(command, "aicall");
        break;
    case DEVICE_ACTION_HANGUP:
        STR_COPY(command, "hangup");
        break;
    case DEVICE_ACTION_CANCEL:
        STR_COPY(command, "cancel");
        break;
    case DEVICE_ACTION_DIAL_DEVICE:
        snprintf(command, sizeof(command), "call %s %s", action->target,
                 action->call_type[0] ? action->call_type :
                 _default_call_type(rt));
        break;
    case DEVICE_ACTION_DIAL_CONTACT_INDEX:
        snprintf(command, sizeof(command), "call %d %s", action->index,
                 action->call_type[0] ? action->call_type :
                 _default_call_type(rt));
        break;
    case DEVICE_ACTION_DIAL_WX_INDEX:
        snprintf(command, sizeof(command), "wxcall %d %s", action->index,
                 action->call_type[0] ? action->call_type :
                 _default_call_type(rt));
        break;
    case DEVICE_ACTION_EXIT:
        STR_COPY(command, "exit");
        break;
    case DEVICE_ACTION_NONE:
    default:
        return;
    }
    if (command[0]) _handle_command(rt, command);
}

static void *_runtime_cmd_thread(void *arg) {
    DeviceRuntime *rt = arg;
    _print_commands();
    while (!g_stop) {
        ai_poll(rt->ai);
        voip_expire_outgoing(rt->voip);
        voip_expire_connection(rt->voip);
        char expired_room[128];
        if (voip_expire_pending(rt->voip, expired_room,
                                sizeof(expired_room)))
            session_arbiter_clear_pending_id(
                &rt->arbiter, SESSION_VOIP, expired_room);
        if (call_expire_pending(rt->call, expired_room,
                                sizeof(expired_room)))
            session_arbiter_clear_pending_id(
                &rt->arbiter, SESSION_CALL, expired_room);
        DeviceProductAction action;
        int action_result = device_product_poll_action(&action, 100);
        if (action_result < 0) {
            device_recovery_report(DEVICE_RECOVERY_PLATFORM, action_result,
                                   "product action poll failed");
            sleep_ms(100);
            continue;
        }
        if (action_result > 0) _handle_product_action(rt, &action);
    }
    return NULL;
}

/* ── Print banner ────────────────────────────────────────────────────────── */

static void _banner(void) {
    printf(C_BOLD "──────────────────────────────────────────────────" C_RESET "\n");
    printf("  TiRTC Linux C 设备端参考实现\n");
    printf(C_BOLD "──────────────────────────────────────────────────" C_RESET "\n\n");
}

static int _validate_media_files(const char *audio_path,
                                 const AudioFormat *audio_format,
                                 const char *video_path,
                                 const VideoFormat *video_format,
                                 const char *ai_audio_path,
                                 const AudioFormat *ai_audio_format) {
    DeviceMediaSource source;
    DeviceMediaSourceConfig config = {
        .audio_locator = audio_path,
        .audio_format = audio_format ? audio_format->name : NULL,
        .video_locator = video_path,
        .video_format = video_format ? video_format->name : NULL,
        .audio_packet_ms = AUDIO_PKT_MS_VOIP,
        .business = DEVICE_BUSINESS_STREAM,
    };
    if (device_media_source_open(&source, &config) != 0)
        return -1;
    device_media_source_close(&source);
    config.audio_locator = ai_audio_path;
    config.audio_format = ai_audio_format ? ai_audio_format->name : NULL;
    config.video_locator = "";
    config.video_format = NULL;
    config.audio_packet_ms = AUDIO_PKT_MS_AI;
    config.business = DEVICE_BUSINESS_AI;
    if (device_media_source_open(&source, &config) != 0)
        return -1;
    device_media_source_close(&source);
    return 0;
}

/* ── Usage ───────────────────────────────────────────────────────────────── */

static void _usage(const char *prog) {
    printf("Usage: %s [OPTIONS]\n\n", prog);
    printf("Runtime: live streaming is always started; VoIP, AI and device calls are available in one process.\n\n");
    printf("Device options:\n");
    printf("  --device-id    ID       Bound device ID (env: DEVICE_ID)\n");
    printf("  --device-key   KEY      Device key (env: DEVICE_KEY)\n");
    printf("  --creds-file   PATH     Credentials file path (env: CREDS_FILE, default: device_creds.json)\n");
    printf("  --mac          MAC      MAC address (default: AA:BB:CC:DD:EE:FF)\n");
    printf("  --timeout      SEC      auth_grant timeout (default: 190)\n\n");
    printf("  --ca-cert      PATH     MQTT/HTTPS CA certificate (env: MQTT_CA_CERT, default: ../assets/ca-certificates.crt)\n\n");
    printf("  --insecure              Disable MQTT/HTTPS certificate checks (test only; env: TIRTC_INSECURE=1)\n\n");
    printf("Linux default media adapter options:\n");
    printf("  --up-audio-file PATH      Encoded audio file for stream/VoIP/device calls"
           " (default: %s)\n", DEFAULT_AUDIO_PATH);
    printf("  --up-audio-format FMT     Its format (default: alaw_8khz)\n");
    printf("  --up-video-file PATH      Encoded video file; pass an empty path for audio-only\n");
    printf("  --up-video-format FMT     Its format: %s (default: h264)\n", video_format_choices());
    printf("  --down-audio-format FMT   Downlink negotiation format (default: alaw_8khz)\n");
    printf("  --down-video-format FMT   Downlink negotiation format (default: h264)\n");
    printf("  --down-media-dir PATH     AI downlink recording root (default: received)\n");
    printf("  --ai-audio-file PATH      AI request audio file (default: --up-audio-file)\n");
    printf("  --ai-up-audio-format FMT  AI request audio format (default: --up-audio-format)\n");
    printf("  The stock adapter records AI audio and discards other downlink media; a product sink replaces this behavior.\n\n");
    printf("Other:\n");
    printf("  --endpoint     URL      service discovery entry (default: http://ep-open.tangeopen.com)\n");
    printf("  --log-level    LEVEL    debug|info|warn|error (default: debug)\n");
    printf("  --help                  Show this help\n");
}

/* ── Main ────────────────────────────────────────────────────────────────── */

/* A product may link an object that provides a strong implementation of this
 * Linux-only hook. It must install DeviceAdapterV1 before worker threads are
 * created. The stock build falls through to the file-media demo adapter. */
__attribute__((weak)) int device_product_adapter_install(void) {
    return DEVICE_ADAPTER_NOT_HANDLED;
}

int device_reference_run(int argc, char *argv[]) {
    int product_adapter_result = device_product_adapter_install();
    if (product_adapter_result < 0) {
        fprintf(stderr, "初始化产品设备适配器失败: %d\n",
                product_adapter_result);
        return 1;
    }
    if (product_adapter_result == 0 && !device_adapter_is_installed()) {
        fprintf(stderr, "产品适配器入口返回成功但未安装 DeviceAdapterV1\n");
        return 1;
    }
    if (product_adapter_result != 0 &&
        product_adapter_result != DEVICE_ADAPTER_NOT_HANDLED) {
        fprintf(stderr, "产品适配器入口返回未知状态: %d\n",
                product_adapter_result);
        return 1;
    }
    if (linux_device_adapter_install_default() != 0) {
        fprintf(stderr, "初始化 Linux 默认设备适配器失败\n");
        return 1;
    }
    if (curl_global_init(CURL_GLOBAL_DEFAULT) != CURLE_OK) {
        fprintf(stderr, "初始化 libcurl 失败\n");
        return 1;
    }
    atexit(curl_global_cleanup);

    /* ── Options ───────────────────────────────────────────────────────── */
    const char *device_id   = "";
    const char *device_key  = "";
    const char *mac         = "AA:BB:CC:DD:EE:FF";
    int         timeout     = 190;
    const char *services_base = "http://ep-open.tangeopen.com"; /* service discovery entry */
    const char *log_level   = "debug";
    const char *creds_file  = "device_creds.json";
    const char *ca_cert     = "../assets/ca-certificates.crt";
    const char *up_audio_format = "alaw_8khz";
    const char *down_audio_format = "alaw_8khz";
    const char *up_video_format = "h264";
    const char *down_video_format = "h264";
    const char *ai_up_audio_format = NULL;
    const char *down_media_dir = "received";
    int insecure = 0;

    /* Media paths */
    char video_path[512];
    char audio_path[512];
    char ai_audio_path[512];

    /* Defaults when launched from the documented device-sim-c directory. */
    snprintf(video_path,     sizeof(video_path),     "../assets/video.h264");
    snprintf(audio_path,     sizeof(audio_path),     DEFAULT_AUDIO_PATH);
    ai_audio_path[0] = '\0';

    /* Parse env vars */
    const char *env;
    if ((env = getenv("DEVICE_ID")))       device_id   = env;
    if ((env = getenv("DEVICE_KEY")))      device_key  = env;
    if ((env = getenv("DEVICE_MAC")))      mac         = env;
    if ((env = getenv("LOG_LEVEL")))       log_level   = env;
    if ((env = getenv("UP_AUDIO_FILE")))   snprintf(audio_path, sizeof(audio_path), "%s", env);
    if ((env = getenv("UP_VIDEO_FILE")))   snprintf(video_path, sizeof(video_path), "%s", env);
    if ((env = getenv("AI_AUDIO_FILE")))   snprintf(ai_audio_path, sizeof(ai_audio_path), "%s", env);
    if ((env = getenv("CREDS_FILE")))      creds_file  = env;
    if ((env = getenv("MQTT_CA_CERT")))    ca_cert     = env;
    if ((env = getenv("DOWN_AUDIO_FORMAT"))) down_audio_format = env;
    if ((env = getenv("UP_AUDIO_FORMAT"))) up_audio_format = env;
    if ((env = getenv("UP_VIDEO_FORMAT"))) up_video_format = env;
    if ((env = getenv("DOWN_VIDEO_FORMAT"))) down_video_format = env;
    if ((env = getenv("DOWN_MEDIA_DIR"))) down_media_dir = env;
    if ((env = getenv("AI_UP_AUDIO_FORMAT"))) ai_up_audio_format = env;
    if ((env = getenv("TIRTC_INSECURE")))
        insecure = strcmp(env, "1") == 0 || strcasecmp(env, "true") == 0;

    /* ── Parse CLI args ────────────────────────────────────────────────── */
    static struct option long_opts[] = {
        {"device-id",   required_argument, 0, 'i'},
        {"device-key",  required_argument, 0, 'k'},
        {"mac",         required_argument, 0, 'm'},
        {"timeout",     required_argument, 0, 't'},
        {"up-video-file", required_argument, 0, 'v'},
        {"up-audio-file", required_argument, 0, 'a'},
        {"up-audio-format", required_argument, 0, 'A'},
        {"up-video-format", required_argument, 0, 'V'},
        {"down-audio-format", required_argument, 0, 'd'},
        {"down-video-format", required_argument, 0, 'D'},
        {"down-media-dir", required_argument, 0, 'r'},
        {"ai-audio-file", required_argument, 0, 'p'},
        {"ai-up-audio-format", required_argument, 0, 'P'},
        {"endpoint",    required_argument, 0, 'e'},
        {"log-level",   required_argument, 0, 'l'},
        {"creds-file",  required_argument, 0, 'f'},
        {"ca-cert",     required_argument, 0, 'c'},
        {"insecure",    no_argument,       0, 'x'},
        {"help",        no_argument,       0, 'h'},
        {0, 0, 0, 0}
    };

    int opt;
    while ((opt = getopt_long(argc, argv, "i:k:m:t:v:a:A:V:d:D:r:p:P:e:l:f:c:xh", long_opts, NULL)) != -1) {
        switch (opt) {
            case 'i': device_id   = optarg; break;
            case 'k': device_key  = optarg; break;
            case 'm': mac         = optarg; break;
            case 't': timeout     = atoi(optarg); break;
            case 'v': snprintf(video_path, sizeof(video_path), "%s", optarg); break;
            case 'a': snprintf(audio_path, sizeof(audio_path), "%s", optarg); break;
            case 'A': up_audio_format = optarg; break;
            case 'V': up_video_format = optarg; break;
            case 'd': down_audio_format = optarg; break;
            case 'D': down_video_format = optarg; break;
            case 'r': down_media_dir = optarg; break;
            case 'p': snprintf(ai_audio_path, sizeof(ai_audio_path), "%s", optarg); break;
            case 'P': ai_up_audio_format = optarg; break;
            case 'e': services_base = optarg; break;
            case 'l': log_level   = optarg; break;
            case 'f': creds_file  = optarg; break;
            case 'c': ca_cert     = optarg; break;
            case 'x': insecure    = 1; break;
            case 'h': _usage(argv[0]); return 0;
            default:  _usage(argv[0]); return 1;
        }
    }
    if (!ai_audio_path[0])
        snprintf(ai_audio_path, sizeof(ai_audio_path), "%s", audio_path);
    if (!ai_up_audio_format)
        ai_up_audio_format = up_audio_format;

    /* ── Set log level ─────────────────────────────────────────────────── */
    if (strcmp(log_level, "info") == 0)      log_set_level(LOG_INFO);
    else if (strcmp(log_level, "warn") == 0) log_set_level(LOG_WARN);
    else if (strcmp(log_level, "error") == 0)log_set_level(LOG_ERROR);
    else                                      log_set_level(LOG_DEBUG);

    set_mqtt_ca_cert(ca_cert);
    if (insecure && !device_security_allow_insecure_transport()) {
        LOG_E("产品安全适配器禁止关闭 TLS 证书校验");
        return 1;
    }
    set_mqtt_insecure(insecure);
    http_tls_configure(ca_cert, insecure);
    if (insecure)
        LOG_W("TLS 证书校验已禁用，仅应在隔离测试环境使用");
    const AudioFormat *up_audio_spec = audio_format_find(up_audio_format);
    const AudioFormat *down_audio_spec = audio_format_find(down_audio_format);
    const AudioFormat *ai_up_audio_spec = audio_format_find(ai_up_audio_format);
    const VideoFormat *up_video_spec = video_format_find(up_video_format);
    const VideoFormat *down_video_spec = video_format_find(down_video_format);
    if (!up_audio_spec || !ai_up_audio_spec) {
        LOG_E("不支持的上行音频格式（可选: %s）", audio_format_choices());
        return 1;
    }
    if (!down_audio_spec) {
        LOG_E("不支持的 --down-audio-format: %s（可选: %s）",
              down_audio_format, audio_format_choices());
        return 1;
    }
    if (!audio_format_ai_codec(ai_up_audio_spec) ||
        !audio_format_ai_codec(down_audio_spec)) {
        LOG_E("AI 音频格式仅支持 G.711A、PCM、AMR、Opus");
        return 1;
    }
    if (!up_video_spec || !down_video_spec) {
        LOG_E("不支持的视频格式（可选: %s）", video_format_choices());
        return 1;
    }
    if (voip_configure_profile(up_audio_spec->name, down_audio_spec->name,
                               up_video_spec->name, down_video_spec->name,
                               video_path[0] != '\0') != 0)
        return 1;
    if (_validate_media_files(audio_path, up_audio_spec, video_path,
                              up_video_spec, ai_audio_path,
                              ai_up_audio_spec) != 0)
        return 1;

    /* ── Signal handlers ───────────────────────────────────────────────── */
    signal(SIGINT,  _on_sigint);
    signal(SIGTERM, _on_sigint);
    signal(SIGPIPE, SIG_IGN);

    _banner();

    /* ── Service discovery ─────────────────────────────────────────────── */
    LOG_I("阶段 0: 服务发现 (base=%s)", services_base);
    DeviceServices svc;
    if (fetch_services(&svc, services_base) != 0) return 1;

    /* Resolve TiRTC endpoint: explicit environment override, then discovery. */
    const char *tirtc_endpoint = getenv("TIRTC_ENDPOINT");
    if (!tirtc_endpoint || !tirtc_endpoint[0])
        tirtc_endpoint = svc.tirtc_endpoint;

    /* ── Phase 1: Bind if needed ───────────────────────────────────────── */
    char did[64]  = "";
    char dkey[256] = "";
    STR_COPY(did, device_id);
    STR_COPY(dkey, device_key);

    if (!did[0] || !dkey[0]) {
        /* Try loading from local creds file before scan-binding */
        if (device_identity_load(creds_file, did, sizeof(did),
                                 dkey, sizeof(dkey)) == 0)
            LOG_I("从身份适配器加载凭证 device_id=%s", did);
    }
    if (!did[0] || !dkey[0]) {
        PHASE_TITLE("阶段一：未绑定上线 — 获取验证码并等待绑定");
        ReportResult rep;
        if (report_device(svc.device_server, mac, did[0] ? did : NULL,
                          dkey[0] ? dkey : NULL, &rep) != 0) {
            return 1;
        }
        LOG_I("验证码      : \033[1m%s\033[0m  ← 设备 TTS 播报此 6 位数字", rep.code);
        LOG_D("已获取临时 MQTT 凭证（敏感内容已隐藏）");
        LOG_I("temp_client  : %s", rep.temp_client_id);
        LOG_I("注册/登录入口: %s", EXPERIENCE_PLATFORM_URL);
        PROMPT_BOX("进入设备绑定并输入验证码: \033[1m%s\033[0m", rep.code);

        if (connect_temp_mqtt(svc.mqtt_host, svc.mqtt_port,
                              rep.temp_client_id, rep.temp_token,
                              timeout, svc.mqtt_tls,
                              did, sizeof(did), dkey, sizeof(dkey)) != 0) {
            /* Pre-burned device: empty auth_grant payload -> use local credentials */
            if (did[0] || dkey[0]) {
                /* got credentials from auth_grant */
            } else {
                /* Pre-burned: use existing creds */
                if (!did[0] || !dkey[0]) {
                    LOG_E("auth_grant 后未获取到凭证");
                    return 1;
                }
            }
        }
        if (did[0] && dkey[0]) {
            SEP_LINE();
            PROMPT_KV("绑定完成！本地持久化存储（Flash）：");
            PROMPT_KV("  device_id  = %s", did);
            PROMPT_KV("  device_key = <hidden>");
            PROMPT_KV("  (来源: auth_grant 下发)");
            SEP_LINE();
            if (device_identity_save(creds_file, did, dkey) != 0) {
                LOG_E("身份适配器无法持久化新凭证");
                device_recovery_report(DEVICE_RECOVERY_IDENTITY, -1,
                                       "save granted credentials failed");
                return 1;
            }
        }
    } else {
        LOG_I("使用预存凭证 device_id=%s，直接进入阶段二", did);
        if (device_identity_save(creds_file, did, dkey) != 0)
            LOG_W("身份适配器未保存显式提供的凭证");
    }

    /* ── Phase 2: Get mqtt_token ───────────────────────────────────────── */
    PHASE_TITLE("阶段二：已绑定上线");

    char mqtt_token[512] = "";
    int tok_ret = get_mqtt_token(svc.device_server, did, dkey, mac,
                                 mqtt_token, sizeof(mqtt_token));
    if (tok_ret == -2) {
        /* 6006: device unbound — restart unbound flow */
        LOG_W("设备已解绑，重新进入验证码绑定流程（保留原 device_id=%s）", did);
        ReportResult rep;
        if (report_device(svc.device_server, mac, did, dkey, &rep) != 0)
            return 1;
        if (connect_temp_mqtt(svc.mqtt_host, svc.mqtt_port,
                              rep.temp_client_id, rep.temp_token,
                              timeout, svc.mqtt_tls,
                              did, sizeof(did), dkey, sizeof(dkey)) != 0)
            return 1;
        if (get_mqtt_token(svc.device_server, did, dkey, mac,
                           mqtt_token, sizeof(mqtt_token)) != 0)
            return 1;
        if (device_identity_save(creds_file, did, dkey) != 0) {
            LOG_E("重新绑定后无法持久化凭证");
            device_recovery_report(DEVICE_RECOVERY_IDENTITY, -1,
                                   "save rebound credentials failed");
            return 1;
        }
    } else if (tok_ret != 0) {
        return 1;
    }

    DeviceRuntime rt;
    memset(&rt, 0, sizeof(rt));
    rt.device_id = did;
    rt.secret_key = dkey;
    rt.client_id = mac;
    rt.endpoint = tirtc_endpoint;
    rt.voip_server = svc.voip_server;
    rt.ai_server = svc.ai_server;
    rt.mqtt_token = mqtt_token;
    rt.ai_audio = ai_audio_path;
    rt.up_audio_format = up_audio_spec->name;
    rt.up_video_format = up_video_spec->name;
    snprintf(rt.video, sizeof(rt.video), "%s", video_path);
    snprintf(rt.audio, sizeof(rt.audio), "%s", audio_path);
    rt.voip = voip_create(svc.voip_server, did, mqtt_token, audio_path);
    rt.ai = ai_create_ex(svc.ai_server, did, mqtt_token, ai_audio_path,
                         ai_up_audio_spec->name, down_audio_spec->name);
    rt.call = call_create_ex(svc.call_server, did, mqtt_token,
                             audio_path, up_audio_spec->name,
                             video_path, up_video_spec->name);
    if (!rt.voip || !rt.ai || !rt.call) {
        LOG_E("创建业务会话状态失败");
        voip_destroy(rt.voip);
        ai_destroy(rt.ai);
        call_destroy(rt.call);
        return 1;
    }
    ai_configure_receive_dir(rt.ai, down_media_dir);
    if (voip_configure_media(rt.voip, audio_path, up_audio_spec->name,
                             video_path, up_video_spec->name) != 0) {
        LOG_E("配置 VoIP 上行媒体源失败");
        voip_destroy(rt.voip);
        ai_destroy(rt.ai);
        call_destroy(rt.call);
        return 1;
    }
    if (stream_service_register() != 0 ||
        voip_service_register() != 0 ||
        ai_service_register() != 0 ||
        call_service_register() != 0) {
        LOG_E("注册 TiRTC 业务回调失败");
        tirtc_runtime_stop();
        voip_destroy(rt.voip);
        ai_destroy(rt.ai);
        call_destroy(rt.call);
        return 1;
    }
    if (tirtc_runtime_start(did, dkey, mac, tirtc_endpoint) != 0) {
        LOG_E("启动进程级 TiRTC runtime 失败");
        voip_destroy(rt.voip);
        ai_destroy(rt.ai);
        call_destroy(rt.call);
        return 1;
    }

    SessionAdapter stream = {_start_stream, _stop_stream, &rt};
    SessionAdapter voip = {_start_voip, _stop_voip, &rt};
    SessionAdapter ai = {_start_ai, _stop_ai, &rt};
    SessionAdapter call = {_start_call, _stop_call, &rt};
    pthread_mutex_init(&rt.lease_lock, NULL);
    if (session_coordinator_init(
            &rt.coordinator, &stream, &voip, &ai, &call) != 0) {
        tirtc_runtime_stop();
        pthread_mutex_destroy(&rt.lease_lock);
        voip_destroy(rt.voip);
        ai_destroy(rt.ai);
        call_destroy(rt.call);
        return 1;
    }
    session_arbiter_init(&rt.arbiter, &rt.coordinator);
    if (!session_arbiter_ready(&rt.arbiter)) {
        session_arbiter_destroy(&rt.arbiter);
        session_coordinator_destroy(&rt.coordinator);
        tirtc_runtime_stop();
        pthread_mutex_destroy(&rt.lease_lock);
        voip_destroy(rt.voip);
        ai_destroy(rt.ai);
        call_destroy(rt.call);
        return 1;
    }
    voip_set_session_end_callback(rt.voip, _finish_voip, &rt);
    voip_set_recovered_start_callback(rt.voip, _recover_voip, &rt);
    ai_set_session_end_callback(rt.ai, _finish_ai, &rt);
    call_set_runtime_callbacks_ex(rt.call, _begin_call, _finish_call, &rt);
    call_set_runtime_action_callback(rt.call, _run_call_action);

    /* Profile is registered before the idle stream, so incoming WeChat calls
     * can find this device even while it is serving H5 live video. */
    cJSON *initial_callers = NULL;
    if (voip_report_profile(svc.voip_server, mqtt_token, &initial_callers) == 0)
        voip_set_auth_list(rt.voip, initial_callers);
    if (session_coordinator_start_stream(&rt.coordinator) != 0) {
        session_arbiter_shutdown(&rt.arbiter);
        tirtc_runtime_stop();
        voip_destroy(rt.voip);
        ai_destroy(rt.ai);
        call_destroy(rt.call);
        session_arbiter_destroy(&rt.arbiter);
        session_coordinator_destroy(&rt.coordinator);
        pthread_mutex_destroy(&rt.lease_lock);
        return 1;
    }

    MqttMsgHandler handler;
    memset(&handler, 0, sizeof(handler));
    handler.on_call_incoming = _mqtt_voip_incoming;
    handler.on_callers_update = _mqtt_voip_callers;
    handler.on_call_cancel = _mqtt_voip_cancel;
    handler.on_device_call_incoming = _mqtt_call_incoming;
    handler.on_device_room_cancel = _mqtt_call_cancel;
    handler.on_device_call_reject = _mqtt_call_reject;
    handler.on_device_callers_update_ex = _mqtt_callers;
    handler.on_device_callee_answered = _mqtt_callee_answered;

    pthread_t cmd_tid;
    if (pthread_create(&cmd_tid, NULL, _runtime_cmd_thread, &rt) != 0) {
        LOG_E("无法创建终端线程");
        session_arbiter_shutdown(&rt.arbiter);
        tirtc_runtime_stop();
        voip_destroy(rt.voip); ai_destroy(rt.ai); call_destroy(rt.call);
        session_arbiter_destroy(&rt.arbiter);
        session_coordinator_destroy(&rt.coordinator);
        pthread_mutex_destroy(&rt.lease_lock);
        return 1;
    }
    connect_mqtt_blocking(svc.mqtt_host, svc.mqtt_port, did, mqtt_token,
                          &handler, &rt, &g_stop, svc.mqtt_tls);

    g_stop = 1;
    pthread_join(cmd_tid, NULL);
    LOG_I("正在关闭...");
    session_arbiter_shutdown(&rt.arbiter);
    tirtc_runtime_stop();
    voip_destroy(rt.voip);
    ai_destroy(rt.ai);
    call_destroy(rt.call);
    session_arbiter_destroy(&rt.arbiter);
    session_coordinator_destroy(&rt.coordinator);
    pthread_mutex_destroy(&rt.lease_lock);

    LOG_I("已退出。");
    return 0;
}
