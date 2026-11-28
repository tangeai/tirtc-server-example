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
  let deviceReads = 0, contactReads = 0;
  let deletedRoles = 0, savedRoles = 0;
  let submitted;
  let registration;
  let recognitionEnabled = false;
  const schemes = ['婴儿哭声检测', '吵架异常声音检测'].map((name, index) => ({
    name, scheme_id: String(index + 1), scheme_version: 1, output_mode: 'text_audio'
  }));
  let enabledSchemes = [...schemes];
  let roleConfig = {};
  const savedIntervals = [];
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
        if (pathname === '/v1/call/user/contacts') { contactReads++; data = { contacts: [] }; }
        if (pathname === '/v1/config/captcha') data = { enabled: false, provider: 'yidun' };
        if (pathname === '/v1/user/login') { submitted = route.request().postDataJSON(); data = { token: 'fixture' }; }
        if (pathname === '/v1/user/register') { registration = route.request().postDataJSON(); data = { token: 'fixture' }; }
        if (pathname === '/v1/user/quota') data = { quota: 1 };
        if (pathname === '/v1/user/me') data = { email: 'fixture@example.test' };
        if (pathname === '/v1/recognition/types') data = schemes;
        if (pathname.endsWith('/alarm')) {
          if (route.request().method() === 'PUT') savedIntervals.push(route.request().postDataJSON().interval_seconds);
          data = { interval_seconds: savedIntervals.at(-1) ?? null };
        }
        if (pathname.includes('/conversations')) data = { items: [], page: 1, has_more: false };
        if (pathname === '/v1/ai/roles') data = { items: [{ id: 'test-role', name: '测试角色' }, { id: 'spare', name: '备用角色' }] };
        if (pathname === '/v1/ai/roles/spare' && route.request().method() === 'DELETE') deletedRoles++;
        if (pathname === '/v1/ai/roles/test-role') {
          if (route.request().method() === 'PUT') {
            savedRoles++;
            roleConfig = route.request().postDataJSON();
          }
          data = { ...roleConfig, id: 'test-role', name: '测试角色' };
        }
        if (pathname.endsWith('/config')) {
          if (route.request().method() === 'PUT') {
            const payload = route.request().postDataJSON();
            recognitionEnabled = payload.enabled;
            enabledSchemes = payload.schemes.map(ref => schemes.find(item => item.scheme_id === ref.scheme_id));
          }
          data = { policy: { enabled: recognitionEnabled, revision: 1, monthly_limit: 100 }, usage: { used: 8 }, schemes: recognitionEnabled ? enabledSchemes : [] };
        }
        if (pathname.endsWith('/mcp/tools')) data = { items: [{ id: 'map', name: '地图' }, { id: 'music', name: '音乐' }, { id: 'news', name: '新闻' }] };
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
    assert.equal(await page.locator('.device-count').innerText(), '（1 台）');
    assert.equal(await page.evaluate(() => localStorage.getItem('token')), 'fixture');
    const states = page.locator('.device-list-grid>.card').first();
    await states.locator('.action').filter({hasText:'AI 识别'}).locator('.device-status-dot.off').waitFor();
    await states.locator('.action').filter({hasText:'联系人'}).locator('.device-status-dot.on').waitFor();
    assert.equal(contactReads, 0);
    recognitionEnabled = true;
    await page.locator('.devices-page .action').filter({ hasText: /^刷新$/ }).click();
    await states.locator('.action').filter({hasText:'AI 识别'}).locator('.device-status-dot.on').waitFor();
    await states.locator('.device-primary-actions .action').filter({hasText:'微信通话'}).click();
    const guide = page.getByRole('dialog', {name:'在微信中通话'});
    await guide.waitFor();
    await guide.locator('img').evaluate(image => image.decode());
    await page.keyboard.press('Escape');
    await guide.waitFor({state:'hidden'});
    const readsBeforeLeaving = deviceReads;
    for (const [route, label, menu] of [
      ['/bind', '添加设备'], ['/agent', 'AI 角色'], ['/recognition', 'AI 识别'],
      ['/contacts', '联系人'], ['/room', '多人对讲', true], ['/history', 'AI 对话记录', true],
    ]) {
      if (menu) await states.locator('.device-head .action').click();
      await page.evaluate(() => {
        const previous = new Set(document.querySelectorAll('.taro_page'));
        window.nextPageFrame = new Promise(resolve => {
          const observer = new MutationObserver(() => {
            const container = [...document.querySelectorAll('.taro_page')].find(el => !previous.has(el) && el.querySelector('.page'));
            if (!container) return;
            observer.disconnect();
            requestAnimationFrame(() => resolve(container.getBoundingClientRect().left));
          });
          observer.observe(document.body, {childList:true, subtree:true});
        });
      });
      const link = route === '/bind' ? page.getByRole('button', {name:label, exact:true})
        : page.locator(menu ? '.device-menu-item' : '.device-feature-links .action').filter({hasText:label});
      await link.click();
      await page.waitForURL(url => url.pathname === route);
      await page.waitForFunction(() => window.nextPageFrame.then(x => Math.abs(x) < 1), null, {timeout:3000});
      await page.goBack();
      await page.getByText('客厅', { exact:true }).waitFor();
      assert.equal(deviceReads, readsBeforeLeaving, route + ' back should restore the device list');
    }
    await page.goto(site + '/account');
    await page.goBack();
    await page.getByText('客厅', { exact: true }).waitFor();
    assert.equal(deviceReads, readsBeforeLeaving, 'Back should restore the device list');
    assert.equal(await page.locator('.devices-page .notice').count(), 0, 'Restoring the list should be silent');
    await page.locator('.device-feature-links taro-button-core').filter({ hasText: 'AI 角色' }).click();
    await page.waitForURL(url => url.pathname === '/agent' && url.searchParams.get('device_id') === 'fixture');
    await page.reload();
    const deleteSpare = page.getByRole('button', { name: '删除角色 · 备用角色' });
    await deleteSpare.click();
    await page.getByText('确定删除“备用角色”？', { exact: true }).waitFor();
    await page.locator('.taro-model__cancel').click();
    assert.equal(deletedRoles, 0);
    await deleteSpare.click();
    await page.locator('.taro-model__confirm').click();
    await deleteSpare.waitFor({ state: 'hidden' });
    assert.equal(deletedRoles, 1);
    await page.locator('.settings-link').filter({ hasText: '测试角色' }).click();
    const saveRole = page.getByRole('button', { name: '保存角色' });
    await saveRole.waitFor();
    assert.equal(await saveRole.isDisabled(), true);
    const saveBounds = await saveRole.boundingBox();
    await page.mouse.click(saveBounds.x + saveBounds.width / 2, saveBounds.y + saveBounds.height / 2);
    await saveRole.evaluate(button => button.click());
    await saveRole.press('Enter');
    await saveRole.press('Space');
    assert.equal(savedRoles, 0);
    assert.equal(await page.locator('.role-toolbar-buttons button,.role-toolbar-buttons taro-button-core').count(), 1);
    await page.locator('.tabs taro-button-core').filter({ hasText: '扩展能力' }).click();
    await page.getByText('设备插件', { exact: true }).waitFor();
    const tools = page.locator('.resource-disclosure').filter({ hasText: 'MCP 工具' });
    assert.equal(await page.locator('.role-tool-link').count(), 0);
    await tools.click();
    const rows = page.locator('.role-tool-list .role-resource-item');
    const toolNames = ['地图', '音乐', '新闻'];
    for (const [index, checked] of [[2, true], [1, true], [2, false]]) {
      await rows.filter({ hasText: toolNames[index] }).getByRole('checkbox').click();
      assert.deepEqual(await rows.locator('.role-tool-name').allTextContents(), toolNames);
      assert.equal(await rows.nth(index).getByRole('checkbox').isChecked(), checked);
    }
    const savedTools = page.waitForRequest(req => req.method() === 'PUT' && req.url().endsWith('/v1/ai/roles/test-role'));
    await saveRole.click();
    assert.deepEqual((await savedTools).postDataJSON().agent_config.mcp_tools, ['music']);
    await page.reload();
    await page.locator('.settings-link').filter({ hasText: '测试角色' }).click();
    await page.locator('.tabs taro-button-core').filter({ hasText: '扩展能力' }).click();
    await tools.click();
    assert.deepEqual(await rows.locator('input').evaluateAll(items => items.map(item => item.checked)), [false, true, false]);
    await page.locator('.role-tool-link').filter({ hasText: '地图' }).click();
    await page.getByText('地图工具详情', { exact: true }).waitFor();
    await page.locator('.sheet taro-button-core').filter({ hasText: '关闭' }).click();
    await page.getByText('地图工具详情', { exact: true }).waitFor({ state: 'hidden' });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await page.locator('.resource-disclosure').filter({ hasText: '设备插件' }).click();
    await page.getByRole('button', {name:'新建设备插件'}).click();
    for (const add of await page.locator('.sheet').getByText('添加参数', {exact:true}).all()) await add.click();
    const params = page.locator('.sheet .role-param-card');
    assert.equal(await params.count(), 2);
    for (const card of await params.all()) {
      const controls = card.locator('.input,.textarea');
      assert.equal(await controls.count(), 5);
      for (const field of await controls.all()) {
        assert.ok(await field.evaluate(el => {
          const css = getComputedStyle(el);
          return css.borderStyle === 'solid' && parseFloat(css.borderWidth) >= 1 &&
            css.backgroundColor !== getComputedStyle(el.closest('.role-param-card')).backgroundColor;
        }), 'Empty parameter controls need a visible boundary');
      }
      const input = card.locator('input').first();
      await input.fill('sample');
      assert.equal(await input.inputValue(), 'sample');
    }
    await page.locator('.sheet').getByText('关闭', {exact:true}).click();
    await page.locator('.taro-model__confirm').click();
    const brand = await page.locator('.console-brand').boundingBox();
    const menu = await page.locator('.console-nav summary').boundingBox();
    assert.ok(brand && menu && brand.x + brand.width <= menu.x && menu.x + menu.width <= width);
    await page.locator('.console-nav summary').focus();
    await page.keyboard.press('Enter');
    await page.getByRole('link', { name: /帮助/ }).waitFor();
    for (const route of ['/contacts', '/recognition', '/room', '/history', '/bind', '/account', '/legal?kind=terms']) {
      await page.goto(site + route + (route.includes('?') ? '&' : '?') + 'device_id=fixture');
      await page.locator('.page').waitFor();
      if (route === '/bind') {
        const cells = page.locator('.code-cells');
        const group = await cells.boundingBox(), card = await page.locator('.bind-page .card').boundingBox();
        assert.ok(group && card && Math.abs(group.x + group.width / 2 - card.x - card.width / 2) <= 1);
        await page.locator('.code-input').click();
        await cells.locator('.code-cell').first().locator('.code-cell-caret').waitFor();
        const bind = page.getByRole('button', { name:'绑定设备', exact:true });
        assert.equal(await bind.isDisabled(), true);
        await page.keyboard.type('123456');
        assert.equal(await bind.isDisabled(), false);
        assert.equal((await cells.locator('.code-cell').allTextContents()).join(''), '123456');
        await page.keyboard.press('Backspace');
        assert.equal((await cells.locator('.code-cell').allTextContents()).join(''), '12345');
        await cells.locator('.code-cell').last().locator('.code-cell-caret').waitFor();
        await page.getByText(/如何获取绑定码/).click();
        await page.getByText('给已安装小钛固件的设备通电。').waitFor();
      }
      if (route === '/contacts') {
        const bar = page.locator('.web-search');
        const input = bar.getByRole('textbox');
        const bounds = await bar.boundingBox();
        assert.ok(bounds);
        const x = bounds.x + bounds.width * 0.75, y = bounds.y + bounds.height / 2;
        // The native input must fill the field even after Taro transforms the CSS.
        assert.ok(await input.evaluate((el, point) => el === document.elementFromPoint(...point), [x, y]));
        await page.mouse.click(x, y);
        assert.equal(await input.evaluate(el => getComputedStyle(el).outlineStyle), 'none');
        assert.notEqual(await bar.evaluate(el => getComputedStyle(el).boxShadow), 'none');
        await page.keyboard.type('abc');
        await page.mouse.click(x, y);
        await page.keyboard.type('d');
        assert.equal(await input.inputValue(), 'abcd');
        await bar.getByRole('button').click();
        assert.equal(await input.inputValue(), '');
      }
      if (route === '/recognition') {
        await page.getByText('请在微信小程序中授权接收此设备的异常提醒。').waitFor();
        const header = page.locator('.scheme-header');
        const sort = header.getByRole('button', { name: '排序' });
        await sort.waitFor();
        for (const label of ['.reminder-heading', '.scheme-count']) {
          const lines = await header.locator(label).evaluate(el => {
            const range = document.createRange();
            range.selectNodeContents(el);
            return new Set(Array.from(range.getClientRects(), rect => Math.round(rect.top))).size;
          });
          assert.equal(lines, 1, width + ' wrapped ' + label);
        }
        assert.ok((await sort.boundingBox()).width < (await header.boundingBox()).width / 3);
        await sort.click();
        await header.getByRole('button', { name: '完成' }).click();
        const switches = page.locator('.scheme-row').getByRole('checkbox');
        for (const enable of [false, true]) {
          const response = page.waitForResponse(r => r.request().method() === 'PUT' && r.url().endsWith('/config'));
          await switches.first().click();
          await response;
          await sort.waitFor({ state: enable ? 'visible' : 'hidden' });
          assert.deepEqual(await switches.evaluateAll(items => items.map(item => item.checked)), [enable, true]);
          assert.match(await page.locator('.scheme-row').first().innerText(), /婴儿哭声检测/);
        }
        assert.deepEqual(enabledSchemes.map(item => item.scheme_id), ['2', '1']);
        await sort.click();
        assert.match(await page.locator('.scheme-row').first().innerText(), /吵架异常声音检测/);
        await header.getByRole('button', { name: '完成' }).click();
        const input = page.locator('.reminder-seconds-input input');
        assert.equal(await input.inputValue(), '60');
        for (const value of ['59', '86401']) {
          await input.fill(value);
          await input.blur();
          await page.getByText('请输入 60 至 86400 的整数秒').waitFor();
        }
        assert.deepEqual(savedIntervals, []);
        for (const label of ['5 分钟', '10 分钟', '1 分钟']) {
          await page.getByText(label, { exact: true }).click();
          await page.getByText('已保存', {exact:true}).waitFor();
        }
        await input.fill('86400');
        await input.blur();
        await page.getByText('已保存', {exact:true}).waitFor();
        assert.deepEqual(savedIntervals, [300, 600, 60, 86400]);
      }
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, width + ' ' + route);
    }
    await page.goto(site + '/forgot-password');
    await page.getByText('返回设备列表', { exact: true }).click();
    await page.waitForURL('**/devices');
    await page.evaluate(() => localStorage.removeItem('token'));
    await page.goto(site + '/forgot-password');
    await page.getByText('返回登录', { exact: true }).click();
    await page.waitForURL('**/login');
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
    const emailInput = page.locator('input[name="email"]');
    await emailInput.focus();
    assert.equal(await emailInput.evaluate(el => getComputedStyle(el).outlineStyle), 'none');
    const field = page.locator('.auth-field .auth-input');
    const emailBox = await field.first().boundingBox();
    const passwordBox = await field.nth(1).boundingBox();
    const eye = await page.locator('.auth-visibility').boundingBox();
    assert.ok(emailBox && passwordBox && eye && Math.abs(emailBox.width - passwordBox.width) < 1);
    assert.ok(eye.x >= passwordBox.x && eye.x + eye.width <= passwordBox.x + passwordBox.width);
    await emailInput.fill('fixture@example.test');
    await page.locator('input[name="password"]').fill('browser-fixture');
    await page.locator('.console-nav summary').click();
    const languageButton = page.getByRole('button', { name: 'English' });
    const languageBox = await languageButton.boundingBox();
    const helpBox = await page.locator('.console-nav details a').first().boundingBox();
    assert.ok(languageBox && helpBox && languageBox.height === helpBox.height && languageBox.x === helpBox.x);
    await languageButton.click();
    assert.equal(await page.locator('input[name="email"]').inputValue(), 'fixture@example.test');
    assert.equal(await page.locator('.console-nav details').getAttribute('open'), null);
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
