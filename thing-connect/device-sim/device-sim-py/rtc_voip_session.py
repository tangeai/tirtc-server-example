#!/usr/bin/env python3
"""
rtc_voip_session.py — VoIP 来/去电状态机

管理三种状态：IDLE → CONNECTING → IN_CALL
实现 device_flow.connect_mqtt_blocking 期望的 handler 接口：
  on_call_incoming(payload)
  on_callers_update()
  on_call_cancel(payload)
"""

import json
import sys
import threading
import time

import requests
import rtc_voip
import http_trace
from terminal_ui import print_box

OUTGOING_CANCEL_ROOM_WAIT_SEC = 10.0
OUTGOING_RING_TIMEOUT_SEC = 30.0
ENDED_OUTGOING_CALL_TTL_SEC = 60.0


def _warn(msg):
    if rtc_voip._LOG_LEVEL <= 30:
        print(f"\033[1;33m[voip]\033[0m {msg}", flush=True)

def _ok(msg):
    if rtc_voip._LOG_LEVEL <= 20:
        print(f"\033[0;32m[voip]\033[0m {msg}", flush=True)

def _log(msg):
    if rtc_voip._LOG_LEVEL <= 10:
        print(f"\033[0;36m[voip]\033[0m {msg}", flush=True)


# 微信 iot/voip/call 后台错误码 → 中文说明
# 来源：https://developers.weixin.qq.com/miniprogram/dev/framework/device/voip-plugin/api/errCode.html
_WX_CALL_ERRCODES = {
    1:  "roomId 错误",
    2:  "设备 deviceId 错误",
    3:  "voip_id 错误",
    4:  "voipToken 错误（刷脸模式）",
    5:  "生成 voip 房间失败",
    7:  "openId 错误",
    8:  "openId 未授权（刷脸模式）",
    9:  "用户未授权该设备，请在小程序中重新授权",
    12: "小程序音视频能力审核未完成，正式版暂时无法使用",
    13: "硬件设备拨打微信时 voipToken 错误",
    14: "微信拨打硬件设备时 voipToken 错误",
    15: "账户欠费",
    17: "voipToken 对应 modelId 错误",
    19: "openId 与小程序 appId 不匹配",
    20: "openId 无效",
    22: "chargeType 参数非法",
    23: "设备 license 已过期",
    24: "设备未激活 license",
}

def _wechat_call_errmsg(raw_msg: str) -> str:
    """从服务端 msg 字符串里提取微信 errcode，附上中文说明。"""
    import re
    m = re.search(r"wechat err (\d+)", raw_msg)
    if m:
        code = int(m.group(1))
        zh = _WX_CALL_ERRCODES.get(code, "")
        hint = zh + f"（微信错误码 {code}）" if zh else f"微信错误码 {code}"
        return f"{hint} — {raw_msg}"
    return raw_msg


def _extract_wx_openid(payload: dict) -> str:
    """兼容不同服务端字段名。"""
    return (
        payload.get("wx_user_openid")
        or payload.get("wx_open_id")
        or payload.get("openid")
        or ""
    )


