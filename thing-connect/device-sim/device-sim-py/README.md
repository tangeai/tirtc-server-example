# TiRTC Python 设备模拟器

不用开发板，在电脑上就能模拟一台 TiRTC 音视频设备，用来验证：

- 远程查看设备实时音视频
- AI 语音对讲
- 设备与设备音视频对讲
- 设备呼微信小程序 VoIP

模拟器默认读取仓库自带的音视频素材，不会使用电脑的摄像头、麦克风或扬声器。
Windows 可通过 `--with-camera` 使用 PC 摄像头，通过 `--with-mic` 使用麦克风和扬声器。

## 快速开始

### 1. 准备运行环境

- Windows 10/11 x64、Ubuntu x64 或 macOS Apple 芯片
- CPython **3.10–3.14**
- 仓库已包含模拟器所需的 TiRTC SDK **2.2.1** 和默认音视频素材

克隆仓库后无需另外下载模拟器 SDK。以 Windows 为例，仓库中应能看到：

```text
thing-connect/device-sim/sdk/windows-x86_64/2.2.1/lib/libTiRTC.dll
```

Ubuntu 和 macOS 对应目录分别为 `linux-x86_64` 和 `macos-arm64`。

### 2. 安装依赖并启动

#### Windows（PowerShell）

```powershell
winget install -e --id Python.Python.3.14
# 首次安装 Python 后，请重新打开 PowerShell

cd thing-connect\device-sim\device-sim-py
py -3.14 -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r requirements.txt
.\.venv\Scripts\python.exe device_sim_main.py

# 可选：使用默认摄像头作为上行视频
.\.venv\Scripts\python.exe -m pip install -r requirements-camera.txt
.\.venv\Scripts\python.exe device_sim_main.py --with-camera
```

#### Ubuntu

```bash
cd thing-connect/device-sim/device-sim-py
sudo apt install -y python3 python3-venv
python3 -m venv .venv
./.venv/bin/python -m pip install -r requirements.txt
./.venv/bin/python device_sim_main.py
```

#### macOS

```bash
cd thing-connect/device-sim/device-sim-py
python3 -m venv .venv
./.venv/bin/python -m pip install -r requirements.txt
./.venv/bin/python device_sim_main.py
```

### 3. 首次绑定

首次启动不需要填写 `device_id` 或 `device_key`。终端会显示六位验证码和体验平台地址：

