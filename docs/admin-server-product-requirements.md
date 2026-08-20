# Admin Server 产品需求与技术方案

## 1. 背景与目标

ThingConnect 现有服务负责设备上线、用户绑定、AI、微信 VoIP 和设备互呼。运营人员缺少安全、可追溯的入口来查询用户与设备，调整业务策略，以及维护 SMTP、验证码等第三方集成。

新增独立的 `admin-server`，提供管理后台和管理 API。它不替代现有业务服务，而是对其数据和受控管理动作提供统一入口。

目标：

- 让运营可完成用户、设备和额度管理，无需直接操作数据库；
- 让技术人员可安全维护可配置的第三方集成；
- 所有高风险动作可授权、审计和追踪；
- 不通过后台泄露或读取任何密钥明文。

## 2. 用户与权限

| 角色 | 权限 |
| --- | --- |
| 超级管理员 | 管理管理员、角色、业务配置、系统配置、全部用户和设备数据 |
| 运营管理员 | 查询用户和设备、调整额度、强制解绑、查看审计日志 |
| 技术支持 | 查询用户、设备、绑定历史和任务状态 |
| 审计员 | 只读查看统计和审计日志 |

管理后台采用 RBAC。配置发布、账号禁用、强制解绑和批量导入属于高风险动作：需要操作理由、二次确认，并写入审计日志。

数据库初始化预置表中四个角色及其最小权限菜单。首个账号固定授予超级管理员；后续新增管理员必须从已启用角色中选择，可直接使用预置角色，也可先在“权限与菜单”中创建自定义角色。新增管理员不应因为缺少可用角色而被迫授予超级管理员权限。

### 2.1 菜单与权限边界

权限码由代码注册，例如 `user.read`、`user.quota.write`、`device.unbind`、`config.write`、`security.mfa.write`、`role.manage`、`menu.manage`。后台可为角色勾选已注册权限，并管理管理员与角色的关联；权限码不能由后台任意创建，以免出现没有服务端鉴权实现的“虚假权限”。角色与权限的关联保存到 `admin_role_permissions`，Casbin 从该表装载授权规则。角色支持单层父角色，Casbin 在授权计算时继承父角色权限；保存时必须禁止自引用与角色环。

菜单可在后台维护名称、图标、父级、排序、显示状态与进入菜单所需的权限码，并可为角色分配可见菜单树。前端按当前管理员的有效权限和角色菜单授权请求导航菜单，服务端只返回可见且有权访问的菜单。菜单对应的页面组件仍由 `admin-web` 随代码注册；后台不能创建指向未知页面的任意 URL。这样既能由运营调整导航，又不会绕过前端路由和接口鉴权。

### 2.2 管理员认证

- 不提供默认管理员账号和固定密码；首个超级管理员通过 `admin-server` 的一次性初始化命令创建，密码以交互输入或仅在本次进程可见的环境变量传入；
- 管理员密码不少于 12 位，使用 bcrypt 保存；登录按账号和 IP 双维度限频，默认 15 分钟内最多失败 5 次，成功登录后清除失败计数；
- Access Token 默认有效期 15 分钟；Refresh Token 默认有效期 7 天，只在 HTTPS 的 HttpOnly、Secure、SameSite Cookie 中传递，数据库只保存 SHA-256 摘要；
- 每次刷新都轮换 Refresh Token。旧 Token 再次出现时撤销同一 `family_id` 下的全部会话；退出、禁用管理员、修改密码或权限时立即撤销相关会话；
- `auth_revision` 写入 Access Token。认证中间件从 Redis/数据库校验当前版本，管理员状态、密码、角色或权限变化时递增版本并删除缓存，使旧 Access Token 立即失效；
- MFA 采用 RFC 6238 标准 TOTP，不依赖第三方服务商；Google Authenticator、Microsoft Authenticator、1Password 等兼容应用均可通过后台显示的 Secret 或 `otpauth://` URI 绑定；
- 支持只显示一次的一次性恢复码；MFA 总开关开启时所有管理员均须绑定 TOTP。管理域名必须使用 HTTPS，登录与 MFA 校验分别限频。

管理员登录状态机必须明确区分密码认证和完整登录：密码验证通过后，如需 MFA 且已绑定 TOTP，只签发 5 分钟有效、单次使用的 `mfa_challenge_token`，该 Token 只能调用 MFA 校验接口；如策略要求 MFA 但账号尚未绑定，只签发 `mfa_setup_token`，仅允许访问绑定、确认和退出接口。只有 TOTP/恢复码校验成功或 MFA 总开关关闭时才签发 Access Token 与 Refresh Token。挑战 Token、TOTP 尝试和恢复码尝试均按账号与 IP 限频。

首个超级管理员首次登录必须进入 MFA 绑定流程。待确认的 TOTP Factor 10 分钟后失效并可重新绑定；管理员 MFA 被重置后，下次登录重新进入绑定流程。所有部署节点必须启用时间同步，服务端时间偏差超过一个时间步长时告警，避免 TOTP 集体失效。

## 3. 功能需求

### 3.1 数据概览

- 展示用户数、已绑定设备数、可用设备池、近 24 小时绑定/解绑数；
- 展示待投递的解绑清理任务及失败任务；
- 展示最近的管理操作；配置健康与已应用修订在对应服务页面和 VoIP 微信应用列表中展示；
- 固定展示 `device-server`、`user-server`、`voip-server`、`ai-server`、`call-server` 五个服务的状态卡。每张卡显示 `健康/降级/离线`、健康实例数/总实例数、版本、启动时间、最近心跳、依赖状态和已应用配置修订；服务页面展示全部跨主机实例；
- 状态判定以 Redis 心跳中的依赖检查为准：全部有效实例依赖正常时为“健康”，仍有实例但至少一个依赖异常时为“降级”，没有有效心跳时为“离线”。

### 3.2 用户管理

- 按用户 ID、邮箱、状态和注册时间分页查询；
- 查看用户的设备、绑定历史、AI 角色和资源摘要；
- 修改用户剩余绑定额度，记录变更前后值和原因；
- 禁用或启用账号。禁用时递增用户 `auth_revision`；用户端鉴权必须校验账号状态和版本，使已签发 Token 同步失效；
- 触发密码重置邮件；不提供明文密码查看或直接编辑。

### 3.3 设备管理

- 查询设备和设备详情，支持按设备 ID、MAC、归属用户邮箱和绑定状态筛选；
- 展示绑定方式、设备名称、在线/离线状态、最近心跳、首次活跃及绑定/解绑时间；在线状态读取设备 MQTT 心跳维护的 Redis TTL，缓存不可用时明确显示“状态未知”，不能误报为离线；
- 页面分为“用户设备”和“设备池”两个页签。设备池支持按设备 ID 和“可分配/已分配/已解绑保留”筛选，展示导入任务与源文件，不返回或展示 `device_key`；可分配数量只统计从未绑定的空闲设备，已解绑设备保留原设备身份且不重新分配；
- 查看 `device_bind_log` 绑定历史；
- CSV 批量导入设备池。导入弹窗直接说明格式并提供模板下载：前两列固定为 `device_id,device_key`，两个值均必填且最长 64 个字符；设备 ID 不可重复，设备密钥按敏感信息处理且示例值不可用于实际设备。页面提交前校验编码、表头、大小及行数，服务端再次校验格式和重复项并异步输出逐行结果；上传文件限制为 UTF-8/UTF-8 BOM、10 MB 和 10 万行，服务端生成存储文件名并拒绝路径字符，下载结果对公式前缀进行转义，防止路径穿越与 CSV 公式注入；
- 强制解绑须复用现有解绑事务与 `cleanup_outbox`，不得直接更新 `device_bind`。

### 3.4 配置与第三方集成

配置中心按实际运行服务提供独立菜单；除真正被多个服务共同读取的配置外，不按“集成配置”“安全配置”等技术类型混放：

