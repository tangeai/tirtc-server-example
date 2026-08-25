# API Reference

> 面向开发者和二次开发的完整接口文档。业务流程和架构说明见 [ThingConnect 开发者文档](README.md)。

<a id="service-discovery"></a>

## 服务发现

### `GET /services`

设备启动时通过该接口获取各业务服务和 TiRTC 的入口地址，无需鉴权。自托管环境需要在 user-server 中启用 `discovery.enabled`，并配置设备可访问的公网地址。

向 `fetch_services()` 传入入口根地址后，Linux C 参考实现会请求根地址下的 `/services`。演示环境入口为 `http://ep-open.tangeopen.com/services`。

**成功响应** — HTTP 200，JSON 对象：

| 字段 | 必填 | 说明 |
|------|:--:|------|
| `device-srv` | ✅ | device-server 根地址 |
| `user-srv` | — | user-server 根地址，供支持用户端入口发现的客户端使用 |
| `voip-srv` | ✅ | voip-server 根地址 |
| `ai-srv` | ✅ | ai-server 根地址 |
| `call-srv` | ✅ | call-server 根地址 |
| `mqtt-srv` | ✅ | MQTT 地址，格式 `mqtt://host:port` 或 `mqtts://host:port` |
| `tirtc-srv` | ✅ | TiRTC SDK 服务入口；用于 `TIRTC_OPT_SERVICE_ENDPOINT` |

设备应使用服务发现返回的地址，不要将各业务服务地址或 `tirtc-srv` 固化在固件中。Linux C 参考实现见 [`fetch_services()`](device-sim/device-sim-c/src/device_flow.c)。

---

## 约定

### 响应格式

| 服务 | 成功 `code` | 成功 `msg` | 错误 HTTP 状态 | 说明 |
|------|:--:|:--:|:--:|------|
| device-server | 200 | `"ok"` | 实际状态码 | 错误通过 HTTP 状态码 + body `code` 字段区分 |
| user-server | 200 | `"ok"` | 实际状态码 | 同上 |
| ai-server | 200 | `"ok"` | 实际状态码 | 同上；上游与内部原始错误会清洗后返回 |
| voip-server | 0 | `"ok"` | 200（鉴权 401 除外） | 错误通过 body `code` 字段区分 |
| call-server | 200 | `"ok"` | 200（鉴权 401 除外） | 与 device/user/ai-server 一致，跟 voip-server 不同（H5 会直连 call-server，两套成功码容易踩坑） |

> 上表适用于使用统一业务响应体的接口。这些接口成功时都包含 `"msg":"ok"`；下文部分示例为简洁起见省略了该字段。
>
> 服务发现和微信回调使用各自的响应格式：`GET /services` 直接返回地址对象，`GET /v1/voip/notification/:wx_app_id` 返回纯文本，`POST /v1/voip/notification/:wx_app_id` 返回 `errcode/errmsg`。
>
> 客户端必须按数值 `code` 分支，不得比较 `msg` 文本；错误说明允许在不改变
> `code`、HTTP 状态和 JSON 字段的前提下优化。完整规则见
> [API 错误响应规范](error-response-policy.md)。

### 鉴权与业务错误

JWT 缺失、无效、过期或缺少必要 claim 时返回 HTTP 401 + `code=401`。JWT
鉴权通过后，各类业务错误按下表处理：

| 服务/场景 | 响应 | 客户端动作 |
|---|---|---|
| voip-server：微信登录状态无效/OpenID 不匹配 | HTTP 200 + `40203` | 重新完成微信登录 |
| voip-server：微信 VoIP 授权不存在或失效 | HTTP 200 + `40205` | 刷新授权列表并引导恢复授权 |
| voip-server：无权访问设备或资源 | HTTP 200 + `40300` | 停止当前资源操作 |
| voip-server：设备已解绑 | HTTP 200 + `6006` | 重新走设备绑定 |
| call-server：无权操作房间、设备或联系人 | HTTP 200 + `40300` | 停止当前资源操作 |
| voip/call-server：内部服务凭证无效 | HTTP 200 + `40301` | 检查共享 `X-Internal-Key` |
| ai-server：内部服务凭证无效 | HTTP 403 + `40301` | 检查共享 `X-Internal-Key` |

### 鉴权方式

| 调用方 | 鉴权方式 | Token 来源 |
|--------|---------|-----------|
| IoT 设备 | `Authorization: Bearer <mqtt_token>` | `POST /v1/device/token` 返回的 `mqtt_token` |
| 待绑定设备 TTS | `Authorization: Bearer <temp_token>` | 同一次 `POST /v1/device/report` 返回的 `temp_token` |
| H5、小程序用户 | `Authorization: Bearer <user_jwt>` | `POST /v1/user/register` 或 `/v1/user/login` 返回的 `token` |
| 微信服务器 | 签名校验（微信标准） | — |
| 内部服务 | `X-Internal-Key: <shared-key>` | 各服务共同配置的内部调用密钥 |
| 公开接口 | 无 | — |

**设备 JWT**: 正式 `mqtt_token` 的 `device_id` claim 是设备 ID；临时 `temp_token` 的 `device_id` claim 是 `temp_client_id`。两者都有 `exp`，由 device-server 签发；TTS 只接受与验证码记录匹配的临时 token。

**用户 JWT**: 包含 `user_id`、`auth_revision`、`iat` 和 `exp`。由 user-server 签发，user-server、
voip-server、ai-server 和 call-server 使用相同的 `jwt_secret` 验证。账号禁用、密码修改或认证版本递增后，只拒绝该用户的旧令牌；历史令牌缺少 `auth_revision` 时按版本 1 兼容。

### Content-Type

业务接口的 POST / PUT 请求默认使用 `Content-Type: application/json`。文件上传使用
`multipart/form-data`；微信 VoIP 通知回调的请求体是 XML，不适用上述 JSON 约定。GET
接口通常无需设置 `Content-Type`。

---

## device-server

面向 IoT 硬件设备，处理设备注册上线。

### `POST /v1/device/report`

上报设备 MAC，获取 6 位验证码和临时 MQTT 连接凭证。

**鉴权**: 无（可选 HMAC 签名）

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Content-Type | ✅ | `application/json` |
| X-Device-Id | 情况1 | 设备 ID（来自 `device_pool`） |
| X-Timestamp | 情况1 | Unix 秒级时间戳，与服务器偏差 ≤300s |
| X-Nonce | 情况1 | 16 位随机十六进制，300s 内不可重复 |
| X-Signature | 情况1 | `Base64(HMAC-SHA256(device_key, device_id + timestamp + nonce))` |

> 四个签名 Header **要么全不带，要么全带**。部分带 = 签名失败（6008）。

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| mac | string | ✅ | 设备 MAC 地址，格式 `AA:BB:CC:DD:EE:FF`，不可为空 |

**请求示例**

```json
{ "mac": "AA:BB:CC:DD:EE:FF" }
```

**两种请求形态**

| 情况 | Header 签名 | 处理方式 |
|------|:--:|---------|
| 1 — 签名信任 | ✅ 四个全带且验签通过 | 跳 L1/L2/L4，校验 device_id↔MAC 一致性，返回验证码 |
| 2 — 裸设备 | ❌ | 走完整四层限频，返回验证码 |

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "code": "386236",
    "temp_token": "eyJhbGciOiJIUzI1NiIs...",
    "temp_client_id": "tmp_a1b2c3d4"
  }
}
```

| 字段 | 说明 |
|------|------|
| code | 6 位验证码，H5 扫码绑定用 |
| temp_token | JWT，临时 MQTT 连接 Password，TTL = `code_ttl` |
| temp_client_id | 临时 MQTT ClientID / Username，格式 `tmp_{8位hex}` |

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体 JSON 解析失败 |
| 6008 | 401 | 签名不完整、签名失败、时间戳偏差过大、Nonce 重放；时间戳格式或偏差错误会在 `msg` 中给出具体原因 |
| 6010 | 400 | mac 为空 |
| 6013 | 403 | 签名上报中的 MAC 与该 device_id 已记录的 MAC 不一致 |
| 40901 | 409 | 该 MAC 已有未消费验证码且本次未命中幂等重放（详见下方「重复上报同一 MAC 的行为」），附 `Retry-After` 头 |
| 429 | 429 | L2 单 IP 新 MAC 过多 / L3 同 MAC 请求超限 / L4 全局待处理码超限，附 `Retry-After` 头 |
| 50000 | 500 | 服务器内部错误（Redis/DB/系统异常） |

**重复上报同一 MAC 的行为**

验证码按 MAC 加锁：锁存活时间 = `code_ttl`（默认 190s）；**绑定消费验证码后立即释放锁**，TTS 播报（`/v1/device/tts`）只读取、不释放锁。同一 MAC 在同一个 `rate_limit_window`（默认 190s）内重复上报，结果取决于第几次上报：

| 上报次序（同一 `rate_limit_window` 内） | 返回 | 说明 |
|------|------|------|
| 第 1 次 | 200 | 生成新验证码 |
| 第 2 ~ `rate_limit_max_hits` 次（默认 ≤10） | 200，**返回与首次完全相同的 `code` / `temp_token` / `temp_client_id`** | 幂等重放：设备重启或重连后重复上报不会换码、也不报错，并绕过全局待处理码上限 |
| 超过 `rate_limit_max_hits` 次 | 429 | 同一 MAC 限频，`Retry-After` = `rate_limit_window` |

> 单个客户端按顺序重复上报同一 MAC 时，只会得到 200（返回原验证码）或超限后的 429。正常情况下不会返回 409。

**409（40901）触发条件**

409 表示该 MAC 仍持有未消费验证码，但本次请求没有命中上表的幂等重放，而是在新建验证码时遇到已有锁。这是并发与缓存时序边界的兜底返回，`Retry-After` = `code_ttl`。

常见原因如下：

- **并发重复上报**：同一 MAC 几乎同时发来两笔 Report，先抢到锁的返回 200，后到的拿 409。
- **限频窗口已过、验证码锁仍在**：当部署配置 `rate_limit_window < code_ttl` 时（默认两者相等，均为 190s），计数器先于锁过期，下一笔上报看上去像首次、不走重放，却撞上仍在的锁。
- **签名 / 匿名上报交错**：同一 MAC 先匿名上报、再带签名上报时，签名路径会作废已有匿名码并重取锁，期间并发请求可能撞锁。

收到 409 属瞬态错误，按 `Retry-After` 重试即可（重试通常会命中幂等重放，返回原码）。

---

### `GET /v1/device/tts`

把 `/v1/device/report` 返回的 6 位设备验证码合成为 8kHz、单声道、16-bit little-endian PCM。该接口只接受与验证码同一次 Report 返回的 `temp_token`，不能使用正式 `mqtt_token` 或其他设备的临时 token。

**鉴权**: ✅ `Authorization: Bearer <temp_token>`

**查询参数**: `code` 为同一次 Report 返回的 6 位验证码；`fmt=wav` 时返回带 44 字节 RIFF/WAV 头的 `audio/wav`，不传时返回 `audio/pcm;rate=8000;channels=1;format=s16le`。

```http
GET /v1/device/tts?code=386236&fmt=wav
Authorization: Bearer <temp_token>
```

**成功响应**: HTTP 200，二进制音频；响应包含 `Cache-Control: no-store`。

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 查询参数缺少 code |
| 401 | 401 | temp_token 缺失、过期或签名错误 |
| 40013 | 404 | code 无效、过期，或不属于该 temp_token |
| 50000 | 500 | Redis 或音频构建失败 |

---

### `POST /v1/device/token`

已持有 device_id + device_key 的设备，用 HMAC 签名换取正式 MQTT 连接 token。

**鉴权**: 无（HMAC 签名）

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| X-Device-Id | ✅ | 设备 ID |
| X-Timestamp | ✅ | Unix 秒级时间戳，与服务器偏差 ≤300s |
| X-Nonce | ✅ | 16 位随机十六进制，300s 内不可重复 |
| X-Signature | ✅ | `Base64(HMAC-SHA256(device_key, device_id + timestamp + nonce))` |
| X-MAC | 否 | 设备 MAC。带上才启用 device_id↔MAC 一致性校验（不一致→`6013`）与「同账号同 MAC 不能绑多个 device_id」校验（冲突→`6015`）；省略则跳过这两项校验 |

**无请求体**

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": { "mqtt_token": "eyJhbGciOiJIUzI1NiIs..." }
}
```

**错误码**

