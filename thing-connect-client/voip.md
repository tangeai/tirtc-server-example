# 微信 VoIP 接入

本指南说明 ThingConnect 小程序与设备的双向微信通话、授权和画面配置。安装与构建见 [README](README.md)，设备侧接听、外呼、MQTT 和媒体协议见[设备 VoIP 接入](../thing-connect/device-voip.md)，HTTP 字段及错误码以 [API Reference](../thing-connect/api-reference.md) 为准。

## 接入准备

使用具备 IoT VoIP 权限的正式 AppID，并在微信后台开通 `wmpf-voip` 插件。插件声明在 [src/app.config.ts](src/app.config.ts)，运行入口在 [src/platform/media.weapp.ts](src/platform/media.weapp.ts)。

`callDevice` 从插件 2.4.0 开始支持，设备显示名需要 2.4.1，项目使用的视频编码旋转参数需要 2.4.5。项目声明使用 `latest`，上传前应在微信开发者工具中确认实际解析版本满足所用能力。

设备须处于正式 MQTT 在线状态，并上报 `profiles.voip`。实时播放使用 `profiles.stream`，设备互呼使用 `profiles.call`，三者不能互相补充缺失字段。手机是否发送摄像头视频取决于设备的屏幕能力，手机是否接收设备视频取决于设备摄像头能力；两端均无视频时使用纯音频。

## 微信授权

微信设备授权允许**设备主动呼叫当前微信用户**。微信官方说明，通过 `callDevice` 从手机呼叫设备本身不要求该授权；ThingConnect 客户端的通话流程仍检查微信授权和服务端授权关系，以同时支持设备回呼。区分平台要求与项目流程，不要仅删除前端判断而忽略联系人和回呼关系。

授权相关的两种身份：

- ThingConnect 用户 JWT：验证当前账号是否拥有设备。
- 微信 OpenID：`wx.login()` 获取 code 后，由 `POST /v1/voip/user/wechat-mini-login` 换取，关联当前微信用户。

授权步骤如下：

1. 查询 `wx.getDeviceVoIPList`，结合服务端授权列表判断微信状态和同步状态。
2. 首次授权前，通过 `POST /v1/voip/user/sn-ticket` 获取票据及设备名称；设备需要先有名称。
3. 调用 `wx.requestDeviceVoIP`，传入设备 SN、ModelID、票据和名称，由用户操作微信授权弹窗。
4. 通过 `POST /v1/voip/user/report-auth` 同步授权关系，设备通过联系人变化通知获取当前联系人。

| 微信状态 | 接口行为与客户端处理 |
|---|---|
| 没有授权记录 | 请求授权，用户同意后同步服务端 |
| 已授权 | 重复请求可通过 `fail` 回调返回 `errCode: 10001`，按已授权处理，不能视为创建了一次新授权 |
| 拒绝或在设置中关闭 | 再调用授权接口不会弹窗，引导用户到小程序设置手动开启 |
| 授权记录清空 | 可以重新请求授权；微信说明在“最近使用”中删除小程序会清空记录 |
| 状态读取失败 | 保持待确认，允许重试；不推断用户已撤销授权 |

设备菜单的“微信通话授权”入口按当前状态处理：已有授权或已关闭时打开微信设置；无记录时申请授权。重复授权返回 `10001` 时，上报 `authorization_created: false`，保留授权名称快照。

`wx.requestDeviceVoIP` 使用 `success/fail` 回调，本身不支持 Promise 风格。实现通过回调封装等待；不要直接把该 API 的返回值作为授权完成信号。

