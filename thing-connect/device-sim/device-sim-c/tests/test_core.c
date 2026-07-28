#define LOG_MODULE "test"
#include "common.h"

#include <assert.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>

#include "file_media_source.h"
#include "media_format.h"
#include "sdk_callback_guard.h"
#include "session_arbiter.h"
#include "session_coordinator.h"
#include "tirtc_ai.h"
#include "tirtc_voip.h"

int g_log_level = 100;
volatile sig_atomic_t g_stop = 0;

static void write_all(int fd, const unsigned char *data, size_t length) {
    while (length) {
        ssize_t written = write(fd, data, length);
        assert(written > 0);
        data += written;
        length -= (size_t)written;
    }
}

static void test_format_tables(void) {
    assert(audio_format_find("alaw_8khz"));
    assert(audio_format_find("g711a_8k") ==
           audio_format_find("alaw_8khz"));
    assert(audio_format_find("pcm_16k") ==
           audio_format_find("pcm_s16le_16khz"));
    assert(!audio_format_find("mp3"));
    assert(video_format_find("h264"));
    assert(video_format_find("h265"));
    assert(video_format_find("mjpeg"));
    assert(!video_format_find("vp9"));
}

static void test_fixed_audio_and_mjpeg(void) {
    char audio_path[] = "/tmp/tirtc-c-audio-XXXXXX";
    char video_path[] = "/tmp/tirtc-c-video-XXXXXX";
    int audio_fd = mkstemp(audio_path);
    int video_fd = mkstemp(video_path);
    assert(audio_fd >= 0 && video_fd >= 0);

    unsigned char audio[17];
    memset(audio, 0x5a, sizeof(audio));
    static const unsigned char mjpeg[] = {
        0xff, 0xd8, 0x11, 0xff, 0xd9,
        0xff, 0xd8, 0x22, 0x33, 0xff, 0xd9
    };
    write_all(audio_fd, audio, sizeof(audio));
    write_all(video_fd, mjpeg, sizeof(mjpeg));
    close(audio_fd);
    close(video_fd);

    FileMediaSource source;
    assert(file_media_source_open(&source, audio_path,
                                  audio_format_find("alaw_8khz"),
                                  video_path, video_format_find("mjpeg"),
                                  40) == 0);
    assert(source.audio_count == 1);
    assert(source.video_count == 2);
    const unsigned char *payload;
    size_t length;
    double duration;
    assert(file_media_source_next_audio(&source, &payload, &length,
                                        &duration));
    assert(length == 320);
    assert(duration == 40.0);
    assert(payload[0] == 0x5a);
    assert(payload[319] == 0xd5);

    int key = 0;
    assert(file_media_source_next_video(&source, &payload, &length,
                                        &key, 1));
    assert(key && length == 5);
    assert(file_media_source_next_video(&source, &payload, &length,
                                        &key, 0));
    assert(key && length == 6);
    assert(file_media_source_next_video(&source, &payload, &length,
                                        &key, 0));
    assert(key && length == 5);
    file_media_source_close(&source);
    unlink(audio_path);
    unlink(video_path);
}

static void test_invalid_amr(void) {
    char audio_path[] = "/tmp/tirtc-c-amr-XXXXXX";
    int fd = mkstemp(audio_path);
    assert(fd >= 0);
    static const unsigned char invalid[] = {0x00, 0x01, 0x02};
    write_all(fd, invalid, sizeof(invalid));
    close(fd);
    FileMediaSource source;
    assert(file_media_source_open(&source, audio_path,
                                  audio_format_find("amr_nb"),
                                  "", NULL, 20) != 0);
    unlink(audio_path);
}

