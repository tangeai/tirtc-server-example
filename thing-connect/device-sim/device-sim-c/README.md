# device-sim-c — Linux C 设备端参考实现

`device-sim-c` 是基于 TiRTC C SDK 的 **Linux 用户态设备端参考实现**。它提供可编译运行的文件媒体示例、版本化的 `DeviceAdapterV1`，以及不含 `main` 的静态库，供真实 Linux 设备按稳定边界进行二次开发。

## 定位与边界

这个目录只面向 Linux C，不包含真实硬件驱动，也不是适用于所有芯片和操作系统的通用固件。其他芯片或操作系统目录中的代码属于独立移植工程，不是本 Linux C 参考实现的一部分；ESP32-S3 是对该参考架构的独立移植，不能与这里的 Linux 实现混编或混写文档。

该实现包含以下能力：

- 通过 HTTP 完成服务发现、Report、Token 和业务凭证请求。
- 通过临时和正式 MQTT 连接完成绑定、ACK、心跳和业务消息分发。
- 在进程生命周期内只启停一次 TiRTC SDK，由统一回调表分发四类业务。
- 运行 H5 实时流、微信 VoIP、AI 对话和设备间 P2P 通话。
- 用已编码文件模拟上行音视频，不进行采集、编码或转码。
- AI 下行音频可异步写入文件；其他下行媒体只做限频日志，不解码、播放或显示。

四类业务共用一个 MQTT 长连接和一个 TiRTC SDK 实例。H5 实时流是空闲基线；VoIP、AI 或设备互呼开始时暂停实时流，结束后再恢复。

## 参考价值与适用结论

### 值得参考的部分

- 设备上线的 HTTP/MQTT 顺序、HMAC 签名串、Topic 和 ACK 规则。
- TiRTC 的 `Init → SetOption → Start → 业务连接 → Stop → Uninit` 生命周期。
- SDK 回调长期有效、回调不阻塞、延后执行断开和业务结束的线程边界。
- 用待接记录、会话所有者、代次号和超时来处理并发、取消及迟到回调。
- 媒体格式、`stream_id`、时间戳、关键帧和文件拆帧方式。
- 四类业务的 HTTP、MQTT、TiRTC 命令和会话恢复调用链。

开发者可以先在 Linux 上跑通这些链路，再保留协议、调用顺序和状态转移，替换平台与硬件部分。这能减少重新推导协议、重复处理 SDK 生命周期和重复设计并发状态机的工作。

### 不应直接复制的部分

- 默认 `main.c` 中的命令行、环境变量、`stdin` 和进程退出逻辑。
- `libcurl`、`libmosquitto`、`pthread`、Linux 文件系统和 `/dev/urandom` 适配。
- 文件媒体源、AI 录音文件和其他下行媒体丢弃逻辑。
- 单进程、单设备、单套媒体资源的固定容量和默认冲突策略。

### 工程成熟度与适用边界

仓库提供严格编译和主机单元测试，覆盖媒体拆帧、格式校验、回调屏障、会话仲裁、超时、迟到回调和 SDK 单实例生命周期。该实现适合作为**可运行的接入参考和二次开发起点**。

设备侧没有一份适用于所有 Linux 芯片、摄像头、声卡和产品形态的“行业标准 C 实现”。正式标准和 SDK 契约约束协议、API、媒体格式及安全要求；平台驱动、硬件资源和产品交互必须由产品实现。

本项目的定位是**可运行、可链接、可替换适配层的 Linux C 产品化参考架构**。它不是行业组织发布的统一设备标准，也没有通过具体产品的量产验收。

`DeviceAdapterV1` 约定了平台时间、身份、上行 source、下行 sink、产品动作、资源、恢复和安全接口。有界队列、回调外处理、单一生命周期所有者、有限状态机、代次隔离、单调时钟超时和幂等结束采用常见工程实践；具体驱动、参数、容量、优先级和恢复策略仍需按实际硬件实现并验证。

## 二次开发接口

公共契约位于 [`src/device_adapter.h`](src/device_adapter.h)，Linux 默认适配位于 [`src/linux_device_adapter.c`](src/linux_device_adapter.c)。产品必须在线程启动前安装一次 `DeviceAdapterV1`；核心会复制函数表，适配器的 `context` 及其指向的对象必须存活到进程退出。运行中不能替换函数表。

