#define LOG_MODULE "test"
#include "common.h"

#include <assert.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>

#include "audio_recorder.h"
#include "device_adapter.h"
#include "device_flow.h"
#include "file_media_source.h"
#include "linux_device_adapter.h"
#include "media_format.h"
#include "media_subscription_policy.h"
#include "sdk_callback_guard.h"
#include "session_arbiter.h"
#include "session_coordinator.h"
#include "tirtc_ai.h"
#include "tirtc_call.h"
#include "tirtc_runtime.h"
#include "tirtc_voip.h"

typedef struct {
    int audio_remaining;
    int video_remaining;
    int closes;
    int sink_submits;
    int sink_flushes;
    int resource_acquires;
    int resource_releases;
    int notifications;
    int recoveries;
    DeviceProductEvent last_event;
} AdapterContractContext;

static int adapter_identity_load(void *opaque, const char *slot,
                                 char *id, size_t id_size,
                                 char *key, size_t key_size) {
    (void)opaque;
    assert(strcmp(slot, "test-slot") == 0);
    return str_copy(id, id_size, "device-test") |
           str_copy(key, key_size, "key-test");
}

static int adapter_media_open(void *opaque,
                              const DeviceMediaSourceConfig *config,
                              void **handle) {
    AdapterContractContext *context = opaque;
    assert(config->business == DEVICE_BUSINESS_CALL);
    context->audio_remaining = 1;
    context->video_remaining = 1;
    *handle = context;
    return 0;
}

static int adapter_media_has_video(void *opaque, void *handle) {
    assert(opaque == handle);
    return 1;
}

static int adapter_media_next_audio(void *opaque, void *handle,
                                    DeviceMediaPacket *packet) {
    static const unsigned char audio[] = {1, 2, 3};
    AdapterContractContext *context = opaque;
    assert(handle == context);
    if (context->audio_remaining <= 0) return 0;
    context->audio_remaining--;
    *packet = (DeviceMediaPacket){audio, sizeof(audio), 20.0, 0};
    return 1;
}

static int adapter_media_next_video(void *opaque, void *handle,
                                    int force_key_frame,
                                    DeviceMediaPacket *packet) {
    static const unsigned char video[] = {4, 5};
    AdapterContractContext *context = opaque;
    assert(handle == context);
    assert(force_key_frame == 1);
    if (context->video_remaining <= 0) return 0;
    context->video_remaining--;
    *packet = (DeviceMediaPacket){video, sizeof(video), 0.0, 7};
    return 1;
}

static void adapter_media_close(void *opaque, void *handle) {
    AdapterContractContext *context = opaque;
    assert(handle == context);
    context->closes++;
}

static int adapter_sink_submit(void *opaque,
                               const DeviceDownlinkFrame *frame) {
    AdapterContractContext *context = opaque;
    assert(frame->business == DEVICE_BUSINESS_CALL);
    assert(frame->generation == 1);
    context->sink_submits++;
    return 0;
}

static void adapter_sink_flush(void *opaque, DeviceBusiness business,
                               uint64_t generation) {
    AdapterContractContext *context = opaque;
    assert(business == DEVICE_BUSINESS_CALL && generation == 1);
    context->sink_flushes++;
}

static void adapter_product_notify(void *opaque,
                                   const DeviceProductEvent *event) {
    AdapterContractContext *context = opaque;
    context->notifications++;
    context->last_event = *event;
}

static int adapter_resource_acquire(void *opaque, DeviceBusiness business) {
    AdapterContractContext *context = opaque;
    assert(business == DEVICE_BUSINESS_CALL);
    context->resource_acquires++;
    return 0;
}

static void adapter_resource_release(void *opaque, DeviceBusiness business) {
    AdapterContractContext *context = opaque;
    assert(business == DEVICE_BUSINESS_CALL);
    context->resource_releases++;
}

static void adapter_recovery_report(void *opaque, DeviceRecoveryDomain domain,
                                    int code, const char *detail) {
    AdapterContractContext *context = opaque;
    assert(domain == DEVICE_RECOVERY_MEDIA && code == -7);
    assert(strcmp(detail, "media failed") == 0);
    context->recoveries++;
}

static int adapter_random_bytes(void *opaque, unsigned char *output,
                                size_t length) {
    (void)opaque;
    memset(output, 0xab, length);
    return 0;
}

