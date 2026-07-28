# ESP32-S3 设备参考实现需求基线

本文档是 ESP32-S3 参考实现的需求与架构基线。后续实现、评审和跨芯片移植均以本文为准；现有
`device-sim-c` 继续作为嵌入式 Linux 参考应用，不直接复制到 ESP-IDF。

## 1. 目标与范围

- 首个目标平台：ESP32-S3，推荐 16 MB Flash、8 MB PSRAM；默认 Demo 不依赖 TF 卡。
- 后续平台：ESP32-P4、其他 ESP-IDF 芯片、ARM/MIPS 嵌入式 Linux、君正 S10 等。
- 功能：H5 音视频、AI 对讲、VoIP、设备互呼。
- ESP32-S3 从 Flash 媒体分区读取小型编码文件并循环发送，不在设备上转码。
- 不保存收到的媒体。无屏/无喇叭时只输出限频帧日志；产品可接入显示和播放模块。

## 2. 设备级媒体配置

一台设备只使用一份媒体配置，所有业务共享，不为每个业务或每个方向配置不同编码。

### 音频

- 默认：G711A、8 kHz、单声道、20 ms/包。
- 支持：G711A 8/16 kHz、AMR-NB 8 kHz、AMR-WB 16 kHz、Opus 8/16 kHz。
- AMR 和 Opus 是变长编码帧，读取和发送时必须保留帧边界。
- G711A 16 kHz 是否可互通，以 TiRTC SDK 和服务端协议定义为准。

### 视频

- 单台设备只配置并打包一种视频格式，不会同时使用 MJPEG 和 H264：选择 MJPEG 时固定为 8 fps，选择 H264 时固定为 15 fps。
- 支持：MJPEG、H264。
- H265 只预留枚举和扩展接口，本阶段不实现。
- 配置项 `uplink_enabled` 和 `downlink_enabled` 分别控制是否上传、接收视频。
- 上下行均关闭时，设备是纯音频产品。

ESP32-S3 可以透传 Flash 中已经编码好的 H264 文件，但这不代表它负责把摄像头原始画面实时编码成
H264。真实摄像头场景默认使用摄像头直接输出的 JPEG/MJPEG；如外部硬件直接输出 H264，也可透传。

## 3. 各业务的媒体方向

| 业务 | 音频 | 上行视频 | 下行视频 |
|---|---|---|---|
| H5 | 双向 | 取设备配置 | 不支持 |
| AI 对讲 | 双向 | 取设备配置 | 本阶段不需要 |
| VoIP | 双向 | 取设备配置 | 取设备配置 |
| 设备互呼 | 双向 | 取设备配置 | 取设备配置 |

协议自身的限制优先于设备配置。例如设备允许接收视频，H5 仍不会建立下行视频。

## 4. Flash 媒体与下行处理

参考 Demo 不依赖 TF 卡。小型测试素材制作成独立 Flash 文件系统镜像：

```text
/media/
├── media_profile.json
├── audio_g711a_8khz_mono_20ms_10s_500packets.g711a
└── video_mjpeg_640x480_8fps_10s_80frames.mjpeg
```

`media_profile.json` 是设备实际采用的唯一媒体配置，文件名用于让开发者直观看懂素材内容。
程序不通过解析文件名推断媒体参数，而是读取配置并校验素材。默认 MJPEG 配置示例：

```json
{
  "audio": {
    "file": "audio_g711a_8khz_mono_20ms_10s_500packets.g711a",
    "codec": "g711a",
    "sample_rate_hz": 8000,
    "channels": 1,
    "packet_ms": 20,
    "duration_ms": 10000,
    "packet_count": 500
  },
  "video": {
    "file": "video_mjpeg_640x480_8fps_10s_80frames.mjpeg",
    "codec": "mjpeg",
    "width": 640,
    "height": 480,
    "fps": 8,
    "duration_ms": 10000,
    "frame_count": 80,
    "uplink_enabled": true,
    "downlink_enabled": true
  }
}
```

