#include "stream_connection_set.h"

#include <string.h>

static StreamConnection *_find(StreamConnectionSet *set, tirtc_conn_t handle) {
    if (!set || !handle) return NULL;
    for (size_t i = 0; i < STREAM_MAX_CONNECTIONS; ++i) {
        if (set->clients[i].state != STREAM_CONNECTION_FREE &&
            set->clients[i].handle == handle)
            return &set->clients[i];
    }
    return NULL;
}

static const StreamConnection *_find_const(const StreamConnectionSet *set,
                                           tirtc_conn_t handle) {
    if (!set || !handle) return NULL;
    for (size_t i = 0; i < STREAM_MAX_CONNECTIONS; ++i) {
        if (set->clients[i].state != STREAM_CONNECTION_FREE &&
            set->clients[i].handle == handle)
            return &set->clients[i];
    }
    return NULL;
}

void stream_connection_set_init(StreamConnectionSet *set, int video_capable) {
    if (!set) return;
    memset(set, 0, sizeof(*set));
    set->video_capable = video_capable ? 1 : 0;
}

int stream_connection_add_pending(StreamConnectionSet *set, tirtc_conn_t handle) {
    if (!set || !handle) return 0;
    StreamConnection *existing = _find(set, handle);
    if (existing) return existing->state == STREAM_CONNECTION_PENDING;
    for (size_t i = 0; i < STREAM_MAX_CONNECTIONS; ++i) {
        StreamConnection *client = &set->clients[i];
        if (client->state != STREAM_CONNECTION_FREE) continue;
        memset(client, 0, sizeof(*client));
        client->handle = handle;
        client->state = STREAM_CONNECTION_PENDING;
        media_subscription_policy_prepare(&client->send_policy,
                                          set->video_capable);
        return 1;
    }
    return 0;
}

int stream_connection_activate(StreamConnectionSet *set, tirtc_conn_t handle) {
    StreamConnection *client = _find(set, handle);
    if (!client || client->state != STREAM_CONNECTION_PENDING) return 0;
    client->state = STREAM_CONNECTION_ACTIVE;
    return 1;
}

int stream_connection_remove(StreamConnectionSet *set, tirtc_conn_t handle) {
    StreamConnection *client = _find(set, handle);
    if (!client) return 0;
    memset(client, 0, sizeof(*client));
    return 1;
}

int stream_connection_is_pending(const StreamConnectionSet *set,
                                 tirtc_conn_t handle) {
    const StreamConnection *client = _find_const(set, handle);
    return client && client->state == STREAM_CONNECTION_PENDING;
}

int stream_connection_is_active(const StreamConnectionSet *set,
                                tirtc_conn_t handle) {
    const StreamConnection *client = _find_const(set, handle);
    return client && client->state == STREAM_CONNECTION_ACTIVE;
}

size_t stream_connection_active_count(const StreamConnectionSet *set) {
    size_t count = 0;
    if (!set) return 0;
    for (size_t i = 0; i < STREAM_MAX_CONNECTIONS; ++i) {
        if (set->clients[i].state == STREAM_CONNECTION_ACTIVE) ++count;
    }
    return count;
}

int stream_connection_subscribe_audio(StreamConnectionSet *set,
                                      tirtc_conn_t handle) {
    StreamConnection *client = _find(set, handle);
    return client && media_subscription_policy_subscribe_audio(
                         &client->send_policy);
}

int stream_connection_subscribe_video(StreamConnectionSet *set,
                                      tirtc_conn_t handle) {
    StreamConnection *client = _find(set, handle);
    if (!client || !media_subscription_policy_subscribe_video(
                       &client->send_policy))
        return 0;
    client->video_waiting_for_key_frame = 1;
    return 1;
}

void stream_connection_unsubscribe_audio(StreamConnectionSet *set,
                                         tirtc_conn_t handle) {
    StreamConnection *client = _find(set, handle);
    if (client) media_subscription_policy_unsubscribe_audio(&client->send_policy);
}

void stream_connection_unsubscribe_video(StreamConnectionSet *set,
                                         tirtc_conn_t handle) {
    StreamConnection *client = _find(set, handle);
    if (client) {
        media_subscription_policy_unsubscribe_video(&client->send_policy);
        client->video_waiting_for_key_frame = 0;
    }
}

int stream_connection_mark_video_recovery(StreamConnectionSet *set,
                                          tirtc_conn_t handle) {
    StreamConnection *client = _find(set, handle);
    if (!client || client->state != STREAM_CONNECTION_ACTIVE ||
        !media_subscription_policy_video_enabled(&client->send_policy))
        return 0;
    int newly_waiting = !client->video_waiting_for_key_frame;
    client->video_waiting_for_key_frame = 1;
    return newly_waiting;
}

int stream_connection_audio_frame_allowed(const StreamConnectionSet *set,
                                          tirtc_conn_t handle) {
    const StreamConnection *client = _find_const(set, handle);
    return client && client->state == STREAM_CONNECTION_ACTIVE &&
           media_subscription_policy_audio_enabled(&client->send_policy);
}

