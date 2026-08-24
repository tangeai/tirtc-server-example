# TiRTC macOS ARM64 SDK 使用说明

这个 SDK 支持静态和动态两种接入方式：

| 接入方式 | 编译期链接 |
|---|---|
| 静态 | `lib/libTiRTC.a` |
| 动态 | `lib/libTiRTC.dylib` |

两种方式都要随程序分发 `lib/libtgrtc.dylib`。

## 包内容

- `include/tirtc/tiRTC.h`：TiRTC 对外 API 头文件
- `include/tirtc/basedef.h`：TiRTC 基础类型定义
- `lib/libTiRTC.a`：TiRTC 静态库
- `lib/libTiRTC.dylib`：TiRTC 动态库
- `lib/libtgrtc.dylib`：运行时依赖库

## 如何使用

业务代码只包含 `include/tirtc/tiRTC.h`，并根据接入方式链接 `libTiRTC.a` 或 `libTiRTC.dylib`。

不要把 `libtgrtc.dylib` 当作 TiRTC SDK 的入口库使用。它是运行时依赖库，会由 SDK 内部按需加载。

## 运行时库摆放位置

静态接入时，把 `libtgrtc.dylib` 放在主程序可执行文件旁边，确保运行时可以通过 `@loader_path/libtgrtc.dylib` 找到它。

示例：

```text
MyApp.app/Contents/MacOS/MyApp
MyApp.app/Contents/MacOS/libtgrtc.dylib
```

动态接入时，建议把 `libTiRTC.dylib` 和 `libtgrtc.dylib` 放在同一个运行时库目录，并确保应用能够找到 `libTiRTC.dylib`。

示例：

```text
MyApp.app/Contents/Frameworks/libTiRTC.dylib
MyApp.app/Contents/Frameworks/libtgrtc.dylib
```

应用通常还要配置运行时搜索路径，例如 `@executable_path/../Frameworks`。

如果运行时报找不到 `libtgrtc.dylib`，请优先检查它是否被打进应用包，以及是否放在运行时可解析的位置。