选择 H264 的产品仍使用同一个 `video` 配置对象，将视频文件改为
`video_h264_annexb_640x480_15fps_10s_150frames.h264`，同时把 `codec`、`fps`、
`frame_count` 分别改为 `h264`、`15`、`150`。Flash 中不再放 MJPEG 文件。

- 音视频素材统一为 10 秒，到结尾后同步循环发送。
- 视频分辨率为 640×480（4:3）或 640×360（16:9）。
- MJPEG 按 8 fps 发送，10 秒共 80 帧，视频 PTS 每帧递增 125 ms。
- 裸 MJPEG 连续流不携带帧率元数据，`ffprobe` 等工具可能显示默认/推测帧率；实际发送帧率只以
  `media_profile.json` 和运行时 PTS 调度为准。
- H264 按 15 fps 发送，10 秒共 150 帧；PTS 按 `frame_index * 1000 / 15` 计算，避免整数步进累计漂移。
- 单台设备只打包配置引用的一个视频素材，不同时打包 MJPEG 和 H264。
- 纯音频配置将两个视频开关都设为 `false`、`file` 设为空字符串，不打包视频文件也不分配视频帧缓冲。
- 启动时校验配置与素材：文件必须存在，G711A 文件大小、音频包数和视频实际帧数必须与配置一致。
- G711A 为 8 kHz、单声道、20 ms/包，10 秒文件固定为 80,000 字节、500 包。
- 默认 MJPEG 素材约 1.08 MB，G711A 素材 80 KB；当前媒体分区为 1.5 MiB，
  替换素材时 SPIFFS 镜像生成步骤会检查容量。
- MJPEG 按 JPEG SOI/EOI（`FFD8`/`FFD9`）拆帧，每帧作为关键帧发送。
- H264 默认读取裸 Annex-B 码流，识别 SPS、PPS、IDR 和普通访问单元。
- G711A 8 kHz、20 ms 每包 160 字节；其他设备配置为 16 kHz 时每包 320 字节。
- AMR 按标准文件头和 TOC 拆帧；Opus 使用 `TIRTCOPUS1\n` 文件头、2 字节大端包长和 payload。
- 收到音频时，有喇叭的产品投递到音频解码/播放任务；无喇叭时只记录帧元数据。
- 收到视频时，有屏的产品投递到视频解码/显示任务；无屏时只记录帧元数据。
- 默认参考实现不保存下行媒体，也不包含录像、容量管理和删除功能。
- TiRTC 回调不得打印原始 payload，也不得每帧同步打印；只打印首批帧和周期统计。

## 5. Wi-Fi 配置

无 TF 卡的默认 Demo 使用串口或 SoftAP 网页首次配置 Wi-Fi，并保存到 NVS。

处理规则：

1. 启动时优先读取 NVS 中最后一次成功配置。
2. 没有配置或连接失败时进入串口/SoftAP 配置模式。
3. 新配置连接成功后写入 NVS。
4. 修改配置不需要改代码或重新编译固件。

Demo 可使用明文配置；正式产品应增加 NVS 加密或安全注入方案。

## 6. AI、呼叫和交互

### AI 对讲

- AI 使用按住保持会话（PTT）的交互。
- 按下：建立 AI 会话并持续发送音频；允许按设备配置同时上传视频。
- 松开：停止媒体、发送结束会话、断开连接，回到空闲态并等待 H5 重连。
- 文件模拟期间，音频文件按时间戳连续循环发送，直到松开。

### VoIP 与设备互呼

两种呼叫均支持：发起、主叫取消、接听、拒接、挂断、忙线、超时和异常断开。

- `cancel`：主叫尚未接通时取消。
- `reject`：被叫振铃时拒接。
- `hangup`：已经接通后结束。
- 无屏设备默认呼叫第一个联系人；无屏不等于只能使用 AI。

### 基础交互

串口命令是所有开发板必须支持的基础交互：

```text
status
ai-press
ai-release
voip-call
contacts
call
cancel
accept
reject
hangup
wifi-set <ssid> <password>
wifi-clear
tirtc-set <device_id> <device_key> [client_id] [endpoint]  # 仅预烧/联调
tirtc-clear                                                # 清除后重新验证码绑定
```

