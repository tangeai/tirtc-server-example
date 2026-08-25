# TiRTC Windows x86_64 SDK 使用说明

这个 SDK 使用动态库。编译时先包含 `include/tirtc/tiRTC.h`，再根据工具链选择导入库：

| 工具链 | 导入库 |
|---|---|
| MSVC | `lib/libTiRTC.lib` |
| MinGW | `lib/libTiRTC.dll.a` |

运行时需要随程序一起分发 `lib/libTiRTC.dll` 和 `lib/webrtc.dll`。

## 包内容

- [`include/tirtc/tiRTC.h`](include/tirtc/tiRTC.h)：TiRTC 对外 API 头文件
- [`include/tirtc/basedef.h`](include/tirtc/basedef.h)：TiRTC 基础类型定义
- `lib/libTiRTC.dll`：TiRTC 动态库
- `lib/libTiRTC.lib`：MSVC import library
- `lib/libTiRTC.dll.a`：MinGW import library
- [`lib/libTiRTC.def`](lib/libTiRTC.def)：导出符号定义
- `lib/webrtc.dll`：运行时依赖库

## 如何使用

业务代码只包含 `include/tirtc/tiRTC.h`，并链接与工具链对应的导入库。

不要把 `webrtc.dll` 当作 TiRTC SDK 的入口库使用。它是运行时依赖库，会由 SDK 内部按需加载。

## 运行时库摆放位置

`libTiRTC.dll` 和 `webrtc.dll` 必须放在 Windows 动态库加载器可以找到的位置。最简单的做法是把它们放在主程序可执行文件旁边。

示例：

```text
my_app.exe
libTiRTC.dll
webrtc.dll
```

如果不放在可执行文件旁边，需要确保所在目录已经加入 `PATH`，否则运行时会加载失败。