| 菜单/namespace | 主要配置 | 生效服务 |
| --- | --- | --- |
| `device-server` | 设备验证码 TTL、指纹/IP 限频、待处理上限、设备 MQTT Token、MQTT ACK 超时 | `device-server` |
| `user-server` | SMTP、人机验证、邮件模板、邮件验证码 TTL/限频、新用户默认额度、用户 Token、绑定流程 MQTT ACK 超时 | `user-server` |
| `voip-server` | 微信小程序、回调密钥、默认 AppID、VoIP 配置健康状态 | `voip-server` |
| `ai-server` | AI 默认角色、默认资源、资源额度、AI 服务端点 | `ai-server` |
| `call-server` | 每设备联系人上限、呼叫房间 TTL | `call-server` |
| `common`（页面名“通用配置”） | TiRTC Endpoint、App ID、Access Key ID、Secret Key ID | `user-server`、`voip-server`、`ai-server`、`call-server` |

业务配置与系统配置严格分开。每个服务页面只请求自身 namespace 的配置定义和值；通用配置必须在注册表中列出全部目标服务，并展示各目标服务实例的应用状态。`admin-server` 自身的 MFA 与登录会话策略放在“系统配置”菜单，使用 `system` namespace，不混入五个业务服务或“通用配置”。审计保留周期和任务共享目录属于部署策略。“管理员”页面只管理管理员账号、MFA 绑定状态和会话，不承担配置项编辑。

五个服务页面都提供“新增配置项”。点击后只列出该 namespace 下由代码配置注册表声明、当前尚未在 `config_entries` 创建的可选项；管理员填写初始值、测试并发布后创建当前值记录。不能自由输入任意 `config_key`，因为业务服务没有对应解析和校验逻辑时，即使数据库保存成功也不会生效。新增一种配置只需在目标服务和 `admin-server` 的注册表中增加定义并发布代码，数据库仍复用 `config_entries`，不新增业务表或专用 CRUD。

内置配置项必须使用中文字段名、开关、数值框和带中文选项的选择器生成业务表单，不能要求运营管理员直接编写 JSON。密钥字段使用密码输入框并提示“留空保留原值”。只有二次开发者新增了配置定义但尚未补充前端字段元数据时，才允许显示明确标注的高级 JSON 编辑器。

五种服务类型固定由代码声明，不提供“新增服务类型”。服务实例也不由管理员手工录入：无论部署在同机、其他服务器还是容器集群，实例启动后使用唯一 `instance_id` 主动心跳并自动出现在实例明细中；实例下线后由 TTL 自动移除。页面提供刷新、筛选和查看实例详情，不提供可能产生幽灵实例的手工添加按钮。

SMTP 配置支持启用状态、主机、端口、TLS 模式、用户名、发件人和密码，以及“发送测试邮件”。`tls_mode` 取值为 `auto`、`implicit_tls` 或 `starttls`：`auto` 在 465 端口使用隐式 TLS，其他端口使用 STARTTLS；TLS 校验证书和服务器名，不提供明文 SMTP 模式。注册验证码和找回密码邮件模板由后台编辑，分别对应 `namespace=user-server, config_key=email.template.registration_code` 与 `namespace=user-server, config_key=email.template.password_reset_code`；每个模板包含主题、HTML 正文、可选纯文本正文和启用状态，SMTP 以 `multipart/alternative` 投递两种正文。验证码配置提供独立的“启用人机验证”开关：禁用后用户端不加载验证组件，登录、注册验证码发送和找回密码验证码发送入口不再要求验证票据；启用时必须选择并校验一个生产 Provider。系统内置网易易盾（`yidun`）、极验（`geetest`）、阿里云（`aliyun`）和腾讯云（`tencent`）四个 Provider，不开放绕过校验的后台 Provider。编辑表单根据当前 Provider 只显示该供应商所需的公开参数和密钥；切换 Provider 时必须填写新供应商的完整密钥，服务端丢弃旧供应商凭据，防止因字段名相同而误用旧密钥。

模板编辑器只允许使用变量白名单：`{{code}}`、`{{expires_in_minutes}}`、`{{product_name}}`、`{{support_email}}`。服务端只执行这些固定占位符的精确替换，不解析表达式或执行函数；保存时校验变量名、主题长度、HTML/文本长度和邮件头注入字符。后台在无脚本沙箱中提供模拟数据预览，并可通过当前 SMTP 配置向指定地址发送测试邮件。修改模板只影响后续发送的邮件。

注册与找回密码邮件验证码共用独立的 `email.code_ttl` 配置，默认 `5m`，可由安全管理员在后台调整（建议范围 `1m`–`30m`）。该值必须与设备绑定验证码的 `service.code_ttl` 分离：后者继续保持当前设备临时凭证、扫码绑定和重放保护所需的 `190s`，不能因为邮件验证码调整而改变。注册/找回密码 Redis 验证码 TTL和邮件模板中的 `{{expires_in_minutes}}` 读取 `email.code_ttl`。

邮件发送限频不得复用验证码有效期。`user-server/email.send_rate_limit/global` 独立保存限频窗口、单邮箱上限和单 IP 上限，默认值为 `{"window":"15m","max_per_email":5,"max_per_ip":20}`，同时作用于注册和找回密码验证码发送接口；修改 `email.code_ttl` 不改变限频规则。后续两种场景策略不同时，可在配置注册表中拆分为两个配置项，仍复用通用配置表。

管理员选择任一内置 Provider，填写或替换该供应商密钥并发布后即可生效，不需要修改代码或重启服务。统一字段映射为：易盾使用 `captcha_id + secret_id + secret_key`；极验使用 `captcha_id + secret_key`；阿里云使用场景 ID `captcha_id`、RAM `secret_id/secret_key` 以及 `public_config.prefix/region`；腾讯云使用数字 `captcha_id`、云 API `secret_id/secret_key` 和验证码 `app_secret_key`。微信小程序可在 `public_config.mini_program_captcha_id` 指定独立实例，极验和腾讯同时使用 `mini_program_secret_key`。Web 登录页与微信小程序按 `provider` 加载对应 Widget Adapter；新增第五种供应商才需要开发新的前后端 Adapter。

后台只显示密钥是否已配置和最近更新时间，永不返回密码、Secret Key 或其解密值。

TiRTC 配置属于 `common/tirtc/global`，包含 Endpoint、App ID、Access Key ID 和 Secret Key ID。微信应用配置专属于 `voip-server`，配置键为 `voip-server/wechat.apps/global`；支持维护 `default_app_id` 和多个 AppID，每个应用包含启用状态、公开的 `model_id`，以及加密保存的 `secret`、回调 `token` 和 `encoding_aes_key`。存在启用应用时，`default_app_id` 必须指向其中一个启用应用，且每个启用应用必须配置 AppSecret；全部应用停用时允许默认 AppID 为空。发布后仅通知 `voip-server` 原子刷新微信 VoIP 配置快照，不发送给无关服务。后台提供新增、编辑、设置默认和停用，不提供物理删除应用的入口。

管理员 MFA 策略使用 `system/mfa.policy/global`，默认值为 `{"enabled":true}`。TOTP Issuer 是部署配置，不通过数据库动态修改；算法固定为 SHA-1、6 位、30 秒周期，允许前后各一个时间步长。每个管理员的 TOTP Secret 不属于全局配置值，由绑定流程随机生成后加密保存到 `admin_mfa_factors.secret_enc`；恢复码只保存摘要。

`enabled` 是 MFA 总开关，默认 `true`。设为 `false` 后所有管理员登录不再发起 TOTP 挑战，但保留原有 TOTP Secret 与未使用恢复码；重新启用后所有管理员恢复校验。修改开关要求 `security.mfa.write` 权限、当前密码、当前 TOTP/恢复码、操作原因及二次确认，并递增所有管理员 `auth_revision`、撤销全部 Refresh Session。

### 3.5 VoIP 服务与微信应用管理

