# 探鸽云工具集

两个独立项目，覆盖设备端和服务端接入探鸽云（tange.ai TiRTC）的场景。

| 项目 | 用途 | 语言 | 入口 |
|------|------|------|------|
| **[tirtc-device-token](tirtc-device-token/)** | 设备端 v1 token 生成 + TGV1 签名库 | Go / Python / JS / TS / Java / PHP / C / Rust | 8 |
| **[tirtc-api-client](tirtc-api-client/)** | 服务端调 OpenAPI 示例 + TGV1 签名库 | Go / Python / Node.js / PHP / Java / C# | 6 |

---

## 核心算法：TGV1-HMAC-SHA256

两个项目的签名核心一致，均为探鸽云 OpenAPI 的 TGV1-HMAC-SHA256 签权方案。

### 8 步签名流程

| 步骤 | 操作 | 说明 |
|------|------|------|
| ① | 构建 Header Map | 签名头 + 业务头（x-tg-algorithm, x-tg-date, x-tg-app-id, x-tg-content-sha256） |
| ② | SignedHeaders | 参与签名的头名字典序排列，分号连接 |
| ③ | 规范化请求 (Canonical Request) | 6 行：Method + URI + Query + Headers + SignedHeaders + BodySHA256 |
| ④ | 哈希规范化请求 | `SHA256(CanonicalRequest)` |
| ⑤ | 待签字符串 | `TGV1-HMAC-SHA256\nDate\nScope\nCanonicalHash` |
| ⑥ | 派生签名密钥 | 三级 HMAC 链：`date → uri → "tgv1_request"`，密钥前缀 `"TGV1" + accessSecret` |
| ⑦ | 生成签名 | `Hex(HMAC-SHA256(StringToSign, SigningKey))` |
| ⑧ | 拼装 Authorization | `TGV1-HMAC-SHA256 Credential=…, SignedHeaders=…, Signature=…` |

### 签名头规则

| 方法 | 参与签名的头 |
|------|-------------|
| `POST` / `PUT` / `PATCH` | `content-length;content-type;x-tg-app-id;x-tg-date` |
| `GET` / `DELETE` | `x-tg-app-id;x-tg-date` |

> `x-tg-algorithm` 和 `x-tg-content-sha256` 随请求发送但**不参与签名**。

### Credential Scope

```
{YYYYMMDD + 7天}/tgv1_request
```

签名时间加 7 天作为有效期窗口，防止时钟偏差。

---

## tirtc-device-token — 设备端

**让设备拿到连接云端的 token。**

### 场景

```
业务服务器                          探鸽云                       设备
    │                                │                          │
    │  POST /getRtcToken             │                          │
    │  (device_id)                   │                          │
    │ ──────────────────────────────→│                          │
    │                                │                          │
    │  GenerateDeviceToken()         │                          │
    │  ↓                             │                          │
    │  {token, app_id, endpoint}     │                          │
    │ ←──────────────────────────────│                          │
    │                                │                          │
    │  返回给设备                     │                          │
    │ ──────────────────────────────────────────────────────────→│
    │                                │                          │
    │                                │   TiRTC SDK connect()    │
    │                                │ ←────────────────────────│
```

### 用法

<details>
<summary><b>Go</b></summary>

```go
import tirtcsigning "github.com/tange-ai/tirtc-device-token/go"

token, claims, _ := tirtcsigning.GenerateDeviceToken(
    accessKeyID,      // 开发者 Access Key（控制台获取）
    secretKeyID,      // 开发者 Secret Key（控制台获取）
    remoteID,         // 设备 ID
    deviceSecretKey,  // 设备密钥（deviceKey）
)
// → "v1.eyJzdWIi..."
```

```bash
cd tirtc-device-token/go
export TIRTC_APP_ID=xxx TIRTC_ACCESS_KEY=xxx TIRTC_SECRET_KEY=xxx
go run ./example -remote-id DEVICE01 -device-secret-key xxx
```

</details>

<details>
<summary><b>Python</b></summary>

```python
from tirtc_signing import sign_request, generate_device_token  # 参考 go 实现

token, claims = generate_device_token(
    access_key="...", secret_key="...",
    remote_id="DEVICE01", device_secret_key="...",
)
```

</details>

<details>
<summary><b>Java</b></summary>

```java
// 参考 tirtc-device-token/java/TirtcSigning.java
String token = TirtcSigning.generateDeviceToken(
    accessKey, secretKey, remoteId, deviceSecretKey);
```

</details>

<details>
<summary><b>C</b></summary>

```c
#include "tirtc_signing.h"

char token[1024];
tirtc_generate_device_token(access_key, secret_key, remote_id, device_secret, token, sizeof(token));
```

```bash
cd tirtc-device-token/c && make test
```

</details>

### 更多语言

