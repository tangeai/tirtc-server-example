# 用户控制台

React + TypeScript + Vite 用户前端。Go user-server 提供构建后的页面；AI、通话、VoIP 和识别请求由同源网关分发到各业务服务。Admin Web 使用独立入口。

设备列表中的联系人、多人对讲、实时查看和 AI 识别在弹窗中操作；AI 角色进入独立配置页面。多人对讲通过“创建房间／加入房间”切换操作，联系人支持搜索和分类，长列表在弹窗内部滚动。关闭实时查看会释放音视频连接。设备和房间状态通过“刷新”手动更新，回到列表或切回前台不会自动刷新。

在 `thing-connect/` 执行：

```sh
npm --prefix user-server/user-web ci
npm --prefix user-server/user-web run dev
```

打开 `http://localhost:5174/login`。本地业务服务使用默认端口；代理配置见 `vite.config.ts`。生产构建使用 `./build.sh user-server`，输出到 `user-server/static/app`。只部署源 HTML 无法运行控制台。

```sh
npm --prefix user-server/user-web run check
```

浏览器回归使用 Playwright，模拟业务响应，不操作真实用户、邮件或设备。先构建前端，再在本目录运行：

```sh
node tests/auth-contacts.browser.cjs
node tests/devices-roles.browser.cjs
node tests/layout.browser.cjs
node tests/captcha-layout.browser.cjs
```

需要可解析的 `playwright` 包及 Chromium；已有独立安装时可通过 `PLAYWRIGHT_MODULE` 指定模块路径、`PLAYWRIGHT_CHROMIUM` 指定浏览器可执行文件。截图存入仓库的 `output/playwright/`。

媒体单元测试使用 SDK 替身，验证流 ID、权限延迟回调和资源释放。真实音视频、微信小程序宿主、人机验证供应商与邮件投递需要对应环境单独验收。
