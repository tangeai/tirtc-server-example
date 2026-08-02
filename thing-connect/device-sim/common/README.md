# device-sim common

`common/device_core` 是当前 **ESP32-S3 独立移植工程**使用的纯 C 组件。它不依赖 ESP-IDF、FreeRTOS、pthread、文件系统实现或 TiRTC SDK 类型，包含：

- 设备媒体配置和业务方向规则；
- H5、AI、VoIP、设备互呼的公共状态和业务名称；
- G711A、AMR-NB、AMR-WB、Opus、MJPEG、H264 Annex-B 文件读取器；
- 主机单元测试。

Linux `device-sim-c` 当前有自己的实现，**没有编译或链接 `device_core`**。两者共享协议目标和部分设计思路，不是同一份程序，也不能用 ESP32-S3 的构建、运行或能力结论描述 Linux C 参考实现。

构建本组件的主机测试：

```bash
cmake -S device_core -B /tmp/device-core-build
cmake --build /tmp/device-core-build
ctest --test-dir /tmp/device-core-build --output-on-failure
```

当前依赖关系：

```text
device-sim-esp32 ──> common/device_core
device-sim-c     ──> 自有 Linux/POSIX 实现（不依赖 device_core）
```

`device_core` 只处理配置、状态和文件拆帧。ESP32-S3 工程仍需在自身组件中实现任务、网络、存储、日志和媒体 source/sink。未来其他平台若要复用它，应建立独立移植工程并单独验证，不能据此宣称 Linux C 参考实现已经跨平台。
