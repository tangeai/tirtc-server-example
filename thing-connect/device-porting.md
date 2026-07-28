# 从 C 参考实现移植到嵌入式设备

本章不是“把 Linux 上的 「C 参考实现」编译到板子上”就结束。它提供设备端协议和 TiRTC 调用的**可运行样板**：嵌入式开发者应保留其上线、MQTT、会话和 RTC 调用顺序，把 Linux 依赖及文件媒体替换为自己平台的网络、存储、任务和采集播放实现。

> 「C 参考实现」位于 [device-sim/device-sim-c](device-sim/device-sim-c)。它直接使用 libcurl、libmosquitto 和 pthread，因此**不能原样移植到 FreeRTOS、RT-Thread、Zephyr 或裸机**。Linux 设备可以先交叉编译运行它验证 SDK；RTOS 产品应按本章的模块边界重建平台适配层。

**文档导航：** [返回总览](README.md) | [设备上线与 MQTT](device-integration.md) | [统一竞态仲裁](device-session-arbiter.md) | [H5 实时](device-h5-live.md) | [AI](device-ai.md) | [VoIP](device-voip.md) | [设备互呼](device-call.md)

## 目标与交付物

完成移植后，设备固件至少要具备：

1. 持久化 device_id 与 device_key。
2. 通过 HTTPS 完成 Report / Token，并用 MQTT 建立正式长连接。
3. 通过 TiRtcStart 常驻接收 H5 实时连接。
4. 将摄像头、麦克风和扬声器接入 TiRTC 帧收发。
5. 收到 MQTT 来电后，按需进入 VoIP 或设备互呼；按键/业务触发 AI。
6. 在 VoIP、AI、设备互呼结束后恢复 H5 实时推流。

## 先确认目标平台

移植开始前先向平台方获取**与设备 CPU、系统、libc 和工具链匹配**的 TiRTC C SDK。SDK 不匹配时，后续代码移植无意义。

| 目标 | 正确做法 | 不应做的事 |
|---|---|---|
| Linux ARM / Linux MIPS 等 | 获取目标架构的 SDK，使用目标交叉工具链和 sysroot 编译 「C 参考实现」 | 用仓库的 linux-x86_64 动态库复制到 ARM 板子 |
| RTOS（ESP-IDF、RT-Thread、Zephyr 等） | 获取该平台可用的 SDK；将协议和媒体适配为任务/SDK API | 链接 libcurl、libmosquitto、pthread 的 Linux Demo |
| 裸机 | 先确认 SDK 和 TLS/MQTT 所需运行时是否支持该平台 | 假设 C 源码天然可在无 OS 环境运行 |

## 识别哪些代码保留、哪些必须替换

| 「C 参考实现」文件/能力 | 在产品中保留 | 必须替换为板端实现 |
|---|---|---|
| device_flow.c | 请求顺序、HMAC 签名串、HTTP/MQTT 消息格式、Topic、ACK 规则 | libcurl、libmosquitto、文件 CA 证书、pthread 心跳 |
| tirtc_stream.c | TiRtcInit / SetOption / Start，TIRTCFRAMEINFO 字段，连接回调逻辑 | H.264/G.711A 文件读取器、pthread 推流任务 |
| tirtc_ai.c | 获取 token、WHIP、0x2100 JSON-RPC、300ms 延迟、会话状态 | curl、PCM 文件读取、pthread、下行日志/丢弃 |
| tirtc_voip.c | profile、MQTT 来电字段、WHIP、0x2001 挂断 | curl、G.711A/H.264 文件读取、扬声器适配 |
| tirtc_call.c / call_session.c | 建房、接听、TiRtcConnect、0x2000 接通确认、房间状态 | curl、pthread、文件媒体 |
| session_arbiter.c / session_coordinator.c | pending ticket、generation lease、deadline、STREAM / VOIP / AI / CALL 的独占与恢复规则 | pthread mutex/condition，替换为单 session task、RTOS mutex/固定队列/事件组；详见 [竞态仲裁参考](device-session-arbiter.md) |
| main.c / Makefile | 仅作 Linux 演示入口和编译参考 | CLI、stdin 命令、getopt、Makefile 中的宿主依赖 |

