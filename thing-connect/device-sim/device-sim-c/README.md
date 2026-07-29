# device-sim-c — C 语言设备端参考实现

基于 TiRTC SDK 的 IoT 设备端 「C 参考实现」，可编译运行在 Linux 嵌入式设备上，用于验证设备上线、鉴权、MQTT 信令、RTC 建连和媒体收发的完整流程。它从本地文件读取上行音视频，不启用摄像头、麦克风或扬声器。Linux 代码中的协议、状态机和 TiRTC 调用顺序可作为产品参考；RTOS 平台必须重写网络、任务、存储和媒体适配层，不能直接编译本目录源码。

**定位：Linux 文件级模拟。** 从文件读取音视频发送；所有 RTC 场景收到的下行音视频仅限频记录日志，随后立即丢弃。程序没有接收文件、接收目录或播放路径，也不适配真实摄像头、麦克风和扬声器。这是嵌入式设备端的协议与 TiRTC 调用参考；产品移植边界、Linux 交叉编译和 RTOS 模块拆分见 [从 「C 参考实现」移植到嵌入式设备](../../device-porting.md)。

**统一运行时：** 程序启动后先常驻实时推流；VoIP、AI 对讲和设备互呼按需独占 TiRTC SDK，会话结束后自动恢复实时推流。四项业务由同一个 MQTT 长连接和终端命令入口处理，不再通过 `--with-*` 选择互斥模式。

四个业务场景：**音视频推流 / VoIP 对讲 / AI 对话 / 设备间 P2P 通话**。

