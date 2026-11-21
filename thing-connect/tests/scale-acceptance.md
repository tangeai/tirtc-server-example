# 容量与故障验收

本流程用于独立测试环境。准备与业务部署分离的压测机、测试 Broker、MySQL 和 Redis；各机同步时钟。记录 Git commit、迁移版本、配置 revision、实例数、CPU/内存、文件描述符上限和 Broker 连接限制。单机功能通过不等于容量、真实 ACL、媒体或多机恢复通过。

## HTTP token 负载

`python3 tests/scale_load.py` 使用标准库，向真实 `/v1/device/token` 发起 HMAC 请求。准备已绑定测试设备的私有 CSV，表头 `device_id,device_key`；文件设为仅当前用户可读，不提交 Git。每次请求生成独立 Nonce，不本地签发 MQTT token，不自动重试。

```bash
python3 tests/scale_load.py \
  --url https://test.example.com/v1/device/token \
  --fixtures /private/test-devices.csv \
  --run-id token-baseline --duration 60 --rate 10 --workers 16 \
  > token-baseline-0.jsonl
```

`--rate` 是每台压测机的目标请求速率，`--workers` 是并发上限，不会无限堆积提交任务。输出不含凭证或响应正文；HTTP 成功但业务失败也计为失败。`dropped` 表示压测机无空闲 worker 或未能按时调度，不能当成服务器成功或从分母中删除。`--timeout` 是网络 I/O 等待上限，默认 10 秒；压测机也须监控线程和网络资源。

分布式运行使用相同 CSV、`--run-id`、`--shards N` 和未来的 `--start-at` Unix 秒时间戳，每台分别指定 `--shard 0..N-1`。工具按 CSV 行号分片，同一设备不会同时分配给多个节点。各节点速率之和为总速率；十万次/60 秒约为总计 1667 请求/秒，先逐级提高负载并检查压测机自身是否饱和。

```bash
python3 tests/scale_load.py --merge token-baseline-0.jsonl token-baseline-1.jsonl
```

汇总拒绝重复分片、缺失分片，以及运行标识、起始时间、持续时间或直方图规格不一致的结果。各节点可使用不同速率和并发上限。p50/p95/p99 是直方图区间上界，不是精确分位点；超过最后一个桶显示 `>30000`，不截断成 30 秒。最终存在失败、丢弃或提前中断时退出码为 1；故障注入时保留该结果，按时间线分析恢复，不能删除失败样本。

## MQTT 稳态负载

`tests/presence-load` 使用真实 MQTT 连接和 QoS 1 心跳。部署链路验收使用 `-consumers 0`，让真实 Device 实例处理事件；Redis 密码可通过 `LOAD_REDIS_PASSWORD` 注入。

真实认证模式指定 `-fixtures` 和 `-token-url`，各节点使用相同 CSV 和 `-shards N`，分别指定 `-shard 0..N-1`。设备数量取本分片 CSV 行数，此时不使用 `-devices` 或 `LOAD_JWT_SECRET`。每次 MQTT 连接及重连以独立签名换取 token；单次 HTTP 总期限 10 秒，每节点同时最多 32 次换取，达到上限计入 `token_admission_rejected`。下次 MQTT 重连尝试会再次换取 token；`token_failures` 和订阅失败必须保留在验收结果中。正常连接需同时获准订阅本设备 cmd/notify。收到 cmd 时回复收包 ACK，通知只计数；工具不执行真实通话、解绑或媒体业务。

```bash
go build -o /tmp/presence-load ./tests/presence-load
/tmp/presence-load -broker tcp://test-broker:1883 \
  -redis test-redis:6379 -redis-db 15 -consumers 0 \
  -fixtures /private/test-devices.csv \
  -token-url https://test.example.com/v1/device/token \
  -shards 2 -shard 0 -prefix real-node0- \
  -connect-rate 100 -heartbeat 30s -duration 10m -json > real-node0.jsonl
```

只隔离测量 Presence 管线时，可省略 CSV 和 token URL，改用 `LOAD_JWT_SECRET` 本地签发测试 token；这是合成身份模式，不能替代真实签发和 ACL 验收。凭证只限专用测试环境。

```bash
go build -o /tmp/presence-load ./tests/presence-load
/tmp/presence-load -broker tcp://test-broker:1883 \
  -redis test-redis:6379 -redis-db 15 -consumers 0 \
  -prefix run01-node0- -devices 10000 -connect-rate 100 \
  -heartbeat 30s -duration 10m -json > presence-node0.jsonl
```

