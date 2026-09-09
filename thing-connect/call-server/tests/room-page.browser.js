// Run with playwright-cli run-code against the local development gateway.
async (page) => {
  const base = 'http://dev-demo-open.tangeai.cn:8080';
  await page.unrouteAll();
  await page.addInitScript(() => localStorage.setItem('token', 'browser-test-token'));
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const device = {device_id: 'room-browser-device', device_name: '客厅设备名称十三字测试设备', online: false};
  await page.route('**/v1/user/me', route => route.fulfill({json:{code:200,data:{user_id:1,email:'browser@example.com'}}}));
  await page.route('**/v1/ai/device/**/role', r => r.fulfill({json:{code:200,data:{role_id:''}}}));
  let state = {desired_state: 'left', state: 'left', online_count: 0, online: false};
  let mode = 'ok';
  const requests = [];
  await page.route('**/v1/user/device/list', r => r.fulfill({json: {code: 200, data: [device]}}));
  await page.route('**/v1/call/room/web/device/**', async route => {
    const request = route.request();
    if (request.method() === 'POST') {
      requests.push({url: request.url(), key: request.headers()['idempotency-key'], body: request.postDataJSON()});
      if (mode === 'network') return route.abort('failed');
      if (mode === 'password') return route.fulfill({json: {code: 40320, msg: '房间密码错误，请重新输入'}});
      await page.waitForTimeout(180);
      state = request.url().endsWith('/leave') ? {desired_state: 'left', state: 'left'} : {desired_state: 'joined', state: 'waiting_device', room_code: '001234', room_id: 'test-room', assignment_version: 1, online_count: 0, password_set: !!request.postDataJSON().password, online: false};
    } else if (mode === 'load') return route.abort('failed');
    return route.fulfill({json: {code: 200, data: state}});
  });
  const check = (value, message) => {if (!value) throw Error(message);};
  const open = () => page.goto(base + '/v1/call/room/page?device_id=' + device.device_id);
  mode = 'load';
  await open();
  await page.locator('#retry').waitFor({state: 'visible'});
  check(await page.locator('#message').isVisible(), 'initial load error invisible');
  mode = 'ok';
  await page.locator('#retry').click();
  await page.locator('#actions').waitFor({state: 'visible'});
  for (const width of [360, 768, 1440]) {
    await page.setViewportSize({width, height: 900});
    check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'horizontal overflow ' + width);
    await page.screenshot({path: 'output/playwright/room-empty-' + width + '.png', fullPage: true});
  }
  await page.locator('[name=passwordMode][value=password]').check();
  await page.locator('#newPassword').fill('12');
  await page.locator('#createSubmit').click();
  check(requests.length === 0, 'invalid password submitted');
  await page.locator('#newPassword').fill('0573');
  mode = 'network';
  await page.locator('#createSubmit').click();
  await page.locator('#message').waitFor({state: 'visible'});
  check(await page.locator('#newPassword').inputValue() === '0573', 'network error lost password');
  mode = 'ok';
  await page.locator('#createSubmit').click();
  check(await page.locator('#createSubmit').isDisabled(), 'submit not disabled');
  await page.locator('#current').waitFor({state: 'visible'});
  check(requests[0].key === requests[1].key, 'retry changed idempotency key');
  check(await page.locator('#successMessage').isVisible(), 'creation success invisible');
  check(await page.locator('#code').textContent() === '001234', 'room code loses zero');
  check((await page.locator('#stateHelp').textContent()).includes('请开机联网'), 'offline explanation missing');
  await page.locator('#leave').click();
  await page.locator('#cancelLeave').click();
  check(requests.length === 2, 'cancel sent leave');
  await page.locator('#leave').click();
  await page.locator('#confirmLeave').click();
  await page.locator('#actions').waitFor({state: 'visible'});
  check((await page.locator('#successMessage').textContent()).includes('已退出'), 'leave feedback missing');
  mode = 'password';
  await page.locator('#roomCode').fill('001234');
  await page.locator('#joinPassword').fill('1234');
  await page.locator('#joinSubmit').click();
  await page.locator('#message').waitFor({state: 'visible'});
  check((await page.locator('#message').textContent()).includes('密码错误'), 'wrong password feedback missing');
  mode = 'ok';
  await page.locator('#joinPassword').fill('0573');
  await page.locator('#joinSubmit').click();
  await page.locator('#current').waitFor({state: 'visible'});
  for (const width of [360, 1440]) {
    await page.setViewportSize({width, height: 900});
    check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'joined overflow');
    await page.screenshot({path: 'output/playwright/room-joined-' + width + '.png', fullPage: true});
  }
  const count = requests.length;
  await page.locator('#home').click();
  await page.waitForURL(base + '/devices');
  check(requests.length === count && state.desired_state === 'joined', 'home left room');
  check(errors.length === 0, errors.join('\n'));
  console.log('PASS: responsive layouts, load retry, validation, network retry, busy state, create, leave/cancel, wrong password, join and home without leaving');
}
