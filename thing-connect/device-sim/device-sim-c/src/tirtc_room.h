#ifndef TIRTC_ROOM_H
#define TIRTC_ROOM_H
#include "session_arbiter.h"
typedef struct RoomState RoomState;
RoomState *room_create(const char *server, const char *device, const char *bearer,
                       const char *audio, const char *up_format, const char *down_format);
int room_register(RoomState *room);
int room_start(RoomState *room, SessionArbiter *arbiter, SessionCoordinator *coordinator);
void room_shutdown(RoomState *room);
/* Destroy only after tirtc_runtime_stop has drained SDK callbacks. */
void room_destroy(RoomState *room);
void room_sync(RoomState *room);
int room_command(RoomState *room, const char *line);
void room_ptt(RoomState *room, int pressed);
#endif
