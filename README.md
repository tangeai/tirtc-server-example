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

不需要硬件，也不用自行部署服务器。只需在电脑上启动一个 Python 设备模拟器，并将它绑定到官方 H5 演示平台，便可查看模拟器推送的实时画面和音频。

### 开始前

- 本页命令适用于 **Ubuntu/Debian x86_64**，需要 CPython 3.10–3.14 和 `sudo` 权限。
- 请准备一个可接收邮件的邮箱，用于注册 H5 演示平台。
- macOS（仅 Apple Silicon）和 Windows x86_64 请使用 [各平台启动说明](thing-connect/device-sim/device-sim-py/README.md#详细说明环境搭建)；Linux ARM 等未随仓库提供 SDK 的平台，需先获取匹配架构的 [TiRTC SDK](https://docs.tange.ai/products/tirtc/overview/what-is-tirtc.html)。

### 步骤 1：准备环境和测试素材

```bash
git clone https://github.com/tange-ai/tirtc-server-example.git
cd tirtc-server-example/thing-connect/device-sim/device-sim-py
# Ubuntu/Debian（CPython 3.10–3.14、x86_64）：安装 Python
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

### 步骤 3：注册或登录官方 H5 平台

浏览器打开 <a href="https://demo-open.tange-ai.com/" target="_blank" rel="noopener">https://demo-open.tange-ai.com/</a>，用邮箱注册账号并登录。

### 步骤 4：绑定模拟器设备

在 H5 设备页面输入步骤 2 终端中显示的 6 位验证码，完成绑定。绑定成功后模拟器自动保存设备凭证并建立长连接，开始推送实时音视频。

### 步骤 5：查看实时画面

在 H5 设备列表中点击刚绑定的设备，就能看到模拟器推送的实时画面并听到音频。

https://github.com/user-attachments/assets/80ee6d4c-35dc-4425-813f-476e63ab1b40

---

## 从模拟器走向真实设备

Python 模拟器已经跑通设备上线、用户绑定、MQTT 长连接和实时音视频。继续开发时，建议按下面的顺序推进：

| 阶段 | 工作内容 | 参考 |
|------|----------|------|
| 1. 熟悉业务功能 | 继续使用 Python 模拟器体验 AI 对讲、微信 VoIP 和设备互呼，了解交互流程、设备侧行为和验收结果。 | [模拟器验证场景](thing-connect/device-sim/device-sim-py/README.md#按验证场景使用) |
| 2. 验证目标 Linux 系统 | 使用匹配架构的 TiRTC C SDK 和 C 参考实现，先跑通文件媒体上行与下行回调。 | [设备移植指南](thing-connect/device-porting.md) |
| 3. 接入真实硬件 | 将文件媒体替换为摄像头、麦克风、扬声器和屏幕，并逐项接入实时查看、AI 对讲、微信 VoIP 和设备互呼。 | [C 参考实现](thing-connect/device-sim/device-sim-c/README.md) |
| 4. 接入生产系统 | 设备端验收通过后，自行部署本仓库服务，或在已有系统中实现相同的接口、鉴权和业务流程。 | [部署与运维](thing-connect/deployment.md) |

### 在目标 Linux 系统上验证文件媒体

接入摄像头、麦克风和扬声器前，建议先用本地编码文件验证目标系统。编译 C 参考实现和匹配的 TiRTC C SDK 后，由文件发送上行音视频。当 H5 能看到画面、听到声音，程序也能收到下行回调时，说明网络、SDK 集成、设备上线、MQTT 和媒体传输路径可以正常工作。

这一步不验证麦克风或摄像头采集，也不验证扬声器播放和屏幕显示。

操作步骤：

1. 从 [TiRTC SDK 说明](https://docs.tange.ai/products/tirtc/download.html) 获取与目标 Linux 系统和 CPU 架构匹配的 C SDK。
2. 按 [C 参考实现快速验证](thing-connect/device-porting.md) 在目标设备上直接编译或交叉编译 C 参考实现，并准备本地测试音视频文件。
3. 启动 C 参考实现，按照本页的验证码流程完成 H5 绑定。
4. 在 H5 设备列表中打开该设备，确认文件画面和音频可以正常上行。

> Python 模拟器和 Linux C 参考实现使用相同的设备上线与业务协议。产品可以复用协议顺序和会话控制思路，但仍需自行适配平台、设备身份、真实媒体、用户交互、资源管理、异常恢复和量产安全，不能直接把文件媒体或 Linux/POSIX 适配当作产品实现。

---

## 深入了解与自建部署

完成模拟器体验后，如果要接入真实设备、修改服务端或私有化部署，可以从下面的项目组成和依赖平台开始了解。

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
| **admin-server/admin-web** | 用户、设备、权限、菜单、字典、动态配置、服务状态和审计管理 |

六个服务共用 MySQL 和 Redis，需要 MQTT 的业务服务连接同一个 Broker。设备通过 MQTT 保持长连接，微信 VoIP 和设备互呼的来电通知通过 MQTT 下发；AI 对讲由设备从 `ai-server` 获取 token，再通过 TiRTC 主动建连。服务架构与接口详情见 [thing-connect/README.md](./thing-connect/README.md)，完整自托管步骤见 [部署指南](./thing-connect/deployment.md)。

### 依赖平台

| 平台或服务 | 说明 |
|---|---|
| [tange.ai TiRTC](https://tange.ai) | IoT 实时音视频 SDK，提供 WHIP 连接和 token 签发 |
| 微信 IoT VoIP | 微信官方 IoT VoIP 能力，需在微信公众平台开通 |
| 微信 wmpf-voip 插件 | 小程序侧通话 UI 插件（provider: `wxf830863afde621eb`） |

微信 VoIP 体验需先使用微信扫描二维码：

![微信 VoIP 体验二维码](images/voip/tangexiaotai-wx.png)

---

## License

MIT © 探鸽智能

参与开发见 [CONTRIBUTING.md](CONTRIBUTING.md)，安全问题见 [SECURITY.md](SECURITY.md)，第三方组件边界见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
