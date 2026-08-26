# 从 Linux C 参考实现进行二次开发

这份指南说明如何使用 [Linux C 设备端参考实现](device-sim/device-sim-c/README.md) 验证真实 Linux 设备，并在此基础上进行产品开发。

C 参考实现用于展示协议、TiRTC 调用顺序和会话处理，不是硬件固件。摄像头、麦克风、扬声器、显示屏和产品交互均由开发者实现。非 Linux 目标属于独立移植工程，应在自己的代码和文档中定义平台 API、构建和验收，不要混入 `device-sim-c` 的 Linux 运行说明。

## 先选择目标

### 目标 A：验证目标 Linux 能运行 TiRTC

这个目标只要求在目标 Linux 上交叉编译或本地编译 C 参考实现，继续用文件模拟媒体。它能验证：

- CPU/ABI/libc 与 TiRTC SDK 是否匹配。
- DNS、TLS、HTTP、MQTT 和 TiRTC 网络是否可用。
- 设备 Report、绑定、token、MQTT 和四类业务是否能在目标 Linux 运行。
- 已编码文件能否按 TiRTC 媒体帧契约发送，以及下行回调能否到达。

这不能证明真实采集、编码、播放、显示、掉电恢复和量产安全已完成。

### 目标 B：开发真实 Linux 产品

除了目标 A，还必须完成下面十项 TODO，用真实平台和硬件实现替换 Linux 演示逻辑，并在目标设备上进行功能、故障、长稳和安全验收。

## 目标形态：可产品化参考架构

参考架构将稳定的协议核心与产品差异隔开，不试图提供适用于所有硬件的通用固件：

```text
设备协议与会话核心
  ├── 上线、签名、MQTT 与业务协议
  ├── TiRTC 单实例生命周期与统一回调
  └── 会话状态、代次、超时和资源准入
                    │
             DeviceAdapterV1
                    │
  ├── 平台：单调时钟、墙上时钟、休眠
  ├── 身份：凭证读取、写入、解绑和恢复出厂
  ├── 媒体：音视频 source、audio sink、video sink
  ├── 产品：按键/UI/提示、资源申请与释放
  └── 运行：分类故障上报、随机数和不安全传输审批
```

C 参考实现提供版本化的 `DeviceAdapterV1`、Linux 默认适配、产品适配模板、独立静态库构建和契约测试。接口约定了函数表复制、上下文寿命、媒体帧所有权、SDK 回调限制、错误返回、会话代次、资源申请回滚和停止清理顺序。

这里所说的**Linux C 产品化参考架构和二次开发框架**，指这些边界可以替换和验证。默认文件适配不具备真实硬件能力，代码也没有通过任何具体产品的量产认证。

实际接口见 [`device_adapter.h`](device-sim/device-sim-c/src/device_adapter.h)，渐进替换入口见 [`product_adapter_template.c`](device-sim/device-sim-c/examples/product_adapter_template.c)。

产品可先调用 `linux_device_adapter_build()` 取得可运行的默认表，再完整替换一个接口组，最后调用一次 `device_adapter_install()`。使用自有进程入口时，可调用 [`device_reference_run()`](device-sim/device-sim-c/src/device_reference.h) 复用完整的上线与会话编排。运行期间不允许重装适配器。

### 接口返回值、线程与所有权

| 接口 | 调用线程 | 返回与所有权 |
|---|---|---|
| `media_source.open/next_*/close` | 对应业务的媒体工作线程 | `next_*`：`1` 为一帧，`0` 为正常结束，负数为故障；帧内存由产品持有到下次取帧或关闭 |
| `media_sink.submit` | TiRTC SDK 回调线程 | 返回前复制到产品自己的有界队列，不能保留 SDK 帧指针，不能播放、显示或阻塞 |
| `media_sink.flush` | 会话停止控制路径 | 清除指定业务和代次的残留帧，返回前不要求完成硬件关闭 |
| `product.poll_action` | 应用控制路径 | UI 动作经统一仲裁，超时返回 0 |
| `product.notify` | MQTT 回调或串行会话控制路径 | 必须复制栈上事件并非阻塞入队；不能反向重入会话切换接口 |
| `resource.acquire/release` | 串行会话切换路径 | `acquire` 失败前完成回滚；`release` 幂等 |
| `recovery.report` | MQTT 回调、工作线程或会话控制路径 | 只做非阻塞提交，不在调用栈中执行长耗时重启 |

## 可保留与必须替换的边界