- 在后台提供独立的“VoIP 服务（voip-server）”菜单，页面顶部显示服务状态，页面内提供“微信小程序”配置区，不把微信应用混入通用配置；
- 小程序列表展示 AppID、是否默认、启用状态、Model ID、凭据是否完整、授权设备数、有效/失效授权数、最近更新时间和配置健康状态；
- 应用详情展示并维护 `default_app_id`、AppID、`enabled`、`model_id`、`secret`、回调 `token` 和 `encoding_aes_key`；Secret 只显示“已配置/未配置”和更新时间，不能回显或导出；
- 支持新增应用、编辑配置、设置默认应用、停用应用和发布；不提供物理删除 AppID 的管理入口；
- 应用详情提供“授权设备”页签，按 `voip_device_auth.wx_app_id` 查询设备 ID、所属用户、设备名称、授权状态、Model ID、授权时间和最近校验时间；
- 授权设备列表同时关联 `voip_device_profile.device_id`，解析展示设备上报的 `screen_width`、`screen_height`、`camera_rotation`、`aspect_ratio`、`hor_mirror`、`vert_mirror`、`object_fit`、`audio_rate`、`audio_channels`、`up_video_mt`、`down_video_mt`、`down_audio_mt`、`no_video` 和 `calling_timeout_sec`，并显示 profile 原始 JSON 与最后上报时间；
- 设备 profile 是设备级媒体能力，同一设备只保存一份，不按 AppID 复制。小程序详情中的设备属性只是通过授权关系筛选后的关联视图；后台只读展示，不能代替设备修改上报值；
- 使用独立权限码 `voip.app.read`、`voip.app.write` 和 `voip.profile.read`；配置发布、默认应用切换、停用应用和凭据替换均写入审计日志。

该菜单复用 `config_entries` 中的 `voip-server/wechat.apps/global` 配置，以及现有 `voip_device_auth`、`voip_device_profile`、`device_bind` 和 `users` 表，不新增微信应用专用配置表。

### 3.6 菜单与权限管理

- 管理管理员：新增、编辑、禁用管理员，重置密码，查看和撤销会话，以及重置 MFA；
- 管理角色：新增、编辑、禁用角色，并勾选已注册的权限码；
- 权限选择器按业务模块分组，主文案使用中文权限名称和用途说明；稳定权限码只作为二次开发标识辅助展示，不允许把一组无说明的英文权限码直接交给运营管理员辨认；
- 管理员授权：为管理员分配一个或多个角色；
- 管理菜单：维护目录/菜单层级、名称、图标、排序、显示状态和所需权限；
- 角色菜单授权：为每个角色分配可见菜单树；按钮和接口仍由 Casbin 权限码控制；
- 角色列表展示直接权限数和继承后的有效权限数；当前账号的侧边导航实时按角色菜单、菜单祖先和权限码生成；
- 角色、权限、菜单与管理员关联变更均写入审计日志，并使该管理员的现有会话权限即时失效或强制刷新。

### 3.7 审计与任务

- 记录每个写操作的操作者、角色、请求 ID、目标、原因、前后摘要、IP、结果和时间；
- 支持按操作者、资源类型、动作、目标和时间范围检索；
- 配置变更在操作日志中记录操作者、理由和脱敏后的变更前后值；
- 独立记录管理员登录成功与失败、登录 IP、User-Agent 和失败原因；
- 可查看设备导入任务的逐行结果、错误和重试次数，可下载导入结果；任务中心同时展示清理 outbox 与配置发布队列的待处理数量。

### 3.8 数据字典管理

- 管理字典类型：编码、名称、状态和备注；
- 管理字典项：显示名称、存储值、排序、默认项、状态、扩展 JSON 和备注；
- 字典编码和同一字典内的存储值唯一；字典编码发布后不可修改；同一字典最多一个启用的默认项，保存时在事务内加锁校验；
- 已发布的字典类型和字典项只允许停用，不允许通过后台物理删除；如需离线清理，必须由代码中的“字典使用登记”确认没有配置、表单或业务数据引用，不能依赖通用 SQL 猜测引用关系；
- 提供按字典编码获取已启用字典项的只读接口，供后台表单和后续业务页面使用；
- 字典变更写入审计日志，并删除 Redis 缓存；服务端以短 TTL 重新加载，避免新增专用同步表。

字典只用于运营可维护的枚举项和展示文案，不保存 Secret，也不用于改变系统安全或业务策略。SMTP、人机验证、额度和验证码时长继续使用通用配置项。

### 3.9 后台菜单结构

一级导航固定按职责分组，避免业务配置、后台治理和运维记录混在一起：

```text
工作台
└── 数据概览（含五个服务状态卡）
业务管理
├── 用户管理
└── 设备管理
服务配置
├── 设备服务（device-server）
├── 用户服务（user-server）
│   └── 邮件模板（页面内页签/入口，不占一级菜单）
├── VoIP 服务（voip-server）
│   └── 微信小程序、授权设备与上报属性（页面内页签）
├── AI 服务（ai-server）
├── 呼叫服务（call-server）
└── 通用配置
系统管理
├── 管理员
├── 权限与菜单
├── 数据字典
└── 系统配置
运维审计
├── 任务中心
├── 登录日志
└── 操作日志
```

五个服务页面使用一致骨架：服务状态摘要、跨主机实例明细、该服务配置分组、目标修订与生效状态、操作日志入口。没有配置项的区域不显示空卡片，但状态和实例区必须保留。菜单树由 `admin_menus` 控制名称、排序和角色可见性，前端注册路由决定可进入的页面，接口权限继续由权限码独立校验。

后台面向运营管理员时以业务语义为主：服务、角色、权限、日志动作、任务类型、设备来源、VoIP 状态、设备上报属性和失败原因使用中文名称；稳定编码、配置键、请求 ID 和原始 JSON 仅在二次开发或排障位置辅助展示。所有后端时间统一转换为浏览器本地时间 `YYYY-MM-DD HH:mm:ss`，不直接展示 ISO 时间字符串。管理员、日志和任务列表优先显示邮箱、名称和业务类型，不要求使用者依靠数字 ID 或内部枚举判断含义。

## 4. YAML 配置的归类

YAML 仍提供启动兜底值，数据库只覆盖允许动态调整的项目。配置归属以“哪个服务消费该值”为准，而不是按 SMTP、验证码或安全等技术类别混放。

### 4.1 五个服务的业务配置

| namespace | 建议配置键 | 来源/内容 | 约束 |
| --- | --- | --- | --- |
| `device-server` | `device.code_policy` | `code_ttl`、验证码/指纹/IP 限频、`global_max_pending_codes` | 设备验证码 TTL 保持默认 `190s`，与邮件验证码分离 |
| `device-server` | `device.token_policy` | `token_expiry` | 只影响新签发的设备 MQTT Token，默认 7 天 |
| `device-server` | `mqtt.ack_policy` | `timeout` | MQTT ACK 等待时长，默认 `5s` |
| `user-server` | `smtp` | 启用状态、Host、Port、TLS、Username、From、Password | Password 加密；发布前可发送测试邮件 |
| `user-server` | `captcha` | 启用状态、Provider、公开参数、供应商密钥 | 支持网易、极验、阿里、腾讯；Secret 加密 |
| `user-server` | `email.template.registration_code`、`email.template.password_reset_code` | 注册与找回密码邮件模板 | 使用模板白名单变量和通用配置表 |
| `user-server` | `email.code_ttl` | 注册与找回密码验证码 TTL | 默认 `5m`，不与发送限频或设备验证码复用 |
| `user-server` | `email.send_rate_limit` | 窗口、单邮箱和单 IP 上限 | 默认 15 分钟内 5 次/邮箱、20 次/IP |
| `user-server` | `user.default_bind_quota` | 新用户默认绑定额度 | 注册流程必须显式读取；不能继续依赖列默认值 |
| `user-server` | `user.token_policy` | 用户 Token 时效 | 只影响新签发 Token |
| `user-server` | `mqtt.ack_policy` | 绑定流程 MQTT ACK 超时 | 默认 `5s` |
| `voip-server` | `wechat.apps` | 默认 AppID、小程序列表、Model ID、Secret 和回调参数 | 启用应用必须配置 Secret；不提供删除入口 |
| `ai-server` | `ai.role_policy` | `default_role_id`、`base_url`、`base_role_url` | URL 必须是无 userinfo 的 HTTPS 地址 |
| `ai-server` | `ai.resource_policy` | `resource_quota`、`default_resources` | 校验额度和资源 ID |
| `call-server` | `call.contact_policy` | `max_contacts_per_device` | 正整数和合理上限 |
| `call-server` | `call.room_policy` | `room_ttl_hours` | 仅影响新建房间或按明确迁移规则处理存量房间 |

