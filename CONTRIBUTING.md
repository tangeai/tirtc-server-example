# Contributing

感谢参与 ThingConnect。提交代码前请先确认改动属于设备接入参考实现、服务端、H5、小程序、Admin 或文档中的哪一层，并保持公开接口向后兼容。

## 分支与 Pull Request

日常开发从最新 `main` 创建短期分支，并向 `main` 提交 Pull Request，不以 `develop` 作为开发或集成入口。一个 PR 聚焦一个功能、修复或完整业务切片。

- 功能使用 `feat/<主题>`，修复使用 `fix/<主题>`，文档使用 `docs/<主题>`，维护工作使用 `chore/<主题>`。
- 外部贡献者先 Fork 仓库，以上游 `main` 为基线，在自己的 Fork 中推送分支，再向本仓库 `main` 提交 PR。
- 有仓库写权限的贡献者可按下列方式开始：

```bash
git fetch origin
git switch -c feat/your-feature origin/main
# 修改代码并完成对应范围的验证
git add <本次修改的文件>
git commit -m "feat: describe the change"
git push -u origin feat/your-feature
```

提交 PR 前完成本地验证，在 PR 中写明测试结果、跳过项和未覆盖的外部环境。未完成的工作使用 Draft PR。合并前必须通过 GitHub Actions 必需检查并解决审查意见；`main` 有新提交时更新分支并重新检查。

维护者审查后使用 Squash merge 合并。合并后的远端功能分支自动删除，本地分支由贡献者自行清理。不要直接推送到 `main` 或 `release/*`，也不要通过绕过规则替代测试。

`release/<主版本.次版本>` 用于发布稳定和版本维护；旧版本修复先进入适用的主干，再由维护者按需回移并提交到对应发布分支的 PR。正式发布以 `v<版本号>` Tag 和 GitHub Release 标识，已发布 Tag 不移动或删除。

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
npm --prefix admin/admin-web run check
```

MySQL 和 Redis 集成测试通过 `THING_CONNECT_TEST_CONFIG` 指定配置。可复制 `thing-connect/tests/testdata/config.yaml`，连接真实 MySQL 8 和 Redis，使用名称以 `_test` 结尾的独立数据库，不要连接生产数据库。从 `thing-connect/` 执行：

```bash
THING_CONNECT_TEST_CONFIG=tests/testdata/config.yaml go test ./... -p 1 -count=1
```

按修改范围补充验证：

| 范围 | 命令（默认从 `thing-connect/` 执行） |
|---|---|
| Go 文件 | 对修改文件运行 `gofmt`，运行相关测试和 `go vet ./...` |
| 组合根、构建或跨服务改动 | `./build.sh <服务>`；跨服务改动执行完整 `./build.sh` |
| 用户 Web | `npm --prefix user-server/user-web run check`；交互变化运行 `npm --prefix user-server/user-web run test:browser` |
| 微信小程序 | 从仓库根目录执行 `npm --prefix thing-connect-client run check` |
| Python 模拟器 | 从 `device-sim/device-sim-py/` 执行 `python -m unittest discover -p 'test_*.py'` |
| Linux C 参考实现 | 从 `device-sim/device-sim-c/` 执行 `make WERROR=1 test`；适配接口变化再执行 `make WERROR=1 framework adapter-template-check` |

本地依赖不可用可能导致集成测试跳过，必须单独报告，不能将跳过视为完整通过。SDK、真实设备、微信开发者工具和外部服务验收应按影响面补充。CI 的具体命令以 [ci.yaml](.github/workflows/ci.yaml) 为准。

## 提交约束

- 不提交真实密钥、用户数据、设备凭证、证书或生产配置。
- 公共 API、配置项、数据库契约和接入流程变化必须同步对应文档与测试。
- 内部重构不在 README 或 API Reference 中写变更记录；发布差异写入 release notes。
- 数据库变化通过有序迁移实现，并同步全新安装用的 `scripts/schema.sql`。
- 新的动态配置项使用现有通用配置结构，明确命名空间、范围、默认值、密钥字段和运行时生效方式。
- 变更用户认证时验证历史令牌兼容策略，避免无必要的全体用户重新登录。

Pull Request 应说明目的、兼容性影响、验证命令和需要运营人员执行的部署步骤。界面变化附截图，协议变化附请求与响应示例。