| code | HTTP | 含义 | 设备动作 |
|------|------|------|---------|
| 6006 | 410 | 设备已解绑（user_id=0），需重新绑定 | 调用 Report（带签名 Header）获取验证码，重新走绑定流程 |
| 6008 | 401 | 任一 Header 字段为空、时间戳无效、签名不匹配、Nonce 重放；时间戳错误会返回“格式错误”或“设备与服务器时间偏差超过 300 秒” | 时间错误先同步设备时钟；其它情况检查 device_key 和签名算法 |
| 6013 | 403 | 带 X-MAC 且与设备已绑 MAC 不一致（疑似换 MAC/克隆） | 核对 X-MAC 与设备真实 MAC |
| 6015 | 409 | 带 X-MAC 且该 MAC 已绑定至本账号其它 device_id | 该 MAC 已在本账号其它设备绑定 |
| 50000 | 500 | 服务器内部错误 | 重试 |

> 签名算法：`Base64(HMAC-SHA256(device_key, device_id + timestamp + nonce))`

**签名示例 (C — mbedTLS)**

```c
#include <mbedtls/md.h>
#include <mbedtls/base64.h>

int device_sign(const char *device_id, const char *device_key,
                const char *timestamp, const char *nonce,
                char *sig_out, size_t sig_size)
{
    char raw[256];
    int raw_len = snprintf(raw, sizeof(raw), "%s%s%s", device_id, timestamp, nonce);
    unsigned char hmac[32];
    if (raw_len < 0 || (size_t)raw_len >= sizeof(raw)) return -1;
    if (mbedtls_md_hmac(mbedtls_md_info_from_type(MBEDTLS_MD_SHA256),
                    (const unsigned char *)device_key, strlen(device_key),
                    (const unsigned char *)raw, (size_t)raw_len, hmac) != 0) return -1;
    size_t olen;
    if (mbedtls_base64_encode((unsigned char *)sig_out, sig_size, &olen, hmac, 32) != 0) return -1;
    if (olen >= sig_size) return -1;
    sig_out[olen] = '\0';
    return 0;
}
```

---

## user-server

面向 H5 浏览器和微信小程序，处理账号与设备管理。

### `GET /v1/config/captcha`

获取当前人机验证 Provider 及其可公开的控件配置，用于初始化客户端控件。

**鉴权**: 无

**无请求参数**

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "provider": "yidun",
    "enabled": true,
    "public_config": { "captcha_id": "xxx" },
    "captcha_id": "xxx"
  }
}
```

`public_config` 仅包含可下发给客户端的配置，绝不包含密钥。`captcha_id` 为易盾兼容字段；新客户端应读取 `provider`、`enabled` 和 `public_config`。

---

### `POST /v1/user/send-code`

发送邮箱验证码（注册前调用）。

**鉴权**: 无

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| email | string | ✅ | 接收验证码的邮箱地址 |
| captcha | object | | 通用人机验证载荷，启用 Provider 时由客户端控件返回 |
| captcha.provider | string | | 签发验证票据的 Provider |
| captcha.token | string | | Provider 返回的验证票据 |
| captcha.metadata | object | | Provider 所需的非敏感附加字段 |
| captcha_id / validate / user | string | | 易盾兼容字段；新接入请使用 `captcha` |

**请求示例**

```json
{ "email": "user@example.com" }
```

**成功响应** — HTTP 200

```json
{ "code": 200, "data": null }
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体 JSON 解析失败或 email 格式无效 |
| 40012 | 400 | 人机验证失败 |
| 429 | 429 | 触发邮件验证码统一限频；注册和找回密码共用同一邮箱、IP 计数，默认 15 分钟内同一邮箱 5 次、同一 IP 20 次，后台配置可调整 |
| 50000 | 500 | 邮件发送失败或服务器内部错误 |

---

### `POST /v1/user/register`

注册新用户。

**鉴权**: 无

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| email | string | ✅ | 邮箱地址 |
| password | string | ✅ | 密码，最少 6 位 |
| code | string | ✅ | 邮箱收到的 6 位验证码 |

**请求示例**

```json
{
  "email": "user@example.com",
  "password": "mypassword",
  "code": "386236"
}
```

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": { "token": "eyJhbGciOi...", "user_id": 1 }
}
```

> `token` 为后续鉴权接口的 Bearer JWT，包含 `user_id`、`auth_revision`、`iat` 和 `exp`。新用户的设备绑定额度取注册时生效的 `user-server.service.quota_per_user`；未发布后台配置时读取 YAML，默认值为 10。账号状态、密码或认证版本变化时，仅该用户的旧令牌失效；缺少 `auth_revision` 的历史令牌按初始版本 1 兼容处理。

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体 JSON 解析失败或字段校验不通过 |
| 40013 | 400 | 验证码无效或已过期 |
| 4090 | 409 | 该邮箱已注册 |
| 50000 | 500 | 服务器内部错误 |

---

### `POST /v1/user/login`

用户登录。

**鉴权**: 无

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| email | string | ✅ | 邮箱地址 |
| password | string | ✅ | 密码 |
| captcha | object | | 通用人机验证载荷，字段含义同发送验证码接口 |
| captcha_id / validate / user | string | | 易盾兼容字段；新接入请使用 `captcha` |

**请求示例**

```json
{
  "email": "user@example.com",
  "password": "mypassword"
}
```

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": { "token": "eyJhbGciOi...", "user_id": 1 }
}
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体 JSON 解析失败或字段校验不通过 |
| 40012 | 400 | 人机验证失败 |
| 4091 | 401 | 邮箱或密码错误 |
| 50000 | 500 | 服务器内部错误 |

---

### `POST /v1/user/password-reset/send-code`

发送找回密码的邮箱验证码。为避免泄露邮箱是否已注册，格式正确且通过人机验证的请求均返回成功；只有已注册邮箱会收到邮件。

成功响应表示请求已受理；验证码邮件由后台异步投递，可能有短暂延迟。未收到邮件时可稍后重新发起请求。

**鉴权**: 无

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| email | string | ✅ | 注册邮箱地址 |
| captcha | object | | 通用人机验证载荷，字段含义同发送验证码接口 |
| captcha_id / validate / user | string | | 易盾兼容字段；新接入请使用 `captcha` |

**成功响应** — HTTP 200

```json
{ "code": 200, "data": null }
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体 JSON 解析失败或 email 格式无效 |
| 40012 | 400 | 人机验证失败 |
| 429 | 429 | 邮件处理繁忙，或触发邮件验证码统一限频；注册和找回密码共用同一邮箱、IP 计数，默认 15 分钟内同一邮箱 5 次、同一 IP 20 次，后台配置可调整 |
| 50000 | 500 | 服务器内部错误 |

---

### `POST /v1/user/password-reset`

使用找回密码验证码设置新密码。验证码仅可使用一次，且不能用于注册。

**鉴权**: 无

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| email | string | ✅ | 注册邮箱地址 |
| password | string | ✅ | 新密码，最少 6 位 |
| code | string | ✅ | 找回密码邮件中的 6 位验证码 |

**成功响应** — HTTP 200

```json
{ "code": 200, "data": null }
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体 JSON 解析失败或字段校验不通过 |
| 40013 | 400 | 验证码无效或已过期 |
| 429 | 429 | 同一注册邮箱尝试超过 5 次，或同一 IP 尝试超过 20 次；限制窗口为验证码有效期 |
| 50000 | 500 | 服务器内部错误 |

---

### 以下接口需鉴权

```
Authorization: Bearer <user_jwt>
```

JWT 由 register / login 返回的 `token` 提供，含 `user_id` claim。缺失或无效返回 **401**。

---

### `GET /v1/user/quota`

查询当前用户剩余设备配额。

**鉴权**: ✅

**无请求体**

**成功响应** — HTTP 200

```json
{ "code": 200, "data": { "quota": 8 } }
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | 未登录或 token 无效 |
| 50000 | 500 | 服务器内部错误 |

---

### `GET /v1/user/device/list`

获取当前用户已绑定设备列表（含在线状态）。

**鉴权**: ✅

**无请求体**

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": [
    {
      "device_id": "TIRZ00000001",
      "device_name": "客厅学习机",
      "status": 1,
      "mac": "AA:BB:CC:DD:EE:FF",
      "bind_time": "2026-06-18T12:00:00",
      "online": true,
      "up_video_mt": "h264",
      "down_video_mt": "mjpeg",
      "down_audio_mt": "amr",
      "audio_rate": 8000,
      "camera_rotation": 90,
      "aspect_ratio": 1.7777777778,
      "hor_mirror": true,
      "vert_mirror": false,
      "object_fit": "contain",
      "has_camera": true,
      "has_screen": true,
      "voip_room_type": "video"
    }
  ]
}
```

| 字段 | 说明 |
|------|------|
| device_id | 设备 ID |
| device_name | 用户设置的设备名称；新绑定默认空字符串，VoIP 授权前应先设置 |
| status | `1`=已绑定 |
| mac | 设备 MAC 地址 |
| bind_time | 绑定时间，格式 `YYYY-MM-DDTHH:MM:SS` |
| online | 设备是否在线 |
| up_video_mt | 设备上行视频编码（设备→小程序） |
| down_video_mt | 设备下行视频编码（小程序→设备） |
| down_audio_mt | 设备下行音频编码（小程序→设备） |
| audio_rate | 音频采样率，`8000` 或 `16000` |
| camera_rotation | 设备视频在微信通话 UI 中的顺时针旋转角度：`0`、`90`、`180` 或 `270` |
| aspect_ratio | 设备视频宽高比，例如 `1.7777777778`（即 `16/9`） |
| hor_mirror | 是否水平镜像设备视频 |
| vert_mirror | 是否垂直镜像设备视频 |
| object_fit | 设备视频缩放方式：`fill` 或 `contain`；未上报时由微信插件使用默认值 |
| has_camera | 是否具备摄像头能力 |
| has_screen | 是否具备带屏能力 |
| voip_room_type | 呼叫房间类型，`voice` 或 `video` |

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | 未登录或 token 无效 |
| 50000 | 500 | 服务器内部错误 |

---

### `PUT /v1/user/device/name`

修改当前用户已绑定设备的名称。名称用于 `wx.requestDeviceVoIP.deviceName` 和
`wmpfVoip.callDevice.deviceName`；最多 13 个 Unicode 字符。修改接口只保存当前名称，
不会修改微信已保存的授权名称。

**鉴权**: ✅

**请求体**

```json
{ "device_id": "TIRZ00000001", "device_name": "客厅学习机" }
```

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "device_id": "TIRZ00000001",
    "device_name": "客厅学习机"
  }
}
```

已完成微信授权后再改名时，微信仍显示授权时的名称。用户需在微信“最近使用”中删除
本小程序以清空授权记录，再重新进入并授权，新的名称才会生效。

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | JSON 解析失败、device_id 为空或名称超过 13 个字符 |
| 401 | 401 | 未登录或 token 无效 |
| 4040 | 404 | 设备不存在；部分用户接口为避免泄露归属信息，也用于设备不属于当前用户 |
| 50000 | 500 | 服务器内部错误 |

---

### `POST /v1/user/device/bind`

验证码绑定设备（用户输入设备 TTS 播报的 6 位码）。

**鉴权**: ✅

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <user_jwt>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| code | string | ✅ | 6 位验证码 |

**请求示例**

```json
{ "code": "386236" }
```

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": { "device_id": "TIRZ00000001", "msg": "bind success" }
}
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体 JSON 解析失败或 code 字段校验不通过 |
| 4002 | 400 | 验证码无效或已过期（含输错验证码、码不存在） |
| 4003 | 422 | 设备配额已用完 |
| 401 | 401 | 未登录或 token 无效 |
| 4040 | 404 | 预烧 device_id 不在 device_pool 中（扫码预烧绑定路径） |
| 5001 | 504 | MQTT 下发 auth_grant 超时 |
| 6002 | 503 | 设备离线（上报与扫码间临时 MQTT 连接已断） |
| 6004 | 409 | 设备已被其他用户绑定 |
| 6010 | 400 | mac 为空 |
| 6011 | 403 | MAC 不符，疑似克隆 |
| 6012 | 503 | 设备池已耗尽 |
| 6013 | 403 | 预烧路径上报 MAC 与已绑 MAC 不一致 |
| 6015 | 409 | 该 MAC 已绑定至本账号其它 device_id |
| 50000 | 500 | 服务器内部错误 |

---

### `POST /v1/user/device/bind-by-id`

按 device_id 直接绑定（无需验证码，device_id 须已存在于 `device_pool`）。