| C 参考实现 | 可借鉴或保留 | 二次开发中必须按产品重做 |
|---|---|---|
| `device_flow.c` | 服务发现、Report/Token 顺序、签名串、MQTT 参数、Topic、ACK 和心跳 | 网络管理、Token 刷新主循环、时间同步、凭证安全存储 |
| `tirtc_runtime.c` | SDK 单实例生命周期、统一回调表、连接代次分发 | 与产品进程管理、看门狗、日志和故障拉起的集成 |
| `sdk_callback_guard.c` | 回调屏障、回调外延后操作、有界队列 | 队列容量、满队策略、线程优先级和实时性参数 |
| `session_arbiter.c` | 待接记录、唯一所有者、代次隔离、超时和幂等结束 | 业务优先级、抢占策略和真实硬件资源映射 |
| `session_coordinator.c` | STREAM / VOIP / AI / CALL 停止、启动和恢复顺序 | 真实采集、编码、播放、显示资源的启停与失败回滚 |
| `tirtc_stream.c` | H5 入站连接、订阅、帧参数和关键帧请求 | 用真实采集替换文件源，用播放/显示队列替换下行日志 |
| `tirtc_ai.c` | AI Token、WHIP、`0x2100`、延迟发送、会话状态 | 用真实音频链路替换文件上行和下行录音文件 |
| `tirtc_voip.c` | profile、授权列表、来电/外呼、WHIP、拒接和挂断 | 用真实媒体和产品交互替换文件上行、下行丢弃和 CLI |
| `tirtc_call.c` / `call_session.c` | 建房、接听、P2P 连接、`0x2000`、拒接/取消/挂断 | 用真实媒体、联系人 UI 和产品提示替换演示逻辑 |
| `main.c` / `Makefile` | Linux 调用顺序、构建参数示例 | CLI、`stdin`、环境变量、启动服务和发布流程 |
| `device_adapter.*` / `linux_device_adapter.*` | V1 线程、所有权、错误码、代次和默认 Linux 行为 | 各产品的驱动、队列、硬件策略、存储、UI、恢复与安全实现 |

## 二次开发 TODO

以下十项都需要在产品中实现，每项都列出了实现要求和验收条件。

### TODO 1：平台适配

#### 先获取匹配的 SDK

必须获取与目标 Linux 的 CPU、ABI、libc 和工具链匹配的 TiRTC C SDK。SDK 不匹配时，修改业务代码无法解决链接或运行问题。

建议 SDK 布局：

```text
thing-connect/device-sim/sdk/
└── <target-platform>/
    └── 2.3.0/
        ├── include/tirtc/
        └── lib/libTiRTC.so
```

交叉编译示例：

```bash
cd thing-connect/device-sim/device-sim-c
make clean
make WERROR=1 \
  CC=aarch64-linux-gnu-gcc \
  PKG_CONFIG=aarch64-linux-gnu-pkg-config \
  SYSROOT=/path/to/target-sysroot \
  SDK_PLATFORM=linux-aarch64 \
  SDK_VERSION=2.3.0
```

只构建不含参考 `main` 的静态库：

```bash
make WERROR=1 framework
```

`PKG_CONFIG` 必须返回目标 sysroot 中的 libcurl 和 libcrypto 参数，不能误用主机库。如果工具链没有 pkg-config wrapper，可显式传入 `CURL_CFLAGS`、`CURL_LIBS`、`OPENSSL_CFLAGS`、`OPENSSL_LIBS`、`CJSON_CFLAGS`、`CJSON_LIBS`、`MOSQUITTO_CFLAGS` 和 `MOSQUITTO_LIBS`。产品安装路径不保留仓库相对 SDK 布局时，设置 `RPATH=` 禁用演示 rpath，并由系统动态链接器配置目标库路径。

真实项目还需要适配：

- 启动顺序：网络、时间和凭证就绪后，再请求 token 并启动 TiRTC。
- 时钟：签名时间使用已校准的墙上时钟；会话超时使用单调时钟。
- 随机数：通过 `security.random_bytes` 接入并验证产品批准的密码学安全随机数源；Linux 默认实现只使用 `getrandom()` 或 `/dev/urandom`，失败时不会降级到伪随机数。
- 网络：处理网口/Wi-Fi/4G 的获取地址、DNS、切换和断网事件；使用 `TIRTC_NETCONN_4G` 时，在 `TiRtcStart` 前通过 `TIRTC_OPT_ICCID` 提供 ICCID。
- 进程：将 CLI 入口接到实际 daemon/service，实现看门狗和有序退出。

验收：冷启动、网络未就绪、校时失败、DNS 失败、SDK 动态库缺失和进程重启都有明确日志和可恢复路径。

### TODO 2：设备身份

C 参考实现的 `identity` 默认适配用 `device_creds.json` 保存 `device_id/device_key`，只用于 Linux 演示。产品需要完整替换 `DeviceIdentityOps`：

