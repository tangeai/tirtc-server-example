# device-sim-esp32

ESP32-S3 TiRTC 设备参考实现，目标硬件为 ESP32-S3 N16R8（16 MB Flash、8 MB
octal PSRAM），使用 ESP-IDF 5.5.x 和 TiRTC SDK 2.2.1。默认 Demo 不依赖 TF 卡，
不保存下行媒体。

需求与跨芯片边界见 [ESP32S3_REFERENCE.md](../ESP32S3_REFERENCE.md)。

## 已实现

- TiRTC SDK 外部目录选择和构建契约校验。
- SPIFFS 媒体分区、`media_profile.json`、素材启动校验和音视频按 PTS 循环发送。
- G711A 固定包、AMR-NB/AMR-WB 标准文件帧、Opus 包容器、MJPEG 和 H264 Annex-B
  读取器。
- Wi-Fi NVS 持久化、串口修改、无配置/连接失败时的 SoftAP 配网页面。
- 首次验证码绑定、临时 MQTT `auth_grant`/ACK、设备凭证 NVS 持久化和解绑重绑。
- 服务发现、SNTP、HMAC-SHA256 设备登录、业务 HTTP、永久 MQTT、ACK 和心跳。
- H5 入站推流、AI PTT、VoIP、设备互呼的统一会话状态和串口交互。
- 下行媒体不落盘：无喇叭/屏幕时输出限频元数据日志，并保留产品接入 TODO。
- H5 和 AI 拒绝下行视频；VoIP/设备互呼按设备唯一媒体配置决定是否接收视频。

当前仓库已完成主机单元测试以及 MJPEG、H264、纯音频三种 ESP-IDF 固件构建。真实账号、
MQTT 信令、P2P/WHIP 媒体互通仍须在 ESP32-S3 实机和对应服务环境上联调。

## 目录

```text
device-sim-esp32/
├── components/
│   ├── platform_client/   # 服务发现、签名登录、HTTP、MQTT
│   ├── session_runtime/   # H5/AI/VoIP/设备互呼状态与交互
│   ├── media_runtime/     # Flash 素材校验、定时读取和发送
│   ├── tirtc_adapter/     # 唯一允许直接依赖 TiRTC 类型的组件
│   ├── wifi_manager/      # NVS、STA、SoftAP 配网页面
│   ├── runtime_config/    # 设备凭证 NVS
│   └── device_console/    # 串口命令
├── media/                 # 默认 MJPEG 配置，只烧录这一套
├── media-profiles/        # 可选 H264、audio-only 配置，不参与默认烧录
└── partitions.csv         # 4 MiB app + 1.5 MiB media
```

平台无关的配置、会话规则和文件拆帧代码位于 `../common/device_core`。

## SDK 位置

默认读取：

```text
../sdk/espressif-esp32s3/2.2.1/
├── include/tirtc/
├── lib/libTiRTC.a
└── manifest/build-contract.env
```

可指定其他 SDK 根目录：

```bash
idf.py -DTIRTC_SDK_DIR=/absolute/path/to/espressif-esp32s3/2.2.1 build
```

## 构建与烧录

默认 MJPEG 640×480、8 fps、10 秒：

```bash
idf.py set-target esp32s3
idf.py build
idf.py -p /dev/ttyUSB0 flash monitor
```

`flash` 会同时烧录应用和 `media.bin`。默认素材是：

```text
audio_g711a_8khz_mono_20ms_10s_500packets.g711a
video_mjpeg_640x480_8fps_10s_80frames.mjpeg
```

构建 H264 640×480、15 fps、10 秒版本时使用独立构建目录：

```bash
idf.py -B build-h264 \
  -DDEVICE_MEDIA_DIR="$PWD/media-profiles/h264" \
  build
idf.py -B build-h264 -p /dev/ttyUSB0 flash monitor
```

H264 目录只含 G711A 和
`video_h264_annexb_640x480_15fps_10s_150frames.h264`。两种视频不会同时进入同一个
Flash 镜像。

