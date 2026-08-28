# TiRTC Embedded Builder 使用说明

从 `thing-connect/` 目录启动 Codex，该目录下的仓库级 Skill 会被自动发现。显式调用方式：

```text
$tirtc-embedded-builder

在“厂商 + 完整开发板型号 + PCB 版本”上实现：
- H5 实时视频和声音
- H5 按住说话
- AI 双向对讲

资料：
- 产品页或资料链接：...
- 原理图：/absolute/path/board-schematic.pdf
- BSP 或示例工程：/absolute/path/vendor-bsp
- 输出目录：/absolute/path/my-tirtc-device

先完成能力分析；具备条件后生成并编译。只有我明确指定串口时才烧录。
```

## 常见输入方式

只有板卡型号：

```text
$tirtc-embedded-builder 分析 <厂商> <型号> <硬件版本>，目标是 H5 实时视频、talkback 和 AI 对讲。先输出缺失资料与能力结论。
```

提供本地资料：

```text
$tirtc-embedded-builder 使用原理图 /path/board.pdf 和 BSP /path/vendor-project，为该板生成 TiRTC H5/AI ESP-IDF 工程并编译。
```

完整实机流程：

```text
$tirtc-embedded-builder 使用 /path/hardware-ir.json 生成工程，编译后烧录到 /dev/ttyACM0，验证绑定、H5 和 AI，并生成 TIRTC_PORTING_REPORT.md。
```

## ESP-IDF 环境检查

环境检查：

```bash
python3 .agents/skills/tirtc-embedded-builder/scripts/doctor.py \
  --expected-idf 5.5 \
  --target esp32s3
```

工程生成后增加配置契约检查：

```bash
python3 .agents/skills/tirtc-embedded-builder/scripts/doctor.py \
  --expected-idf 5.5 \
  --target esp32s3 \
  --project /absolute/path/my-tirtc-device
```

如果 `idf.py` 缺失，Skill 默认只报告安装计划。只有明确授权安装版本、目录和环境修改后，才按照 Espressif 官方步骤安装并重新运行检查。

需要安装时可以明确输入：

```text
$tirtc-embedded-builder 检查开发环境；如果缺失，安装 ESP-IDF 5.5.x 到 /absolute/path/esp-idf-5.5，仅启用 esp32s3，并只激活当前终端，不修改 shell 启动文件。安装完成后重新运行 doctor。
```

## Hardware IR 工具

生成一份待填写的 IR：

```bash
python3 .agents/skills/tirtc-embedded-builder/scripts/hardware_ir.py \
  init /tmp/hardware-ir.json
```

检查结构和来源引用：

```bash
python3 .agents/skills/tirtc-embedded-builder/scripts/hardware_ir.py \
  validate /tmp/hardware-ir.json
```

判定当前请求能否进入移植：

```bash
python3 .agents/skills/tirtc-embedded-builder/scripts/hardware_ir.py \
  assess --strict /tmp/hardware-ir.json
```

`BLOCKED` 表示资料已经确认硬件不满足；`NEEDS_CONFIRMATION` 表示仍有未知项或只有单一来源；`READY_TO_PORT` 表示可以生成并实现板级适配；`HIL_VERIFIED` 表示端到端实机验收通过。

## 当前边界

仓库已经有 ESP32-S3 H5/AI 模板和生成器，但默认媒体适配器不包含真实摄像头、麦克风、H.264 编码和扬声器驱动。未注册的新板第一次使用时，Skill 会先完成 Hardware IR 和能力审计；只有型号而没有载板版本、BSP 或媒体资料时，不会把模板生成误报为 Web/AI 功能完成。
