#!/usr/bin/env python3
"""MQTT 业务消息组合路由与统一终端命令入口。"""

import os
import select
import sys
import threading

from callback_work_queue import CallbackWorkQueue
from call_type_policy import CallTypeError, resolve_call_type
from session_arbiter import IncomingDecision
from session_coordinator import SessionKind
from terminal_ui import print_box

PENDING_CALL_TTL_SEC = 45.0


class SessionMessageRouter:
    """保持各协议状态机独立，只转发其所属的 MQTT 消息。"""

    def __init__(self, arbiter, voip, call):
        self.arbiter = arbiter
        self.voip = voip
        self.call = call
        self._reject_work = CallbackWorkQueue(
            "session-reject-http",
            self._run_io,
            self._warn,
            maxsize=32,
        )
        self._refresh_work = CallbackWorkQueue(
            "session-refresh-http",
            self._run_io,
            self._warn,
            maxsize=1,
        )
        self._reject_work.start()
        self._refresh_work.start()
        self._timer_lock = threading.Lock()
        self._pending_timers = {}
        set_reject_submitter = getattr(
            self.voip, "set_reject_submitter", None)
        if callable(set_reject_submitter):
            set_reject_submitter(self._submit_voip_reject)
        set_callers_refresh_submitter = getattr(
            self.voip, "set_callers_refresh_submitter", None)
        if callable(set_callers_refresh_submitter):
            set_callers_refresh_submitter(
                lambda callback:
                    self._submit_io(
                        self._refresh_work,
                        "VoIP 授权列表刷新",
                        callback,
                    ))

    def shutdown(self) -> None:
        with self._timer_lock:
            timers = list(self._pending_timers.values())
            self._pending_timers.clear()
        for timer in timers:
            timer.cancel()
        for timer in timers:
            if timer.is_alive() and timer is not threading.current_thread():
                timer.join()
        self._refresh_work.stop()
        self._reject_work.stop()

    def wait_for_idle(self) -> None:
        """测试和有序关闭使用；MQTT 回调本身不等待 HTTP。"""
        self._reject_work.drain()
        self._refresh_work.drain()

    @staticmethod
    def _warn(message: str) -> None:
        print(f"[router] {message}", flush=True)

    @staticmethod
    def _run_io(item) -> None:
        label, callback, args = item
        try:
            callback(*args)
        except BaseException as exc:
            print(f"[router] {label}任务失败: {exc}", flush=True)

    def _submit_io(
        self,
        work: CallbackWorkQueue,
        label: str,
        callback,
        *args,
    ) -> bool:
        submitted = work.submit((label, callback, args))
        if not submitted:
            print(f"[router] {label}任务未提交：队列已满或已停止", flush=True)
        return submitted

    def _submit_device_reject(self, payload, reason: str) -> None:
        self._submit_io(
            self._reject_work,
            "设备忙线拒接",
            self.call.reject_incoming,
            dict(payload),
            reason,
        )

    def _submit_voip_reject(self, payload, reason: int) -> None:
        self._submit_io(
            self._reject_work,
            "VoIP 忙线拒接",
            self.voip.reject_incoming,
            dict(payload),
            reason,
        )

    def _arm_pending_expiry(self, kind, session_id: str, state) -> None:
        def expire():
            try:
                if state.expire_pending(session_id):
                    self.arbiter.clear_pending(kind, session_id)
            finally:
                with self._timer_lock:
                    if self._pending_timers.get(kind) is timer:
                        self._pending_timers.pop(kind, None)

        timer = threading.Timer(PENDING_CALL_TTL_SEC, expire)
        timer.daemon = True
        with self._timer_lock:
            previous = self._pending_timers.pop(kind, None)
            self._pending_timers[kind] = timer
        if previous is not None:
            previous.cancel()
        timer.start()

    def on_call_incoming(self, payload):
        room_id = payload.get("wx_room_id", "")
        if not room_id:
            self._submit_voip_reject(payload, 7)
            return
        decision = self.arbiter.admit_incoming(
            SessionKind.VOIP, room_id, PENDING_CALL_TTL_SEC)
        if decision == IncomingDecision.CURRENT:
            # 本设备外呼的回铃也经由同一个 MQTT 方法到达。
            if self.voip.is_active():
                self.voip.on_call_incoming(payload)
            else:
                self._submit_voip_reject(payload, 5)
            return
        if decision == IncomingDecision.BUSY:
            self._submit_voip_reject(payload, 5)
            return
        try:
            self.voip.on_call_incoming(payload, replace_pending=True)
        except BaseException:
            self.arbiter.clear_pending(SessionKind.VOIP, room_id)
            raise
        if (not self.voip.has_pending()
                and self.arbiter.current != SessionKind.VOIP):
            self.arbiter.clear_pending(SessionKind.VOIP, room_id)
        else:
            self._arm_pending_expiry(SessionKind.VOIP, room_id, self.voip)
    def on_callers_update(self): self.voip.on_callers_update()
    def on_call_cancel(self, payload):
        room_id = payload.get("wx_room_id", "")
        self.voip.on_call_cancel(payload)
        if not self.voip.has_pending():
            self.arbiter.clear_pending(SessionKind.VOIP, room_id)
    def on_device_call_incoming(self, payload):
        room_id = payload.get("room_id", "")
        if not room_id:
            self._submit_device_reject(payload, "invalid")
            return
        if not self.arbiter.offer_pending(
                SessionKind.CALL, room_id, PENDING_CALL_TTL_SEC):
            self._submit_device_reject(payload, "busy")
            return
        try:
            self.call.on_device_call_incoming(payload)
        except BaseException:
            self.arbiter.clear_pending(SessionKind.CALL, room_id)
            raise
        if not self.call.has_pending():
            self.arbiter.clear_pending(SessionKind.CALL, room_id)
        else:
            self._arm_pending_expiry(SessionKind.CALL, room_id, self.call)
    def on_room_cancel(self, payload):
        room_id = payload.get("room_id", "")
        self.call.on_room_cancel(payload)
        if not self.call.has_pending():
            self.arbiter.clear_pending(SessionKind.CALL, room_id)
    def on_device_call_reject(self, payload):
        room_id = payload.get("room_id", "")
        self.call.on_device_call_reject(payload)
        if not self.call.has_pending():
            self.arbiter.clear_pending(SessionKind.CALL, room_id)
    def on_device_callers_update(self): self.call.on_device_callers_update()
    def on_device_callers_update_payload(self, payload): self.call.on_device_callers_update(payload)
    def on_callee_answered(self, payload): self.call.on_callee_answered(payload)


