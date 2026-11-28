const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const root = path.resolve(__dirname, '../../../..');
const build = path.join(root, 'thing-connect-client/dist/h5');
const site = 'http://console.test';
const device = { device_id: 'fixture', device_name: '客厅', status: 1, online: true };
async function verify(browser, width) {
  const page = await browser.newPage({ viewport: { width, height: 860 }, locale: 'zh-CN' });
  let unavailable = true;
  let deviceReads = 0;
  let submitted;
  let registration;
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  try {
    await page.addInitScript(() => {
      if (!sessionStorage.getItem('initialized')) {
        localStorage.setItem('token', '{"data":"fixture"}');
        sessionStorage.setItem('initialized', '1');
      }
    });
    await page.route(site + '/**', async route => {
      const pathname = new URL(route.request().url()).pathname;
      if (pathname.startsWith('/static/console/')) return route.fulfill({ path: path.join(build, pathname.slice('/static/console/'.length)) });
      if (pathname === '/v1/config/language.js') return route.fulfill({ contentType: 'text/javascript', body:
        'window.WebLanguageConfig={default:"zh-CN",allow_switch:true};' + fs.readFileSync(path.join(root, 'thing-connect/internal/webui/runtime.js'), 'utf8') });
      if (pathname.startsWith('/static/')) return route.fulfill({ path: path.join(root, 'thing-connect/user-server', pathname) });
      if (pathname.startsWith('/v1/')) {
        if (pathname === '/v1/user/device/list') {
          deviceReads++;
          assert.equal(route.request().headers().authorization, 'Bearer fixture');
          if (unavailable) return route.fulfill({ status: 503, json: { code: 503, msg: '设备在线状态暂不可用，请稍后刷新' } });
          return route.fulfill({ json: { code: 200, data: [device] } });
        }
        let data = { items: [] };
        if (pathname === '/v1/config/captcha') data = { enabled: false, provider: 'yidun' };
        if (pathname === '/v1/user/login') { submitted = route.request().postDataJSON(); data = { token: 'fixture' }; }
        if (pathname === '/v1/user/register') { registration = route.request().postDataJSON(); data = { token: 'fixture' }; }
        if (pathname === '/v1/user/me') data = { email: 'fixture@example.test' };
        if (pathname === '/v1/recognition/types') data = [];
        if (pathname.endsWith('/alarm')) data = { interval_seconds: 60 };
        if (pathname.includes('/conversations')) data = { items: [], page: 1, has_more: false };
        if (pathname === '/v1/ai/roles') data = { items: [{ id: 'test-role', name: '测试角色' }] };
        if (pathname === '/v1/ai/roles/test-role') data = { id: 'test-role', name: '测试角色' };
        if (pathname.endsWith('/config')) data = { policy: { enabled: false, monthly_limit: 100 }, usage: { used: 8 }, schemes: [] };
        if (pathname.endsWith('/mcp/tools')) data = { items: [{ id: 'map', name: '地图' }] };
        if (pathname.endsWith('/mcp/tools/map')) data = { id: 'map', name: '地图', description: '地图工具详情' };
        if (pathname === '/v1/config/navigation') data = { links: [{ enabled: true, name: '帮助', url: 'https://example.test/help' }] };
        return route.fulfill({ json: { code: 200, data } });
      }
      return route.fulfill({ path: path.join(build, 'index.html') });
    });
    await page.goto(site + '/devices');
    await page.getByText('设备在线状态暂不可用，请稍后刷新').waitFor();
    assert.equal(await page.locator('.device-count').count(), 0);
    assert.equal(await page.getByText('添加第一台设备，开始使用小钛').count(), 0);
    unavailable = false;
    await page.locator('taro-button-core').filter({ hasText: '重新加载设备' }).click();
    await page.getByText('客厅', { exact: true }).waitFor();
    assert.equal(await page.locator('.device-count').innerText(), '1');
    assert.equal(await page.evaluate(() => localStorage.getItem('token')), 'fixture');
    const readsBeforeLeaving = deviceReads;
    await page.goto(site + '/account');
    await page.goBack();
    await page.getByText('客厅', { exact: true }).waitFor();
    assert.equal(deviceReads, readsBeforeLeaving, 'Back should restore the device list');
    await page.locator('.device-feature-links taro-button-core').filter({ hasText: 'AI 角色' }).click();
    await page.waitForURL(url => url.pathname === '/agent' && url.searchParams.get('device_id') === 'fixture');
    await page.reload();
    await page.locator('.settings-link').filter({ hasText: '测试角色' }).click();
    await page.locator('.tabs taro-button-core').filter({ hasText: '扩展能力' }).click();
    await page.getByText('设备插件', { exact: true }).waitFor();
    await page.locator('.role-tool-link').filter({ hasText: '地图' }).click();
    await page.getByText('地图工具详情', { exact: true }).waitFor();
    await page.locator('.sheet taro-button-core').filter({ hasText: '关闭' }).click();
    await page.getByText('地图工具详情', { exact: true }).waitFor({ state: 'hidden' });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    const brand = await page.locator('.console-brand').boundingBox();
    const menu = await page.locator('.console-nav summary').boundingBox();
    assert.ok(brand && menu && brand.x + brand.width <= menu.x && menu.x + menu.width <= width);
    await page.locator('.console-nav summary').focus();
    await page.keyboard.press('Enter');
    await page.getByRole('link', { name: /帮助/ }).waitFor();
    for (const route of ['/contacts', '/recognition', '/room', '/history', '/bind', '/account', '/legal?kind=terms']) {
      await page.goto(site + route + (route.includes('?') ? '&' : '?') + 'device_id=fixture');
      await page.locator('.page').waitFor();
      if (route === '/recognition') await page.getByText('请在微信小程序中授权接收此设备的异常提醒。').waitFor();
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, width + ' ' + route);
    }
    await page.evaluate(() => localStorage.removeItem('token'));
    await page.goto(site + '/register');
    const code = page.locator('input[name="code"]');
    await code.fill('012345');
    assert.equal(await code.inputValue(), '012345');
    const codeBox = await code.boundingBox();
    const sendBox = await page.locator('.auth-field .auth-inline-button').first().boundingBox();
    assert.ok(codeBox && sendBox && codeBox.width >= 80 && codeBox.x + codeBox.width <= sendBox.x + 1);
    await page.locator('input[name="email"]').fill('registration@example.test');
    await page.locator('input[name="password"]').fill('browser-fixture');
    await page.locator('input[name="confirm"]').fill('browser-fixture');
    await page.locator('.auth-checkbox').click();
    await page.locator('.auth-submit').filter({ hasText: '注册并登录' }).click();
    await page.waitForURL('**/devices');
    assert.equal(registration.code, '012345');
    await page.evaluate(() => localStorage.removeItem('token'));
    await page.goto(site + '/agent?device_id=fixture');
    await page.waitForURL('**/login');
    await page.locator('input[name="email"]').fill('fixture@example.test');
    await page.locator('input[name="password"]').fill('browser-fixture');
    await page.locator('.console-nav summary').click();
    await page.getByRole('button', { name: 'English' }).click();
    assert.equal(await page.locator('input[name="email"]').inputValue(), 'fixture@example.test');
    await page.locator('.console-nav summary').click();
    await page.locator('.auth-checkbox').click();
    const login = page.locator('.auth-submit');
    if (width < 768) await login.focus();
    else await page.locator('input[name="password"]').focus();
    await page.keyboard.press('Enter');
    await page.waitForURL(url => url.pathname === '/agent' && url.searchParams.get('device_id') === 'fixture');
    assert.equal(submitted.email, 'fixture@example.test');
    assert.deepEqual(errors, []);
  } finally { await page.close(); }
}
(async () => {
  const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM || undefined, args: ['--no-sandbox'] });
  try {
    for (const width of [320, 390, 768, 1440, 1920]) await verify(browser, width);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
