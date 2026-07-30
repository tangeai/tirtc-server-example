#!/usr/bin/env python3
from __future__ import annotations
"""
rtc_call_session.py — 设备间通话（call-server）状态机

实现 device_flow.connect_mqtt_blocking 期望的 channel=device handler 接口：
  on_device_call_incoming(payload)
  on_room_cancel(payload)
  on_device_call_reject(payload)
  on_device_callers_update(payload)

跟 rtc_voip_session.py 的结构一样：本文件管 HTTP + 状态机，P2P 信令交给 rtc_call.py。
"""

import threading

import requests
import rtc_call
import http_trace
from call_type_policy import CallTypeError, resolve_call_type
from terminal_ui import print_box


def _log(msg):
    if rtc_call._LOG_LEVEL <= 10:
        print(f"\033[0;36m[call]\033[0m {msg}", flush=True)

def _ok(msg):
    if rtc_call._LOG_LEVEL <= 20:
        print(f"\033[0;32m[call]\033[0m {msg}", flush=True)

def _warn(msg):
    if rtc_call._LOG_LEVEL <= 30:
        print(f"\033[1;33m[call]\033[0m {msg}", flush=True)


class CallState:
    """设备间通话状态机，实现 handler 接口供 device_flow 路由消息。"""

    def __init__(self, call_server: str, device_id: str, mqtt_token: str,
                 send_audio: str = "", send_video: str = "", recv_dir: str = "",
                 audio_fmt: str = "alaw_8khz", up_video_fmt: str = "h264",
                 down_video_fmt: str = "h264", before_start=None,
                 after_stop=None, before_accept=None, before_continue=None,
                 before_accept_ticket=None):
        self._call_server = call_server
        self._device_id   = device_id
        self._mqtt_token  = mqtt_token
        self._before_start = before_start or (lambda action: action())
        self._before_accept = before_accept or self._before_start
        self._before_accept_ticket = before_accept_ticket
        self._before_continue = before_continue or self._before_start
        self._after_stop = after_stop or (lambda: None)
        self._video_capable = bool(send_video)
        self._pending_call = None   # {"room_id","caller_id","caller_name","call_type"}
        self._incoming_generation = 0
        self._room_id       = None   # 当前所在房间（主叫或被叫都用这个字段）
        self._role           = None  # "caller" | "callee"
        self._call_type      = None  # "video" | "audio"
        self._lock = threading.Lock()
        self._cancel_timer: "threading.Timer | None" = None
        self._contact_list: list = []
        rtc_call.register_p2p_connected_cb(self._on_p2p_connected)
        rtc_call.register_connect_failed_cb(self._on_connect_failed)
        if send_audio or send_video:
            rtc_call.configure_media(
                device_id, send_audio, send_video, recv_dir,
                audio_fmt, up_video_fmt, down_video_fmt)

    def _headers(self) -> dict:
        return {"Authorization": f"Bearer {self._mqtt_token}", "Content-Type": "application/json"}

    # ── device_flow handler 接口 ──────────────────────────────────────────────

    def on_device_call_incoming(self, p: dict) -> None:
        room_id     = p.get("room_id", "")
        caller_id   = p.get("caller_id", "")
        if not room_id or not caller_id:
            _warn("忽略缺少 room_id/caller_id 的设备来电")
            return
        caller_name = p.get("caller_name", caller_id)
        requested_type = p.get("call_type")
        call_type = (
            "audio"
            if requested_type == "audio"
            or not getattr(self, "_video_capable", True)
            else "video"
        )
        with self._lock:
            self._incoming_generation += 1
            generation = self._incoming_generation
            if self._room_id:
                # 通话中收到新来电：暂存，hangup 后可 accept
                self._pending_call = {
                    "room_id": room_id, "caller_id": caller_id,
                    "caller_name": caller_name, "call_type": call_type,
                    "generation": generation,
                }
                _warn("")
                _warn(f"通话中有新来电！{caller_name}({caller_id}) room={room_id}")
                _warn("已暂存，hangup 挂断当前通话后可 accept 接听")
                _warn("")
                return
            self._pending_call = {
                "room_id": room_id, "caller_id": caller_id,
                "caller_name": caller_name, "call_type": call_type,
                "generation": generation,
            }
        _warn("")
        _warn(f"来电！{caller_name}({caller_id}) room={room_id} type={call_type}")
        _warn("输入 accept 接听，reject 拒接")
        _warn("")

    def on_callee_answered(self, p: dict) -> None:
        room_id   = p.get("room_id", "")
        callee_id = p.get("callee_id", "")
        with self._lock:
            if self._room_id != room_id:
                return
        self._cancel_ring_timer()
        self._before_continue(lambda: None)
        _ok(f"对方正在连接中 callee={callee_id} room={room_id}（等待 P2P 建连）")

    def on_room_cancel(self, p: dict) -> None:
        room_id = p.get("room_id", "")
        reason  = p.get("reason", "")
        with self._lock:
            if self._pending_call and self._pending_call.get("room_id") == room_id:
                self._pending_call = None
                self._incoming_generation += 1
                _warn(f"来电已取消 room_id={room_id} reason={reason}")
                return
            if self._room_id != room_id:
                _log(f"room_cancel room_id={room_id} 跟当前状态不匹配，忽略")
                return
            self._room_id = None
            self._role    = None
            self._call_type = None
            self._incoming_generation += 1
        self._cancel_ring_timer()
        _warn(f"通话已结束 room_id={room_id} reason={reason}")
        if rtc_call.is_active():
            rtc_call.hangup()
        else:
            rtc_call.clear_call_type()
        self._after_stop()

    def on_device_call_reject(self, p: dict) -> None:
        room_id = p.get("room_id", "")
        _warn(f"对方拒接 room_id={room_id} reason={p.get('reason')}")
        with self._lock:
            is_current = bool(self._room_id and self._room_id == room_id)
            if is_current:
                self._room_id = None
                self._role = None
                self._call_type = None
                self._incoming_generation += 1
        if is_current:
            self._cancel_ring_timer()
            rtc_call.clear_expected_room()
            rtc_call.clear_call_type()
            self._after_stop()

    def on_device_callers_update(self, payload: dict | None = None) -> None:
        payload = payload or {}
        action = payload.get("action", "")
        contact_type = payload.get("contact_type", "")
        peer = payload.get("peer_id", "")
        if action == "request":
            _log(f"收到联系人申请 peer={peer}，执行 ct pending 查看并处理")
            return
        labels = {
            "accept": "联系人申请已同意",
            "reject": "联系人申请已拒绝",
            "delete": "联系人已删除",
            "remark": "联系人备注已更新",
        }
        detail = labels.get(action, "联系人数据已变更")
        suffix = f" type={contact_type} peer={peer}" if contact_type or peer else ""
        _log(f"{detail}{suffix}，可执行 ct list / ct pending 刷新")

    def has_pending(self) -> bool:
        with self._lock:
            return self._pending_call is not None

    def expire_pending(self, room_id: str) -> bool:
        """仅清理仍匹配该房间的超时来电，供统一路由的 TTL 使用。"""
        with self._lock:
            if (not self._pending_call
                    or self._pending_call.get("room_id") != room_id):
                return False
            self._pending_call = None
            self._incoming_generation += 1
        _warn(f"设备来电等待接听超时，已清理 room_id={room_id}")
        return True

    def is_outgoing(self) -> bool:
        with self._lock:
            return bool(self._room_id and self._role == "caller")

    def has_room(self) -> bool:
        """本地是否持有服务端房间，包括进程重启后由 room 命令恢复的状态。"""
        with self._lock:
            return bool(self._room_id)

    # ── HTTP 调用 ─────────────────────────────────────────────────────────────

    def do_call(self, target_id: str, call_type: str | None = None) -> None:
        try:
            call_type = resolve_call_type(
                call_type, getattr(self, "_video_capable", True),
                subject="设备通话")
        except CallTypeError as exc:
            _warn(str(exc))
            return
        with self._lock:
            if self._room_id:
                _warn(f"已在房间 {self._room_id} 中，不能发起新呼叫")
                return
        self._before_start(lambda: None)
        rtc_call.set_call_type(call_type)
        url = f"{self._call_server}/v1/call/request"
        payload = {"targets": [target_id], "call_type": call_type}
        headers = self._headers()
        try:
            r = http_trace.request("POST", url, json=payload, headers=headers, timeout=10)
            resp = r.json()
        except (requests.RequestException, ValueError) as e:
            _warn(f"发起呼叫异常: {e}")
            rtc_call.clear_call_type()
            self._after_stop()
            return
        if not isinstance(resp, dict):
            _warn("发起呼叫失败：响应不是 JSON 对象")
            rtc_call.clear_call_type()
            self._after_stop()
            return
        if resp.get("code") == 200:
            data = resp.get("data")
            room_id = (
                data.get("room_id", "") if isinstance(data, dict) else "")
            if not room_id:
                _warn("发起呼叫失败：成功响应缺少 data.room_id")
                rtc_call.clear_call_type()
                self._after_stop()
                return
            with self._lock:
                self._room_id = room_id
                self._role    = "caller"
                self._call_type = call_type
            rtc_call.set_expected_room(room_id)
            _ok(f"已发起呼叫 room_id={room_id}，等待接听（30s 超时）")
            self._start_ring_timer(room_id)
        else:
            _warn(f"发起呼叫失败（code={resp.get('code')}）: {resp.get('msg')}")
            rtc_call.clear_call_type()
            self._after_stop()

    def do_accept(self) -> None:
        with self._lock:
            pc = dict(self._pending_call) if self._pending_call else None
        if not pc:
            _warn("当前没有待接听的来电")
            return
        try:
            r = http_trace.request(
                "POST", f"{self._call_server}/v1/call/device/info",
                json={"device_id": pc["caller_id"], "room_id": pc["room_id"], "purpose": "call"},
                headers=self._headers(), timeout=10)
            resp = r.json()
        except requests.RequestException as e:
            _warn(f"接听异常: {e}")
            return
        if resp.get("code") != 200:
            _warn(f"接听失败（code={resp.get('code')}）: {resp.get('msg')}")
            return
        token = (resp.get("data") or {}).get("token", "")
        if not token:
            _warn("接听失败：响应缺少 token")
            return
        with self._lock:
            current = self._pending_call
            if (not current
                    or current.get("room_id") != pc["room_id"]
                    or current.get("generation") != pc.get("generation")):
                _warn(f"来电已在获取 token 期间取消 room_id={pc['room_id']}")
                return
            self._pending_call = None
            self._room_id = pc["room_id"]
            self._role    = "callee"
            accepted_call_type = (
                "audio" if pc.get("call_type") == "audio" else "video")
            self._call_type = accepted_call_type
        _ok(f"接听成功，正在建立 P2P 连接 room_id={pc['room_id']}")
        try:
            action = lambda: rtc_call.connect_to(
                pc["caller_id"], token, pc["room_id"],
                call_type=accepted_call_type)
            if self._before_accept_ticket:
                self._before_accept_ticket(action, pc["room_id"])
            else:
                self._before_accept(action)
        except BaseException:
            with self._lock:
                if self._room_id == pc["room_id"]:
                    self._room_id = None
                    self._role = None
                    self._call_type = None
                    if (self._incoming_generation == pc.get("generation")
                            and self._pending_call is None):
                        self._pending_call = pc
            rtc_call.clear_call_type()
            raise

    def reject_incoming(self, payload: dict, reason: str = "busy") -> None:
        """拒绝未登记到本地状态机的后来来电，不覆盖首个待接来电。"""
        room_id = payload.get("room_id", "")
        if not room_id:
            return
        try:
            http_trace.request(
                "POST", f"{self._call_server}/v1/call/reject",
                json={"room_id": room_id, "reason": reason},
                headers=self._headers(), timeout=10)
            _ok(f"忙线拒接设备来电 room={room_id}")
        except requests.RequestException as e:
            _warn(f"忙线拒接异常: {e}")

    def do_reject(self, reason: str = "decline") -> None:
        with self._lock:
            pc = dict(self._pending_call) if self._pending_call else None
        if not pc:
            _warn("当前没有待接听的来电")
            return
        with self._lock:
            self._pending_call = None
            self._incoming_generation += 1
        try:
            http_trace.request(
                "POST", f"{self._call_server}/v1/call/reject",
                json={"room_id": pc["room_id"], "reason": reason},
                headers=self._headers(), timeout=10)
            _ok("已拒接")
        except requests.RequestException as e:
            _warn(f"拒接异常: {e}")

    def do_hangup(self) -> None:
        with self._lock:
            room_id = self._room_id
        if not room_id:
            _warn("当前不在通话中")
            return
        self._cancel_ring_timer()
        try:
            http_trace.request(
                "POST", f"{self._call_server}/v1/call/hangup",
                json={"room_id": room_id, "reason": "hangup"},
                headers=self._headers(), timeout=10)
        except requests.RequestException as e:
            _warn(f"挂断异常: {e}")
        rtc_call.hangup()
        rtc_call.clear_expected_room()
        with self._lock:
            self._room_id = None
            self._role    = None
            self._call_type = None
        _ok("挂断完成")
        self._after_stop()

    def _start_ring_timer(self, room_id: str) -> None:
        self._cancel_ring_timer()
        def _timeout():
            with self._lock:
                if self._room_id != room_id:
                    return
            _warn(f"等待超时（30s），自动取消呼叫 room_id={room_id}")
            try:
                http_trace.request(
                    "POST", f"{self._call_server}/v1/call/cancel",
                    json={"room_id": room_id},
                    headers=self._headers(), timeout=10)
            except requests.RequestException as e:
                _warn(f"超时取消异常: {e}")
            rtc_call.clear_expected_room()
            with self._lock:
                if self._room_id == room_id:
                    self._room_id = None
                    self._role    = None
                    self._call_type = None
                    ended = True
                else:
                    ended = False
            if ended:
                rtc_call.clear_call_type()
                self._after_stop()
        with self._lock:
            self._cancel_timer = threading.Timer(30.0, _timeout)
            self._cancel_timer.daemon = True
            self._cancel_timer.start()

    def _cancel_ring_timer(self) -> None:
        with self._lock:
            t = self._cancel_timer
            self._cancel_timer = None
        if t:
            t.cancel()

    def _on_p2p_connected(self, room_id: str) -> None:
        self._cancel_ring_timer()
        _ok(f"P2P 建连成功 room_id={room_id}，通话中")

    def _on_connect_failed(self) -> None:
        with self._lock:
            room_id = self._room_id
        _warn(f"连接主叫全部失败，调用挂断 room_id={room_id}")
        self.do_hangup()

    def do_cancel(self) -> None:
        with self._lock:
            room_id, role = self._room_id, self._role
        if not room_id or role != "caller":
            _warn("当前没有可取消的外呼")
            return
        self._cancel_ring_timer()
        try:
            http_trace.request(
                "POST", f"{self._call_server}/v1/call/cancel",
                json={"room_id": room_id},
                headers=self._headers(), timeout=10)
            _ok("已取消呼叫")
        except requests.RequestException as e:
            _warn(f"取消异常: {e}")
        with self._lock:
            self._room_id = None
            self._role    = None
            self._call_type = None
        rtc_call.clear_expected_room()
        rtc_call.clear_call_type()
        self._after_stop()

    def do_list_contacts(self) -> list:
        """拉取并展示联系人列表，带下标。返回联系人 dict 列表。"""
        try:
            r = http_trace.request(
                "GET", f"{self._call_server}/v1/call/device/contacts",
                headers=self._headers(), timeout=10)
            resp = r.json()
        except requests.RequestException as e:
            _warn(f"拉取联系人异常: {e}")
            return []
        if resp.get("code") != 200:
            _warn(f"拉取联系人失败: {resp.get('msg')}")
            return []
        contacts = resp["data"].get("contacts", [])
        # A successful empty response is authoritative. Clear the cache so a
        # later `call N` cannot dial a contact removed since the previous list.
        self._contact_list = contacts
        if not contacts:
            print_box("联系人列表（共 0 条）", ["当前没有联系人"])
            return []
        rows = []
        for i, c in enumerate(contacts):
            ct = c.get("type", "device")
            if rows:
                rows.append("")
            rows.append(
                f"[{i}] device_id={c.get('device_id', '-')}  type={ct}"
            )
            if ct == "voip":
                remark = c.get('remark', '')
                rows.extend((
                    f"    remark={remark or '-'}  source={c.get('source', '-')}",
                    f"    wx_app_id={c.get('wx_app_id', '-')}",
                    f"    wx_model_id={c.get('wx_model_id', '-')}",
                    f"    wx_open_id={c.get('wx_open_id', '-')}",
                ))
            else:
                rows.append(
                    "    "
                    f"online={c.get('online')}  remark={c.get('remark') or '-'}  "
                    f"source={c.get('source')}"
                )
        print_box(f"联系人列表（共 {len(contacts)} 条）", rows)
        return contacts

    def get_cached_contacts(self) -> list:
        return list(self._contact_list)

    def do_list_pending_contacts(self) -> list:
        """拉取当前设备可以审批的联系人申请。"""
        try:
            r = http_trace.request(
                "GET", f"{self._call_server}/v1/call/device/contacts/pending",
                headers=self._headers(), timeout=10)
            resp = r.json()
        except requests.RequestException as e:
            _warn(f"拉取待审批申请异常: {e}")
            return []
        if resp.get("code") != 200:
            _warn(f"拉取待审批申请失败: {resp.get('msg')}")
            return []
        pending = resp["data"].get("pending", [])
        if not pending:
            print_box("待审批联系人（共 0 条）", ["当前没有待审批申请"])
            return []
        rows = []
        for index, item in enumerate(pending):
            contact_type = item.get("type", "device")
            if rows:
                rows.append("")
            rows.extend((
                f"[{index}] type={contact_type}  "
                f"peer_device_id={item.get('peer_device_id', '-')}",
                f"    created_at={item.get('created_at', '-')}",
            ))
        print_box(f"待审批联系人（共 {len(pending)} 条）", rows)
        return pending

    def do_add_contact(self, target_id: str) -> None:
        url = f"{self._call_server}/v1/call/device/contacts/request"
        payload = {"target_device_id": target_id}
        headers = self._headers()
        try:
            r = http_trace.request("POST", url, json=payload, headers=headers, timeout=10)
            resp = r.json()
        except requests.RequestException as e:
            _warn(f"发起联系人申请异常: {e}")
            return
        if resp.get("code") == 200:
            _ok(f"申请已发送（status={resp['data'].get('status')}）")
        else:
            _warn(f"发起申请失败（code={resp.get('code')}）: {resp.get('msg')}")

    def do_respond_contact(self, peer_id: str, accept: bool) -> None:
        url = f"{self._call_server}/v1/call/device/contacts/respond"
        payload = {
            "peer_device_id": peer_id,
            "action": "accept" if accept else "reject",
        }
        headers = self._headers()
        try:
            r = http_trace.request("POST", url, json=payload, headers=headers, timeout=10)
            resp = r.json()
        except requests.RequestException as e:
            _warn(f"审批联系人申请异常: {e}")
            return
        if resp.get("code") == 200:
            _ok(f"已{'同意' if accept else '拒绝'}")
        else:
            _warn(f"操作失败（code={resp.get('code')}）: {resp.get('msg')}")

    def do_delete_contact(self, peer_id: str) -> None:
        """删除一个已接受的跨账号手动设备联系人。"""
        url = f"{self._call_server}/v1/call/device/contacts"
        params = {"peer_id": peer_id}
        headers = self._headers()
        try:
            r = http_trace.request(
                "DELETE", url, params=params, headers=headers, timeout=10)
            resp = r.json()
        except requests.RequestException as e:
            _warn(f"删除联系人异常: {e}")
            return
        if resp.get("code") == 200:
            self._contact_list = [
                item for item in self._contact_list
                if item.get("type", "device") != "device" or item.get("device_id") != peer_id
            ]
            _ok(f"联系人已删除 peer_id={peer_id}")
        else:
            _warn(f"删除联系人失败（code={resp.get('code')}）: {resp.get('msg')}")

    def do_remark(self, peer_id: str, remark: str) -> None:
        url = f"{self._call_server}/v1/call/device/contacts/remark"
        payload = {"peer_id": peer_id, "remark": remark}
        headers = self._headers()
        try:
            r = http_trace.request("PUT", url, json=payload, headers=headers, timeout=10)
            resp = r.json()
        except requests.RequestException as e:
            _warn(f"修改备注异常: {e}")
            return
        if resp.get("code") == 200:
            _ok(f"备注已更新 peer_id={peer_id}")
        else:
            _warn(f"修改备注失败（code={resp.get('code')}）: {resp.get('msg')}")

    def do_query_room(self) -> None:
        try:
            r = http_trace.request(
                "GET", f"{self._call_server}/v1/call/room",
                headers=self._headers(), timeout=10)
            resp = r.json()
        except requests.RequestException as e:
            _warn(f"查询房间异常: {e}")
            return
        if resp.get("code") != 200:
            _warn(f"查询房间失败: {resp.get('msg')}")
            return
        data = resp.get("data")
        if data is None:
            with self._lock:
                self._room_id = None
                self._role    = None
                self._call_type = None
            rtc_call.clear_call_type()
            _ok("当前不在任何房间")
        else:
            # 服务端有房间但本地状态丢失（进程重启后常见），自动同步
            room_id = data['room_id']
            role    = data['role']
            call_type = (
                "audio"
                if data.get("call_type") == "audio"
                or not getattr(self, "_video_capable", True)
                else "video"
            )
            with self._lock:
                if self._room_id != room_id:
                    _ok(f"同步房间状态: room_id={room_id} role={role}")
                self._room_id = room_id
                self._role    = role
                self._call_type = call_type
            rtc_call.set_call_type(call_type)
            _ok(f"当前房间: room_id={data['room_id']} status={data['status']} "
                f"role={data['role']} caller={data['caller']} type={call_type}")

    # ── 终端命令 ──────────────────────────────────────────────────────────────

    def run_cmd_loop(self, stop_event) -> None:
        Y = "\033[1;33m"
        R = "\033[0m"
        print(f"{Y}[call] ╔══════════════════════════════════════════════════╗{R}")
        print(f"{Y}[call]   终端命令就绪：{R}")
        print(f"{Y}[call]     call                  — 列出联系人并选择拨打{R}")
        print(f"{Y}[call]     call <N>              — 拨打联系人列表中下标为 N 的人{R}")
        print(f"{Y}[call]     call <device_id>       — 按设备 ID 发起呼叫{R}")
        print(f"{Y}[call]     accept                — 接听来电{R}")
        print(f"{Y}[call]     reject [busy|decline] — 拒接来电{R}")
        print(f"{Y}[call]     hangup                — 挂断通话{R}")
        print(f"{Y}[call]     cancel                — 主叫取消{R}")
        print(f"{Y}[call]     联系人管理：ct list（列表）/ ct pending（待审批）{R}")
        print(f"{Y}[call]                 ct add|accept|reject|del <device_id>{R}")
        print(f"{Y}[call]                 ct remark <peer_id> [备注]{R}")
        print(f"{Y}[call]     room                  — 查询当前所在房间{R}")
        print(f"{Y}[call]     exit                  — 优雅退出程序{R}")
        print(f"{Y}[call] ╚══════════════════════════════════════════════════╝{R}")
        while not stop_event.is_set():
            try:
                line = input().strip()
            except EOFError:
                break
            if not line:
                continue
            parts = line.split()
            cmd = parts[0].lower()

            if cmd == "call":
                if len(parts) == 1:
                    # 无参：列联系人，提示选择
                    contacts = self.do_list_contacts()
                    if contacts:
                        try:
                            idx = int(input(f"{Y}[call] 选择下标 [0-{len(contacts)-1}]: {R}").strip())
                        except (EOFError, ValueError):
                            _warn("无效输入")
                            continue
                        if 0 <= idx < len(contacts):
                            self.do_call(contacts[idx]["device_id"])
                        else:
                            _warn(f"下标超出范围 [0-{len(contacts)-1}]")
                elif len(parts) >= 2:
                    arg = parts[1]
                    call_type = parts[2] if len(parts) >= 3 else None
                    if arg.isdigit():
                        # 数字下标
                        idx = int(arg)
                        if not self._contact_list:
                            self.do_list_contacts()
                        if 0 <= idx < len(self._contact_list):
                            self.do_call(self._contact_list[idx]["device_id"], call_type)
                        else:
                            _warn(f"下标超出范围 [0-{len(self._contact_list)-1}]，先执行 ct list 刷新列表")
                    else:
                        # device_id
                        self.do_call(arg, call_type)
            elif cmd == "accept":
                self.do_accept()
            elif cmd == "reject":
                reason = parts[1].lower() if len(parts) >= 2 else "decline"
                self.do_reject(reason)
            elif cmd == "hangup":
                self.do_hangup()
            elif cmd == "cancel":
                self.do_cancel()
            elif cmd in ("contact", "ct"):
                if len(parts) < 2:
                    _warn("用法: ct list|pending|add|accept|reject|del|remark")
                    continue
                action = parts[1].lower()
                if action in ("list", "ls"):
                    self.do_list_contacts()
                elif action == "pending":
                    self.do_list_pending_contacts()
                elif action in ("add", "accept", "reject", "del", "delete", "remark"):
                    if len(parts) < 3:
                        target_name = "peer_id" if action == "remark" else "device_id"
                        _warn(f"用法: ct {action} <{target_name}>")
                        continue
                    peer = parts[2]
                    if action == "remark":
                        self.do_remark(peer, " ".join(parts[3:]))
                    elif action == "add":
                        self.do_add_contact(peer)
                    elif action in ("accept", "reject"):
                        self.do_respond_contact(peer, action == "accept")
                    else:
                        self.do_delete_contact(peer)
                else:
                    _warn("用法: ct list|pending|add|accept|reject|del|remark")
            elif cmd == "room":
                self.do_query_room()
            elif cmd == "exit":
                _ok("正在退出…")
                if self._room_id:
                    self.do_hangup()
                stop_event.set()
                break
            else:
                _warn(f"未知命令: {line}"
                      "（可用：call / accept / reject / hangup / cancel / ct / room / exit）")