class TerminalController:
    """唯一读取 stdin 的组件；命令执行仍委派给各业务状态机。"""

    _COMMAND_ALIASES = {
        "w": "wxcall",
        "a": "accept",
        "r": "reject",
        "h": "hangup",
        "e": "exit",
        "ct": "contact",
    }

    def __init__(self, arbiter, voip, ai, call, video_capable: bool = True):
        self.arbiter = arbiter
        self.voip = voip
        self.ai = ai
        self.call = call
        self._video_capable = bool(video_capable)
        self._pending_selection = None

    def run_cmd_loop(self, stop_event) -> None:
        self._print_help()
        while not stop_event.is_set():
            try:
                raw_line = self._readline(stop_event)
            except EOFError:
                break
            if raw_line is None:
                continue
            if raw_line == "":
                break
            line = raw_line.strip()
            if line:
                try:
                    self.execute(line, stop_event)
                except Exception as exc:
                    print(f"[terminal] 命令执行失败: {exc}", flush=True)

    @staticmethod
    def _readline(stop_event):
        """Read one command while remaining interruptible during shutdown."""
        if os.name == "nt":
            import msvcrt

            chars = []
            while not stop_event.is_set():
                if not msvcrt.kbhit():
                    stop_event.wait(0.1)
                    continue
                char = msvcrt.getwch()
                if char in ("\r", "\n"):
                    print()
                    return "".join(chars)
                if char == "\003":
                    stop_event.set()
                    return None
                if char in ("\000", "\xe0"):
                    if msvcrt.kbhit():
                        msvcrt.getwch()
                    continue
                if char == "\b":
                    if chars:
                        chars.pop()
                        print("\b \b", end="", flush=True)
                    continue
                chars.append(char)
                print(char, end="", flush=True)
            return None

        try:
            ready, _, _ = select.select([sys.stdin], [], [], 0.2)
        except (OSError, ValueError):
            return sys.stdin.readline()
        if not ready:
            return None
        return sys.stdin.readline()

    def execute(self, line: str, stop_event) -> None:
        try:
            self._execute(line, stop_event)
        except CallTypeError as exc:
            self._pending_selection = None
            print(f"[terminal] {exc}", flush=True)

    def _execute(self, line: str, stop_event) -> None:
        parts = line.split()
        command = self._COMMAND_ALIASES.get(parts[0].lower(), parts[0].lower())
        if self._pending_selection:
            if self._try_execute_pending_selection(parts):
                return
        if command == "wxcall":
            self._pending_selection = None
            self._handle_wxcall(parts)
        elif command == "aicall":
            self._pending_selection = None
            self.ai.call()
        elif command == "call":
            self._pending_selection = None
            self._handle_call(parts)
        elif command == "accept":
            self._pending_selection = None
            self._accept()
        elif command == "reject":
            self._pending_selection = None
            self._reject(parts[1] if len(parts) >= 2 else "decline")
        elif command == "cancel":
            self._pending_selection = None
            self._cancel()
        elif command == "hangup":
            self._pending_selection = None
            current = self.arbiter.current
            if current == SessionKind.VOIP:
                self.voip.hangup()
            elif current == SessionKind.AI:
                self.ai.hangup()
            elif current == SessionKind.CALL:
                self.call.do_hangup()
            elif current in (None, SessionKind.STREAM) and self.call.has_room():
                # 进程重启后，room 命令能恢复 call-server 房间，但此时协调器
                # 仍处于 STREAM。以房间状态兜底，确保服务端房间可以被挂断。
                self.call.do_hangup()
            else:
                print("[terminal] 当前没有通话", flush=True)
        elif command == "contact":
            self._pending_selection = None
            self._execute_contact_command(parts)
        elif command == "room":
            self._pending_selection = None
            self.call.do_query_room()
        elif command == "help":
            self._pending_selection = None
            self._print_help()
        elif command == "exit":
            self._pending_selection = None
            self.arbiter.shutdown()
            stop_event.set()
        else:
            self._pending_selection = None
            print("[terminal] 未知命令，输入 help 查看命令", flush=True)

    def _execute_contact_command(self, parts: list[str]) -> None:
        usage = "ct list|pending | ct add|accept|reject|del <device_id> | ct remark <peer_id> [备注]"
        if len(parts) < 2:
            print(f"[terminal] 用法: {usage}", flush=True)
            return
        action = parts[1].lower()
        if action in ("list", "ls"):
            self.call.do_list_contacts()
            return
        if action == "pending":
            self.call.do_list_pending_contacts()
            return
        if action not in ("add", "accept", "reject", "del", "delete", "remark"):
            print(f"[terminal] 用法: {usage}", flush=True)
            return
        if len(parts) < 3:
            target_name = "peer_id" if action == "remark" else "device_id"
            print(f"[terminal] 用法: contact/ct {action} <{target_name}>", flush=True)
            return
        peer_id = parts[2]
        if action == "remark":
            self.call.do_remark(peer_id, " ".join(parts[3:]))
        elif action == "add":
            self.call.do_add_contact(peer_id)
        elif action in ("accept", "reject"):
            self.call.do_respond_contact(peer_id, action == "accept")
        else:
            self.call.do_delete_contact(peer_id)

    def _dial_wxcall(self, index_text: str, call_type: str) -> None:
        callers = self.voip.list_callers()
        self.voip.do_call(callers[int(index_text)], call_type)
        self._pending_selection = None

    def _handle_wxcall(self, parts) -> None:
        call_type = self._parse_call_type(parts[2] if len(parts) >= 3 else None)
        if len(parts) == 1:
            self._show_wxcall_selection(call_type)
            return
        if len(parts) == 2 and parts[1].lower() in ("video", "audio"):
            self._show_wxcall_selection(self._parse_call_type(parts[1]))
            return
        try:
            self._dial_wxcall(parts[1], call_type)
        except (ValueError, IndexError):
            print("[terminal] 无效的 VoIP 联系人下标", flush=True)

    def _handle_call(self, parts) -> None:
        call_type = self._parse_call_type(parts[2] if len(parts) >= 3 else None)
        if len(parts) == 1:
            self._show_call_selection(call_type)
            return
        if len(parts) == 2 and parts[1].lower() in ("video", "audio"):
            self._show_call_selection(self._parse_call_type(parts[1]))
            return
        target = parts[1]
        if target.isdigit():
            if self._dial_contact_index(int(target), call_type):
                return
            print("[terminal] 无效的联系人下标，输入 call 查看列表", flush=True)
            return
        if self._dial_contact_target(target, call_type):
            return
        self.call.do_call(target, call_type)

    def _try_execute_pending_selection(self, parts) -> bool:
        if not parts[0].isdigit():
            return False
        call_type = self._pending_selection["call_type"]
        if len(parts) >= 2:
            call_type = self._parse_call_type(parts[1])
        if self._pending_selection["kind"] == "wxcall":
            try:
                self._dial_wxcall(parts[0], call_type)
                return True
            except (ValueError, IndexError):
                return False
        if self._pending_selection["kind"] == "call":
            return self._dial_contact_index(int(parts[0]), call_type)
        return False

    def _show_wxcall_selection(self, call_type: str) -> None:
        callers = self.voip.list_callers()
        self._pending_selection = {"kind": "wxcall", "call_type": call_type}
        rows = []
        for index, caller in enumerate(callers):
            remark = caller.get("remark", "")
            openid = caller.get("wx_open_id", "?")
            if rows:
                rows.append("")
            rows.extend((
                f"[{index}] remark={remark or '未命名'}",
                f"    wx_open_id={openid}",
            ))
        print_box(f"微信联系人列表（共 {len(callers)} 条）", rows)
        print(f"[terminal] 输入序号发起{'视频' if call_type == 'video' else '语音'}呼叫，或输入其他命令取消本次选择", flush=True)

    def _show_call_selection(self, call_type: str) -> None:
        contacts = self.call.do_list_contacts()
        self._pending_selection = {"kind": "call", "call_type": call_type}
        if contacts:
            print(f"[terminal] 输入序号发起{'视频' if call_type == 'video' else '语音'}呼叫，或输入其他命令取消本次选择", flush=True)

    def _dial_contact_index(self, index: int, call_type: str) -> bool:
        contacts = self.call.get_cached_contacts()
        if not contacts and hasattr(self.call, "do_list_contacts"):
            contacts = self.call.do_list_contacts()
        if index < 0 or index >= len(contacts):
            return False
        self._pending_selection = None
        return self._dial_contact(contacts[index], call_type)

    def _dial_contact_target(self, target: str, call_type: str) -> bool:
        contacts = self.call.get_cached_contacts()
        for contact in contacts:
            if target in (contact.get("device_id", ""), contact.get("wx_open_id", "")):
                self._pending_selection = None
                return self._dial_contact(contact, call_type)
        callers = self.voip.list_callers()
        for caller in callers:
            if target == caller.get("wx_open_id", ""):
                self._pending_selection = None
                self.voip.do_call(caller, call_type)
                return True
        return False

    def _dial_contact(self, contact: dict, call_type: str) -> bool:
        if contact.get("type") == "voip" or contact.get("wx_open_id"):
            target = {
                "wx_open_id": contact.get("wx_open_id", ""),
                "wx_app_id": contact.get("wx_app_id", ""),
                "wx_model_id": contact.get("wx_model_id", ""),
            }
            if not target["wx_open_id"]:
                print("[terminal] 该 VoIP 联系人缺少 wx_open_id，无法呼叫", flush=True)
                return False
            self.voip.do_call(target, call_type)
            return True
        self.call.do_call(contact.get("device_id", ""), call_type)
        return True

    def _parse_call_type(self, call_type: str | None) -> str:
        return resolve_call_type(
            call_type, self._video_capable, subject="通话")

    def _accept(self) -> None:
        if self.arbiter.has_pending(SessionKind.CALL):
            self.call.do_accept()
            return
        if self.arbiter.has_pending(SessionKind.VOIP):
            self.voip.accept()
            return
        print("[terminal] 当前没有待接听的来电", flush=True)

    def _reject(self, reason: str) -> None:
        if self.arbiter.has_pending(SessionKind.CALL):
            self.call.do_reject(reason)
            self.arbiter.clear_pending(SessionKind.CALL)
            return
        if self.arbiter.has_pending(SessionKind.VOIP):
            self.voip.reject()
            self.arbiter.clear_pending(SessionKind.VOIP)
            return
        print("[terminal] 当前没有待拒接的来电", flush=True)

    def _cancel(self) -> None:
        current = self.arbiter.current
        if current == SessionKind.CALL and self.call.is_outgoing():
            self.call.do_cancel()
            return
        if current == SessionKind.VOIP:
            self.voip.cancel()
            return
        print("[terminal] 当前没有可取消的外呼", flush=True)

    @staticmethod
    def _print_help() -> None:
        y = "\033[1;33m"
        r = "\033[0m"
        banner = "\n".join((
            f"{y}[terminal] ╔══════════════════════════════════════════════════╗{r}",
            f"{y}[terminal]   wxcall [N] [video|audio] 微信联系人呼叫       {r}",
            f"{y}[terminal]   aicall           发起 AI 对话                {r}",
            f"{y}[terminal]   call [N|device_id|openid] [video|audio]    {r}",
            f"{y}[terminal]   accept / reject [reason] / cancel / hangup{r}",
            f"{y}[terminal]   首字母缩写: w=wxcall a=accept r=reject h=hangup e=exit{r}",
            f"{y}[terminal]   联系人管理：ct list（列表）/ ct pending（待审批）{r}",
            f"{y}[terminal]               ct add|accept|reject|del <device_id>{r}",
            f"{y}[terminal]               ct remark <peer_id> [备注]          {r}",
            f"{y}[terminal]   room / help / exit                             {r}",
            f"{y}[terminal] ╚══════════════════════════════════════════════════╝{r}",
        ))
        print(banner, flush=True)
