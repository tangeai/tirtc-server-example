package handler

// Tutorials summarize the checked-in device guides. Detailed protocol contracts
// stay in their source documents; these pages describe a user's verification path.
type tutorialLink struct{ Label, URL string }
type tutorialStep struct{ ID, Heading, Text, Code, Expected string }
type tutorialProblem struct{ Symptom, Check string }
type publicTutorial struct {
	Path, Title, Description, Audience, Boundary string
	Prerequisites                                []string
	Steps                                        []tutorialStep
	Problems                                     []tutorialProblem
	Sources                                      []tutorialLink
}

const sourceRoot = "https://github.com/tangeai/tirtc-server-example/blob/main/"

var publicTutorials = []publicTutorial{
	{
		Path: "/guides/simulator", Title: "不用开发板，在电脑上验证实时音视频",
		Description: "用 Python 设备模拟器完成启动、六位码绑定与 Web 实时查看，按成功现象逐步排查。",
		Audience:    "第一次体验小钛的开发者", Boundary: "本页使用 Ubuntu x86_64 文件媒体模式。它验证设备接入与媒体传输，不验证摄像头、麦克风、扬声器或目标硬件性能。",
		Prerequisites: []string{"Ubuntu x86_64、Git、CPython 3.10–3.14；Windows x64 与 macOS Apple Silicon 的命令见文末模拟器说明。", "可接收邮件的邮箱，以及可以访问体验平台的网络。", "从仓库根目录开始操作。仓库随附默认音视频素材和对应平台的 TiRTC SDK 2.5.0。"},
		Steps: []tutorialStep{
			{ID: "prepare", Heading: "01 · 下载代码，准备 Python 环境", Text: "在终端执行以下命令。使用虚拟环境安装依赖，避免修改系统 Python。", Code: "sudo apt update\nsudo apt install -y git python3 python3-venv\ngit clone https://github.com/tangeai/tirtc-server-example.git\ncd tirtc-server-example/thing-connect/device-sim/device-sim-py\npython3 -m venv .venv\n./.venv/bin/python -m pip install -r requirements.txt", Expected: "依赖安装完成，没有 Python 版本或 SDK 平台不匹配错误。"},
			{ID: "start", Heading: "02 · 启动模拟器，保留终端运行", Text: "首次启动不需要手工填写设备 ID 或设备密钥。默认读取仓库内的文件素材。", Code: "./.venv/bin/python device_sim_main.py", Expected: "终端显示六位绑定验证码和体验平台地址。不要关闭这个终端。"},
			{ID: "bind", Heading: "03 · 登录平台，绑定这台模拟设备", Text: "打开 xiaotai.chat 注册或登录，在设备页面输入当前终端显示的六位验证码。绑定成功后，模拟器保存本地凭证并上线。后续启动沿用同一目录即可读取凭证。", Expected: "设备出现在当前账号的设备列表，终端提示实时流业务已就绪。"},
			{ID: "verify", Heading: "04 · 打开设备，确认画面和声音", Text: "在设备列表打开这台设备，确认能看到默认素材画面并听到音频。状态未更新时点击列表的刷新按钮。退出查看页后，保持终端运行以继续其他场景。", Expected: "Web 实时查看正常。仅看到设备在线还不能证明媒体已经跑通。"},
		},
		Problems: []tutorialProblem{
			{"没有出现绑定码", "先检查终端中的 Python、依赖、SDK 和素材文件错误。已保存凭证的设备可能直接上线，不会再次显示首次绑定码。"},
			{"绑定码过期或绑定超时", "停止模拟器后重新启动，使用新显示的验证码，并在绑定期间保持终端和网络连接。"},
			{"设备在线但没有画面或声音", "先确认实时流业务就绪和素材文件存在，再按模拟器完整说明检查网络与媒体参数；不要把在线状态当作媒体验收结果。"},
		},
		Sources: []tutorialLink{{"Python 模拟器：平台命令、版本与故障排查", sourceRoot + "thing-connect/device-sim/device-sim-py/README.md"}, {"项目首次体验流程", sourceRoot + "README.md"}, {"继续验证微信 VoIP", "/guides/wechat-voip"}},
	},
	{
		Path: "/guides/wechat-voip", Title: "设备如何呼叫微信：从授权到通话验收",
		Description: "先核对微信 VoIP 的设备、服务与授权前提，再用模拟器验证联系人、来电、接听和挂断。",
		Audience:    "已完成设备上线与绑定的开发者", Boundary: "本页是接入验收路径。微信平台能力、账号资质与配置以当前微信平台要求为准；文件媒体模式不代表真实麦克风或扬声器已适配。",
		Prerequisites: []string{"设备已经绑定、在线，并使用支持微信 VoIP 的固件或设备模拟器。", "使用具备设备 VoIP 能力且完成配置的微信小程序。自建服务还需要对应的 VoIP、微信回调和 TiRTC 配置。", "设备已上报 VoIP 媒体能力，微信用户已对这台设备授权；仅登录 Web 控制台不能替代微信授权。"},
		Steps: []tutorialStep{
			{ID: "baseline", Heading: "01 · 先把设备上线与绑定跑通", Text: "先完成模拟器或真实设备的上线和绑定，核对实际设备身份。若用 Python 模拟器，可先按电脑体验教程完成 Web 音视频验证，再保持模拟器运行。", Expected: "可以确认设备在线，且使用的是本次要验证的设备。"},
			{ID: "authorize", Heading: "02 · 在微信小程序中完成设备授权", Text: "登录小程序并选择该设备，完成设备 VoIP 授权。若此前关闭了通话提醒，需要在小程序设置的语音、视频通话提醒中重新开启，而不是反复请求弹窗。", Expected: "这位微信用户的有效授权已同步到设备联系人列表。"},
			{ID: "call", Heading: "03 · 从设备发起微信呼叫", Text: "在运行中的 Python 模拟器交互终端输入 wxcall 查看联系人，再使用输出中的实际序号执行 wxcall N。下面的 1 仅表示列表中存在的第一位联系人。", Code: "wxcall\nwxcall 1", Expected: "对应微信用户收到来电；请求受理并不等于对方已接听或媒体已连接。"},
			{ID: "accept", Heading: "04 · 接听、验证媒体，再验证结束", Text: "在微信端接听，确认约定的音频或视频可用。再分别验证挂断、拒接和呼叫取消，确认设备能结束当前会话。Python 文件模式的接收媒体保存在 received/<device_id>/；硬件音频需按模拟器说明另行配置。", Expected: "通话可以建立和结束；文件模式按记录与文件验收，真实设备还要确认扬声器、麦克风和画面。"},
		},
		Problems: []tutorialProblem{
			{"wxcall 联系人列表为空", "检查是不是对当前设备完成授权、微信侧提醒是否开启，以及设备是否收到联系人更新。"},
			{"已授权但设备收不到来电", "检查设备在线状态、VoIP 媒体能力上报、MQTT 连接和微信服务配置；按完整 VoIP 指南定位具体阶段。"},
			{"呼叫受理但没有通话", "分别检查微信端是否收到来电、是否接听，以及设备媒体连接是否建立。不要用单个 HTTP 成功响应判定整个通话成功。"},
		},
		Sources: []tutorialLink{{"微信 VoIP 完整接入与授权规则", sourceRoot + "thing-connect/device-voip.md"}, {"模拟器交互命令与媒体模式", sourceRoot + "thing-connect/device-sim/device-sim-py/README.md"}, {"先完成电脑音视频体验", "/guides/simulator"}},
	},
	{
		Path: "/guides/linux-device", Title: "Linux 设备接入：先验证文件媒体，再接真实外设",
		Description: "用 Linux C 参考实现检查 SDK、构建和媒体链路，再通过 DeviceAdapterV1 接入真实硬件。",
		Audience:    "准备适配 Linux 音视频设备的开发者", Boundary: "下面的快速命令用于 Ubuntu x86_64。其他架构必须取得匹配的 SDK、工具链与依赖；该示例不是通用硬件固件，也不包含真实采集、播放或显示驱动。",
		Prerequisites: []string{"确认目标 CPU、ABI、libc 与 TiRTC SDK 匹配；交叉编译按设备移植指南配置工具链和 sysroot。", "Ubuntu x86_64 开发环境具备编译器及 libcurl、OpenSSL、libmosquitto、cJSON 和 pkg-config。", "已下载项目源码，具备可访问体验平台的网络和用于绑定设备的账号。"},
		Steps: []tutorialStep{
			{ID: "build", Heading: "01 · 在 Ubuntu x86_64 构建参考程序", Text: "从仓库根目录开始。先用默认文件媒体验证构建和网络，再开始外设适配。", Code: "sudo apt install -y build-essential pkg-config libcurl4-openssl-dev libssl-dev libmosquitto-dev libcjson-dev\ncd thing-connect/device-sim/device-sim-c\nmake WERROR=1\nmake WERROR=1 test", Expected: "参考程序编译成功，主机测试通过。这个结果只代表相应的软件检查通过，不代表真实设备已经验收。"},
			{ID: "stream", Heading: "02 · 启动、绑定并查看默认文件媒体", Text: "以下物理标识是单台演示设备的示例；同时验证多台设备时，应为每台使用独立、稳定的标识。按终端显示的六位验证码完成绑定，然后在 Web 打开设备。", Code: "./device-sim --mac AA:BB:CC:DD:EE:FF", Expected: "Web 能看到文件画面、听到音频。验证下行时检查对应回调；默认 C 适配不会解码播放或显示下行媒体。"},
			{ID: "adapter", Heading: "03 · 按适配接口替换真实媒体", Text: "通过 DeviceAdapterV1 接入真实媒体源、播放与显示队列、设备身份和产品交互。SDK 回调中只做有界复制和事件投递，不在回调内等待硬件。按接口组逐项替换，先验证单个方向再验证完整通话。", Code: "make WERROR=1 framework\nmake WERROR=1 adapter-template-check", Expected: "产品适配模板和框架检查通过，目标设备的真实采集、编码、播放和显示分别完成实机验收。"},
			{ID: "acceptance", Heading: "04 · 在目标设备验证异常和恢复", Text: "逐项验证冷启动、断网重连、取消与挂断、重复或迟到回调、媒体切换和进程重启。记录硬件型号、固件或源码版本、SDK 版本、环境及测试结果；延迟、内存和稳定性结论只对应这些测试条件。", Expected: "资源可以释放并重新使用，异常有可定位的结果。量产前继续完成移植指南中的安全、身份存储和长稳验收。"},
		},
		Problems: []tutorialProblem{
			{"链接失败或程序无法启动", "优先核对 CPU、ABI、libc、SDK 动态库与工具链。不要把宿主机的依赖库混入目标系统。"},
			{"文件媒体正常，真实外设不工作", "文件示例没有提供真实驱动；分别验证采集、编码、队列、解码和硬件输出，并检查适配器返回值。"},
			{"切换通话后音视频无法恢复", "检查媒体与会话资源的停止顺序、回调代次隔离和恢复流程，按设备移植指南逐项验收。"},
		},
		Sources: []tutorialLink{{"Linux C 参考实现：构建与接口约束", sourceRoot + "thing-connect/device-sim/device-sim-c/README.md"}, {"设备移植与量产验收清单", sourceRoot + "thing-connect/device-porting.md"}, {"没有目标硬件时先用电脑体验", "/guides/simulator"}},
	},
}
