# TiRTC Windows x86_64 SDK 使用说明

本 SDK 使用动态接入方式：

1. 编译期包含 `include/tirtc/tiRTC.h`
2. MSVC 工程链接 `lib/libTiRTC.lib`
3. MinGW 工程链接 `lib/libTiRTC.dll.a`

运行时需要随程序一起分发 `lib/libTiRTC.dll` 和 `lib/webrtc.dll`。

## 包内容

- `include/tirtc/tiRTC.h`：TiRTC 对外 API 头文件
- `include/tirtc/basedef.h`：TiRTC 基础类型定义
- `lib/libTiRTC.dll`：TiRTC 动态库
- `lib/libTiRTC.lib`：MSVC import library
- `lib/libTiRTC.dll.a`：MinGW import library
- `lib/libTiRTC.def`：导出符号定义
- `lib/webrtc.dll`：运行时依赖库

## 如何使用

业务代码只应包含 `include/tirtc/tiRTC.h`，并链接 `libTiRTC.lib` 或 `libTiRTC.dll.a`。

不要把 `webrtc.dll` 当作 TiRTC SDK 的入口库使用。它是运行时依赖库，会由 SDK 内部按需加载。

## 运行时库摆放位置

`libTiRTC.dll` 和 `webrtc.dll` 必须放在 Windows 动态库加载器能找到的位置。推荐把它们放在主程序可执行文件旁边。

示例：

```text
my_app.exe
libTiRTC.dll
webrtc.dll
```

如果不放在可执行文件旁边，需要确保所在目录已经加入 `PATH`，否则运行时会加载失败。