`device-server` 和 `user-server` 都存在名字相同的 `Service` 字段，但后台键必须按消费服务拆开，不能因为 YAML 结构相似就共享。`user-server.service.rate_limit_window` 与 `rate_limit_max_hits` 不作为动态配置发布；注册流程显式读取 `user.default_bind_quota`。字段是否开放后台编辑由配置注册表决定；未登记字段仍只允许通过 YAML 和部署流程修改。

### 4.2 通用配置

`common/tirtc/global` 保存 TiRTC Endpoint、App ID、Access Key ID 和 Secret Key ID，目标服务固定列出 `user-server`、`voip-server`、`ai-server`、`call-server`。页面分别展示四个目标服务的实例应用状态；Secret 只配置给确实需要解密的服务。没有被两个及以上业务服务消费的配置不得放入 `common`。

### 4.3 系统配置

`system` 只管理 `admin-server` 和管理后台本身的运行策略，与五个业务服务及通用配置分开：

- `system/mfa.policy/global`：所有管理员共用的 MFA 总开关；
- `system/admin.session_policy/global`：管理端 Access/Refresh Token 时效、登录失败锁定和会话上限；

### 4.4 保留部署配置

- 五个业务服务和 `admin-server` 的 HTTP 端口、可信反向代理网段、数据库、Redis、MQTT 地址与凭据；
- 用户和管理员 JWT 签名密钥、`internal.key`、服务间 URL 与 WebSocket 代理路由；
- 动态配置 AES-GCM 主密钥只放在 `admin-server/config.yaml` 中，生产文件不提交仓库且权限限制为 `0600`；
- 实例 ID、健康检查/心跳周期、Redis 心跳 TTL、共享任务目录等保证服务启动与可观测性的参数。

这些值决定服务能否连接基础设施、建立信任或解密数据库配置，不能依赖数据库动态配置自身来启动，必须经过部署流程修改。部署方案不引入 KMS/Vault；后续可在不改变数据库密文信封格式的情况下替换密钥来源。

## 5. 技术方案

### 5.1 服务与前端选型

| 层 | 选型 | 原因 |
| --- | --- | --- |
| 管理 API | Go 1.21 + Gin | 与现有五个服务一致，可复用日志、DB、Redis 与响应规范 |
| 前端 | React + TypeScript + Vite | 生态成熟、类型约束好、适合独立后台 SPA |
| UI | Ant Design 5 | 表格、表单、权限菜单、筛选和详情抽屉覆盖后台高频场景 |
| 授权 | Casbin v3 | 仅作为 RBAC 判定引擎；管理员、角色、菜单仍使用本项目表结构和管理页面 |
| 状态与请求 | React Hooks + Fetch + React Router | 统一 API 封装处理鉴权刷新和错误响应 |
| 表单校验 | Ant Design Form + 服务端配置注册表 | 前端负责交互校验，服务端负责最终安全边界 |

保留现有用户侧静态页，不要求迁移。管理后台构建产物从 `admin/admin-web/dist` 由 `admin-server` 托管，路径可在 YAML 中覆盖。管理端前端使用 `/admin/` 作为基础路径；同域 Nginx 将 `/admin/`（转发时去掉此前缀）和 `/v1/admin/` 转发至 `admin-server`，并将 `/admin` 重定向到 `/admin/`。这样页面刷新、静态资源和 Refresh Token Cookie 均保持同源。

后台采用“参考实现”而非复制整套脚手架：参考仍在维护的 `gin-vue-admin` 的登录、动态菜单、角色权限、日志和配置管理流程，但不复制其 Vue、GORM 或代码生成体系；`admin-server` 保持 Gin + sqlx，`admin-web` 保持 React + Ant Design。Casbin 只接收由 `admin_roles`、`admin_role_permissions` 和 `admin_user_roles` 装载出的角色继承、权限与成员关系，作为接口中间件的唯一授权判定器；角色或权限更新后清除 Enforcer 缓存并使管理员会话刷新。

### 5.2 目录建议

```text
admin/
  admin-server/
    main.go
    config.yaml.example
  admin-web/
    src/                          # React 源码
    dist/                         # Vite 构建产物
    package.json
internal/admin/                   # RBAC、管理员认证、配置、审计与业务编排
internal/dynamicconfig/           # 业务服务动态配置客户端
internal/servicestatus/           # 五服务心跳与状态聚合
internal/captcha/                 # 四个验证码 Provider Adapter
internal/store/mysql/             # 管理、审计和配置 Store
```

### 5.3 通用配置模型与数据模型

配置不按 SMTP、验证码、额度等业务域分别建表。管理员、RBAC、MFA、字典、任务和审计各自使用职责单一的业务表；动态配置只使用以下两张通用配置表。为现有 `users` 增加 `status`、`disabled_at` 与 `auth_revision`。

| 表 | 作用 |
| --- | --- |
| `config_entries` | 配置当前值。以 `namespace + config_key + scope_type + scope_id` 唯一定位一项配置，保存当前生效的非敏感 JSON，以及可选的 `secret_value_enc` 加密字段。 |
| `config_publish_outbox` | 在发布事务中写入，负责重试向 Redis 发布“配置已变更”事件；数据库版本校验负责最终一致性。 |

`config_entries` 的统一形态如下，SMTP、验证码、AI 策略均只是不同的 `config_key`，以后新增项不需要数据库迁移：

```text
namespace:     device-server | user-server | voip-server | ai-server | call-server | common | system
config_key:    smtp | captcha | email.code_ttl | wechat.apps | tirtc | mfa.policy
scope_type:    global
scope_id:      空字符串
value:         非敏感 JSON 字符串，例如 host、port、provider、captcha_id
secret_value_enc: 可选的 AES-GCM 密文，例如 password、secret_id、secret_key
status:        1=active | 0=disabled
revision:       乐观锁版本号（不保存历史快照）
```

当前系统没有多租户，完整交付范围也只开放 `global`。表中保留 `scope_type` 与 `scope_id` 是为了避免未来扩展时迁移唯一键，但 API 和配置注册表必须拒绝 `tenant`、`user`、`device` 等未定义作用域。真正引入新作用域前，必须先定义覆盖优先级、授权、缓存键和冲突处理规则。

例如 `user-server/captcha/global` 的 `value` 可为 `{"enabled":true,"provider":"geetest","captcha_id":"...","public_config":{...}}`，密钥保存在同一行的 `secret_value_enc` 中；`user-server/user.default_bind_quota/global` 则只需要一个普通 JSON 值，`secret_value_enc` 为空。邮件模板以 `user-server/email.template.registration_code/global` 保存，例如 `{"enabled":true,"subject":"{{product_name}} 注册验证码","html_body":"...{{code}}...","text_body":"..."}`。

未来新增模板仍复用该规则，例如 `user-server/email.template.device_bind_notice`、`user-server/sms.template.registration_code`；它们都只是 `config_entries` 中的新行，并归属实际发送它们的服务。模板列表页通过查询对应服务 namespace 的模板分组生成，新增模板只需增加注册表定义和对应业务发送点，不新增模板表、接口或页面。

邮件验证码有效期以 `user-server/email.code_ttl/global` 保存，例如 `{"duration":"5m"}`。YAML 只保留启动兜底值：