| 接口组 | 覆盖范围 | 关键契约 |
|---|---|---|
| `platform` | 单调时钟、墙上时钟、休眠 | 超时使用单调时钟；墙上时钟用于日志和签名时间 |
| `identity` | 凭证加载、原子保存、清除 | 不记录密钥；产品处理掉电、解绑和恢复出厂 |
| `media_source` | 真实上行音频、真实上行视频 | 在媒体工作线程调用；返回完整编码帧，数据有效到下一次取帧或关闭 |
| `media_sink` | 下行播放、视频显示 | `submit` 在 SDK 回调中调用，只能有界复制后立即返回；`flush` 清理已停止代次 |
| `product` | UI/按键动作和状态通知 | `notify` 可能来自 MQTT 回调或会话控制路径，须复制后非阻塞入队；产品动作仍经过统一仲裁 |
| `resource` | 麦克风、扬声器、摄像头、编解码器、屏幕等 | 一次申请完整资源集，失败自行回滚；释放必须幂等 |
| `recovery` | 分类故障上报 | 产品据此执行有界重试、降级、看门狗或重启策略 |
| `security` | 密码学随机数和不安全传输审批 | 生产实现必须拒绝不安全传输并使用经过批准的随机源 |

`media_source.next_*` 返回 `1` 表示得到一帧，`0` 表示正常结束，负数表示故障；`media_sink.submit` 返回 `0` 表示已复制接收，`DEVICE_ADAPTER_NOT_HANDLED` 表示使用 Linux 演示 fallback，负数表示产品明确丢弃或失败。详细线程和所有权规则以头文件注释为准。

## 二次开发 TODO

以下十项已有明确的适配入口，但**默认 Linux 适配没有实现真实产品能力**。使用真实设备时，开发者必须替换相应接口并逐项验收，不能因为函数表已经存在就把 TODO 标记为完成。

- [ ] **平台适配**：准备与目标 Linux CPU、ABI、libc 和工具链匹配的 TiRTC SDK；适配启动服务、网络管理、时钟、随机数、存储、日志和进程守护。
- [ ] **设备身份**：用设备安全存储替换 `device_creds.json`；实现首次绑定、预置凭证、解绑重绑、掉电保护、时间同步和身份注销策略。
- [ ] **真实上行音频**：用麦克风、音频 codec 和编码器替换音频文件；实现采样、编码、时间戳、缓冲、欠载/过载和发送节奏。
- [ ] **真实上行视频**：用摄像头和视频编码器替换 Annex-B/MJPEG 文件；实现帧边界、关键帧请求、PTS、分辨率、帧率和码率管理。
- [ ] **下行播放**：将 H5 talkback、AI、VoIP 和设备互呼的下行音频从 SDK 回调投递到有界播放队列，由独立任务解码、混音并驱动扬声器。
- [ ] **视频显示**：将下行视频投递到解码和显示队列；实现帧丢弃、旋转、镜像、缩放、宽高比和屏幕生命周期。
- [ ] **产品交互**：将终端命令替换为按键、触摸屏、应用 UI、LED、提示音或其他产品交互；明确来电、忙线、超时和错误反馈。
- [ ] **资源仲裁**：保留或按产品需求调整会话仲裁规则，并将逻辑会话映射到真实麦克风、扬声器、摄像头、编码器、屏幕和内存资源的申请与释放。
- [ ] **异常恢复**：实现网络重连、MQTT token 刷新、服务重新发现、业务超时、SDK 启动失败、队列满、任务异常、迟到回调、看门狗和进程拉起。
- [ ] **安全与量产**：实现安全注入或加密存储、可验证随机数源、时间防回拨、TLS/CA 更新、日志脱敏、最小权限、固件签名/OTA、弱网与长稳测试、故障注入和量产验收。

逐项实施和验收方法见 [从 Linux C 参考实现进行二次开发](../../device-porting.md)。

## 编译与测试

默认目标是 Linux x86_64，依赖 `libcurl`、`libmosquitto`、`cJSON`、`pthread` 和仓库附带的 TiRTC SDK。

```bash
sudo apt install libcurl4-openssl-dev libmosquitto-dev libcjson-dev pkg-config

cd thing-connect/device-sim/device-sim-c
make WERROR=1
make WERROR=1 test
make WERROR=1 framework
make WERROR=1 adapter-template-check
```

`make framework` 生成不含 `main` 的 `libdevice-reference.a`。[`examples/product_adapter_template.c`](examples/product_adapter_template.c) 从 Linux 默认适配器开始，按十项 TODO 逐组替换；也可将它编译为对象并注入参考入口：

```bash
make WERROR=1 adapter-template-check
make WERROR=1 product PRODUCT_ADAPTER_OBJ=obj/product_adapter_template.o
```