static void test_encoded_audio_containers(void) {
    char path[] = "/tmp/tirtc-c-encoded-XXXXXX";
    int fd = mkstemp(path);
    assert(fd >= 0);
    unsigned char amr[6 + 13] = "#!AMR\n";
    memset(amr + 6, 0, 13); /* AMR-NB frame type 0 */
    write_all(fd, amr, sizeof(amr));
    close(fd);

    FileMediaSource source;
    assert(file_media_source_open(&source, path,
                                  audio_format_find("amr_nb"),
                                  "", NULL, 20) == 0);
    assert(source.audio_count == 1);
    assert(source.audio_chunks[0].length == 13);
    assert(source.audio_chunks[0].duration_ms == 20.0);
    file_media_source_close(&source);

    fd = open(path, O_WRONLY | O_TRUNC);
    assert(fd >= 0);
    static const unsigned char adts[] = {
        0xff, 0xf1, 0x00, 0x00, 0x00, 0xe0, 0x00
    };
    write_all(fd, adts, sizeof(adts));
    close(fd);
    assert(file_media_source_open(&source, path,
                                  audio_format_find("aac_adts_8khz"),
                                  "", NULL, 20) == 0);
    assert(source.audio_count == 1);
    assert(source.audio_chunks[0].length == sizeof(adts));
    assert(source.audio_chunks[0].duration_ms == 128.0);
    file_media_source_close(&source);

    fd = open(path, O_WRONLY | O_TRUNC);
    assert(fd >= 0);
    unsigned char ogg[27 + 2 + 9] = {0};
    memcpy(ogg, "OggS", 4);
    ogg[26] = 2;
    ogg[27] = 8;
    ogg[28] = 1;
    memcpy(ogg + 29, "OpusHead", 8);
    ogg[37] = 0x08; /* one 20 ms Opus frame */
    write_all(fd, ogg, sizeof(ogg));
    close(fd);
    assert(file_media_source_open(&source, path,
                                  audio_format_find("opus_16khz"),
                                  "", NULL, 20) == 0);
    assert(source.audio_count == 1);
    assert(source.audio_chunks[0].length == 1);
    assert(source.audio_chunks[0].duration_ms == 20.0);
    file_media_source_close(&source);
    unlink(path);
}

static void test_annexb_video(void) {
    char audio_path[] = "/tmp/tirtc-c-annex-audio-XXXXXX";
    char video_path[] = "/tmp/tirtc-c-annex-video-XXXXXX";
    int audio_fd = mkstemp(audio_path);
    int video_fd = mkstemp(video_path);
    assert(audio_fd >= 0 && video_fd >= 0);
    static const unsigned char audio[] = {0xd5};
    static const unsigned char h264[] = {
        0, 0, 0, 1, 0x67, 0x01,
        0, 0, 0, 1, 0x65, 0x02,
        0, 0, 0, 1, 0x61, 0x03
    };
    write_all(audio_fd, audio, sizeof(audio));
    write_all(video_fd, h264, sizeof(h264));
    close(audio_fd);
    close(video_fd);

    FileMediaSource source;
    assert(file_media_source_open(&source, audio_path,
                                  audio_format_find("alaw_8khz"),
                                  video_path, video_format_find("h264"),
                                  40) == 0);
    assert(source.video_count == 2);
    assert(source.video_chunks[0].key);
    assert(!source.video_chunks[1].key);
    file_media_source_close(&source);

    video_fd = open(video_path, O_WRONLY | O_TRUNC);
    assert(video_fd >= 0);
    static const unsigned char h265[] = {
        0, 0, 0, 1, 0x40, 0x01, /* VPS, type 32 */
        0, 0, 0, 1, 0x26, 0x01, /* IDR, type 19 */
        0, 0, 0, 1, 0x02, 0x01  /* VCL, type 1 */
    };
    write_all(video_fd, h265, sizeof(h265));
    close(video_fd);
    assert(file_media_source_open(&source, audio_path,
                                  audio_format_find("alaw_8khz"),
                                  video_path, video_format_find("h265"),
                                  40) == 0);
    assert(source.video_count == 2);
    assert(source.video_chunks[0].key);
    assert(!source.video_chunks[1].key);
    file_media_source_close(&source);
    unlink(audio_path);
    unlink(video_path);
}

typedef struct {
    SdkCallbackGuard *guard;
    int *finished;
} GuardThread;

static void *guard_worker(void *opaque) {
    GuardThread *thread = opaque;
    sdk_callback_enter(thread->guard);
    sleep_ms(60);
    *thread->finished = 1;
    sdk_callback_leave(thread->guard);
    return NULL;
}

static void test_callback_guard(void) {
    SdkCallbackGuard guard = SDK_CALLBACK_GUARD_INITIALIZER;
    int finished = 0;
    GuardThread context = {&guard, &finished};
    pthread_t thread;
    assert(pthread_create(&thread, NULL, guard_worker, &context) == 0);
    sleep_ms(10);
    sdk_callback_wait_idle(&guard);
    assert(finished);
    pthread_join(thread, NULL);
}

typedef struct {
    SessionCoordinator *coordinator;
    SessionKind kind;
    int starts;
    int stops;
} CoordinatorAdapterContext;

static int coordinator_start(void *opaque) {
    CoordinatorAdapterContext *context = opaque;
    context->starts++;
    return 0;
}