完整实现见 [tirtc-device-token/](tirtc-device-token/)：Go / Python / JavaScript / TypeScript / Java / PHP / C / Rust，共享 `test-vectors.json` 交叉验证。

---

## tirtc-api-client — 服务端

**服务端调探鸽云 OpenAPI，支持以下 4 个接口：**

| 命令 | 方法 | 路径 | 描述 |
|------|------|------|------|
| `wxvoip` | POST | `/v1/token/wxvoip` | 微信 VoIP 通话凭证 |
| `aichat` | POST | `/v1/token/aichat` | AI 语音对话凭证 |
| `login` | POST | `/v2/user/login/user-id` | 用户登录 |
| `plans` | GET | `/v2/cloud-service/plans` | 查询套餐列表 |

### 环境变量

```bash
export TIRTC_APP_ID=你的appId
export TIRTC_ACCESS_KEY=你的accessKey
export TIRTC_SECRET_KEY=你的secretKey
```

### 用法

<details open>
<summary><b>Go</b></summary>

```bash
cd tirtc-api-client/go
go run . wxvoip   # POST — 微信 VoIP
go run . aichat   # POST — AI 对话
go run . login    # POST — 用户登录
go run . plans    # GET  — 套餐列表
```

```go
import "github.com/tange-ai/tirtc-api-client/signing"

headers := signing.SignRequest(accessKey, accessSecret, appID,
    "POST", "/v2/user/login/user-id", "", bodyBytes, time.Now().UTC())
// headers.Get("Authorization") → "TGV1-HMAC-SHA256 Credential=…"
```

</details>

<details>
<summary><b>Python</b></summary>

```bash
cd tirtc-api-client/python
python main.py wxvoip
python main.py aichat
python main.py login
python main.py plans
```

```python
from tirtc_signing import sign_request

headers = sign_request(
    access_key="...", access_secret="...", app_id="...",
    method="POST", uri_path="/v2/user/login/user-id",
    body='{"user_id":"test"}',
)
```

</details>

<details>
<summary><b>Node.js</b></summary>

```bash
cd tirtc-api-client/nodejs
node main.js wxvoip
node main.js aichat
node main.js login
node main.js plans
```

```javascript
const { signRequest } = require('./tirtc_signing');

const headers = signRequest(accessKey, accessSecret, appId,
    'POST', '/v2/user/login/user-id', '{"user_id":"test"}');
```

</details>

<details>
<summary><b>PHP</b></summary>

```bash
cd tirtc-api-client/php
php main.php wxvoip
php main.php aichat
php main.php login
php main.php plans
```

```php
require_once 'TirtcSigning.php';

$headers = TirtcSigning::signRequest(
    $accessKey, $accessSecret, $appId,
    'POST', '/v2/user/login/user-id', '{"user_id":"test"}'
);
```

</details>

<details>
<summary><b>Java</b></summary>

```bash
cd tirtc-api-client/java
javac TirtcSigning.java Main.java && java Main wxvoip
```

```java
Map<String, String> headers = TirtcSigning.signRequest(
    accessKey, accessSecret, appId,
    "POST", "/v2/user/login/user-id", "", "{\"user_id\":\"test\"}", null
);
```

</details>

<details>
<summary><b>C#</b></summary>

```bash
cd tirtc-api-client/csharp
dotnet run -- wxvoip
```

```csharp
var headers = TirtcSigning.SignRequest(
    accessKey, accessSecret, appId,
    "POST", "/v2/user/login/user-id", "", "{\"user_id\":\"test\"}"
);
```

</details>

### 响应解读

```json
{"code": 0, "msg": "ok", "data": {...}}     // ✅ 成功
{"code": 40105, "message": "INVALID_..."}    // ❌ 签名失败
{"code": 401, "msg": "AuthFailure..."}       // ❌ 鉴权失败
```

---

## 目录结构

```
tools/
├── README.md
├── tirtc-device-token/      ← 设备端 token 生成（8 语言）
│   ├── go/  python/  javascript/  typescript/
│   ├── java/  php/  c/  rust/
│   ├── test-vectors.json    ← 跨语言测试向量
│   └── README.md
│
└── tirtc-api-client/        ← 服务端 API 签名（6 语言 + 4 接口）
    ├── go/  python/  nodejs/  php/  java/  csharp/
    └── (本文件)
```

## 参考

- [探鸽云 HTTP 鉴权规范](https://tange-ai.feishu.cn/wiki/wikcn6GaETEIVO0jgCP1ASHf3Mg)
- [微信 VoIP 服务端 API](https://docs.tange.ai/products/wxvoip/api-reference/server-api)
- [AI Chat 服务端 API](https://docs.tange.ai/products/ai-chat/api-reference/server-api)
- [探鸽云开放平台](https://tange.ai)