1. 定义未绑定、已绑定、已解绑和凭证损坏的持久化状态。
2. 用安全存储或设备密钥封装替换普通 JSON 文件。
3. 保留“本地凭证 → token；6006 → 带签名 Report 重新绑定”的顺序。
4. 区分验证码绑定与工厂预置凭证，不在日志中打印 `device_key`。
5. 定义解绑、设备转移、恢复出厂和存储写失败的产品行为。

验收：首次绑定、掉电重启、服务端解绑、本地凭证损坏和存储写入失败均不会泄露凭证或进入无限重试。

### TODO 3：真实上行音频

实现 `DeviceMediaSourceOps`，将默认 `FileMediaSource` 的音频输出替换为：

```text
麦克风 / 音频 codec → 采样 → 预处理 → 编码 → 有界发送队列 → TiRtcSendAudioStream
```

保留的媒体契约：

| 业务 | 默认 `stream_id` | 参考格式 |
|---|---:|---|
| H5 实时流 | 10 | `alaw`（G.711 A-law）8 kHz 单声道 |
| 微信 VoIP | 10 | 以 profile 上报和房间协商为准 |
| 设备互呼 | 10 | 以双方设备能力为准 |
| AI | 1 | 以 `start_session.input_audio` 为准 |

TiRTC 要求所有媒体、消息、订阅和关键帧请求使用 `0..15` 范围内的 `stream_id`，表中的默认值均满足该约束。

需要实现发送节奏、PTS、输入欠载、发送缓冲满、音频中断、采集设备重启和媒体切换。不能在 TiRTC 回调线程中采集、编码或等待音频硬件。

验收：无音频累积延迟、无长时间漂移，采集中断后可恢复，会话切换不会重复占用麦克风。

### TODO 4：真实上行视频

在同一 `DeviceMediaSourceOps` 中将视频文件源替换为：

```text
摄像头 → 图像处理 → H.264/H.265/MJPEG 编码 → Access Unit 队列 → TiRtcSendVideoStream
```

开发者必须根据 H5、VoIP 对端和设备互呼的实际能力选择编码格式，不能因为参考程序能拆 MJPEG/H.265 文件就假设所有对端都能播放。

需要实现：

- 完整编码帧边界和正确 `media/flags/ts/length`。
- 第一帧为关键帧，并响应 `on_request_key_frame`。
- 码率、帧率、分辨率、旋转和镜像与上报 profile 一致。
- 队列满时优先丢弃可丢帧，在下一个关键帧恢复。

验收：H5、VoIP 对端和另一台设备均能在声明的分辨率/帧率下解码，关键帧请求能恢复画面。

### TODO 5：下行播放

C 参考实现通过 `DeviceMediaSinkOps.submit` 投递下行帧，但默认没有硬件播放。产品 sink 应实现：

```text
on_audio → 校验会话/代次/格式 → 复制到有界环形队列 → 返回
                                                        ↓
                                      独立播放任务解码并驱动扬声器
```

H5 talkback 默认使用 `stream_id=14`；AI、VoIP 和设备互呼以各业务协商格式为准。播放任务需处理抖动缓冲、解码错误、静音、音量、开关功放和快速终止。

验收：四类业务的下行音频可听，无回调阻塞，挂断后不播放旧会话残留音频。

### TODO 6：视频显示

C 参考实现用同一 `DeviceMediaSinkOps.submit` 的 `video` 字段区分视频。默认不解码或显示，产品需要与音频相同的回调外队列，并实现：

- 按会话和代次清理迟到视频帧。
- 解码器重置、关键帧等待和错误恢复。
- 显示旋转、镜像、缩放、裁切、宽高比和屏幕开关。
- 解码或显示来不及时的明确丢帧策略。

验收：视频姿态和 profile 一致，切换/挂断不显示旧会话画面，解码过载不拖慢 SDK 回调。

### TODO 7：产品交互

终端命令只是默认 `DeviceProductOps` 测试入口。产品需用 `poll_action/notify` 将下列动作映射到实际 UI/按键：

- AI 开始、结束或 PTT 按下/松开。
- VoIP 和设备互呼的联系人选择、外呼、接听、拒接、取消和挂断。
- 来电、连接中、通话中、忙线、超时、断网和鉴权失效的可见反馈。
- 并发操作和重复按键的防抖、幂等和禁用状态。

UI 不应直接调用底层断开或修改业务私有状态；所有会话操作仍经过统一仲裁入口。

验收：快速重复操作、两类来电同时到达、连接中取消和断网时操作都不会造成重复会话或 UI 假状态。

### TODO 8：资源仲裁

C 参考实现包含逻辑会话仲裁，并在会话切换时调用 `DeviceResourceOps.acquire/release`，但默认操作为空，不管理真实硬件。二次开发必须将：

```text
STREAM / VOIP / AI / CALL
          ↓
麦克风、扬声器、摄像头、编码器、解码器、屏幕、内存和带宽
```

