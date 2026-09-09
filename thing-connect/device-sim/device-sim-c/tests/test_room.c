/* Exercise callback handoff and media gating with an in-process SDK double.
 * Include this private module so fault injection does not add production APIs. */
#include "../src/tirtc_room.c"
#include <assert.h>

static _Atomic int sent_audio, received_audio, disconnects, send_result;
static unsigned char packet_data[160];
static int snapshot_requests;
int TiRtcSendCommand(tirtc_conn_t conn, uint32_t cmd, const void *data, uint32_t len) {
    assert(conn && cmd == ROOM_CMD && data && len);
    cJSON *message = cJSON_ParseWithLength(data, len);
    assert(message);
    if (strcmp(string_field(message, "method"), "get_room_snapshot") == 0) {
        assert(!cJSON_GetObjectItemCaseSensitive(message, "id"));
        snapshot_requests++;
    }
    cJSON_Delete(message);
    return (int)len;
}
int TiRtcSendAudioStream(tirtc_conn_t conn, const TIRTCFRAMEINFO *frame,
                         const void *data) {
    assert(conn && data && frame->length == sizeof(packet_data));
    sent_audio++;
    return send_result;
}
int TiRtcDisconnect(tirtc_conn_t conn) {
    assert(conn);
    disconnects++;
    return 0;
}
static int source_open(void *ctx, const DeviceMediaSourceConfig *cfg, void **handle) {
    (void)ctx;
    assert(cfg->business == DEVICE_BUSINESS_ROOM);
    *handle = packet_data;
    return 0;
}
static int next_audio(void *ctx, void *handle, DeviceMediaPacket *out) {
    (void)ctx;
    assert(handle == packet_data);
    *out = (DeviceMediaPacket){
        .data = packet_data, .length = sizeof(packet_data), .duration_ms = 20};
    return 1;
}
static void source_close(void *ctx, void *handle) {
    (void)ctx;
    assert(handle == packet_data);
}
static int sink(void *ctx, const DeviceDownlinkFrame *frame) {
    (void)ctx;
    assert(frame->business == DEVICE_BUSINESS_ROOM && frame->length == 160);
    assert(((const unsigned char *)frame->data)[0] == 0xd5);
    received_audio++;
    return 0;
}
static void deliver(RoomState *r, const char *text) {
    RoomEvent event = {.type = 3, .conn = r->conn, .generation = r->generation};
    str_copy(event.text, sizeof(event.text), text);
    assert(process_event(r, &event) == 0);
}
int main(void) {
    DeviceAdapterV1 adapter = {.abi_version = DEVICE_ADAPTER_ABI_V1,
                               .struct_size = sizeof(adapter)};
    adapter.media_source.open = source_open;
    adapter.media_source.next_audio = next_audio;
    adapter.media_source.close = source_close;
    adapter.media_sink.submit = sink;
    assert(device_adapter_install(&adapter) == 0);
    RoomState *r = room_create("http://localhost", "test-device", "test-token", "audio",
                               "alaw_8khz", "alaw_8khz");
    assert(r);
    s_room = r;
    r->active = 1;
    r->generation = 2;
    r->conn = (tirtc_conn_t)(uintptr_t)1;
    str_copy(r->room_id, sizeof(r->room_id), "room-1");
    r->lease_seconds = 45;
    deliver(r,
            "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"session_id\":\"s1\",\"input_"
            "audio\":{\"codec\":\"g711a\",\"sample_rate\":8000,\"channels\":1},\"output_"
            "audio\":{\"codec\":\"g711a\",\"sample_rate\":8000,\"channels\":1}}}");
    assert(r->joined && !r->ptt);
    assert(snapshot_requests == 1);
    assert(room_command(r, "room status") == 1);
    assert(snapshot_requests == 2);
    r->joined = 0;
    room_command(r, "room status");
    assert(snapshot_requests == 2);
    r->joined = 1;
    RoomEvent invalid_operation = {.type = 5};
    str_copy(invalid_operation.text, sizeof(invalid_operation.text), "room invalid");
    assert(process_event(r, &invalid_operation) == 0 && r->joined);
    deliver(r, "{\"jsonrpc\":\"2.0\",\"method\":\"room_snapshot\",\"params\":{"
               "\"participants\":[{\"participant_id\":\"p1\"}]}}");
    deliver(r, "{\"jsonrpc\":\"2.0\",\"method\":\"participant_joined\",\"params\":{"
               "\"participant\":{\"participant_id\":\"p1\"}}}");
    assert(cJSON_GetArraySize(r->members) == 1);
    const char *snapshot = "{\"jsonrpc\":\"2.0\",\"method\":\"room_snapshot\",\"params\":{\"participants\":[{\"participant_id\":\"p1\"},{\"participant_id\":\"p2\"}]}}";
    command_cb(r->conn, 0x12342201u, snapshot, (uint32_t)strlen(snapshot));
    assert(r->count == 1);
    RoomEvent snapshot_event = r->queue[r->head];
    r->head = (r->head + 1) % ROOM_QUEUE;
    r->count--;
    assert(process_event(r, &snapshot_event) == 0);
    assert(cJSON_GetArraySize(r->members) == 2);
    command_cb(r->conn, 0x12342101u, snapshot, (uint32_t)strlen(snapshot));
    assert(r->count == 0);

    RoomEvent late = {.type = 1, .conn = (tirtc_conn_t)(uintptr_t)2, .generation = 1};
    assert(process_event(r, &late) == 0 && disconnects == 1 &&
           r->conn == (void *)(uintptr_t)1);
    memset(packet_data, 0xd5, sizeof(packet_data));
    TIRTCFRAMEINFO frame = {.length = sizeof(packet_data),
                            .stream_id = 1,
                            .media = r->down->media,
                            .flags = r->down->flags};
    audio_cb(r->conn, &frame, packet_data);
    packet_data[0] = 0; /* Callback must copy payload before SDK storage is reused. */
    assert(pthread_create(&r->media_worker, NULL, media_worker, r) == 0);
    r->media_started = 1;
    for (int i = 0; i < 100 && !received_audio; i++)
        sleep_ms(5);
    assert(received_audio == 1 && sent_audio == 0);
    room_ptt(r, 1);
    for (int i = 0; i < 100 && !sent_audio; i++)
        sleep_ms(5);
    assert(sent_audio > 0);
    room_ptt(r, 0);
    int count = sent_audio;
    sleep_ms(60);
    assert(sent_audio == count);
    send_result = TIRTC_E_CONN_REMOTECLOSE;
    room_ptt(r, 1);
    for (int i = 0; i < 100 && sent_audio == count; i++)
        sleep_ms(5);
    pthread_mutex_lock(&r->lock);
    assert(!r->ptt);
    pthread_mutex_unlock(&r->lock);
    stop_service(r);
    count = sent_audio;
    sleep_ms(60);
    assert(sent_audio == count && !r->ptt && !r->joined && !r->conn && !r->members);
    room_shutdown(r);
    assert(enqueue(r, 3, NULL, 0, r->generation, "{}") == -1);
    room_destroy(r);
    puts("device-sim-c room protocol/media tests passed");
    return 0;
}
