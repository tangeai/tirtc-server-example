package handler

// Public explanations are shared by the visible HTML and structured data.
// Keep claims bounded by the repository's documented device and deployment support.
type publicSection struct {
	ID, Heading, Text, Link, LinkText string
}

var gettingStartedSections = []publicSection{
	{"overview", "小钛适合什么项目？", "小钛面向 IoT 嵌入式开发者，提供基于 TiRTC 的服务端、Web、微信小程序和设备接入示例，用于验证远程实时音视频、AI 语音对话、设备呼叫微信、设备互呼和多人对讲。具体能力取决于设备型号、固件和服务配置。", "https://github.com/tangeai/tirtc-server-example", "查看服务端与接入示例源码"},
	{"simulator", "没有开发板：先用电脑验证音视频", "运行 Python 设备模拟器，保持终端开启，在 Web 控制台注册或登录，输入终端显示的六位绑定码，再打开设备查看音视频。默认使用仓库内的文件素材；这一步不验证真实摄像头、麦克风或扬声器。", "/guides/simulator", "开始电脑音视频体验"},
	{"hardware", "有开发板：核对型号后烧录固件", "先核对开发板完整型号、芯片和适配状态，再按该型号的烧录指南下载固件、联网并绑定。不要仅凭相同芯片选择其他型号固件；以目录中实际提供的固件和指南为准。", "/boards", "查看开发板、固件与烧录指南"},
	{"porting", "接入自有硬件：先验证文件媒体，再接真实外设", "在目标 Linux 系统上使用匹配架构的 TiRTC C SDK 和参考实现，验证文件媒体上行与下行回调，再适配摄像头、麦克风、扬声器和屏幕。设备示例展示接入流程，产品仍需完成资源管理、异常恢复和量产安全。", "/guides/linux-device", "查看 Linux 设备接入步骤"},
	{"hosting", "自行部署：准备基础设施与业务服务配置", "自行部署需要 MySQL 8、Redis，以及业务所需的 MQTT Broker 和 TiRTC 配置。微信 VoIP、AI 等能力还需要对应的平台配置。按部署指南启动服务后，再验证设备上线、绑定和实际业务流程。", "https://github.com/tangeai/tirtc-server-example/blob/main/thing-connect/deployment.md", "阅读部署与运维指南"},
}

var faqSections = []publicSection{
	{"relationship", "小钛、ThingConnect 与 TiRTC 是什么关系？", "小钛是面向设备音视频与 AI 交互的体验和接入方案。此仓库的 ThingConnect 提供服务端、控制台、小程序和设备示例；TiRTC 是其使用的 IoT 实时音视频 SDK。接入时需要同时遵守业务协议与 SDK 的平台约束。", "https://github.com/tangeai/tirtc-server-example", "查看项目组成"},
	{"hardware-required", "没有硬件可以体验吗？", "可以。Python 设备模拟器支持在文档列出的 Windows、Ubuntu x86_64 和 macOS Apple Silicon 环境中验证设备上线、绑定与音视频。默认播放文件素材，不代表真实外设已经验证。", "/guides/simulator", "从模拟器开始"},
	{"board-support", "同一芯片的开发板可以共用固件吗？", "不能仅凭芯片型号判断。屏幕、音频器件、引脚、Flash 与内存配置可能不同。请选择完整型号匹配的固件，并核对适配状态和烧录指南；未列出的型号需要进一步适配。", "/boards", "核对开发板资源"},
	{"wechat", "设备呼叫微信需要哪些条件？", "需要支持该能力的设备固件、微信小程序及 VoIP 服务配置，并完成设备绑定和微信侧所需授权。仅运行设备模拟器或部署 Web 页面并不能替代微信平台配置。", "/guides/wechat-voip", "查看微信呼叫验证步骤"},
	{"production", "设备示例可以直接用于量产吗？", "设备示例用于展示协议和接入流程。量产设备仍需适配真实媒体与硬件、设备身份、会话生命周期、资源管理、异常恢复及安全机制，并在目标设备上验收。", "https://github.com/tangeai/tirtc-server-example/blob/main/thing-connect/device-porting.md", "查看设备接入约束"},
	{"refresh", "设备状态没有更新怎么办？", "保持设备程序运行，检查网络和绑定结果，再使用设备列表的刷新按钮更新状态。设备列表不定时轮询，也不会在切回前台或浏览器后退时自动刷新。", "/devices", "打开设备列表"},
}