static void coordinator_stop(void *opaque) {
    CoordinatorAdapterContext *context = opaque;
    context->stops++;
    if (context->kind == SESSION_VOIP)
        session_coordinator_finish_async(context->coordinator, SESSION_VOIP);
}

static void test_coordinator_reentrant_terminal_callback(void) {
    SessionCoordinator coordinator;
    CoordinatorAdapterContext stream = {&coordinator, SESSION_STREAM, 0, 0};
    CoordinatorAdapterContext voip = {&coordinator, SESSION_VOIP, 0, 0};
    CoordinatorAdapterContext ai = {&coordinator, SESSION_AI, 0, 0};
    CoordinatorAdapterContext call = {&coordinator, SESSION_CALL, 0, 0};
    SessionAdapter stream_adapter = {coordinator_start, coordinator_stop, &stream};
    SessionAdapter voip_adapter = {coordinator_start, coordinator_stop, &voip};
    SessionAdapter ai_adapter = {coordinator_start, coordinator_stop, &ai};
    SessionAdapter call_adapter = {coordinator_start, coordinator_stop, &call};

    session_coordinator_init(&coordinator, &stream_adapter, &voip_adapter,
                             &ai_adapter, &call_adapter);
    assert(session_coordinator_start_stream(&coordinator) == 0);
    assert(session_coordinator_begin(&coordinator, SESSION_VOIP) == 0);
    assert(session_coordinator_begin(&coordinator, SESSION_VOIP) != 0);
    session_coordinator_finish(&coordinator, SESSION_VOIP);
    assert(session_coordinator_current(&coordinator) == SESSION_STREAM);
    assert(stream.starts == 2);
    assert(voip.starts == 1 && voip.stops == 1);
    session_coordinator_destroy(&coordinator);
}

typedef struct {
    int starts;
    int stops;
    int fail_start;
} ArbiterAdapterContext;

static int arbiter_adapter_start(void *opaque) {
    ArbiterAdapterContext *context = opaque;
    context->starts++;
    if (context->fail_start > 0) {
        context->fail_start--;
        return -1;
    }
    return 0;
}

static void arbiter_adapter_stop(void *opaque) {
    ArbiterAdapterContext *context = opaque;
    context->stops++;
}

typedef struct {
    SessionArbiter *arbiter;
    SessionKind kind;
    int granted;
} PendingOfferThread;

static void *offer_pending_worker(void *opaque) {
    PendingOfferThread *thread = opaque;
    thread->granted =
        session_arbiter_offer_pending(thread->arbiter, thread->kind) == 0;
    return NULL;
}

typedef struct {
    SessionArbiter *arbiter;
    const char *session_id;
} CancelActionContext;

static int cancel_during_action(void *opaque) {
    CancelActionContext *context = opaque;
    assert(session_arbiter_cancel_id(
               context->arbiter, SESSION_CALL,
               context->session_id) == 2);
    return 0;
}

typedef struct {
    SessionLease lease;
    int published;
    int start_saw_published;
} LeasePublishContext;

static void capture_lease(void *opaque, const SessionLease *lease) {
    LeasePublishContext *context = opaque;
    context->lease = *lease;
    context->published = 1;
}

static int start_after_lease_published(void *opaque) {
    LeasePublishContext *context = opaque;
    context->start_saw_published = context->published;
    return 0;
}

static void stop_after_lease_published(void *opaque) {
    (void)opaque;
}