```yaml
service:
  code_ttl: 190s       # 仅设备绑定验证码与临时凭证

email:
  code_ttl: 5m         # 注册与找回密码邮件验证码的启动兜底值
```

每个配置项仍必须在代码中的**配置注册表**声明其语义，注册表不是数据库表，包含服务 namespace、业务分组、显示名称、类型、默认值、JSON Schema/校验器、Secret 字段路径、最小/最大值、所需权限、目标服务列表、是否支持热更新和测试器。五个服务页面按 namespace 生成配置分组；`common` 允许多个目标服务，`system` 只生成系统配置页面。例如：

```text
user-server/captcha
  type: object; secret_paths: [secret_id, secret_key]
  enum provider: [yidun, geetest, aliyun, tencent]
  targets: [user-server]; menu_group: captcha
  reload: runtime; validator: ProviderConfigValidator

user-server/email.code_ttl
  type: duration; default: 5m; range: 1m–30m
  targets: [user-server]; menu_group: email_policy; reload: runtime

user-server/email.send_rate_limit
  type: object; default: {window: 15m, max_per_email: 5, max_per_ip: 20}
  targets: [user-server]; menu_group: email_policy; reload: runtime

system/mfa.policy
  type: object; default: {enabled: true}
  fixed: {algorithm: SHA1, digits: 6, period_seconds: 30, allowed_skew_steps: 1}
  targets: [admin-server]; menu_group: admin_security; reload: runtime

voip-server/wechat.apps
  type: object; secret_paths: [apps.*.secret, apps.*.token, apps.*.encoding_aes_key]
  targets: [voip-server]; menu_group: wechat_apps
  reload: runtime; validator: WechatVoIPConfigValidator

common/tirtc
  type: object; secret_paths: [access_key_id, secret_key_id]
  targets: [user-server, voip-server, ai-server, call-server]
  menu_group: tirtc; reload: runtime; validator: TiRTCConfigValidator
```

这保证后台不会成为“任意 JSON 写库工具”：增加一个真正会影响业务的新配置，仍需增加注册表定义和服务读取逻辑，但不需要新增数据表或 CRUD 接口。

业务功能是否开启属于 `value` 的明确字段，例如人机验证和 SMTP 使用 `{"enabled":false}`。当前注册配置一经创建必须保持 `config_entries.status=1`，API 不接受将记录停用；这样避免“停用记录后应回退 YAML 还是继续使用最近快照”的歧义。数据库保留 `status` 字段供将来设计完整的覆盖撤销语义。

该模型适用于后续所有配置项：新项只增加注册表定义和服务读取逻辑，不增加数据表、通用 API 或新的配置页面框架；不能让后台任意填写一个未知 key 后自动改变业务行为。

密文使用 AES-GCM 加密。部署方案不引入 KMS/Vault：加密主密钥直接配置在 `admin-server/config.yaml`，不进入数据库、管理 API 或审计日志。建议配置形态如下：

```yaml
security:
  # Base64 编码的 32 字节随机密钥；只配置给 admin-server
  config_encryption_key_id: "local-v1"
  config_encryption_key: "replace-with-base64-32-byte-random-key"
```

`admin-server` 用它加解密后台提交的 SMTP、验证码和微信应用密钥。业务服务不持有加密主密钥，而是通过带 `X-Internal-Key` 的内部配置 API 获取已解密的目标快照；该内部链路必须位于可信网络，跨主机生产部署应使用 HTTPS。生产环境的 `config.yaml` 不提交代码仓库，并限制为服务运行账号可读（建议 `0600`）。

`secret_value_enc` 保存带版本的密文信封：`version`、`key_id`、`nonce` 和 `ciphertext`。AES-GCM 的 AAD 固定为 `namespace + config_key + scope_type + scope_id`，防止密文被复制到另一配置项后仍可解密。当前只启用 `local-v1` 本地密钥；以后迁移 KMS 时保持信封格式，只替换密钥获取方式，并可按 `key_id` 渐进重加密。

### 5.4 MySQL 目标表结构

以下为完整目标结构，落地时由 `internal/db/migrate.go` 和 `scripts/schema.sql` 同步维护。字段沿用现有表规范：`BIGINT AUTO_INCREMENT` 主键、snake_case、`created_at` / `updated_at`、`TINYINT` 状态值、无数据库外键；对象值统一序列化为字符串，不使用数据库原生 `JSON` 类型。表不按具体配置项扩张；增加 SMTP、验证码或其他业务配置不会新增配置表。