static void test_device_adapter_contract(void) {
    device_adapter_reset_for_testing();
    DeviceAdapterV1 invalid = {0};
    assert(device_adapter_install(&invalid) != 0);

    AdapterContractContext context = {0};
    DeviceAdapterV1 adapter = {
        .abi_version = DEVICE_ADAPTER_ABI_V1,
        .struct_size = sizeof(adapter),
        .identity = {.context = &context, .load = adapter_identity_load},
        .media_source = {
            .context = &context,
            .open = adapter_media_open,
            .has_video = adapter_media_has_video,
            .next_audio = adapter_media_next_audio,
            .next_video = adapter_media_next_video,
            .close = adapter_media_close,
        },
        .media_sink = {
            .context = &context,
            .submit = adapter_sink_submit,
            .flush = adapter_sink_flush,
        },
        .product = {
            .context = &context,
            .notify = adapter_product_notify,
        },
        .resource = {
            .context = &context,
            .acquire = adapter_resource_acquire,
            .release = adapter_resource_release,
        },
        .recovery = {
            .context = &context,
            .report = adapter_recovery_report,
        },
        .security = {
            .context = &context,
            .random_bytes = adapter_random_bytes,
        },
    };
    assert(device_adapter_install(&adapter) == 0);
    memset(&adapter, 0, sizeof(adapter));

    char id[32], key[32];
    assert(device_identity_load("test-slot", id, sizeof(id),
                                key, sizeof(key)) == 0);
    assert(strcmp(id, "device-test") == 0 &&
           strcmp(key, "key-test") == 0);

    DeviceMediaSource source;
    DeviceMediaSourceConfig config = {
        .audio_locator = "capture://microphone",
        .audio_format = "alaw_8khz",
        .video_locator = "capture://camera",
        .video_format = "h264",
        .audio_packet_ms = 20,
        .business = DEVICE_BUSINESS_CALL,
    };
    assert(device_media_source_open(&source, &config) == 0);
    assert(device_media_source_has_video(&source));
    DeviceMediaPacket packet;
    assert(device_media_source_next_audio(&source, &packet) == 1);
    assert(packet.length == 3 && packet.duration_ms == 20.0);
    assert(device_media_source_next_audio(&source, &packet) == 0);
    assert(device_media_source_next_video(&source, 1, &packet) == 1);
    assert(packet.length == 2 && packet.key_frame == 1);
    device_media_source_close(&source);
    assert(context.closes == 1);

    assert(device_resource_acquire(DEVICE_BUSINESS_CALL) == 0);
    device_adapter_session_starting(DEVICE_BUSINESS_CALL);
    device_adapter_session_started(DEVICE_BUSINESS_CALL);
    unsigned char downlink[] = {9};
    assert(device_media_sink_submit(
               DEVICE_BUSINESS_CALL, 0, 10, 1, 0, 20,
               downlink, sizeof(downlink)) == 0);
    assert(device_media_sink_is_enabled(DEVICE_BUSINESS_CALL, 0));
    device_adapter_session_stopped(DEVICE_BUSINESS_CALL);
    device_resource_release(DEVICE_BUSINESS_CALL);
    assert(context.resource_acquires == 1 && context.resource_releases == 1);
    assert(context.sink_submits == 1 && context.sink_flushes == 1);
    assert(context.notifications == 3);
    assert(context.last_event.type == DEVICE_SESSION_STOPPED);

    device_recovery_report(DEVICE_RECOVERY_MEDIA, -7, "media failed");
    assert(context.recoveries == 1);
    unsigned char random[4] = {0};
    assert(device_security_random_bytes(random, sizeof(random)) == 0);
    assert(random[0] == 0xab && random[3] == 0xab);
    assert(!device_security_allow_insecure_transport());

    device_adapter_reset_for_testing();
    assert(linux_device_adapter_install_default() == 0);
}

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
    assert(strcmp(
               audio_format_ai_codec(audio_format_find("alaw_8khz")),
               "g711a") == 0);
    assert(strcmp(
               audio_format_ai_codec(audio_format_find("pcm_s16le_16khz")),
               "pcm") == 0);
    assert(audio_format_ai_codec(audio_format_find("aac_adts_16khz")) ==
           NULL);
    assert(video_format_find("h264"));
    assert(video_format_find("h265"));
    assert(video_format_find("mjpeg"));
    assert(!video_format_find("vp9"));
}

static void *close_audio_recorder(void *opaque) {
    audio_recorder_close(opaque);
    return NULL;
}