## 路径 A：Linux 嵌入式设备交叉编译验证

此路径的目的只是先证明**目标 Linux 的 SDK、网络、TLS、MQTT 和 RTC 能工作**。媒体仍然可以读文件；这不是产品媒体接入。

### 1. 准备目标 SDK 与 sysroot

将 SDK 放在与目标相符的位置，例如：

~~~text
thing-connect/device-sim/sdk/
└── linux-aarch64/
    └── 2.2.1/
        ├── include/tirtc/tiRTC.h
        └── lib/libTiRTC.so
~~~

sysroot 中必须有目标架构的 curl、mosquitto、cJSON 头文件和库。不能混用宿主机 x86_64 的 pkg-config 结果。

### 2. 交叉编译

~~~bash
cd thing-connect/device-sim/device-sim-c
export PKG_CONFIG_SYSROOT_DIR=/opt/aarch64-sysroot
export PKG_CONFIG_LIBDIR=/opt/aarch64-sysroot/usr/lib/aarch64-linux-gnu/pkgconfig

make clean
make \
  CC=aarch64-linux-gnu-gcc \
  SDK_PLATFORM=linux-aarch64 \
  SDK_VERSION=2.2.1 \
  MBEDTLS_SDK_VERSION=0.1.6
~~~

如果目标 SDK 没有与 0.1.6 兼容的 mbedTLS 头文件，不能通过修改版本号强行编译；应让平台提供兼容 SDK，或把 device_flow.c 的 HMAC/Base64 改为目标平台 mbedTLS 实现。

> `MBEDTLS_SDK_VERSION=0.1.6` 取自 `Makefile:15` 的默认值，对应 `sdk/<平台>/0.1.6/include/mbedtls/` 提供的头文件。libTiRTC 已内嵌 mbedTLS，**不要额外链接** `libmbedcrypto`/`libssl`/`libcrypto`，否则符号冲突（见 [device-sim-c/README.md](device-sim/device-sim-c/README.md)）。仓库的 `windows-x86_64` SDK 不含 mbedtls 目录，Windows 不能套用本 Makefile。

### 3. 部署并验证

将以下文件复制到板端同一发布目录：device-sim、目标架构 libTiRTC.so、CA 证书、video.h264、audio.g711a。设置运行时库路径后启动：

~~~bash
export LD_LIBRARY_PATH=/opt/tirtc/lib
./device-sim \
  --device-id "$DEVICE_ID" \
  --device-key "$DEVICE_KEY" \
  --ca-cert /etc/ssl/certs/ca-certificates.crt \
  --up-audio-file /opt/tirtc/media/audio.g711a \
  --up-video-file /opt/tirtc/media/video.h264
~~~

验证 H5 能看到画面、听到声音，说明目标 Linux 环境可以进入下一步。此时不要宣称产品已完成；摄像头、麦克风、扬声器和掉电恢复仍未接入。

## 路径 B：RTOS/产品固件移植