class VoipCallState:
    """VoIP 来/去电状态机，实现 handler 接口供 device_flow 路由消息。"""

    def __init__(self, voip_server: str, device_id: str,
                 mqtt_token: str, voip_audio: str, auth_list: list = None,
                 before_start=None, after_stop=None, before_accept=None,
                 before_continue=None, before_accept_ticket=None):
        self._voip_server  = voip_server
        self._device_id    = device_id
        self._mqtt_token   = mqtt_token
        self._voip_audio   = voip_audio
        self._auth_list    = auth_list or []
        self._callers_refreshing = False
        self._before_start = before_start or (lambda action: action())
        self._before_accept = before_accept or self._before_start
        self._before_accept_ticket = before_accept_ticket
        self._before_continue = before_continue or self._before_start
        self._after_stop   = after_stop or (lambda: None)
        self._pending_call    = {}
        self._incoming_generation = 0
        self._outgoing_call   = False
        self._outgoing_openid = ""
        self._outgoing_call_id = ""
        self._outgoing_call_type = "video"
        self._outgoing_cancel_requested = False
        self._outgoing_cancel_timer = None
        self._outgoing_ring_timer = None
        self._outgoing_generation = 0
        self._ended_outgoing_calls = {}
        self._active_room_id  = ""
        self._lock = threading.Lock()

    def _cancel_outgoing_ring_timer_locked(self) -> None:
        timer, self._outgoing_ring_timer = self._outgoing_ring_timer, None
        if timer is not None:
            timer.cancel()

    def _cancel_outgoing_cancel_timer_locked(self) -> None:
        timer, self._outgoing_cancel_timer = self._outgoing_cancel_timer, None
        if timer is not None:
            timer.cancel()

    def _prune_ended_outgoing_locked(self) -> None:
        now = time.monotonic()
        self._ended_outgoing_calls = {
            call_id: expires_at
            for call_id, expires_at in self._ended_outgoing_calls.items()
            if expires_at > now
        }

    def _remember_current_outgoing_ended_locked(self) -> None:
        if not self._outgoing_call_id:
            return
        self._prune_ended_outgoing_locked()
        self._ended_outgoing_calls[self._outgoing_call_id] = (
            time.monotonic() + ENDED_OUTGOING_CALL_TTL_SEC
        )

    def _is_ended_outgoing_locked(self, call_id: str) -> bool:
        if not call_id:
            return False
        self._prune_ended_outgoing_locked()
        return call_id in self._ended_outgoing_calls

    def _reset_outgoing_locked(self) -> None:
        self._cancel_outgoing_ring_timer_locked()
        self._cancel_outgoing_cancel_timer_locked()
        self._outgoing_generation += 1
        self._outgoing_call = False
        self._outgoing_openid = ""
        self._outgoing_call_id = ""
        self._outgoing_call_type = "video"
        self._outgoing_cancel_requested = False

    def _arm_outgoing_ring_timer_locked(self) -> None:
        self._cancel_outgoing_ring_timer_locked()
        generation = self._outgoing_generation
        call_id = self._outgoing_call_id

        def _expire():
            with self._lock:
                if not self._outgoing_call or self._outgoing_generation != generation:
                    return
                if call_id and self._outgoing_call_id != call_id:
                    return
                self._remember_current_outgoing_ended_locked()
                self._reset_outgoing_locked()
            _warn(f"等待对方接听超时（{OUTGOING_RING_TIMEOUT_SEC:.0f}s），已清理 VoIP 外呼状态")
            self._after_stop()

        timer = threading.Timer(OUTGOING_RING_TIMEOUT_SEC, _expire)
        timer.daemon = True
        self._outgoing_ring_timer = timer
        timer.start()

    def _arm_outgoing_cancel_timer_locked(self) -> None:
        self._cancel_outgoing_ring_timer_locked()
        self._cancel_outgoing_cancel_timer_locked()
        generation = self._outgoing_generation
        call_id = self._outgoing_call_id

        def _expire():
            with self._lock:
                if (
                    not self._outgoing_call
                    or not self._outgoing_cancel_requested
                    or self._outgoing_generation != generation
                ):
                    return
                if call_id and self._outgoing_call_id != call_id:
                    return
                self._remember_current_outgoing_ended_locked()
                self._reset_outgoing_locked()
            _warn(f"等待房间通知超时（{OUTGOING_CANCEL_ROOM_WAIT_SEC:.0f}s），取消 VoIP 外呼状态即可")
            self._after_stop()

        timer = threading.Timer(OUTGOING_CANCEL_ROOM_WAIT_SEC, _expire)
        timer.daemon = True
        self._outgoing_cancel_timer = timer
        timer.start()

    def _incoming_remark(self, payload: dict, wx_openid: str) -> str:
        remark = (
            payload.get("wx_user_remark")
            or payload.get("remark")
            or payload.get("wx_user_nickname")
            or ""
        )
        if remark:
            return str(remark)
        with self._lock:
            for contact in self._auth_list:
                if wx_openid and contact.get("wx_open_id") == wx_openid:
                    return str(contact.get("remark") or "")
        return ""

    def _begin_session(self, room_id: str, action,
                       consume_pending: bool = False) -> bool:
        try:
            if consume_pending and self._before_accept_ticket:
                self._before_accept_ticket(action, room_id)
            else:
                callback = (
                    self._before_accept if consume_pending
                    else self._before_continue
                )
                callback(action)
            return True
        except Exception as exc:
            with self._lock:
                if self._active_room_id == room_id:
                    self._active_room_id = ""
            _warn(f"VoIP 会话启动失败 room={room_id}: {exc}")
            return False

    def on_call_incoming(self, p: dict, replace_pending: bool = False) -> None:
        peer_id         = p.get("peer_id", "")
        token           = p.get("token", "")
        wx_room_id      = p.get("wx_room_id", "")
        wx_server_token = p.get("wx_server_token", "")
        wx_payload_str  = p.get("wx_payload", "")
        wx_user_openid  = _extract_wx_openid(p)
        wx_user_remark  = self._incoming_remark(p, wx_user_openid)
        wx_app_id       = p.get("wx_app_id", "")
        wx_model_id     = p.get("wx_model_id", "")
        wx_call_id      = p.get("wx_call_id", "")
        wx_from         = p.get("wx_from", "")
        wx_room_type    = p.get("wx_room_type", "")
        if not wx_room_id:
            _warn("忽略缺少 wx_room_id 的 VoIP 来电")
            return

        with self._lock:
            if replace_pending and self._pending_call:
                self._pending_call.clear()
                self._incoming_generation += 1
            has_pending     = bool(self._pending_call)
            is_outgoing     = self._outgoing_call
            outgoing_openid = self._outgoing_openid
            outgoing_call_id = self._outgoing_call_id
            cancel_requested = self._outgoing_cancel_requested
            ended_outgoing = self._is_ended_outgoing_locked(wx_call_id)

        if ended_outgoing:
            _warn(f"忽略已经取消或超时的外呼回铃 call_id={wx_call_id} room={wx_room_id}")
            if wx_app_id and wx_model_id:
                rtc_voip.reject_session(wx_app_id, wx_model_id,
                                        wx_server_token, wx_room_id, wx_payload_str, 7)
            return

        matches_outgoing = False
        if is_outgoing:
            openid_matches = not wx_user_openid or wx_user_openid == outgoing_openid
            if outgoing_call_id:
                matches_outgoing = bool(
                    wx_call_id
                    and wx_call_id == outgoing_call_id
                    and openid_matches
                )
            elif wx_call_id:
                # 请求响应可能晚于 MQTT 房间通知。此时本地还没有 call_id，
                # 但新版服务端的 from 可以证明它属于本设备刚发起的外呼。
                matches_outgoing = wx_from == self._device_id and openid_matches
            else:
                # 兼容未返回 call_id 的旧服务端，只能退回到 OpenID 判断。
                matches_outgoing = openid_matches

        if is_outgoing and not matches_outgoing:
            _warn(
                f"外呼等待中，拒接不匹配的来电 room_id={wx_room_id} "
                f"call_id={wx_call_id or '-'}"
            )
            if wx_app_id and wx_model_id:
                rtc_voip.reject_session(wx_app_id, wx_model_id,
                                        wx_server_token, wx_room_id, wx_payload_str, 5)
            return

        if rtc_voip.is_active() or has_pending:
            _warn(f"{'已在对讲中' if rtc_voip.is_active() else '已有待确认来电'}，自动拒接")
            if wx_app_id and wx_model_id:
                rtc_voip.reject_session(wx_app_id, wx_model_id,
                                        wx_server_token, wx_room_id, wx_payload_str, 7)
            return

        own_outgoing_recovery = bool(
            not is_outgoing
            and wx_call_id
            and wx_from == self._device_id
        )

        if is_outgoing or own_outgoing_recovery:
            if not peer_id or not token:
                _warn(f"外呼回铃缺少 peer_id/token，无法建立连接 room={wx_room_id}")
                release_owner = False
                with self._lock:
                    if is_outgoing:
                        if cancel_requested:
                            self._remember_current_outgoing_ended_locked()
                        self._reset_outgoing_locked()
                        release_owner = True
                if release_owner:
                    self._after_stop()
                return
            if is_outgoing and not wx_user_openid:
                _log(f"外呼回铃未携带 openid，按当前外呼目标继续建连 room={wx_room_id}")
            with self._lock:
                call_type = (
                    self._outgoing_call_type
                    if is_outgoing
                    else ("audio" if wx_room_type == "voice" else "video")
                )
                self._active_room_id  = wx_room_id
                was_cancel_requested = self._outgoing_cancel_requested
                if is_outgoing:
                    self._reset_outgoing_locked()
            if was_cancel_requested:
                _ok(f"外呼取消：房间已到达 room={wx_room_id}，自动进房间后发送 0x2001 挂断")
                self._begin_session(
                    wx_room_id,
                    lambda: rtc_voip.start_session(
                        peer_id, token, self._voip_audio,
                        with_video=(call_type != "audio"),
                        session_role="device_caller_cancel",
                        cancel_on_connect=True),
                    consume_pending=own_outgoing_recovery)
            else:
                _ok(
                    f"{'外呼回铃恢复' if own_outgoing_recovery else '外呼回铃'} "
                    f"联系人={wx_user_remark or '未命名'} "
                    f"openid={wx_user_openid or '-'} room={wx_room_id}，自动接听"
                )
                self._begin_session(
                    wx_room_id,
                    lambda: rtc_voip.start_session(
                        peer_id, token, self._voip_audio,
                        with_video=(call_type != "audio"),
                        session_role="device_caller"),
                    consume_pending=own_outgoing_recovery)
            return

        if not peer_id or not token:
            _warn(f"来电缺少 peer_id/token，无法接听 room={wx_room_id}")
            if wx_app_id and wx_model_id:
                rtc_voip.reject_session(wx_app_id, wx_model_id,
                                        wx_server_token, wx_room_id, wx_payload_str, 7)
            return

        with self._lock:
            self._pending_call.clear()
            self._incoming_generation += 1
            self._pending_call.update({
                "peer_id": peer_id, "token": token,
                "wx_room_id": wx_room_id, "wx_server_token": wx_server_token,
                "wx_payload": wx_payload_str, "wx_user_openid": wx_user_openid,
                "wx_user_remark": wx_user_remark,
                "wx_app_id": wx_app_id, "wx_model_id": wx_model_id,
                "generation": self._incoming_generation,
            })
        print_box(
            "微信来电",
            (
                f"联系人={wx_user_remark or '未命名'}",
                f"wx_open_id={wx_user_openid or '-'}",
                f"room_id={wx_room_id or '-'}",
                "输入 accept(a) 接听，reject(r) 拒接",
            ),
            prefix="[voip]",
        )

    def reject_incoming(self, p: dict, reason: int = 5) -> None:
        """设备正忙时直接拒绝尚未写入本地状态的微信来电。"""
        wx_app_id = p.get("wx_app_id", "")
        wx_model_id = p.get("wx_model_id", "")
        if wx_app_id and wx_model_id:
            rtc_voip.reject_session(
                wx_app_id,
                wx_model_id,
                p.get("wx_server_token", ""),
                p.get("wx_room_id", ""),
                p.get("wx_payload", ""),
                reason,
            )

    def on_callers_update(self) -> None:
        with self._lock:
            if self._callers_refreshing:
                _log("授权列表正在刷新，合并本次 callers_update")
                return
            self._callers_refreshing = True

        def refresh():
            try:
                _log("收到授权更新通知，后台重新拉取授权列表…")
                new_list = rtc_voip.report_profile(
                    self._voip_server,
                    self._mqtt_token,
                    contacts_error_none=True,
                )
                if new_list is None:
                    _warn("授权列表刷新失败，保留上一次联系人列表")
                else:
                    with self._lock:
                        self._auth_list[:] = new_list
            finally:
                with self._lock:
                    self._callers_refreshing = False

        thread = threading.Thread(
            target=refresh,
            daemon=True,
            name="voip-callers-refresh",
        )
        try:
            thread.start()
        except RuntimeError as exc:
            with self._lock:
                self._callers_refreshing = False
            _warn(f"无法启动授权列表刷新线程: {exc}")

    def list_callers(self) -> list:
        """返回授权呼叫对象；列表为空时通过 HTTP 刷新。"""
        with self._lock:
            has_callers = bool(self._auth_list)
        if not has_callers:
            headers = {"Authorization": f"Bearer {self._mqtt_token}"}
            try:
                response = http_trace.request(
                    "GET", f"{self._voip_server}/v1/voip/device/contacts",
                    headers=headers, timeout=10)
                body = response.json()
                if response.status_code == 200 and body.get("code") == 0:
                    with self._lock:
                        self._auth_list[:] = body.get("data", {}).get("contacts", [])
            except (requests.RequestException, ValueError) as exc:
                _warn(f"拉取授权列表异常: {exc}")
        with self._lock:
            return list(self._auth_list)

    def replace_callers(self, callers: list) -> None:
        with self._lock:
            self._auth_list[:] = callers

    def has_pending(self) -> bool:
        with self._lock:
            return bool(self._pending_call)

    def expire_pending(self, room_id: str) -> bool:
        """仅清理仍匹配该房间的超时来电。"""
        with self._lock:
            if self._pending_call.get("wx_room_id") != room_id:
                return False
            self._pending_call.clear()
            self._incoming_generation += 1
        _warn(f"VoIP 来电等待接听超时，已清理 room_id={room_id}")
        return True

    def is_outgoing(self) -> bool:
        with self._lock:
            return self._outgoing_call

    def is_active(self) -> bool:
        with self._lock:
            active = bool(self._outgoing_call or self._active_room_id)
        return active or rtc_voip.is_active()

    def accept(self) -> None:
        with self._lock:
            pending = dict(self._pending_call)
            if pending:
                self._pending_call.clear()
                self._active_room_id = pending["wx_room_id"]
        if not pending:
            _warn("当前没有待确认的来电")
            return
        started = self._begin_session(
            pending["wx_room_id"],
            lambda: rtc_voip.start_session(
                pending["peer_id"], pending["token"], self._voip_audio,
                session_role="wx_caller"),
            consume_pending=True,
        )
        if not started:
            with self._lock:
                if (self._incoming_generation == pending.get("generation")
                        and not self._pending_call
                        and not self._active_room_id):
                    self._pending_call.update(pending)

    def reject(self) -> None:
        with self._lock:
            pending = dict(self._pending_call)
            self._pending_call.clear()
            if pending:
                self._incoming_generation += 1
        if not pending:
            _warn("当前没有待确认的来电")
            return
        app_id, model_id = pending.get("wx_app_id", ""), pending.get("wx_model_id", "")
        if app_id and model_id:
            rtc_voip.reject_session(app_id, model_id, pending["wx_server_token"],
                                    pending["wx_room_id"], pending["wx_payload"], 7)

    def cancel(self) -> None:
        state = rtc_voip.get_state()
        if state in ("CONNECTING", "IN_CALL"):
            rtc_voip.stop_session()
            self._after_stop()
            _ok("已发送挂断信令")
            return
        with self._lock:
            if not self._outgoing_call:
                _warn("当前没有可取消的 VoIP 外呼")
                return
            if self._outgoing_cancel_requested:
                _warn("已请求取消外呼，正在等待房间通知")
                return
            self._outgoing_cancel_requested = True
            self._arm_outgoing_cancel_timer_locked()
        _ok("已请求取消 VoIP 外呼；若房间通知到达，将进房间后发送 0x2001 挂断")

    def hangup(self) -> None:
        if rtc_voip.is_active():
            rtc_voip.stop_session()
        with self._lock:
            if self._outgoing_call:
                self._remember_current_outgoing_ended_locked()
            self._active_room_id = ""
            self._reset_outgoing_locked()
        self._after_stop()

    def on_session_end(self) -> None:
        """低层 SDK 异步断开时清理房间关联并恢复默认会话。"""
        with self._lock:
            self._active_room_id = ""
        self._after_stop()

    def on_call_cancel(self, p: dict) -> None:
        wx_room_id = p.get("wx_room_id", "")
        wx_user_openid = _extract_wx_openid(p)
        wx_call_id = p.get("wx_call_id", "")
        _warn(f"对方已取消/拒接呼叫 room_id={wx_room_id}")
        cancelled_pending = False
        cancelled_active = False
        with self._lock:
            if self._pending_call.get("wx_room_id") == wx_room_id:
                self._pending_call.clear()
                self._incoming_generation += 1
                cancelled_pending = True
            if self._active_room_id and self._active_room_id == wx_room_id:
                self._active_room_id = ""
                self._incoming_generation += 1
                cancelled_active = True
        state = rtc_voip.get_state()
        if state in ("CONNECTING", "IN_CALL"):
            if not cancelled_active:
                with self._lock:
                    active = self._active_room_id
                _warn(f"room_id 不匹配（active={active}），忽略取消")
                return
            _warn(f"强制挂断（state={state}）")
            rtc_voip.stop_session()
            self._after_stop()
            return
        if cancelled_active:
            # 接听可能已经进入仲裁 STARTING，但 SDK 尚未切到 CONNECTING。
            self._after_stop()
            return
        if cancelled_pending:
            return

        cleared_outgoing = False
        with self._lock:
            outgoing_openid = self._outgoing_openid
            outgoing_call_id = self._outgoing_call_id
            call_matches = bool(
                wx_call_id
                and outgoing_call_id
                and wx_call_id == outgoing_call_id
            )
            openid_matches = bool(
                wx_user_openid
                and outgoing_openid
                and wx_user_openid == outgoing_openid
            )
            if self._outgoing_call and (call_matches or openid_matches):
                self._remember_current_outgoing_ended_locked()
                self._reset_outgoing_locked()
                cleared_outgoing = True
        if cleared_outgoing:
            _ok("外呼已结束，清理本地等待状态")
            self._after_stop()

    def do_call(self, target: dict, call_type: str = "video") -> None:
        call_type = (call_type or "video").lower()
        if call_type not in ("video", "audio"):
            _warn("VoIP 呼叫类型仅支持 video 或 audio")
            return
        with self._lock:
            if self._outgoing_call:
                _warn("已有外呼进行中，请等待对方接听或输入 cancel 取消")
                return
        if rtc_voip.is_active():
            _warn(f"当前通话中（state={rtc_voip.get_state()}），不能发起新呼叫")
            return
        headers = {"Authorization": f"Bearer {self._mqtt_token}",
                   "Content-Type": "application/json"}
        outgoing_generation = None
        try:
            updated_callers = rtc_voip.report_profile(
                self._voip_server, self._mqtt_token, with_video=(call_type == "video"))
            with self._lock:
                self._auth_list[:] = updated_callers
            target_openid = target.get("wx_open_id", "")
            refreshed_target = next(
                (item for item in updated_callers
                 if item.get("wx_open_id", "") == target_openid),
                None,
            )
            if refreshed_target is None:
                _warn("联系人授权已失效或联系人列表刷新失败，请让用户在小程序重新授权")
                return
            target = refreshed_target
            wx_app_id = target.get("wx_app_id", "")
            wx_model_id = target.get("wx_model_id", "")
            if not wx_app_id or not wx_model_id:
                _warn("授权记录缺少 wx_app_id / wx_model_id，无法发起呼叫")
                return
            body = {
                "device_id":       self._device_id,
                "wx_app_id":       wx_app_id,
                "wx_user_openid":  target_openid,
                "wx_model_id":     wx_model_id,
                "wx_room_type":    "video" if call_type == "video" else "voice",
                "wx_version_type": 2,
            }
            # 先设置带代次的 provisional 外呼状态，再申请 RTC。这样 MQTT
            # 在所有权切换边界到达时，也不会把反向来电误认成本次回铃。
            with self._lock:
                if self._outgoing_call:
                    _warn("已有外呼进行中，请等待对方接听或输入 cancel 取消")
                    return
                self._reset_outgoing_locked()
                self._outgoing_call = True
                self._outgoing_openid = target_openid
                self._outgoing_call_type = call_type
                outgoing_generation = self._outgoing_generation
            # 外呼请求发出前先取得唯一 RTC 所有权；后续来电只能收到 busy。
            try:
                self._before_start(lambda: None)
            except BaseException:
                with self._lock:
                    if (self._outgoing_call
                            and self._outgoing_generation == outgoing_generation):
                        self._reset_outgoing_locked()
                raise
            # 先登记外呼，再发 HTTP。微信回调可能先于 HTTP 响应经 MQTT
            # 到达；若等响应后才置位，会把自己的回铃误当成新来电。
            r = http_trace.request("POST", f"{self._voip_server}/v1/voip/device/call",
                                   json=body, headers=headers, timeout=10)
            resp = r.json() if r.headers.get("Content-Type", "").startswith("application/json") else {}
            code = resp.get("code", -1)
            msg  = resp.get("msg", r.text)
            if r.status_code == 200 and code == 0:
                _ok(
                    f"已发起{'视频' if call_type == 'video' else '语音'}呼叫 → "
                    f"联系人={target.get('remark') or '未命名'} "
                    f"openid={target.get('wx_open_id', '?')}"
                )
                with self._lock:
                    data = resp.get("data")
                    call_id = str(
                        data.get("call_id") or ""
                        if isinstance(data, dict)
                        else ""
                    )
                    still_waiting = (
                        self._outgoing_call
                        and self._outgoing_generation == outgoing_generation
                    )
                    if still_waiting:
                        self._outgoing_call_id = call_id
                        if not self._outgoing_cancel_requested:
                            self._arm_outgoing_ring_timer_locked()
                    elif call_id and not self._active_room_id:
                        # 本地已取消/超时，HTTP 响应才回来；记住 call_id，
                        # 防止对应的迟到回铃被“HTTP 丢响应恢复”逻辑重新接起。
                        self._ended_outgoing_calls[call_id] = (
                            time.monotonic() + ENDED_OUTGOING_CALL_TTL_SEC
                        )
            else:
                _warn(f"发起呼叫失败（code={code}）: {_wechat_call_errmsg(msg)}")
                release_owner = True
                with self._lock:
                    if (
                        self._outgoing_call
                        and self._outgoing_generation == outgoing_generation
                    ):
                        self._reset_outgoing_locked()
                    release_owner = not self._active_room_id
                if r.status_code == 401:
                    _warn("设备登录凭证无效或已过期，请重新获取 mqtt_token")
                elif code == 40205:
                    with self._lock:
                        self._auth_list[:] = [
                            item for item in self._auth_list
                            if item.get("wx_open_id", "") != target_openid
                        ]
                elif code == 6006:
                    _warn("设备已解绑，请重新完成设备绑定")
                if release_owner:
                    self._after_stop()
        except (requests.RequestException, ValueError) as e:
            release_owner = outgoing_generation is not None
            with self._lock:
                if (
                    outgoing_generation is not None
                    and self._outgoing_call
                    and self._outgoing_generation == outgoing_generation
                ):
                    self._reset_outgoing_locked()
                release_owner = release_owner and not self._active_room_id
            _warn(f"发起呼叫异常: {e}")
            if release_owner:
                self._after_stop()

    def run_cmd_loop(self, stop_event) -> None:
        """终端命令输入线程：wxcall / accept / reject / cancel / hangup"""
        Y = "\033[1;33m"
        R = "\033[0m"
        print(f"{Y}[voip] ╔══════════════════════════════════════════════════╗{R}")
        print(f"{Y}[voip]   终端命令就绪：{R}")
        print(f"{Y}[voip]     wxcall  — 从授权列表选用户发起呼叫{R}")
        print(f"{Y}[voip]     accept  — 接听来电（a 也可）{R}")
        print(f"{Y}[voip]     reject  — 拒接来电（r 也可）{R}")
        print(f"{Y}[voip]     cancel  — 取消主叫{R}")
        print(f"{Y}[voip]     hangup  — 挂断通话（h 也可）{R}")
        print(f"{Y}[voip]     exit    — 退出程序（e 也可）{R}")
        print(f"{Y}[voip] ╚══════════════════════════════════════════════════╝{R}")
        while not stop_event.is_set():
            try:
                line = input().strip().lower()
            except EOFError:
                break
            if not line:
                continue

            if line in ("accept", "a"):
                with self._lock:
                    pc = dict(self._pending_call)
                if not pc:
                    _warn("当前没有待确认的来电")
                    continue
                with self._lock:
                    self._pending_call.clear()
                    self._active_room_id = pc["wx_room_id"]
                started = self._begin_session(
                    pc["wx_room_id"],
                    lambda: rtc_voip.start_session(
                        pc["peer_id"], pc["token"], self._voip_audio,
                        session_role="wx_caller"),
                    consume_pending=True,
                )
                if not started:
                    with self._lock:
                        if not self._pending_call:
                            self._pending_call.update(pc)

            elif line in ("reject", "r"):
                with self._lock:
                    pc = dict(self._pending_call)
                if not pc:
                    _warn("当前没有待确认的来电")
                    continue
                with self._lock:
                    self._pending_call.clear()
                wx_app_id   = pc.get("wx_app_id", "")
                wx_model_id = pc.get("wx_model_id", "")
                if wx_app_id and wx_model_id:
                    rtc_voip.reject_session(wx_app_id, wx_model_id,
                                            pc["wx_server_token"], pc["wx_room_id"],
                                            pc["wx_payload"], 7)

            elif line in ("wxcall", "w"):
                callers = self.list_callers()
                if not callers:
                    _warn("授权用户列表为空")
                    continue
                rows = []
                for i, item in enumerate(callers):
                    remark = item.get("remark", "")
                    if rows:
                        rows.append("")
                    rows.extend((
                        f"[{i}] remark={remark or '未命名'}",
                        f"    wx_open_id={item.get('wx_open_id', '?')}",
                    ))
                print_box(
                    f"微信联系人列表（共 {len(callers)} 条）",
                    rows,
                )
                try:
                    idx_s = input("请输入索引（回车取消）: ").strip()
                except EOFError:
                    continue
                if not idx_s:
                    continue
                try:
                    self.do_call(callers[int(idx_s)])
                except (ValueError, IndexError):
                    _warn(f"无效索引: {idx_s}")

            elif line == "cancel":
                self.cancel()

            elif line in ("hangup", "h"):
                if not rtc_voip.is_active():
                    _warn(f"当前未在对讲中（state={rtc_voip.get_state()}）")
                    continue
                with self._lock:
                    self._active_room_id = ""
                rtc_voip.stop_session()
                self._after_stop()
                _ok("挂断完成")

            elif line in ("exit", "e"):
                _ok("正在退出…")
                if rtc_voip.is_active():
                    rtc_voip.stop_session()
                stop_event.set()
                break

            else:
                _warn(f"未知命令: {line}（可用：wxcall(w) / accept(a) / reject(r) / cancel / hangup(h) / exit(e)）")