纯音频设备可选择不含视频文件、也不分配视频帧缓冲的配置：

```bash
idf.py -B build-audio-only \
  -DDEVICE_MEDIA_DIR="$PWD/media-profiles/audio-only" \
  build
```

## 首次配置

设备没有 Wi-Fi 配置时会启动：

```text
SSID: TiRTC-Setup-XXXX
密码: tirtc1234
网页: http://192.168.4.1
```

网页提交 SSID/密码后写入 NVS 并重启。也可使用串口：

```text
wifi-set "My WiFi" "12345678"
wifi-clear
```

Wi-Fi 连通后，如果 NVS 中没有设备凭证，设备会自动：

1. 上报 Wi-Fi STA MAC，获取验证码和临时 MQTT 凭证。
2. 在串口醒目打印验证码，等待用户在 H5 绑定页输入。
3. 接收临时 MQTT 的 `auth_grant`，发送 QoS1 ACK。
4. 将下发的 `device_id/device_key` 写入 NVS，随后启动正式 MQTT 和 TiRTC。

以后开机直接读取 NVS，不再要求输入验证码。收到 `unbind` 或登录返回设备已解绑时，会重新进入绑定流程。
`tirtc-set` 仅作为预烧凭证和底层联调入口，不是正常用户配置步骤；密钥不会打印到日志：

```text
tirtc-set <device_id> <device_key>
tirtc-set <device_id> <device_key> <client_id> <可选TiRTC端点>
tirtc-clear  # 清除绑定凭证，重启后重新显示验证码
```

端点留空时通过 `https://ep-open.tangeopen.com/services` 做服务发现。

## 串口交互

```text
status          查看 Wi-Fi、平台、MQTT、TiRTC、会话和媒体配置
ai-press        模拟按住 AI 键，建连后持续循环发送配置的音频/可选视频
ai-release      模拟松开 AI 键，结束 AI 会话
voip-call       呼叫第一个授权 VoIP 联系人
contacts        查看设备互呼联系人
call            呼叫第一个设备联系人
accept          接听 VoIP 或设备来电
reject          拒接来电
cancel          取消尚未接通的外呼
hangup          挂断当前会话
restart         重启
```

底层联调命令 `voip-connect <service_description> <token>` 和
`call-direct <remote_device_id> <token>` 可绕过业务信令直接验证 SDK 连接。

串口命令就是所有开发板共有的基础交互。产品的三键、触摸屏或按键矩阵只需要把动作映射到
`session_runtime_*()` 接口，不需要修改会话业务代码。

## 媒体配置

一台设备只读取一份 `media_profile.json`，所有业务共用同一音频、视频编码和上下行视频开关。

- G711A：裸数据，20 ms 固定包；8 kHz 每包 160 字节，16 kHz 每包 320 字节。
- AMR-NB：标准 `#!AMR\n` 文件；AMR-WB：标准 `#!AMR-WB\n` 文件；按 TOC 保留帧边界。
- Opus：Demo 容器以 `TIRTCOPUS1\n` 开头，每包为 2 字节大端长度加 Opus payload。
- MJPEG：按 JPEG SOI/EOI 拆帧，每帧作为关键帧。
- H264：裸 Annex-B，按访问单元拆帧并识别 IDR。

裸 MJPEG 连续流没有帧率元数据，分析工具可能显示推测帧率；本 Demo 始终按配置中的
`fps=8` 和 PTS 节拍发送。

AMR/Opus 配置也要求 10 秒、20 ms/包、共 500 包。启动校验不通过时不会启动媒体发送。

## 下行接入点

当前下行回调只统计并限频打印，不写 Flash：

- 有喇叭：在 `tirtc_adapter.c` 的 `TODO(product-audio)` 后投递到独立播放任务。
- 有屏：在 `TODO(product-video)` 后投递到独立显示任务。

不要在 TiRTC SDK 回调线程中同步解码、播放、渲染或逐帧打印。
