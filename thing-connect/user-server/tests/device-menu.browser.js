// Run with playwright-cli run-code against the local development gateway.
async (page) => {
  const base = 'http://dev-demo-open.tangeai.cn:8080';
  await page.unrouteAll();
  await page.addInitScript(() => localStorage.setItem('token', 'browser-test-token'));
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.route('**/v1/user/me', route => route.fulfill({json:{code:200,data:{user_id:1,email:'browser@example.com'}}}));
  await page.route('**/v1/user/device/list', r => r.fulfill({json: {code:200, data:[{device_id:'test-device-menu',device_name:'客厅设备名称十三字测试设备', online:true}]}}));
  await page.route('**/v1/ai/device/**/role', r => r.fulfill({json:{code:200,data:{role_id:''}}}));
  await page.route('**/v1/call/room/web/device/**', r => r.fulfill({json:{code:200,data:{desired_state:'left'}}}));
  await page.goto(base + '/devices');
  const check = (ok, message) => { if (!ok) throw Error(message); };
  const trigger = page.locator('[aria-haspopup=menu]');
  for (const width of [360,1440]) {
    await page.setViewportSize({width,height:900});
    await trigger.click();
    await page.locator('#device-menu').waitFor({state:'visible'});
    check(await trigger.getAttribute('aria-expanded') === 'true', 'expanded state missing');
    const box = await page.locator('#device-menu').boundingBox();
    check(box.x >= 0 && box.x + box.width <= width, 'menu overflow');
    check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'card overflow');
    await page.screenshot({path:'output/playwright/device-menu-' + width + '.png', fullPage:true});
    await page.keyboard.press('ArrowDown');
    check(await page.locator('[data-menu-action=info]').evaluate(e=>e===document.activeElement), 'keyboard navigation failed');
    await page.keyboard.press('Escape');
    check(await page.locator('#device-menu').isHidden(), 'Escape did not dismiss');
    check(await trigger.evaluate(e=>e===document.activeElement), 'focus not restored');
    await trigger.click();
    await page.locator('[data-menu-action=info]').click();
    await page.locator('#device-info').waitFor({state:'visible'});
    await page.locator('#info-close').click();
    await trigger.click();
    await page.locator('[data-menu-action=rename]').click();
    await page.locator('#name-modal').waitFor({state:'visible'});
    check(await page.locator('#device-name-input').inputValue() === '客厅设备名称十三字测试设备', 'wrong device selected');
    await page.keyboard.press('Escape');
    await trigger.click();
    await page.locator('h1').click();
    check(await page.locator('#device-menu').isHidden(), 'outside click failed');
  }
  check(errors.length === 0, errors.join('\n'));
  console.log('PASS: desktop/mobile device menu positioning, keyboard, dismissal, device information and rename');
}