多机使用不同的 `-prefix` 和相同的未来 `-start-at` Unix 秒。输出每 5 秒包含本分片连接、在线数、心跳、发布失败、重连、token 请求、订阅、收包和 ACK 计数，以及 Redis 中业务实例的状态、配置 revision、队列和连接池指标。服务指标是全环境快照，各分片的重复快照不可相加。`status_sample_failed=true` 表示无法读取状态，不代表零积压；`online_sample_failed=true` 时在线数不能作为有效样本。真实认证模式不删除在线投影；合成身份模式退出仅清理自己前缀的在线 key，不清除 Device 拥有的共享补写或缺口状态。MQTT 工具输出观测数据，不自动判定验收通过。

默认心跳初始相位随机。`-sync-heartbeats` 将心跳对齐到墙钟间隔边界，用于同步突发基线；多机须先校时。分别保留正常随机心跳、同步心跳和依赖故障的结果，不混成一份平均值。

先完成 1 万、5 万设备的稳定窗口，再用多机合计 10 万连接、30 秒心跳运行 `-duration 72h`。注意单源 IP 到单个 Broker 地址的临时端口上限；增加压测机或 Broker 监听地址。测试机与业务机分别记录 CPU、RSS、文件描述符、带宽和连接数。采样覆盖率只能证明在线投影，不是心跳端到端 p99；心跳延迟目标须结合 Broker 与 Device 的时间戳/追踪数据独立测量。

## 故障和专项矩阵

每个场景记录注入时间、恢复时间、影响实例、请求成功/失败、最大积压、最老年龄和排空时间。注入动作由测试环境运维执行，负载工具不会停止数据库、修改 ACL 或杀死业务进程。

| 场景 | 操作及通过条件 |
|---|---|
| Device 接管 | 保持两个真实 Device 实例，停止一个；其余实例继续消费，恢复后共享订阅没有重复普通订阅，在线状态和连接 IP 收敛 |
| Broker 重启/网络中断 | 恢复后设备带随机退避重连；缺口被核对、健康恢复，旧连接不能覆盖新 IP |
| Redis 暂停/只读/脚本缓存清除 | 请求在预算内结束；脚本缓存清除可恢复，写失败不被当成在线更新成功；恢复后缺口和积压可见并排空 |
| MySQL 不可达/慢查询 | HTTP 不无限等待；连接 IP 补写保留，恢复后持久化最新快照；单次写成功但还有超时积压时不得显示健康 |
| 三类业务队列 | 断开下游后创建待处理工作；观察数量、年龄、降级，重启 worker 后能继续；替换事件不能被旧确认删除 |
| 真实 ACL | 从非 Broker 本机连接，验证设备只能订阅自己的 cmd/notify、发布自己的 up/ack；跨设备和通配主题拒绝，冒用 ClientID 拒绝；验证服务账号自身的订阅和实际收包 |
| Presence 切换 | 按部署指南迁移共享订阅；核对旧会话清理、重复/乱序事件、消费者掉线和 Broker API 核对恢复 |
| 跨机器恢复 | 不同机器上至少两个业务实例，通过负载均衡访问；停止一台机器后业务可继续，恢复后没有重复领取或丢失待处理工作 |
| 设备协议与媒体 | 使用真实 Python/Linux C 示例及目标硬件检查绑定、重连、ACK、RTC、VoIP、AI、识别和会话互斥；HTTP/MQTT 成功不替代音视频验收 |
| 发布回滚 | 在测试环境备份数据库、配置和产物，演练更新失败；迁移后禁止直接启动旧二进制，恢复整套备份或使用兼容当前 schema 的版本后重新验收 |

恢复成功必须同时满足业务成功率恢复、旧积压排空及连续新样本健康。识别通知耗尽 8 次时停止自动投递，设备按原任务查询补偿；排查并修复 Broker 后，由运维在测试库中对确认需要重投、租约已过期的记录恢复重试，保留 event_id，验证设备去重。不要清空整张队列表来消除告警。

## 验收结果

保存全部节点 JSONL、服务日志、资源曲线、Broker 指标和注入时间线。测试前固定混合业务比例、成功率和 p95/p99 目标，以及故障排空时间目标；未提供业务比例或缺少测量的数据标记为“未验收”。72 小时结果必须覆盖完整窗口，不能用短测外推。最终报告分别列出功能、容量、故障、真实设备、回滚的结论和未覆盖项。