依据：[微信授权指南](https://developers.weixin.qq.com/miniprogram/dev/framework/device/voip/auth.html)、[授权 API](https://developers.weixin.qq.com/miniprogram/dev/api/open-api/device-voip/wx.requestDeviceVoIP.html)、[腾讯云重复授权说明](https://cloud.tencent.com/document/product/1081/138442)。

## 设备名称与联系人备注

| 名称 | 作用与修改方式 |
|---|---|
| 设备名称 | 用户设备列表中的名称；空名称时点击卡片“设置名称”，已有名称在更多菜单修改，限制为 1–13 个字符 |
| 微信授权名称 | 授权时显示及保存的设备名称；不能假定修改业务名称或重复调用授权 API 会覆盖它 |
| 微信联系人备注 | 某台设备对某个微信身份的独立备注，用于该设备的联系人列表及来电识别 |

设备改名后，已有微信授权的名称可能仍是授权时名称；客户端使用服务端保存的授权名称快照。需要重新建立授权时，按微信提供的清空授权流程处理，再使用当前名称授权；仅关闭再开启开关不等于清空记录。微信来电页的最终显示应在真机上验证。

联系人备注通过联系人页编辑，不通过重新授权修改。备注按 `device_id + wx_open_id + wx_app_id` 独立保存，修改仅影响指定设备，空字符串表示清空。

保存微信联系人备注后，服务端向 `device/sn_{device_id}/notify` 发布 `callers_update`，`channel` 为 `device`，payload 包含 `action: "remark"`、`contact_type: "voip"` 和 `peer_id`。通知不携带新备注文本，设备需重新读取联系人；重复或乱序通知按刷新当前状态处理。授权增删通知也使用 `callers_update`，但 `channel: "wx"`、payload 为空，不能按同一种 payload 强制解析。

接口定义见[修改联系人备注](../thing-connect/api-reference.md#put-v1callusercontactsremark)及[设备联系人查询](../thing-connect/api-reference.md#get-v1calldevicecontacts)。

## 发起与接收通话

### 小程序呼叫设备

客户端通过微信插件创建房间，不通过业务 HTTP 接口创建微信房间：

```text
小程序 callDevice
  → 微信 iot_voip_notify 回调
  → voip-server
  → MQTT call_incoming（channel=wx）
  → 设备确认通知并接听
```

项目传入 `isCloud: true`，以触发服务端回调；`payload` 用于外呼关联，由服务端作为 `wx_payload` 下发。`callDevice` 成功返回 `roomId` 后，客户端保存设备 ID、房间号和账号会话，再进入插件 `CALL_PAGE_PATH`。

失败通过异常处理，调用结束释放创建中的锁。`timeLimit` 是最大通话时长，不是创建房间的接口超时；项目不为 `callDevice` 增加独立的强制超时。接口参数见[微信 callDevice](https://developers.weixin.qq.com/miniprogram/dev/framework/device/voip-plugin/api/callDevice.html)。

### 设备呼叫小程序

来电可能直接拉起插件通话页，入呼初始化在应用级完成：

1. 应用启动初始化插件，读取 `getPluginEnterOptions`；应用显示时补读入呼参数。
2. 插件触发 `callPageOnShow` 时，读取 `getPluginOnloadOptions` 并设置画面参数。
3. 同一事件中安排一次 `0ms` 延迟补设置，兼容插件来电页初始化时序。

这两次 UI 设置有时序用途，不能直接合并。`src/platform/voip/voip-incoming-query.js` 兼容对象、字符串以及包含字面量 `\u0026` 分隔符的入呼参数；缺失的视频字段可由 `profiles.voip` 缓存补齐。冷启动来电仍需设备提供可用参数，并通过真机验证。

### 结束与异常收尾

事件监听在插件初始化时注册一次，不随页面反复显示而重复注册。对主动呼叫，客户端按 `roomId` 关联终止事件：

- `busy` 和 `timeout` 分别提示占线和无人接听，并释放本地当前房间。
- `cancelVoip`、`abortVoip`、`hangUpVoip`、`joinFailCaller` 清理当前房间，并在账号会话仍匹配时通知 `/v1/voip/user/cancel`。
- `rejectVoip`、`endVoip`、`finishVoip` 可结束对应本地状态；`finishVoip` 是晚于结束事件的结算事件，不依赖它才能释放状态。
- 已处理或旧房间的事件不再改变当前通话。

创建房间成功但通话页打开失败时，先解除本地房间关联，再调用 `forceHangUpVoip(roomId)` 和服务端取消。任一清理失败不会阻止另一侧执行；未完全确认结束时给出提示。强制结束必须传房间号，避免误挂其他通话。

官方约定见[通话事件](https://developers.weixin.qq.com/miniprogram/dev/framework/device/voip-plugin/api/onVoipEvent.html)和[强制结束通话](https://developers.weixin.qq.com/miniprogram/dev/framework/device/voip-plugin/api/forceHangUpVoip.html)。

## 视频画面配置

`caller` 和 `listener` 表示当前通话的角色，不能固定等同于手机或设备。

| 方向 | 手机角色 | 设备配置位置 |
|---|---|---|
| 小程序呼设备 | caller | 设备旋转、镜像、缩放写入 listenerUI |
| 设备呼小程序 | listener | 设备配置写入 callerUI；来电页还需同步到 listenerUI，手机本机旋转保持 0 |

实现位于 `src/platform/voip/`，归一化和映射规则由 `tests/voip-ui-config.test.mjs` 验证：

- 手机本机 `cameraRotation` 固定为 `0`；设备旋转、水平/垂直镜像及缩放来自 `profiles.voip`。
- 手机容器比例使用 `screenHeight / screenWidth`，不使用设备屏幕或素材分辨率。
- 设备呼小程序且 `object_fit=contain` 时，两端 `aspectRatio` 使用手机屏幕比例；获取不到时保留可用 profile 比例。
- `down_video_rotation` 控制手机发送给设备的视频编码方向，值为 `1` 或 `2` 时映射为 `encodeVideoRotation`；缺省或 `0` 时不传。它不属于 `setUIConfig`。
- 未上报字段保持未配置，保留插件默认行为；来电参数优先，缓存按字段回退。

## 问题排查

| 触发条件或症状 | 检查方法与处理 |
|---|---|
| 授权接口未弹窗 | 先查询微信状态；已授权按同步处理，拒绝或关闭时去设置开启，检查票据有效期及 AppID/ModelID |
| 微信已授权但设备没有联系人 | 检查 `wechat-mini-login` 和 `report-auth` 是否成功，设备是否收到 `callers_update` 后重新拉取联系人 |
| 点击呼叫但设备无来电 | 检查正式 MQTT 在线、`profiles.voip`、微信回调路由、`isCloud` 与设备接收 `call_incoming` 的链路 |
| 设备外呼未弹出来电页 | 核对有效授权、OpenID、AppID、ModelID及外呼结果；HTTP 成功不能代替后续微信回调与媒体验收 |
| `contain` 看起来与 `fill` 相同 | 检查当前通话方向和手机容器比例，避免设备素材比例覆盖手机容器比例 |
| 视频仅一个方向正常 | 分别检查设备在当前方向的 caller/listener 角色，画面旋转与发送编码方向分开验证 |
| 插件没有 `callDevice` | 在开发者工具检查实际解析的插件版本及 AppID 插件权限 |
| 取消后设备仍响铃 | 检查房间号关联、服务端取消结果及设备 MQTT `call_cancel` 处理；结束通知失败时检查设备实际状态 |
| 模拟器提示不能通话 | 用正式 AppID 的手机微信验证；模拟器只用于页面与 HTTP 联调 |

完整验收矩阵见 [README：检查与验收](README.md#检查与验收)。诊断记录应包含场景、微信与插件版本、设备 profile 和脱敏错误信息，不记录 JWT、设备密钥或完整微信用户标识。
