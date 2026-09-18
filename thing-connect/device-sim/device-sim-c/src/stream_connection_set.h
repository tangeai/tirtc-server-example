#ifndef STREAM_CONNECTION_SET_H
#define STREAM_CONNECTION_SET_H

#include <stddef.h>
#include <stdint.h>

#include "media_subscription_policy.h"
#include "tirtc_limits.h"
#include "tirtc/tiRTC.h"

#define STREAM_MAX_CONNECTIONS TIRTC_REFERENCE_MAX_CONNECTIONS

typedef enum {
    STREAM_CONNECTION_FREE = 0,
    STREAM_CONNECTION_PENDING,
    STREAM_CONNECTION_ACTIVE,
} StreamConnectionState;

typedef struct {
    tirtc_conn_t handle;
    StreamConnectionState state;
    MediaSubscriptionPolicy send_policy;
    int down_audio_subscribed;
    int down_video_subscribed;
    unsigned int consecutive_send_failures;
} StreamConnection;

typedef struct {
    StreamConnection clients[STREAM_MAX_CONNECTIONS];
    int video_capable;
} StreamConnectionSet;

void stream_connection_set_init(StreamConnectionSet *set, int video_capable);
int stream_connection_add_pending(StreamConnectionSet *set, tirtc_conn_t handle);
int stream_connection_activate(StreamConnectionSet *set, tirtc_conn_t handle);
int stream_connection_remove(StreamConnectionSet *set, tirtc_conn_t handle);
int stream_connection_is_pending(const StreamConnectionSet *set,
                                 tirtc_conn_t handle);
int stream_connection_is_active(const StreamConnectionSet *set,
                                tirtc_conn_t handle);
size_t stream_connection_active_count(const StreamConnectionSet *set);

int stream_connection_subscribe_audio(StreamConnectionSet *set,
                                      tirtc_conn_t handle);
int stream_connection_subscribe_video(StreamConnectionSet *set,
                                      tirtc_conn_t handle);
void stream_connection_unsubscribe_audio(StreamConnectionSet *set,
                                         tirtc_conn_t handle);
void stream_connection_unsubscribe_video(StreamConnectionSet *set,
                                         tirtc_conn_t handle);
void stream_connection_set_down_audio(StreamConnectionSet *set,
                                      tirtc_conn_t handle, int subscribed);
void stream_connection_set_down_video(StreamConnectionSet *set,
                                      tirtc_conn_t handle, int subscribed);
int stream_connection_accepts_down_audio(const StreamConnectionSet *set,
                                         tirtc_conn_t handle);
int stream_connection_accepts_down_video(const StreamConnectionSet *set,
                                         tirtc_conn_t handle);

size_t stream_connection_snapshot_audio(const StreamConnectionSet *set,
                                        tirtc_conn_t *handles, size_t capacity);
size_t stream_connection_snapshot_video(const StreamConnectionSet *set,
                                        tirtc_conn_t *handles, size_t capacity);
size_t stream_connection_snapshot_all(const StreamConnectionSet *set,
                                      tirtc_conn_t *handles, size_t capacity);

unsigned int stream_connection_note_send_result(StreamConnectionSet *set,
                                                tirtc_conn_t handle,
                                                int success);

#endif