static void test_audio_recorder_files(void) {
    char root[] = "/tmp/tirtc-c-recorder-XXXXXX";
    assert(mkdtemp(root) != NULL);

    AudioRecorder recorder;
    assert(audio_recorder_init(&recorder) == 0);
    assert(audio_recorder_open(
               &recorder, root, "DEV001", "ai_test.raw") == 0);

    TIRTCFRAMEINFO frame;
    memset(&frame, 0, sizeof(frame));
    frame.stream_id = STREAM_ID_AI;
    frame.media = TIRTC_AUDIO_ALAW;
    frame.flags = TIRTC_AUDIOSAMPLE_8K16B1C;
    static const unsigned char payload[] = {0xd5, 0xd5, 0xd5, 0xd5};
    frame.length = sizeof(payload);
    assert(audio_recorder_submit(&recorder, &frame, payload) == 0);
    assert(audio_recorder_submit(&recorder, &frame, payload) == 0);
    pthread_t first_close;
    pthread_t second_close;
    assert(pthread_create(
               &first_close, NULL, close_audio_recorder, &recorder) == 0);
    assert(pthread_create(
               &second_close, NULL, close_audio_recorder, &recorder) == 0);
    assert(pthread_join(first_close, NULL) == 0);
    assert(pthread_join(second_close, NULL) == 0);

    assert(audio_recorder_frame_count(&recorder) == 2);
    assert(audio_recorder_dropped_frames(&recorder) == 0);

    struct stat info;
    const char *raw_path = audio_recorder_raw_path(&recorder);
    const char *wav_path = audio_recorder_wav_path(&recorder);
    assert(stat(raw_path, &info) == 0);
    assert(info.st_size == 8);
    assert(stat(wav_path, &info) == 0);
    assert(info.st_size == 44 + 16);

    FILE *wav = fopen(wav_path, "rb");
    assert(wav != NULL);
    unsigned char header[12];
    assert(fread(header, 1, sizeof(header), wav) == sizeof(header));
    fclose(wav);
    assert(memcmp(header, "RIFF", 4) == 0);
    assert(memcmp(header + 8, "WAVE", 4) == 0);

    char fmt_path[1024];
    snprintf(fmt_path, sizeof(fmt_path), "%s/DEV001/ai_test.fmt.json",
             root);
    FILE *fmt = fopen(fmt_path, "r");
    assert(fmt != NULL);
    char metadata[512];
    size_t metadata_length =
        fread(metadata, 1, sizeof(metadata) - 1, fmt);
    fclose(fmt);
    metadata[metadata_length] = '\0';
    assert(strstr(metadata, "\"encoding\":\"alaw\"") != NULL);
    assert(strstr(metadata, "\"sample_rate\":8000") != NULL);
    assert(strstr(metadata, "\"frames\":2") != NULL);

    assert(unlink(raw_path) == 0);
    assert(unlink(wav_path) == 0);
    assert(unlink(fmt_path) == 0);
    char device_dir[1024];
    snprintf(device_dir, sizeof(device_dir), "%s/DEV001", root);
    assert(rmdir(device_dir) == 0);
    assert(rmdir(root) == 0);
    audio_recorder_destroy(&recorder);
}

static void test_ai_start_session_json_declares_codecs(void) {
    AiState *ai = ai_create_ex(
        "https://ai.example", "DEV001", "token", "audio.g711a",
        "alaw_8khz", "opus_16khz");
    assert(ai != NULL);
    char *json = ai_test_build_start_session_json(ai, "request-1");
    assert(json != NULL);
    assert(strstr(
               json,
               "\"input_audio\":{\"codec\":\"g711a\","
               "\"sample_rate\":8000,\"channels\":1}") != NULL);
    assert(strstr(
               json,
               "\"output_audio\":{\"codec\":\"opus\","
               "\"sample_rate\":16000,\"channels\":1}") != NULL);
    free(json);
    ai_destroy(ai);

    assert(ai_create_ex(
               "https://ai.example", "DEV001", "token", "audio.aac",
               "aac_adts_16khz", "alaw_8khz") == NULL);
}

static void test_media_subscription_policy(void) {
    MediaSubscriptionPolicy policy = {0};

    media_subscription_policy_prepare(&policy, 0);
    assert(!media_subscription_policy_video_enabled(&policy));
    assert(!media_subscription_policy_subscribe_video(&policy));

    media_subscription_policy_prepare(&policy, 1);
    assert(media_subscription_policy_video_enabled(&policy));
    media_subscription_policy_unsubscribe_video(&policy);
    assert(!media_subscription_policy_video_enabled(&policy));
    assert(media_subscription_policy_subscribe_video(&policy));
    assert(media_subscription_policy_video_enabled(&policy));

    media_subscription_policy_reset(&policy);
    assert(!media_subscription_policy_video_enabled(&policy));
    assert(!media_subscription_policy_subscribe_video(&policy));
}