int stream_connection_video_frame_allowed(const StreamConnectionSet *set,
                                          tirtc_conn_t handle,
                                          int key_frame) {
    const StreamConnection *client = _find_const(set, handle);
    return client && client->state == STREAM_CONNECTION_ACTIVE &&
           media_subscription_policy_video_enabled(&client->send_policy) &&
           (!client->video_waiting_for_key_frame || key_frame);
}

void stream_connection_complete_video_recovery(StreamConnectionSet *set,
                                               tirtc_conn_t handle) {
    StreamConnection *client = _find(set, handle);
    if (client && client->state == STREAM_CONNECTION_ACTIVE)
        client->video_waiting_for_key_frame = 0;
}

void stream_connection_set_down_audio(StreamConnectionSet *set,
                                      tirtc_conn_t handle, int subscribed) {
    StreamConnection *client = _find(set, handle);
    if (client && client->state == STREAM_CONNECTION_ACTIVE)
        client->down_audio_subscribed = subscribed ? 1 : 0;
}

void stream_connection_set_down_video(StreamConnectionSet *set,
                                      tirtc_conn_t handle, int subscribed) {
    StreamConnection *client = _find(set, handle);
    if (client && client->state == STREAM_CONNECTION_ACTIVE)
        client->down_video_subscribed = subscribed ? 1 : 0;
}

int stream_connection_accepts_down_audio(const StreamConnectionSet *set,
                                         tirtc_conn_t handle) {
    const StreamConnection *client = _find_const(set, handle);
    return client && client->state == STREAM_CONNECTION_ACTIVE &&
           client->down_audio_subscribed;
}

int stream_connection_accepts_down_video(const StreamConnectionSet *set,
                                         tirtc_conn_t handle) {
    const StreamConnection *client = _find_const(set, handle);
    return client && client->state == STREAM_CONNECTION_ACTIVE &&
           client->down_video_subscribed;
}

int stream_connection_has_down_media(const StreamConnectionSet *set) {
    if (!set) return 0;
    for (size_t i = 0; i < STREAM_MAX_CONNECTIONS; ++i) {
        const StreamConnection *client = &set->clients[i];
        if (client->state == STREAM_CONNECTION_ACTIVE &&
            (client->down_audio_subscribed || client->down_video_subscribed))
            return 1;
    }
    return 0;
}

int stream_connection_has_send_media(const StreamConnectionSet *set) {
    if (!set) return 0;
    for (size_t i = 0; i < STREAM_MAX_CONNECTIONS; ++i) {
        const StreamConnection *client = &set->clients[i];
        if (client->state != STREAM_CONNECTION_FREE &&
            (media_subscription_policy_audio_enabled(&client->send_policy) ||
             media_subscription_policy_video_enabled(&client->send_policy)))
            return 1;
    }
    return 0;
}

size_t stream_connection_video_subscriber_count(
    const StreamConnectionSet *set) {
    size_t count = 0;
    if (!set) return 0;
    for (size_t i = 0; i < STREAM_MAX_CONNECTIONS; ++i) {
        const StreamConnection *client = &set->clients[i];
        if (client->state == STREAM_CONNECTION_ACTIVE &&
            media_subscription_policy_video_enabled(&client->send_policy))
            count++;
    }
    return count;
}

static size_t _snapshot(const StreamConnectionSet *set, tirtc_conn_t *handles,
                        size_t capacity, int media) {
    size_t count = 0;
    if (!set || !handles) return 0;
    for (size_t i = 0; i < STREAM_MAX_CONNECTIONS && count < capacity; ++i) {
        const StreamConnection *client = &set->clients[i];
        if (client->state == STREAM_CONNECTION_FREE) continue;
        int include = media == 0;
        if (media == 1)
            include = client->state == STREAM_CONNECTION_ACTIVE &&
                      media_subscription_policy_audio_enabled(
                          &client->send_policy);
        else if (media == 2)
            include = client->state == STREAM_CONNECTION_ACTIVE &&
                      media_subscription_policy_video_enabled(
                          &client->send_policy);
        if (include) handles[count++] = client->handle;
    }
    return count;
}

size_t stream_connection_snapshot_audio(const StreamConnectionSet *set,
                                        tirtc_conn_t *handles, size_t capacity) {
    return _snapshot(set, handles, capacity, 1);
}

size_t stream_connection_snapshot_video(const StreamConnectionSet *set,
                                        tirtc_conn_t *handles, size_t capacity) {
    return _snapshot(set, handles, capacity, 2);
}

size_t stream_connection_snapshot_all(const StreamConnectionSet *set,
                                      tirtc_conn_t *handles, size_t capacity) {
    return _snapshot(set, handles, capacity, 0);
}

unsigned int stream_connection_note_send_result(StreamConnectionSet *set,
                                                tirtc_conn_t handle,
                                                int success) {
    StreamConnection *client = _find(set, handle);
    if (!client || client->state != STREAM_CONNECTION_ACTIVE) return 0;
    if (success)
        client->consecutive_send_failures = 0;
    else
        ++client->consecutive_send_failures;
    return client->consecutive_send_failures;
}
