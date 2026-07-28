# device-sim common

`device_core` 是不依赖 ESP-IDF、FreeRTOS、pthread、文件系统实现和 TiRTC SDK 类型的纯 C
公共层，供 ESP32-S3、后续 ESP32-P4 和嵌入式 Linux 逐步共用。

当前公共内容：

- 一台设备唯一的音视频配置与业务方向规则。
- H5、AI、VoIP、设备互呼的公共状态/业务名称。
- G711A、AMR-NB、AMR-WB、Opus 包、MJPEG、H264 Annex-B 文件读取器。
- 主机单元测试。

构建测试：

```bash
cmake -S device_core -B /tmp/device-core-build
cmake --build /tmp/device-core-build
ctest --test-dir /tmp/device-core-build --output-on-failure
```

平台代码只负责把这些公共能力接到对应系统：

```text
公共 device_core
    ├── ESP-IDF：FreeRTOS + SPIFFS + NVS + esp_http_client + esp_mqtt
    └── Linux：pthread + 普通文件 + libcurl + libmosquitto
```

TiRTC 类型必须留在各平台的 `tirtc_adapter` 内。新增芯片时先复用 `device_core`，再实现任务、
网络、存储、日志和硬件媒体 sink/source；不要复制一份业务状态机后各自修改。

现有 `device-sim-c` 可直接用于有完整 Linux 用户态的设备，但必须提供与目标 CPU、ABI 和 libc
匹配的 TiRTC SDK、交叉编译器及 sysroot。它的 Makefile 已支持 `CC`、`SDK_PLATFORM`、
`SDK_VERSION` 和 `SDK_DIR` 覆盖；x86_64 的 `libTiRTC.so` 不能放到 ARM/MIPS 设备运行。