产品可增加三键、LCD、LED、提示音或本地网页。GPIO 只在板级配置中定义，业务代码只接收抽象动作。

## 7. 会话模型

单一 SessionManager 任务拥有所有会话状态，MQTT、TiRTC、按键、串口和定时器只向它投递事件。

```text
OFFLINE
IDLE
H5_STREAMING
AI_CONNECTING
AI_ACTIVE
RINGING
CALLING
IN_CALL
RESTORING_H5
```

- 前台通话优先级高于振铃、AI 和 H5。
- VoIP 与设备互呼同级；已有通话时新来电返回忙线。
- AI 或已有通话占用媒体链路时，新来电按忙线处理；振铃态由用户决定接听或拒接。
- 前台会话结束后释放对应媒体链路，回到空闲态并接受 H5 重连。
- TiRTC 连接使用 generation id 让媒体源在换连接时从素材起点重新发送；业务异步响应由
  SessionManager 按当前状态和业务类型再次校验。

## 8. 代码边界

```text
device-sim/
├── common/                 # 纯 C 公共模型、状态机、媒体读取和命令解析
├── device-sim-c/           # Linux 应用与 POSIX 适配
├── device-sim-esp32/       # ESP-IDF 应用与 FreeRTOS/Flash/NVS/Wi-Fi/HTTP/MQTT 适配
├── sdk/                    # 按平台和版本存放 TiRTC SDK
└── assets/                 # 测试媒体
```

公共代码不得直接依赖 pthread、FreeRTOS、POSIX、FatFS、ESP-IDF 或 TiRTC SDK 类型。TiRTC 公开类型只允许出现在
`tirtc_adapter` 内。Linux 与 ESP-IDF 的任务、锁、时钟、网络、存储和硬件驱动分别实现平台接口。

## 9. SDK 与构建

ESP32-S3 SDK 默认位置：

```text
sdk/espressif-esp32s3/2.2.1/
├── include/tirtc/
├── lib/libTiRTC.a
└── manifest/build-contract.env
```

SDK 路径必须可以通过 `TIRTC_SDK_DIR` 或构建参数覆盖，不把库复制到应用组件中。2.2.1 包要求 ESP-IDF
5.5.x、`CONFIG_FREERTOS_HZ=1000`，并关闭 FreeRTOS trace/stats 相关配置。旧工程中的 UNICORE 是旧版 SDK
问题的规避项，新 SDK 不直接继承，需通过双核实机压力测试决定。

嵌入式 Linux 使用目标 CPU/ABI/libc 匹配的 TiRTC SDK、交叉工具链和 sysroot；原有 x86_64 二进制不能复制到
ARM/MIPS 设备执行。

ESP32-S3 业务信令沿用 `device-sim-c` 的服务发现、HMAC-SHA256 设备登录、业务 HTTP、永久 MQTT、
ACK 和心跳协议；`platform_client` 负责这些平台通信，`tirtc_adapter` 只负责媒体连接、命令和帧。
首次启动沿用 `device-sim-c` 的验证码绑定：设备上报 MAC，显示验证码，通过临时 MQTT 等待
`auth_grant`，ACK 后将 `device_id/device_key` 保存到 NVS。以后直接读取凭证；解绑后自动重新绑定。

## 10. 实施顺序

1. 已完成：ESP-IDF 工程、SDK 外部链接和构建契约验证。
2. 已完成：公共设备配置、媒体读取和 SessionManager。
3. 已完成：Flash 媒体、NVS Wi-Fi、串口和 SoftAP 配置。
4. 已完成：MJPEG/H264、G711A/AMR/Opus 文件源、校验和定时发送。
5. 已完成：验证码绑定、凭证 NVS 持久化、解绑重绑和正式平台登录。
6. 已完成参考代码：H5、AI、VoIP、设备互呼和平台信令；仍需账号与 ESP32-S3 实机联调。
7. 产品扩展：真实 I2S、摄像头、物理按键、屏幕和视频渲染模块。