**鉴权**: ✅

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <user_jwt>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| device_id | string | ✅ | 设备 ID（须在 device_pool 中） |
| mac | string | 否 | 设备 MAC。带上才启用 MAC 一致性校验（`6013`）与同账号同 MAC 查重（`6015`）；省略则沿用该 device_id 已存指纹。公开绑定 UI 通常只提交 device_id |

**请求示例**

```json
{ "device_id": "TIRZ00000001" }
```

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": { "device_id": "TIRZ00000001", "msg": "bind success" }
}
```

> **在线证明**：当目标 device_id 尚无归属时，要求设备近期已通过签名 Report 证明持有对应 device_key（已建立临时 MQTT 连接），否则返回 6002。若设备已归属当前用户（重复绑定）或归属他人，不受此限制。

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体 JSON 解析失败或 device_id 为空 |
| 4003 | 422 | 设备配额已用完 |
| 401 | 401 | 未登录或 token 无效 |
| 4040 | 404 | device_id 不在 device_pool 中 |
| 6002 | 503 | 设备无主且未通过在线证明 |
| 6004 | 409 | 设备已被其他用户绑定 |
| 6011 | 403 | MAC 不符，疑似克隆 |
| 6013 | 403 | 上报 MAC 与已绑 MAC 不一致（仅当请求带 mac） |
| 6015 | 409 | 该 MAC 已绑定至本账号其它 device_id（仅当请求带 mac） |
| 50000 | 500 | 服务器内部错误 |

---

### `DELETE /v1/user/device/reset`

解绑设备并释放配额。若设备在线，推送 `unbind` 通知并踢除 MQTT 连接。解绑同时清空
`device_name`，并删除该设备的 VoIP 授权和 profile，避免下一个绑定用户继承上一用户的名称。

**鉴权**: ✅

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <user_jwt>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| device_id | string | ✅ | 要解绑的设备 ID |

**请求示例**

```json
{ "device_id": "TIRZ00000001" }
```

**成功响应** — HTTP 200

```json
{ "code": 200, "data": { "msg": "reset success" } }
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体 JSON 解析失败或 device_id 为空 |
| 401 | 401 | 未登录或 token 无效 |
| 4040 | 404 | 设备不存在或不属于当前用户 |
| 50000 | 500 | 服务器内部错误 |

---

### `GET /v1/user/device/rtc-token`

获取 TiRTC token（H5 直连 TiRTC 用）。

**鉴权**: ✅

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <user_jwt>` |

**查询参数**

| 参数 | 必填 | 说明 |
|------|:--:|------|
| device_id | ✅ | 设备 ID |

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "token": "v1.eyJ...",
    "app_id": "2818153",
    "endpoint": "https://api-tirtc.tange365.com",
    "in_call": false
  }
}
```

| 字段 | 说明 |
|------|------|
| token | TiRTC 连接 token，有效期 1 小时，scope=`connect:device://{device_id}` |
| app_id | TiRTC App ID |
| endpoint | TiRTC API 地址 |
| in_call | 设备当前是否在对讲中（`true` 时仍正常签发 token，由 H5 自行决定是否提示用户） |

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 缺少 device_id 参数 |
| 401 | 401 | 未登录或 token 无效 |
| 40300 | 403 | device_id 不存在或不属于当前用户 |
| 50000 | 500 | 服务器内部错误或 token 构建失败 |

---

## voip-server

处理微信 IoT VoIP 通话全流程。

> **响应格式约定**：`/voip/device/*`、`/voip/user/*` 和 `/voip/internal/*`
> 成功时返回 HTTP 200 + `code=0`，业务失败时返回 HTTP 200 + 非零业务码。
> 只有 JWT 中间件鉴权失败返回 HTTP 401 + `code=401`。其中 `40203` 表示微信登录
> 状态无效，`40205` 表示微信 VoIP 授权无效，`40300` 表示无权访问资源，
> `40301` 表示内部服务凭证无效，`6006` 表示设备已解绑。
> `/voip/notification/*` 是微信回调，响应字段使用 `errcode`，不使用 `code`。

### `GET /v1/voip/notification/:wx_app_id`

微信服务器 URL 验证回调。

**鉴权**: 微信签名校验

**成功响应** — HTTP 200

返回 `echostr` 参数值（纯文本）。

---

### `POST /v1/voip/notification/:wx_app_id`

微信服务器事件推送。处理 `iot_voip_notify` 事件。

**鉴权**: 微信签名校验 + AES 解密（若 `encrypt_type=aes`）

**流程**:
1. 校验签名
2. AES 解密（若加密）
3. 若 `action=join_voip_room`：调 TiRTC Token 服务获取 peer_id + token
4. 通过 MQTT 向设备推送 `call_incoming`

**成功响应** — HTTP 200

```json
{ "errcode": 0, "errmsg": "ok" }
```

**错误码**

| errcode | 含义 |
|---------|------|
| 0 | 成功 |
| 2 | 意外的微信消息类型（非 `iot_voip_notify`） |
| 3 | `wx_app_id` 对应的微信 App 未配置 |
| 4 | 意外的 action（非 `join_voip_room`） |
| 5 | 签名校验失败 |
| 9 | 请求体无效（空或解析/AES 解密失败） |
| 10 | 向设备推送 `call_incoming` 失败，或同一房间 10 分钟内重复回调仍在处理中 |

> **去重 / 重试**：以 `voip:notify:{wx_app_id}:{room_id}` 为键做 10 分钟去重。微信重试时，若上一笔仍在处理中 → `errcode 10`；若已完成 → `errcode 0`（幂等返回成功，不再建立第二次 TiRTC 会话、不再向设备推第二次 `call_incoming`）。

**推送设备消息格式** (topic: `device/sn_{id}/cmd`):

```json
{
  "type": "call_incoming",
  "channel": "wx",
  "payload": {
    "peer_id": "whips://wxvoip?x_wx_room_id=...",
    "token": "v1.eyJ...",
    "wx_app_id": "wxXXX",
    "wx_model_id": "HRHY_xxx",
    "wx_room_id": "wxf...",
    "wx_user_openid": "o4DLd5...",
    "wx_user_remark": "客厅联系人",
    "wx_server_token": "...",
    "wx_session_key": "...",
    "wx_call_id": "...",
    "wx_from": "...",
    "wx_room_type": "video",
    "wx_payload": "eyJpZCI6Ii4uLiJ9"
  }
}
```

| 字段 | 说明 |
|------|------|
| type | 消息类型，固定 `call_incoming` |
| channel | 通道，固定 `wx` |
| payload.peer_id | TiRTC WHIP 连接 URL |
| payload.token | TiRTC JWT token |
| payload.wx_app_id | 微信 AppID |
| payload.wx_model_id | VoIP 硬件型号 ID |
| payload.wx_room_id | 微信 VoIP 房间 ID |
| payload.wx_user_openid | 主叫用户 openid |
| payload.wx_user_remark | 当前设备联系人列表中该微信身份的统一备注名，未设置时为空 |
| payload.wx_server_token | 微信服务端 token（设备接听时回传） |
| payload.wx_session_key | 微信会话密钥 |
| payload.wx_payload | 微信原始 `Payload` 字符串（通常是 Base64 文本），始终携带；服务端不改写该值 |
| payload.wx_call_id | `Payload.id`（Payload 可解析时携带）；服务端自动生成 Payload 时等于 `/voip/device/call` 返回的 call_id |
| payload.wx_from | 主叫标识（Payload 可解析时携带） |
| payload.wx_room_type | 房间类型 `voice`/`video`（Payload 可解析时携带） |

---

### `POST /v1/voip/device/profile`

上报设备媒体能力（设备上线时调用，**必须在接受来电前调用**）。

**鉴权**: ✅ `Authorization: Bearer <mqtt_token>`（JWT 需含 `device_id` claim）

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <mqtt_token>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| screen_width | int | | 设备自身屏幕宽度，与视频素材分辨率无关（`no_video=true` 时传 1） |
| screen_height | int | | 设备自身屏幕高度，与视频素材分辨率无关（`no_video=true` 时传 1） |
| camera_rotation | int | | 设备视频在微信通话 UI 中的顺时针旋转角度：`0`、`90`、`180`、`270`；小程序默认 `0` |
| aspect_ratio | number | | 设备视频宽高比，必须大于 `0`，例如 `1.7777777778`（`16/9`）；小程序默认 `4/3` |
| hor_mirror | bool | | 是否水平镜像设备视频；小程序默认 `false` |
| vert_mirror | bool | | 是否垂直镜像设备视频；小程序默认 `false` |
| object_fit | string | | 设备视频缩放方式：`fill` 或 `contain`；小程序默认 `fill` |
| audio_rate | int | ✅ | 采样率：`8000` 或 `16000` |
| audio_channels | int | ✅ | 声道数：`1` 或 `2` |
| video_mt | string | | 兼容旧设备的上下行统一视频编码：`h264`、`mjpeg`、`none`；新设备使用方向字段 |
| up_video_mt | string | | 上行视频编码（设备→小程序）：`h264`、`h265`、`mjpeg`、`none` |
| down_video_mt | string | | 下行视频编码（小程序→设备）：`h264`、`mjpeg`、`none`（不支持 h265） |
| video_res_mode | string | | 微信下行视频分辨率适配：`auto`、`fit_screen`、`fill_screen`；省略等同 `auto` |
| down_audio_mt | string | | 下行音频编码（小程序→设备）：`alaw`、`amr`、`opus`，默认 `alaw` |
| no_video | bool | | 是否无视频能力 |
| calling_timeout_sec | int | | 呼叫超时秒数 |

**上报规则**

| 参数类别 | 开发者需要遵守的规则 |
|----------|----------------------|
| 请求格式 | 请求体必须是 JSON 对象，最大 **512 字节** |
| 上报时机 | 设备上线后、接听来电前完成上报；媒体能力或屏幕参数变化后重新上报 |
| TiRTC profile 参数 | 使用 TiRTC Server API 中的同名顶层字段；字段名、类型和组合由开发者保证 |
| 视频编码兼容 | 旧设备可继续使用 `video_mt`；同时上报方向字段时，以 `up_video_mt`、`down_video_mt` 为准，不向 TiRTC 发送 `video_mt` |
| 视频 UI 参数 | `camera_rotation`、`aspect_ratio`、`hor_mirror`、`vert_mirror`、`object_fit` 仅用于小程序通话页面，不作为 TiRTC 会话参数 |
| 会话身份参数 | 不要上报 `wx_session_key`、`wx_room_id`、`wx_session_token`、`wx_app_id`、`device_id`、`wx_payload`、`wx_model_id`；这些值按本次呼叫确定，profile 中的同名字段无效 |

`video_res_mode` 只影响小程序发送给设备的下行视频，不负责旋转画面：

| 取值 | 下行画面处理 | 使用要求 |
|------|-------------|----------|
| `auto` | 保持微信下行画面的原始尺寸，不缩放、不裁剪 | 无；省略字段时使用此模式 |
| `fit_screen` | 按比例缩小到屏幕范围内，不放大、不裁剪；输出宽高向下取偶数 | `down_video_mt=mjpeg`，并上报有效的屏幕宽高 |
| `fill_screen` | 按比例缩放并居中裁剪到屏幕尺寸，允许放大 | `down_video_mt=mjpeg`，并上报有效且为偶数的屏幕宽高 |

