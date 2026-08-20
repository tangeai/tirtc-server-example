# Contributing

感谢参与 ThingConnect。提交代码前请先确认改动属于设备接入参考实现、服务端、H5、小程序、Admin 或文档中的哪一层，并保持公开接口向后兼容。

## 开发环境

```bash
cd thing-connect
go mod download
npm ci
npm --prefix admin/admin-web ci
```

Go 版本以 `thing-connect/go.mod` 为准；前端需要 Node.js 20.19+ 或 22.12+。

## 验证

```bash
cd thing-connect
go vet ./...
go test ./... -p 1 -count=1
npm run build:css
npm --prefix admin/admin-web run build
```

MySQL 和 Redis 集成测试通过 `THING_CONNECT_TEST_CONFIG` 指定配置。可复制 `thing-connect/tests/testdata/config.yaml` 并使用独立测试数据库，不要连接生产数据库。

## 提交约束

- 不提交真实密钥、用户数据、设备凭证、证书或生产配置。
- 公共 API、配置项、数据库契约和接入流程变化必须同步对应文档与测试。
- 内部重构不在 README 或 API Reference 中写变更记录；发布差异写入 release notes。
- 数据库变化通过有序迁移实现，并同步全新安装用的 `scripts/schema.sql`。
- 新的动态配置项使用现有通用配置结构，明确命名空间、范围、默认值、密钥字段和运行时生效方式。
- 变更用户认证时验证历史令牌兼容策略，避免无必要的全体用户重新登录。

Pull Request 应说明目的、兼容性影响、验证命令和需要运营人员执行的部署步骤。界面变化附截图，协议变化附请求与响应示例。