static void test_session_arbiter_policy(void) {
    SessionCoordinator coordinator;
    SessionArbiter arbiter;
    ArbiterAdapterContext stream = {0}, voip = {0}, ai = {0}, call = {0};
    SessionAdapter stream_adapter = {arbiter_adapter_start,
                                     arbiter_adapter_stop, &stream};
    SessionAdapter voip_adapter = {arbiter_adapter_start,
                                   arbiter_adapter_stop, &voip};
    SessionAdapter ai_adapter = {arbiter_adapter_start,
                                 arbiter_adapter_stop, &ai};
    SessionAdapter call_adapter = {arbiter_adapter_start,
                                   arbiter_adapter_stop, &call};
    session_coordinator_init(&coordinator, &stream_adapter, &voip_adapter,
                             &ai_adapter, &call_adapter);
    session_arbiter_init(&arbiter, &coordinator);
    assert(session_coordinator_start_stream(&coordinator) == 0);
    assert(session_arbiter_admit_incoming(&arbiter, SESSION_VOIP) == 0);
    assert(session_arbiter_admit_incoming(&arbiter, SESSION_VOIP) == -1);
    session_arbiter_clear_pending(&arbiter, SESSION_VOIP);

    assert(session_arbiter_offer_pending_id(
               &arbiter, SESSION_CALL, "room-a", 45000) == 0);
    assert(session_arbiter_cancel_id(
               &arbiter, SESSION_CALL, "") == 0);
    assert(session_arbiter_has_pending_id(
        &arbiter, SESSION_CALL, "room-a"));
    assert(session_arbiter_cancel_id(
               &arbiter, SESSION_CALL, "stale-room") == 0);
    assert(session_arbiter_has_pending_id(
        &arbiter, SESSION_CALL, "room-a"));
    assert(session_arbiter_cancel_id(
               &arbiter, SESSION_CALL, "room-a") == 1);

    assert(session_arbiter_offer_pending_id(
               &arbiter, SESSION_CALL, "expires", 1) == 0);
    sleep_ms(2);
    assert(!session_arbiter_has_pending_id(
        &arbiter, SESSION_CALL, "expires"));

    PendingOfferThread offers[12];
    pthread_t threads[12];
    for (size_t i = 0; i < 12; i++) {
        offers[i] = (PendingOfferThread){
            &arbiter, i % 2 ? SESSION_VOIP : SESSION_CALL, 0};
        assert(pthread_create(&threads[i], NULL, offer_pending_worker,
                              &offers[i]) == 0);
    }
    int granted = 0;
    SessionKind winner = SESSION_NONE;
    for (size_t i = 0; i < 12; i++) {
        pthread_join(threads[i], NULL);
        if (offers[i].granted) {
            granted++;
            winner = offers[i].kind;
        }
    }
    assert(granted == 1);
    assert(session_arbiter_begin(&arbiter, SESSION_AI, 0) != 0);
    assert(session_arbiter_begin(&arbiter, winner, 1) == 0);
    assert(session_arbiter_current(&arbiter) == winner);
    assert(session_arbiter_admit_incoming(&arbiter, winner) == 1);
    assert(session_arbiter_begin(&arbiter, SESSION_AI, 0) != 0);
    session_arbiter_finish(&arbiter, winner);
    assert(session_arbiter_current(&arbiter) == SESSION_NONE);
    assert(session_coordinator_current(&coordinator) == SESSION_STREAM);

    call.fail_start = 1;
    assert(session_arbiter_offer_pending(&arbiter, SESSION_CALL) == 0);
    assert(session_arbiter_begin(&arbiter, SESSION_CALL, 1) != 0);
    assert(session_arbiter_has_pending(&arbiter, SESSION_CALL));
    assert(session_arbiter_current(&arbiter) == SESSION_NONE);
    session_arbiter_clear_pending(&arbiter, SESSION_CALL);

    call.fail_start = 0;
    SessionLease first_lease;
    assert(session_arbiter_begin_id(
               &arbiter, SESSION_CALL, 0, "outbound-1",
               &first_lease) == 0);
    session_arbiter_finish_lease(&arbiter, &first_lease);
    SessionLease second_lease;
    assert(session_arbiter_begin_id(
               &arbiter, SESSION_CALL, 0, "outbound-2",
               &second_lease) == 0);
    session_arbiter_finish_lease(&arbiter, &first_lease);
    assert(session_arbiter_current(&arbiter) == SESSION_CALL);
    session_arbiter_finish_lease_async_restore_pending(
        &arbiter, &second_lease);
    for (int i = 0; i < 100 &&
         session_arbiter_current(&arbiter) != SESSION_NONE; i++)
        sleep_ms(1);
    assert(session_arbiter_has_pending(&arbiter, SESSION_CALL));
    assert(session_arbiter_current(&arbiter) == SESSION_NONE);
    session_arbiter_clear_pending(&arbiter, SESSION_CALL);

    LeasePublishContext lease_publish = {0};
    SessionAdapter original_ai_adapter = coordinator.adapters[SESSION_AI];
    coordinator.adapters[SESSION_AI] = (SessionAdapter){
        start_after_lease_published, stop_after_lease_published,
        &lease_publish};
    SessionLease published_lease;
    assert(session_arbiter_begin_id_ex(
               &arbiter, SESSION_AI, 0, "sync-terminal",
               &published_lease, capture_lease, &lease_publish) == 0);
    assert(lease_publish.start_saw_published);
    assert(lease_publish.lease.generation == published_lease.generation);
    assert(strcmp(lease_publish.lease.session_id, "sync-terminal") == 0);
    session_arbiter_finish_lease(&arbiter, &lease_publish.lease);
    coordinator.adapters[SESSION_AI] = original_ai_adapter;

    assert(session_arbiter_offer_pending_id(
               &arbiter, SESSION_CALL, "cancel-in-action", 45000) == 0);
    SessionLease action_lease;
    assert(session_arbiter_begin_id(
               &arbiter, SESSION_CALL, 1, "cancel-in-action",
               &action_lease) == 0);
    CancelActionContext action_context = {
        &arbiter, "cancel-in-action"};
    assert(session_arbiter_run_action(
               &arbiter, &action_lease, cancel_during_action,
               &action_context) != 0);
    assert(session_arbiter_current(&arbiter) == SESSION_NONE);
    assert(session_coordinator_current(&coordinator) == SESSION_STREAM);

    assert(session_arbiter_begin_id(
               &arbiter, SESSION_AI, 0, "ai-recovery", NULL) == 0);
    stream.fail_start = 1;
    session_arbiter_finish(&arbiter, SESSION_AI);
    assert(session_coordinator_current(&coordinator) == SESSION_STREAM);

    session_arbiter_destroy(&arbiter);
    session_coordinator_destroy(&coordinator);
}