建立明确映射。默认非抢占策略可以保留，也可按产品需求调整；不论选择哪种策略，都必须定义资源申请、启动失败回滚、结束释放和 H5 实时流恢复。

验收：并发来电、AI 中来电、连接中取消、启动硬件失败和迟到回调不会双重占用或遗留硬件资源。

### TODO 9：异常恢复

C 参考实现演示了业务级超时和会话恢复，并通过 `DeviceRecoveryOps.report` 分类提交关键失败，但默认不执行产品恢复策略。产品还需要运行时主循环：

```text
网络就绪
  → 服务发现
  → 加载/绑定设备身份
  → 获取 MQTT Token
  → 启动正式 MQTT 和 TiRTC
  → 运行
  → 根据错误类型回到对应步骤
```

必须区分：

- 短暂断网：使用当前 token 有界退避重连。
- token 过期或认证失败：重新请求 token，不用旧 token 无限重连。
- 6006/解绑：进入重新绑定，不当作普通网络错误。
- 服务入口失效：重新服务发现。
- SDK 启动失败：完整反初始化后再有界重试。
- 业务超时：使当前代次失效、释放会话、恢复 H5。
- 队列满：可观测地丢弃、合并或触发状态校准，不静默遗失生命周期事件。

所有重试必须有上限、退避和可观测错误，不应将网络故障变成 CPU/流量无限循环。

验收：注入 DNS 失败、HTTP 超时、MQTT 断开、Token 过期、SDK 不回调、队列满和媒体任务退出，设备能恢复或进入明确的降级状态。

### TODO 10：安全与量产

最低要求：

- 默认启用 TLS 证书链和主机名校验；产品不开放 `--insecure` 能力。
- 凭证、MQTT token、WHIP token 和用户媒体不写入普通日志。
- 使用安全注入、加密存储或安全芯片保护长期凭证。
- 使用密码学安全随机数，并保证签名时间不可被未授权回拨。
- 让 `DeviceSecurityOps.allow_insecure_transport` 在生产配置恒为 false；测试开关不能进入量产镜像。
- 定义 CA 更新、固件签名、OTA、回滚和恢复出厂流程。
- 使用专用低权限账号运行设备进程，限制文件、设备节点和网络权限。
- 建立版本兼容矩阵、软件物料清单、漏洞修复流程和现场日志脱敏规则。

验收：安全评审、弱网长稳、长时间内存/句柄监测、升级与回滚、掉电注入和批量出厂/绑定流程均通过。

## 建议实施顺序

1. **先在开发机跑通 C 参考实现**：使协议、账号和服务环境可验证，并运行 `make WERROR=1 test`。
2. **复制产品适配模板并在目标 Linux 使用默认文件媒体运行**：排除 SDK、工具链、TLS、MQTT 和 RTC 环境问题。
3. **完成设备身份和运行时主循环**：确保掉电、断网和 token 过期可恢复。
4. **先接入 H5 真实音频上下行**：验证采集、编码、队列和播放。
5. **再接入 H5 真实视频**：验证编码格式、关键帧和弱网恢复。
6. **按 AI → VoIP → 设备互呼增加业务**：每增加一项，同时验证结束后恢复 H5。
7. **接入产品交互和真实资源仲裁**：不允许 UI 绕过仲裁器直接操作 SDK 会话。
8. **完成故障注入、长稳、安全和量产验收**。

## 验收清单

- [ ] 冷启动和掉电后能正确读取设备身份并上线。
- [ ] 无凭证、凭证损坏、服务端解绑和 6006 都进入正确绑定路径。
- [ ] MQTT token 过期后能重新获取 token 并恢复订阅。
- [ ] H5 可以看、听和对讲，下行音频由扬声器播放。
- [ ] AI、VoIP 和设备互呼的上下行媒体均使用真实硬件。
- [ ] 四类业务的同时触发、拒接、取消、超时和迟到回调符合产品策略。
- [ ] 前台会话结束后恢复 H5，不留下旧会话音视频或硬件资源。
- [ ] TiRTC 回调中无 HTTP、MQTT、文件 I/O、编解码、播放、显示或阻塞等待。
- [ ] 队列满、SDK 不回调、网络反复断开和媒体设备重启都有可观测恢复路径。
- [ ] 日志不包含 `device_key`、完整 token 或用户敏感媒体。
- [ ] 通过目标设备的弱网、长稳、资源、温度、安全、升级和掉电测试。

## 相关文档

- [Linux C 参考实现 README](device-sim/device-sim-c/README.md)
- [设备上线与 MQTT](device-integration.md)
- [H5 实时查看与对讲](device-h5-live.md)
- [AI 对讲](device-ai.md)
- [微信 VoIP](device-voip.md)
- [设备互呼](device-call.md)
- [统一状态机](device-session-model.md)
- [多业务会话仲裁](device-session-arbiter.md)