产品项目也可以链接 `libdevice-reference.a`。自有入口需要在创建工作线程前调用 `device_adapter_install()`，然后调用 [`device_reference_run()`](src/device_reference.h) 复用完整的上线和会话编排。薄入口 `reference_main.c` 不会进入静态库。

`make test` 会运行主机单元测试和适配器契约测试，并检查完整程序的 `--help` 启动路径。进行内存和未定义行为检查前，应先清理其他编译参数产生的对象文件：

```bash
make clean
make SANITIZE=address,undefined WERROR=1 test
```

在受调试器或 `ptrace` 管理的容器中，LeakSanitizer 可能直接报 `LeakSanitizer does not work under ptrace`。这表示泄漏检查没有启动，不表示发现泄漏，也不能据此宣称无泄漏。可先只验证 AddressSanitizer/UndefinedBehaviorSanitizer：

```bash
make clean
ASAN_OPTIONS=detect_leaks=0 make SANITIZE=address,undefined WERROR=1 test
```

泄漏检查必须在没有 tracer 的原生 Linux 或 CI 任务中另跑，并保持 `detect_leaks=1`。

Linux ARM、MIPS 或其他架构必须使用与 CPU、ABI 和 libc 匹配的 TiRTC SDK、交叉编译器和 sysroot；不能把 x86_64 动态库复制到其他架构运行。

## 快速运行

从本目录启动，默认媒体和 CA 路径才能正确解析：

```bash
./device-sim --mac AA:BB:CC:DD:EE:FF
```

首次运行会获取验证码并等待绑定。已有凭证时也可显式传入：

```bash
./device-sim --device-id DEV001 --device-key your-key
```

默认使用 `../assets/audio.g711a` 和 `../assets/video.h264`。程序启动后先运行 H5 实时流，再由同一终端入口处理 VoIP、AI 和设备互呼。

## 凭证持久化

首次绑定后，`device_id` 和 `device_key` 默认原子写入运行目录下的 `device_creds.json`，文件权限为 `0600`。

凭证优先级为：CLI 参数 → 环境变量 → `device_creds.json` → 验证码绑定。

```json
{"device_id":"TIRZ88CLF5CN","device_key":"..."}
```

这是 Linux 演示存储，不是产品密钥保护方案。真实设备必须完成“设备身份”和“安全与量产” TODO。

## 运行参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--device-id` | `$DEVICE_ID` | 已绑定设备 ID |
| `--device-key` | `$DEVICE_KEY` | 设备密钥 |
| `--creds-file` | `device_creds.json` | Linux 测试凭证文件 |
| `--mac` | `AA:BB:CC:DD:EE:FF` | 未绑定流程中上报的 MAC |
| `--endpoint` | `http://ep-open.tangeopen.com` | 服务发现入口 |
| `--timeout` | `190` | 等待 `auth_grant` 的秒数 |
| `--log-level` | `debug` | `debug` / `info` / `warn` / `error` |
| `--up-audio-file` | `../assets/audio.g711a` | 四类业务共用的上行编码音频文件 |
| `--up-audio-format` | `alaw_8khz` | 上行音频文件的声明格式 |
| `--down-audio-format` | `alaw_8khz` | 下行音频协商格式 |
| `--up-video-file` | `../assets/video.h264` | 实时流、VoIP 和设备互呼的上行编码视频文件；空字符串表示纯音频 |
| `--up-video-format` | `h264` | 上行视频文件的声明格式 |
| `--down-video-format` | `h264` | 下行视频协商格式；默认适配收到后丢弃，产品 sink 可显示 |
| `VOIP_VIDEO_RES_MODE` | `auto` | 微信 VoIP 下行视频分辨率模式：`auto` / `fit_screen` / `fill_screen`；后两者要求下行 MJPEG 和有效屏幕宽高 |
| `--ai-audio-file` | 与 `--up-audio-file` 相同 | AI 上行音频文件 |
| `--ai-up-audio-format` | 与 `--up-audio-format` 相同 | AI 上行音频格式 |
| `--down-media-dir` | `received` | AI 下行音频保存根目录 |
| `--ca-cert` | `../assets/ca-certificates.crt` | MQTT 与 HTTPS 共用的 CA 证书 |
| `--insecure` | 关闭 | 禁用 MQTT/HTTPS 证书校验，仅用于隔离测试环境 |

