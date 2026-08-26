# ThingConnect 开发者文档

ThingConnect 提供设备接入示例，覆盖 **H5 实时预览与对讲、AI 对讲、微信 IoT VoIP、设备互呼** 四类能力。仓库中包含 Linux C 参考实现、Python 模拟器、H5 和小程序前端，以及五个业务服务和一个 Admin Server。C 和 Python 示例用于说明协议和接入流程，不能直接当作量产设备实现。

第一次使用时，建议先按[项目快速体验](../README.md)跑通设备上线、绑定和 H5 出图，再根据产品需要接入其他对讲能力。下面给出最短接入路径和 TiRTC SDK 调用骨架；完整字段、错误码和排查方法可从各节进入对应专题文档查看。

设备端使用 **TiRTC C SDK**（[SDK 头文件](device-sim/sdk/linux-x86_64/2.3.0/include/tirtc/tiRTC.h)），H5 使用 [TiRTC Web SDK](device-h5-live.md#h5-端接入)，微信小程序使用[微信 IoT VoIP](weixin-mini-program/README.md#微信-voip-开发)。设备协议顺序和会话控制可参考 [Linux C 参考实现](device-sim/device-sim-c/README.md)。

接入真实产品时，还需要完成[十项二次开发 TODO](device-porting.md#二次开发-todo)，不能只替换几个系统库。

---

## 业务流程

设备接入分三步：先完成上线和绑定，再跑通 H5 实时出图，最后按需加入其他对讲能力。完整流程如下。

> **设备启动时先检查持久化存储：**
>
> - 没有 `device_id + device_key`：调用 [`POST /v1/device/report`](api-reference.md#post-v1devicereport) 注册绑定。
> - 已有凭证：先调用 [`POST /v1/device/token`](api-reference.md#post-v1devicetoken) 获取 token。
> - token 接口返回 **HTTP 410**，且响应体业务错误码为 **`6006`**：设备未绑定或已解绑，携带 HMAC 签名重新调用 [`POST /v1/device/report`](api-reference.md#post-v1devicereport)。

```mermaid
sequenceDiagram
    autonumber
    participant D as 设备端（Linux C 文件媒体示例）
    participant DS as device-server
    participant MQ as MQTT Broker
    participant US as user-server
    participant U as 用户 (H5/小程序)
    participant RTC as TiRTC

    Note over D,U: ① 上线与绑定
    alt 路径 A：持久化存储中没有 device_id + device_key
        D->>DS: POST /v1/device/report（body: {mac}，无签名 Header）
        DS-->>D: code + temp_client_id + temp_token
        D->>MQ: 临时连接（ClientID=User=temp_client_id，Pass=temp_token）
        U->>US: 输验证码绑定
        US->>MQ: auth_grant（device_id + device_key）
        MQ-->>D: auth_grant
        D->>DS: POST /v1/device/token（HMAC 签名，Header: X-Device-Id / X-Timestamp / X-Nonce / X-Signature）
        DS-->>D: mqtt_token
    else 路径 B：持久化存储中已有 device_id + device_key
        D->>DS: POST /v1/device/token（HMAC 签名，Header 同上）
        alt token 获取成功
            DS-->>D: mqtt_token
        else HTTP 410，响应体 code=6006
            DS-->>D: 设备未绑定或已解绑
            D->>DS: POST /v1/device/report（HMAC 签名）
            DS-->>D: code + temp_client_id + temp_token
            D->>MQ: 临时连接（ClientID=User=temp_client_id，Pass=temp_token）
            U->>US: 输验证码重新绑定
            US->>MQ: auth_grant（device_id + device_key）
            MQ-->>D: auth_grant
            D->>DS: POST /v1/device/token（HMAC 签名，Header 同上）
            DS-->>D: mqtt_token
        end
    end
    D->>MQ: 正式连接（ClientID=sn_{device_id}，User=device_id，Pass=mqtt_token）

    Note over D,U: ② H5 实时出图（第一个设备端功能）
    D->>RTC: TiRtcStart（被动监听）
    U->>US: GET rtc-token
    US-->>U: token + app_id
    U->>RTC: Web SDK connect(deviceId, token)
    RTC-->>D: on_conn_accepted
    D->>RTC: 推流 stream 10/11
    RTC-->>U: 出图出声

    Note over D,U: ③ 扩展对讲（按需）
    D->>RTC: AI: TiRtcWhipConnect
    Note over U,MQ: VoIP: 小程序呼叫 → 微信回调 → MQTT call_incoming → 设备接听
    Note over D,RTC: 设备互呼: TiRtcConnect (P2P)
```

设备侧需要处理的内容如下。

**① 上线与绑定**（设备获得身份）

根据持久化存储中是否已有 `device_id + device_key` 选择上线流程。

- **路径 A：持久化存储中没有凭证。** 调用 [`POST /v1/device/report`](api-reference.md#post-v1devicereport) 获取验证码和临时 MQTT 凭证。用户绑定后，设备会收到 `device_id + device_key`。先安全持久化这两个值，再调用 [`POST /v1/device/token`](api-reference.md#post-v1devicetoken) 获取正式 MQTT token。

- **路径 B：持久化存储中已有凭证。** 先调用 [`POST /v1/device/token`](api-reference.md#post-v1devicetoken)。成功后直接使用返回的 MQTT token；如果接口返回 **HTTP 410**，且响应体业务错误码为 **`6006`**，则携带 HMAC 签名调用 [`POST /v1/device/report`](api-reference.md#post-v1devicereport)，重新完成绑定。

两条路径调用 [`POST /v1/device/token`](api-reference.md#post-v1devicetoken) 时，都需要携带 `X-Device-Id`、`X-Timestamp`、`X-Nonce` 和 `X-Signature` 四个签名请求头。Linux C 参考实现使用 **OpenSSL HMAC-SHA256 → Base64**：

```c
// 签名串 = device_id + timestamp + nonce
// 签名值 = Base64(HMAC-SHA256(device_key, 签名串))

#include <openssl/evp.h>
#include <openssl/hmac.h>

char raw[256];
int n = snprintf(raw, sizeof(raw), "%s%s%s", device_id, timestamp, nonce);
if (n < 0 || (size_t)n >= sizeof(raw)) return -1;

unsigned char hmac[EVP_MAX_MD_SIZE];
unsigned int hmac_len = 0;
if (HMAC(EVP_sha256(), device_key, (int)strlen(device_key),
         (const unsigned char *)raw, strlen(raw), hmac, &hmac_len) == NULL ||
    hmac_len != 32)
    return -1;

char sig[64];
int olen = EVP_EncodeBlock((unsigned char *)sig, hmac, (int)hmac_len);
if (olen != 44 || (size_t)olen >= sizeof(sig))
    return -1;
sig[olen] = '\0';
```

Linux C 参考实现将签名过程封装在 `hmac_sha256_b64()` 中，代码见 [device_flow.h](device-sim/device-sim-c/src/device_flow.h)。拿到 `mqtt_token` 后，使用 `ClientID=sn_{device_id}`、`User=device_id`、`Pass=mqtt_token` 建立正式连接。

**② H5 实时出图**（第一个设备端功能）

设备调用 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcstart" target="_blank" rel="noopener">`TiRtcStart`</a> 进入被动监听。H5 获取 `rtc-token` 后，通过 Web SDK 连接设备。设备收到 `on_conn_accepted` 回调后向固定 stream 推送音视频，H5 便可播放画面和声音，并支持按住说话。

**③ 扩展对讲**（第一个闭环跑通后，按需接入）

- **AI 对讲**：设备主动调用 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcwhipconnect" target="_blank" rel="noopener">`TiRtcWhipConnect`</a>。
- **微信 VoIP**：小程序与设备通话，来电通知通过 MQTT 下发。
- **设备互呼**：设备之间调用 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcconnect" target="_blank" rel="noopener">`TiRtcConnect`</a> 建立 P2P 连接。

**各方职责：**

| 角色 | 职责 |
|---|---|
| Linux C 参考实现 | 上线、凭证、MQTT、TiRTC、会话控制，以及 `DeviceAdapterV1` 二次开发边界；默认使用文件媒体和 stdin 演示 |
| 产品设备 | 平台与硬件适配、真实身份、采集编码、解码播放、视频显示、产品交互、资源仲裁、异常恢复和量产安全 |
| H5、小程序 | 登录、绑定、获取 token、发起和接听通话 |
| 服务端（五个业务服务和 Admin Server） | 身份、绑定、业务 token、MQTT 信令、微信回调、房间、配置与后台管理 |
| MQTT Broker | 设备长连接、来电与通知下发 |
| TiRTC | 实时音视频传输 |

建议先熟悉 TiRTC SDK 并跑通 H5 出图，再接入产品需要的对讲能力。

---

## 设备端

> **正式 MQTT 连接参数**
>
> - **ClientID** = `sn_{device_id}`
> - **Username** = `device_id`（不带 `sn_` 前缀，EMQX 会用它比对 token 中的 `device_id` claim）
> - **Password** = `mqtt_token`（`POST /v1/device/token` 签发的 JWT）
>
> 临时连接、token 签发与刷新、心跳、断连原因码见 [device-integration.md](device-integration.md#凭证与连接)。

### TiRTC SDK 速查

四类能力共用同一个 TiRTC C SDK。接入时先确认下面五项。

**① 进程级生命周期**

调用顺序固定，不能跳过中间状态：

| 阶段 | 操作 |
|---|---|
| 初始化前 | 调用 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsetoption" target="_blank" rel="noopener">`TiRtcSetOption(TIRTC_OPT_MAX_SEND_BUFFER)`</a> |
| 初始化 | 调用 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcinit" target="_blank" rel="noopener">`TiRtcInit`</a> |
| 配置 | 调用其余 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsetoption" target="_blank" rel="noopener">`TiRtcSetOption`</a> |
| 启动 | 调用 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcstart" target="_blank" rel="noopener">`TiRtcStart`</a>，等待 `on_event(SYS_STARTED)` |
| 运行 | 多个业务会话共用 SDK runtime 和同一张回调表 |
| 退出 | 调用 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcstop" target="_blank" rel="noopener">`TiRtcStop`</a>，等待 `on_event(SYS_STOPPED)`，最后调用 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcuninit" target="_blank" rel="noopener">`TiRtcUninit`</a> |

H5 实时、VoIP、AI 和设备互呼共用一个进程级 SDK runtime。切换业务时，只停止媒体、断开当前连接并更新会话代次，不调用 `TiRtcStop` 或 `TiRtcUninit`。连接和异步结果都要绑定会话代次，迟到的回调不能进入下一次同类会话。

`device_id` 和 `device_key` 来自[设备上线](#业务流程)；`endpoint` 使用[服务发现](api-reference.md#service-discovery)返回的 `tirtc-srv`；`client_id` 建议使用设备 MAC，并保持全局唯一且不变，否则设备将无法连接。

下面示例中的 `g_sdk_ready`、`s_active_conn` 和 `s_force_key` 是应用自己的状态变量，不是 SDK 字段。完整实现由 `tirtc_runtime` 保存 SDK 状态和连接归属。

```c
/* ── 1. 实现 SDK 回调（运行在 SDK 内部线程，避免 sleep、阻塞和耗时操作）── */

/* 系统事件：data/len 是事件附加数据。SYS_STARTED=启动完成可开始连接；SYS_STOPPED=已停止 */
static void on_event(int event, const void *data, int len) {
    (void)data; (void)len;
    if (event == TIRTC_EVENT_SYS_STARTED)      g_sdk_ready = 1;
    else if (event == TIRTC_EVENT_SYS_STOPPED) g_sdk_ready = 0;
}

/* 新连接接入（H5 直连 / 设备互呼对端连入）。
   SDK 回调只向应用自己的固定队列投递事件；控制任务再保存 hconn、启动媒体。 */
static void on_conn_accepted(tirtc_conn_t hconn) {
    app_rtc_event_push(APP_RTC_CONNECTED, hconn, 0);
}

/* 连接出错：此后该 hconn 上收发全部失效。Disconnect 由控制任务延后执行。 */
static void on_conn_error(tirtc_conn_t hconn, int error) {
    app_rtc_event_push(APP_RTC_CONN_ERROR, hconn, error);
}

/* 收到对端音频：pFi 描述本帧（stream_id / media / length / ts），data 指向 payload。
   ⚠️ data 在回调返回后即失效，需要保留须先拷贝。
   stream 14 = H5 talkback 音频；AI 下行音频也走这里 → 拷贝后交给扬声器。 */
static void on_audio(tirtc_conn_t hconn, const TIRTCFRAMEINFO *pFi, void *data) {
    /* 复制后投递；控制/媒体任务再调用 speaker_write。 */
    app_rtc_media_push(hconn, pFi, data, pFi->length);
}

/* 0x2000 / 0x2100 是平台预留业务命令；复制后由控制任务解析。 */
static void on_command(tirtc_conn_t hconn, uint32_t cmdw, const void *data, uint32_t len) {
    app_rtc_command_push(hconn, cmdw, data, len);
}

/* 对端请求在 stream_id 上立即发关键帧 → 置标志，推流线程下一帧强制 IDR（第一个功能） */
static void on_request_key_frame(tirtc_conn_t hconn, uint8_t stream_id) {
    (void)hconn; (void)stream_id;
    /* 例：s_force_key = 1; 见第一个功能 push_thread */
}

/* 未使用的回调也提供 NOP，订阅回调返回 0 表示接受。 */
static void on_disconnected_nop(tirtc_conn_t hconn) { (void)hconn; }
static void on_frame_nop(tirtc_conn_t hconn, const TIRTCFRAMEINFO *fi, void *data) {
    (void)hconn; (void)fi; (void)data;
}
static int on_subscribe_nop(tirtc_conn_t hconn, uint8_t stream_id) {
    (void)hconn; (void)stream_id; return 0;
}
static void on_unsubscribe_nop(tirtc_conn_t hconn, uint8_t stream_id) {
    (void)hconn; (void)stream_id;
}

/* ── 2. 注册回调 + 初始化 + 启动（顺序固定）── */
static TIRTCCALLBACKS cbs;                 /* 必须 static：SDK 只存指针，生命周期须覆盖整个运行期 */

static int process_rtc_start(const char *endpoint, const char *device_key,
                             const char *client_id, const char *device_id) {
    int rc;
    uint32_t max_send_buffer = 512 * 1024;

    memset(&cbs, 0, sizeof cbs);
    cbs.on_event             = on_event;
    cbs.on_conn_accepted     = on_conn_accepted;
    cbs.on_conn_error        = on_conn_error;
    cbs.on_disconnected      = on_disconnected_nop;
    cbs.on_audio             = on_audio;
    cbs.on_video             = on_frame_nop;
    cbs.on_message           = on_frame_nop;
    cbs.on_command           = on_command;
    cbs.on_request_key_frame = on_request_key_frame;
    cbs.on_subscribe_video   = on_subscribe_nop;
    cbs.on_unsubscribe_video = on_unsubscribe_nop;
    cbs.on_subscribe_audio   = on_subscribe_nop;
    cbs.on_unsubscribe_audio = on_unsubscribe_nop;

    /* 必须在 TiRtcInit 前设置。 */
    rc = TiRtcSetOption(TIRTC_OPT_MAX_SEND_BUFFER, &max_send_buffer, sizeof max_send_buffer);
    if (rc != 0) return rc;
    rc = TiRtcInit();
    if (rc != 0) return rc;

    /* endpoint 必须取 GET http://ep-open.tangeopen.com/services 的 tirtc-srv。
       空值只作防御性处理：不设置 option，也不在设备端固化替代地址。 */
    if (endpoint && endpoint[0]) {
        rc = TiRtcSetOption(TIRTC_OPT_SERVICE_ENDPOINT, endpoint, (uint32_t)strlen(endpoint));
        if (rc != 0) goto fail;
    }
    rc = TiRtcSetOption(TIRTC_OPT_DEVICE_SECRET_KEY, device_key, (uint32_t)strlen(device_key));
    if (rc != 0) goto fail;
    /* client_id：推荐设备 MAC；必须唯一且保持不变，变动后设备无法连接。 */
    rc = TiRtcSetOption(TIRTC_OPT_CLIENT_ID, client_id, (uint32_t)strlen(client_id));
    if (rc != 0) goto fail;

    rc = TiRtcStart(device_id, &cbs); /* 返回 0 仅初检通过；真正启动看 SYS_STARTED */
    if (rc != 0) goto fail;
    return 0;

fail:
    TiRtcUninit();
    return rc;
}
```

> SDK 回调运行在内部线程，回调函数应尽快返回：
>
> - 只复制回调结束后仍要使用的数据、更新受保护状态，或向应用自己的固定队列投递事件。
> - 不要在回调中 `sleep`、阻塞、创建或等待线程，也不要反向调用 `TiRtcDisconnect`、`TiRtcStop` 或 `TiRtcUninit`。
> - 断开连接、启停线程、解析命令、文件或声卡 I/O、会话恢复和延时动作，都由常驻控制任务或媒体任务在回调栈外处理。
>
> `app_rtc_event_push()`、`app_rtc_media_push()` 和 `app_rtc_command_push()` 是上例中的应用队列抽象，不属于 TiRTC API。需要延时的操作，例如 AI 建连后等待 300ms，只记录时间，再由业务主循环执行。
>
> [`TiRtcStart`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcstart) 返回 0 只表示初步检查通过。收到 `SYS_STARTED` 后，SDK 才能使用。

**② 三种连接方式**

| 能力 | SDK 调用 | 方向 |
|---|---|---|
| H5 实时 | 被动：<a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcstart" target="_blank" rel="noopener">`TiRtcStart`</a> 后等 `on_conn_accepted` | 设备不主动连 |
| AI / VoIP | <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcwhipconnect" target="_blank" rel="noopener">`TiRtcWhipConnect(peer_id, token, cb, user)`</a> | WHIP client → server |
| 设备互呼 | <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcconnect" target="_blank" rel="noopener">`TiRtcConnect(caller_id, token, cb, user)`</a> | 设备 ↔ 设备 P2P（被叫发起） |

[`TiRtcWhipConnect`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcwhipconnect) 和 [`TiRtcConnect`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcconnect) 都通过 `TIRTCCONNECTCALLBACK` 返回连接结果：`void cb(int error, tirtc_conn_t hconn, void *user_data)`。`error == 0` 时可使用 `hconn` 发送媒体或命令。

调用 [`TiRtcDisconnect`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcdisconnect) 会异步断开连接。该方法返回后，`hconn` 立即失效。

**③ 发送音视频**（`TIRTCFRAMEINFO` 帧头 + payload）

```c
/* 音频：stream_id 与视频不能重复。 */
TIRTCFRAMEINFO audio = {0};
audio.stream_id = 10;                         /* 0~15，全局唯一 */
audio.media     = TIRTC_AUDIO_ALAW;           /* PCM=1/ALAW=2/AAC=3/OPUS=4/AMR=5 */
audio.flags     = TIRTC_AUDIOSAMPLE_8K16B1C;  /* 音频采样规格 */
audio.ts        = (uint32_t)(audio_pts_ms & 0xFFFFFFFF); /* 主机序，精度 ms */
audio.length    = 320;                        /* A-law/alaw 8kHz：约 320B/40ms */
TiRtcSendAudioStream(hconn, &audio, audio_pkt);

/* 视频：同样使用 TIRTCFRAMEINFO，但媒体类型、flags 与发送接口不同。 */
TIRTCFRAMEINFO video = {0};
video.stream_id = 11;                          /* 必须与音频 stream_id 不同 */
video.media     = TIRTC_VIDEO_H264;            /* 也可为 TIRTC_VIDEO_H265 / TIRTC_VIDEO_JPEG */
video.flags     = is_key_frame ? TIRTC_FRAME_FLAG_KEY_FRAME : 0; /* bit0：关键帧 */
video.ts        = (uint32_t)(video_pts_ms & 0xFFFFFFFF); /* 主机序，精度 ms */
video.length    = video_len;                   /* 一帧编码后视频数据的字节数 */
TiRtcSendVideoStream(hconn, &video, video_frame);
```

> 音频与视频都使用 `TIRTCFRAMEINFO`，发送接口不同：
>
> - 音频调用 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsendaudiostream" target="_blank" rel="noopener">`TiRtcSendAudioStream`</a>。
> - 视频调用 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsendvideostream" target="_blank" rel="noopener">`TiRtcSendVideoStream`</a>，**第一帧必须是关键帧**。
> - 返回 `TIRTC_E_BUSY` 表示发送缓冲已满。SDK 会丢弃非关键帧，直到收到下一个关键帧。

**④ 命令通道（信令）**：调用 [`TiRtcSendCommand`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsendcommand) 发送命令。开发者自定义 `cmdw` 必须 `≥ 0x10000`；`0x2000` 和 `0x2100` 是平台预定义命令，不能用于自定义业务。AI 使用 `0x2100`，设备互呼使用 `0x2000`。`on_command` 收到原始 `cmdw`，直接按原值分发。

**⑤ 错误处理**：所有 SDK 返回码统一用 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcgeterrorstr" target="_blank" rel="noopener">`TiRtcGetErrorStr(rc)`</a> 转可读串；版本号 <a href="https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcgetversion" target="_blank" rel="noopener">`TiRtcGetVersion()`</a>。

Linux C 参考实现通过 [session_arbiter.c](device-sim/device-sim-c/src/session_arbiter.c) 原子处理准入、pending 和 generation，再由 [session_coordinator.c](device-sim/device-sim-c/src/session_coordinator.c) 串行切换四类业务。

设备空闲时运行 `stream`；进入 `voip`、`ai` 或 `call` 后暂停 `stream`，由当前业务独占 TiRTC，结束后再恢复。这套方式适合媒体资源有限、业务互斥的设备，但不是 SDK 的限制。

---

### 你的第一个设备端功能：H5 出图

如果还没跑通过完整流程，请先按[项目快速体验](../README.md)使用 Python 模拟器完成上线、绑定和 H5 出图。本节用 Linux C 参考实现验证设备协议和文件媒体路径；接入真实设备时，再按[二次开发文档](device-porting.md)适配硬件。

#### 步骤 1：设备上线

所有能力都以设备上线为前提。上线完成后，设备应持有 `device_id + device_key`、`mqtt_token` 和正式 MQTT 长连接。Linux C 参考实现用 `device_flow.c` 封装这段流程；[main.c](device-sim/device-sim-c/src/main.c) 是可在 Linux 上运行的控制流示例，不是硬件产品的启动代码：

```c
#include "device_flow.h"

DeviceServices svc;
fetch_services(&svc, "http://ep-open.tangeopen.com");   /* 阶段 0：服务发现，拿到各 server 地址 + tirtc_endpoint */

/* 阶段 1：未绑定设备 → report 拿 6 位验证码 → 临时 MQTT 等用户在 H5 输码绑定 */
ReportResult rep;
report_device(svc.device_server, mac, NULL, NULL, &rep); /* → rep.code：TTS 播报的 6 位验证码 */
char did[64] = "", dkey[256] = "";
connect_temp_mqtt(svc.mqtt_host, svc.mqtt_port,
                  rep.temp_client_id, rep.temp_token, 190, svc.mqtt_tls,
                  did, sizeof did, dkey, sizeof dkey);   /* 收到 auth_grant 后回填 did/dkey */

/* 阶段 2：已绑定 → HMAC 签名换 mqtt_token（返回 6006 表示已解绑，需带签名重新 report，见 device-integration） */
char mqtt_token[512];
get_mqtt_token(svc.device_server, did, dkey, mac, mqtt_token, sizeof mqtt_token);

/* 阶段 3：正式 MQTT（ClientID=sn_{device_id}），阻塞运行 */
MqttMsgHandler handler = {0};   /* 业务回调集合：各能力把来电等回调注册进来（见扩展能力） */
connect_mqtt_blocking(svc.mqtt_host, svc.mqtt_port, did, mqtt_token,
                      &handler, &rt /*运行时上下文，透传给回调*/,
                      &g_stop /*停止标志*/, svc.mqtt_tls);
```

> 绑定后必须持久化 `did/dkey`。Linux C 参考实现将凭证写入权限为 0600 的 `device_creds.json`，写入时使用同目录临时文件、`fsync` 和 `rename`。产品设备应改用受保护的设备存储。已预置凭证的设备可以直接从阶段 2 开始。
>
> 完整字段、临时和正式连接参数、token 刷新及断连原因码见 [device-integration.md](device-integration.md#上线全流程)。

#### 步骤 2：H5 推流出图

H5 实时预览由设备被动监听，不需要调用业务 HTTP API，也不需要发送 MQTT 消息。设备调用 [`TiRtcStart`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcstart) 后等待 H5 连接，连接建立后开始推送音视频。

H5 使用固定的 stream，不进行协商：

| 方向 | stream_id | 格式 |
|------|-----------|------|
| 设备 → H5 音频 | `10` | G.711 A-law（`alaw`），8kHz（320B / 40ms） |
| 设备 → H5 视频 | `11` | H.264 裸流，首帧须关键帧 |
| H5 → 设备 按住说话 | `14` | G.711 A-law（`alaw`），默认 8kHz |

`on_conn_accepted` 回调只负责投递连接事件。回调返回后，设备控制任务保存句柄并启动推流线程；推流线程按时间戳节流，依次取帧、填写帧头并发送。

下面的产品侧伪代码只用于说明调用顺序。`app_rtc_event_push`、`h264_source_next_*`、`src` 和 `VIDEO_FRAME_MS` 都不是 TiRTC API，也不是可直接编译的 Linux C 参考实现符号。

实际代码见 [tirtc_stream.c](device-sim/device-sim-c/src/tirtc_stream.c) 和 [file_media_source.c](device-sim/device-sim-c/src/file_media_source.c)。

```c
/* SDK 回调：只投递事件。 */
static void on_conn_accepted(tirtc_conn_t hconn) {
    app_rtc_event_push(APP_RTC_CONNECTED, hconn, 0);
}

/* 应用控制任务：从固定队列取事件后调用，不在 SDK 回调栈内。 */
static void stream_control_on_connected(tirtc_conn_t hconn) {
    s_active_conn = hconn;
    pthread_create(&s_push_thread, NULL, push_thread, NULL);
}

/* 推流线程：按 pts 节流，循环发送 */
static void *push_thread(void *arg) {
    int64_t audio_pts = 0, video_pts = 0, start = now_ms();   /* now_ms / sleep_ms 见 common.h */
    while (s_active_conn && !g_stop) {
        /* 节流：对齐单调时钟，等到下一帧该发的时刻，避免发太快 */
        int64_t target = audio_pts < video_pts ? audio_pts : video_pts;
        int64_t wait = target - (now_ms() - start);
        if (wait > 2) { sleep_ms(wait > 50 ? 50 : (int)wait); continue; }

        if (audio_pts <= video_pts) {
            unsigned char pkt[320];
            int len = h264_source_next_audio(&src, pkt, 320);   /* 取一帧 A-law；产品替换为编码器输出 */
            TIRTCFRAMEINFO fi = {0};
            fi.stream_id = 10; fi.media = TIRTC_AUDIO_ALAW; fi.flags = TIRTC_AUDIOSAMPLE_8K16B1C;
            fi.ts = (uint32_t)audio_pts; fi.length = len;
            if (TiRtcSendAudioStream(s_active_conn, &fi, pkt) == TIRTC_E_INVALID_HANDLE)
                { sleep_ms(5); continue; }                      /* 句柄未就绪，短暂重试 */
            audio_pts += 40;                                    /* A-law/alaw 8kHz = 40ms/帧 */
        } else {
            unsigned char *frame = NULL; int is_key = 0;
            int len = h264_source_next_video(&src, &frame, &is_key, s_force_key);  /* 取一帧 H.264；s_force_key 由 on_request_key_frame 置位 */
            TIRTCFRAMEINFO fi = {0};
            fi.stream_id = 11; fi.media = TIRTC_VIDEO_H264;
            fi.flags = is_key ? TIRTC_FRAME_FLAG_KEY_FRAME : 0;  /* 首帧须关键帧 */
            fi.ts = (uint32_t)video_pts; fi.length = len;
            TiRtcSendVideoStream(s_active_conn, &fi, frame);
            free(frame);
            video_pts += VIDEO_FRAME_MS;                        /* 视频帧间隔，如 33ms@30fps */
        }
    }
}
/* 其余回调（在 cbs 里注册，见速查①）：
   on_request_key_frame(hconn, sid) → 置 s_force_key，下一帧强制 IDR
   on_audio(hconn, pFi, data)       → sid=14；产品复制并投递给播放队列 */
```

> 上述变量和函数只服务于伪代码。Linux C 默认适配使用 `FileMediaSource` 读取已编码文件；产品通过 `DeviceMediaSourceOps` 接入真实的采集和编码模块。连接断开及 `TIRTC_E_BUSY` 的处理见 [tirtc_stream.c](device-sim/device-sim-c/src/tirtc_stream.c) 中的 `_push_thread()`。

跑通后，H5 可以播放参考实现从文件发送的音视频；按住说话时，参考实现能够记录收到的 H5 音频帧。这里记录到音频帧只说明接收成功，不代表扬声器已经播放。

相关代码分在两个文件中：[tirtc_runtime.c](device-sim/device-sim-c/src/tirtc_runtime.c) 统一管理进程级 SDK 生命周期和回调分发，[tirtc_stream.c](device-sim/device-sim-c/src/tirtc_stream.c) 管理实时流会话和媒体发送。完整契约与排查方法见 [device-h5-live.md](device-h5-live.md#设备侧接入)。

---

### 扩展更多能力

上线和 H5 出图跑通后，再根据产品需要接入 AI 对讲、微信 VoIP 或设备互呼。一次接入一种能力，完成联调和验收后再继续，问题会更容易定位。

#### 1. AI 对讲

AI 对讲由设备主动发起，不经过 MQTT 来电。设备先调用 [`GET /v1/ai/token`](api-reference.md#get-v1aitoken)，并在请求中携带 `Authorization: Bearer <mqtt_token>`。接口返回建立会话所需的 `peer_id`、`token` 和 `role_id`。

拿到 token 后，调用 [`TiRtcWhipConnect`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcwhipconnect) 建立连接。建连 300ms 后，再通过 [`TiRtcSendCommand`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsendcommand) 发送 `start_session`。

上行音频使用 [`TiRtcSendAudioStream`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsendaudiostream) 发送，下行音频从 `on_audio` 回调接收。

```c
/* peer_id / token / role_id 来自协议步骤第 1 步 */

TiRtcWhipConnect(peer_id, token, on_ai_connected, NULL);   /* WHIP 建连（异步） */

void on_ai_connected(int err, tirtc_conn_t hconn, void *user) {
    if (err) { LOG_E("%s", TiRtcGetErrorStr(err)); return; }   /* SDK 错误码统一转串 */
    g_ai_conn = hconn;
    g_ai_connect_at = now_ms();   /* 业务侧记录连接时刻，供主循环判断 300ms（now_ms=平台毫秒时间戳） */
}

/* 主循环（业务线程）：连接满 300ms 后发 start_session */
if (g_ai_conn && !g_ai_started && now_ms() - g_ai_connect_at >= 300) {
    TiRtcSendCommand(g_ai_conn, 0x2100, start_session_json, start_session_len);
    g_ai_started = 1;             /* start_session_json：JSON-RPC payload，字段见 device-ai.md */
}

/* on_command 收到 0x2100 成功响应 → 起上行线程：TiRtcSendAudioStream(g_ai_conn, &fi, pcm_16k_buf) */
/* on_audio(hconn, pFi, data) → AI 下行音频，交给扬声器播放 */
```

验收时，设备发起对话后应能听到 AI 应答，并能正常进行多轮对话。

参考代码在 [tirtc_ai.c](device-sim/device-sim-c/src/tirtc_ai.c)：`ai_get_token()` 获取会话凭证，`ai_start_session()` 建立会话，`ai_poll()` 处理 300ms 延迟信令。完整字段见 [device-ai.md](device-ai.md#建立会话)。

---

#### 2. 微信 VoIP 对讲

微信小程序和设备通过 VoIP 进行双向音视频对讲，设备使用 [`TiRtcWhipConnect`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcwhipconnect) 建立连接。接入顺序如下：

1. 调用 [`POST /v1/voip/device/profile`](api-reference.md#post-v1voipdeviceprofile)（`Bearer <mqtt_token>`）上报媒体能力。没有 profile 时，来电无法下发：

   ```http
   POST /v1/voip/device/profile
   Authorization: Bearer <mqtt_token>
   Content-Type: application/json

   {
     "screen_width": 640,        // 设备自身屏幕宽度（px）；no_video=true 时传 1
     "screen_height": 480,       // 设备自身屏幕高度（px）；no_video=true 时传 1
     "camera_rotation": 0,       // 微信通话 UI 顺时针旋转：0 / 90 / 180 / 270
     "aspect_ratio": 1.3333333333, // 视频宽高比，例如 4/3
     "object_fit": "contain",    // 可选：fill / contain；省略时使用微信默认值
     "hor_mirror": false,        // 水平镜像
     "vert_mirror": false,       // 垂直镜像
     "audio_rate": 8000,         // 音频采样率：8000 / 16000
     "audio_channels": 1,        // 声道数：1 / 2
     "up_video_mt": "h264",      // 上行视频编码（设备→小程序）：h264 / h265 / mjpeg / none
     "down_video_mt": "mjpeg",  // 下行视频编码（小程序→设备）：h264 / mjpeg / none（不支持 h265）
     "video_res_mode": "fit_screen", // 微信下行视频等比缩小到设备屏幕范围
     "down_audio_mt": "amr",     // 下行音频编码（小程序→设备）：alaw / amr / opus，默认 alaw
     "no_video": false,          // 无视频能力置 true，此时 up/down_video_mt 可留空
     "calling_timeout_sec": 30   // 呼叫超时秒数
   }
   ```

   > `fit_screen` 和 `fill_screen` 仅适用于 `down_video_mt=mjpeg`，并要求有效的屏幕宽高。上行音频编码由 `TiRtcSendAudioStream` 实际发送的帧格式决定，不在 profile 中上报。完整字段与取值见 [api-reference.md](api-reference.md#post-v1voipdeviceprofile)。

2. 监听 MQTT `device/sn_{device_id}/cmd`，从 `call_incoming` 的 payload 中读取 `peer_id` 和 `token`；在 `device/sn_{device_id}/notify` 接收 `call_cancel` 和 `callers_update`。
3. 设备主动呼叫小程序时，调用 [`POST /v1/voip/device/call`](api-reference.md#post-v1voipdevicecall)。微信回调后，设备仍按第 2 步接收 `call_incoming`。

收到来电并决定接听后，设备调用 [`TiRtcWhipConnect`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcwhipconnect) 建立连接。

上行音频通过 [`TiRtcSendAudioStream`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsendaudiostream) 发送；启用视频时，再调用 [`TiRtcSendVideoStream`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsendvideostream)。下行媒体分别从 `on_audio` 和 `on_video` 回调接收。

```c
/* call_incoming payload 里的 peer_id / token（来自协议步骤第 2 步） */
TiRtcWhipConnect(peer_id, token, on_voip_connected, NULL);

void on_voip_connected(int err, tirtc_conn_t hconn, void *user) {
    if (err) { LOG_E("%s", TiRtcGetErrorStr(err)); return; }
    g_voip_conn = hconn;   /* 随后 SendAudioStream 上行；on_audio 收对端音频 */
}
```

验收时需要确认两个方向：小程序呼叫设备能够接通并正常传输双向音视频；设备主动呼叫时，小程序能够收到来电提醒。

参考代码在 [tirtc_voip.c](device-sim/device-sim-c/src/tirtc_voip.c)。其中，`voip_report_profile()`、`voip_accept_pending()`、`voip_reject_pending()` 和 `voip_dial_authorized()` 分别处理能力上报、接听、拒接和外呼。

设备侧完整流程见 [device-voip.md](device-voip.md#小程序呼设备)，小程序侧见[微信小程序开发](#微信小程序开发)。

---

#### 3. 设备互呼

设备互呼使用 [`TiRtcConnect`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcconnect) 在两台设备之间建立 P2P 音视频通话，不使用 VoIP 和 AI 对讲所用的 [`TiRtcWhipConnect`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcwhipconnect)。接入顺序如下：

1. 双方需要是已接受的设备联系人。同账号下的设备无需添加好友；跨账号设备由一方调用 [`POST /v1/call/device/contacts/request`](api-reference.md#post-v1calldevicecontactsrequest) 发起申请，对方再调用 [`POST /v1/call/device/contacts/respond`](api-reference.md#post-v1calldevicecontactsrespond) 接受。
2. 主叫调用 [`POST /v1/call/request`](api-reference.md#post-v1callrequest) 创建房间，等待被叫通过 [`TiRtcConnect`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcconnect) 建立连接。
3. 被叫从 MQTT 收到 `call_incoming(channel=device)`。
4. 被叫调用 [`POST /v1/call/device/info`](api-reference.md#post-v1calldeviceinfo) 换取连接 `token`。

被叫调用 [`TiRtcConnect`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcconnect) 主动连接主叫。连接成功后，先通过 [`TiRtcSendCommand`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsendcommand) 发送 `0x2000` 接通确认。

媒体通过 [`TiRtcSendAudioStream`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsendaudiostream) 和 [`TiRtcSendVideoStream`](https://docs.tange.ai/products/tirtc/api-reference/c.html#tirtcsendvideostream) 发送，下行媒体从 `on_audio` 和 `on_video` 回调接收。

```c
/* caller_id（主叫 device_id）/ token 来自协议步骤第 4 步 */

TiRtcConnect(caller_id, token, on_call_connected, NULL);   /* P2P 建连（异步） */

void on_call_connected(int err, tirtc_conn_t hconn, void *user) {
    if (err) { LOG_E("%s", TiRtcGetErrorStr(err)); return; }
    TiRtcSendCommand(hconn, 0x2000, room_id_json, room_id_len);   /* 通知主叫“已接通” */
    /* 随后 SendAudioStream / SendVideoStream 双向收发 */
}
```

验收时，同账号下的两台设备应能直接接通；跨账号设备应在完成好友绑定后接通。

参考代码分别在 [call_session.c](device-sim/device-sim-c/src/call_session.c) 和 [tirtc_call.c](device-sim/device-sim-c/src/tirtc_call.c)。`call_session_do_call()` 负责主叫建房，`call_on_device_call_incoming()` 负责被叫获取 token、建立连接并发送 `0x2000` 接通确认。

完整流程见 [device-call.md](device-call.md#被叫流程)。

如果一台设备同时承载多类业务，例如推流时收到 VoIP 来电，或 AI 会话中收到设备呼叫，必须通过统一状态机处理抢占和恢复。状态设计见[统一状态机](device-session-model.md)，并发控制和产品实现细节见[会话仲裁参考](device-session-arbiter.md)。

---

## 前端开发

> **H5：** 使用 TiRTC Web SDK，通过 [`TiRtcConn`](device-h5-live.md#h5-端接入) 直连设备。
>
> **微信小程序：** 使用[微信 IoT VoIP](weixin-mini-program/README.md#微信-voip-开发) 与设备对讲，不使用 `TiRtcConn`。

### H5 开发

H5 页面由 user-server 提供静态文件。用户登录并获取 RTC token 后，通过 Web SDK 直连设备。完整页面见 [player.html](user-server/static/player.html)。
SDK 类、方法和参数以 [TiRTC Web API 参考](https://docs.tange.ai/products/tirtc/api-reference/web.html)
为准。

登录后可以在 [devices.html](user-server/static/devices.html) 管理设备。页面通过 [`GET /v1/user/device/list`](api-reference.md#get-v1userdevicelist) 获取当前用户绑定的设备；点击设备名称旁的“修改”，再调用 [`PUT /v1/user/device/name`](api-reference.md#put-v1userdevicename) 保存新名称。

列表还会显示设备在线状态，以及是否带摄像头、屏幕等能力。

页面按以下顺序接入：

1. 用户登录 user-server 拿 `user_jwt`，且 `device_id` 已绑定在该用户名下
2. [`GET /v1/user/device/rtc-token?device_id=...`](api-reference.md#get-v1userdevicertc-token) 拿 `token`、`app_id`
3. 用 `app_id` 初始化 Web SDK，按固定 stream 建连、订阅

```js
const { rtcToken, appId } = await fetchRtcToken();   // GET /v1/user/device/rtc-token

TiRtc.initialize(TiRtcInitOptions({ appId }));
await TiRtc.videoOutputReady();                       // 等待视频输出就绪
const conn = new TiRtcConn();
const audioOutput = TiRtcAudioOutput({ connection: conn, streamId: 10 });      // 设备音频
const videoOutput = TiRtcVideoOutput({ connection: conn, streamId: 11 });      // 设备视频
const talkback    = new TiRtcAudioInput({ connection: conn, streamId: 14 });   // 按住说话
talkback.setOptions({ sampleRate: 8000 });

await conn.connect({ deviceId, token: rtcToken });   // 触发设备 on_conn_accepted
videoOutput.attach(); audioOutput.attach();
conn.subscribeVideo({ streamId: 11 });
conn.subscribeAudio({ streamId: 10 });
/* 离开页面：conn.disconnect() + 两个输出对象 detach() + talkback.stop()/detach() */
```

跑通后，页面能够播放设备的画面和声音；按住说话按钮时，设备端能够收到音频。

更多接入细节见 [device-h5-live.md](device-h5-live.md#h5-端接入)。

---

### 微信小程序开发

小程序负责用户账户、设备绑定和微信 VoIP 授权；设备侧来电仍由 C 固件处理。工程位于 [weixin-mini-program](weixin-mini-program)，可以直接用微信开发者工具打开。

接入步骤如下：

1. 导入 `thing-connect/weixin-mini-program`，AppID 用已开通 IoT VoIP 的正式 AppID（不要用测试号）
2. 编辑 [app.js](weixin-mini-program/app.js) 的 `globalData`：

```js
globalData: {
  userServerBaseUrl: 'https://api.example.com',   // 登录、注册、绑定、设备列表
  voipServerBaseUrl: 'https://api.example.com',   // 微信登录、授权、取消呼叫
  modelId: '微信 IoT 平台分配的 ModelID',
  wxAppId: '你的正式小程序 AppID',
}
```

3. 跑通账户与设备：登录 → 绑定（6 位验证码或 device_id）→ 设备列表 → `wx.login()` + [`POST /v1/voip/user/wechat-mini-login`](api-reference.md#post-v1voipuserwechat-mini-login)
4. 接入 VoIP（**不能只调插件，须先建授权关系**）：

```js
const ticket = await POST('/v1/voip/user/sn-ticket');          // 1. 取授权票据（见下方链接）
await wx.requestDeviceVoIP({ /* modelId / ticket / sn */ });    // 2. 微信侧授权
await POST('/v1/voip/user/report-auth', { /* 微信返回结果 */ }); // 3. 回写 voip-server（见下方链接）
// 4. 用户在插件通话 UI 发起呼叫 → 微信回调 voip-server → MQTT call_incoming 下发设备
```

调用顺序与字段说明：[`POST /v1/voip/user/sn-ticket`](api-reference.md#post-v1voipusersn-ticket) → [`POST /v1/voip/user/report-auth`](api-reference.md#post-v1voipuserreport-auth)。
微信授权函数的参数和限制见
[`wx.requestDeviceVoIP`](https://developers.weixin.qq.com/miniprogram/dev/framework/device/voip/auth.html)。

跑通后，小程序应能完成登录、绑定和授权，并成功发起 VoIP 呼叫。

更多说明见 [weixin-mini-program/README.md](weixin-mini-program/README.md)。

---

## 服务端：功能与部署

五个业务服务和 Admin Server 共用 MySQL、Redis，需要 MQTT 的服务连接同一个 Broker。五个业务服务的 `jwt_secret` 必须一致，六个服务的 `internal.key` 也必须一致；Admin 使用独立的 `admin.jwt_secret`。

`config.yaml` 保存数据库、Redis、MQTT、服务地址和进程认证密钥等启动引导参数。Admin 注册表中的业务配置使用数据库发布值；没有发布记录时，使用注册表默认值。

五个业务服务必须在启动时连接 Admin，完成首次配置加载。通用配置中的 TiRTC 应用 ID 和访问密钥是必填项，需要在 Admin Web 中配置；缺少这两项时，服务不会启动。

| 服务 | 调用方 | 主要职责 |
|---|---|---|
| device-server | 设备 | Report、Token、设备上线 |
| user-server | H5、小程序 | 用户账户、设备绑定、H5 RTC token、静态页面 |
| voip-server | 小程序、微信服务器、设备 | 微信 VoIP 回调、授权、来电 MQTT 通知 |
| ai-server | 设备、H5 管理页 | AI 会话 token、AI 角色管理 |
| call-server | 设备、H5 | 设备联系人、房间、设备互呼 token 与通知 |
| admin-server | 管理员、五个业务服务 | Admin Web、RBAC、用户设备管理、动态配置、服务状态与审计 |

生产环境固定安装 Admin、Device、User，VoIP、AI、Call 按需选择。服务器准备、MySQL 权限、首次 Web 安装、Supervisor、Nginx、验收、更新和迁移见[部署指南](deployment.md)。

Admin 功能和使用约束见 [Admin Server README](admin/admin-server/README.md)。

---

## 查接口与错误码

HTTP 接口的请求和返回字段、成功码、业务错误码及微信回调错误码集中在以下两份文档中：

- [api-reference.md](api-reference.md)：五个业务服务的接口与错误码。
- [admin/admin-server/API.md](admin/admin-server/API.md)：Admin 登录、RBAC、配置和运维接口。

各专题文档末尾还有问题排查和协议速查，可用于定位常见错误。MQTT 断连原因码、TiRTC SDK 返回值约定见 [device-integration.md](device-integration.md)。

---

## 文档地图

| 文档 | 何时看 |
|---|---|
| [设备上线与 MQTT](device-integration.md) | 第一个功能 · 步骤 1 上线 |
| [H5 实时查看与按住说话](device-h5-live.md) | 第一个功能 · 步骤 2 出图 / H5 开发 |
| [AI 对讲设备接入](device-ai.md) | 扩展：AI 对讲 |
| [微信 VoIP 对讲设备接入](device-voip.md) | 扩展：VoIP / 小程序 |
| [设备呼设备接入](device-call.md) | 扩展：设备互呼 |
| [设备统一状态机](device-session-model.md) | 一台设备承载多类业务时 |
| [设备会话竞态仲裁](device-session-arbiter.md) | 待处理来电、会话代次、迟到回调与事件队列 |
| [从 Linux C 参考实现进行二次开发](device-porting.md) | Linux 交叉编译 / 十项产品 TODO 与验收 |
| [微信小程序开发](weixin-mini-program/README.md) | 做小程序 |
| [部署与运维](deployment.md) | 上生产 / 二次开发服务端 |
| [API Reference](api-reference.md) | 联调字段、排错 |
| [API 错误响应规范](error-response-policy.md) | 错误码兼容、`msg` 使用和安全边界 |

## 代码地图

```text
thing-connect/
├── device-sim/device-sim-c/  # Linux C 参考实现（文件媒体，不接硬件）
├── device-sim/device-sim-py/  # Python 设备模拟器（首次体验用）
├── device-sim/sdk/<platform>/2.3.0/include/tirtc/tiRTC.h  # TiRTC C SDK 权威头文件
├── device-server/            # 设备身份与 MQTT token
├── user-server/              # 用户、绑定、H5 静态页面与 RTC token
├── voip-server/              # 微信 IoT VoIP
├── ai-server/                # AI 对话和角色管理
├── call-server/              # 设备联系人和设备互呼
├── weixin-mini-program/      # 微信小程序
├── internal/                 # Go 服务共享能力
├── scripts/                  # 数据库与迁移脚本
└── tests/                    # 集成测试
```

## License

MIT © 探鸽智能