配置不符合要求时，VoIP 呼叫可能失败。完整约束见
[TiRTC Server API](https://docs.tange.ai/products/wxvoip/api-reference/server-api.html)。

五个视频 UI 字段均可省略。`callerUI` / `listenerUI` 的对应关系见
[小程序 VoIP 页面参数](weixin-mini-program/README.md#5-callerui-和-listenerui)。

本接口没有上行音频字段；设备以 `TiRtcSendAudioStream` 实际发送的帧格式为准。

**视频设备示例**（MJPEG 下行完整适配到 640 × 480 屏幕）

```json
{
  "screen_width": 640,
  "screen_height": 480,
  "camera_rotation": 90,
  "aspect_ratio": 1.7777777778,
  "hor_mirror": true,
  "vert_mirror": false,
  "object_fit": "contain",
  "audio_rate": 8000,
  "audio_channels": 1,
  "up_video_mt": "h264",
  "down_video_mt": "mjpeg",
  "video_res_mode": "fit_screen",
  "down_audio_mt": "amr",
  "no_video": false,
  "calling_timeout_sec": 30
}
```

**纯语音设备示例**

```json
{
  "screen_width": 1,
  "screen_height": 1,
  "audio_rate": 8000,
  "audio_channels": 1,
  "up_video_mt": "none",
  "down_video_mt": "none",
  "down_audio_mt": "alaw",
  "no_video": true,
  "calling_timeout_sec": 30
}
```

**成功响应** — HTTP 200

```json
{ "code": 0, "msg": "ok", "data": null }
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 鉴权失败 |
| 40000 | 200 | JSON 解析失败、请求体不是 JSON 对象、超过 512 字节，或视频 UI 字段类型/取值不合法 |
| 50000 | 200 | 数据库保存失败 |

---

### `GET /v1/voip/device/contacts`

设备查询有效授权的小程序 VoIP 联系人。此接口只返回 `voip_device_auth` 中
`auth_status=active` 的联系人，
不包含设备联系人；查询完整联系人列表使用 call-server 的
`GET /v1/call/device/contacts`。

**鉴权**: ✅ `Authorization: Bearer <mqtt_token>`（JWT 需含 `device_id` claim）

**成功响应** — HTTP 200

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "contacts": [
      {
        "wx_open_id": "o4DLd5...",
        "wx_app_id": "wxXXX",
        "wx_model_id": "HRHY_xxx",
        "remark": "小雨",
        "created_at": "2026-06-18T12:00:00Z"
      }
    ]
  }
}
```

`code`、`msg` 遵循 voip-server 的统一响应约定；`data` 字段如下：

| 字段 | 类型 | 说明 |
|------|------|------|
| data.contacts | object[] | 当前设备的有效 VoIP 联系人，按授权创建时间倒序排列；没有联系人时为 `[]` |
| data.contacts[].wx_open_id | string | 微信用户 OpenID；发起外呼时作为 `wx_user_openid` |
| data.contacts[].wx_app_id | string | 授权所属的微信小程序 AppID |
| data.contacts[].wx_model_id | string | 授权对应的微信设备型号 ID；发起外呼时服务端从授权记录读取 |
| data.contacts[].remark | string | 当前 `wx_open_id + wx_app_id` 的统一联系人名称；未设置时为空字符串 |
| data.contacts[].created_at | string | 该设备授权记录的创建时间，RFC 3339 格式 |

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 鉴权失败 |
| 50000 | 200 | 数据库查询失败 |

---

### `POST /v1/voip/device/call`

设备主动呼叫用户。

**鉴权**: ✅ `Authorization: Bearer <mqtt_token>`（JWT 需含 `device_id` claim）

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <mqtt_token>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| device_id | string | ✅ | 主叫设备 ID |
| wx_user_openid | string | ✅ | 被叫用户 openid |
| wx_room_type | string | ✅ | `voice` 或 `video` |
| wx_app_id | string | | 微信 AppID，不传则用默认；必须与有效授权记录一致 |
| wx_version_type | int | | 版本类型：`0`=正式版、`1`=开发版、`2`=体验版 |
| wx_listener_name | string | | 被叫方展示名称 |
| wx_query | string | | 自定义查询参数 |
| wx_caller_camera_status | int | | 主叫摄像头状态：`0`=开启、`1`=关闭 |
| wx_listener_camera_status | int | | 被叫摄像头状态：`0`=开启、`1`=关闭 |
| payload | string | | 自定义 payload |

**视频 UI 参数**

| 场景 | 行为 |
|------|------|
| profile 已上报视频 UI 字段 | 呼叫使用 profile 中的 `camera_rotation`、`aspect_ratio`、`hor_mirror`、`vert_mirror`、`object_fit` |
| `wx_query` 包含同名字段 | 以 profile 为准 |
| profile 未上报某个字段 | 不向 query 添加该字段，使用小程序插件默认值 |

调用方不需要在每次 `/device/call` 请求中重复传视频 UI 参数。
小程序视频 UI 使用 `query`，不读取 `payload` 或 `wxa_payload`。

**payload 行为**

| 请求方式 | 行为 |
|----------|------|
| 省略 `payload` | 接口生成包含 `id`、`from`、`to`、`room_type` 的 JSON；`id` 等于响应中的 `call_id` |
| 传入自定义 `payload` | 按原值透传；如需精确关联 MQTT 回铃，应自行包含 `id`、`from`、`to`、`room_type` |

`payload` / `wxa_payload` 仅用于加入房间和设备通知链路，微信和 TiRTC 不解析其业务内容。

**发起条件**

| 检查项 | 规则 |
|--------|------|
| 设备状态 | 设备必须仍处于绑定状态 |
| 联系人授权 | 授权必须为 `active`；`wx_model_id` 取自授权记录 |
| 小程序 | `wx_app_id` 用于选择小程序并匹配授权；省略时使用默认 AppID |
| 重复呼叫 | 同一设备或联系人 30 秒内不能重复发起；微信房间通知成功下发到在线设备后解除限制 |

**请求示例**

```json
{
  "device_id": "TIRZ00000001",
  "wx_user_openid": "o4DLd5...",
  "wx_room_type": "video"
}
```

**成功响应** — HTTP 200

```json
{ "code": 0, "msg": "ok", "data": { "call_id": "8d4bc1f..." } }
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 缺失、无效、过期或缺少 `device_id` claim |
| 40000 | 200 | JSON 解析失败、必填字段缺失或 wx_room_type 非法 |
| 40205 | 200 | 微信 VoIP 授权不存在或已失效 |
| 40900 | 200 | 同一设备或联系人短时间内重复发起 |
| 50001 | 200 | 微信 App 未配置 |
| 50002 | 200 | 微信 API 调用失败 |
| 50000 | 200 | 服务器内部错误（profile/授权查询、call_id 生成、Redis 预留等失败） |
| 6006 | 200 | 设备已解绑，需要重新完成设备绑定 |

微信主叫 API 返回错误码 `9` 时，服务端将授权标记为 `invalid`，通知设备刷新联系人，
并返回业务码 `40205`。该错误表示微信侧授权已不可用，不应直接等同于“用户主动取消”；
小程序需要重新检查微信授权状态并引导恢复。

---

### `POST /v1/voip/user/wechat-mini-login`

微信 code 换 openid。

**鉴权**: ✅ `Authorization: Bearer <user_jwt>`（JWT 需含 `user_id` claim）

成功后服务端会将当前用户、`wx_app_id` 与返回的 `wx_user_openid` 关联 24 小时，
供后续 `contact-remark` / `auth-list` / `report-auth` / `delete-auth` 校验；小程序在查询或上报授权前
应重新调用本接口。

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <user_jwt>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| code | string | ✅ | 微信登录 code |
| wx_app_id | string | | 微信 AppID，不传则用默认 |

**请求示例**

```json
{
  "code": "0b1a2b3c4d5e6f7g8h9i0j",
  "wx_app_id": "wxXXX"
}
```

**成功响应** — HTTP 200

```json
{ "code": 0, "msg": "ok", "data": { "wx_user_openid": "o4DLd5..." } }
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 鉴权失败 |
| 40000 | 200 | JSON 解析失败或 code 为空 |
| 50001 | 200 | 微信 App 未配置 |
| 50002 | 200 | 微信 API 调用失败 |
| 50000 | 200 | 服务器内部错误（如 24h 登录绑定写入失败） |

---

### `GET /v1/voip/user/contacts`

H5 查询指定设备的小程序 VoIP 联系人。此接口与设备接口分开鉴权，且只返回
`voip_device_auth` 中的联系人；查询完整联系人列表使用 call-server 的
`GET /v1/call/user/contacts?device_id=...`。

**鉴权**: ✅ `Authorization: Bearer <user_jwt>`（JWT 需含 `user_id` claim）

**查询参数**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| device_id | ✅ | 当前用户名下的设备 ID |

**成功响应** — HTTP 200

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "contacts": [
      {
        "wx_open_id": "o4DLd5...",
        "wx_app_id": "wxXXX",
        "wx_model_id": "HRHY_xxx",
        "remark": "小雨",
        "created_at": "2026-06-18T12:00:00Z"
      }
    ]
  }
}
```

`code`、`msg` 遵循 voip-server 的统一响应约定；`data.contacts` 的字段、排序和空列表行为与
`GET /v1/voip/device/contacts` 相同：

| 字段 | 类型 | 说明 |
|------|------|------|
| data.contacts | object[] | 指定设备的有效 VoIP 联系人，按授权创建时间倒序排列；没有联系人时为 `[]` |
| data.contacts[].wx_open_id | string | 微信用户 OpenID |
| data.contacts[].wx_app_id | string | 授权所属的微信小程序 AppID |
| data.contacts[].wx_model_id | string | 授权对应的微信设备型号 ID |
| data.contacts[].remark | string | 当前 `wx_open_id + wx_app_id` 的统一联系人名称；未设置时为空字符串 |
| data.contacts[].created_at | string | 该设备授权记录的创建时间，RFC 3339 格式 |

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 鉴权失败 |
| 40000 | 200 | 缺少 device_id |
| 40300 | 200 | 设备不属于当前用户 |
| 50000 | 200 | 数据库查询失败 |

---

### `GET /v1/voip/user/auth-list`

查询当前微信用户在当前账号名下设备上的 VoIP 授权记录。只读取统一联系人名称时使用
`GET /v1/voip/user/contact-remark`。

**鉴权**: ✅ `Authorization: Bearer <user_jwt>`（JWT 需含 `user_id` claim）

**查询参数**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| wx_app_id | | 微信 AppID，不传则使用默认 AppID |

服务端根据最近一次 `wechat-mini-login` 获取当前微信 OpenID，只返回同时满足以下条件的有效记录：

- 设备属于当前登录账号
- 授权记录属于当前微信 OpenID
- 授权记录属于指定小程序 AppID
- `auth_status=active`

**成功响应** — HTTP 200

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "list": [
      {
        "device_id": "TIRZ00000001",
        "remark": "小雨",
        "authorized_device_name": "客厅学习机",
        "auth_status": "active"
      }
    ]
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| data.list | object[] | 当前微信身份在当前账号名下设备上的有效授权记录；没有授权时为 `[]` |
| data.list[].device_id | string | 已授权的设备 ID |
| data.list[].remark | string | 当前 `wx_open_id + wx_app_id` 的统一联系人名称；未设置时为空字符串 |
| data.list[].authorized_device_name | string | 创建该微信授权时使用的设备名称；不是设备当前绑定名称 |
| data.list[].auth_status | string | 授权状态；本接口只返回有效记录，因此固定为 `active` |

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 鉴权失败 |
| 40203 | 200 | 尚未完成 `wechat-mini-login`，微信登录状态不存在或已过期 |
| 50000 | 200 | Redis 或数据库查询失败 |

---

### `GET /v1/voip/user/contact-remark`

查询当前小程序 OpenID 的统一联系人名称。小程序应先调用 `wechat-mini-login`；服务端
据此确定 OpenID，客户端不传 `wx_open_id`。

**鉴权**: ✅ `Authorization: Bearer <user_jwt>`

**查询参数**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| wx_app_id | | 微信 AppID，不传则使用默认 AppID |

**成功响应**

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "wx_open_id": "o4DLd5...",
    "remark": "小雨"
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| data.wx_open_id | string | 最近一次 `wechat-mini-login` 确定的当前微信用户 OpenID |
| data.remark | string | 当前 `wx_open_id + wx_app_id` 的统一联系人名称；尚未设置时为 `""` |

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 鉴权失败 |
| 40203 | 200 | 尚未完成 `wechat-mini-login`，微信登录状态不存在或已过期 |
| 50000 | 200 | Redis 或数据库查询失败 |

---

### `PUT /v1/voip/user/contact-remark`

修改当前小程序 OpenID 的统一联系人名称。该名称会同步到同一
`wx_open_id + wx_app_id` 的全部设备授权记录；它不是设备名称。设备端、H5 和小程序
均可修改，最后一次成功写入生效，并向所有受影响设备推送 `callers_update`。

**鉴权**: ✅ `Authorization: Bearer <user_jwt>`

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| wx_app_id | string | | 微信 AppID，不传则使用默认 AppID |
| remark | string | ✅ | 联系人名称，去除首尾空格后 1–64 个 Unicode 字符 |

```json
{ "wx_app_id": "wxXXX", "remark": "小雨" }
```

**成功响应**: `{ "code": 0, "msg": "ok" }`；响应不包含 `data` 字段。

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 200 | JSON 解析失败、remark 为空或超过 64 个字符 |
| 401 | 401 | JWT 鉴权失败 |
| 40203 | 200 | 尚未完成 `wechat-mini-login`，微信登录状态不存在或已过期 |
| 50000 | 200 | 数据库写入失败 |

---

### `POST /v1/voip/user/report-auth`

上报 VoIP 授权（用户在小程序完成 `wx.requestDeviceVoIP` 后调用）。成功后推送 `callers_update` 通知到设备。

**鉴权**: ✅ `Authorization: Bearer <user_jwt>`（JWT 需含 `user_id` claim）

目标设备必须属于当前用户，且 `wx_open_id` 必须与同一用户最近一次
`wechat-mini-login` 的结果一致。

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <user_jwt>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| device_id | string | ✅ | 设备 ID |
| wx_open_id | string | ✅ | 微信用户 openid |
| wx_app_id | string | | 微信 AppID，不传则用默认 |
| wx_model_id | string | | 型号 ID，不传则用 App 配置的默认 model_id |
| remark | string | | 当前 OpenID 的统一联系人名称，去除首尾空格后最多 64 个字符；非空值会同步到该 OpenID 的所有设备；未传或空值沿用已保存名称；不是设备名称 |
| device_name | string | `authorization_created=true` | 本次微信授权使用的设备名称，最多 13 个 Unicode 字符；必须与当前绑定名称一致 |
| authorization_created | bool | | 本次是否新建了微信授权；新授权为 `true`，微信已授权或状态恢复上报为 `false` |

`remark` 属于 `wx_open_id + wx_app_id`，不是某一台设备。小程序可通过本接口或
`PUT /v1/voip/user/contact-remark` 修改，设备端和 H5 也可通过 call-server 的联系人
备注接口修改；所有入口采用最后一次成功写入生效。本接口未传或传入空 `remark` 时
沿用已保存名称。

**请求示例**

```json
{
  "device_id": "TIRZ00000001",
  "wx_open_id": "o4DLd5...",
  "remark": "小雨",
  "device_name": "客厅学习机",
  "authorization_created": true
}
```

**成功响应** — HTTP 200

```json
{ "code": 0, "msg": "ok" }
```

成功响应不包含 `data` 字段。

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 鉴权失败 |
| 40000 | 200 | JSON/必填字段错误、名称超长，或新授权使用的设备名与当前绑定名称不一致 |
| 40203 | 200 | 尚未完成微信登录，或 wx_open_id 与当前微信登录不一致 |
| 40300 | 200 | 设备不属于当前用户 |
| 50000 | 200 | 数据库保存失败 |

---

### `POST /v1/voip/user/delete-auth`

删除授权。实际删除到授权记录时推送 `callers_update` 通知到设备；重复删除保持幂等，
不会重复推送。

**鉴权**: ✅ `Authorization: Bearer <user_jwt>`（JWT 需含 `user_id` claim）

目标设备必须属于当前用户，且 `wx_open_id` 必须与同一用户最近一次
`wechat-mini-login` 的结果一致。

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <user_jwt>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| device_id | string | ✅ | 设备 ID |
| wx_open_id | string | ✅ | 微信用户 openid |
| wx_app_id | string | | 微信 AppID，不传则用默认 |

**请求示例**

```json
{
  "device_id": "TIRZ00000001",
  "wx_open_id": "o4DLd5..."
}
```

**成功响应** — HTTP 200

```json
{ "code": 0, "msg": "ok" }
```

成功响应不包含 `data` 字段。

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 鉴权失败 |
| 40000 | 200 | JSON 解析失败或 device_id / wx_open_id 缺失 |
| 40203 | 200 | 尚未完成微信登录，或 wx_open_id 与当前微信登录不一致 |
| 40300 | 200 | 设备不属于当前用户 |
| 50000 | 200 | 数据库删除失败 |

---

### `POST /v1/voip/user/sn-ticket`

获取 SN ticket。`device_id` 必须属于当前用户。响应中的 `device_name` 为设备绑定名称；
未设置名称时返回 `device_id`。

**鉴权**: ✅ `Authorization: Bearer <user_jwt>`（JWT 需含 `user_id` claim）

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <user_jwt>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| device_id | string | ✅ | 设备 ID |
| wx_app_id | string | | 微信 AppID，不传则用默认 |

**请求示例**

```json
{
  "device_id": "TIRZ00000001"
}
```

**成功响应** — HTTP 200

```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "sn_ticket": "xxx",
    "device_name": "客厅学习机"
  }
}
```

小程序必须将响应中的 `device_name` 原样传给
`wx.requestDeviceVoIP.deviceName`，并在 `report-auth` 中同步上报，避免页面缓存名称与
服务端当前名称不一致。

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 鉴权失败 |
| 40000 | 200 | JSON 解析失败或 device_id 为空 |
| 40300 | 200 | 设备不属于当前用户 |
| 50001 | 200 | 微信 App 未配置或缺少 model_id |
| 50002 | 200 | 微信 API 调用失败 |
| 50000 | 200 | 服务器内部错误（设备归属校验或名称查询失败） |

---

### `POST /v1/voip/user/cancel`

取消呼叫（小程序挂断后调用）。推送 `call_cancel` 通知到设备。`device_id` 必须属于当前用户。

**鉴权**: ✅ `Authorization: Bearer <user_jwt>`（JWT 需含 `user_id` claim）

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <user_jwt>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| device_id | string | ✅ | 设备 ID |
| wx_room_id | string | | 微信房间 ID |

**请求示例**

```json
{
  "device_id": "TIRZ00000001",
  "wx_room_id": "wxf..."
}
```

**成功响应** — HTTP 200

```json
{ "code": 0, "msg": "ok", "data": null }
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 鉴权失败 |
| 40000 | 200 | JSON 解析失败或 device_id 为空 |
| 40300 | 200 | 设备不属于当前用户 |
| 50000 | 200 | MQTT 推送失败 |