微信 VoIP 显示和分辨率参数通过 `VOIP_SCREEN_WIDTH`、`VOIP_SCREEN_HEIGHT`、`VOIP_VIDEO_RES_MODE`、`VOIP_CAMERA_ROTATION`、`VOIP_ASPECT_RATIO`、`VOIP_OBJECT_FIT`、`VOIP_HOR_MIRROR` 和 `VOIP_VERT_MIRROR` 环境变量配置；其含义见 [微信 VoIP 设备接入](../../device-voip.md#设备侧前提)。

使用 MJPEG 下行并将画面完整缩小到设备屏幕范围：

```bash
VOIP_SCREEN_WIDTH=640 VOIP_SCREEN_HEIGHT=480 VOIP_VIDEO_RES_MODE=fit_screen \
  ./device-sim --down-video-format mjpeg
```

## 终端命令

- `wxcall [N] [video|audio]`：列出或呼叫微信联系人。
- `call [N|device_id] [video|audio]`：列出或呼叫统一联系人；`voip` 类型走微信 VoIP，其他类型走设备 P2P。
- `aicall`：发起 AI 对话。
- `accept`、`reject [reason]`、`cancel`、`hangup`：处理当前来电或会话。
- `ct list|pending|add|accept|reject|del|remark`：查询和维护联系人。
- `room`、`help`、`exit`；常用缩写为 `w/a/r/h/e`。

`call` 和 `wxcall` 未指定通话类型时，有上行视频文件则默认为 `video`，否则为 `audio`。显式选择 `video` 时必须已配置视频文件。

## 架构与会话规则

```text
main.c
  ├─ DeviceAdapterV1         Linux 默认适配或产品适配（十项 TODO 边界）
  ├─ device_flow             HTTP、设备签名、临时/正式 MQTT、消息路由
  ├─ SessionArbiter          待接记录、唯一会话所有者、代次号和超时
  ├─ SessionCoordinator      STREAM / VOIP / AI / CALL 的停止、启动和恢复
  ├─ tirtc_runtime           进程级 SDK 生命周期、统一回调和连接代次
  ├─ tirtc_stream            H5 实时流
  ├─ tirtc_voip              微信 VoIP
  ├─ tirtc_ai                AI 对话
  └─ tirtc_call/call_session 设备 P2P 通话
```

默认会话策略：

- H5 实时流是空闲基线，待接来电不暂停它。
- 全局只有一个待接位，VoIP 和设备来电按仲裁器的到达顺序先到先得。
- VoIP 外呼、AI、设备外呼和接听来电互斥，不做跨业务抢占。
- 待接位绑定 `room_id` 和 45 秒超时；迟到取消不能清理后来的新会话。
- 业务从开始、连接到结束都携带代次号；旧回调不能结束新会话。
- 失败、拒接、取消、超时、远端挂断统一释放会话所有权并恢复 H5 实时流。
- MQTT 回调和 SDK 回调不执行阻塞 HTTP、文件 I/O 或线程创建。

以上是参考实现的默认策略，不属于 TiRTC SDK 的强制限制。产品可以按实际资源和交互调整。完整状态和并发处理见 [统一状态机](../../device-session-model.md) 和 [多业务会话仲裁](../../device-session-arbiter.md)。

## 媒体能力与限制

文件读取器支持已编码的：

- 音频：A-law 8/16 kHz、AMR-NB/WB、Ogg Opus 8/16 kHz、PCM S16LE 8/16 kHz、AAC ADTS 8/16 kHz。
- 视频：H.264/H.265 Annex-B、MJPEG。

AI 上下行格式支持 G.711A、PCM、AMR 和 Opus，不支持 AAC。启动时会按声明格式校验并拆分整个文件；声明与内容不匹配时拒绝启动。

| 场景 | 默认格式 | 默认间隔 | `stream_id` |
|---|---|---:|---:|
| H5 / VoIP / 设备通话上行音频 | G.711 A-law 8 kHz | 40 ms | 10 |
| AI 上行音频 | G.711 A-law 8 kHz | 20 ms | 1 |
| 上行视频 | H.264 Annex-B | 约 66.7 ms | 11 |

H.264/H.265 按 Annex-B Access Unit 拆帧，MJPEG 按 JPEG SOI/EOI 拆帧。文件源不转码；MP4 容器文件不能直接作为 H.264/H.265 输入。

二次开发的视频源应在 H5 视频订阅、远端关键帧请求或 `TIRTC_E_BUSY` 恢复时尽快返回关键帧。
使用预编码文件时，如果视频必须前进到下一个 IDR，音频读取位置也应移动到相同媒体时间；
音视频发送时间戳仍须单调递增，避免恢复后内容错位。

AI 下行音频默认保存为：

```text
received/<device_id>/ai_<timestamp>.raw
received/<device_id>/ai_<timestamp>.fmt.json
received/<device_id>/ai_<timestamp>.wav   # 仅 G.711A/PCM
```

## TiRTC SDK 核心 API

SDK API 定义和媒体帧字段以目标平台 SDK 的 [`tiRTC.h`](../sdk/linux-x86_64/2.2.1/include/tirtc/tiRTC.h) 为准。本实现的关键约束是：

1. `tirtc_runtime` 是 SDK 生命周期和 `TIRTCCALLBACKS` 的唯一所有者。
2. 设置 `TIRTC_OPT_DEVICE_SECRET_KEY` 和 `TIRTC_OPT_CLIENT_ID` 后，调用一次 `TiRtcStart(device_id, &callbacks)`。
3. `TiRtcStart` 返回 0 后仍要等待 `TIRTC_EVENT_SYS_STARTED`。
4. 业务模块只注册回调、管理连接和媒体，不调用 SDK 初始化或反初始化。
5. SDK 回调只做有界复制、状态提交或队列投递；断开、文件 I/O 和业务恢复在回调栈外执行。
6. 进程退出时，先停止当前业务并排空回调/控制队列，再 `TiRtcStop`，等待 `SYS_STOPPED` 后 `TiRtcUninit`。

## 各业务模块调用

下列代码只是生命周期索引，不代替 `main.c` 中的 Arbiter/Coordinator 调用。

```c
VoipState *voip = voip_create(voip_server, device_id, mqtt_token,
                              "../assets/audio.g711a");
AiState *ai = ai_create_ex(ai_server, device_id, mqtt_token,
                           "../assets/audio.g711a",
                           "alaw_8khz", "alaw_8khz");
CallState *call = call_create_ex(call_server, device_id, mqtt_token,
                                 "../assets/audio.g711a", "alaw_8khz",
                                 "../assets/video.h264", "h264");

stream_service_register();
voip_service_register();
ai_service_register();
call_service_register();
tirtc_runtime_start(device_id, device_key, client_id, tirtc_endpoint);

/* 由 SessionCoordinator 启动空闲实时流。 */
stream_service_start("../assets/video.h264", "../assets/audio.g711a",
                     "alaw_8khz", "h264");

/* VoIP：上报 profile，MQTT 收到来电后接听/拒接，或主动外呼。 */
voip_service_start(voip);
voip_accept_pending(voip);
voip_do_outgoing_call_ex(voip, contact, "video");

/* AI：获取业务凭证后建立 WHIP 连接。 */
ai_service_start(ai);
ai_get_token(ai_server, mqtt_token, device_id,
             peer_id, sizeof(peer_id), token, sizeof(token),
             role_id, sizeof(role_id));
ai_start_session(ai, peer_id, token, "../assets/audio.g711a",
                 device_id, role_id);

/* 设备互呼：主叫建房，被叫获取 token 后 TiRtcConnect。 */
call_service_start();
call_session_do_call(call, "TIRZ00000002", "video");
call_session_do_accept(call);

/* 退出：先通过 Coordinator/Arbiter 停止业务。 */
tirtc_runtime_stop();
voip_destroy(voip);
ai_destroy(ai);
call_destroy(call);
```

## 协议和业务文档

不在本 README 中重复维护全部字段定义。实施时按以下顺序查阅：

1. [设备上线与 MQTT](../../device-integration.md)：Report、token、临时和正式 MQTT、Topic、ACK、心跳和 token 过期。
2. [H5 实时查看与对讲](../../device-h5-live.md)：入站连接、音视频流和 talkback。
3. [AI 对讲](../../device-ai.md)：AI Token、WHIP、`0x2100` JSON-RPC 和会话事件。
4. [微信 VoIP](../../device-voip.md)：profile、授权联系人、来电、外呼、拒接和挂断。
5. [设备互呼](../../device-call.md)：联系人、建房、P2P 建连和房间恢复。
6. [统一状态机](../../device-session-model.md) 与 [会话竞态仲裁](../../device-session-arbiter.md)：多业务冲突、取消、代次和超时。

## 已知运行边界

- 默认从本目录运行，否则必须显式传入媒体、凭证和 CA 路径。
- 短暂 MQTT 断线由 libmosquitto 使用当前凭证重连；token 过期后，本程序不会在进程内刷新 token 或重建整个运行时。这属于“异常恢复” TODO。
- Linux 默认适配优先使用 `getrandom()`，再尝试 `/dev/urandom`；两者都失败时随机数请求失败，不使用伪随机 fallback。产品仍必须接入并验证自己的密码学安全随机数源。
- Linux 默认适配没有硬件 sink；“收到回调”不等于“已播放或已显示”。
- 只有单元测试不能证明真实服务互通、弱网长稳、媒体质量和产品安全；这些必须在目标环境另行验收。
