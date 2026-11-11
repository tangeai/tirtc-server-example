/* Test composition-root admission with the real arbiter and no network. */
#define call_on_device_call_incoming test_incoming
#define call_session_reject_incoming_payload_async test_reject
#define call_session_has_pending test_has_pending
#include "../src/main.c"
#include <assert.h>

static int delivered, rejected;
void test_incoming(void *ctx, const cJSON *payload) {
    CallState *cs = ctx;
    (void)payload;
    delivered++;
    cs->pending_call = 1;
}
int test_reject(CallState *cs, const cJSON *payload, const char *reason) {
    (void)cs; (void)payload;
    assert(strcmp(reason, "busy") == 0);
    rejected++;
    return 0;
}
int test_has_pending(CallState *cs) { return cs->pending_call; }
static int start(void *user) { (void)user; return 0; }
static void stop(void *user) { (void)user; }
int main(void) {
    DeviceRuntime rt = {0};
    CallState cs = {0};
    rt.call = &cs;
    SessionAdapter adapter = {start, stop, NULL};
    assert(session_coordinator_init(&rt.coordinator, &adapter, &adapter, &adapter, &adapter) == 0);
    session_arbiter_init(&rt.arbiter, &rt.coordinator);
    cJSON *incoming = cJSON_Parse("{\"room_id\":\"room-a\",\"caller_id\":\"caller\"}");
    _mqtt_call_incoming(&rt, incoming);
    _mqtt_call_incoming(&rt, incoming);
    assert(delivered == 1 && !rejected);
    SessionLease lease;
    assert(session_arbiter_begin_id(&rt.arbiter, SESSION_CALL, 1, "room-a", &lease) == 0);
    _mqtt_call_incoming(&rt, incoming);
    assert(delivered == 1 && !rejected);
    cJSON_Delete(incoming);
    incoming = cJSON_Parse("{\"room_id\":\"room-b\",\"caller_id\":\"caller\"}");
    _mqtt_call_incoming(&rt, incoming);
    assert(delivered == 1 && rejected == 1);
    cJSON_Delete(incoming);
    session_arbiter_destroy(&rt.arbiter);
    session_coordinator_destroy(&rt.coordinator);
    puts("device-sim-c incoming idempotency tests passed");
    return 0;
}
