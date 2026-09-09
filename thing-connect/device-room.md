# 设备多人对讲

多人对讲由 call-server 提供。用户在“我的设备 → 多人对讲”安排设备建房、加入或退出；页面地址为 `/v1/call/room/page?device_id=<设备ID>`，页面和 `/v1/call/room/*` 接口均由 call-server 处理。与 AI 角色管理一致，统一域名部署按路径访问对应服务，user-server 不转发房间请求。

页面中的“返回首页”进入设备列表，设备保持当前房间关系；只有确认“退出房间”才安排设备退出。创建和加入成功后，页面分别显示请求结果及设备实际加入状态，离线或忙碌设备可等待自动恢复。

## 房间与设备

- 房间号是六位 ASCII 数字，保留前导零。密码可留空，设置时为四位数字。密码只用于加入校验，查询接口不返回密码。
- 一个设备只有一个期望房间。加入另一个房间会结束旧房间租约并增加 `assignment_version`。
- 每个房间最多 100 个有效连接，连接中的预占也计入容量。离线设备的期望加入关系不占在线名额；恢复上线后重新申请连接。
- 设备进入 AI、一对一设备通话或微信通话时暂停多人对讲；业务结束后读取服务端最新关系再恢复。暂停不等于退出房间。
- 连续无人连接 24 小时，房间自动关闭。房间号冷却 24 小时后可复用，内部 `room_id` 永不复用。
- 默认 15 秒续租，45 秒租约失效。只有完成 TiRTC `join_room` 确认后才报告 `joined`；失联或续租失败的设备关闭当前音频并重试。

## 控制与恢复

设备使用设备 JWT 调用 `/v1/call/room/device/*`，服务端从 JWT 获取设备身份。H5 使用用户 JWT，只能操作自己绑定的设备。建房、加入和退出须提供 8–64 字符的 `Idempotency-Key`；重试同一操作时沿用原键及请求体。

MQTT 下行使用现有 `device/sn_<device_id>/cmd` topic，消息为：

```json
{"type":"room_assignment_changed","request_id":"<事件ID>","assignment_version":3,"expires_at":1900000000}
```

通知只触发读取 `/v1/call/room/device/assignment`，不携带密码、TiRTC token，也不控制麦克风。设备在启动、MQTT 重连及周期同步时都读取最新关系，不能用消息到达顺序覆盖关系版本。服务端通过事务 outbox 重试投递；设备周期同步覆盖通知丢失。

当 `desired_state` 为 `joined` 且本机资源可用时，设备生成新的 `session_id`，携带 `room_id`、`assignment_version` 和 `session_id` 领取 `connect-token`。该凭证仅给目标设备，不能通过 H5 或 MQTT 获取。所有续租和离开上报必须携带同一组标识；旧会话的迟到上报不能改变新连接。

## TiRTC 音频与本机 PTT

设备以 token 响应中的 `peer_id`、`token` 调用 `TiRtcWhipConnect`。连接成功后通过 `0x2200` 发送 JSON-RPC `join_room`，请求 ID 为 `1`，参数含业务 `room_id`、`device_id`、`input_audio` 和 `output_audio`。音频描述为 `codec`、`sample_rate`、`channels`，支持 8/16 kHz 单声道 G.711 A-law（协议名 `g711a`）、PCM、Opus、AMR。示例要求服务端确认的上下行格式分别与请求一致；格式不匹配时关闭连接。

音频使用流 ID `1`；下行仅接收协商格式对应的 `media`、`flags`，其余帧丢弃。TiRTC 字段定义见[官方设备集成说明](https://docs.tange.ai/products/room/guides/device-integration.html)。

房间事件中的 `room_id` 可以是业务房间 ID，也可以是 `<应用命名空间>:<业务房间 ID>`。设备在当前连接内校验业务 ID 并固定命名空间，重连时清除该映射；上报接口仍使用原始业务房间 ID。

设备处理 `room_snapshot`、`participant_joined`、`participant_left`、`participant_mic_state_changed` 和 `room_closed`，维护有界成员列表。成员列表属于当前连接，重连后重新接收快照。

加入房间默认只收听。只有本机 `room ptt down` 或产品按键打开上行音频；`room ptt up`、业务抢占、断开和关闭流程都关闭发送。`set_mic_state` 只同步显示状态，不能代替本地音频发送开关。多人可同时按住说话，房间服务负责混音。示例不保存多人对讲下行录音。

Python 与 C 示例的命令一致：

```text
room create [四位密码]
room join 六位房间号 [四位密码]
room status
room ptt down
room ptt up
room leave
```

Python `--with-mic` 使用本机麦克风和扬声器，依赖音频扩展包；文件模式在按住 PTT 时发送配置的上行文件。C 通过 `DeviceAdapterV1` 的媒体源、播放 sink 和产品按键接入本机硬件，`DEVICE_BUSINESS_ROOM` 标识该业务。Linux 默认适配器使用文件源，接收音频不落盘；真实扬声器播放需要提供产品 sink。

## 部署与验收

先使用迁移账号执行标准安装或迁移，再以 DML 运行账号启动 call-server。房间表为 `call_rooms`、`call_room_codes`、`call_assignments`、`call_leases`、`call_requests` 和 `call_outbox`。MySQL 持有关系和租约，Redis 执行限流，MQTT 唤醒设备；TiRTC 凭证沿用服务的 `tirtc` 配置。

`call-server` 的动态配置 `room.policy` 包含 `participant_limit`、`empty_ttl`、`presence_heartbeat`、`presence_lease`、`code_cooldown`、`command_ttl` 和 `token_timeout`。未发布动态配置时使用 YAML `room` 值。租约必须大于两倍心跳间隔，领取 token 的超时必须小于租约。容量配置用于新建房间，已有房间保留创建时的容量上限。

验收需覆盖：三台设备进同一房间、至少两台同时 PTT、松键停止上行、忙时暂停与结束后恢复、离线加入、重复通知、旧会话回调、密码错误锁定、100 人容量预占、空房关闭及进程重启。自动测试不能代替实际 TiRTC 混音、麦克风/扬声器回声和长时间弱网验收。

设备加入多人对讲房间后，终端显示房间号和房间 ID；成员快照同步完成后显示在线设备列表，成员进出时自动更新。`room status` 可随时查询列表，包含本机标记及成员的收听／发言状态。

输入 `room status` 会显示本地房间状态，并向房间服务请求最新成员快照；收到快照后输出在线成员人数、设备 ID 和发言状态。加入成功时也主动请求一次快照。若持续显示“等待房间同步”，可输入 `room status` 重新查询，并检查房间服务的 `get_room_snapshot` / `room_snapshot` 信令。

多人对讲的六位房间号用于用户加入；内部房间 ID 在新建时使用 `group_room_` 前缀，与设备一对一呼叫区分。既有房间 ID 保持有效，客户端应将内部 ID 作为不透明字符串处理，不依赖前缀判断业务。设备重启后自动同步原房间；旧连接未释放时等待租约到期再连接，无需重新加入。