---

### `POST /v1/voip/internal/unbind`

服务间调用：设备解绑后清空设备名称，删除该设备的 VoIP profile 和全部授权记录，
清理未完成的外呼防重状态，并通知设备刷新联系人。

**鉴权**：`X-Internal-Key` 请求头，值需匹配服务端配置的内部调用密钥。

**请求体**：`{ "device_id": "TIRZ00000001" }`

**成功响应**：`{ "code": 0, "msg": "ok" }`

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 200 | JSON 解析失败或缺少 device_id |
| 40301 | 200 | 内部服务凭证未配置或不匹配 |
| 50000 | 200 | 数据库事务或清理操作失败 |

---

## ai-server

面向 IoT 硬件设备和 H5 管理端，提供 AI 语音对话连接凭证与智能体配置。

H5 智能体管理页面为 `GET /v1/ai/agent?device_id=xxx`。该路径返回 HTML，
不是 JSON API；同源部署时需将 `/v1/ai/*` 代理到 ai-server，参见
[`thing-connect.nginx.conf`](deploy/nginx/thing-connect.nginx.conf)。

### `GET /v1/ai/token`

获取 AI 连接凭证。

**鉴权**: ✅ `Authorization: Bearer <mqtt_token>`（JWT 需含 `device_id` claim）

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <mqtt_token>`，由 device-server `/v1/device/token` 签发 |

**无请求体 / 查询参数**

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "msg": "ok",
  "data": {
    "peer_id": "whips://ai?x_role_id=fin63bby1og0&...",
    "token": "v1.eyJ...",
    "role_id": "fin63bby1og0"
  }
}
```

| 字段 | 说明 |
|------|------|
| peer_id | WHIP 连接 URL，含 AI 角色信息 |
| token | TiRTC JWT token，用于建立 WHIP 连接 |
| role_id | 当前使用的角色 ID（优先设备绑定角色，否则 default_role_id） |

> 已配置数据库时，ai-server 查询 `ai_device_role` 表获取设备绑定的角色；未绑定则使用 `default_role_id`。未配置数据库时所有设备使用 `default_role_id`。

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | 缺少 Authorization 头、JWT 无效或缺少 device_id claim |
| -1 | 200 | 上游探鸽云 API 网络/解析错误 |
| 上游透传 | 200 | 探鸽云返回的非零业务码直接透传给客户端 |
| 50000 | 500 | 服务器内部错误 |

### AI 角色管理

> 以下接口需要配置 `tirtc_aichat` 段。代理探鸽云 `/ai/aigcrtc/roles` CRUD，本地维护 `ai_user_role` 索引和 `ai_device_role` 绑定。所有接口需鉴权。

#### `GET /v1/ai/roles`

列出当前用户创建的角色。

**鉴权**: ✅

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "items": [
      {
        "id": "finxxxxxxxx",
        "name": "我的助手"
      }
    ],
    "total": 3
  }
}
```

#### `GET /v1/ai/roles/default`

获取全局默认角色详情。

**鉴权**: ✅

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "id": "fin63bby1og0",
    "name": "默认助手",
    "agent_config": {
      "prompt": "你是一个智能助手",
      "welcome_text": "你好！",
      "ali_rag": null
    }
  }
}
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | 未登录或 token 无效 |
| 40400 | 404 | 未配置 default_role_id，或 AI 云服务中不存在该默认角色 |
| 50200 | 502 | AI 云服务暂不可用 |

#### `POST /v1/ai/roles`

创建角色（代理至探鸽云，成功后记录到本地 `ai_user_role`）。

**鉴权**: ✅

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <user_jwt>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| name | string | ✅ | 角色名称 |
| agent_config | object | | 角色配置（prompt、welcome_text 等） |

**请求示例**

```json
{
  "name": "我的助手",
  "agent_config": { "prompt": "你是一个智能助手", "welcome_text": "你好！" }
}
```

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "id": "finxxxxxxxx",
    "name": "我的助手",
    "agent_config": {
      "prompt": "你是一个智能助手",
      "welcome_text": "你好！",
      "ali_rag": null
    }
  }
}
```

#### `GET /v1/ai/roles/:id`

查看角色详情。

**鉴权**: ✅

#### `PUT /v1/ai/roles/:id`

更新角色。

**鉴权**: ✅

**请求体**: 同 `POST /v1/ai/roles`

#### `DELETE /v1/ai/roles/:id`

删除角色（云端 + 本地 `ai_user_role` 同步删除）。

**鉴权**: ✅

#### 角色 CRUD 通用错误码

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体解析失败或必填字段缺失 |
| 401 | 401 | 未登录或 token 无效 |
| 40300 | 403 | 角色不属于当前用户（无权操作） |
| 50200 | 502 | AI 服务暂不可用（探鸽云 API 调用失败） |
| 50000 | 500 | 服务器内部错误 |

---

#### `GET /v1/ai/device/:device_id/role`

查询设备的角色绑定。

**鉴权**: ✅

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": { "default_role_id": "fin63bby1og0", "role_id": "finxxxxxxxx" }
}
```

> 设备未绑定时 `role_id` 为空字符串。

#### `PUT /v1/ai/device/:device_id/role`

绑定设备到角色。

**鉴权**: ✅

**请求头**

| 字段 | 必填 | 说明 |
|------|:--:|------|
| Authorization | ✅ | `Bearer <user_jwt>` |
| Content-Type | ✅ | `application/json` |

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| role_id | string | ✅ | 角色 ID |

**请求示例**

```json
{ "role_id": "finxxxxxxxx" }
```

**成功响应** — HTTP 200

```json
{ "code": 200, "data": null }
```

#### `DELETE /v1/ai/device/:device_id/role`

解除设备角色绑定。

**鉴权**: ✅

**成功响应** — HTTP 200

```json
{ "code": 200, "data": null }
```

#### 设备角色绑定通用错误码

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体解析失败或缺少 role_id |
| 401 | 401 | 未登录或 token 无效 |
| 40300 | 403 | 设备或角色不属于当前用户 |
| 50000 | 500 | 服务器内部错误 |

---

#### 设备角色批量操作（V2 代理）

> 以下接口代理至探鸽云 `/v2/ai/device-roles` 进行批量设备-角色绑定管理。所有接口需鉴权。

##### `POST /v1/ai/device-roles`

批量创建设备-角色绑定。

**鉴权**: ✅

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| device_ids | string[] | ✅ | 设备 ID 列表 |
| role_id | string | ✅ | 角色 ID |

**请求示例**

```json
{
  "device_ids": ["TIRZ00000001", "TIRZ00000002"],
  "role_id": "finxxxxxxxx"
}
```

**成功响应** — HTTP 200

```json
{ "code": 200, "data": null }
```

##### `POST /v1/ai/device-roles/query`

批量查询设备-角色绑定。

**鉴权**: ✅

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| device_ids | string[] | ✅ | 设备 ID 列表 |

**请求示例**

```json
{ "device_ids": ["TIRZ00000001", "TIRZ00000002"] }
```

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "items": [
      { "device_id": "TIRZ00000001", "role_id": "finxxxxxxxx", "created_at": 1720000000, "updated_at": 1720000000 }
    ]
  }
}
```