> ⚠️ **先拿到目标平台的 TiRTC SDK。** 仓库只附带 `linux-x86_64` / `macos-arm64` / `windows-x86_64` 的预编译库，**不能**复制到 ESP32 或其它 RTOS。TiRTC 头文件 `basedef.h` 内部已为 `__ESP32S3__` / `__ESP32P4__` / `__FREERTOS__` / `__EC71X__` 等平台预留类型分支，但对应的预编译库需向平台方获取（见 [TiRTC SDK 下载](https://docs.tange.ai/products/tirtc/download.html)）。拿到 SDK 前，下面的代码移植无意义。ESP-IDF 的 component 骨架与 sdkconfig 项见 [device-sim-c/README.md](device-sim/device-sim-c/README.md)。

### 1. 创建五个板端模块

不要复制 main.c 后到处加条件编译。按下面接口拆分，业务模块不直接依赖具体 RTOS：

~~~text
app/
├── device_identity.c   # Flash/NVS：device_id、device_key
├── device_http.c       # HTTPS：Report、Token、AI/VoIP/Call HTTP
├── device_mqtt.c       # MQTT TLS、订阅、ACK、心跳、重连
├── device_media.c      # 摄像头/麦克风/编码器/扬声器/环形缓冲
├── device_session.c    # STREAM/VOIP/AI/CALL 资源仲裁
└── device_tirtc.c      # TiRTC 初始化、回调、帧收发
~~~

device_identity、device_http、device_mqtt 的字段和状态转移以 [device-integration.md](device-integration.md) 的 C 调用顺序为准。不要将 device_key 下发给前端或写入日志。

各模块到 ESP-IDF API 的映射（TiRTC C 调用本身不变，只是替换它周围的网络/存储/任务/媒体实现）：

| 板端模块 | 「C 参考实现」源文件 | ESP-IDF 替换 API | 替换要点 |
|---|---|---|---|
| device_identity | device_flow.c（凭证读写） | NVS：`nvs_open` + `nvs_get_str`/`nvs_set_str` | key 名 `device_id`/`device_key` 不变（同 `device_creds.json`）；预烧设备直接读 NVS 跳过验证码 |
| device_http | device_flow.c（Report/Token）、tirtc_ai.c、tirtc_voip.c、call_session.c | `esp_http_client` + mbedtls HMAC | Report 4 签名 header / Token 5 签名 header，签名串 `device_id+timestamp+nonce` |
| device_mqtt | device_flow.c（临时/正式连接、ack、心跳） | `esp_mqtt_client`（`MQTT_OVER_SSL`） | `.cert_pem` 嵌入 PEM；`/cmd` 必须 ack；30s 心跳 |
| device_media | tirtc_stream.c（H264FileSource）、各 tirtc_*.c 下行日志/丢弃 | `esp_camera` + I2S/codec + 环形缓冲 | 上行替换为真实采集/编码；下行替换为扬声器缓冲/显示队列（Linux C 示例只记录元数据后丢弃） |
| device_session | session_arbiter.c / session_coordinator.c（pthread mutex/cond） | FreeRTOS `xSemaphoreCreateMutex` / 固定队列 / event group | Arbiter 负责准入、pending 与 generation；Coordinator 只负责 STREAM/VOIP/AI/CALL 的 SDK 切换 |
| device_tirtc | 各 tirtc_*.c | TiRTC C API（不变） | 仅日志 sink、时间源、随机源适配（见下表） |

「C 参考实现」在 `common.h` 里用的几个 POSIX 运行时工具，也要按下表替换（`now_ms`/`sleep_ms`/`rand_hex` 在各业务模块里被频繁调用）：

| 「C 参考实现」（common.h） | 作用 | ESP-IDF / FreeRTOS 替换 |
|---|---|---|
| `now_ms()`（`clock_gettime CLOCK_MONOTONIC`） | 毫秒时间戳 | `(uint64_t)xTaskGetTickCount() * portTICK_PERIOD_MS` |
| `sleep_ms(ms)`（`nanosleep`） | 毫秒延时 | `vTaskDelay(pdMS_TO_TICKS(ms))` |
| `rand_hex`（`/dev/urandom`，有 fallback） | 生成 nonce | `esp_fill_random` 再转 hex |
| pthread mutex/cond（session_arbiter / session_coordinator） | 准入状态、生命周期队列和 SDK 切换串行化 | `xSemaphoreCreateMutex` / 固定 queue / event group |
| `log_set_sink`（`common.h:34`） | 日志重定向 | 接 UART/RTT 的 sink |

### 2. 先完成设备上线，再接媒体

先在不打开摄像头的情况下完成：Flash 读取凭证 → Report/Token → MQTT 正式连接 → 收到 cmd 并 ACK。正式 MQTT 运行后才初始化实时流和业务路由。

需要保留的调用关系如下：

~~~c
/* 具体 HTTP/MQTT 函数由 ESP-IDF、Paho、lwIP 等实现。 */
if (load_credentials(&device_id, &device_key) == 0) {
    rc = request_mqtt_token(device_id, device_key, mac, &mqtt_token);
}
if (rc == DEVICE_UNBOUND) {
    report = report_device_signed_if_possible(mac, device_id, device_key);
    wait_auth_grant_and_persist(report.temp_client_id, report.temp_token);
    mqtt_token = request_mqtt_token(device_id, device_key, mac);
}
mqtt_connect("sn_{device_id}", device_id, mqtt_token);
mqtt_subscribe("device/sn_{device_id}/cmd", 1);
mqtt_subscribe("device/sn_{device_id}/notify", 1);
~~~

下面给出 ESP-IDF 上的三段最小实现。协议字段、签名串、MQTT 参数一律以 「C 参考实现」的 `device_flow.c` 为准，这里只把 IO/网络层换成 ESP-IDF API。

#### 凭证读写（NVS）

key 名与 `device_creds.json` 一致；预烧设备直接命中、跳过验证码：

~~~c
#include "nvs.h"

int load_credentials(char *did, size_t did_n, char *dkey, size_t dkey_n) {
    nvs_handle_t h;
    if (nvs_open("tirtc", NVS_READONLY, &h) != ESP_OK) return -1;     /* 未绑定 */
    esp_err_t e = nvs_get_str(h, "device_id", did, &did_n);
    if (e == ESP_OK) e = nvs_get_str(h, "device_key", dkey, &dkey_n);
    nvs_close(h);
    return (e == ESP_OK) ? 0 : -1;
}

void save_credentials(const char *did, const char *dkey) {            /* 收到 auth_grant 后持久化 */
    nvs_handle_t h;
    if (nvs_open("tirtc", NVS_READWRITE, &h) != ESP_OK) return;
    nvs_set_str(h, "device_id", did);
    nvs_set_str(h, "device_key", dkey);
    nvs_commit(h);
    nvs_close(h);
}
~~~

#### HMAC + Report/Token（esp_http_client + mbedtls）

签名串与算法与 `device_flow.c:168-181` 完全一致：

~~~c
#include "mbedtls/md.h"
#include "mbedtls/base64.h"
#include "esp_http_client.h"
#include "esp_system.h"

static void hmac_sha256_b64(const char *key, const char *data, char *out, size_t out_n) {
    unsigned char digest[32];
    mbedtls_md_hmac(mbedtls_md_info_from_type(MBEDTLS_MD_SHA256),
                    (const unsigned char *)key, strlen(key),
                    (const unsigned char *)data, strlen(data), digest);
    size_t olen = 0;
    mbedtls_base64_encode((unsigned char *)out, out_n, &olen, digest, sizeof digest);
    out[olen] = '\0';
}

/* POST /v1/device/token：空 body、5 个签名 header（Report 只发 4 个、且 body 是 {"mac":"..."}） */
static int request_mqtt_token(const char *server, const char *device_id,
                              const char *device_key, const char *mac,
                              char *token_out, size_t token_n) {
    char ts[16], nonce_raw[8], nonce_hex[17], raw[256], sig[64];
    snprintf(ts, sizeof ts, "%ld", (long)time(NULL));       /* 需先 SNTP 对时 */
    esp_fill_random(nonce_raw, 8);                          /* 对应 device_flow.c 的 rand_hex */
    bytes_to_hex(nonce_raw, nonce_hex, sizeof nonce_raw);   /* → 16 hex 字符 */
    snprintf(raw, sizeof raw, "%s%s%s", device_id, ts, nonce_hex);   /* 签名串无分隔 */
    hmac_sha256_b64(device_key, raw, sig, sizeof sig);

    char url[256]; snprintf(url, sizeof url, "%s/v1/device/token", server);
    esp_http_client_config_t cfg = {
        .url = url, .method = HTTP_METHOD_POST,
        .cert_pem = server_ca_pem,            /* 嵌入 PEM，打开校验（demo 关了校验，产品必须开） */
    };
    esp_http_client_handle_t c = esp_http_client_init(&cfg);
    esp_http_client_set_header(c, "X-Device-Id", device_id);
    esp_http_client_set_header(c, "X-Timestamp", ts);
    esp_http_client_set_header(c, "X-Nonce", nonce_hex);
    esp_http_client_set_header(c, "X-Mac", mac);            /* Report 无此 header */
    esp_http_client_set_header(c, "X-Signature", sig);
    esp_http_client_set_post_field(c, "", 0);               /* 空 body */

    int rc = -1;
    if (esp_http_client_perform(c) == ESP_OK)
        rc = parse_token_from_response(c, token_out, token_n);   /* 6006=已解绑，需带签名重 report */
    esp_http_client_cleanup(c);
    return rc;
}
~~~

#### 正式 MQTT（esp_mqtt_client）

ClientID / Username / Password 与 `device_flow.c:796-866` 一致：

~~~c
#include "esp_mqtt.h"
#include "esp_timer.h"

static char device_id[64];   /* 上线后填入，供 ack/心跳 topic 拼接 */

static void mqtt_event_cb(void *arg, esp_event_base_t base, int32_t id, void *data) {
    esp_mqtt_event_handle_t e = data;
    if (id == MQTT_EVENT_DATA && topic_is_cmd(e->topic, e->topic_len)) {
        char ack[80]; snprintf(ack, sizeof ack, "device/sn_%s/ack", device_id);
        esp_mqtt_client_publish(e->client, ack, "{\"ack\":true}", 11, 1, 0);  /* /cmd 必须 ack */
        dispatch_cmd(e->data, e->data_len);   /* 按 type/channel 分发到 VoIP/Call 模块 */
    }
}

void start_formal_mqtt(const char *uri, const char *mqtt_token) {
    char client_id[80]; snprintf(client_id, sizeof client_id, "sn_%s", device_id);
    esp_mqtt_client_config_t mcfg = {
        .uri = uri,                          /* mqtts://...:8883 → MQTT_OVER_SSL */
        .client_id = client_id,
        .username = device_id,               /* 不带 sn_：EMQX 用它比对 token 里的 device_id */
        .password = mqtt_token,
        .cert_pem = broker_ca_pem,           /* 嵌入 PEM */
        .keepalive = 60,
    };
    esp_mqtt_client_handle_t mq = esp_mqtt_client_init(&mcfg);
    esp_mqtt_client_register_event(mq, ESP_MQTT_EVENT_ANY, mqtt_event_cb, NULL);
    esp_mqtt_client_start(mq);
    /* MQTT_EVENT_CONNECTED 后再 subscribe cmd/notify，并用 esp_timer 起 30s 心跳
       （publish device/sn_{id}/up = {"type":"heartbeat","seq":N,"ts":T}，见 device_flow.c:771-794） */
}
~~~

> ⚠️ **三个易错点（均来自 device_flow.c）：** ① `temp_client_id` 来自 Report 响应的**服务端下发**，**不能**用 MAC 本地派生（device_flow.c:375）；② CA 证书用嵌入 PEM（`.cert_pem`），ESP32 没有文件路径；③ demo 的 HTTP 关了 TLS 校验，产品必须用 `cert_pem` 打开校验。sdkconfig 至少开 `CONFIG_MBEDTLS_MD_ENABLED` / `CONFIG_MBEDTLS_SHA256_ENABLED` / `CONFIG_MBEDTLS_BASE64_ENABLED`。

### 3. 用真实媒体替换文件读写

H5 实时流的媒体契约固定如下：

| 方向 | stream_id | 固件要做什么 |
|---|---:|---|
| 设备到 H5 音频 | 10 | 麦克风采样 → G.711A 编码 → 发送 |
| 设备到 H5 视频 | 11 | 摄像头 → H.264 Annex-B 编码器 → 发送 |
| H5 到设备 talkback | 14 | 接收 G.711A → 解码 → 扬声器播放 |

采集和播放必须在设备任务中运行，不能阻塞 TiRTC 回调。下面代码是产品适配层的最小发送和接收方式：

~~~c
void media_send_h5_audio(tirtc_conn_t hconn, const uint8_t *alaw,
                         uint32_t bytes, uint32_t pts_ms) {
    TIRTCFRAMEINFO fi = {0};
    fi.stream_id = 10;
    fi.media = TIRTC_AUDIO_ALAW;
    fi.flags = TIRTC_AUDIOSAMPLE_8K16B1C;
    fi.ts = pts_ms;
    fi.length = bytes;
    TiRtcSendAudioStream(hconn, &fi, (void *)alaw);
}

void media_send_h5_video(tirtc_conn_t hconn, const uint8_t *annexb_au,
                         uint32_t bytes, uint32_t pts_ms, int is_idr) {
    TIRTCFRAMEINFO fi = {0};
    fi.stream_id = 11;
    fi.media = TIRTC_VIDEO_H264;
    fi.flags = is_idr ? TIRTC_FRAME_FLAG_KEY_FRAME : 0;
    fi.ts = pts_ms;
    fi.length = bytes;
    TiRtcSendVideoStream(hconn, &fi, (void *)annexb_au);
}

void on_audio(tirtc_conn_t hconn, const TIRTCFRAMEINFO *fi, void *data) {
    (void)hconn;
    if (fi->stream_id == 14 && fi->media == TIRTC_AUDIO_ALAW)
        speaker_queue_g711a(data, fi->length); /* 只入队，回调内不解码/播放 */
}
~~~

> ⚠️ **ESP32 媒体现实：** ESP32-CAM 普遍输出 **MJPEG**，但当前 H5 页面只接受 **H.264**（见 [device-h5-live.md](device-h5-live.md#媒体格式与默认约定)）。ESP32 没有硬件 H.264 编码器，需软件编码或外置编码芯片，否则要同时改前端和设备实现。下行音视频在 Linux 「C 参考实现」里只限频记录元数据后丢弃；产品要把回调数据复制到扬声器缓冲和显示队列——不要在 TiRTC 回调里直接解码或播放。

完整 TiRTC 初始化、全部回调和停止顺序见 [H5 实时查看与按住说话](device-h5-live.md)。

### 4. 按功能逐项接入

1. 调用 TiRtcStart 后，先完成 H5 实时流和 talkback。
2. 接入 AI：复用 HTTP token，调用 TiRtcWhipConnect，300ms 后发 0x2100 start_session。
3. 接入 VoIP：启动时上报 profile，MQTT call_incoming 后由业务任务决定接听/拒接。
4. 接入设备互呼：主叫建房；被叫取 token 后 TiRtcConnect 并发送 0x2000。
5. 用 session coordinator 保证四类会话互斥；前台会话结束后恢复 STREAM。

各能力的完整 C 方法与接口参见 [AI](device-ai.md)、[VoIP](device-voip.md)、[设备互呼](device-call.md) 与 [统一状态机](device-session-model.md)。

## 板端验收清单

- [ ] 断电重启后仍能从 Flash 读取设备凭证并重新上线。
- [ ] mqtt_token 过期或 MQTT 被拒绝后，重新请求 Token 并重连。
- [ ] H5 可看视频、听音频；按住说话可从扬声器播放。
- [ ] TiRTC 回调中不进行 HTTP、MQTT、Flash、编码、解码或阻塞等待。
- [ ] AI、VoIP、设备互呼结束后，H5 实时流会恢复。
- [ ] 设备日志不打印 device_key、mqtt_token、WHIP token。

## C 参考实现的用途

Linux 「C 参考实现」仍然有价值：它是协议字段、HTTP 请求、MQTT 路由、TiRTC API 调用和媒体帧属性的可运行对照。产品开发应从其中复制**调用顺序和错误处理**，不是复制其 libcurl/libmosquitto/pthread/文件读写实现。
