# 设备音频分析接入

AI识别由独立的 `recognition-server` 提供，与 AI 通话、VoIP、设备通话服务分别部署。服务端接收一段完整音频，按用户选择的识别类型依次分析，异步返回文字、完整语音或两者。图片、视频及流式输出不接受。

## 管理与用户配置

管理后台“服务配置 → AI识别 → 识别类型”中创建类型：填写名称、公开说明、提示词、声音理解模型、文件与时长限制，勾选“文字”和/或“语音”。语音使用该类型的音色及识别服务配置中的 TTS 模型。保存草稿后发布，用户才能选择；编辑不会覆盖已经发布的版本。停用类型阻止新调用及尚未领取的子任务。

后台“服务配置 → AI识别 → 服务配置”配置 `recognition.models` 的千问 API Key、音频分析 API 地址、TTS 地址和模型，以及 `mqtt.connection`。凭证不下发设备，不进入用户接口。千问声音理解使用兼容 Chat Completions，语音合成使用非流式 HTTP 接口；必须选择账号地域支持的模型及音色。参考[千问 Omni](https://help.aliyun.com/en/model-studio/qwen-omni)及[千问 TTS](https://help.aliyun.com/en/model-studio/qwen-tts-api)。

用户设备卡片中的“AI识别”默认关闭。用户添加多个类型、通过拖动手柄或菜单排序，开启并保存后生效。后台可以禁止设备使用或调整月上限，不覆盖用户选择和开关。

## 设备接口

服务根地址使用服务发现的可选 `recognition-srv`；未提供时不能推断 AI 通话服务支持识别。统一代理部署可以使用站点根地址。所有设备请求使用 `Authorization: Bearer <mqtt_token>`，临时设备凭证不能使用。

| 方法与路径 | 内容 |
|---|---|
| `GET /v1/recognition/config` | 读取 policy、公开 schemes、有效 upload_limits、usage 和 effective_enabled |
| `POST /v1/recognition/requests` | multipart 上传并受理异步任务 |
| `GET /v1/recognition/requests/:request_id` | 查询任务当前快照 |
| `GET /v1/recognition/requests?cursor=0` | 按游标递增补查，最多50条，返回 items/next_cursor |
| `GET /v1/recognition/requests/:request_id/audio/:item_id` | 鉴权下载完整 WAV 音频，响应不缓存 |

设备依据 effective_enabled 决定是否上传，不能只检查用户开关。上传接受一个 PCM S16LE 原始音频文件或16位 PCM WAV，1–60秒、8–48kHz、1–2声道。原始 PCM 不带文件头，不压缩，不重采样，双声道按采样帧交错排列；不能用 A-law 或 MP3 文件改名代替。服务端只为原始 PCM 补44字节 WAV 文件头，采样数据保持不变，再交给模型。平台最多5MiB；原始 PCM 按文件字节数加44字节计算大小限制。实际还受所选各类型更小的文件和时长上限限制。multipart 请求体最多6MiB。

| multipart 字段 | 说明 |
|---|---|
| audio_format | 原始 PCM 必填 `pcm_s16le`；WAV 可填 `wav` 或省略以兼容既有设备 |
| sample_rate | 原始 PCM 必填整数采样率（Hz），8000–48000；WAV 不填写 |
| channels | 原始 PCM 必填 `1` 或 `2`；WAV 不填写 |
| files | 单个音频文件；PCM 使用 `application/octet-stream`，WAV 使用 `audio/wav` |

16kHz、单声道、10秒的原始 PCM 为320000字节，可直接上传设备采集缓冲区：

```bash
curl "$RECOGNITION_SERVER/v1/recognition/requests" \
  -H "Authorization: Bearer $DEVICE_TOKEN" \
  -F 'request_id=audio_000001' \
  -F 'config_revision=1' \
  -F 'media_type=audio' \
  -F 'audio_format=pcm_s16le' \
  -F 'sample_rate=16000' \
  -F 'channels=1' \
  -F 'files=@recording.pcm;type=application/octet-stream'
```

config_revision 使用配置响应中的 policy.revision。request_id 为设备范围唯一的1–64位字母、数字、下划线或连字符。首次受理返回 HTTP 202、code=200 和 queued 任务；不等待推理完成。重试相同编号、音频内容（含采样率、声道）及配置版本返回原任务；同编号不同内容/配置返回409，不重复扣量。旧配置的新请求也返回409，设备须刷新并确认用途。

## 结果和播放

HTTP 与 MQTT 结果都使用 code/msg/data。data 包含 request_id、device_id、status_revision、status、results[]、error。results[] 按用户顺序包含 item_id、order、scheme_id、scheme_version、output_mode、status、result、error、speech。

| 返回方式 | output_mode | 设备文字结果 | 语音处理与用量 |
|---|---|---|---|
| 文字 | text | 分析成功后返回 summary/alarm/labels/details | 从任务受理时起 speech.status=skipped；不调用 TTS，不产生该任务的合成字符用量或语音文件 |
| 语音 | audio | result=null；内部仍生成分析文字用于合成 | 分析成功后调用 TTS；ready 时提供完整 WAV 下载 |
| 文字＋语音 | text_audio | 分析完成即返回文字 | 随后调用 TTS，完成后再通知 ready；语音失败保留文字 |

返回方式属于类型发布版本，每个选中类型独立生效，不是设备上传参数。一段音频可以同时选择文字类型和语音类型，服务端分别处理。修改草稿或发布新版本不会改变已受理任务的快照；后续任务按设备实际选择的 scheme_version 执行。仅文字也会消耗分析次数及分析 Token，不能理解为无模型用量。`skipped` 表示不需要合成，不是错误。

summary 为中文说明；alarm 表示模型按管理员分析目标判断是否异常，不能将此标志解释为经过人工确认的真实事件。labels 是声音类别，details 可说明不确定性。未成功分析时 result=null，不能把它当作“无异常”。摘要最多500字，超长或非法结构作为明确失败处理，不静默截断后播报。

父任务状态为 queued/processing/succeeded/partial_failed/failed/unknown/cancelled。当前协议在文字完成、等待语音或下一类型时可能回到 queued，不表示识别结果丢失；结合子项与语音状态显示进度。子项分析状态独立于 speech.status（pending/synthesizing/ready/failed/unknown/skipped）。明确失败后继续下一个类型；未知结果暂停，不自动重新调用模型。设备应展示失败或待确认，不能当作已经完成。

MQTT topic 沿用 `device/sn_{device_id}/notify`，QoS1、retain=false，type=recognition_result、channel=recognition、event_id 为事件标识，payload 是固定响应信封。同一任务每次更新递增 status_revision，设备忽略旧版本。配置变化 type=recognition_config_changed，设备重新读取配置。通知最多尝试投递8次，失败后由查询补偿；MQTT 发布确认不代表设备收到或完成播放，当前没有设备结果 ACK 接口。文字随通知直接返回，语音就绪后通过 HTTP 下载。重连时逐个补查本地未完成任务；列表 cursor 只按任务创建顺序分页，不会因旧任务更新而变化。

speech.url 是本识别服务的相对路径，必须相对 recognition-srv 请求并附设备凭证，不能当作无需鉴权的公开下载地址。按任务编号和 item_id 去重播放，按 order 排队；不能因重复通知重播，也不能打断设备现有通话。硬件音频格式适配与会话仲裁由设备接入层实现；服务端提供 WAV 完整文件，不提供实时音频会话。

解绑递增绑定生命周期版本，历史任务、下载和通知不向新绑定开放，识别开关及选择重新默认为关闭/空。

## 用量与限额

每设备默认每 UTC 自然月100次，后台可以调整为0至1000000，0禁止新任务。一次上传选择 N 个类型会原子占用 N 次，避免并发超额；受理后的失败/未知仍占次数，不自动退款或重跑。每次分析和 TTS 调用分别记录提供商上报用量，缺失数据记为未知。分析 Token 与 TTS 字符等不同单位不混加。类型版本保留在任务快照中。

后台识别记录可按设备查看最近50条任务及每次分析/TTS用量，设备额度页显示月使用量、分析输入/输出 Token、合成字符与未知调用数。

## 错误及恢复

400/40000：参数或音频不合规；413/40000：请求体超过上限；401/401：凭证无效；403/40300：设备归属不符；404/40400：任务或音频不存在；409/40901：开关关闭、配置冲突或类型不可用；429/429：月额度不足；503/50300：模型 Key 未配置；500/50000：内部错误。

模型分析和合成失败在子项 error/speech.error 中返回50200，读取任务本身仍是 HTTP200、code200。结果未知时继续查询原编号，不改编号盲目重试。服务端领取任务的120秒租约到期后标记 unknown，防止进程重启重复调用已经收费的模型。

管理后台“业务管理 → AI识别”提供设备用量、额度设置和识别记录；“服务配置 → AI识别”提供识别类型及 MQTT、模型连接配置。页签按管理员已有权限显示，菜单授权与接口权限分别检查。

## 接口版本与验收边界

可调用字段以 [API Reference 当前协议](api-reference.md#recognition-config) 为准。该文档末尾的“待确认的结构整理方案”是评审草案，不应按 items/recognition/audio 等草案字段解析现有 v1 响应。

验收分别覆盖仅文字、仅语音、文字＋语音；核对每个任务的 analysis/tts 调用记录、额度占用、重复请求不重复扣量、重复/乱序通知过滤，以及语音下载鉴权和设备实际播放。未知用量保留未知，不与0混淆。测试结果须区分模拟单元测试、真实 MySQL/Redis 集成、真实千问调用、MQTT 实际转发和设备播放，跳过的场景不视为验收通过。
