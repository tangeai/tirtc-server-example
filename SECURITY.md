# Security Policy

## Supported versions

安全修复面向最新公开版本和主分支。部署者应及时升级，并在升级前备份数据库和配置。

## Reporting a vulnerability

请使用 GitHub 仓库的 Private vulnerability reporting / Security Advisory 私下报告安全问题。不要在公开 Issue 中披露可利用细节、真实密钥、用户数据或设备凭证。

报告中请包含受影响版本、组件、复现条件、潜在影响和建议缓解方式。维护者确认问题前，不要对不属于你的线上系统进行测试。

## Deployment baseline

- 所有公网流量使用 HTTPS，MQTT 使用 TLS。
- MySQL、Redis、MQTT 和 `/v1/internal/*` 不直接暴露到公网。
- 五个业务服务共享业务 JWT 密钥，Admin 使用独立 JWT 密钥；所有公开占位值必须替换。
- `trusted_proxies` 只填写实际反向代理地址。
- 数据库配置密钥使用 32 字节随机加密密钥保护，配置文件权限设为 `0600`。
- 应用数据库账号只授予运行需要的 DML 权限，迁移使用独立账号。
- 定期检查登录日志、操作日志、服务状态和依赖告警。
