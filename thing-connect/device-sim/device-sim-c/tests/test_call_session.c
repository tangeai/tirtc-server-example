/* Private HTTP/SDK fault injection; no live server or SDK connection needed. */
#define DEVICE_SIM_TESTING
#define call_connect_to test_connect_to
#include "../src/call_session.c"
#include <assert.h>

static int acquired, requests, hangups, connects, failure;
static CallState *current;

int test_connect_to(const char *peer, const char *token, const char *room,
                    int attempts, int timeout) {
    assert(acquired && strcmp(peer, "caller") == 0);
    assert(strcmp(token, "token") == 0 && strcmp(room, "room-a") == 0);
    assert(attempts == 3 && timeout == 10);
    connects++;
    return failure == 4 ? -1 : 0;
}
static int begin(void *user, int pending) {
    (void)user;
    assert(pending);
    if (failure == 1) return -1;
    acquired = 1;
    return 0;
}
static int request(const char *url, const char *bearer, const char *body,
                   char *output, size_t capacity, long *status) {
    (void)bearer;
    assert(acquired);
    requests++;
    cJSON *json = cJSON_Parse(body);
    assert(json);
    assert(strcmp(cJSON_GetObjectItem(json, "room_id")->valuestring, "room-a") == 0);
    cJSON_Delete(json);
    *status = 200;
    if (strstr(url, "/hangup")) {
        hangups++;
        str_copy(output, capacity, "{\"code\":200}");
        return 0;
    }
    assert(strstr(url, "/device/info"));
    if (failure == 2) return -1;  /* remote commit, response lost */
    if (failure == 3) {
        str_copy(output, capacity, "{\"code\":200,\"data\":{}}");
        return 0;
    }
    if (failure == 5) {
        /* Old request completes after cancellation and another incoming call. */
        current->pending_generation++;
        STR_COPY(current->pending_room_id, "room-b");
    }
    str_copy(output, capacity, "{\"code\":200,\"data\":{\"token\":\"token\"}}");
    return 0;
}
int main(void) {
    http_post_test_hook = request;
    for (failure = 0; failure <= 5; failure++) {
        CallState cs = {0};
        current = &cs;
        acquired = requests = hangups = connects = 0;
        pthread_mutex_init(&cs.lock, NULL);
        cs.before_start_ex = begin;
        cs.pending_call = 1;
        cs.pending_generation = cs.incoming_generation = 1;
        STR_COPY(cs.pending_room_id, "room-a");
        STR_COPY(cs.pending_caller_id, "caller");
        STR_COPY(cs.pending_call_type, "audio");
        STR_COPY(cs.call_server, "https://call.test");
        int rc = call_session_do_accept(&cs);
        if (failure == 0) {
            assert(rc == 0 && cs.active && !cs.pending_call && connects == 1 && hangups == 0);
        } else if (failure == 1) {
            assert(rc != 0 && requests == 0 && cs.pending_call);
        } else {
            assert(rc != 0 && hangups == 1 && !cs.active);
            assert(cs.pending_call == (failure == 5));
            if (failure == 5) assert(strcmp(cs.pending_room_id, "room-b") == 0);
        }
        pthread_mutex_destroy(&cs.lock);
    }
    puts("device-sim-c call transaction tests passed");
    return 0;
}
