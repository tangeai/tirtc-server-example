# TiRTC 服务端 HTTP 签名

业务服务器调用 [tange.ai TiRTC](https://tange.ai) 开放 API 时，每个请求必须携带 `Authorization` 及若干 `X-Tg-*` 头部。本项目实现 **TGV1-HMAC-SHA256** 签名的 8 种语言版本，通过共享测试向量逐字节验证一致。

---

## 快速开始

参照 [tirtc-developer-tools](https://github.com/tangeai/tirtc-developer-tools) 的 `token-issuer/issuer/signing.go`：自签名生成 `v1` 格式设备连接 token，设备端用此 token 调用 TiRTC SDK 连接云端。

### 参数说明

| 参数 | 含义 | 来源 |
|------|------|------|
| `accessKeyID` | 开发者 Access Key | 控制台，与 appId 关联 |
| `secretKeyID` | 开发者 Secret Key | 控制台，与 appId 关联（**保密**） |
| `remoteID` | 设备 ID（device_id） | 设备的唯一标识 |
| `deviceSecretKey` | 设备密钥（deviceKey） | thing-connect device-server 数据库 |

### 生成 token

```go
import tirtcsigning "github.com/tange-ai/token-signing/go"

token, claims, _ := tirtcsigning.GenerateDeviceToken(
    accessKeyID,      // 开发者 Access Key（控制台获取）
    secretKeyID,      // 开发者 Secret Key（控制台获取）
    remoteID,         // 设备 ID
    deviceSecretKey,  // 设备密钥（deviceKey）
)
// token  = "v1.eyJzdWIi..."
// claims.Scope  = "connect:device://<deviceID>"
// claims.Iss   = accessKeyID
```

业务服务器返回给客户端：

```json
{"token": "v1.eyJzdWIi...", "app_id": "你的appId", "endpoint": "wss://..."}
```

设备拿到后调用 `TiRTC SDK connect()` 连接云端。

> `go/example/main.go` 可运行：设好凭证环境变量后 `go run . -remote-id XX -device-secret-key YY`。

---

## HTTP API 签名

调 TiRTC 开放 API（非设备连接场景）时，用 `SignRequest` 生成 TGV1-HMAC-SHA256 签名头：

```go
headers := tirtcsigning.SignRequest(
    accessKeyID, secretKeyID, appID,
    "POST", "/v1/token/wxvoip", "",
    body, time.Now().UTC(),
)
```

> `go/example/main.go` 包含完整端到端调用，验证凭证是否有效。

---

## 函数签名

```
SignRequest(accessKey, accessSecret, appId, method, uriPath, rawQuery, body, signingTime) → headers
```

### 参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `accessKey` | string | 是 | 控制台 Access Key |
| `accessSecret` | string | 是 | 控制台 Access Secret（保密，存服务端） |
| `appId` | string | 是 | 应用 ID |
| `method` | string | 是 | `GET` / `POST` / `PUT` / `PATCH` / `DELETE` |
| `uriPath` | string | 是 | 路径，如 `/v1/token/wxvoip` |
| `rawQuery` | string | 否 | query string（不含 `?`），POST 等方法忽略 |
| `body` | string/bytes | 否 | 请求体，GET/DELETE 传空 |
| `signingTime` | datetime | 否 | UTC 时间，默认当前时间 |

### 返回值

| 头部 | 何时出现 |
|------|---------|
| `X-Tg-Algorithm` | 始终 |
| `X-Tg-Date` | 始终 |
| `X-Tg-App-Id` | 始终 |
| `X-Tg-Content-Sha256` | 始终 |
| `X-Tg-Signed-Headers` | 始终 |
| `Authorization` | 始终 |
| `Content-Type: application/json` | POST/PUT/PATCH |
| `Content-Length` | POST/PUT/PATCH |

---

## 签名算法（8 步详解）

以下面这个请求为例，逐步演示每个中间值：

```
POST /v1/token/wxvoip
Body:    {"device_id":"TESTDEVICE01","wx_session_key":"test-key","wx_room_id":"room-1","wx_session_token":"token-1","wx_app_id":"wxapp","wx_model_id":"model-1","audio_rate":8000,"audio_channels":1}
签名时间: 2024-01-15T12:00:00Z
```

使用凭证 `accessKey=test-access-key-123`, `accessSecret=test-secret-456`, `appId=app-789`。

---

### 1. 构建 Header Map

| Key | Value |
|-----|-------|
| `x-tg-algorithm` | `TGV1-HMAC-SHA256` |
| `x-tg-date` | `20240115T120000Z` |
| `x-tg-app-id` | `app-789` |
| `x-tg-content-sha256` | `b953c579ce8e6b9bd78395c0719967b395fdf150f56ab04e575b7f16a5164784` |
| `content-type` | `application/json` |
| `content-length` | `188` |

`x-tg-content-sha256` = `SHA256(body)` 的十六进制小写。`content-type`/`content-length` 仅 POST/PUT/PATCH。

### 2. SignedHeaders

所有 key 转小写，字母排序，分号连接：

```
content-length;content-type;x-tg-algorithm;x-tg-app-id;x-tg-content-sha256;x-tg-date
```

### 3. 规范化请求（Canonical Request）

固定 6 行结构：

```
<METHOD>
<URI路径>
<QueryString>
<规范化Header>
<SignedHeaders>
<请求体SHA256>
```

填充实例：

```
POST
/v1/token/wxvoip

content-length:188
content-type:application/json
x-tg-algorithm:TGV1-HMAC-SHA256
x-tg-app-id:app-789
x-tg-content-sha256:b953c579ce8e6b9bd78395c0719967b395fdf150f56ab04e575b7f16a5164784
x-tg-date:20240115T120000Z
content-length;content-type;x-tg-algorithm;x-tg-app-id;x-tg-content-sha256;x-tg-date
b953c579ce8e6b9bd78395c0719967b395fdf150f56ab04e575b7f16a5164784
```

细节：
- URI 路径：去掉尾部 `/`（`/` 本身保留）
- Query String：POST/PUT/PATCH 为空行；GET/DELETE 去 `?` 前缀，`+` 替换为 `%20`
- Header 行：只包含 signed headers 中的 key，全小写，字典序，格式 `key:value`

### 4. 哈希规范化请求

```
SHA256(规范化请求) = 90d76363f70f4a2d6587c962a4d5249a1c47eaa92877a08ed3ec7e54f430920b
```

### 5. 待签字符串（String to Sign）

```
TGV1-HMAC-SHA256
<X-Tg-Date>
<CredentialScope>
<规范化请求的SHA256>
```

CredentialScope = 签名日期 + 7 天的 `YYYYMMDD` + `/tgv1_request`。实例：

```
TGV1-HMAC-SHA256
20240115T120000Z
20240122/tgv1_request
90d76363f70f4a2d6587c962a4d5249a1c47eaa92877a08ed3ec7e54f430920b
```

### 6. 派生签名密钥

三级 HMAC-SHA256 链式派生：

```
k1 = HMAC-SHA256(key="TGV1" + accessSecret,    data="20240115")
k2 = HMAC-SHA256(key=k1,                        data="/v1/token/wxvoip")
k3 = HMAC-SHA256(key=k2,                        data="tgv1_request")
```

中间值：

```
k1 = de72e7a9eaa708049a30da671aa152370aa24e3484488fc2875c85a6fd684816
k2 = b584143a9b65d79b91bac1978aec2aa03098a25ed500d4dfb214b84c6f09dd82
k3 = 03ff6eadf2837c530cd84ab0be19252e8a95e0b735a25c4db00895bf781a4e3f
```

### 7. 生成签名

```
Signature = Hex(HMAC-SHA256(key=k3, data=待签字符串))
         = 1a1ab70f9e3b7e847738bf741fdc44d9a991e21a145536bd7d93d89d59ba4ca1
```

### 8. 拼装 Authorization

```
TGV1-HMAC-SHA256 Credential=<accessKey>/<CredentialScope>, SignedHeaders=<SignedHeaders>, Signature=<Signature>
```

结果：

```
TGV1-HMAC-SHA256 Credential=test-access-key-123/20240122/tgv1_request, SignedHeaders=content-length;content-type;x-tg-algorithm;x-tg-app-id;x-tg-content-sha256;x-tg-date, Signature=1a1ab70f9e3b7e847738bf741fdc44d9a991e21a145536bd7d93d89d59ba4ca1
```

---

## 各语言

每种子目录是可直接复用的单文件。C 需 OpenSSL，Rust 需几个 crate，其余语言零外部依赖。

### Go

```go
import tirtcsigning "github.com/tange-ai/token-signing/go"

headers := tirtcsigning.SignRequest(
    accessKey, accessSecret, appId,
    method, uriPath, rawQuery, body, signingTime,
) // → http.Header
```

### Python

```python
from tirtc_signing import sign_request

headers = sign_request(
    access_key="...", access_secret="...", app_id="...",
    method="POST", uri_path="/v1/token/wxvoip",
    body='{"device_id":"TEST","wx_session_key":"xxx",...}',
) # → dict
```

Python ≥ 3.7，仅 `hashlib`/`hmac`/`datetime`。

### JavaScript

```javascript
const { signRequest } = require('./tirtc_signing');

const headers = signRequest(
    accessKey, accessSecret, appId,
    'POST', '/v1/token/wxvoip', '{"device_id":"TEST",...}',
); // → object
```

Node.js ≥ 12，仅 `crypto`。

### TypeScript

```typescript
import { signRequest, SigningHeaders } from './tirtc_signing';

const headers: SigningHeaders = signRequest(
    accessKey, accessSecret, appId,
    'POST', '/v1/token/wxvoip', '{"device_id":"TEST",...}',
);
```

### Java

```java
Map<String, String> headers = TirtcSigning.signRequest(
    accessKey, accessSecret, appId,
    "POST", "/v1/token/wxvoip", "{\"device_id\":\"TEST\",...}", "", null
); // null → 当前时间
```

JDK ≥ 11，纯标准库。

### PHP

```php
$headers = TirtcSigning::signRequest(
    $accessKey, $accessSecret, $appId,
    'POST', '/v1/token/wxvoip', '{"device_id":"TEST",...}'
); // → array
```

PHP ≥ 7.4，仅内置 `hash()`/`hash_hmac()`。

### Rust

```rust
use tirtc_signing::sign_request;

let headers = sign_request(
    access_key, access_secret, app_id,
    "POST", "/v1/token/wxvoip", r#"{"device_id":"TEST",...}"#, "", None,
); // → HashMap<String, String>
```

依赖 `hmac` + `sha2` + `hex` + `chrono`。

### C

```c
#include "tirtc_signing.h"

TirtcHeaders h = tirtc_sign_request(
    access_key, access_secret, app_id,
    "POST", "/v1/token/wxvoip",
    "{\"device_id\":\"TEST\",...}", (size_t)-1,   /* -1 = C 字符串 */
    "",                                              /* rawQuery */
    0                                                /* 0 = 当前时间 */
);
// 遍历 h.entries[i].name / h.entries[i].value
```

C11 + OpenSSL (`-lcrypto`)。非线程安全（内部静态缓冲区），适合嵌入式。

---

## 测试

每个子目录可独立跑测试。全部通过：

```
Go:      go test ./...
Python:  python -m unittest test_tirtc_signing
JS:      node tirtc_signing.test.js
TS:      npm install && npx ts-node tirtc_signing.test.ts
Java:    javac TirtcSigning.java TirtcSigningTest.java && java TirtcSigningTest
PHP:     php -d assert.active=1 test_tirtc_signing.php
Rust:    cargo test
C:       make test
```

| Go | Python | JS | TS | Java | PHP | C | Rust |
|----|--------|-----|-----|------|------|----|------|
| ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |

所有实现通过 `test-vectors.json` 的 8 组固定向量交叉验证（POST/GET/PUT/DELETE、空 body、带 query、URI 尾部斜杠、根路径）。

---

## 运行示例

`go/example/main.go` 完整展示: **自签名生成 token → 本地验证 → TGV1 调 API 端到端验证**。

```bash
export TIRTC_APP_ID=你的appId
export TIRTC_ACCESS_KEY=你的accessKey       # accessKeyID
export TIRTC_SECRET_KEY=你的secretKey       # secretKeyID
go run . -remote-id 设备ID -device-secret-key 设备密钥
```

---

## 目录结构

```
token-signing/
  README.md
  test-vectors.json           ← 8 组跨语言测试向量

  go/          Go             ← 纯标准库 + example 端到端验证
  python/      Python         ← 纯标准库
  javascript/  Node.js        ← 仅 crypto
  typescript/  TypeScript     ← Node + TS
  java/        Java           ← 纯 JDK
  php/         PHP            ← 纯内置函数
  rust/        Rust           ← hmac + sha2 + hex + chrono
  c/           C              ← C11 + OpenSSL
```

## 参考

- [TiRTC 开放平台](https://tange.ai)
- [服务端 API 接口文档](https://docs.tange.ai/products/wxvoip/api-reference/server-api)
- [HTTP 鉴权规范（飞书）](https://tange-ai.feishu.cn/wiki/wikcn6GaETEIVO0jgCP1ASHf3Mg)

## License

MIT
