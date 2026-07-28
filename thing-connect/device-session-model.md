# 设备统一状态机与业务抢占规则

同一台设备同时接入 H5 实时、微信 VoIP、AI 对讲、设备呼设备时，建议用一套统一状态机管理 TiRTC 生命周期、MQTT 消息路由和业务抢占规则。

> 本文描述多业务共存时的设备端主干设计。设备上线与 MQTT 规范见 [device-integration.md](device-integration.md)；各业务接入细节见 [device-h5-live.md](device-h5-live.md)、[device-voip.md](device-voip.md)、[device-ai.md](device-ai.md)、[device-call.md](device-call.md)。
>
> generation lease、pending ticket、deadline、迟到回调、锁顺序和 RTOS
> 移植细节见 [多业务会话竞态仲裁与嵌入式实现参考](device-session-arbiter.md)。

**文档导航：** [返回总览](README.md) | [返回设备入口](device-integration.md) | [H5 实时](device-h5-live.md) | [微信 VoIP](device-voip.md) | [AI 对讲](device-ai.md) | [设备呼设备](device-call.md)

## 目录

- [先看结论](#先看结论)
- [统一状态机](#统一状态机)
- [业务抢占规则](#业务抢占规则)
- [MQTT 消息路由](#mqtt-消息路由)
- [H5 实时与通话并存策略](#h5-实时与通话并存策略)
- [推荐实现方式](#推荐实现方式)

---

## 先看结论

如果设备同时做四类业务，最稳妥的工程策略是：

1. **同一时刻只允许一个 TiRTC 业务会话占用主媒体资源**
2. H5 实时流视为“后台常驻流”，VoIP / AI / 设备呼设备视为“前台抢占会话”
3. 前台会话开始前暂停实时流，结束后恢复实时流
4. VoIP 与设备呼设备的来电都通过 MQTT 到达，但必须交给各自独立状态机处理
5. 冲突来电不要“排队自动接听”，要么自动拒接，要么明确进入 pending 态等待用户/业务层处理

这也是当前 「C 参考实现」的实现方式：`SessionArbiter` 维护唯一前台 owner、pending 与 generation，`SessionCoordinator` 只执行 `STREAM / VOIP / AI / CALL` 的停止、启动和恢复。

---

## 统一状态机

推荐把设备整体状态分成两层。

### 1. 顶层资源拥有者

这一层只回答一个问题：**当前谁拥有 TiRTC 主媒体资源。**

| 状态 | 说明 |
|------|------|
| `STREAM` | H5 实时预览常驻推流 |
| `VOIP` | 微信 VoIP 会话 |
| `AI` | AI 对讲会话 |
| `CALL` | 设备呼设备会话 |
| `NONE` | 启动中、切换中或停止状态 |

推荐规则：

- 启动完成后默认进入 `STREAM`
- 进入 `VOIP / AI / CALL` 前，先停止 `STREAM`
- `VOIP / AI / CALL` 结束后，自动恢复 `STREAM`

### 2. 各业务自己的子状态机

每条业务链路再维护自己的局部状态，不要混在总状态机里。

例如：

- VoIP：`IDLE -> CONNECTING -> IN_CALL`
- AI：`IDLE -> CONNECTING -> IN_CALL`
- 设备呼设备：`IDLE -> OUTGOING / PENDING / IN_CALL`
- H5 实时：`IDLE -> LISTENING -> STREAMING`

这样做的好处是：

- MQTT 路由清晰
- 业务 HTTP 调用和媒体会话不会耦死
- 后面要新增“录音”“报警”“云存储”等能力时，不需要重写整套状态机

---

## 业务抢占规则

下面这套规则和当前模拟器实现一致，适合做默认策略。

### 1. H5 实时是后台态

- 默认启动实时流
- 当 `VOIP / AI / CALL` 任一会话开始时，暂停实时流
- 当前台会话结束时，再恢复实时流

### 2. AI 与其它通话互斥

- AI 会话进行中收到 VoIP 来电：自动拒接 VoIP
- AI 会话进行中收到设备呼设备来电：自动 `busy` 拒接
- AI 会话进行中，不允许再发起 VoIP 或设备呼设备

原因很简单：AI 一般独占麦克风、扬声器和主会话上下文，和人工通话混跑通常没有收益。

### 3. VoIP 与设备呼设备互斥

- VoIP 进行中收到设备呼设备来电：`busy` 拒接
- 设备呼设备进行中收到 VoIP 来电：VoIP 直接拒接

当前 「C 参考实现」在 [main.c](device-sim/device-sim-c/src/main.c) 注册 MQTT 回调，所有来电、终端动作和 SDK 异步结束事件先进入 `SessionArbiter`。Arbiter 原子判断 pending、owner、room_id 与 generation；准入成功后才由 `SessionCoordinator` 串行停止 STREAM 并启动目标业务。各业务模块不再互相读取状态拼接冲突判断。

### 4. 全局只保留一个 pending 来电

当前模拟器使用一个带 `room_id`、代次和 45 秒 TTL 的全局 pending
ticket。VoIP/设备来电 first-wins，后来的来电立即 busy 拒绝，不能覆盖
第一通来电。这样 `accept` 永远只对应一个确定房间。

如果产品需要 VoIP 优先等抢占策略，应按
[显式抢占协议](device-session-arbiter.md#4-默认冲突策略)实现，不能在 SDK
回调里直接覆盖当前状态。

---

## MQTT 消息路由

统一状态机不等于把所有 MQTT 处理写在一个大函数里。推荐做法是：

- 总路由器只按 `type + channel` 分发
- VoIP 状态机只处理自己的消息
- 设备呼设备状态机只处理自己的消息
- AI 不消费 MQTT 呼叫消息

推荐分工如下：

| 消息 | 去向 | 说明 |
|------|------|------|
| `call_incoming` + `channel=wx` | VoIP 状态机 | 微信来电 |
| `call_cancel` | VoIP 状态机 | 微信侧取消/挂断 |
| `callers_update` + VoIP 来源 | VoIP 状态机 | 微信授权列表变化 |
| `call_incoming` + `channel=device` | 设备呼设备状态机 | 设备来电 |
| `room_cancel` | 设备呼设备状态机 | 房间结束 |
| `call_reject` | 设备呼设备状态机 | 某个被叫拒接 |
| `callers_update` + call 来源 | 设备呼设备状态机 | 联系人变化 |

这也是当前 「C 参考实现」的代码组织方式，参考：

- MQTT 回调注册与总入口：[device-sim/device-sim-c/src/main.c](device-sim/device-sim-c/src/main.c)
- 会话准入与竞态仲裁：[device-sim/device-sim-c/src/session_arbiter.c](device-sim/device-sim-c/src/session_arbiter.c)
- 会话资源协调器：[device-sim/device-sim-c/src/session_coordinator.c](device-sim/device-sim-c/src/session_coordinator.c)
- MQTT 连接与按 `type + channel` 分发：[device-sim/device-sim-c/src/device_flow.c](device-sim/device-sim-c/src/device_flow.c)

---

## H5 实时与通话并存策略

服务端当前不会阻止 H5 在设备通话中继续申请实时 token：

- [`GET /v1/user/device/rtc-token`](api-reference.md#get-v1userdevicertc-token) 会返回 `in_call`
- 即使 `in_call=true`，也仍会签发 token

所以“H5 实时是否与通话并存”主要由设备端自己决定。

推荐有两种策略：

### 策略 A：严格互斥

- 通话中暂停实时流
- H5 即使连上，也看不到实时音视频或只能看到断流

优点：

- 设备实现简单
- 摄像头 / 麦克风 / 编码器资源压力小

缺点：

- H5 预览体验会中断

### 策略 B：预览继续，业务独占上行控制

- 通话中仍允许 H5 看实时视频
- 但禁止 H5 talkback，或禁止 H5 抢占主音频链路

优点：

- 监控类场景体验更连续

缺点：

- 设备端要解决多路音视频复用
- 更容易出现“谁在占麦克风 / 扬声器”的资源冲突

如果没有明确产品需求，建议先做 **策略 A**。

---

## 推荐实现方式

设备端代码建议拆成 5 层：

1. `device_flow`
   - 负责设备上线、MQTT 长连接、消息收发
2. `session_router`
   - 只负责按消息类型把 MQTT 分发到各业务状态机
3. `session_arbiter`
   - 负责来电准入、唯一 owner、pending ticket、deadline 与 generation 隔离
4. `session_coordinator`
   - 不判断业务优先级，只串行停止当前适配器、启动目标适配器和恢复 STREAM
5. `voip / ai / call / stream`
   - 各自负责本业务 HTTP、媒体、命令和局部状态机

这样比“一个大类里写完所有逻辑”更稳，原因是：

- 不会把 MQTT 协议和 TiRTC 生命周期绑死
- 不会把业务规则和媒体线程绑死
- 以后新增业务时，可先接入仲裁器和生命周期适配器，不必重写旧协议

device-sim-c 是当前仓库的 「C 参考实现」，代码组织如下：

- 组合入口：[device-sim/device-sim-c/src/main.c](device-sim/device-sim-c/src/main.c)
- 会话仲裁器：[device-sim/device-sim-c/src/session_arbiter.c](device-sim/device-sim-c/src/session_arbiter.c)
- 会话协调器：[device-sim/device-sim-c/src/session_coordinator.c](device-sim/device-sim-c/src/session_coordinator.c)
- MQTT 路由：[device-sim/device-sim-c/src/device_flow.c](device-sim/device-sim-c/src/device_flow.c)
- 完整竞态与嵌入式参考：[device-session-arbiter.md](device-session-arbiter.md)

如果你在做嵌入式移植，建议优先保留 Router → Arbiter → Coordinator/业务适配器的边界，即使底层语言换成 C / C++ / RTOS 任务模型也一样成立。