static void test_service_discovery_parser(void) {
    static const char valid[] =
        "{"
        "\"device-srv\":\"https://device.example\","
        "\"voip-srv\":\"https://voip.example\","
        "\"ai-srv\":\"https://ai.example\","
        "\"call-srv\":\"https://call.example\","
        "\"mqtt-srv\":\"mqtts://mqtt.example:8883\","
        "\"tirtc-srv\":\"http://rtc.example\""
        "}";
    DeviceServices services;
    assert(device_services_parse_json(&services, valid) == 0);
    assert(strcmp(services.device_server, "https://device.example") == 0);
    assert(strcmp(services.voip_server, "https://voip.example") == 0);
    assert(strcmp(services.ai_server, "https://ai.example") == 0);
    assert(strcmp(services.call_server, "https://call.example") == 0);
    assert(strcmp(services.mqtt_host, "mqtt.example") == 0);
    assert(services.mqtt_port == 8883);
    assert(services.mqtt_tls == 1);
    assert(strcmp(services.tirtc_endpoint, "http://rtc.example") == 0);

    assert(device_services_parse_json(
               &services,
               "{\"device-srv\":\"d\",\"voip-srv\":\"v\","
               "\"ai-srv\":\"a\",\"mqtt-srv\":\"mqtt://m:1883\","
               "\"tirtc-srv\":\"r\"}") != 0);
    assert(device_services_parse_json(
               &services,
               "{\"device-srv\":\"d\",\"voip-srv\":\"v\","
               "\"ai-srv\":\"a\",\"call-srv\":\"c\","
               "\"mqtt-srv\":\"mqtt://m:1883\"}") != 0);
    assert(device_services_parse_json(
               &services,
               "{\"device-srv\":\"d\",\"voip-srv\":\"v\","
               "\"ai-srv\":\"a\",\"call-srv\":\"c\","
               "\"mqtt-srv\":\"http://m:1883\","
               "\"tirtc-srv\":\"r\"}") != 0);
    assert(device_services_parse_json(
               &services,
               "{\"device-srv\":\"d\",\"voip-srv\":\"v\","
               "\"ai-srv\":\"a\",\"call-srv\":\"c\","
               "\"mqtt-srv\":\"mqtt://m:0\","
               "\"tirtc-srv\":\"r\"}") != 0);
    assert(device_services_parse_json(
               &services,
               "{\"device-srv\":\"d\",\"voip-srv\":\"v\","
               "\"ai-srv\":\"a\",\"call-srv\":\"c\","
               "\"mqtt-srv\":\"mqtt://m:1883junk\","
               "\"tirtc-srv\":\"r\"}") != 0);
    assert(device_services_parse_json(
               &services,
               "{\"device-srv\":\"d\",\"voip-srv\":\"v\","
               "\"ai-srv\":\"a\",\"call-srv\":\"c\","
               "\"mqtt-srv\":\"mqtt://m:1883\","
               "\"tirtc-srv\":\"r\"} trailing") != 0);
    assert(device_services_parse_json(&services, "[]") != 0);
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

static void test_force_key_realigns_audio(void) {
    char audio_path[] = "/tmp/tirtc-c-key-audio-XXXXXX";
    char video_path[] = "/tmp/tirtc-c-key-video-XXXXXX";
    int audio_fd = mkstemp(audio_path);
    int video_fd = mkstemp(video_path);
    assert(audio_fd >= 0 && video_fd >= 0);

    unsigned char audio[8 * 320];
    for (size_t packet = 0; packet < 8; ++packet)
        memset(audio + packet * 320, (int)packet, 320);
    static const unsigned char h264[] = {
        0, 0, 0, 1, 0x67, 0x01,
        0, 0, 0, 1, 0x65, 0x02,
        0, 0, 0, 1, 0x41, 0x03,
        0, 0, 0, 1, 0x41, 0x04,
        0, 0, 0, 1, 0x67, 0x05,
        0, 0, 0, 1, 0x65, 0x06,
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
    assert(source.video_count == 4);

    const unsigned char *payload;
    size_t length;
    double duration;
    int key = 0;
    assert(file_media_source_next_audio(
               &source, &payload, &length, &duration));
    assert(payload[0] == 0);
    assert(file_media_source_next_video(
               &source, &payload, &length, &key, 0));
    assert(key);
    assert(file_media_source_next_video(
               &source, &payload, &length, &key, 1));
    assert(key);
    assert(file_media_source_next_audio(
               &source, &payload, &length, &duration));
    /* Forced IDR is video frame 3, or 200 ms at 15 fps. */
    assert(payload[0] == 5);

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
    int *values;
    int *count;
    int value;
} DeferredMark;

static void mark_deferred(void *opaque) {
    DeferredMark *mark = opaque;
    mark->values[(*mark->count)++] = mark->value;
}

static void copy_deferred(void *opaque, const void *data, size_t length) {
    DeferredMark *mark = opaque;
    assert(length == 1);
    mark->values[(*mark->count)++] = *(const unsigned char *)data;
}

static void test_callback_control_queue(void) {
    SdkCallbackGuard guard = SDK_CALLBACK_GUARD_INITIALIZER;
    int values[3] = {0};
    int count = 0;
    DeferredMark first = {values, &count, 1};
    DeferredMark second = {values, &count, 2};

    assert(sdk_callback_guard_start(&guard) == 0);
    sdk_callback_enter(&guard);
    assert(sdk_defer_action(&guard, mark_deferred, &first) == 0);
    assert(sdk_defer_action(&guard, mark_deferred, &second) == 0);
    unsigned char copied = 3;
    assert(sdk_defer_copy_action(
               &guard, copy_deferred, &second,
               &copied, sizeof(copied)) == 0);
    sleep_ms(10);
    assert(count == 0);
    sdk_callback_leave(&guard);
    sdk_callback_wait_all(&guard);
    assert(count == 3);
    assert(values[0] == 1 && values[1] == 2 && values[2] == 3);
    sdk_callback_guard_stop(&guard);
    assert(sdk_defer_action(&guard, mark_deferred, &first) != 0);
}

typedef struct {
    int starts;
    int stops;
} CoordinatorAdapterContext;

typedef struct {
    DeviceBusiness held;
    int acquires;
    int releases;
    int started;
    int stopped;
    int flushes;
} CoordinatorProductContext;

static int coordinator_resource_acquire(void *opaque,
                                        DeviceBusiness business) {
    CoordinatorProductContext *context = opaque;
    assert(context->held == DEVICE_BUSINESS_NONE);
    context->held = business;
    context->acquires++;
    return 0;
}

static void coordinator_resource_release(void *opaque,
                                         DeviceBusiness business) {
    CoordinatorProductContext *context = opaque;
    assert(context->held == business);
    context->held = DEVICE_BUSINESS_NONE;
    context->releases++;
}

static void coordinator_product_notify(void *opaque,
                                       const DeviceProductEvent *event) {
    CoordinatorProductContext *context = opaque;
    if (event->type == DEVICE_SESSION_STARTED) context->started++;
    if (event->type == DEVICE_SESSION_STOPPED) context->stopped++;
}

static void coordinator_sink_flush(void *opaque, DeviceBusiness business,
                                   uint64_t generation) {
    CoordinatorProductContext *context = opaque;
    assert(business != DEVICE_BUSINESS_NONE && generation > 0);
    context->flushes++;
}

static int coordinator_start(void *opaque) {
    CoordinatorAdapterContext *context = opaque;
    context->starts++;
    return 0;
}

static void coordinator_stop(void *opaque) {
    CoordinatorAdapterContext *context = opaque;
    context->stops++;
}

static void test_coordinator_switching(void) {
    device_adapter_reset_for_testing();
    CoordinatorProductContext product = {0};
    DeviceAdapterV1 product_adapter = {
        .abi_version = DEVICE_ADAPTER_ABI_V1,
        .struct_size = sizeof(product_adapter),
        .media_sink = {
            .context = &product,
            .flush = coordinator_sink_flush,
        },
        .product = {
            .context = &product,
            .notify = coordinator_product_notify,
        },
        .resource = {
            .context = &product,
            .acquire = coordinator_resource_acquire,
            .release = coordinator_resource_release,
        },
    };
    assert(device_adapter_install(&product_adapter) == 0);
    SessionCoordinator coordinator;
    CoordinatorAdapterContext stream = {0};
    CoordinatorAdapterContext voip = {0};
    CoordinatorAdapterContext ai = {0};
    CoordinatorAdapterContext call = {0};
    SessionAdapter stream_adapter = {coordinator_start, coordinator_stop, &stream};
    SessionAdapter voip_adapter = {coordinator_start, coordinator_stop, &voip};
    SessionAdapter ai_adapter = {coordinator_start, coordinator_stop, &ai};
    SessionAdapter call_adapter = {coordinator_start, coordinator_stop, &call};

    assert(session_coordinator_init(
               &coordinator, &stream_adapter, &voip_adapter,
               &ai_adapter, &call_adapter) == 0);
    assert(session_coordinator_start_stream(&coordinator) == 0);
    assert(session_coordinator_begin(&coordinator, SESSION_VOIP) == 0);
    assert(session_coordinator_begin(&coordinator, SESSION_VOIP) != 0);
    session_coordinator_finish(&coordinator, SESSION_VOIP);
    assert(session_coordinator_current(&coordinator) == SESSION_STREAM);
    assert(stream.starts == 2);
    assert(voip.starts == 1 && voip.stops == 1);
    session_coordinator_destroy(&coordinator);
    assert(product.held == DEVICE_BUSINESS_NONE);
    assert(product.acquires == 3 && product.releases == 3);
    assert(product.started == 3 && product.stopped == 3);
    assert(product.flushes == 3);
    device_adapter_reset_for_testing();
    assert(linux_device_adapter_install_default() == 0);
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
    assert(session_coordinator_init(
               &coordinator, &stream_adapter, &voip_adapter,
               &ai_adapter, &call_adapter) == 0);
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

static void test_audio_only_call_downgrades_incoming_video(void) {
    CallState *call = call_create_ex(
        "https://call.example", "device-1", "token",
        "audio.g711a", "alaw_8khz", "", "h264");
    assert(call);
    cJSON *incoming = cJSON_Parse(
        "{\"room_id\":\"room-video\",\"caller_id\":\"caller-1\","
        "\"call_type\":\"video\"}");
    assert(incoming);
    call_on_device_call_incoming(call, incoming);
    assert(strcmp(call->pending_call_type, "audio") == 0);
    cJSON_Delete(incoming);
    call_destroy(call);
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

typedef struct {
    int accepted;
    int disconnected;
    int audio;
    int commands;
} RuntimeCallbackCounts;

static RuntimeCallbackCounts runtime_stream_counts;
static RuntimeCallbackCounts runtime_voip_counts;
static SdkCallbackGuard runtime_service_guard =
    SDK_CALLBACK_GUARD_INITIALIZER;

static void runtime_stream_accepted(tirtc_conn_t hconn) {
    assert(hconn);
    runtime_stream_counts.accepted++;
}

static void runtime_stream_disconnected(tirtc_conn_t hconn) {
    assert(hconn);
    runtime_stream_counts.disconnected++;
}

static void runtime_stream_audio(tirtc_conn_t hconn,
                                 const TIRTCFRAMEINFO *frame, void *data) {
    assert(hconn && frame && data);
    runtime_stream_counts.audio++;
}

static void runtime_stream_command(tirtc_conn_t hconn, uint32_t command,
                                   const void *data, uint32_t length) {
    assert(hconn && command == 0x2000 && data && length == 1);
    runtime_stream_counts.commands++;
}

static void runtime_voip_audio(tirtc_conn_t hconn,
                               const TIRTCFRAMEINFO *frame, void *data) {
    assert(hconn && frame && data);
    runtime_voip_counts.audio++;
}

static void test_process_runtime_single_sdk_lifecycle(void) {
    tirtc_runtime_test_prepare_lifecycle();

    TIRTCCALLBACKS stream = {0};
    TIRTCCALLBACKS ai = {0};
    assert(tirtc_runtime_register_service(
               TIRTC_SERVICE_STREAM, &stream, NULL) == 0);
    assert(tirtc_runtime_register_service(
               TIRTC_SERVICE_AI, &ai, NULL) == 0);
    assert(tirtc_runtime_start(
               "device-1", "secret-1", "client-1",
               "http://rtc.example") == 0);

    TirtcRuntimeTestSdkStats stats = {0};
    tirtc_runtime_test_sdk_get_stats(&stats);
    assert(stats.set_option_calls == 4);
    assert(stats.init_calls == 1);
    assert(stats.start_calls == 1);
    assert(stats.stop_calls == 0);
    assert(stats.uninit_calls == 0);

    for (int i = 0; i < 100; ++i) {
        uint64_t stream_generation =
            tirtc_runtime_activate(TIRTC_SERVICE_STREAM);
        assert(stream_generation != 0);
        assert(tirtc_runtime_deactivate(
                   TIRTC_SERVICE_STREAM, stream_generation) == 0);
        uint64_t ai_generation =
            tirtc_runtime_activate(TIRTC_SERVICE_AI);
        assert(ai_generation > stream_generation);
        assert(tirtc_runtime_deactivate(
                   TIRTC_SERVICE_AI, ai_generation) == 0);
    }

    tirtc_runtime_test_sdk_get_stats(&stats);
    assert(stats.init_calls == 1);
    assert(stats.start_calls == 1);
    assert(stats.stop_calls == 0);
    assert(stats.uninit_calls == 0);

    tirtc_runtime_stop();
    tirtc_runtime_test_sdk_get_stats(&stats);
    assert(stats.init_calls == 1);
    assert(stats.start_calls == 1);
    assert(stats.stop_calls == 1);
    assert(stats.uninit_calls == 1);
    assert(!tirtc_runtime_is_started());

    /* Process shutdown is idempotent. */
    tirtc_runtime_stop();
    tirtc_runtime_test_sdk_get_stats(&stats);
    assert(stats.stop_calls == 1);
    assert(stats.uninit_calls == 1);
}

static void test_process_runtime_start_failure_cleanup(void) {
    tirtc_runtime_test_prepare_lifecycle();
    TIRTCCALLBACKS stream = {0};
    assert(tirtc_runtime_register_service(
               TIRTC_SERVICE_STREAM, &stream, NULL) == 0);
    tirtc_runtime_test_sdk_configure(0, 0, -40003, 0, 1, 1);

    assert(tirtc_runtime_start(
               "device-1", "secret-1", "client-1", NULL) != 0);
    TirtcRuntimeTestSdkStats stats = {0};
    tirtc_runtime_test_sdk_get_stats(&stats);
    assert(stats.init_calls == 1);
    assert(stats.start_calls == 1);
    assert(stats.stop_calls == 0);
    assert(stats.uninit_calls == 1);
    assert(!tirtc_runtime_is_started());

    /* The same process can retry after a native SDK start failure. */
    tirtc_runtime_test_sdk_configure(0, 0, 0, 0, 1, 1);
    assert(tirtc_runtime_start(
               "device-1", "secret-1", "client-1", NULL) == 0);
    tirtc_runtime_stop();
    tirtc_runtime_test_sdk_get_stats(&stats);
    assert(stats.init_calls == 2);
    assert(stats.start_calls == 2);
    assert(stats.stop_calls == 1);
    assert(stats.uninit_calls == 2);
}

typedef struct {
    pthread_mutex_t lock;
    pthread_cond_t ready;
    int entered;
    int release;
} RuntimeDeferredBlock;

static void runtime_blocking_deferred(void *opaque) {
    RuntimeDeferredBlock *block = opaque;
    pthread_mutex_lock(&block->lock);
    block->entered = 1;
    pthread_cond_broadcast(&block->ready);
    while (!block->release)
        pthread_cond_wait(&block->ready, &block->lock);
    pthread_mutex_unlock(&block->lock);
}

static void *runtime_stop_worker(void *opaque) {
    (void)opaque;
    tirtc_runtime_stop();
    return NULL;
}

static void test_process_runtime_stop_drains_service_work(void) {
    tirtc_runtime_test_prepare_lifecycle();
    TIRTCCALLBACKS stream = {0};
    assert(tirtc_runtime_register_service(
               TIRTC_SERVICE_STREAM, &stream,
               &runtime_service_guard) == 0);
    assert(tirtc_runtime_start(
               "device-1", "secret-1", "client-1", NULL) == 0);

    RuntimeDeferredBlock block = {
        .lock = PTHREAD_MUTEX_INITIALIZER,
        .ready = PTHREAD_COND_INITIALIZER,
    };
    assert(sdk_defer_action(
               &runtime_service_guard, runtime_blocking_deferred,
               &block) == 0);
    pthread_mutex_lock(&block.lock);
    while (!block.entered)
        pthread_cond_wait(&block.ready, &block.lock);
    pthread_mutex_unlock(&block.lock);

    pthread_t stop_thread;
    assert(pthread_create(
               &stop_thread, NULL, runtime_stop_worker, NULL) == 0);
    sleep_ms(50);
    TirtcRuntimeTestSdkStats stats = {0};
    tirtc_runtime_test_sdk_get_stats(&stats);
    assert(stats.stop_calls == 0);
    assert(stats.uninit_calls == 0);

    pthread_mutex_lock(&block.lock);
    block.release = 1;
    pthread_cond_broadcast(&block.ready);
    pthread_mutex_unlock(&block.lock);
    pthread_join(stop_thread, NULL);

    tirtc_runtime_test_sdk_get_stats(&stats);
    assert(stats.stop_calls == 1);
    assert(stats.uninit_calls == 1);
    assert(sdk_defer_action(
               &runtime_service_guard, runtime_blocking_deferred,
               &block) != 0);
    pthread_cond_destroy(&block.ready);
    pthread_mutex_destroy(&block.lock);
    tirtc_runtime_test_prepare_lifecycle();
}

static void test_process_runtime_generation_dispatch(void) {
    memset(&runtime_stream_counts, 0, sizeof(runtime_stream_counts));
    memset(&runtime_voip_counts, 0, sizeof(runtime_voip_counts));
    tirtc_runtime_test_reset();

    TIRTCCALLBACKS stream = {0};
    stream.on_conn_accepted = runtime_stream_accepted;
    stream.on_disconnected = runtime_stream_disconnected;
    stream.on_audio = runtime_stream_audio;
    stream.on_command = runtime_stream_command;
    assert(tirtc_runtime_register_service(
               TIRTC_SERVICE_STREAM, &stream, NULL) == 0);

    TIRTCCALLBACKS voip = {0};
    voip.on_audio = runtime_voip_audio;
    assert(tirtc_runtime_register_service(
               TIRTC_SERVICE_VOIP, &voip, NULL) == 0);

    tirtc_conn_t stream_conn = (tirtc_conn_t)(uintptr_t)0x101;
    TIRTCFRAMEINFO frame = {0};
    unsigned char payload = 0xd5;
    uint64_t stream_generation =
        tirtc_runtime_activate(TIRTC_SERVICE_STREAM);
    assert(stream_generation != 0);
    tirtc_runtime_test_on_conn_accepted(stream_conn);
    assert(runtime_stream_counts.accepted == 1);
    tirtc_runtime_test_on_audio(stream_conn, &frame, &payload);
    tirtc_runtime_test_on_command(
        stream_conn, 0x2000, &payload, sizeof(payload));
    assert(runtime_stream_counts.audio == 1);
    assert(runtime_stream_counts.commands == 1);
    assert(tirtc_runtime_deactivate(
               TIRTC_SERVICE_STREAM, stream_generation) == 0);

    /* Every callback from the cancelled generation is discarded. */
    tirtc_runtime_test_on_audio(stream_conn, &frame, &payload);
    tirtc_runtime_test_on_command(
        stream_conn, 0x2000, &payload, sizeof(payload));
    tirtc_runtime_test_on_disconnected(stream_conn);
    assert(runtime_stream_counts.audio == 1);
    assert(runtime_stream_counts.commands == 1);
    assert(runtime_stream_counts.disconnected == 0);

    uint64_t voip_generation =
        tirtc_runtime_activate(TIRTC_SERVICE_VOIP);
    assert(voip_generation != 0);
    tirtc_conn_t voip_conn = (tirtc_conn_t)(uintptr_t)0x202;
    /* Outbound-only services must reject an unrelated inbound connection
     * instead of binding it to the current generation. */
    tirtc_conn_t unexpected_inbound =
        (tirtc_conn_t)(uintptr_t)0x203;
    tirtc_runtime_test_on_conn_accepted(unexpected_inbound);
    tirtc_runtime_test_on_audio(
        unexpected_inbound, &frame, &payload);
    assert(runtime_voip_counts.audio == 0);
    assert(tirtc_runtime_bind_active_connection(
               TIRTC_SERVICE_STREAM, voip_conn) != 0);
    assert(tirtc_runtime_bind_active_connection(
               TIRTC_SERVICE_VOIP, voip_conn) == 0);
    tirtc_runtime_test_on_audio(voip_conn, &frame, &payload);
    assert(runtime_voip_counts.audio == 1);
    assert(runtime_stream_counts.audio == 1);
    assert(tirtc_runtime_deactivate(
               TIRTC_SERVICE_VOIP, voip_generation) == 0);

    /* Re-entering the same service creates a new generation. */
    uint64_t next_voip_generation =
        tirtc_runtime_activate(TIRTC_SERVICE_VOIP);
    assert(next_voip_generation > voip_generation);
    tirtc_runtime_test_on_audio(voip_conn, &frame, &payload);
    assert(runtime_voip_counts.audio == 1);
    assert(tirtc_runtime_deactivate(
               TIRTC_SERVICE_VOIP, next_voip_generation) == 0);

    /* Repeated service switches reuse the same process runtime and always
     * advance the generation; no connection from an earlier turn survives. */
    uint64_t previous_generation = next_voip_generation;
    for (int i = 0; i < 100; ++i) {
        uint64_t stream_turn =
            tirtc_runtime_activate(TIRTC_SERVICE_STREAM);
        assert(stream_turn > previous_generation);
        assert(tirtc_runtime_deactivate(
                   TIRTC_SERVICE_STREAM, stream_turn) == 0);
        uint64_t voip_turn =
            tirtc_runtime_activate(TIRTC_SERVICE_VOIP);
        assert(voip_turn > stream_turn);
        assert(tirtc_runtime_deactivate(
                   TIRTC_SERVICE_VOIP, voip_turn) == 0);
        previous_generation = voip_turn;
    }
}

int main(void) {
    g_log_level = 100;
    assert(linux_device_adapter_install_default() == 0);
    test_device_adapter_contract();
    test_format_tables();
    test_audio_recorder_files();
    test_ai_start_session_json_declares_codecs();
    test_media_subscription_policy();
    test_service_discovery_parser();
    test_fixed_audio_and_mjpeg();
    test_invalid_amr();
    test_encoded_audio_containers();
    test_annexb_video();
    test_force_key_realigns_audio();
    test_callback_guard();
    test_callback_control_queue();
    test_coordinator_switching();
    test_session_arbiter_policy();
    test_voip_malformed_cancel_and_failed_recovery();
    test_audio_only_call_downgrades_incoming_video();
    test_ai_connect_timeout();
    test_process_runtime_single_sdk_lifecycle();
    test_process_runtime_start_failure_cleanup();
    test_process_runtime_stop_drains_service_work();
    test_process_runtime_generation_dispatch();
    puts("device-sim-c core tests passed");
    return 0;
}
