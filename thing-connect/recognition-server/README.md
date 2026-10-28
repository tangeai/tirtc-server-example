# AI识别服务

独立音频分析服务，默认 HTTP 端口9006。配置示例见 [config.yaml.example](config.yaml.example)，设备协议与管理流程见 [音频分析接入](../device-recognition.md)。

```bash
# 在 thing-connect 目录执行
./build.sh recognition-server
./bin/recognition-server -c recognition-server/config.yaml
```

数据库结构由迁移账号通过项目迁移流程安装；服务只做只读版本检查。MySQL、Redis、MQTT 是 readiness 必需依赖。服务注册名称 recognition-server，服务发现字段 recognition-srv，HTTP 前缀 /v1/recognition。

与其他业务服务共享 jwt_secret 和 internal.key。Admin 使用独立令牌。识别 Key 支持 YAML 回退：Admin 无已发布配置时使用 recognition 段，发布后原子应用并上报配置 revision。未配置 Key 时服务仍可管理配置，但拒绝新模型任务。MQTT 配置与现有服务一样为重启生效配置。

接收 SIGTERM 后停止 worker 和动态配置轮询，完成有界状态写入、等待后台任务退出，再关闭依赖。音频分析使用90秒超时，数据库领取租约为120秒。任务、原始媒体、完整语音、逐次用量与通知保存在 MySQL；运维应将识别表纳入备份、访问控制和容量监控。原始音频在父任务终止后清除，历史文字及合成文件保留，需按业务保留要求规划存储。

一期支持 PCM S16LE 原始音频、PCM WAV 和千问；媒体类型与提供商 adapter 分离，为后续图片、视频保留扩展边界，但当前接口拒绝这些输入。服务不依赖 TiRTC AI 通话会话。

## 并发与容量

每个服务实例使用一个后台 worker，一次执行一个分析或语音合成调用；多设备上传先持久化排队。同一任务的多个识别类型按配置顺序执行。并发上传通过设备行锁检查额度，相同 request_id 重试不重复预占次数；不同 request_id 分别计费。

多个实例共享数据库时通过 `FOR UPDATE SKIP LOCKED` 领取任务，租约令牌隔离迟到结果。实例必须使用各自唯一的 MQTT ClientID。通知允许重复，设备按 request_id 和 status_revision 去重；发送成功状态不会被迟到的失败回写覆盖。模型调用与通知投递共用 worker，模型响应慢时也会延迟通知；设备可通过任务查询接口补查。

容量验收需记录并发设备数、排队时长、上游限流、数据库连接数和任务完成延迟。单元测试不代表吞吐量承诺；扩容还受千问账号的并发和频率限制约束。

## 返回方式

每个识别类型的发布版本指定 output_mode。text 只调用声音分析，speech.status=skipped，不创建 TTS 调用或合成文件；audio 内部生成分析文字并合成语音，设备 result=null；text_audio 返回文字后继续合成语音。返回方式不由上传请求覆盖。语音失败不会删除 text_audio 已完成的文字结果，unknown 不自动重试收费调用。

## 音频识别验证

从 `thing-connect/` 执行不依赖数据库、MQTT 或千问 Key 的测试：

```bash
go test ./internal/recognition ./internal/recognitionprovider ./recognition-server/handler -v
```

模拟上游响应覆盖音频限制、结果解析、Token 读取、语音下载、失败分类与接口鉴权。模拟测试不能证明模型对真实哭声的识别效果。

直接验证真实千问时，准备 PCM16 WAV（不超过5 MiB、60秒），执行：

```bash
read -rsp 'DashScope API Key: ' DASHSCOPE_API_KEY
export DASHSCOPE_API_KEY
export RECOGNITION_LIVE_TEST=1
export RECOGNITION_AUDIO_FILE=/absolute/path/crying.wav
# 可选：同时合成返回语音，并保存用于试听。
export RECOGNITION_TEST_TTS=1
export RECOGNITION_OUTPUT_DIR=/tmp/recognition-audio-test
# 可选：已知哭声样本期待报警；正常声音样本设置 false。
export RECOGNITION_EXPECT_ALARM=true
go test ./internal/recognitionprovider -run '^TestLiveAudioRecognition$' -v -count=1
unset DASHSCOPE_API_KEY RECOGNITION_LIVE_TEST
```

真实测试会产生模型费用，输出分析文字、报警判断、声音标签和上游返回的 Token 用量；只有 RECOGNITION_TEST_TTS=1 时才执行合成并输出 WAV 路径。使用可公开的测试素材，Key 不写入代码或命令参数。`RECOGNITION_MODEL` 可选择账号支持的音频理解模型，默认 `qwen3-omni-flash`。未显式启用时显示 `SKIP`；启用但缺少 Key 或有效音频时失败。

此测试直接验证千问适配器，不依赖运行服务。设备上传、数据库额度、MQTT 通知需单独集成验证；识别效果应使用哭声、争吵、日常交谈、安静和混合噪声样本分别验收。

## MQTT 通知授权验收

识别服务的 MQTT 账号需要发布 `device/+/notify`，并订阅公共 Broker adapter 使用的 `device/+/up` 和 `$SYS/brokers/+/clients/+/connected`、`$SYS/brokers/+/clients/+/disconnected`。用户名认证可将 [ACL 片段](../deploy/emqx/recognition-acl.conf) 中的用户名替换为管理后台实际值，合并到 EMQX 当前生效的文件授权源，并置于通用拒绝规则之前；固定 ClientID 认证则按该 ClientID 配置对应规则。

“连接成功”和 QoS1 发布确认不能替代授权验收。EMQX 使用 MQTT 3.1.1 且拒绝策略为 ignore 时，发布端可能收到确认，但消息被 ACL 丢弃。验收必须确认设备订阅的 notify topic 实际收到结果，并检查 Broker 日志不存在 `cannot_publish_to_topic_due_to_not_authorized`。Dashboard 管理过的文件授权源可能使用 `data/authz/acl.conf`，以运行配置为准，修改后热加载并清理该账号连接的授权缓存。

## 端到端验收与故障定位

1. 发布识别类型并在用户设备设置中选择对应版本、开启识别；检查 effective_enabled=true。
2. 分别以 text、audio、text_audio 受理音频任务，保存 request_id。核对仅文字任务没有 tts 调用，语音类型在分析成功后产生合成调用；用量以提供商上报为准。
3. 验证设备现有 notify 订阅实际收到结果，按 status_revision 去重；语音 ready 后鉴权下载，并在目标设备验证解码与播放。
4. 验证上传重试、离线补查、额度不足、TTS 失败和结果未知。HTTP 查询成功不代表模型任务成功；检查子项 error 和 speech.error。

任务成功但通知缺失时，先检查 outbox 尝试次数，再检查 Broker 发布 ACL、设备订阅与真实转发。通知调度统一使用 UTC；数据库会话为 UTC+8 时也应立即领取首次通知，后续失败才按退避时间投递。delivered 仅表示发布侧收到 Broker 确认，不是设备结果 ACK。

数据库回归测试使用专用 `_test` 库和 Redis，在 `thing-connect/` 执行：

```bash
THING_CONNECT_TEST_CONFIG=tests/testdata/config.yaml go test ./internal/store/mysql -run TestRecognition -v -count=1
```

测试输出中的 SKIP 表示依赖未就绪，不能当作数据库场景通过。单元测试、适配器真实调用、数据库与 MQTT 集成、设备播放分别记录验收结果；本说明不承诺某个开发环境已经通过全部测试。
