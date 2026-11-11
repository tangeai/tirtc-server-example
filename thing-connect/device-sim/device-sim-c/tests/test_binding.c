/* Exercise callback -> durable identity -> QoS1 ACK without a live broker. */
#define device_identity_save test_identity_save
#define mosquitto_publish test_publish
#include "../src/device_flow.c"
#include <assert.h>

static int saves, publishes, save_error;
static TempMqttCtx *active_ctx;
int test_identity_save(const char *path, const char *did, const char *key) {
    assert(strcmp(path, "test-storage") == 0);
    assert(strcmp(did, "d") == 0 && strcmp(key, "k") == 0);
    /* Persistence cannot hold the callback's mutex. */
    assert(pthread_mutex_trylock(&active_ctx->mtx) == 0);
    pthread_mutex_unlock(&active_ctx->mtx);
    saves++;
    return save_error;
}
int test_publish(struct mosquitto *mq, int *mid, const char *topic,
                 int length, const void *payload, int qos, bool retain) {
    (void)mq;
    assert(saves == 1 && !save_error);
    assert(strcmp(topic, "device/tmp/ack") == 0);
    assert(length == 12 && strcmp(payload, "{\"ack\":true}") == 0);
    assert(qos == 1 && !retain);
    *mid = 7;
    publishes++;
    return MOSQ_ERR_SUCCESS;
}
static void receive(TempMqttCtx *ctx, const char *payload) {
    struct mosquitto_message msg = {0};
    msg.topic = "device/tmp/cmd";
    msg.payload = (void *)payload;
    msg.payloadlen = (int)strlen(payload);
    _temp_on_message(NULL, ctx, &msg);
}
int main(void) {
    for (int scenario = 0; scenario < 4; scenario++) {
        TempMqttCtx ctx = {0};
        active_ctx = &ctx;
        STR_COPY(ctx.client_id, "tmp");
        ctx.ack_mid = -1;
        saves = publishes = 0;
        save_error = scenario == 1 ? -1 : 0;
        pthread_mutex_init(&ctx.mtx, NULL);
        assert(monotonic_cond_init(&ctx.cond) == 0);
        receive(&ctx, "{\"type\":\"auth_grant\",\"payload\":{\"device_id\":1}}");
        assert(!ctx.grant_ready);
        receive(&ctx, scenario >= 2 ? "{\"type\":\"auth_grant\"}" :
                "{\"type\":\"auth_grant\",\"payload\":{\"device_id\":\"d\",\"device_key\":\"k\"}}");
        assert(ctx.grant_ready && !saves && !publishes);
        receive(&ctx, "{\"type\":\"auth_grant\",\"payload\":{\"device_id\":\"other\",\"device_key\":\"other\"}}");
        pthread_mutex_lock(&ctx.mtx);
        _temp_save_and_ack(&ctx, NULL, "test-storage", scenario == 2 ? "d" : "", 64,
                            scenario == 2 ? "k" : "", 256);
        _temp_save_and_ack(&ctx, NULL, "test-storage", "d", 64, "k", 256);
        pthread_mutex_unlock(&ctx.mtx);
        if (scenario == 1 || scenario == 3) {
            assert(!publishes && ctx.done && ctx.connect_rc != 0);
        } else {
            assert(saves == 1 && publishes == 1 && !ctx.done);
            _temp_on_publish(NULL, &ctx, 6);
            assert(!ctx.done);
            _temp_on_publish(NULL, &ctx, 7);
            assert(ctx.done && !ctx.connect_rc);
        }
        pthread_cond_destroy(&ctx.cond);
        pthread_mutex_destroy(&ctx.mtx);
    }
    puts("device-sim-c binding persistence tests passed");
    return 0;
}
