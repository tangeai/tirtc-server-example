# TiRTC-Nano SDK 包说明

本包面向 `windows-x86-64-desktop`，功能变体为 `standard`。机器可读的构建事实位于
`manifest/build-info.env`；下表逐项解释这些事实及其对接入工程的影响。

## 构建与兼容基线

| ENV 字段 | 当前值 | 含义 | 对接影响 |
| --- | --- | --- | --- |
| `SDK_NAME` | `TiRTC-Nano` | SDK 名称 | 用于确认正在接入 TiRTC Nano SDK。 |
| `SDK_VERSION` | `v2.5.0` | SDK 版本 | 应与依赖声明和发布记录中的版本保持一致。 |
| `TARGET` | `windows-x86-64-desktop` | 发布目标 | 选择与应用部署平台完全相同的包。 |
| `VARIANT` | `standard` | 功能变体 | standard 与 mini 的能力和依赖不同，不能混用。 |
| `ARCH` | `x86_64` | 处理器架构 | 应用与最终链接必须使用兼容的目标架构。 |
| `ABI` | `mingw-w64` | 目标 ABI | 编译选项、浮点 ABI 和对象 ABI 必须兼容。 |
| `TARGET_OS` | `windows` | 目标操作系统 | 应用需使用该系统对应的加载和链接方式。 |
| `BUILD_TOOLCHAIN` | `GCC` | 构建工具链 | 记录本包实际使用的编译器系列。 |
| `BUILD_TOOLCHAIN_VERSION` | `10` | 构建工具链版本 | 这是验证基线；更换版本时应重新完成编译和链接验证。 |

`BUILD_*` 记录本包实际使用的构建基线。它们不自动等同于使用者唯一可用
的版本；换用其他工具链或平台 SDK 时，应重新完成目标编译、链接和运行验证。

## 功能选择

功能开关以 `manifest/build-info.env` 中实际出现的字段为准。省略的条件字段表示该平台
没有发布可核实的公共承诺，不能据此推断能力已启用。

## 包内容

- `README.md`
- `include/tirtc/basedef.h`
- `include/tirtc/tgtrp.h`
- `include/tirtc/tiRTC.h`
- `include/tirtc/tiRTC_stat.h`
- `include/tirtc/ticloudstorage.h`
- `lib/libTiRTC.def`
- `lib/libTiRTC.dll`
- `lib/libTiRTC.dll.a`
- `lib/libTiRTC.lib`
- `lib/webrtc.dll`
- `manifest/build-info.env`

## 接入方法

在工程中加入 `include/tirtc` 头文件目录，并按上面的包内容选择 `lib/` 下的
TiRTC 入口库。动态运行库和 framework 必须随应用部署，并位于目标系统能够
解析的运行时搜索路径中。不要直接链接或调用仅作为运行依赖交付的库。

## 平台注意事项

## windows-x86_64-desktop 接入注意事项

MSVC 工程链接 libTiRTC.lib，MinGW 工程链接 libTiRTC.dll.a，运行时部署 libTiRTC.dll，并随程序发布 webrtc.dll。本包使用 x86_64 MinGW ABI；调用约定、CRT 选择和 DLL 搜索路径必须由最终应用保持一致。

## 验证范围

发布门禁校验包身份、公共头文件、目标二进制格式和 machine、工具链事实以及
最小 consumer 链接。除平台说明明确记录的证据外，目标设备上的调度、网络、
资源上限和长期运行兼容性仍由集成工程验证。