> TiRTC SDK C API 文档：[docs.tange.ai](https://docs.tange.ai/products/tirtc/api-reference/c.html)

## 凭证持久化

首次绑定成功后，`device_id` 和 `device_key` 自动保存到 `device_creds.json`（运行目录下）。下次启动无需再传 `--device-id` / `--device-key`，程序自动加载。

**凭证优先级：** CLI 参数 → 环境变量 → `device_creds.json` → 扫码绑定。

```json
{"device_id": "TIRZ88CLF5CN", "device_key": "..."}
```

**嵌入式移植**：替换 `save_creds_to_file()` / `load_creds_from_file()` 为 Flash 读写（NVS / LittleFS / EEPROM），JSON 格式不变。

## 编译（Linux x86_64）

```bash
# 安装依赖
sudo apt install libcurl4-openssl-dev libmosquitto-dev libcjson-dev pkg-config

# 编译
cd device-sim-c && make

# 直接在本目录运行；默认媒体位于 ../assets/
./device-sim --device-id DEV001 --device-key your-key
```

持续集成或交付前使用 `make WERROR=1` 将编译警告视为错误，并用 `make WERROR=1 test` 运行文件分帧与 SDK 回调屏障测试。

仓库已内置默认的 `../assets/video.h264` 和 `../assets/audio.g711a`，可直接启动。需要额外格式素材时再生成：
```bash
bash ../scripts/gen_assets.sh
```
生成 PCM / G.711a / Opus / AMR 测试音频，以及 H.264 / MJPEG 测试视频到
`assets/`。音频默认优先使用 Microsoft Edge TTS；安装或在线合成失败时自动回退到
`espeak-ng`。详细选项见 [Python 模拟器素材说明](../device-sim-py/README.md#生成扩展测试素材)。

## 命令行

```bash
# 首次上线（未绑定）
./device-sim --mac AA:BB:CC:DD:EE:FF

# 统一设备运行时：默认推流，同时可在终端使用 wxcall / aicall / call
./device-sim --device-id DEV001 --device-key your-key
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--device-id` | `$DEVICE_ID` | 已绑定设备 ID |
| `--device-key` | `$DEVICE_KEY` | 设备密钥 |
| `--mac` | `AA:BB:CC:DD:EE:FF` | 设备 MAC（未绑定流程） |
| `--endpoint` | `http://ep-open.tangeopen.com` | 服务发现入口 |
| `--log-level` | `debug` | `debug` / `info` / `warn` / `error` |
| `--up-audio-file` | `../assets/audio.g711a` | 推流、VoIP、设备互呼共用的编码音频文件（环境变量 `UP_AUDIO_FILE`） |
| `--up-audio-format` | `alaw_8khz` | 上述文件格式（环境变量 `UP_AUDIO_FORMAT`） |
| `--down-audio-format` | `alaw_8khz` | 下行协商格式；接收数据仍会丢弃（环境变量 `DOWN_AUDIO_FORMAT`） |
| `--up-video-file` | `../assets/video.h264` | 推流、VoIP、设备互呼共用的编码视频文件；空路径表示纯音频（环境变量 `UP_VIDEO_FILE`） |
| `--up-video-format` | `h264` | 上述文件格式（环境变量 `UP_VIDEO_FORMAT`） |
| `--down-video-format` | `h264` | 下行协商格式；接收数据仍会丢弃（环境变量 `DOWN_VIDEO_FORMAT`） |
| `--ai-audio-file` | `../assets/ai.pcm` | AI 请求上行音频文件（环境变量 `AI_AUDIO_FILE`） |
| `--ai-up-audio-format` | `pcm_s16le_16khz` | AI 请求音频格式（环境变量 `AI_UP_AUDIO_FORMAT`） |
| `VOIP_SCREEN_WIDTH`（环境变量） | `1280` | 设备自身屏幕宽度（像素），与上行视频素材分辨率无关 |
| `VOIP_SCREEN_HEIGHT`（环境变量） | `720` | 设备自身屏幕高度（像素），与上行视频素材分辨率无关 |
| `VOIP_CAMERA_ROTATION`（环境变量） | `0` | 微信 VoIP 通话 UI 顺时针旋转角度，仅支持 `0/90/180/270`，随 device profile 上报 |
| `VOIP_ASPECT_RATIO`（环境变量） | `1.3333333333` | 微信 VoIP 视频宽高比，必须大于 `0` |
| `VOIP_OBJECT_FIT`（环境变量） | 空 | 微信 VoIP 设备视频缩放方式：`fill/contain`；为空时不上传，使用微信默认值 |
| `VOIP_HOR_MIRROR`（环境变量） | `false` | 是否水平镜像微信 VoIP 视频 |
| `VOIP_VERT_MIRROR`（环境变量） | `false` | 是否垂直镜像微信 VoIP 视频 |
| `--ca-cert` | `../assets/ca-certificates.crt` | MQTT 与 HTTPS 共用的 TLS CA 证书（环境变量 `MQTT_CA_CERT`） |
| `--insecure` | 关闭 | 禁用 MQTT/HTTPS 证书校验，仅用于隔离测试环境（环境变量 `TIRTC_INSECURE=1`） |

### 统一终端命令

- `wxcall [N] [video|audio]`：列出或呼叫微信联系人；不带编号时先列出，再直接输入编号。
- `call [N|device_id] [video|audio]`：列出或呼叫统一联系人。联系人条目是 `voip` 类型时自动走微信 VoIP，否则走设备 P2P。
- `aicall`、`accept`、`reject [reason]`、`cancel`、`hangup`：发起 AI 对话或处理当前会话。
- `ct list|pending|add|accept|reject|del|remark`：联系人查询和维护。
- `room`、`help`、`exit`；常用缩写为 `w/a/r/h/e`。

`call` 和 `wxcall` 未指定通话类型时，有上行视频素材则使用 `video`，未配置上行视频素材则使用 `audio`。显式指定 `video` 时必须已经配置上行视频素材。

音频呼叫只读取并发送上行音频文件；视频呼叫才会同时读取上行视频文件。

### 会话冲突与竞态规则

`SessionArbiter` 是 MQTT、终端和 SDK 回调进入 RTC 生命周期前的唯一仲裁点，C 与 Python 模拟器使用相同规则：

- H5 实时流是空闲基线，不占业务会话名额；仅有待接来电时继续推流。
- 全局只有一个待接槽位，VoIP/设备来电按到达仲裁器的先后顺序 first-wins；后来的来电直接回复 busy，不会覆盖第一通来电。
- 待接槽位绑定 `room_id` 和票据代次，并有 45 秒 TTL；迟到取消只能取消同一房间，不能清掉后来到达的新来电。
- VoIP 外呼、AI 对讲、设备外呼以及接听来电都必须先取得唯一 RTC 所有权；所有权存在或已有待接来电时，其他业务不能启动，不做跨业务抢占。
- 当前 VoIP 外呼回铃与真正的新来电由仲裁器原子分类，避免“检查后状态改变”的竞态。
- 接听从 `PENDING → STARTING → ACTIVE` 提交；获取 token 或连接期间收到同房间取消时不会继续连接，也不会复活旧来电。
- 失败、拒接、取消、超时、远端挂断统一归还所有权并恢复 H5；异步结束携带会话代次，旧代次事件不能结束新会话。生命周期结束使用常驻队列，H5 恢复失败会限次重试。
- 设备忙线拒接 HTTP 由后台队列执行，不阻塞 MQTT 网络回调。

后续新增需要独占 TiRTC 的业务时，只需增加 `SessionKind`、生命周期适配器并统一经过 `SessionArbiter`，不要在业务模块间互相读取状态拼接冲突判断。

### 当前 C 参考实现的媒体参数范围

当前仓库的默认 C 文件素材是 G.711 A-law 8 kHz 和 H.264。文件读取器支持以下已经编码好的格式，不负责转码：

- 音频：A-law 8/16 kHz、AMR-NB/WB、Ogg Opus 8/16 kHz、PCM S16LE 8/16 kHz、AAC ADTS 8/16 kHz。
- 视频：H.264/H.265 Annex-B、MJPEG。

启动时会按声明的格式完整校验文件并分帧，声明和内容不匹配会直接退出。下行格式只参与协商和日志；回调不保存、不解码、不播放收到的数据。

## 日志

所有运行日志采用统一格式：`HH:MM:SS.mmm LEVEL [module] message`。`DEBUG` 和 `INFO` 输出到标准输出，`WARN` 和 `ERROR` 输出到标准错误；终端操作提示和验证码属于用户交互信息，不受日志级别过滤。日志默认不输出 ANSI 颜色，便于串口采集、重定向和机器解析。

| `--log-level` | 输出内容 |
|---|---|
| `debug`（默认） | `DEBUG`、`INFO`、`WARN`、`ERROR`，包含 TiRTC SDK 诊断日志 |
| `info` | `INFO`、`WARN`、`ERROR` |
| `warn` | `WARN`、`ERROR` |
| `error` | 仅 `ERROR` |

日志实现集中在 `src/common.c`，并提供 `log_set_sink()`。移植到板端时，可在 `main()` 初始化后将 sink 换成 UART、syslog、RTT 或平台日志服务；业务模块无需修改，也不应直接调用 `printf` 输出运行日志。

## 模块速查

```
src/
├── main.c                 # 入口：命令行、统一 MQTT 与终端路由
├── session_arbiter.h/c    # 竞态策略：待接槽、独占所有权、代次隔离
├── session_coordinator.h/c # 单 SDK 会话切换：通话结束恢复推流
├── common.h               # 公共定义
├── file_media_source.h/c  # 多格式编码媒体文件分帧器
├── media_rx_log.h/c       # 下行音视频限频日志与丢弃
├── sdk_callback_guard.h/c # SDK 回调生命周期屏障与延后动作
├── http_tls.h/c           # libcurl TLS 校验配置
├── device_flow.h/c        # 设备上线协议（HTTP + MQTT + HMAC 签名）
├── tirtc_voip.h/c         # VoIP 模块（WHIP 连接、文件媒体上行、拒接信令）
├── tirtc_ai.h/c           # AI 对话模块
├── tirtc_stream.h/c       # 推流模块
├── tirtc_call.h/c         # 设备间 P2P 通话模块
└── call_session.h/c       # 通话状态机
```

### device_flow — 设备上线协议

```c
#include "device_flow.h"

// 1. 服务发现
DeviceServices svc;
fetch_services(&svc, NULL);  // 或传入 base_url
// svc: { device_server, voip_server, ai_server, call_server,
//        mqtt_host, mqtt_port, mqtt_tls, tirtc_endpoint }

// 2. 未绑定：上报指纹 → 临时 MQTT → 等 auth_grant
ReportResult rep;
report_device(svc.device_server, "AA:BB:CC:DD:EE:FF", NULL, NULL, &rep);
char did[64] = "", dkey[256] = "";
connect_temp_mqtt(svc.mqtt_host, svc.mqtt_port,
                  rep.temp_client_id, rep.temp_token,
                  190, svc.mqtt_tls, did, sizeof(did), dkey, sizeof(dkey));

// 3. 已绑定：HMAC 签名换取 token
char mqtt_token[512];
int ret = get_mqtt_token(svc.device_server, did, dkey, mac,
                         mqtt_token, sizeof(mqtt_token));
// ret==0 成功，ret==-2 设备已解绑(6006)，ret==-1 其他错误

// 4. 正式 MQTT 长连接
MqttMsgHandler handler = { ... };
connect_mqtt_blocking(svc.mqtt_host, svc.mqtt_port,
                       did, mqtt_token, &handler, ctx,
                       &g_stop, svc.mqtt_tls);
```

### HMAC 签名

`POST /v1/device/token` 和 Report（解绑重绑）均需要 HMAC-SHA256 签名：

```
签名串 = device_id + timestamp + nonce
签名值 = Base64(HMAC-SHA256(device_key, 签名串))
请求头 = X-Device-Id, X-Timestamp, X-Nonce, X-Signature
```

```c
// mbedTLS 实现（ESP32 / STM32 / nRF 通用）
#include <mbedtls/md.h>
#include <mbedtls/base64.h>

char raw[256];
snprintf(raw, sizeof(raw), "%s%s%s", device_id, timestamp, nonce);

unsigned char hmac[32];
mbedtls_md_hmac(mbedtls_md_info_from_type(MBEDTLS_MD_SHA256),
                (const unsigned char *)device_key, strlen(device_key),
                (const unsigned char *)raw, strlen(raw), hmac);

size_t olen; char sig[64];
mbedtls_base64_encode((unsigned char *)sig, sizeof(sig), &olen, hmac, 32);
sig[olen] = '\0';
```

### SSL/TLS

MQTT TLS 开关由服务发现 `svc.mqtt_tls`（`mqtt-srv` scheme 决定：`mqtts://` → 1, `mqtt://` → 0）。

CA 证书文件：`../assets/ca-certificates.crt`（从 Linux 系统证书库提取），需定期刷新：

```bash
cp /etc/ssl/certs/ca-certificates.crt ../assets/ca-certificates.crt
```

只有隔离测试环境可以显式跳过 MQTT 和 HTTPS 验证：
```bash
./device-sim --insecure
```
正常运行默认校验证书链和主机名。

### TiRTC SDK 核心 API

SDK 的初始化与回调注册、WHIP/P2P 连接、媒体帧 `TIRTCFRAMEINFO`、命令通道与错误处理的完整用法，统一见 [thing-connect/README 的「TiRTC SDK 速查」](../../README.md#tirtc-sdk-速查)——那里是权威速查，本节不再重复。

> 关键约束（速查里有完整版）：`TIRTCCALLBACKS` 必须 `static`；回调在 SDK 线程触发、禁止阻塞；`TiRtcStart` 返回 0 不等于启动成功，须等 `on_event(SYS_STARTED)`；WHIP 建连后 AI 要延时 ~300ms 再发 `0x2100`。
>
> 本仓库 「C 参考实现」的凭证传递：`device_id`、`secret_key`、`client_id` 分别传给各业务模块的 `*_init_sdk()`，模块内部依次设置 `TIRTC_OPT_DEVICE_SECRET_KEY`、`TIRTC_OPT_CLIENT_ID` 后调用 `TiRtcStart(device_id, &cbs)`。不要把 `device_id` 与 `device_key` 拼成一个字符串传给 `TiRtcStart`。

### 各业务模块调用

```c
// ── VoIP ──
// 一个进程只有一个 TiRTC SDK 实例；SessionArbiter 准入后由
// session_coordinator 在进入 VoIP 时调用。
voip_init_sdk(did, dkey, client_id, svc.tirtc_endpoint);
VoipState *vs = voip_create(svc.voip_server, did, mqtt_token,
                            "../assets/audio.g711a");
voip_configure_media(vs, "../assets/audio.g711a", "alaw_8khz",
                     "../assets/video.h264", "h264");
cJSON *auth_list = NULL;
voip_report_profile(svc.voip_server, mqtt_token, &auth_list);
voip_set_auth_list(vs, auth_list);
// 来电接听：voip_start_session(vs, peer_id, token, audio_file)
// 来电拒接：voip_reject_session(app_id, model_id, token, room_id, payload, 7)
// 主叫：    voip_do_outgoing_call_ex(vs, caller, "video")
// 挂断：    voip_stop_session(vs)
voip_destroy(vs); voip_uninit_sdk();

// ── AI ──
ai_init_sdk(did, dkey, client_id, svc.tirtc_endpoint);
AiState *as = ai_create_ex(svc.ai_server, did, mqtt_token,
                           "../assets/ai.pcm",
                           "pcm_s16le_16khz", "pcm_s16le_16khz");
ai_get_token(svc.ai_server, mqtt_token, did,
             peer_id, sizeof(peer_id), token, sizeof(token),
             role_id, sizeof(role_id));
ai_start_session(as, peer_id, token, "../assets/ai.pcm", did, role_id);
ai_stop_session(as);
ai_destroy(as); ai_uninit_sdk();

// ── Call ──
call_init_sdk("DEV001", "your-key", "AA:BB:CC:DD:EE:FF", svc.tirtc_endpoint);
CallState *cs = call_create_ex(svc.call_server, did, mqtt_token,
                               "../assets/audio.g711a", "alaw_8khz",
                               "../assets/video.h264", "h264");
// 主叫：call_session_do_call(cs, "TIRZ00000002", "video")
// 被叫：accept → call_session_do_accept(cs) → TiRtcConnect → 0x2000
// 被叫：reject → call_session_do_reject(cs, "decline")
call_session_do_hangup(cs);
call_destroy(cs); call_uninit_sdk();

// ── Stream ──
stream_init_sdk_ex(did, dkey, client_id, svc.tirtc_endpoint,
                   "../assets/video.h264", "../assets/audio.g711a",
                   "alaw_8khz", "h264");
while (!g_stop) sleep_ms(100);
stream_uninit_sdk();
```

## MQTT 消息

### 主题

| 主题 | 方向 | ACK | 说明 |
|------|------|-----|------|
| `device/{temp_client_id}/cmd` | 下行 | 必须 | 临时连接，`auth_grant` |
| `device/sn_{device_id}/cmd` | 下行 | 必须 | 正式连接，来电/解绑 |
| `device/sn_{device_id}/notify` | 下行 | 不需要 | 通知（call_cancel 等） |
| `device/sn_{device_id}/up` | 上行 | — | 心跳（30s） |
| `device/sn_{device_id}/ack` | 上行 | — | `{"ack": true}` |

### 关键消息

**auth_grant** — `device/{temp_client_id}/cmd`：
```json
{"type":"auth_grant","payload":{"device_id":"DEV001","device_key":"..."}}
```
空 payload → 预烧设备被解绑，用本地凭证。

**call_incoming (VoIP)** — `device/sn_{id}/cmd`，`channel:"wx"`：来电 payload
携带 `wx_user_remark` 联系人备注；设备显示备注后回 ACK → `TiRtcWhipConnect(peer_id, token)`。

**call_incoming (设备间)** — `device/sn_{id}/cmd`，`channel:"device"`：
```json
{"type":"call_incoming","channel":"device","payload":{"room_id":"...","caller_id":"...","call_type":"video"}}
```

## AI 信令（0x2100，JSON-RPC 2.0）

| method | 方向 | 说明 |
|--------|------|------|
| `start_session` | 设备→平台 | Request，含 `role_id`、`input_audio`、`output_audio` |
| `start_session` 响应 | 平台→设备 | Response，含 `session_id` |
| `caption` | 平台→设备 | Notification，字幕/ASR |
| `round_start` / `round_end` | 平台→设备 | 对话轮次边界 |
| `end_session` | 双向 | 结束会话 |

```json
{"jsonrpc":"2.0","id":"uuid","method":"start_session",
 "params":{"device_id":"DEV001","role_id":"...","input_audio":{"sample_rate":16000,"channels":1},"output_audio":{"sample_rate":16000,"channels":1}}}
```

WHIP 连接后等 ~300ms KCP 握手再发送。

## 文件媒体节奏

固定码率的 PCM/A-law 按采样率和包间隔切包；AMR、AAC ADTS、Ogg Opus 按各自容器帧时长推进时间戳。H.264/H.265 Annex-B 按 Access Unit 分帧，MJPEG 按 JPEG SOI/EOI 分帧；视频默认按 15 fps 发送。

| 场景 | 默认格式 | 默认包间隔 | stream_id |
|------|----------|------------|-----------|
| VoIP / 设备通话 / 实时流音频 | A-law 8 kHz | 40 ms | 10 |
| AI 上行 | PCM S16LE 16 kHz | 20 ms | 1 |
| 视频 | H.264 Annex-B | 约 66.7 ms | 11 |

H.264 必须重新编码（`-c copy` 不可用）：
```bash
ffmpeg -i input.mp4 -c:v libx264 -r 15 -bf 0 -g 30 -bsf:v h264_mp4toannexb -an output.h264
```

## 错误码

### HTTP 业务码

| 码 | 含义 | 动作 |
|----|------|------|
| 429 | 请求过频 | 等 Retry-After 秒 |
| 40901 | 上一验证码仍有效 | 等 Retry-After 秒 |
| 6006 | 设备已解绑 | 带 device_id 走 Report（`get_mqtt_token` 返回 -2） |
| 6008 | HMAC 签名失败 | 检查 device_key、时间同步 |
| 6009 | 设备 ID 被其他 MAC 占用 | 联系运维 |
| 6010 | 指纹全空 | 至少填 mac |
| 6014 | 设备 ID 不可信 | Report 时带上签名 Header |

### MQTT 断连

| 原因码 | 含义 | 动作 |
|--------|------|------|
| `152`/`153` | 认证被拒 | 重新调 Token |
| `0x98` | JWT 过期 | 重新调 Token 重连 |

### TiRTC SDK

| 错误码 | 说明 |
|--------|------|
| `-40002` | `TIRTC_E_INVALID_HANDLE` — 连接未就绪，短暂重试 |
| `-40012` | `TIRTC_E_SERVER_ERROR` — 检查 peer_id/token |

## 移植指南

> 完整移植流程（目标平台选择、Linux 交叉编译、RTOS 五模块重建、板端验收清单）见 [device-porting.md](../../device-porting.md)。本节只做 Demo 侧的速查与 ESP32/mbedTLS 补充。

### 模块替换

| 模块 | Linux 仿真 | 嵌入式 |
|------|-----------|--------|
| HTTP | libcurl | `esp_http_client` / lwIP |
| MQTT | libmosquitto | Paho MQTT C / `esp_mqtt` |
| JSON | cJSON | 直接复制 `cjson.c`（零依赖） |
| HMAC | mbedTLS | mbedTLS（ESP32/STM32/nRF 内置） |
| 线程 | pthread | `xTaskCreate` |
| 随机数 | `/dev/urandom` | `esp_random()` / 硬件 RNG |

### ESP32（ESP-IDF）接入边界

不要把本目录的 device_flow.c、tirtc_voip.c 直接写进 ESP-IDF 的 SRCS：它们包含 curl、mosquitto 和 pthread。应新建 device_http.c、device_mqtt.c、device_media.c 等 ESP-IDF 适配模块，再按 [移植章节](../../device-porting.md#路径-brtos产品固件移植) 复用协议字段和 TiRTC 调用顺序。

适配模块的 component 注册可从下面骨架开始：

```cmake
idf_component_register(
    SRCS "device_identity.c" "device_http.c" "device_mqtt.c"
         "device_media.c" "device_session.c" "device_tirtc.c"
    REQUIRES mbedtls esp_http_client mqtt esp_timer
)
```

`sdkconfig` 开启：`CONFIG_MBEDTLS_MD_ENABLED=y`, `CONFIG_MBEDTLS_SHA256_ENABLED=y`, `CONFIG_MBEDTLS_BASE64_ENABLED=y`

### mbedTLS 版本与链接

Linux 「C 参考实现」的 libTiRTC.so 内嵌 mbedTLS。不要额外链接系统 libmbedcrypto：

```makefile
LDFLAGS += -lTiRTC
# 不加 -lssl / -lcrypto / -lmbedcrypto
```

## 常见坑

1. **`TIRTCCALLBACKS` 必须 `static`**：SDK 只存指针，局部变量函数返回后失效
2. **回调内不能阻塞或反向调用断开/反初始化**：当前实现用回调屏障跟踪所有回调，并把断开、线程启动和会话恢复延后到回调栈之外
3. **`on_conn_accepted` 后推流返回 -40002**：ICE/DTLS 握手未完成，短暂重试
4. **VoIP `peer_id` 缓冲区 ≥ 1024 字节**：URL 参数长度不固定，截断导致 -40012
5. **AI WHIP 后等 ~300ms**：KCP 握手完成前发命令会丢失
6. **AI 文件发完即停止上行**：连接保持到服务端返回 `end_session`
7. **H.264/H.265 文件必须是 Annex-B**：MP4 中的长度前缀 NAL 不能直接作为输入
8. **下行媒体没有文件输出开关**：接收回调固定为限频日志后丢弃
9. **默认命令从 `device-sim-c/` 目录运行**：媒体和 CA 默认位于 `../assets/`；部署到板端时用 `--up-*-file`、`--ca-cert` 传入实际路径
10. **`ca-certificates.crt` 有过期时间**：最早 2026-11-28，发版前刷新
