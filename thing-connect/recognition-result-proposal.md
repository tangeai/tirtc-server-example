# AI 识别结果协议调整方案

状态：待确认，仅用于评审。本文描述拟调整的目标行为，代码尚未实现；设备接入仍以 [API Reference](api-reference.md#recognition-task) 为准。确认后再修改服务端、模拟器和正式接口文档。

## 调整范围

保留 `code/msg/data`、`results[]`、`result`、`speech`，不改成另一套字段层级。HTTP 上传受理、单个任务查询和 MQTT 结果通知使用相同的任务对象。文字随通知返回，语音通过带设备鉴权的 HTTP 接口下载。

| 项目 | 当前实现 | 建议调整 |
|---|---|---|
| 整个任务 `data.status` | 分析完成后等待语音或下一类型时可能回到 queued | queued 只表示任务从未开始；任一阶段开始后保持 processing，直到结束或结果未知 |
| 单类型 `results[].status` | 表示分析阶段，不能代表语音完成 | 保留原含义，增加 overall_status 明确表示该类型整体进度 |
| 类型名称 | 只有 scheme_id 和 scheme_version | 增加 name，保存任务创建时所选发布版本的名称快照 |

保留 `results[].status` 的分析含义，避免把分析成功误改成“仍未成功”。新增的 `overall_status` 用于判断该类型要求返回的内容是否都已准备好。

此次方案不增加图片、视频、流式音频、设备 ACK、重连自动补查或模型失败自动重试。

## 字段层级

| 字段 | 含义 |
|---|---|
| code / msg | 本次请求或通知的处理结果；code=200 不代表识别任务成功 |
| data.request_id / device_id | 任务编号、所属设备 |
| data.status | 整个任务的状态 |
| data.status_revision | 同一任务递增的快照版本，用于去重和过滤迟到结果 |
| data.results | 按用户配置顺序排列的各类型结果 |
| results[].item_id / order | 类型子任务编号、执行顺序 |
| results[].scheme_id / scheme_version | 本次任务使用的类型及发布版本 |
| results[].name | **新增**：该发布版本的名称快照，后续改名不改变历史任务名称 |
| results[].output_mode | text、audio 或 text_audio |
| results[].overall_status | **新增**：整个类型子任务的状态，包含分析及所需语音阶段 |
| results[].status | 分析阶段状态，含义保持不变 |
| results[].result | 分析结果，包含 summary、alarm、labels、details；纯语音类型为 null |
| results[].error | 分析阶段错误；语音错误仍放在 speech.error |
| results[].speech | 语音状态、就绪后的下载地址和格式，以及合成错误 |
| data.error | 父任务级错误；子项失败由对应 error 或 speech.error 描述，不合并成一条模糊错误 |

任务列表接口的 `data.items` 仍然是多个任务的分页容器，每个任务内部使用 `results[]`。

## 状态规则

`data.status` 与 `results[].overall_status` 共用以下状态枚举，但分别作用于整个任务和单个类型：

| 状态 | 含义 |
|---|---|
| queued | 尚未开始执行 |
| processing | 已开始，仍有分析、语音合成或后续类型待处理 |
| succeeded | 要求返回的内容全部准备完成 |
| partial_failed | 至少有一部分要求返回的内容可交付，同时存在失败或取消的部分 |
| failed | 明确失败，且没有可交付的内容 |
| unknown | 上游结果无法确认；暂停后续处理，不自动重新调用模型 |
| cancelled | 已取消，且没有可交付的内容；取消不等于分析失败 |

语音状态继续使用 `pending / synthesizing / ready / failed / unknown / skipped`。仅文字类型从受理开始即为 skipped，不调用 TTS。

**按用户选择的返回方式判断成功，不能只看分析成功：**

| 返回方式与场景 | 分析 status | speech.status | overall_status |
|---|---|---|---|
| 文字：尚未分析 | queued | skipped | queued |
| 文字：分析成功 | succeeded | skipped | succeeded |
| 文字＋语音：文字完成，等待合成 | succeeded | pending | processing |
| 文字＋语音：语音合成中 | succeeded | synthesizing | processing |
| 文字＋语音：全部完成 | succeeded | ready | succeeded |
| 文字＋语音：文字成功，合成明确失败 | succeeded | failed | partial_failed |
| 语音：内部分析成功，等待合成 | succeeded | pending | processing |
| 语音：合成成功 | succeeded | ready | succeeded |
| 语音：合成明确失败 | succeeded | failed | failed |
| 任意模式：分析明确失败 | failed | skipped | failed |
| 任意模式：分析结果未知 | unknown | skipped | unknown |
| 含语音模式：合成结果未知 | succeeded | unknown | unknown |

纯语音模式的内部分析文字不属于可交付内容，所以合成失败时为 failed。文字＋语音模式的文字仍可使用，所以合成失败时为 partial_failed。unknown 优先表示结果待确认，即使已有文字也不能据此标记任务已全部结束。

多个类型时，父任务按以下顺序聚合：

1. 整个任务被取消时，按是否已有可交付内容确定 partial_failed 或 cancelled，并停止后续处理。
2. 任一类型为 unknown 时，父任务为 unknown，暂停后续类型，不自动重试收费调用。
3. 仍有 queued 或 processing 类型时：整个任务从未开始则为 queued，否则为 processing。
4. 全部结束后：所有类型成功则为 succeeded；有可交付内容且存在失败或取消则为 partial_failed；无可交付内容且存在明确失败则为 failed；其余全部取消则为 cancelled。

明确失败允许继续处理下一个类型。后续类型尚未开始时，其 overall_status 可以为 queued，但父任务必须保持 processing，不能退回 queued。

## 返回示例

以下示例均为拟调整后的返回，不是当前服务返回。三个示例中的单个任务对象也用于 MQTT 的 payload.data。

### 文字完成，语音等待合成

分析 status 已是 succeeded，但该类型 overall_status 与父任务 status 都是 processing。

```json
{
  "code": 200,
  "msg": "ok",
  "data": {
    "request_id": "audio_000001",
    "device_id": "DEVICE001",
    "status": "processing",
    "status_revision": 3,
    "results": [
      {
        "item_id": "item_1",
        "order": 1,
        "scheme_id": "cry_detection",
        "scheme_version": 1,
        "name": "婴儿哭声检测",
        "output_mode": "text_audio",
        "overall_status": "processing",
        "status": "succeeded",
        "result": {
          "summary": "检测到婴儿哭声，建议及时查看宝宝情况。",
          "alarm": true,
          "labels": [
            "婴儿哭声"
          ],
          "details": {
            "uncertainty": "仅凭哭声无法可靠判断具体原因。"
          }
        },
        "error": null,
        "speech": {
          "status": "pending",
          "error": null
        }
      }
    ],
    "error": null,
    "created_at": "2026-10-28T07:30:07Z"
  }
}
```

### 文字和语音全部完成（MQTT 通知）

MQTT 外层保持 type/channel/event_id/payload。HTTP 单任务查询返回下面的 payload 对象。speech.url 仍是带设备鉴权访问的相对路径。

```json
{
  "type": "recognition_result",
  "channel": "recognition",
  "event_id": "event_example_005",
  "payload": {
    "code": 200,
    "msg": "ok",
    "data": {
      "request_id": "audio_000001",
      "device_id": "DEVICE001",
      "status": "succeeded",
      "status_revision": 5,
      "results": [
        {
          "item_id": "item_1",
          "order": 1,
          "scheme_id": "cry_detection",
          "scheme_version": 1,
          "name": "婴儿哭声检测",
          "output_mode": "text_audio",
          "overall_status": "succeeded",
          "status": "succeeded",
          "result": {
            "summary": "检测到婴儿哭声，建议及时查看宝宝情况。",
            "alarm": true,
            "labels": [
              "婴儿哭声"
            ],
            "details": {
              "uncertainty": "仅凭哭声无法可靠判断具体原因。"
            }
          },
          "error": null,
          "speech": {
            "status": "ready",
            "url": "/v1/recognition/requests/audio_000001/audio/item_1",
            "format": "wav",
            "error": null
          }
        }
      ],
      "error": null,
      "created_at": "2026-10-28T07:30:07Z"
    }
  }
}
```

### 仅返回语音，合成失败

HTTP 查询仍然正常返回 code=200；任务失败由 data.status 表达。result 为 null，不把内部文字下发设备；错误位于 speech.error。

```json
{
  "code": 200,
  "msg": "ok",
  "data": {
    "request_id": "audio_000001",
    "device_id": "DEVICE001",
    "status": "failed",
    "status_revision": 5,
    "results": [
      {
        "item_id": "item_1",
        "order": 1,
        "scheme_id": "cry_detection",
        "scheme_version": 1,
        "name": "婴儿哭声检测",
        "output_mode": "audio",
        "overall_status": "failed",
        "status": "succeeded",
        "result": null,
        "error": null,
        "speech": {
          "status": "failed",
          "error": {
            "code": 50200,
            "msg": "模型调用未完成，请查询任务状态"
          }
        }
      }
    ],
    "error": null,
    "created_at": "2026-10-28T07:30:07Z"
  }
}
```

## 设备处理规则

- 判断整个任务是否完成看 data.status；判断某个类型是否完成看 results[].overall_status；展示分析进度看 results[].status，播放准备状态看 speech.status。
- HTTP 上传响应可能晚于 MQTT 通知到达，二者都按 request_id/status_revision 过滤旧快照，不能用迟到的上传响应覆盖新结果。
- 只有 speech.status=ready 时下载语音；按 request_id/item_id 去重，并按 order 排队播放。
- succeeded 表示服务端所需输出准备完成，不表示设备已经下载或播放，更不是设备 ACK。
- 配置变更通知保持原结构，只携带 device_id 和 revision；不能把配置 revision 当作任务 status_revision。

## 实施与验收边界

新增 name、overall_status 是字段扩展；data.status 的等待阶段语义与纯语音合成失败的聚合规则属于行为调整，必须与设备消费者一起验证，不能只更新文档后声称服务已生效。

实施时核对 Python 模拟器、用户 Web、微信小程序及设备示例中的消费者；有识别逻辑的消费者按新规则适配。历史任务名称从所保存的类型版本快照读取，整体状态按已保存的阶段结果推导；读取历史任务不能重新调用模型或扣费。

验收覆盖三种返回方式、多个类型顺序执行、文字完成后等待语音、语音失败、类型取消、unknown 暂停、HTTP/MQTT 重复及乱序、任务重启恢复、历史任务查询与名称快照。通过后同步 API Reference 和接入指南；本方案不引入新路由或额外的协议版本字段。
