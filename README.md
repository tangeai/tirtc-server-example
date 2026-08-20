# tirtc-server-example

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](thing-connect/go.mod)
[![Gin](https://img.shields.io/badge/Gin-1.10-008ECF?logo=go&logoColor=white)](https://gin-gonic.com/)
[![MySQL](https://img.shields.io/badge/MySQL-8+-4479A1?logo=mysql&logoColor=white)](https://www.mysql.com/)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D?logo=redis&logoColor=white)](https://redis.io/)
[![MQTT](https://img.shields.io/badge/MQTT-Broker-660066?logo=mqtt&logoColor=white)](https://mqtt.org/)
[![Platform](https://img.shields.io/badge/Platform-WeChat%20IoT%20VoIP-07C160?logo=wechat)](https://developers.weixin.qq.com/miniprogram/dev/platform-capabilities/hardware-frame/IoT/voip.html)
[![WeChat Mini Program](https://img.shields.io/badge/Frontend-WeChat%20Mini%20Program-07C160?logo=wechat&logoColor=white)](thing-connect/weixin-mini-program/README.md)
[![Tailwind CSS](https://img.shields.io/badge/Tailwind%20CSS-3.4-06B6D4?logo=tailwindcss&logoColor=white)](thing-connect/package.json)
[![Python](https://img.shields.io/badge/Device%20Simulator-Python-3776AB?logo=python&logoColor=white)](thing-connect/device-sim/device-sim-py/README.md)
[![C](https://img.shields.io/badge/Linux%20Device%20Reference-C-A8B9CC?logo=c&logoColor=black)](thing-connect/device-sim/device-sim-c/README.md)

面向 IoT 嵌入式开发者的开源示例合集，基于 [tange.ai TiRTC（IoT-RTC）](https://tange.ai) SDK，演示如何让设备快速具备**远程实时查看、AI 对讲、微信 VoIP 呼叫、设备间互呼**能力。

---

## 首次体验：跑通实时音视频

不需要硬件，也不需要部署服务器。你将在电脑上启动一个 Python 设备模拟器，并把它绑定到官方 H5 演示平台。

完成后，你会在 H5 中看到模拟器推送的实时画面、听到实时音频。

### 开始前

- 本页命令适用于 **Ubuntu / Debian x86_64**，需要 CPython 3.10–3.14 和 `sudo` 权限。
- 请准备一个可接收邮件的邮箱，用于注册 H5 演示平台。
- macOS（仅 Apple Silicon）和 Windows x86_64 请使用 [各平台启动说明](thing-connect/device-sim/device-sim-py/README.md#详细说明环境搭建)；Linux ARM 等未随仓库提供 SDK 的平台，需先获取匹配架构的 [TiRTC SDK](https://docs.tange.ai/products/tirtc/overview/what-is-tirtc.html)。

### 步骤 1：准备环境和测试素材

```bash
git clone https://github.com/tange-ai/tirtc-server-example.git
cd tirtc-server-example/thing-connect/device-sim/device-sim-py
# Ubuntu / Debian（CPython 3.10–3.14、x86_64）：安装 Python
sudo apt update
sudo apt install -y python3 python3-venv

# 使用虚拟环境，避免新版系统拒绝修改全局 Python 环境
python3 -m venv .venv
source .venv/bin/activate
python -m pip install --upgrade pip
python -m pip install -r requirements.txt

```

仓库已随附默认的 `audio.g711a` 与 `video.h264`，无需生成即可继续。需要测试 PCM、Opus、AMR 或 MJPEG 等扩展格式时，再按 [生成扩展测试素材](thing-connect/device-sim/device-sim-py/README.md#生成扩展测试素材) 安装 `ffmpeg`、`espeak-ng` 并运行生成脚本。音频默认优先使用 Microsoft Edge TTS；脚本会提示并尝试安装 `edge-tts`，安装或在线合成失败时自动回退到 `espeak-ng`。

### 步骤 2：启动模拟器并获取验证码

```bash
# 以未绑定设备身份启动
python device_sim_main.py --mac AA:BB:CC:DD:EE:FF
```

终端会打印一个 **6 位验证码**（`验证码=xxxxxx`）和 H5 登录入口。保持模拟器运行，不要关闭终端。

> 未看到验证码，或提示 SDK、依赖、素材文件错误时，先查看 [device-sim-py README](thing-connect/device-sim/device-sim-py/README.md#详细说明环境搭建) 的环境搭建与失败排查。验证码过期或绑定超时后，停止程序并重新执行本步骤即可。

### 步骤 3：注册 / 登录官方 H5 平台

浏览器打开 <a href="https://demo-open.tange-ai.com/" target="_blank" rel="noopener">https://demo-open.tange-ai.com/</a>，用邮箱注册账号并登录。

### 步骤 4：绑定模拟器设备

在 H5 设备页面输入步骤 2 终端中显示的 6 位验证码，完成绑定。绑定成功后模拟器自动保存设备凭证并建立长连接，开始推送实时音视频。

### 步骤 5：查看实时画面

在 H5 设备列表点击刚绑定的设备，即可看到模拟器推送的实时画面、听到实时音频。至此首个闭环完成 ✅

https://github.com/user-attachments/assets/80ee6d4c-35dc-4425-813f-476e63ab1b40

---

## 下一步

### 首个闭环已打通

无需自建服务器或接入真实硬件，你已经让一个“设备”完成上线、绑定，并在 H5 中实时出图、出声。这说明设备接入所需的核心链路——设备身份、用户绑定、鉴权、MQTT 长连接和实时音视频——已经端到端验证通过。

### 下一个目标：让 Linux 设备在 H5 中出图、出声

这一步用本地编码文件代替真实采集，无需接摄像头、麦克风或扬声器。在目标 Linux 系统上编译 「C 参考实现」和匹配的 TiRTC C SDK（也可交叉编译），由文件发送上行音视频；当 H5 能看到画面、听到声音，并且程序能收到下行回调时，就验证了目标系统上的网络、SDK 集成、设备上线、MQTT、文件媒体上行和下行回调路径。它不验证麦克风/摄像头采集、扬声器播放或屏幕显示。

按以下步骤完成这个目标：

1. 从 [TiRTC SDK 说明](https://docs.tange.ai/products/tirtc/download.html) 获取与目标 Linux 系统和 CPU 架构匹配的 C SDK。
2. 按 [「C 参考实现」快速验证](thing-connect/device-porting.md) 在目标设备交叉编译或直接编译 「C 参考实现」，并准备本地测试音视频文件。
3. 在目标设备启动 「C 参考实现」，按本页相同的验证码流程完成 H5 绑定。
4. 在 H5 设备列表打开该设备，确认文件画面和文件音频可以上行；至此完成目标 Linux 系统上的文件媒体闭环。

### 完成首个闭环后，按以下顺序推进

设备端已经能出图、出声，但 AI 对讲、微信 VoIP、设备互呼还没接。Python 模拟器已经把这几项跑通，先在它上面理解每项能力的交互流程、设备侧行为和验收标准，再到阶段 2 在真实设备上开发。

| 阶段 | 要做什么 | 目的与参考 |
|------|------|------|
| 1 | 用 Python 模拟器研究其余功能 | 体验 AI 对讲、微信 VoIP 和设备互呼，摸清每项能力的交互流程、设备侧行为和验收结果。[模拟器验证场景](thing-connect/device-sim/device-sim-py/README.md#按验证场景使用) |
| 2 | 在真实设备上逐项集成 | 按“音视频推流 → AI 对讲 → VoIP 对讲 → 设备互呼”逐项实现。可参照 [「C 参考实现」代码](thing-connect/device-sim/device-sim-c/README.md) 中的上线、MQTT、TiRTC、文件媒体和会话控制流程；真实硬件接入与量产能力按其中列出的十项 TODO 实现和验收。 |
| 3 | 设备端验收后接入生产系统 | 现阶段开发时可对接演示系统（`demo-open.tange-ai.com`）；但正式商业落地时演示系统不可用于商用，你需要自行部署本仓库这套服务，或在已有系统里实现这些功能（接口、鉴权与业务流程可参考本仓库）。[查看部署与运维](thing-connect/deployment.md) |

> Python 模拟器与 Linux C 参考实现使用相同的设备上线和业务协议。产品可以复用协议顺序和会话控制思路，但不能直接把文件媒体或 Linux/POSIX 适配当成真实设备实现；平台、设备身份、真实媒体、交互、资源、恢复和量产安全仍需二次开发。

---

## 深入了解与自建部署

当你已完成模拟器体验，或准备接入真实设备、修改服务端、私有化部署时，再从项目架构与外部依赖开始了解。

### 项目组成

#### [thing-connect/](./thing-connect)

一套覆盖**远程实时查看、AI 对讲、微信 VoIP 呼叫、设备间互呼和后台管理**的 IoT 系统，包含五个业务服务及一个 Admin Server：

| 子项目 | 说明 |
|---|---|
| **device-server** | 设备端 HTTP 服务：设备上报物理标识获取验证码、签名换取 MQTT token |
| **user-server** | 用户端 HTTP 服务：邮箱注册登录、设备绑定重置、TiRTC token 签发、H5 静态页面 |
| **voip-server** | 微信 VoIP 服务：接收微信回调、下发呼叫通知、管理 VoIP 授权 |
| **ai-server** | AI 对话服务：签发 TiRTC AI 连接 token，供设备接入 AI 语音对话 |
| **call-server** | 设备间音视频通话服务：联系人、呼叫信令和房间管理 |
| **admin-server / admin-web** | 用户、设备、权限、菜单、字典、动态配置、服务状态和审计管理 |

六个服务共用 MySQL 和 Redis，需要 MQTT 的业务服务连接同一个 Broker。设备通过 MQTT 保持长连接，VoIP、AI 和设备间通话指令通过 MQTT 实时推送到设备。服务架构与接口详情见 [thing-connect/README.md](./thing-connect/README.md)，完整自托管步骤见 [Admin Server 部署指南](./thing-connect/admin/admin-server/README.md)。

### 依赖平台

| 平台 / 服务 | 说明 |
|---|---|
| [tange.ai TiRTC](https://tange.ai) | IoT 实时音视频 SDK，提供 WHIP 连接、Token 签发 |
| 微信 IoT VoIP | 微信官方 IoT VoIP 能力，需在微信公众平台开通 |
| 微信 wmpf-voip 插件 | 小程序侧通话 UI 插件（provider: `wxf830863afde621eb`） |

---

## License

MIT © 探鸽智能

参与开发见 [CONTRIBUTING.md](CONTRIBUTING.md)，安全问题见 [SECURITY.md](SECURITY.md)，第三方组件边界见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