##### `DELETE /v1/ai/device-roles`

批量删除设备-角色绑定。

**鉴权**: ✅

**请求体**: 同 `POST /v1/ai/device-roles`（`role_id` 可不传，按 `device_ids` 删除所有绑定）

**成功响应** — HTTP 200

```json
{ "code": 200, "data": null }
```

**批量操作通用错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体解析失败或必填字段缺失 |
| 401 | 401 | 未登录或 token 无效 |
| 40300 | 403 | 设备或角色不属于当前用户 |
| 50200 | 502 | AI 服务暂不可用（探鸽云 API 调用失败） |
| 50000 | 500 | 服务器内部错误 |

---

#### TTS 音色

##### `GET /v1/ai/voices?language=zh-CN`

获取可用 TTS 音色列表。

**鉴权**: ✅

**查询参数**

| 参数 | 必填 | 说明 |
|------|:--:|------|
| language | | 语言过滤（如 `zh-CN`），空则返回全部 |

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "items": [
      { "id": "voice_001", "name": "小云", "languages": ["zh-CN"], "model": "cosyvoice", "scene": "default", "sample_url": "https://..." }
    ]
  }
}
```

---

#### MCP 工具（全局）

##### `GET /v1/ai/mcp/tools`

列出内置全局 MCP 工具。

**鉴权**: ✅

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "items": [
      { "id": "tool_001", "name": "web_search", "description": "搜索互联网" }
    ]
  }
}
```

##### `GET /v1/ai/mcp/tools/:id`

查看单个全局 MCP 工具详情。

**鉴权**: ✅

**成功响应** — HTTP 200

```json
{ "code": 200, "data": { "id": "tool_001", "name": "web_search", "description": "搜索互联网" } }
```

---

#### MCP 工具（应用级）

> 应用级 MCP 工具在全局工具基础上添加自定义配置（URL、认证等）。所有接口需鉴权。

##### `GET /v1/ai/mcp/app-tools`

列出当前用户创建的应用级 MCP 工具，以及配置为全局默认的 MCP 工具。列表从本地索引
读取，只返回轻量引用；完整配置需调用单项详情接口。

**鉴权**: ✅

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "items": [
      { "id": "app_tool_001", "name": "my_tool" }
    ]
  }
}
```

##### `POST /v1/ai/mcp/app-tools`

创建应用级 MCP 工具。

**鉴权**: ✅

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| config | object | ✅ | 工具配置 |
| config.name | string | ✅ | 工具名称 |
| config.url | string | ✅ | MCP 服务 URL |
| config.description | string | | 工具描述 |
| config.type | string | | 传输协议：`sse` 或 `streamableHttp`，默认 `sse` |
| config.authentication | object | | 认证配置，`type` 仅支持 `BearerToken` |
| enabled | bool | | 是否启用，默认 `true` |

**请求示例**

```json
{
  "config": {
    "name": "my_tool",
    "url": "https://mcp.example.com/sse",
    "description": "自定义工具",
    "type": "sse"
  },
  "enabled": true
}
```

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "id": "app_tool_001",
    "app_id": "2818153",
    "enabled": true,
    "config": {
      "name": "my_tool",
      "url": "https://mcp.example.com/sse",
      "description": "自定义工具",
      "type": "sse"
    }
  }
}
```

##### `GET /v1/ai/mcp/app-tools/:id`

查看单个应用级 MCP 工具。

**鉴权**: ✅

##### `PUT /v1/ai/mcp/app-tools/:id`

更新应用级 MCP 工具。

**鉴权**: ✅

**请求体**: `config` 和 `enabled` 均可选，传什么更新什么。

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "id": "app_tool_001",
    "enabled": false,
    "config": {
      "name": "my_tool",
      "url": "https://mcp.example.com/sse",
      "description": "自定义工具",
      "type": "sse"
    }
  }
}
```

##### `DELETE /v1/ai/mcp/app-tools/:id`

删除应用级 MCP 工具。

**鉴权**: ✅

**MCP 工具通用错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体解析失败或必填字段缺失 |
| 401 | 401 | 未登录或 token 无效 |
| 40300 | 403 | 工具不属于当前用户（无权操作） |
| 40400 | 404 | 工具不存在 |
| 42900 | 429 | 工具创建额度已用尽（受 `tirtc_aichat.resource_quota.mcp` 限制） |
| 50200 | 502 | AI 服务暂不可用 |
| 50000 | 500 | 服务器内部错误 |

---

#### 设备插件

> 探鸽云设备插件 CRUD 代理。所有接口需鉴权。

##### `GET /v1/ai/plugins`

列出当前用户创建的设备插件，以及配置为全局默认的设备插件。列表从本地索引读取，
只返回轻量引用；完整配置需调用单项详情接口。

**鉴权**: ✅

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "items": [
      { "id": "plg_001", "name": "开灯" }
    ]
  }
}
```

##### `POST /v1/ai/plugins`

创建设备插件。

**鉴权**: ✅

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| name | string | ✅ | 插件名称 |
| action | string | ✅ | 设备端指令标识，仅允许 `[a-zA-Z0-9_]`，长度 1-64 |
| description | string | | 描述 |
| input_params | object[] | | 输入参数定义。子字段：`name`(string), `type`(string: `string`/`integer`/`boolean`/`array`/`object`), `description`(string), `required`(bool), `enum`(string[]), `default_value`(string) |
| return_params | object[] | | 返回值定义，格式同 `input_params` |

**请求示例**

```json
{
  "name": "开灯",
  "action": "turn_on",
  "description": "打开设备灯"
}
```

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "id": "plg_001",
    "name": "开灯",
    "action": "turn_on",
    "description": "打开设备灯"
  }
}
```

##### `GET /v1/ai/plugins/:id`

查看单个设备插件。

**鉴权**: ✅

##### `PUT /v1/ai/plugins/:id`

更新设备插件。

**鉴权**: ✅

**请求体**: 同 `POST /v1/ai/plugins`（`name` 和 `action` 必填）

##### `DELETE /v1/ai/plugins/:id`

删除设备插件。

**鉴权**: ✅

**设备插件通用错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体解析失败或 name/action 缺失 |
| 401 | 401 | 未登录或 token 无效 |
| 40300 | 403 | 插件不属于当前用户（无权操作） |
| 40400 | 404 | 插件不存在 |
| 42900 | 429 | 插件创建额度已用尽（受 `tirtc_aichat.resource_quota.device_plugin` 限制） |
| 50200 | 502 | AI 服务暂不可用 |
| 50000 | 500 | 服务器内部错误 |

---

#### 知识库管理

> 探鸽云知识库 CRUD 代理。支持索引管理、文档列表和文件管理。所有接口需鉴权。

##### `GET /v1/ai/knowledge/indexes`

列出当前用户创建的知识库索引，以及配置为全局默认的知识库索引。

**鉴权**: ✅

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "items": [
      { "id": "idx_001", "name": "产品手册" }
    ],
    "total": 5
  }
}
```

> 列表从本地索引读取，仅返回 `{id, name}` 引用；`total` 等于 `items` 数量，包含当前
> 用户资源和配置的默认资源。索引完整信息见 `GET /v1/ai/knowledge/indexes/:id`；
> 索引下文档见 `GET /v1/ai/knowledge/indexes/:id/documents`。

##### `POST /v1/ai/knowledge/indexes`

创建知识库索引。

**鉴权**: ✅

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| name | string | ✅ | 索引名称 |
| description | string | ✅ | 索引描述 |

**请求示例**

```json
{
  "name": "产品手册",
  "description": "产品知识库"
}
```

**成功响应** — HTTP 200

```json
{ "code": 200, "data": { "index_id": "idx_001", "name": "产品手册", "description": "产品知识库", "document_ids": [] } }
```

##### `GET /v1/ai/knowledge/indexes/:id`

查看单个知识库索引。

**鉴权**: ✅

##### `PUT /v1/ai/knowledge/indexes/:id`

更新知识库索引。

**鉴权**: ✅

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| name | string | | 索引名称 |
| description | string | | 索引描述 |
| document_ids | string[] | | 关联文档 ID 列表 |

**请求示例**

```json
{ "name": "产品手册 v2", "document_ids": ["doc_001", "doc_003"] }
```

##### `DELETE /v1/ai/knowledge/indexes/:id`

删除知识库索引。

**鉴权**: ✅

##### `GET /v1/ai/knowledge/indexes/:id/documents?page=1&page_size=20`

分页列出索引下的文档。当前用户必须拥有该索引，配置的默认知识库也允许读取。

**鉴权**: ✅

非当前用户资源返回 HTTP 403 + `code=40300`，并且不会向上游 AI 服务查询。

**查询参数**

| 参数 | 必填 | 默认值 | 说明 |
|------|:--:|:--:|------|
| page | | 1 | 页码；非正整数按默认值处理 |
| page_size | | 20 | 每页条数，范围 1–100；非法值按默认值处理 |

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "items": [
      { "document_id": "doc_001", "name": "快速入门.pdf", "status": "ready", "size": 102400, "modified_at": 1720000000 }
    ],
    "total": 2
  }
}
```

##### `GET /v1/ai/knowledge/files`

列出当前用户已上传的知识库文件。服务端按本地所有权记录过滤上游结果，
不会返回其他用户上传的文件。

**鉴权**: ✅

**成功响应** — HTTP 200

```json
{
  "code": 200,
  "data": {
    "items": [
      { "file_id": "file_001", "file_name": "manual.pdf", "file_type": "pdf", "status": "done", "size_in_bytes": 204800, "create_time": "2026-01-01T00:00:00Z" }
    ]
  }
}
```

##### `POST /v1/ai/knowledge/files`

上传知识库文件。上传成功后，文件 ID 会记录为当前用户资源，供后续列表
过滤和删除鉴权使用。

**鉴权**: ✅

**Content-Type**: `multipart/form-data`

| 表单字段 | 类型 | 必填 | 说明 |
|----------|------|:--:|------|
| file | file | ✅ | 要上传的文件；服务端保留原始文件名并转发给上游 AI 服务 |

**请求示例**

```bash
curl -X POST "$AI_SERVER/v1/ai/knowledge/files" \
  -H "Authorization: Bearer $USER_JWT" \
  -F "file=@manual.pdf"