```sql
CREATE TABLE admin_users (
    id             BIGINT NOT NULL AUTO_INCREMENT,
    email          VARCHAR(255) NOT NULL,
    password       VARCHAR(255) NOT NULL,
    nick_name      VARCHAR(64) NOT NULL DEFAULT '' COMMENT '管理员展示昵称',
    status         TINYINT NOT NULL DEFAULT 1 COMMENT '1=active 0=disabled',
    auth_revision  BIGINT NOT NULL DEFAULT 1 COMMENT '密码、角色或状态变更时递增',
    must_change_password TINYINT NOT NULL DEFAULT 0 COMMENT '1=下次登录必须修改密码',
    password_updated_at DATETIME NULL,
    last_login_ip  VARCHAR(45) NOT NULL DEFAULT '',
    last_login_at  DATETIME NULL,
    remark         VARCHAR(256) NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_admin_email (email),
    KEY idx_admin_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_roles (
    id          BIGINT NOT NULL AUTO_INCREMENT,
    code        VARCHAR(64) NOT NULL,
    name        VARCHAR(64) NOT NULL,
    parent_id   BIGINT NOT NULL DEFAULT 0 COMMENT '0=无父角色',
    sort_no     INT NOT NULL DEFAULT 0,
    status      TINYINT NOT NULL DEFAULT 1 COMMENT '1=active 0=disabled',
    remark      VARCHAR(256) NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_admin_role_code (code),
    KEY idx_admin_role_parent (parent_id),
    KEY idx_admin_role_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_user_roles (
    admin_user_id BIGINT NOT NULL,
    role_id       BIGINT NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (admin_user_id, role_id),
    KEY idx_admin_user_role_role (role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_role_permissions (
    role_id         BIGINT NOT NULL,
    permission_code VARCHAR(64) NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (role_id, permission_code),
    KEY idx_admin_role_permission_code (permission_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_menus (
    id              BIGINT NOT NULL AUTO_INCREMENT,
    parent_id       BIGINT NOT NULL DEFAULT 0 COMMENT '0=顶级菜单',
    menu_code       VARCHAR(64) NOT NULL COMMENT '前端已注册页面标识',
    name            VARCHAR(64) NOT NULL,
    icon            VARCHAR(64) NOT NULL DEFAULT '',
    path            VARCHAR(128) NOT NULL DEFAULT '',
    permission_code VARCHAR(64) NOT NULL DEFAULT '',
    menu_type       TINYINT NOT NULL DEFAULT 2 COMMENT '1=目录 2=菜单',
    sort_no         INT NOT NULL DEFAULT 0,
    visible         TINYINT NOT NULL DEFAULT 1 COMMENT '1=显示 0=隐藏',
    status          TINYINT NOT NULL DEFAULT 1 COMMENT '1=active 0=disabled',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_admin_menu_code (menu_code),
    KEY idx_admin_menu_parent_sort (parent_id, sort_no),
    KEY idx_admin_menu_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_role_menus (
    role_id    BIGINT NOT NULL,
    menu_id    BIGINT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (role_id, menu_id),
    KEY idx_admin_role_menu_menu (menu_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_sessions (
    id            BIGINT NOT NULL AUTO_INCREMENT,
    admin_user_id BIGINT NOT NULL,
    family_id     CHAR(36) NOT NULL COMMENT 'refresh token rotation family',
    token_hash    CHAR(64) NOT NULL COMMENT 'refresh token SHA-256',
    replaced_by_id BIGINT NOT NULL DEFAULT 0,
    expires_at    DATETIME NOT NULL,
    revoked_at    DATETIME NULL,
    revoked_reason VARCHAR(128) NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_admin_session_token (token_hash),
    KEY idx_admin_session_user_expiry (admin_user_id, expires_at),
    KEY idx_admin_session_family (family_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_mfa_factors (
    id             BIGINT NOT NULL AUTO_INCREMENT,
    admin_user_id  BIGINT NOT NULL,
    factor_type    VARCHAR(16) NOT NULL DEFAULT 'totp',
    secret_enc     TEXT NOT NULL COMMENT 'AES-GCM encrypted TOTP secret',
    status         TINYINT NOT NULL DEFAULT 0 COMMENT '0=pending 1=active 2=disabled',
    last_used_step BIGINT NOT NULL DEFAULT 0 COMMENT 'reject TOTP replay',
    confirmed_at   DATETIME NULL,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_admin_mfa_user_type (admin_user_id, factor_type),
    KEY idx_admin_mfa_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_mfa_recovery_codes (
    id            BIGINT NOT NULL AUTO_INCREMENT,
    admin_user_id BIGINT NOT NULL,
    code_hash     CHAR(64) NOT NULL COMMENT 'SHA-256 of a high-entropy recovery code',
    used_at       DATETIME NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_admin_mfa_recovery_hash (code_hash),
    KEY idx_admin_mfa_recovery_user (admin_user_id, used_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_login_log (
    id            BIGINT NOT NULL AUTO_INCREMENT,
    admin_user_id BIGINT NOT NULL DEFAULT 0 COMMENT '0=账号不存在或认证失败',
    email         VARCHAR(255) NOT NULL DEFAULT '',
    client_ip     VARCHAR(45) NOT NULL DEFAULT '',
    user_agent    VARCHAR(512) NOT NULL DEFAULT '',
    status        TINYINT NOT NULL DEFAULT 1 COMMENT '1=success 0=failed',
    message       VARCHAR(512) NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_admin_login_user_time (admin_user_id, created_at),
    KEY idx_admin_login_email_time (email, created_at),
    KEY idx_admin_login_status_time (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_dict_types (
    id          BIGINT NOT NULL AUTO_INCREMENT,
    code        VARCHAR(64) NOT NULL COMMENT '字典编码',
    name        VARCHAR(64) NOT NULL COMMENT '字典名称',
    status      TINYINT NOT NULL DEFAULT 1 COMMENT '1=active 0=disabled',
    remark      VARCHAR(256) NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_admin_dict_type_code (code),
    KEY idx_admin_dict_type_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_dict_items (
    id             BIGINT NOT NULL AUTO_INCREMENT,
    dict_type_code VARCHAR(64) NOT NULL,
    label          VARCHAR(128) NOT NULL COMMENT '展示文案',
    value          VARCHAR(128) NOT NULL COMMENT '业务存储值',
    sort_no        INT NOT NULL DEFAULT 0,
    is_default     TINYINT NOT NULL DEFAULT 0 COMMENT '1=默认项 0=非默认项',
    status         TINYINT NOT NULL DEFAULT 1 COMMENT '1=active 0=disabled',
    extra          VARCHAR(1024) NOT NULL DEFAULT '' COMMENT 'JSON扩展字段',
    remark         VARCHAR(256) NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_admin_dict_item_value (dict_type_code, value),
    KEY idx_admin_dict_item_list (dict_type_code, status, sort_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_audit_log (
    id              BIGINT NOT NULL AUTO_INCREMENT,
    admin_user_id   BIGINT NOT NULL DEFAULT 0 COMMENT '0=未登录或系统任务',
    role_codes      VARCHAR(1024) NOT NULL DEFAULT '' COMMENT '操作时有效角色编码快照，逗号分隔',
    request_id      VARCHAR(64) NOT NULL,
    method          VARCHAR(8) NOT NULL DEFAULT '',
    path            VARCHAR(255) NOT NULL DEFAULT '',
    http_status     INT NOT NULL DEFAULT 0,
    latency_ms      BIGINT NOT NULL DEFAULT 0,
    action          VARCHAR(64) NOT NULL,
    resource_type   VARCHAR(64) NOT NULL,
    resource_id     VARCHAR(128) NOT NULL DEFAULT '',
    reason          VARCHAR(512) NOT NULL DEFAULT '',
    before_value    TEXT NULL COMMENT 'JSON, secrets redacted',
    after_value     TEXT NULL COMMENT 'JSON, secrets redacted',
    client_ip       VARCHAR(45) NOT NULL DEFAULT '',
    user_agent      VARCHAR(512) NOT NULL DEFAULT '',
    success         TINYINT NOT NULL DEFAULT 1,
    error_message   VARCHAR(512) NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_audit_resource_time (resource_type, resource_id, created_at),
    KEY idx_audit_admin_time (admin_user_id, created_at),
    KEY idx_audit_request (request_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_jobs (
    id              BIGINT NOT NULL AUTO_INCREMENT,
    job_type        VARCHAR(64) NOT NULL COMMENT 'device_pool_import etc.',
    status          TINYINT NOT NULL DEFAULT 0 COMMENT '0=pending 1=running 2=success 3=partial 4=failed',
    source_name     VARCHAR(255) NOT NULL DEFAULT '',
    input_file      VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'path relative to configured job storage',
    result_file     VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'path relative to configured job storage',
    total_count     INT NOT NULL DEFAULT 0,
    succeeded_count INT NOT NULL DEFAULT 0,
    failed_count    INT NOT NULL DEFAULT 0,
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at      DATETIME NULL,
    worker_id       VARCHAR(64) NOT NULL DEFAULT '',
    lease_until     DATETIME NULL,
    finished_at     DATETIME NULL,
    last_error      VARCHAR(1024) NOT NULL DEFAULT '',
    created_by      BIGINT NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    KEY idx_admin_job_due (status, next_attempt_at),
    KEY idx_admin_job_lease (status, lease_until),
    KEY idx_admin_job_creator_time (created_by, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE admin_job_items (
    id            BIGINT NOT NULL AUTO_INCREMENT,
    job_id        BIGINT NOT NULL,
    row_no        INT NOT NULL,
    status        TINYINT NOT NULL DEFAULT 0 COMMENT '0=pending 1=success 2=failed',
    resource_id   VARCHAR(128) NOT NULL DEFAULT '',
    error_code    VARCHAR(64) NOT NULL DEFAULT '',
    error_message VARCHAR(512) NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_admin_job_row (job_id, row_no),
    KEY idx_admin_job_item_status (job_id, status),
    KEY idx_admin_job_item_resource (resource_id, status, job_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE config_entries (
    id               BIGINT NOT NULL AUTO_INCREMENT,
    namespace        VARCHAR(64) NOT NULL,
    config_key       VARCHAR(128) NOT NULL,
    scope_type       VARCHAR(16) NOT NULL DEFAULT 'global',
    scope_id         VARCHAR(128) NOT NULL DEFAULT '',
    value            TEXT NOT NULL COMMENT 'non-secret JSON configuration',
    secret_value_enc TEXT NULL COMMENT 'AES-GCM encrypted secret JSON',
    status           TINYINT NOT NULL DEFAULT 1 COMMENT '1=active 0=disabled',
    revision         BIGINT NOT NULL DEFAULT 1,
    updated_by       BIGINT NOT NULL DEFAULT 0,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_config_scope (namespace, config_key, scope_type, scope_id),
    KEY idx_config_namespace (namespace, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE config_publish_outbox (
    id              BIGINT NOT NULL AUTO_INCREMENT,
    config_entry_id BIGINT NOT NULL,
    revision        BIGINT NOT NULL,
    event_type      VARCHAR(32) NOT NULL DEFAULT 'config.updated',
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    delivered_at    DATETIME NULL,
    last_error      VARCHAR(1024) NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_config_publish_revision (config_entry_id, revision),
    KEY idx_config_publish_due (delivered_at, next_attempt_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE users
    ADD COLUMN status TINYINT NOT NULL DEFAULT 1 COMMENT '1=active 0=disabled',
    ADD COLUMN disabled_at DATETIME NULL,
    ADD COLUMN auth_revision BIGINT NOT NULL DEFAULT 1 COMMENT '密码或账号状态变更时递增';
```