1. 打开 [TiRTC 体验平台](https://demo-open.tange-ai.com) 并登录。
2. 输入六位验证码绑定模拟设备。
3. 绑定成功后，模拟器自动上线并开始发送默认音视频。

设备凭证会保存到本地，之后执行同一条启动命令即可直接上线。

### 4. 验证是否跑通

看到“实时流业务已就绪”并进入命令行，表示模拟器已经运行。此时可以：

| 操作 | 用法 |
|------|------|
| 查看实时音视频 | 在体验平台中打开当前设备 |
| AI 语音对讲 | 输入 `aicall` |
| 呼叫微信小程序 | 输入 `wxcall` 查看联系人，再输入 `wxcall N` |
| 呼叫另一台设备 | 输入 `call <device_id> video` |
| 接听 / 拒接 / 挂断 | 输入 `accept` / `reject` / `hangup` |
| 查看全部命令 | 输入 `help` |

接收到的音视频保存在 `received/<device_id>/`。先跑通默认配置，再按需阅读下面的详细说明。

## 相关文档

- [示例系统总览](../../README.md)
- [设备接入与 MQTT 规范](../../device-integration.md)
- [HTTP / MQTT 接口参考](../../api-reference.md)
- [TiRTC C API](https://docs.tange.ai/products/tirtc/api-reference/c.html)

<a id="详细说明环境搭建"></a>

## 环境与依赖

### macOS

```bash
# 首次：安装系统依赖 + Python venv + pip 依赖
chmod +x ../scripts/setup_mac.sh
../scripts/setup_mac.sh

source ../venv/bin/activate

# 启动
python3 device_sim_main.py --device-id DEV000001 --device-key your-key
```

`setup_mac.sh` 检查 Python 3.10–3.14、创建 venv，并安装 Python 与系统依赖。

Python 3.13 起标准库不再包含 `audioop`；`requirements.txt` 会仅在
Python 3.13–3.14 下安装兼容包 `audioop-lts`，无需手工处理。

macOS 使用**文件媒体模式**模拟设备采集；`--with-mic` 硬件音频模式仅支持 Windows。

可选快捷方式：

```bash
../scripts/run.sh --device-id DEV000001 --device-key your-key
```

### Linux (Ubuntu)

```bash
sudo apt install python3 python3-venv
python3 -m venv .venv
source .venv/bin/activate
python -m pip install --upgrade pip
python -m pip install -r requirements.txt
python device_sim_main.py --device-id DEV000001 --device-key your-key
```

Linux 默认使用本地媒体文件模拟设备采集：VoIP/推流/设备间通话从命令行指定的音频、视频文件循环读取，不访问 PC 麦克风或扬声器。音频支持 `pcm/alaw/amr/opus/aac` 的 `8k/16k` 组合；视频支持 `h264/h265/mjpeg`。

### Windows

```bash
# 在 Git Bash 中执行；需预先安装 Python 并加入 PATH
python -m venv .venv
source .venv/Scripts/activate
python -m pip install --upgrade pip
python -m pip install -r requirements.txt
# 文件媒体模式
python device_sim_main.py --device-id DEV000001 --device-key your-key

# 硬件音频（--with-mic）：安装基础依赖和可选声卡依赖
python -m pip install -r requirements-audio.txt
python device_sim_main.py --device-id DEV000001 --device-key your-key --with-mic

# 摄像头视频（--with-camera）：安装基础依赖和可选摄像头依赖
python -m pip install -r requirements-camera.txt
python device_sim_main.py --device-id DEV000001 --device-key your-key --with-camera
```

`requirements-audio.txt` 和 `requirements-camera.txt` 均包含基础 `requirements.txt`。
只使用一种 PC 硬件时，安装对应的依赖文件即可；同时使用摄像头和麦克风时，两份都要安装。

Windows 也可使用文件模式；只有显式传入 `--with-mic` 时，VoIP、AI 或设备间通话才使用 PC 麦克风和扬声器。此模式线上上下行必须同时使用 `alaw_8khz` 或同时使用 `alaw_16khz`（G.711A、单声道）；PCM/AMR/Opus 只能去掉 `--with-mic` 后使用预编码文件测试。

显式传入 `--with-camera` 时，实时推流、VoIP 和设备间视频通话使用 `--camera-index` 指定的 PC 摄像头，`--up-video-file` 被摄像头替代。画面统一缩放并编码为 `1280x720`、15fps、H.264 Annex-B；`--up-video-format` 必须为 `h264`。未传 `--with-camera` 时，视频继续从 `--up-video-file` 循环读取，支持 `h264/h265/mjpeg`。

如果 Windows 环境没有 `python3` 命令，可用 `py -3` 等价执行。

### 生成扩展测试素材

仓库已随附首次启动所需的 `audio.g711a` 与 `video.h264`，脚本不会覆盖它们。需要测试其他音视频编码格式时，再安装 `ffmpeg`、`espeak-ng` 并执行：

```bash
bash ../scripts/gen_assets.sh
```

会在 `device-sim/assets/` 生成常用测试资源，包括：

- 音频：`pcm_s16le_8khz/16khz`、`alaw_8khz/16khz`、`amr_nb/amr_wb`、`opus_8khz/16khz`
- 音频内容循环播报“一到十 + 当前编码格式 + 采样率 + 单声道”。默认优先使用
  Microsoft Edge TTS 的 `zh-CN-XiaoxiaoNeural`；未安装 `edge-tts` 时脚本会提示并
  尝试通过 pip 安装，安装或在线合成失败则自动回退到 `espeak-ng`，不会中断素材生成。
- 视频裸流：H264 仅生成 `1280x720`、15fps、10 秒；MJPEG 仅生成 `240x320`、`320x240`、`640x480`、`480x640`，均为 8fps、10 秒。模拟器会循环读取文件。
- 每份视频裸流同时生成一个 `preview_*.mp4` 预览文件；预览文件只用于查看画面，不作为模拟器输入。
- 视频文件名统一包含编码格式、分辨率、帧率、时长和总帧数，例如 `video_mjpeg_640x480_8fps_10s_80frames.mjpeg`。
- 默认素材：`audio.g711a`、`video.h264` 随仓库提供，可直接给模拟器默认参数使用。
- 组合测试素材：供 VoIP / AI / 推流 / 呼设备几条链路复用

可通过环境变量选择语音引擎：

```bash
# 默认：优先 Microsoft，失败自动回退 espeak-ng
bash ../scripts/gen_assets.sh

# 强制使用离线默认语音，不检查或安装 edge-tts
TTS_ENGINE=espeak bash ../scripts/gen_assets.sh

# 不允许脚本自动安装 edge-tts；未安装时直接回退
EDGE_TTS_AUTO_INSTALL=0 bash ../scripts/gen_assets.sh

# 修改 Microsoft 中文音色和语速
MICROSOFT_TTS_VOICE=zh-CN-YunxiNeural \
MICROSOFT_TTS_RATE=+10% \
bash ../scripts/gen_assets.sh
```

`TTS_ENGINE` 支持 `auto`、`microsoft`、`espeak`。Microsoft Edge TTS 是在线服务；
需要完全离线、可重复生成时使用 `TTS_ENGINE=espeak`。

如果你只想验证纯音频，可以把 `--up-video-file` 传空字符串，或者传 `audio-only`。

### 依赖清单

| 依赖 | pip 包 | 用途 |
|------|--------|------|
| 基础 | `paho-mqtt`, `requests` | MQTT 通信、HTTP 请求 |
| 文件模式 / 素材生成 | `numpy`, `soxr` | PCM 重采样、素材生成 |
| 可选硬件音频 | `requirements-audio.txt`（`sounddevice`） | Windows 麦克风/扬声器（`--with-mic`） |
| 可选摄像头视频 | `requirements-camera.txt`（`opencv-python`, `av`） | Windows 摄像头采集和 H.264 编码（`--with-camera`） |

## 启动方式

```bash
# 已绑定设备：启动实时推流，同时监听三类通话
python3 device_sim_main.py --device-id DEV000001 --device-key your-key \
  --up-audio-file ../assets/audio.g711a

# 首次上线（未绑定）
python3 device_sim_main.py --mac AA:BB:CC:DD:EE:FF

# 文件媒体模式
python3 device_sim_main.py --device-id DEV000001 --device-key your-key \
  --up-audio-file ../assets/audio.g711a \
  --up-video-file ../assets/video.h264 --down-media-dir ./received

# Windows PC 音视频：麦克风上行 + 扬声器下行 + 默认摄像头上行
python3 device_sim_main.py --device-id DEV000001 --device-key your-key \
  --with-mic --with-camera \
  --up-audio-format alaw_8khz --down-audio-format alaw_8khz
```

`--with-mic` 时声卡仍采集和播放 PCM 16k，但模拟器会在本地完成
`PCM 16k ↔ G.711 A-law 8k` 转码；实时流、VoIP、AI 对讲和设备互呼在线上默认统一使用
`alaw_8khz`。

推荐先从这两个最简单的命令开始：

```bash
# 1. 未绑定设备，先上线拿凭证
python3 device_sim_main.py --mac AA:BB:CC:DD:EE:FF

# 2. 已绑定设备，使用默认素材启动
python3 device_sim_main.py --device-id DEV000001 --device-key your-key \
  --up-audio-file ../assets/audio.g711a --up-video-file ../assets/video.h264
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--device-id` | `$DEVICE_ID` | 已绑定设备 ID |
| `--device-key` | `$DEVICE_KEY` | 设备密钥 |
| `--mac` | `AA:BB:CC:DD:EE:FF` | 设备 MAC（未绑定流程） |
| `--endpoint` | `http://ep-open.tangeopen.com` | 服务发现入口 |
| `--with-mic` | — | Windows 下使用 PC 麦克风/扬声器；上下行须同时为 `alaw_8khz` 或 `alaw_16khz` |
| `--with-camera` | — | Windows 下使用 PC 摄像头替代上行视频文件；输出固定为 720P、15fps、H.264 Annex-B |
| `--camera-index` | `0` | `--with-camera` 使用的摄像头编号 |
| `--up-audio-format` | `alaw_8khz` | 上行音频格式 |
| `--down-audio-format` | `alaw_8khz` | 下行音频格式 |
| `--up-audio-file` | `../assets/audio.g711a` | 各媒体模式通用的 G.711A 8 kHz 单声道音频文件 |
| `--up-video-file` | `../assets/video.h264` | 各媒体模式通用的上行视频文件；空值或 `audio-only` 表示纯音频 |
| `--up-video-format` | `h264` | 上行视频格式，支持 `h264/h265/mjpeg` |
| `--down-video-format` | `h264` | 下行视频保存格式后缀，支持 `h264/h265/mjpeg` |
| `VOIP_SCREEN_WIDTH`（环境变量） | `1280` | 设备自身屏幕宽度（像素），与上行视频素材分辨率无关 |
| `VOIP_SCREEN_HEIGHT`（环境变量） | `720` | 设备自身屏幕高度（像素），与上行视频素材分辨率无关 |
| `VOIP_VIDEO_RES_MODE`（环境变量） | `auto` | 微信 VoIP 下行视频分辨率模式：`auto/fit_screen/fill_screen`；后两者要求 `--down-video-format mjpeg` 和有效屏幕宽高 |
| `VOIP_CAMERA_ROTATION`（环境变量） | `0` | 微信 VoIP 通话 UI 顺时针旋转角度，仅支持 `0/90/180/270`，随 device profile 上报 |
| `VOIP_ASPECT_RATIO`（环境变量） | `1.3333333333` | 微信 VoIP 视频宽高比，必须大于 `0` |
| `VOIP_OBJECT_FIT`（环境变量） | 空 | 微信 VoIP 设备视频缩放方式：`fill/contain`；为空时不上传，使用微信默认值 |
| `VOIP_HOR_MIRROR`（环境变量） | `false` | 是否水平镜像微信 VoIP 视频 |
| `VOIP_VERT_MIRROR`（环境变量） | `false` | 是否垂直镜像微信 VoIP 视频 |
| `--down-media-dir` | `device-sim-py/received` | 各媒体模式通用的下行音视频保存目录 |
| `--log-level` | `debug` | `debug` / `info` / `warn` / `error` |

使用 MJPEG 下行并将画面完整缩小到设备屏幕范围：

```bash
VOIP_SCREEN_WIDTH=640 VOIP_SCREEN_HEIGHT=480 VOIP_VIDEO_RES_MODE=fit_screen \
  python3 device_sim_main.py --down-video-format mjpeg
```

`--down-audio-format` 同时用于微信 VoIP profile，因此文件模式只允许云端支持的
`alaw/amr/opus`。使用 `--with-mic` 时，上下行必须同时为 `alaw_8khz` 或同时为
`alaw_16khz`；PCM/AMR/Opus 只能去掉 `--with-mic` 后使用预编码文件测试。

程序启动后会持续发送实时媒体流。`--up-video-file` 为空或设为 `audio-only` 时，只发送音频。

通过终端发起或接听 VoIP、AI、设备通话后，实时流自动暂停，通话结束后再恢复。三类通话互斥，不会同时运行。

### 会话冲突与竞态规则

Python 与 C 模拟器统一通过 `SessionArbiter` 仲裁 MQTT、终端和 SDK 回调：

- H5 实时流是空闲基线；来电仅进入待接状态时仍继续推流。
- 全局只有一个待接槽位，VoIP/设备来电 first-wins；后来来电直接 busy 拒绝，不能覆盖先到来电。
- 待接槽位绑定 `room_id` 和票据代次，并有 45 秒 TTL；迟到取消只能作用于同一房间。
- 外呼、AI 对讲和接听来电取得唯一 RTC 所有权后，其他业务不能启动；不做自动抢占。
- VoIP 回铃和新来电在同一把锁内分类；接听按 `PENDING → STARTING → ACTIVE` 提交，token 请求或启动期间取消不会复活旧来电。
- 失败、拒接、取消、超时及远端挂断都归还所有权并恢复 H5；生命周期结束使用常驻队列，H5 恢复失败会限次重试。
- 每次所有权都有递增代次，迟到的旧结束事件不能终止后来启动的同类会话；设备忙线拒接 HTTP 不在 MQTT 回调线程执行。

需要接入新的独占 RTC 业务时，扩展 `SessionKind` 和生命周期适配器，并统一接入仲裁器。各业务状态机不要互相查询状态来决定竞争结果。

### TiRTC 运行时架构

进程中只有一个 `TiRtcRuntime`，统一持有 SDK 回调表和连接归属表。进程启动时调用一次 `TiRtcInit` / `TiRtcStart`，退出时调用一次 `TiRtcStop` / `TiRtcUninit`。

实时流、VoIP、AI 和设备互呼只注册各自的业务回调。切换业务时停止媒体、断开连接并激活下一业务代次，不重启 SDK。

每个连接都绑定到建立它的业务代次。SDK 回调先经过 runtime 检查，再分发给对应业务。已经取消或切换的连接回调会被丢弃；成功结果迟到时，连接会在回调返回后断开。

每个回调域使用一个随进程常驻的有界控制队列。媒体线程启停、连接断开、命令解析和会话恢复都在 SDK 回调栈之外执行；下行文件写入与声卡播放则使用独立的有界媒体队列。

## 启动后如何使用

程序启动后会做几件事：

1. 先走设备上线流程，拿到 `mqtt_token` 并建立正式 MQTT 长连接。
2. 注册四类业务回调并启动进程级 TiRTC runtime。
3. 激活实时流业务；VoIP、AI、设备互呼按会话仲裁结果临时切换。
4. 进入终端交互模式，等待你输入命令。

终端常用命令：

- `wxcall` 查看微信可呼叫用户，`wxcall N` 呼叫第 N 个用户
- `accept / reject [reason]` 接听或拒接来电
- `aicall` 发起 AI 对话
- `call <device_id> [video|audio]` 呼叫设备
- `accept / reject [reason] / cancel / hangup` 处理当前通话
- `ct list / ct pending / room / help / exit`

`call` 和 `wxcall` 未指定通话类型时，有上行视频素材则使用 `video`，未配置上行视频素材则使用 `audio`。显式指定 `video` 时必须已经配置上行视频素材。

常用首字母缩写：

- `w` = `wxcall`
- `a` = `accept`
- `r` = `reject`
- `h` = `hangup`
- `e` = `exit`

终端持续接收输入。涉及网络访问或会话切换的命令尚未执行完成时，后续命令按输入顺序排队，最多等待 32 条；超出上限的指令会提示未提交。输入 `exit`、`e`，或在 Linux/macOS 交互终端按 Ctrl+D，会结束模拟器。

这些缩写可以直接单独输入后回车。例如来电时输入 `a` 等价于 `accept`，输入 `r` 等价于 `reject`。

启动后的常见验证路径：

1. 验证设备上线：看终端是否拿到 `mqtt_token`，是否进入可交互状态。
2. 验证实时推流：看服务端是否能收到实时音视频；纯音频模式下确认未发送视频。
3. 验证微信 VoIP：小程序触发呼叫，设备收到通知后入房，待 `0x2000` 后开始发音视频。
4. 验证 AI：输入 `aicall`，确认设备能建立会话并收到下行音频。
5. 验证呼设备：输入 `call <device_id>` 或在小程序侧发起，确认双方建连、收发媒体正常。
6. 验证接收文件：检查 `--down-media-dir/<device_id>/` 下的 `received_audio.*`、`received_video.*`。

## 按验证场景使用

下面的场景都先启动设备模拟器，再从终端、H5 或小程序操作。

### 1. 验证设备 H5 出图

目的：验证设备实时视频流能否被 H5 管理端看到。

启动命令：

```bash
python3 device_sim_main.py --device-id DEV000001 --device-key your-key \
  --up-audio-file ../assets/audio.g711a \
  --up-video-file ../assets/video.h264 \
  --up-video-format h264
```

操作步骤：

1. 保持设备模拟器运行。
2. 打开 H5 管理端页面，入口见 [`thing-connect/README.md`](../../README.md) 里的 `user-server` 章节。
3. 在 H5 里找到对应设备，查看实时画面/预览流。

预期结果：

- H5 能看到设备视频画面。
- H5 每次订阅视频后都能尽快收到关键帧，不需要等待默认素材的下一个自然 GOP。
- 发送繁忙或关键帧恢复导致视频前进到下一个 IDR 时，音频文件同步到相同媒体位置，音画内容保持对应。
- 设备端持续输出实时流相关日志。
- 如果 H5 只看图不听音，也不影响设备侧持续发音视频。

### 2. 验证纯音频 VoIP 呼叫

目的：验证小程序与设备之间的纯音频 VoIP。

启动命令：

```bash
python3 device_sim_main.py --device-id DEV000001 --device-key your-key \
  --up-audio-file ../assets/audio.g711a \
  --up-audio-format alaw_8khz \
  --up-video-file audio-only
```

操作步骤：

1. 在小程序侧发起音频 VoIP 呼叫，或在设备终端输入 `wxcall N audio`。
2. 如果是设备收到来电，在终端输入 `accept`。
3. 建连后确认设备等待 `0x2000`，收到后才开始发音频。

预期结果：

- 设备只发音频，不发视频。
- 对讲结束后，`received/` 下只有音频文件或视频文件为空。

### 3. 验证音视频 VoIP 呼叫

目的：验证小程序与设备之间的音视频 VoIP。

启动命令：

```bash
python3 device_sim_main.py --device-id DEV000001 --device-key your-key \
  --up-audio-file ../assets/audio.g711a \
  --up-audio-format alaw_8khz \
  --up-video-file ../assets/video.h264 \
  --up-video-format h264
```

操作步骤：

1. 在小程序侧发起视频 VoIP 呼叫，或在设备终端输入 `wxcall N video`。
2. 如果是被叫，设备收到通知后输入 `accept`。
3. 设备入房后等待 `0x2000`；收到后再开始启动音频线程和视频线程。

预期结果：

- 小程序侧能收到设备音频和视频。
- 设备侧日志可看到“收到 `0x2000` 后开始发送本地音频和视频”。

### 4. 验证 `mjpeg/h264 + amr/opus` 组合

目的：验证不同上行音视频编码组合。

`h264 + amr_wb` 示例：

```bash
python3 device_sim_main.py --device-id DEV000001 --device-key your-key \
  --up-audio-file ../assets/number.amr_wb \
  --up-audio-format amr_wb \
  --up-video-file ../assets/video_h264_annexb_1280x720_15fps_10s_150frames.h264 \
  --up-video-format h264
```

`mjpeg + opus_16khz` 示例：

```bash
python3 device_sim_main.py --device-id DEV000001 --device-key your-key \
  --up-audio-file ../assets/number.opus_16khz \
  --up-audio-format opus_16khz \
  --up-video-file ../assets/video_mjpeg_640x480_8fps_10s_80frames.mjpeg \
  --up-video-format mjpeg \
  --down-video-format mjpeg
```

推荐验证点：

1. 启动后先看实时流是否正常发送。
2. 再分别走一遍 VoIP 或呼设备链路，确认通话时也能正确使用该组合。
3. 通话结束后检查 `received_audio.*`、`received_video.*` 是否被正确保存和转封装。

### 5. 验证 AI 对讲

目的：验证设备与 AI 的双向语音链路。

文件模式启动命令：

```bash
python3 device_sim_main.py --device-id DEV000001 --device-key your-key \
  --up-audio-file ../assets/audio.g711a \
  --up-audio-format alaw_8khz \
  --down-audio-format alaw_8khz \
  --up-video-file ""
```

操作步骤：

1. 启动后在终端输入 `aicall`。
2. 观察设备是否成功获取 AI `peer_id` 和 `token`。
3. 确认 AI 会话建立后，设备开始发送上行音频并接收下行音频。
4. 结束时输入 `hangup`。

预期结果：

- 设备能建立 AI 会话。
- `--down-media-dir/<device_id>/` 下能看到 AI 下行音频文件。

### 6. 验证设备呼设备

目的：验证设备间点对点呼叫。

设备 A、设备 B 都要启动。建议两端都用同一类素材，便于对比。

启动命令示例：

```bash
python3 device_sim_main.py --device-id DEV_A --device-key key-a \
  --up-audio-file ../assets/number.opus_16khz \
  --up-audio-format opus_16khz \
  --up-video-file ../assets/video.h264 \
  --up-video-format h264
```

```bash
python3 device_sim_main.py --device-id DEV_B --device-key key-b \
  --up-audio-file ../assets/number.opus_16khz \
  --up-audio-format opus_16khz \
  --up-video-file ../assets/video.h264 \
  --up-video-format h264
```

操作步骤：

1. 在设备 A 终端输入 `call DEV_B video` 或 `call DEV_B audio`。
2. 在设备 B 终端输入 `accept` 接听。
3. 验证双方是否都开始收发媒体。
4. 任一方输入 `hangup` 挂断。

预期结果：

- 两边都能收到对端媒体。
- 两边的 `received/` 目录都能看到下行音视频文件。

## 凭证持久化

首次绑定成功后，`device_id` 和 `device_key` 自动保存到 `device_creds.json`（与 `device_sim_main.py` 同目录）。下次启动无需再传 `--device-id` / `--device-key`，程序自动加载。

**凭证优先级：** CLI 参数 → 环境变量 → `device_creds.json` → 扫码绑定。

```json
{"device_id": "TIRZ88CLF5CN", "device_key": "..."}
```

**保存时机：**
- 扫码绑定成功（`auth_grant` 收到凭证）
- 每次用已有凭证成功启动（刷新时间戳）
- 解绑重绑后（6006 恢复流程）

**自动加载**：程序启动时，如果未通过 CLI 或环境变量提供凭证，自动从 `device_creds.json` 读取。加载失败不报错，走未绑定流程。

**C 版同样支持**，见 [device-sim-c/README.md](../device-sim-c/README.md#凭证持久化)。

## 模块速查

```
device-sim-py/
├── device_sim_main.py     # 入口：命令行解析、模式分发
├── device_rtc_runtime.py  # 四类业务的统一运行时组合
├── tirtc_runtime.py       # 进程级 SDK 生命周期、统一回调与连接代次
├── session_arbiter.py     # 竞态策略：待接槽、独占所有权、代次隔离
├── session_coordinator.py # 业务切换与 H5 恢复
├── session_router.py      # MQTT 与终端统一路由
├── device_flow.py         # 设备上线协议（HTTP + MQTT + HMAC 签名）
├── tirtc_sdk.py           # TiRTC SDK ctypes 绑定
├── audio_device.py        # 跨平台音频设备（麦克风/扬声器）
├── camera_video_source.py # Windows 摄像头采集与 H.264 编码
├── rtc_echo_gate.py       # 回声门控（替代 AEC，远端有声时衰减麦克风）
├── g711.py                # G.711 A-law 编解码
├── media_source.py        # Annex-B H.264 帧级读取
│
├── rtc_voip.py            # VoIP 模块（WHIP、音视频收发、PC 音频）
├── rtc_voip_session.py    # VoIP 来/去电状态机
│
├── rtc_ai.py              # AI 对话（文件模式）
├── rtc_ai_hw.py           # AI 对话（硬件模式，跨平台）
├── rtc_ai_session.py      # AI 会话状态机
│
├── rtc_call.py            # 设备间通话（P2P 信令、TiRtcConnect）
├── rtc_call_media.py      # 通话媒体（文件收发 + 硬件采集/播放 + 格式定义）
├── rtc_call_session.py    # 通话状态机 + 联系人管理
│
└── rtc_stream.py          # 音视频推流
```

### device_flow — 设备上线协议

```python
from device_flow import fetch_services, report_device, connect_temp_mqtt, get_mqtt_token, connect_mqtt_blocking, DeviceResetError

# 1. 服务发现
svc = fetch_services(base_url="http://ep-open.tangeopen.com")
# svc: { device_server, voip_server, ai_server, call_server,
#        mqtt_host, mqtt_port, mqtt_tls, tirtc_endpoint }

# 2. 未绑定：上报指纹 → 临时 MQTT → 等 auth_grant
result = report_device(svc["device_server"], mac="AA:BB:CC:DD:EE:FF")
device_id, device_key = connect_temp_mqtt(
    svc["mqtt_host"], svc["mqtt_port"],
    result["temp_client_id"], result["temp_token"],
    timeout_sec=190, use_tls=svc["mqtt_tls"],
)

# 3. 已绑定：HMAC 签名换取 token
try:
    mqtt_token = get_mqtt_token(svc["device_server"], device_id, device_key)
except DeviceResetError:  # code=6006，设备已解绑
    # 带 device_id 重走绑定流程
    pass

# 4. 正式 MQTT 长连接
connect_mqtt_blocking(svc["mqtt_host"], svc["mqtt_port"],
                       device_id, mqtt_token, handler,
                       use_tls=svc["mqtt_tls"])
```

`mqtt_tls` 由服务发现的 `mqtt-srv` scheme 决定（`mqtts://` → True，`mqtt://` → False）。

### tirtc_sdk — SDK ctypes 绑定

```python
from tirtc_sdk import (
    TIRTC_AUDIO_ALAW, TIRTC_AUDIO_PCM,
    TIRTC_AUDIOSAMPLE_8K16B1C, TIRTC_AUDIOSAMPLE_16K16B1C,
    TIRTC_EVENT_SYS_STARTED, CONN_FATAL_ERRORS,
)

# 发送音频帧
fi = TIRTCFRAMEINFO()
fi.stream_id = 10                    # VoIP:10, AI:1
fi.media = TIRTC_AUDIO_ALAW          # A-law or PCM
fi.flags = TIRTC_AUDIOSAMPLE_8K16B1C
fi.ts = int(pts_ms) & 0xFFFFFFFF
fi.length = len(pkt)
buf = (ctypes.c_uint8 * len(pkt)).from_buffer_copy(pkt)
rc = TiRtcSendAudioStream(hconn, ctypes.byref(fi), buf)
# rc < 0 且 rc in CONN_FATAL_ERRORS → 连接已断开
```

### audio_device — 跨平台音频

```python
from audio_device import (
    select_mic, select_speaker,                  # 自动选择设备
    open_input_stream, open_output_stream,       # 打开流（3 级策略）
    SpeakerPlayback,                             # 扬声器播放封装
)

# 打开麦克风（直开 → MME → 原生率+soxr）
stream, actual_rate = open_input_stream(device=None, target_rate=16000)
```

多级打开策略自动处理 Windows WASAPI/MME 和 macOS CoreAudio。

### rtc_echo_gate — 回声门控

```python
from rtc_echo_gate import EchoGate

gate = EchoGate.create(sample_rate=16000, frame_ms=20, attenuation_db=24)

# 喂入远端参考（对方正在播放的音频）
gate.feed_far_end(rx_pcm, source_rate=8000)

# 处理近端麦克风（远端有声时衰减，远端安静时透传）
tx_pcm = gate.process(mic_pcm)
```

滞回阈值：远端 RMS > 500 → 衰减，< 200 → 透传。`source_rate` 与门控采样率不同时自动重采样。

### 业务模块调用方式

```python
import rtc_ai
import rtc_call
import rtc_stream
import rtc_voip
import threading
from device_rtc_runtime import DeviceRtcRuntime, RuntimeConfig
from device_flow import connect_mqtt_blocking

config = RuntimeConfig(
    device_id=device_id,
    device_key=device_key,
    client_id=device_mac,
    mqtt_token=mqtt_token,
    tirtc_endpoint=svc["tirtc_endpoint"],
    voip_server=svc["voip_server"],
    ai_server=svc["ai_server"],
    call_server=svc["call_server"],
    up_audio_file="assets/audio.g711a",
    up_video_file="assets/video.h264",  # 空字符串表示设备无视频能力
    down_media_dir="./received",
)
runtime = DeviceRtcRuntime(
    config, rtc_stream, rtc_voip, rtc_ai, rtc_call)
runtime.start()  # 进程级 SDK 启动一次，并进入空闲 H5 实时流
stop_event = threading.Event()
command_thread = threading.Thread(
    target=runtime.run_cmd_loop,
    args=(stop_event,),
    name="cmd-loop",
)
command_thread.start()
try:
    connect_mqtt_blocking(
        mqtt_host, mqtt_port, device_id, mqtt_token,
        runtime.message_handler, stop_event=stop_event,
        use_tls=mqtt_tls)
finally:
    stop_event.set()
    command_thread.join()
    runtime.shutdown()  # 停业务和媒体后，进程级 SDK 停止一次
```

VoIP、AI、设备互呼和实时流模块不单独初始化或反初始化 SDK。终端命令和 MQTT 消息通过 `SessionArbiter` / `SessionCoordinator` 切换当前业务；模块只负责本业务的 HTTP、连接状态和媒体。

## SSL/TLS 配置

### MQTT

TLS 开关由服务发现返回的 `mqtt_tls`（bool）自动决定，无需额外配置。CA 根证书使用系统默认信任链。

paho-mqtt 连接参数由 `device_flow.connect_mqtt_blocking` 自动组装：

```python
client.tls_set(ca_certs=None, certfile=None, keyfile=None)  # 使用系统 CA 证书
client.connect(host, port, keepalive=30)
```

自定义 CA 证书或跳过验证（仅调试！）时修改 `tls_set` 参数：

```python
# 跳过证书校验（仅内网调试）
client.tls_set(cert_reqs=ssl.CERT_NONE)
# 指定自签名 CA
client.tls_set(ca_certs="/path/to/ca.pem")
```

### HTTP

`device_flow` 所有 HTTP 请求走 `requests` 库，是否使用 TLS 由服务发现地址的 `http://` / `https://` scheme 决定，并使用系统 CA 信任链。

## HMAC 签名

### 算法

`POST /v1/device/token` 和 Report（解绑重绑时）均需要设备签名：

```python
import hmac, hashlib, base64, time, secrets

ts    = str(int(time.time()))       # Unix 时间戳（秒）
nonce = secrets.token_hex(8)        # 8 字节随机十六进制
raw   = (device_id + ts + nonce).encode()
sig   = base64.b64encode(
    hmac.new(device_key.encode(), raw, hashlib.sha256).digest()
).decode()
```

请求头：

```
X-Device-Id:  TIRZ88CLF5CN
X-Timestamp:  1783583536
X-Nonce:      9e0853dc29c19139
X-Signature:  AUN5tAb0BZf8gW1cPSyn/...
```

### 代码位置

`device_flow.py` 中 `_hmac_headers(device_id, device_key)` 封装了上述逻辑。调用方不需要手动构造签名：

```python
headers = _hmac_headers(device_id, device_key)
resp = requests.post(f"{server}/v1/device/token", headers=headers)
```

### 失败排查

| 错误码 | 含义 | 检查项 |
|--------|------|--------|
| 6008 | 签名校验失败 | `device_key` 是否正确、时间戳偏差是否 > 5 分钟 |
| 6006 | 设备已解绑 | 带 `device_id` 重走 Report 流程 |

## 音频格式

### 文件模式支持的发送格式（`--up-audio-format` 参数）

| 格式 | 编码 | 采样率 | 每包字节 | `--up-audio-format` 值 |
|------|------|--------|---------|----------------------|
| G.711 A-law | A-law | 8kHz | 20ms: 160 / 40ms: 320 | `alaw_8khz` |
| G.711 A-law | A-law | 16kHz | 20ms: 320 / 40ms: 640 | `alaw_16khz` |
| AMR | AMR-NB | 8kHz | 按文件帧长 | `amr_nb` |
| AMR | AMR-WB | 16kHz | 按文件帧长 | `amr_wb` |
| Opus | Ogg/Opus | 8kHz | 按文件 packet | `opus_8khz` |
| Opus | Ogg/Opus | 16kHz | 按文件 packet | `opus_16khz` |
| PCM | s16le | 8kHz | 20ms: 320 / 40ms: 640 | `pcm_s16le_8khz` |
| PCM | s16le | 16kHz | 20ms: 640 / 40ms: 1280 | `pcm_s16le_16khz` |
| AAC | ADTS AAC | 8kHz | 按文件帧长 | `aac_adts_8khz` |
| AAC | ADTS AAC | 16kHz | 按文件帧长 | `aac_adts_16khz` |

### 视频格式

| 类型 | `--up-video-format` / `--down-video-format` |
|------|---------------------------------------------|
| H.264 Annex-B | `h264` |
| H.265 Annex-B | `h265` |
| MJPEG | `mjpeg` |

### 各场景音频参数

| 场景 | 方向 | 格式 | 采样率 | 每包 | stream_id |
|------|------|------|--------|------|-----------|
| VoIP 文件 | 上行 | 可配置 | 8k/16k | 40ms | 10 |
| VoIP 实时麦克风 | 线上双向 | `alaw_8khz`（本地声卡 PCM 16k） | 8kHz | 40ms | 10 |
| AI 文件 | 上行 | 可配置 | 8k/16k | 20ms | 1 |
| AI 硬件 | 线上双向 | `alaw_8khz`（本地声卡 PCM 16k） | 8kHz | 160B/20ms | 1 |
| 通话文件 | 发送 | 可配置 | 见上表 | — | AUDIO_STREAM_ID |
| 通话硬件 | 线上双向 | 默认 `alaw_8khz`（本地声卡 PCM 16k） | 8kHz | 20ms | AUDIO_STREAM_ID |

文件模式下，音频文件必须已经是目标编码格式：`aac` 需 ADTS，`opus` 需 Ogg/Opus，`amr` 需标准 `#!AMR`/`#!AMR-WB` 文件头；视频文件需分别提供 `h264/h265 Annex-B` 或 `mjpeg` 裸流。

Windows `--with-mic` 在线上使用上下行一致的 `alaw_8khz` 或 `alaw_16khz`，默认
`alaw_8khz`。麦克风 PCM 16k 在发送前按目标采样率重采样并编码为 G.711 A-law；
收到的 G.711 A-law 解码后再交给扬声器。AI `start_session` 会显式协商
`codec=g711a`、对应采样率及单声道。视频来源由 `--with-camera` 或
`--up-video-file` 决定。

### 接收媒体与音频格式检测

VoIP 收到的媒体写入 `--down-media-dir/<device_id>/`：

```text
received_audio.raw
received_video.h264      # 或 .h265 / .mjpeg，取决于 --down-video-format
```

Call/Stream 模式会根据首帧生成 `received_audio.fmt.json`，用于记录实际接收格式：

```json
{"encoding": "s16le", "sample_rate": 16000}
```

`encoding` 字段映射：

| SDK 格式 | `encoding` 值 | ffmpeg `-f` 参数 |
|----------|--------------|------------------|
| `TIRTC_AUDIO_PCM` | `s16le` | `s16le` |
| `TIRTC_AUDIO_ALAW` | `alaw` | `alaw` |
| `TIRTC_AUDIO_AAC` | `aac` | `aac` |
| `TIRTC_AUDIO_OPUS` | `opus` | `opus` |

### 媒体文件准备

```bash
# 生成全部测试素材
bash ../scripts/gen_assets.sh

# 按需转换
ffmpeg -i input.mp3 -ar 8000  -ac 1 -f alaw output.alaw_8khz
ffmpeg -i input.mp3 -ar 16000 -ac 1 -f s16le output.pcm_s16le_16khz
ffmpeg -i input.mp3 -ar 16000 -ac 1 -c:a libopus -b:a 32k -f ogg output.opus_16khz
ffmpeg -i input.mp4 -c:v libx264 -r 15 -bf 0 -g 30 -bsf:v h264_mp4toannexb -an output.h264
```

## MQTT 消息

### 主题

| 主题 | 方向 | ACK | 说明 |
|------|------|-----|------|
| `device/{temp_client_id}/cmd` | 下行 | 必须 | 临时连接，下发 `auth_grant` |
| `device/sn_{device_id}/cmd` | 下行 | 必须 | 正式连接，来电/解绑等指令 |
| `device/sn_{device_id}/notify` | 下行 | 不需要 | 通知类消息 |
| `device/sn_{device_id}/up` | 上行 | — | 心跳（30s） |
| `device/sn_{device_id}/ack` | 上行 | — | ACK：`{"ack": true}` |

连接凭证说明：
- 临时连接：`ClientID = Username = temp_client_id`
- 正式连接：`ClientID = sn_{device_id}`，`Username = device_id`

### 关键消息

**auth_grant**（`device/{temp_client_id}/cmd`，临时连接）：
```json
{"type":"auth_grant","payload":{"device_id":"DEV000001","device_key":"..."}}
```
空 payload 表示预烧设备被解绑，用本地凭证继续。

**call_incoming — VoIP**（`device/sn_{id}/cmd`，`channel:"wx"`）：
payload 中的 `wx_user_remark` 是设备联系人备注名；字段缺失时按 `wx_user_openid` 从本地联系人缓存查找。设备收到后回 ACK，然后 `TiRtcWhipConnect(peer_id, token)`；WHIP 建连成功后继续等待平台下发 `0x2000`，收到后才启动本地音视频发送线程。若 10 秒内未收到 `0x2000`，设备会主动断开并恢复空闲态。

**call_incoming — 设备间**（`device/sn_{id}/cmd`，`channel:"device"`）：
```json
{"type":"call_incoming","channel":"device","payload":{"room_id":"...","caller_id":"...","call_type":"video"}}
```
设备执行 `accept` 后调用 `POST /v1/call/device/info` 获取 token，然后 `TiRtcConnect`。

**unbind**（`device/sn_{id}/cmd`）：设备被解绑，清除本地状态，走 Report 重新绑定。

## AI 信令（0x2100，JSON-RPC 2.0）

| method | 方向 | 说明 |
|--------|------|------|
| `start_session` | 设备→平台 | 发起会话（含 `role_id`、`input_audio`、`output_audio`） |
| `start_session` 响应 | 平台→设备 | 含 `session_id` |
| `caption` | 平台→设备 | 字幕/ASR 结果 |
| `round_start` / `round_end` | 平台→设备 | 对话轮次边界 |
| `end_session` | 双向 | 结束会话 |

```json
{"jsonrpc":"2.0","id":"uuid","method":"start_session",
 "params":{"device_id":"...","role_id":"...","input_audio":{"sample_rate":8000,"channels":1},"output_audio":{"sample_rate":8000,"channels":1}}}
```

WHIP 连接成功后需等 ~300ms KCP 握手再发 `start_session`。

## 错误码

### HTTP 业务码

| 码 | 接口 | 含义 | 动作 |
|----|------|------|------|
| 429 | `POST /v1/device/report` | 请求过频 | 等 `Retry-After` 秒 |
| 40901 | `POST /v1/device/report` | 上一验证码仍有效 | 等 `Retry-After` 秒 |
| 6006 | `POST /v1/device/token` | 设备已解绑 | 带 `device_id` 走 Report，用已有 `device_key` 签名 |
| 6008 | `POST /v1/device/token` | HMAC 签名失败 | 检查 `device_key`、时间同步 |
| 6009 | `POST /v1/device/token` | 设备 ID 被其他 MAC 占用 | 联系运维 |
| 6010 | `POST /v1/device/report` | 指纹全空 | 至少填 `mac` |
| 6014 | `POST /v1/device/report` | 设备 ID 不可信 | 调用 Report 时带上签名 Header（用已有 `device_key`） |

### MQTT 断连

| 原因码 | 含义 | 动作 |
|--------|------|------|
| `152`/`153` | 认证被拒 | 重新调 Token |
| `0x98` | JWT 过期 | 重新调 Token 后重连 |
| `0x99` | ACL 拒绝 | 检查 `device_id` claim |

模拟器中 `0x98`/`0x99`/`152`/`153` 均自动触发重连。

### TiRTC SDK

| 错误码 | 常量 | 说明 |
|--------|------|------|
| `-40002` | `TIRTC_E_INVALID_HANDLE` | 连接未就绪，短暂重试 |
| `-40012` | `TIRTC_E_SERVER_ERROR` | 服务端拒绝，检查 peer_id/token |

`TiRtcSendCommand` 返回正数 = 已发送字节数（成功），负数 = 错误码。

### 音频设备

| PortAudio 错误 | 含义 |
|---------------|------|
| `-9999` | `Unanticipated host error` — 通常 WDM-KS 设备，已自动过滤 |
| `-9997` | `Invalid sample rate` — 已自动切 MME 或原生率兜底 |

## 常见坑

1. **回调对象必须保持存活**：将 ctypes `CFUNCTYPE` 包装器存入 `cbs._cb_refs` 列表，避免被 GC 回收。
2. **不要在回调内阻塞，也不要反向调用断开或反初始化**：SDK 回调在内部线程执行，只复制数据、更新受保护状态或投递事件。每个回调域只有一个常驻的有界控制队列，`Disconnect`、命令解析、线程启停和会话恢复都要等回调返回后再执行。文件与声卡 I/O 使用独立的有界媒体队列。
3. **`TiRtcWhipConnect` 返回 0 不代表连接成功**：连接结果以 `connect_cb` 回调的 `error` 参数为准。
4. **AI WHIP 连接后等 ~300ms**：KCP 握手完成前发送命令会丢失。
5. **AI 文件发完即停止上行**：连接保持到服务端返回 `end_session`，不再额外补静音。
6. **H.264 必须重新编码**：`-c copy` 会造成 SPS/PPS 缺失、B 帧残留。
7. **SDK 生命周期属于进程级 runtime**：只在进程启动时 Init/Start，业务切换不 Stop/Uninit，进程退出时才 Stop/Uninit。
8. **扬声器外放时有回声**：将电脑音量调到 ~15%，程序启动时会提示。
9. **摄像头无法打开**：确认只在 Windows 使用 `--with-camera`，检查 Windows“相机隐私设置”是否允许桌面应用访问，并用 `--camera-index 1` 等编号选择其他摄像头。