static void count_voip_end(void *opaque) {
    int *count = opaque;
    (*count)++;
}

static int count_recovered_start(void *opaque) {
    int *count = opaque;
    (*count)++;
    return 0;
}

static void test_voip_malformed_cancel_and_failed_recovery(void) {
    VoipState *voip = voip_create(
        "https://voip.example", "device-1", "token", "audio.g711a");
    assert(voip);

    cJSON *incoming = cJSON_Parse(
        "{\"peer_id\":\"peer\",\"token\":\"token\","
        "\"wx_room_id\":\"room-1\"}");
    assert(incoming);
    voip_on_call_incoming(voip, incoming);
    assert(voip_has_pending(voip));
    cJSON_Delete(incoming);

    cJSON *malformed = cJSON_Parse("{\"wx_room_id\":123}");
    assert(malformed);
    voip_on_call_cancel(voip, malformed);
    assert(voip_has_pending(voip));
    cJSON_Delete(malformed);

    cJSON *cancel = cJSON_Parse("{\"wx_room_id\":\"room-1\"}");
    assert(cancel);
    voip_on_call_cancel(voip, cancel);
    assert(!voip_has_pending(voip));
    cJSON_Delete(cancel);

    int recovered = 0;
    int ended = 0;
    voip_set_recovered_start_callback(
        voip, count_recovered_start, &recovered);
    voip_set_session_end_callback(voip, count_voip_end, &ended);
    cJSON *missing_token = cJSON_Parse(
        "{\"wx_room_id\":\"room-recovery\","
        "\"wx_call_id\":\"call-1\",\"wx_from\":\"device-1\","
        "\"peer_id\":\"peer\"}");
    assert(missing_token);
    voip_on_call_incoming(voip, missing_token);
    assert(recovered == 1);
    assert(ended == 1);
    cJSON_Delete(missing_token);

    voip_test_force_outgoing_timeout(voip);
    assert(voip_expire_outgoing(voip) == 1);
    assert(ended == 2);

    voip_test_force_connect_timeout(voip);
    assert(voip_expire_connection(voip) == 1);
    assert(ended == 3);

    voip_set_session_end_callback(voip, NULL, NULL);
    voip_destroy(voip);
}

static void test_ai_connect_timeout(void) {
    AiState *ai = ai_create_ex(
        "https://ai.example", "device-1", "token", "audio.pcm",
        "pcm_s16le_16khz", "pcm_s16le_16khz");
    assert(ai);
    int ended = 0;
    ai_set_session_end_callback(ai, count_voip_end, &ended);
    ai_test_force_connect_timeout(ai);
    ai_poll(ai);
    assert(ended == 1);
    assert(!ai_is_active(ai));
    ai_set_session_end_callback(ai, NULL, NULL);
    ai_destroy(ai);
}

int main(void) {
    test_format_tables();
    test_fixed_audio_and_mjpeg();
    test_invalid_amr();
    test_encoded_audio_containers();
    test_annexb_video();
    test_callback_guard();
    test_coordinator_reentrant_terminal_callback();
    test_session_arbiter_policy();
    test_voip_malformed_cancel_and_failed_recovery();
    test_ai_connect_timeout();
    puts("device-sim-c core tests passed");
    return 0;
}