`admin_role_permissions` 只保存代码注册表中存在的权限码，Casbin 从该表构造角色授权规则。导航可见性取“角色菜单授权”、菜单 `visible=1` 和权限码的交集，接口授权只由 Casbin 权限码决定。菜单 `visible=0` 只隐藏导航，`status=0` 则完全停用菜单。管理员拥有多个角色时不使用角色级默认菜单，登录后进入排序最靠前且有权访问的菜单，避免多个角色默认值冲突。`auth_revision` 写入管理员或用户 JWT Claim；密码、账号状态、角色或权限变更时递增，以立即使旧 Token/会话失效。

`admin_jobs` 与 `admin_job_items` 承载设备池导入等后台异步任务；任务中心同时聚合展示 `admin_jobs`、`cleanup_outbox` 和 `config_publish_outbox`。运行实例通过 `worker_id` 和 `lease_until` 领取任务并周期续租，租约过期的执行中任务自动重新排队。每一行设备池写入与 `admin_job_items` 成功标记使用同一事务，重启后跳过已经完成的行。导入原文件和结果文件放在 `admin-server/config.yaml` 指定的受限目录，数据库只保存相对路径；多实例部署时该目录必须使用共享卷。上传大小由部署配置限制，生产运维负责该目录的备份和生命周期清理。

`config_entries` 是全部动态配置项的唯一当前值表。`secret_value_enc` 为空时表示该配置没有敏感字段。所有 Secret、TOTP 密钥和恢复码在写入审计日志前替换为 `configured: true/false`，不记录明文或可还原内容。审计保留与数据库归档由部署侧生命周期策略负责。

参照多个后台系统后的取舍：采用其角色—菜单关联、菜单显示/停用分离、登录日志、操作时延和接口路径审计等成熟模式，并增加轻量的数据字典以统一维护业务枚举；不增加部门、岗位、数据范围、任意动态组件路径和代码生成表。当前系统没有组织层级或多租户需求，提前引入这些表会增加维护成本，且无法服务已定义的用户、设备与配置管理场景。

### 5.5 管理 API

配置管理只提供一组通用端点，通过配置注册表决定字段、权限和页面渲染；用户、设备、权限与任务使用语义明确的领域端点：

```text
GET  /v1/admin/config-definitions?namespace=user-server
GET  /v1/admin/configs?namespace=user-server&scope_type=global
GET  /v1/admin/configs/:namespace/:config_key?scope_type=global
POST /v1/admin/configs/:namespace/:config_key/validate
POST /v1/admin/configs/:namespace/:config_key/test
PUT  /v1/admin/configs/:namespace/:config_key

POST /v1/admin/auth/login
POST /v1/admin/auth/mfa/verify
POST /v1/admin/auth/refresh
POST /v1/admin/auth/logout
POST /v1/admin/me/mfa/totp/enroll
POST /v1/admin/me/mfa/totp/confirm
POST /v1/admin/me/mfa/recovery-codes/regenerate
PUT  /v1/admin/me/password
GET  /v1/admin/permissions
GET  /v1/admin/roles
POST /v1/admin/roles
PUT  /v1/admin/roles/:id
PUT  /v1/admin/roles/:id/permissions
GET  /v1/admin/admin-users
POST /v1/admin/admin-users
PUT  /v1/admin/admin-users/:id
PUT  /v1/admin/admin-users/:id/roles
GET  /v1/admin/admin-users/:id/sessions
POST /v1/admin/admin-users/:id/sessions/revoke
POST /v1/admin/admin-users/:id/mfa/reset
GET  /v1/admin/login-logs
GET  /v1/admin/audit-logs
GET  /v1/admin/services/status
GET  /v1/admin/services/:service/status

GET  /v1/admin/users
GET  /v1/admin/users/:id
PUT  /v1/admin/users/:id/status
PUT  /v1/admin/users/:id/bind-quota
POST /v1/admin/users/:id/password-reset-email
GET  /v1/admin/devices
GET  /v1/admin/device-pool
GET  /v1/admin/devices/:device_id
GET  /v1/admin/devices/:device_id/bind-logs
POST /v1/admin/devices/:device_id/force-unbind

GET  /v1/admin/voip/apps
GET  /v1/admin/voip/apps/:app_id
GET  /v1/admin/voip/apps/:app_id/devices
GET  /v1/admin/voip/devices/:device_id/profile

GET  /v1/admin/dict-types
POST /v1/admin/dict-types
PUT  /v1/admin/dict-types/:id
GET  /v1/admin/dict-types/:code/items
POST /v1/admin/dict-types/:code/items
PUT  /v1/admin/dict-items/:id
GET  /v1/admin/dictionaries/:code
GET  /v1/admin/roles/:id/menus
PUT  /v1/admin/roles/:id/menus
GET  /v1/admin/menus
POST /v1/admin/menus
PUT  /v1/admin/menus/:id
GET  /v1/admin/me/navigation
POST /v1/admin/device-pool/imports
GET  /v1/admin/jobs
GET  /v1/admin/jobs/:id
GET  /v1/admin/jobs/:id/result
POST /v1/admin/jobs/:id/retry
```

列表接口统一使用 `page/page_size` 分页和稳定排序；批量导入走异步任务，禁止在单个 HTTP 请求中扫描全表。管理写接口接收 `reason`，配置或状态更新同时接收 `expected_revision` 或期望旧值；配置发布和强制解绑还必须携带 `confirm=true`。重置他人 MFA、撤销会话、禁用管理员和修改角色权限要求操作者重新验证自己的 MFA。

微信 VoIP 应用写入仍使用通用配置的 `validate` 与 `PUT /v1/admin/configs/voip-server/wechat.apps`，保证配置注册表、Secret 合并、乐观锁和发布流程只有一套。`/v1/admin/voip/*` 是面向独立菜单的聚合只读接口，用于组合应用配置、授权统计、设备归属和设备 profile；不得通过这些查询接口返回微信 Secret。

读接口按注册表脱敏；写接口以 `revision` 做乐观锁。首次添加注册配置项仍使用同一个 `PUT`，请求携带 `expected_revision=0`，服务端只允许创建注册表中存在且当前尚未实例化的键；并发创建命中唯一键时返回 409。Secret 字段省略表示保留旧值，传入非空值表示替换；接口返回的掩码或占位符不能作为新密钥写入。`voip-server/wechat.apps` 按 AppID 合并嵌套 Secret，编辑一个应用不会覆盖其他应用的密钥。`validate` 执行结构和业务约束校验，`test` 使用候选 SMTP 参数或已发布模板发送测试邮件；验证码 `enabled=true` 时必须包含所选 Provider 的完整密钥及有效公开参数，`enabled=false` 时不要求密钥。校验通过后，`PUT` 在同一事务内更新当前值、写入 `admin_audit_log` 和 `config_publish_outbox`。

邮件模板的预览和测试发送不新建配置表：前端用模拟变量在沙箱中预览，调用 `POST /v1/admin/configs/user-server/email.template.registration_code/test` 通过当前 SMTP 配置发送已发布模板的测试邮件；正式保存仍使用通用 `PUT` 接口。

不保留配置版本快照，也不提供一键回滚。操作日志保留脱敏后的旧值和新值，便于追查；如需恢复普通配置，由管理员重新提交旧值。由于密钥不会写入审计日志，恢复 SMTP 或验证码密钥时必须重新输入该密钥。

### 5.6 生效与发布

配置优先级为“代码默认值 < YAML 部署默认值 < 已发布的数据库覆盖值”。配置编辑遵循：

```text
参数校验 → 原子更新当前配置 → Redis 通知 → 服务原子换用新快照
```

