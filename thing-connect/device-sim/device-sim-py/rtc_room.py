"""Device-owned Room connection, local PTT and durable assignment reconciliation.

One control worker owns HTTP and transitions; SDK callbacks enqueue bounded
copies. The process runtime owns SDK lifetime and connection generations.
"""
import ctypes
import json
import queue
import threading
import time
import uuid

import requests
import tirtc_sdk as sdk
from media_file_reader import AudioFileReader
from media_formats import AUDIO_FORMATS
from session_coordinator import SessionKind
from sdk_callback_guard import SdkCallbackGuard
from tirtc_runtime import ServiceKind

ROOM_COMMAND = 0x2200


def descriptor(format_name):
    spec = AUDIO_FORMATS[format_name]
    if spec.codec not in ('alaw', 'pcm', 'opus', 'amr'):
        raise ValueError('多人对讲不支持该音频格式')
    return {'codec': 'g711a' if spec.codec == 'alaw' else spec.codec,
            'sample_rate': spec.sample_rate, 'channels': 1}


class RoomError(RuntimeError):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code


class RoomSession:
    def __init__(self, config, runtime, begin, finish, idle):
        self.config, self.runtime = config, runtime
        self.begin, self.finish, self.idle = begin, finish, idle
        self.lock = threading.RLock()
        self.events = queue.Queue(128)
        self.wake = threading.Event()
        self.closed = threading.Event()
        self.worker = None
        self.media_worker = None
        self.guard = SdkCallbackGuard()
        self.generation = 0
        self.rtc_generation = None
        self.conn = None
        self.assignment = None
        self.session_id = ''
        self.state = 'idle'
        self.ptt = False
        self.members = {}
        self.members_synced = False
        self.deadline = 0
        self.next_heartbeat = 0
        self.lease_deadline = 0
        self.heartbeat = 15
        self.lease_seconds = 45
        self.reader = None
        self.next_audio = 0
        self.http = requests.Session()
        self.audio_lock = threading.Lock()
        self.hardware = None
        self.audio_queue = queue.Queue(64)
        if config.hardware_audio:
            from room_audio import RoomAudio
            self.hardware = RoomAudio(config.up_audio_format, config.down_audio_format)
        self.callbacks = self._callbacks()
        # One process-lifetime function pointer; opaque userdata identifies the
        # generation so late callbacks do not require unbounded retained closures.
        self.connect_cb = sdk.ConnectCB(self.guard.wrap(self._connected))
        runtime.register_service(ServiceKind.ROOM, self.callbacks,
                                 accepts_inbound=False, callback_guard=self.guard)

    def _enqueue(self, event):
        try:
            self.events.put_nowait(event)
        except queue.Full:
            # Dropped control events lead to timeout/reconciliation; audio may
            # be dropped without retaining SDK-owned pointers.
            self.wake.set()
            return False
        return True

    def _connected(self, error, conn, userdata):
        generation = int(userdata or 0)
        if not self._enqueue(('connected', generation, conn, error)) and conn:
            # Cleanup is deferred through the runtime guard, never in callback.
            self.guard.defer(sdk.TiRtcDisconnect, conn,
                                               name='room-overflow-disconnect')

    def _callbacks(self):
        cbs = sdk.TIRTCCALLBACKS()
        def disconnected(conn):
            self._enqueue(('disconnected', self.generation, conn))
        def command(conn, command, data, length):
            # The SDK command word may carry a sequence and the response bit.
            if (command & 0xfffe) == ROOM_COMMAND and data and 0 < length <= 32768:
                self._enqueue(('command', self.generation, conn,
                               ctypes.string_at(data, length)))
        cbs.on_disconnected = sdk.OnDisconnCB(disconnected)
        cbs.on_conn_error = sdk.OnConnErrCB(lambda conn, error: disconnected(conn))
        cbs.on_command = sdk.OnCmdCB(command)
        cbs.on_subscribe_audio = sdk.OnSubAudioCB(lambda conn, stream: 0 if stream == 1 else -1)
        # File-mode simulator consumes downlink without recording private room
        # content; a product sink can be supplied through on_audio.
        def audio(conn, frame, data):
            if not data or not frame:
                return
            fi = ctypes.cast(frame, ctypes.POINTER(sdk.TIRTCFRAMEINFO)).contents
            spec = AUDIO_FORMATS[self.config.down_audio_format]
            if (0 < fi.length <= 8192 and fi.stream_id == 1 and
                    fi.media == spec.media and fi.flags == spec.flags):
                try:
                    self.audio_queue.put_nowait((self.generation, conn, fi.media, fi.flags,
                                                ctypes.string_at(data, fi.length)))
                except queue.Full:
                    pass
        cbs.on_audio = sdk.OnAudioCB(audio)
        cbs._refs = [cbs.on_disconnected, cbs.on_conn_error, cbs.on_command,
                     cbs.on_subscribe_audio, cbs.on_audio]
        self.on_audio = lambda media, flags, payload: None
        return cbs

    def start(self):
        self.worker = threading.Thread(target=self._run, name='room-control', daemon=True)
        self.worker.start()
        self.media_worker = threading.Thread(target=self._media_loop, name="room-media", daemon=True)
        self.media_worker.start()

    def shutdown(self):
        self.closed.set()
        self.wake.set()
        if self.worker:
            self.worker.join()
        if self.media_worker:
            self.media_worker.join()
        self.stop_service()
        # The control worker has exited, so a queued release cannot be delivered.
        with self.lock:
            final_presence = self._presence('suspended') if self.assignment and self.session_id else None
        try:
            if final_presence:
                self._report(final_presence)
        except Exception:
            print('[room] 会话释放上报失败，将在租约到期后自动释放', flush=True)
        finally:
            self.http.close()

    def sync(self):
        self.wake.set()

    def start_service(self):
        with self.lock:
            self.rtc_generation = self.runtime.activate(ServiceKind.ROOM)

    def stop_service(self):
        with self.lock:
            self.generation += 1
            self.ptt = False
            conn, self.conn = self.conn, None
            prior = self._presence('suspended') if self.assignment else None
            self.state = 'idle'
            self.reader = None
            self.members = {}
            self.members_synced = False
            if conn:
                self._send(conn, {'jsonrpc': '2.0', 'method': 'leave_room'})
                sdk.TiRtcDisconnect(conn)
            if self.rtc_generation is not None:
                self.runtime.deactivate(ServiceKind.ROOM, self.rtc_generation)
                self.rtc_generation = None
            if prior and prior['session_id']:
                self._enqueue(('report', prior))
        with self.audio_lock:
            if self.hardware:
                self.hardware.close()
        self.sync()

    def set_ptt(self, pressed):
        # Release closes the media gate synchronously, including while HTTP is
        # waiting. Only local terminal/button handlers call this method.
        with self.lock:
            self.ptt = bool(pressed) and self.state == 'joined'
            if self.conn:
                self._send(self.conn, {'jsonrpc': '2.0', 'method': 'set_mic_state',
                                      'params': {'mic_state': 'speaking' if self.ptt else 'off'}})

    def command(self, parts):
        if len(parts) < 2 or parts[0] != 'room':
            return False
        if parts[1:] == ['ptt', 'down']:
            self.set_ptt(True)
        elif parts[1:] == ['ptt', 'up']:
            self.set_ptt(False)
        elif parts[1:] == ['status']:
            with self.lock:
                self._print_status()
                self._request_members()
        elif len(parts) >= 2 and parts[1] in ('create', 'join', 'leave'):
            self._enqueue(('operation', tuple(parts[1:])))
        else:
            print('room create [四位密码] | room join 六位房间号 [密码] | room leave | room status | room ptt down/up')
        return True

    def _request_members(self):
        # Called under the session lock so a query cannot target a replaced connection.
        if self.state != 'joined' or not self.conn:
            return
        rc = self._send(self.conn, {'jsonrpc': '2.0', 'method': 'get_room_snapshot'})
        print('[room] 正在查询房间成员…' if rc > 0 else
              f'[room] 成员查询发送失败 code={rc}，请输入 room status 重试', flush=True)

    def _print_status(self):
        # Call under the session lock; never print raw member payloads or credentials.
        def label(value):
            return ''.join(c for c in str(value or '—')[:128] if c.isprintable())
        assignment = self.assignment or {}
        state = {'joined': '已加入', 'joining': '正在加入', 'idle': '未连接'}.get(self.state, self.state)
        print(f"[room] {state} | 房间号：{label(assignment.get('room_code'))} | 房间 ID：{label(assignment.get('room_id'))}", flush=True)
        if self.state != 'joined' or not self.members_synced:
            print('[room] 在线成员：等待房间同步', flush=True)
            return
        print(f'[room] 在线成员（{len(self.members)}）：', flush=True)
        for i, member in enumerate(self.members.values(), 1):
            device = member.get('device_id') or member.get('participant_id')
            own = '（本机）' if member.get('device_id') == self.config.device_id else ''
            mic = '正在说话' if member.get('mic_state') == 'speaking' else '收听中'
            print(f'[room]   {i}. {label(device)}{own} · {mic}', flush=True)

    def _api(self, method, path, body=None, key=None):
        headers = {'Authorization': 'Bearer ' + self.config.mqtt_token}
        if key:
            headers['Idempotency-Key'] = key
        response = self.http.request(method, self.config.call_server.rstrip('/') +
                                     '/v1/call/room/device/' + path, json=body,
                                     headers=headers, timeout=5)
        if len(response.content) > 65536:
            raise RoomError(50200, '房间响应过大')
        data = response.json()
        if data.get('code') != 200:
            raise RoomError(data.get('code', 50000), data.get('msg', '房间请求失败'))
        return data.get('data')

    def _presence(self, state):
        a = self.assignment or {}
        return {'room_id': a.get('room_id', ''), 'assignment_version': a.get('assignment_version', 0),
                'session_id': self.session_id, 'state': state}

    @staticmethod
    def _send(conn, message):
        raw = json.dumps(message, separators=(',', ':')).encode()
        return sdk.TiRtcSendCommand(conn, ROOM_COMMAND, raw, len(raw))

    def _reconcile(self):
        a = self._api('GET', 'assignment')
        with self.lock:
            changed = (not self.assignment or
                       a['assignment_version'] != self.assignment['assignment_version'])
        if changed or a['desired_state'] != 'joined':
            self.finish()
            with self.lock:
                self.assignment = a
                if changed:
                    self.session_id = ''
        if a['desired_state'] != 'joined':
            return
        if not self.idle():
            with self.lock:
                if self.state == 'idle':
                    if not self.session_id:
                        self.session_id = uuid.uuid4().hex
                    suspended = self._presence('suspended')
                else:
                    return
            self._report(suspended)
            return
        with self.lock:
            if self.state != 'idle':
                return
            self.assignment = a
            self.session_id = uuid.uuid4().hex
            p = self._presence('connecting')
        credentials = self._api('POST', 'connect-token', p)
        with self.lock:
            self.heartbeat = max(1, credentials.get('heartbeat_seconds', 15))
            self.lease_seconds = max(3, credentials.get('lease_seconds', 45))
        def connect():
            with self.lock:
                self.generation += 1
                generation = self.generation
                self.state = 'connecting'
                self.deadline = time.monotonic() + 10
                rc = sdk.TiRtcWhipConnect(credentials['peer_id'].encode(),
                                         credentials['token'].encode(), self.connect_cb,
                                         ctypes.c_void_p(generation))
                if rc:
                    raise RoomError(rc, '多人对讲连接失败')
        try:
            self.begin(connect)
        except Exception:
            self._api('POST', 'presence', {**p, 'state': 'connect_failed'})
            raise

    def _report(self, presence):
        try:
            self._api('POST', 'presence', presence)
        except RoomError as exc:
            # A queued report may refer to the session just preempted or left.
            # It must never tear down a newer connection.
            if exc.code not in (40921, 40400):
                raise

    def _event(self, event):
        kind = event[0]
        if kind == 'operation':
            parts = event[1]
            body = {}
            if parts[0] == 'create':
                body['password'] = parts[1] if len(parts) == 2 else ''
            elif parts[0] == 'join':
                if len(parts) not in (2, 3):
                    print('room join 六位房间号 [四位密码]', flush=True)
                    return
                body = {'room_code': parts[1], 'password': parts[2] if len(parts) == 3 else ''}
            try:
                self._api('POST', parts[0], body, uuid.uuid4().hex)
            except Exception as exc:
                print('[room] 操作失败 code=' + str(getattr(exc, 'code', 50200)), flush=True)
            self.sync()
            return
        if kind == 'report':
            self._report(event[1])
            return
        self.guard.wait_for_idle()
        _, generation, conn, *args = event
        with self.lock:
            if generation != self.generation or (kind != 'connected' and conn != self.conn):
                if kind == 'connected' and conn:
                    sdk.TiRtcDisconnect(conn)
                return
            if kind == 'connected':
                if args[0] or not conn:
                    raise RoomError(50200, '多人对讲连接失败')
                if not self.runtime.bind_active_connection(ServiceKind.ROOM, conn):
                    sdk.TiRtcDisconnect(conn)
                    return
                self.conn = conn
                self.state = 'joining'
                self.deadline = time.monotonic() + 8
                self._send(conn, {'jsonrpc': '2.0', 'id': 1, 'method': 'join_room',
                                  'params': {'room_id': self.assignment['room_id'],
                                             'device_id': self.config.device_id,
                                             'input_audio': descriptor(self.config.up_audio_format),
                                             'output_audio': descriptor(self.config.down_audio_format)}})
            elif kind == 'disconnected':
                raise RoomError(50200, '房间连接已断开')
            elif kind == 'audio':
                if self.state == 'joined':
                    self.on_audio(*args)
            elif kind == 'command':
                self._signal(json.loads(args[0]))

    def _signal(self, message):
        if message.get('jsonrpc') != '2.0':
            return
        if message.get('id') == 1 and self.state == 'joining':
            result = message.get('result', {})
            if ('error' in message or not result.get('session_id') or
                    result.get('input_audio') != descriptor(self.config.up_audio_format) or
                    result.get('output_audio') != descriptor(self.config.down_audio_format)):
                raise RoomError(50200, '房间未接受设备音频格式')
            self.state = 'joined'
            self._print_status()
            self.ptt = False
            self.lease_deadline = time.monotonic() + self.lease_seconds
            self.next_heartbeat = 0
            if self.config.up_audio_file and not self.hardware:
                self.reader = AudioFileReader(self.config.up_audio_file,
                                               self.config.up_audio_format)
            self._send(self.conn, {'jsonrpc': '2.0', 'method': 'set_mic_state',
                                   'params': {'mic_state': 'off'}})
            self._request_members()
        params = message.get('params', {})
        if params.get('room_id') not in (None, self.assignment['room_id']):
            return
        method = message.get('method')
        if method == 'room_snapshot':
            participants = params.get('participants', [])
            if len(participants) > 100:
                raise RoomError(50200, '房间成员列表超限')
            self.members = {p['participant_id']: p for p in participants}
            self.members_synced = True
            self._print_status()
        elif method == 'participant_joined':
            p = params.get('participant', {})
            if p.get('participant_id') and (p['participant_id'] in self.members or len(self.members) < 100):
                changed = self.members.get(p['participant_id']) != p
                self.members[p['participant_id']] = p
                if changed:
                    self._print_status()
        elif method == 'participant_left':
            if self.members.pop(params.get('participant_id'), None) is not None:
                self._print_status()
        elif method == 'participant_mic_state_changed':
            member = self.members.get(params.get('participant_id'))
            if member:
                member['mic_state'] = params.get('mic_state', 'off')
        elif method == 'room_closed':
            raise RoomError(40400, '房间连接已关闭，正在同步')

    def _media_loop(self):
        while not self.closed.wait(.005):
            try:
                with self.lock:
                    generation, conn = self.generation, self.conn
                    joined, pressed = self.state == 'joined', self.ptt
                    reader = self.reader
                payload, duration = None, 20
                with self.audio_lock:
                    if self.hardware:
                        if joined:
                            self.hardware.open_speaker()
                        if joined and pressed:
                            payload, duration = self.hardware.capture()
                        else:
                            self.hardware.release_mic()
                    elif joined and pressed and reader and time.monotonic() >= self.next_audio:
                        packet = reader.next_packet()
                        if packet:
                            payload, duration = packet.payload, packet.duration_ms
                    for _ in range(16):
                        try:
                            gen, handle, media, flags, audio = self.audio_queue.get_nowait()
                        except queue.Empty:
                            break
                        if gen == generation and handle == conn and joined:
                            if self.hardware:
                                self.hardware.play(media, flags, audio)
                            else:
                                self.on_audio(media, flags, audio)
                if payload:
                    spec = AUDIO_FORMATS[self.config.up_audio_format]
                    frame = sdk.TIRTCFRAMEINFO()
                    frame.stream_id, frame.media, frame.flags = 1, spec.media, spec.flags
                    frame.ts, frame.length = int(time.monotonic()*1000)&0xffffffff, len(payload)
                    data = ctypes.create_string_buffer(payload)
                    with self.lock:
                        if generation != self.generation or conn != self.conn or not self.ptt:
                            continue
                        rc = sdk.TiRtcSendAudioStream(conn, ctypes.byref(frame), data)
                        if rc in sdk.CONN_FATAL_ERRORS:
                            self._enqueue(('disconnected', generation, conn))
                    self.next_audio = time.monotonic() + duration/1000
            except Exception:
                self.set_ptt(False)
                self.sync()

    def _run(self):
        next_sync, retry = 0, 1
        while not self.closed.is_set():
            try:
                try:
                    self._event(self.events.get(timeout=.01))
                except queue.Empty:
                    pass
                now = time.monotonic()
                with self.lock:
                    expired = ((self.state in ('connecting', 'joining') and now >= self.deadline) or
                               (self.state == 'joined' and now >= self.lease_deadline))
                    heartbeat = self.state == 'joined' and now >= self.next_heartbeat
                    presence = self._presence('joined')
                if expired:
                    raise RoomError(50200, '房间连接或续租超时')
                if heartbeat:
                    self._api('POST', 'presence', presence)
                    with self.lock:
                        self.lease_deadline = time.monotonic() + self.lease_seconds
                        self.next_heartbeat = time.monotonic() + self.heartbeat
                if now >= next_sync or self.wake.is_set():
                    self.wake.clear()
                    self._reconcile()
                    next_sync, retry = time.monotonic() + 3, 1

            except Exception as exc:
                # Never print HTTP exceptions containing request credentials.
                if getattr(exc, 'code', None) == 40921:
                    print('[room] 正在同步房间会话，稍后自动重连', flush=True)
                else:
                    print('[room] 同步失败 code=' + str(getattr(exc, 'code', 50200)), flush=True)
                self.finish()
                self.closed.wait(retry)
                retry = min(30, retry * 2)
                next_sync = 0