```

**成功响应** — HTTP 200

```json
{ "code": 200, "msg": "ok", "data": { "file_id": "file_001" } }
```

**错误码**: `40000`（缺少 file 或读取失败）、`401`（鉴权失败）、`50200`（上游 AI 服务不可用）

##### `DELETE /v1/ai/knowledge/files/:id`

删除当前用户拥有的知识库文件。文件 ID 不属于当前用户时返回 HTTP 403 +
`code=40300`，且不会调用上游删除接口。

**鉴权**: ✅

**知识库错误码**

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | 请求体解析失败或必填字段缺失 |
| 401 | 401 | 未登录或 token 无效 |
| 40300 | 403 | 索引或知识文件不属于当前用户，或无权访问该资源 |
| 40400 | 404 | 单项索引不存在 |
| 42900 | 429 | 创建索引额度已用尽（受 `tirtc_aichat.resource_quota.kb` 限制） |
| 50200 | 502 | AI 服务暂不可用 |
| 50000 | 500 | 服务器内部错误 |

---

### `POST /v1/ai/internal/unbind`

服务间调用：设备解绑后删除本地设备角色绑定，并清理 AI 云服务中的设备角色绑定。

**鉴权**：`X-Internal-Key` 请求头，值需匹配服务端配置的内部调用密钥。

**请求体**：`{ "device_id": "TIRZ00000001" }`

**成功响应**：`{ "code": 200, "msg": "ok" }`

| code | HTTP | 含义 |
|------|------|------|
| 40000 | 400 | JSON 解析失败或缺少 device_id |
| 40301 | 403 | 内部服务凭证未配置或不匹配 |
| 50200 | 502 | AI 云服务解绑失败 |
| 50000 | 500 | 本地设备角色绑定清理失败 |

---

## call-server

面向 IoT 硬件设备（+ H5 联系人管理），实现设备间音视频通话。

> **响应格式约定：**
>
> - 成功时返回 HTTP 200 + `code=200`。
> - 业务失败时返回 HTTP 200 + 非 200 业务码。
> - JWT 中间件鉴权失败返回 HTTP 401 + `code=401`。
> - JWT 鉴权后的资源权限错误使用 `40300`，内部服务凭证错误使用 `40301`。
> - 所有端点都可能返回 HTTP 200 + `code=50000`（服务器内部错误），下列端点不再重复列出该通用错误。
>
> **跨域**: call-server 不加 CORS。H5 联系人页面通过 nginx 反向代理跟 user-server 统一到同一个域名下（见 [`thing-connect.nginx.conf`](deploy/nginx/thing-connect.nginx.conf)：`/v1/call/*` 转发到 call-server，其余转发到 user-server），浏览器全程同源。

### `POST /v1/call/request`

发起呼叫（一对多）。

**鉴权**: ✅ `Authorization: Bearer <mqtt_token>`（设备 JWT，含 `device_id` claim）

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| targets | string[] | ✅ | 被叫设备 ID 数组 |
| call_type | string | ✅ | `audio` 或 `video` |

**请求示例**

```json
{
  "targets": ["TIRZ00000002", "TIRZ00000003"],
  "call_type": "video"
}
```

**成功响应** — HTTP 200

```json
{ "code": 200, "msg": "ok", "data": {
  "room_id": "d_roomid_c5b745c0bf61494e84a8432b199a693e",
  "online":  {"TIRZ00000002": true, "TIRZ00000003": false},
  "offline": ["TIRZ00000003"]
}}
```

| 字段 | 说明 |
|------|------|
| room_id | 房间 ID，格式 `d_roomid_` + 32 位十六进制（UUID v4 去横线） |
| online | 目标设备在线状态映射，`true`=在线、`false`=离线 |
| offline | 建房间时已离线的设备 ID 列表（直接计入 rejected_by） |

> `room_id` 前缀 `d_` 与微信 VoIP 的 `wx_room_id` 区分，避免跨系统排查时混淆。

**错误码**

| code | 含义 |
|------|------|
| 40000 | targets 为空或 call_type 不是 audio/video，或呼叫了自己 |
| 40205 | 存在非"已接受"联系人的 target（整单失败，不做部分过滤） |
| 40201 | 所有 target 均离线 |
| 40202 | 主叫已在其他房间中（`data.room_id` 为已有房间，需先 `/v1/call/hangup` 或 `/v1/call/cancel` 才能重呼） |

> 建房间时离线的 target 直接计入 `rejected_by`（它们收不到 `call_incoming`，永远不会主动拒接）。

---

### `POST /v1/call/device/info`

接听来电（`purpose=call`）。该操作会执行 SETNX/DEL 并发送 MQTT 通知，因此使用 POST。

**鉴权**: ✅ `Authorization: Bearer <mqtt_token>`（设备 JWT）

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| device_id | string | ✅ | 主叫设备 ID（从 `call_incoming` 中获取） |
| room_id | string | ✅ | 房间 ID（从 `call_incoming` 中获取） |
| purpose | string | ✅ | 必须是 `"call"`（call-server 不实现 `live_preview`） |

**请求示例**

```json
{
  "device_id": "TIRZ00000001",
  "room_id": "d_roomid_c5b745c0bf61494e84a8432b199a693e",
  "purpose": "call"
}
```

**成功响应** — HTTP 200

```json
{ "code": 200, "msg": "ok", "data": { "token": "v1.eyJ...", "device_id": "TIRZ00000001" } }
```

**错误码**

| code | HTTP | 含义 |
|------|------|------|
| 401 | 401 | JWT 鉴权失败 |
| 40000 | 200 | 缺少 device_id/room_id，或 purpose 不是 call |
| 40210 | 200 | 房间已被其他设备抢接 |
| 40300 | 200 | 调用者不是该房间的合法 caller/target |
| 40400 | 200 | 房间不存在（已取消或已过期） |

> 接听者如果已锁定在另一个房间，服务端会先释放原房间，并通过 `room_cancel{reason:"caller_left"}` 通知原房间对方，然后接听新来电。该流程没有“忙”分支；设备不想切换房间时，应调用 `/v1/call/reject` 拒接。

---

### `POST /v1/call/reject`

拒接来电。

**鉴权**: ✅ 设备 JWT，且必须是该房间的 target 之一

**请求体**: `{ "room_id": "xxx", "reason": "busy" }`（`reason` 建议值 `busy` | `decline`，默认 `decline`；服务端不校验，原样透传给对端）

**成功响应**: `{ "code": 200, "msg": "ok" }`

**错误码**: `401`（JWT 鉴权失败，HTTP 401）、`40000`（缺 room_id）、`40300`（不是该房间 target，HTTP 200）、`40400`（房间不存在）

> 所有 target（含离线预拒接的）都拒接后，房间解散，主叫收到 `room_cancel{reason:"all_rejected"}`。

---

### `POST /v1/call/hangup`

挂断，释放房间。**鉴权**: ✅ 设备 JWT，且必须是 caller 或 answered_by。

**请求体**: `{ "room_id": "xxx", "reason": "hangup" }`（`reason` 建议值 `hangup` | `p2p_error`，默认 `hangup`；服务端不校验，原样透传给对端）

**成功响应**: `{ "code": 200, "msg": "ok" }`　**错误码**: `401`（JWT 鉴权失败，HTTP 401）、`40000`、`40300`（不是 caller/answered_by，HTTP 200）、`40400`

---

### `POST /v1/call/cancel`

主叫取消呼叫（仅 caller 可调用）。**鉴权**: ✅ 设备 JWT。

**请求体**: `{ "room_id": "xxx" }`　**成功响应**: `{ "code": 200, "msg": "ok" }`　**错误码**: `401`（JWT 鉴权失败，HTTP 401）、`40000`、`40300`（非 caller，HTTP 200）、`40400`

---

### `GET /v1/call/room`

查询当前设备所在房间（崩溃恢复用：进程重启后本地状态丢失，可用此接口把服务端房间状态同步回来）。

**鉴权**: ✅ 设备 JWT

**成功响应** — 不在任何房间时 `data` 为 `null`

```json
{ "code": 200, "msg": "ok", "data": {
  "room_id": "d_roomid_c5b745c0bf61494e84a8432b199a693e",
  "status": "answered",
  "caller": "TIRZ00000001",
  "call_type": "video",
  "role": "callee"
}}
```

| 字段 | 说明 |
|------|------|
| room_id | 房间 ID，格式 `d_roomid_` + 32 位十六进制 |
| status | `active`=呼叫中（未接听）、`answered`=已接听 |
| caller | 主叫设备 ID |
| call_type | `audio` 或 `video` |
| role | 当前设备在该房间中的角色：`caller` 或 `callee` |

---

### `GET /v1/call/device/contacts`

设备侧联系人列表，同时返回**设备联系人**（`call_contact` 表）和 **VoIP 联系人**（`voip_device_auth` 表，微信小程序授权用户）。用 `type` 字段区分。

**鉴权**: ✅ 设备 JWT。

**成功响应**

```json
{ "code": 200, "msg": "ok", "data": { "contacts": [
  {"device_id": "TIRZ00000002", "type": "device", "remark": "门铃", "source": "manual", "online": true},
  {"id": 3, "device_id": "o4DLd5...", "type": "voip", "source": "voip", "remark": "妈妈",
   "wx_open_id": "o4DLd5...", "wx_app_id": "wxXXX", "wx_model_id": "HRHY_xxx"}
]}}
```

`code`、`msg` 遵循 call-server 的统一响应约定；`data` 字段如下。标记为“仅 device”或
“仅 voip”的字段在另一种联系人对象中不返回，而不是返回 `null`。

| 字段 | 适用 type | 说明 |
|------|:--:|------|
| data.contacts | — | 联系人对象数组；没有联系人时为 `[]` |
| data.contacts[].device_id | 全部 | device 联系人为对方设备 ID；voip 联系人为 `wx_open_id`（无独立设备身份） |
| data.contacts[].type | 全部 | 联系人类型：`device`（设备联系人）或 `voip`（微信授权联系人）；设备必须按此字段选择呼叫接口 |
| data.contacts[].id | 仅 voip | `voip_device_auth` 表主键；`PUT /v1/call/device/contacts/remark` 不使用它，而是使用 `peer_id` |
| data.contacts[].remark | 全部 | device 为本设备对该联系人的备注；voip 为当前 `wx_open_id + wx_app_id` 的统一联系人名称；未设置时为空字符串 |
| data.contacts[].source | 全部 | 联系人来源：`manual`（跨账号申请）、`auto`（同账号自动关联）或 `voip`（小程序授权） |
| data.contacts[].online | 仅 device | 对方设备当前是否在线 |
| data.contacts[].wx_open_id | 仅 voip | 微信用户 OpenID；与该项的 `device_id` 相同 |
| data.contacts[].wx_app_id | 仅 voip | 授权所属的微信小程序 AppID |
| data.contacts[].wx_model_id | 仅 voip | 授权对应的微信设备型号 ID；发起 VoIP 外呼时由 voip-server 从授权记录读取 |

> **设备联系人**：同账号下的其他设备会在首次拉取时懒创建为 `source:"auto"` 的已接受联系人（无需事件驱动）。跨账号需走 `request`/`respond` 申请流程。
>
> **VoIP 联系人**：只要用户在小程序完成授权（`voip-server` 的 `POST /v1/voip/user/report-auth`）即出现在列表里，无需设备侧申请/审批。设备呼叫时根据 `type` 选择走 `POST /v1/call/request`（device）还是 `POST /v1/voip/device/call`（voip），两条呼叫链路完全独立。

---

### `GET /v1/call/device/contacts/pending`

查询当前设备可以审批的联系人申请。只返回当前设备是非发起方的 pending 请求。

**鉴权**: ✅ 设备 JWT

**成功响应**:

```json
{ "code": 200, "msg": "ok", "data": { "pending": [
  { "type": "device", "peer_device_id": "TIRZ00000001", "created_at": "2026-07-22T14:00:00+08:00" }
]}}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| data.pending | object[] | 当前设备可审批的联系人申请；没有待审批申请时为 `[]` |
| data.pending[].type | string | 联系人类型；申请流程只适用于设备联系人，因此固定为 `device` |
| data.pending[].peer_device_id | string | 发起申请的对方设备 ID；审批时作为 `peer_device_id` |
| data.pending[].created_at | string | 申请创建时间，RFC 3339 格式 |

收到 `channel:"device"` 的 `callers_update` 后，设备应同时刷新联系人列表和 pending 列表。当前没有 `contacts_update` 事件。

`callers_update.payload` 字段：

| 字段 | 说明 |
|------|------|
| action | `request`（申请）/ `accept`（同意）/ `reject`（拒绝）/ `delete`（删除）/ `remark`（备注变化） |
| contact_type | `device` 或 `voip`；联系人申请相关动作固定为 `device` |
| peer_id | 对端标识；device 为设备 ID，voip 为 `wx_open_id` |

---

### `POST /v1/call/device/contacts/request`

发起跨账号联系人申请。同账号设备会直接自动接受，不走 pending 流程。该接口只适用于设备联系人；VoIP 联系人没有申请流程。

**鉴权**: ✅ 设备 JWT　**请求体**: `{ "target_device_id": "TIRZ00000002" }`

**成功响应**: `{ "code": 200, "msg": "ok", "data": {"status": "pending", "source": "manual"} }`。

| 字段 | 说明 |
|------|------|
| data.status | 跨账号申请为 `pending`；同账号设备直接建立联系人时为 `accepted` |
| data.source | 跨账号申请为 `manual`；同账号自动联系人为 `auto` |

成功后向目标设备的 `device/sn_{target_device_id}/notify` 推送：

```json
{
  "type": "callers_update",
  "channel": "device",
  "payload": {
    "action": "request",
    "contact_type": "device",
    "peer_id": "TIRZ00000001"
  }
}
```

目标设备收到后应提示联系人申请，并重新拉取联系人和 pending 列表。

**错误码**: `40000`（缺参数/呼叫自己）、`40400`（target 不存在）、`40206`（已是联系人）、`40207`（已有待处理申请）、`40209`（超过 `max_contacts_per_device`）

---

### `POST /v1/call/device/contacts/respond`

审批联系人申请（仅接收方可调用）。

**鉴权**: ✅ 设备 JWT　**请求体**: `{ "peer_device_id": "xxx", "action": "accept" }`（`action`: `accept` | `reject`）

**成功响应**: `{ "code": 200, "msg": "ok", "data": {"status": "accepted"} }`。
`data.status` 为审批后的状态：`accepted` 或 `rejected`。成功后向申请发起设备推送
`callers_update`。

**错误码**: `40000`、`40205`（申请不存在或非法响应）、`40209`（申请方或接收方联系人数量已达上限）

---

### `PUT /v1/call/device/contacts/remark`

修改联系人备注，设备联系人和 VoIP 联系人统一走这一个接口，服务端按 `peer_id` 自动判断类型。
当 `peer_id` 是 VoIP 联系人的 `wx_open_id` 时，修改的是该 OpenID 的统一联系人名称，
同一小程序下所有已授权设备都会更新，并收到 `callers_update`；不是修改设备名称。

**鉴权**: ✅ 设备 JWT

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| peer_id | string | ✅ | 设备联系人传对方 `device_id`；VoIP 联系人传 `wx_open_id` |
| remark | string | | 备注内容，空字符串清空备注，最多 64 个 Unicode 字符 |

**请求示例**

```json
{ "peer_id": "TIRZ00000002", "remark": "门铃" }
```

**成功响应**: `{ "code": 200, "msg": "ok" }`；响应不包含 `data` 字段。

**错误码**: `40000`（缺 peer_id 或 remark 超过 64 个字符）、`40205`（peer_id 既不是已接受的设备联系人，也不是本设备的 VoIP 授权用户）

---

### `DELETE /v1/call/device/contacts`

删除已接受的跨账号手动联系人（`source:"manual"`，软删除为 status=3，**双向生效**：自己和对方都失去该联系人），成功后向对端推送 `channel:"device"` 的 `callers_update`，对端应重新拉取联系人。同账号 `source:"auto"` 联系人属于账号内设备拓扑，不允许删除；VoIP 联系人的移除走小程序取消授权，不在此接口。

**鉴权**: ✅ 设备 JWT

**查询参数**

| 字段 | 类型 | 必填 | 说明 |
|------|------|:--:|------|
| peer_id | string | ✅ | 对方 `device_id` |

**请求示例**

```
DELETE /v1/call/device/contacts?peer_id=TIRZ00000002
```

**成功响应**: `{ "code": 200, "msg": "ok" }`；响应不包含 `data` 字段。

**错误码**: `40000`（缺 peer_id）、`40205`（联系人不存在、不是 accepted 状态或已删除）、`40211`（同账号 auto 联系人受保护，不允许删除）

---

### H5 侧联系人管理（`UserJWTAuth`，鉴权同 voip-server `/v1/voip/user/*`）

#### `GET /v1/call/user/contacts?device_id=xxx`

查询当前用户某台设备的完整联系人列表，同时返回设备联系人和 VoIP 联系人。

**查询参数**：`device_id` 必填，且必须属于当前用户。

**成功响应**：

```json
{ "code": 200, "msg": "ok", "data": { "contacts": [
  {"id": 12, "device_id": "TIRZ00000002", "type": "device", "remark": "门铃", "source": "manual", "online": true},
  {"id": 3, "device_id": "o4DLd5...", "type": "voip", "source": "voip", "remark": "妈妈",
   "wx_open_id": "o4DLd5...", "wx_app_id": "wxXXX", "wx_model_id": "HRHY_xxx"}
]}}
```

| 字段 | 适用 type | 说明 |
|------|:--:|------|
| data.contacts | — | 指定设备的联系人对象数组；没有联系人时为 `[]` |
| data.contacts[].id | 全部 | 对应数据表的数字主键：device 来自 `call_contact`，voip 来自 `voip_device_auth` |
| data.contacts[].device_id | 全部 | device 联系人为对方设备 ID；voip 联系人为 `wx_open_id` |
| data.contacts[].type | 全部 | `device` 或 `voip` |
| data.contacts[].remark | 全部 | device 为指定设备对该联系人的备注；voip 为当前 `wx_open_id + wx_app_id` 的统一联系人名称 |
| data.contacts[].source | 全部 | `manual`、`auto` 或 `voip` |
| data.contacts[].online | 仅 device | 对方设备当前是否在线 |
| data.contacts[].wx_open_id | 仅 voip | 微信用户 OpenID；与该项的 `device_id` 相同 |
| data.contacts[].wx_app_id | 仅 voip | 授权所属的微信小程序 AppID |
| data.contacts[].wx_model_id | 仅 voip | 授权对应的微信设备型号 ID |

**错误码**：`40000`（缺少 `device_id`）、`40300`（设备不属于当前用户）。

#### `GET /v1/call/user/contacts/pending`

查询当前用户名下所有设备可以审批的联系人申请。

**成功响应**：

```json
{ "code": 200, "msg": "ok", "data": { "pending": [
  {"id": 12, "type": "device", "initiator_device": "TIRZ00000001",
   "target_device": "TIRZ00000002", "created_at": "2026-07-22T14:00:00+08:00"}
]}}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| data.pending | object[] | 当前用户可审批的申请；没有待审批申请时为 `[]` |
| data.pending[].id | integer | `call_contact` 表主键；审批时作为 `POST /v1/call/user/contacts/respond` 的 `id` |
| data.pending[].type | string | 联系人类型；申请流程只适用于设备联系人，因此固定为 `device` |
| data.pending[].initiator_device | string | 发起申请的设备 ID |
| data.pending[].target_device | string | 当前用户负责审批的接收设备 ID |
| data.pending[].created_at | string | 申请创建时间，RFC 3339 格式 |

#### 其他 H5 联系人操作

| 接口 | 请求 | 成功响应与说明 |
|------|------|----------------|
| `GET /v1/voip/user/contacts?device_id=xxx` | 查询参数 `device_id` | 某台名下设备的小程序 VoIP 联系人列表；由 voip-server 提供，不包含设备联系人；字段见该接口章节 |
| `POST /v1/call/user/contacts/request` | `{device_id, target_device_id}` | `data.status` 为 `pending`（跨账号）或 `accepted`（同账号），`data.source` 对应为 `manual` 或 `auto` |
| `POST /v1/call/user/contacts/respond` | `{id, action}` | `data.status` 为审批后的 `accepted` 或 `rejected`；服务端根据申请 ID 确定接收设备，不能代替发起方审批 |
| `PUT /v1/call/user/contacts/remark` | `{device_id, peer_id, remark}` | 不返回 `data` 字段；`device_id` 是当前用户名下的本方设备，`peer_id` 是对方设备 ID 或 VoIP 联系人的 `wx_open_id`；VoIP 名称会同步到同一小程序下的全部授权设备；remark 最多 64 个 Unicode 字符 |
| `DELETE /v1/call/user/contacts/:id` | 路径参数 `id` | 不返回 `data` 字段；按 `call_contact.id` 删除 manual 设备联系人；auto 联系人返回 `40211`，VoIP 联系人的移除走小程序取消授权 |

> `id` 不是鉴权凭据。审批和删除会根据 ID 找到实际设备并校验所有权；申请和备注会校验请求中的 `device_id` 属于当前用户。H5 发起的审批、备注和删除会向受影响设备推送 `callers_update`。JWT 鉴权失败返回 HTTP 401 + `code=401`；设备或联系人不属于当前用户返回 HTTP 200 + `code=40300`。

---

### `POST /v1/call/internal/unbind`

服务间调用：设备解绑时永久删除所有涉及该设备的 `call_contact` 记录（包括待审批、已拒绝和已软删除记录），释放房间，并向原有未删除联系人的对端发送 `callers_update`。

**鉴权**: `X-Internal-Key` 请求头，值需匹配配置 `internal.key`

**请求体**: `{ "device_id": "TIRZ00000001" }`　**成功响应**: `{ "code": 200, "msg": "ok" }`　**错误码**: `40301`（key 不匹配或未配置）、`40000`（缺 device_id）；错误仍使用 HTTP 200。

---

## 错误码汇总

同一个业务码在不同服务中可能具有不同 HTTP 状态或语义，客户端应先按服务和接口判断，
不要只建立一张跨服务的全局映射。下表列出各服务实际使用的返回码；
单个接口只会返回其接口章节列出的子集。

### device-server / user-server / ai-server（HTTP 状态型响应）

| code | HTTP | 说明 |
|------|------|------|
| 200 | 200 | 成功 |
| 4002 | 400 | 验证码无效或已过期（含输错验证码、码不存在） |
| 4003 | 422 | 设备配额已用完 |
| 4040 | 404 | 设备不存在或不属于当前用户 |
| 4090 | 409 | 邮箱已注册 |
| 4091 | 401 | 邮箱或密码错误 |
| 40000 | 400 | 请求参数错误（JSON 解析失败、必填字段缺失等） |
| 40012 | 400 | 人机验证失败 |
| 40013 | 400/404 | 用户注册验证码无效时为 HTTP 400；设备 TTS 验证码无效时为 HTTP 404 |
| 401 | 401 | 未登录或 token 无效（缺少 Authorization 头 / JWT 无效 / 过期） |
| 40300 | 403 | 无权操作资源（rtc-token 的设备归属校验，以及 AI 角色、设备和资源归属校验） |
| 40301 | 403 | AI 内部服务凭证无效 |
| 40400 | 404 | 资源未找到（agent default role 未配置/未找到） |
| 40901 | 409 | 验证已在进行中 |
| 429 | 429 | 请求过频繁（L2 单 IP 新 MAC 过多 / L3 同 MAC 频率 / L4 全局上限） |
| 42900 | 429 | AI MCP、设备插件或知识库创建额度已用尽 |
| 50000 | 500 | 服务器内部错误 |
| 5001 | 504 | MQTT 下发 auth_grant 超时 |
| 50200 | 502 | AI 服务暂不可用（探鸽云 API 调用失败） |
| 6002 | 503 | 设备离线（bind-by-id 无主设备无临时 MQTT 连接） |
| 6004 | 409 | 设备已被其他用户绑定 |
| 6006 | 410 | 设备已解绑（user_id=0） |
| 6008 | 401 | HMAC 签名校验失败、签名 Header 不完整、时间戳无效/偏差过大或 Nonce 重放；具体原因见 `msg` |
| 6010 | 400 | mac 为空 |
| 6011 | 403 | MAC 不符，疑似克隆 |
| 6012 | 503 | 设备池已耗尽 |
| 6013 | 403 | 签名 Report、Token 或绑定请求中的 MAC 与 device_id 已记录的 MAC 不一致 |
| 6014 | 400 | Body 带了未声明的 device_id |
| 6015 | 409 | 同 MAC 已绑定至本账号其它设备 |

### voip-server

| code | HTTP | 说明 |
|------|------|------|
| 0 | 200 | 成功 |
| 401 | 401 | JWT 鉴权失败（中间件直接返回，不经 apiresp） |
| 40000 | 200 | 请求参数错误 |
| 40203 | 200 | 微信登录状态不存在、已过期或 OpenID 不匹配 |
| 40205 | 200 | 微信 VoIP 联系人或授权不存在/已失效 |
| 40300 | 200 | 无权访问指定设备或资源 |
| 40301 | 200 | 内部服务凭证无效 |
| 40400 | 200 | 资源未找到 |
| 40900 | 200 | 资源冲突 |
| 50000 | 200 | 内部服务器错误 |
| 50001 | 200 | 微信 App 配置错误 |
| 50002 | 200 | 微信 API 调用失败 |
| 6006 | 200 | 设备已解绑，需要重新绑定 |

### call-server

| code | HTTP | 说明 |
|------|------|------|
| 200 | 200 | 成功 |
| 401 | 401 | JWT 鉴权失败（中间件直接返回） |
| 40000 | 200 | 请求参数错误 |
| 40201 | 200 | 被叫全部离线 |
| 40202 | 200 | 主叫忙（已在其他房间中） |
| 40205 | 200 | 联系人不存在（或非已接受状态） |
| 40206 | 200 | 联系人已存在 |
| 40207 | 200 | 已有待处理的联系人申请 |
| 40209 | 200 | 联系人数量达上限 |
| 40210 | 200 | 房间已被抢接 |
| 40211 | 200 | 同账号自动联系人受保护，不允许删除 |
| 40300 | 200 | 无权操作指定房间、设备或联系人 |
| 40301 | 200 | 内部服务凭证无效 |
| 40400 | 200 | 房间/资源未找到 |
| 50000 | 200 | 内部服务器错误 |

### 微信通知回调 errcode

| errcode | 含义 |
|---------|------|
| 0 | 成功 |
| 2 | 意外的信令消息 |
| 3 | wx_app_id 未配置 |
| 4 | 意外的 action |
| 5 | 签名校验失败 |
| 9 | 请求体无效 |
| 10 | TiRTC/MQTT 处理失败，或同一房间的上一笔回调仍在处理中 |