Redis Pub/Sub 不是可靠消息队列，因此通知只负责加速，不承担最终一致性：

- 每个业务服务以 YAML 启动，并通过 `admin-server` 内部配置 API 拉取已发布覆盖值；内部 API 不可用时继续使用 YAML 启动值；
- `config_publish_outbox` Worker 重试发布包含 `config_entry_id + revision` 的 Redis 事件，`delivered_at` 只表示事件已发布到 Redis，不表示所有实例都已应用；
- 服务收到事件后，仅在事件 `revision` 大于本地版本时通过内部 API 重新读取、校验并原子替换快照；重复或乱序事件安全忽略；
- 每个服务每 30 秒重新读取目标配置，使订阅中断或实例重启期间漏掉的通知最终收敛；
- 内部 API 不可用或新值校验失败时继续使用最近一次有效快照并记录告警，不以 YAML 覆盖已经成功加载过的数据库配置。

`user-server` 由运行时配置快照创建并原子替换 SMTP Mailer 与验证码 Verifier；新配置加载失败时继续使用上一份有效快照。验证码开关关闭时使用 No-op Verifier，`GET /v1/config/captcha` 返回 `enabled=false`，用户端不加载 Widget；重新启用后立即恢复已选 Provider。网易易盾、极验、阿里云和腾讯云是内置实现，在它们之间切换或轮换密钥不需要发版。用户侧通过公开的 `enabled + provider + public_config` 选择并加载对应 Widget Adapter；新增 Provider 才需随代码发布。

### 5.7 管理写操作边界

- 管理资源命令集中在 `internal/admin`，不从前端拼接或执行任意 SQL；
- 强制解绑在单个数据库事务中完成设备解绑、配额返还、`device_bind_log`、`cleanup_outbox` 和管理员审计记录，不只更新 `device_bind.user_id`；
- 用户额度调整使用行锁或带旧值条件的原子更新，禁止负数；请求携带期望旧值，冲突时返回 409 并要求管理员刷新；
- 禁用用户时原子更新 `status`、`disabled_at` 和 `auth_revision`。用户端登录与所有受保护接口必须校验当前状态和版本，不能只验证 JWT 签名；
- 配置发布在同一事务内更新 `config_entries`、写入脱敏审计和 `config_publish_outbox`；文件导入先落受限临时目录，解析成功后创建任务，逐行写入必须具备 `device_id` 幂等约束；
- 对同库的高风险写操作，业务结果与成功审计原子提交。

### 5.8 安全边界

- 管理员 token 使用独立 `admin_jwt_secret`，不能复用用户 JWT；
- 管理端仅 HTTPS，部署于独立域名，MFA 默认启用；如需 IP 白名单，由反向代理或网关实施；各服务默认不信任转发的客户端 IP 头，仅将 `server.trusted_proxies` 中明确登记的代理网段作为可信来源；
- 所有写接口要求 RBAC、请求 ID 与审计；Access Token 通过 Authorization Header 传递，Refresh Cookie 使用 HttpOnly、Secure 和 SameSite；认证接口另有 Redis 限频；
- 密钥字段只允许写入，接口返回 `configured: true/false`；
- TOTP Secret 使用与动态配置相同的 AES-GCM 密文信封保存，但采用独立 AAD；恢复码只保存高熵原值的 SHA-256 摘要并且仅显示一次；
- 强制解绑使用共享业务服务和 outbox，保证 AI、VoIP、Call 清理可重试；
- 生产环境只开放四个内置验证码 Provider，SMTP 只允许隐式 TLS 或 STARTTLS。

### 5.9 五个服务的运行状态

状态是短时运行事实，不写入 MySQL，也不新增服务状态表。方案不假设五个服务与 `admin-server` 同机：服务可以部署在不同物理机、虚拟机、容器集群或可用区，同一服务也可以同时运行多个实例。五个服务统一提供：

- `GET /health/live`：只表示进程存活，不执行昂贵依赖检查；
- `GET /health/ready`：检查实例是否已加载必需配置并可接收流量，返回自身依赖的 MySQL、Redis、MQTT 等检查摘要；
- 每 15 秒主动写入共享 Redis `thingconnect:service:instance:{service}:{instance_id}`，TTL 为 45 秒，同时把键加入 `thingconnect:services:instances` 集合。值包含 `service`、`instance_id`、脱敏后的节点名/可用区、`version`、`commit`、`started_at`、`last_heartbeat`、依赖状态和该实例已应用的配置修订号。

`instance_id` 必须在所有主机上唯一：容器环境使用 Pod UID/任务 ID，固定主机可显式配置稳定 ID；未配置时进程生成包含主机名、PID 和时间随机量的实例 ID。`admin-server` 从固定索引集合读取心跳、用 `MGET` 聚合并清除已过期成员，不执行全库 `KEYS/SCAN`，也不在页面请求中跨网络同步探测所有实例。实例优雅退出时删除自己的心跳；进程异常、主机宕机或网络分区时由 TTL 自动过期。`/health/*` 供本机编排器或负载均衡器使用，不要求对 `admin-server` 开放入站访问。

心跳只返回依赖名称和 `healthy/unhealthy`，不返回连接串、完整 IP、主机凭据或第三方 Secret。后台实例明细展示节点别名/可用区、实例 ID、版本、启动时间、最近心跳和配置修订。单服务状态 API 只接受五个代码注册的服务名，并要求 `service.status.read` 权限；概览聚合接口要求 `dashboard.read`。

服务页面展示每个实例已应用的配置修订，供管理员和监控判断实例是否收敛。五个服务即使没有数据库覆盖配置，也报告运行状态；`admin-server` 自身健康由部署监控负责，不计入这五张业务服务状态卡。

跨主机配置同步使用 MySQL 作为 `admin-server` 的事实源、共享 Redis Pub/Sub 作为加速通知，业务实例通过带内部密钥的 HTTP(S) API 拉取配置并每 30 秒对账，不依赖本地文件共享。部署网络必须允许业务实例访问 `admin-server` 和 Redis；共享 Redis 短时不可用时，服务继续使用最近一次有效配置，恢复连接后重新对账最新修订。

## 6. 实现范围与验收

完整实现范围包括：管理员认证、TOTP MFA、RBAC、菜单与权限、用户和设备管理、设备池批量导入、设备强制解绑、任务中心、审计与登录日志、数据字典、邮件模板、五个服务业务配置、独立的通用配置与系统配置，以及 SMTP、验证码、TiRTC 和 `voip-server` 微信 VoIP 应用配置管理。验证码覆盖网易易盾、极验、阿里云和腾讯云。

验收要求：无权限访问必须拒绝；MFA 默认启用，启用时所有管理员必须绑定 TOTP，绑定与验证码校验不依赖外部服务；关闭 MFA 后登录不再要求 TOTP，但已绑定密钥与恢复码保留，且全部管理员会话失效并留下高风险审计；密钥永不回显；SMTP 和模板测试失败不修改线上当前配置；动态配置通知丢失或服务重启后能通过周期对账收敛到最新修订；管理员解绑产生清理任务并可追踪；设备池导入可查看逐行错误并安全重试；任务中心可查询和重试失败任务；数据字典停用后不再出现在只读接口中。人机验证可在后台启用或禁用，四个 Provider 均可切换并轮换密钥。概览同时显示五个服务状态；最后一个实例心跳过期后显示离线，依赖失败显示降级，恢复后自动回到健康。

### 6.1 联调与部署前置条件

- 提供网易易盾、极验、阿里云和腾讯云各环境所需的测试账号、密钥、域名白名单与前端 Widget 配置；
- 提供可发送测试邮件的 SMTP 账号和测试收件地址；
- 提供微信小程序 AppID、Model ID、Secret、回调 Token/AES Key，并完成合法域名和回调域名配置；
- 提供 TiRTC 测试环境 Endpoint 与凭据；
- 部署侧提供管理域名 HTTPS/DNS、可靠时间同步、Redis，以及多实例任务文件所需的受限共享卷。
